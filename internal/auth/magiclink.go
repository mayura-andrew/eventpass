// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"sync"
	"time"
)

// MagicLinkTTL is how long an emailed sign-in link works.
const MagicLinkTTL = 15 * time.Minute

// maxPending bounds memory if someone floods the endpoint past the rate
// limiter (for example from many IPs). Oldest entries are dropped first.
const maxPending = 10000

type pending struct {
	email    string
	kind     Kind
	expires  time.Time
	issuedAt time.Time
}

// MagicLinks keeps pending sign-in tokens in memory (ADR 0004). Only the
// SHA-256 of each token is kept, so a memory dump can't sign anyone in.
type MagicLinks struct {
	mu  sync.Mutex
	m   map[[32]byte]pending
	now func() time.Time
}

func NewMagicLinks() *MagicLinks {
	return &MagicLinks{m: map[[32]byte]pending{}, now: time.Now}
}

// SetClock replaces the clock; tests use it to expire links.
func (l *MagicLinks) SetClock(now func() time.Time) { l.now = now }

// Issue creates a token for email. The caller emails it; it is never stored.
func (l *MagicLinks) Issue(email string, kind Kind) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	if len(l.m) >= maxPending {
		var oldestKey [32]byte
		var oldest time.Time
		for k, p := range l.m {
			if oldest.IsZero() || p.issuedAt.Before(oldest) {
				oldestKey, oldest = k, p.issuedAt
			}
		}
		delete(l.m, oldestKey)
	}
	l.m[sha256.Sum256([]byte(token))] = pending{
		email: strings.ToLower(strings.TrimSpace(email)), kind: kind,
		expires: now.Add(MagicLinkTTL), issuedAt: now,
	}
	return token, nil
}

// Consume returns the email and kind for a token and deletes it, so each
// link works once. ok is false for unknown, used and expired tokens.
func (l *MagicLinks) Consume(token string) (email string, kind Kind, ok bool) {
	key := sha256.Sum256([]byte(token))
	l.mu.Lock()
	defer l.mu.Unlock()
	p, found := l.m[key]
	if !found {
		return "", "", false
	}
	delete(l.m, key)
	if !l.now().Before(p.expires) {
		return "", "", false
	}
	return p.email, p.kind, true
}

func (l *MagicLinks) sweep(now time.Time) {
	for k, p := range l.m {
		if !now.Before(p.expires) {
			delete(l.m, k)
		}
	}
}
