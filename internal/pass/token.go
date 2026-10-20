// SPDX-License-Identifier: Apache-2.0

// Package pass signs pass tokens and renders passes (QR, PNG card, PDF).
//
// A token is EP1.<keyId>.<eventCode>.<passId>.<signature>. The signature
// (Ed25519, base64url) covers EP1|keyId|eventId|participantId|passId, so a
// scanner holding only public keys can verify it offline once it knows the
// event and participant. The QR holds no personal data. A token never signs
// anyone in to the portal; revocation is a status check on the server.
package pass

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const prefix = "EP1"

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// EventCode is a short, stable code for an event inside tokens. It is not a
// secret; it only narrows which event a scanned token belongs to.
func EventCode(eventID string) string {
	sum := sha256.Sum256([]byte(eventID))
	return b32.EncodeToString(sum[:5])
}

// Token is a parsed pass token.
type Token struct {
	KeyID     string
	EventCode string
	PassID    string
	sig       []byte
	raw       string
}

func (t Token) String() string { return t.raw }

var (
	ErrMalformed  = errors.New("malformed pass token")
	ErrUnknownKey = errors.New("pass token signed with an unknown key")
	ErrBadSig     = errors.New("pass token signature does not match")
)

// Parse splits a token without verifying it. Verify needs the event and
// participant IDs, which the server looks up from EventCode and PassID.
func Parse(s string) (Token, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ".")
	if len(parts) != 5 || parts[0] != prefix || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return Token{}, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Token{}, ErrMalformed
	}
	return Token{KeyID: parts[1], EventCode: parts[2], PassID: parts[3], sig: sig, raw: s}, nil
}

func message(keyID, eventID, participantID, passID string) []byte {
	return []byte(strings.Join([]string{prefix, keyID, eventID, participantID, passID}, "|"))
}

// KeyRing holds the signing key and every public key still accepted.
// Keys live in DATA_DIR/keys as pass-<keyId>.key (seed, base64) with the
// active key ID in the file "active". Rotating adds a key and makes it
// active; old keys keep verifying until their file is removed.
type KeyRing struct {
	mu     sync.RWMutex
	dir    string
	active string
	keys   map[string]ed25519.PrivateKey
}

// keyID derives a short ID from the public key.
func keyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return strings.ToLower(b32.EncodeToString(sum[:4]))[:6]
}

// LoadKeyRing reads dir, creating a first key when there is none.
func LoadKeyRing(dir string) (*KeyRing, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	kr := &KeyRing{dir: dir, keys: map[string]ed25519.PrivateKey{}}
	files, err := filepath.Glob(filepath.Join(dir, "pass-*.key"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // G304: files inside our own key directory
		if err != nil {
			return nil, err
		}
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s: not a pass signing key", f)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		kr.keys[keyID(priv.Public().(ed25519.PublicKey))] = priv
	}
	if b, err := os.ReadFile(filepath.Join(dir, "active")); err == nil { //nolint:gosec // G304: our own key directory
		kr.active = strings.TrimSpace(string(b))
	}
	if _, ok := kr.keys[kr.active]; !ok {
		if len(kr.keys) == 0 {
			if _, err := kr.Rotate(); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("%s: active key %q not found; set it to one of %v", filepath.Join(dir, "active"), kr.active, kr.IDs())
		}
	}
	return kr, nil
}

// Rotate creates a new key and makes it active. It returns the new key ID.
func (kr *KeyRing) Rotate() (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	id := keyID(pub)
	seed := base64.StdEncoding.EncodeToString(priv.Seed())
	if err := os.WriteFile(filepath.Join(kr.dir, "pass-"+id+".key"), []byte(seed+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(kr.dir, "active"), []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	kr.mu.Lock()
	kr.keys[id], kr.active = priv, id
	kr.mu.Unlock()
	return id, nil
}

// IDs lists key IDs, sorted.
func (kr *KeyRing) IDs() []string {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	ids := make([]string, 0, len(kr.keys))
	for id := range kr.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Active returns the ID of the signing key.
func (kr *KeyRing) Active() string {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	return kr.active
}

// PublicKeys returns every accepted public key, for offline scanners.
func (kr *KeyRing) PublicKeys() map[string]ed25519.PublicKey {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	out := make(map[string]ed25519.PublicKey, len(kr.keys))
	for id, k := range kr.keys {
		out[id] = k.Public().(ed25519.PublicKey)
	}
	return out
}

// Sign issues a token with the active key.
func (kr *KeyRing) Sign(eventID, participantID, passID string) string {
	kr.mu.RLock()
	id, priv := kr.active, kr.keys[kr.active]
	kr.mu.RUnlock()
	sig := ed25519.Sign(priv, message(id, eventID, participantID, passID))
	return strings.Join([]string{prefix, id, EventCode(eventID), passID, base64.RawURLEncoding.EncodeToString(sig)}, ".")
}

// Verify checks t against the event and participant it claims to be for.
func (kr *KeyRing) Verify(t Token, eventID, participantID string) error {
	if t.EventCode != EventCode(eventID) {
		return ErrBadSig
	}
	kr.mu.RLock()
	priv, ok := kr.keys[t.KeyID]
	kr.mu.RUnlock()
	if !ok {
		return ErrUnknownKey
	}
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), message(t.KeyID, eventID, participantID, t.PassID), t.sig) {
		return ErrBadSig
	}
	return nil
}
