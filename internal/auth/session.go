// SPDX-License-Identifier: Apache-2.0

// Package auth issues magic links and session cookies.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Kind is who a session belongs to (ADR 0002).
type Kind string

const (
	KindUser        Kind = "user"
	KindParticipant Kind = "participant"
)

// Session lifetimes from the security requirements.
const (
	UserSessionTTL        = 12 * time.Hour
	ParticipantSessionTTL = 30 * 24 * time.Hour
)

// TTL returns how long a session of this kind lasts.
func (k Kind) TTL() time.Duration {
	if k == KindParticipant {
		return ParticipantSessionTTL
	}
	return UserSessionTTL
}

// Session is what the cookie carries. It is encrypted and authenticated, so
// the browser can neither read nor change it.
type Session struct {
	Kind    Kind   `json:"k"`
	UserID  string `json:"u,omitempty"`
	Email   string `json:"e,omitempty"` // participant sessions only
	Version int    `json:"v,omitempty"` // User.SessionVersion at sign-in
	Expires int64  `json:"x"`
}

// ErrInvalidSession covers tampered, expired and undecodable cookies alike.
var ErrInvalidSession = errors.New("invalid session")

// Codec seals sessions with AES-256-GCM.
type Codec struct {
	aead cipher.AEAD
	now  func() time.Time
}

// DeriveKey derives a purpose-specific 32-byte key from the master secret,
// so the cookie key and the secrets-file key are never the same bytes.
func DeriveKey(master []byte, purpose string) ([]byte, error) {
	if len(master) < 32 {
		return nil, fmt.Errorf("secret key must be at least 32 bytes, got %d", len(master))
	}
	return hkdf.Key(sha256.New, master, nil, "eventpass/"+purpose, 32)
}

// NewCodec builds a codec from EVENTPASS_SECRET_KEY.
func NewCodec(master []byte) (*Codec, error) {
	key, err := DeriveKey(master, "session")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Codec{aead: aead, now: time.Now}, nil
}

// cookieAAD binds the ciphertext to its use, so a value sealed for something
// else with the same key can't be replayed as a session.
var cookieAAD = []byte("eventpass-session-v1")

// Encode seals s. Expires is set from the kind when zero.
func (c *Codec) Encode(s Session) (string, error) {
	if s.Expires == 0 {
		s.Expires = c.now().Add(s.Kind.TTL()).Unix()
	}
	plain, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plain, cookieAAD)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decode opens and checks a cookie value.
func (c *Codec) Decode(v string) (Session, error) {
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(raw) < c.aead.NonceSize() {
		return Session{}, ErrInvalidSession
	}
	n := c.aead.NonceSize()
	plain, err := c.aead.Open(nil, raw[:n], raw[n:], cookieAAD)
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	var s Session
	if err := json.Unmarshal(plain, &s); err != nil {
		return Session{}, ErrInvalidSession
	}
	if c.now().Unix() >= s.Expires {
		return Session{}, ErrInvalidSession
	}
	switch s.Kind {
	case KindUser:
		if s.UserID == "" {
			return Session{}, ErrInvalidSession
		}
	case KindParticipant:
		if s.Email == "" {
			return Session{}, ErrInvalidSession
		}
	default:
		return Session{}, ErrInvalidSession
	}
	return s, nil
}
