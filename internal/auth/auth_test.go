// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

var master = bytes.Repeat([]byte{7}, 32)

func TestSessionRoundTrip(t *testing.T) {
	c, err := NewCodec(master)
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Encode(Session{Kind: KindUser, UserID: "u1", Version: 3})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Decode(v)
	if err != nil || s.UserID != "u1" || s.Version != 3 {
		t.Fatalf("Decode = %+v, %v", s, err)
	}
	if strings.Contains(v, "u1") {
		t.Fatal("cookie must be encrypted")
	}
}

func TestSessionRejects(t *testing.T) {
	c, _ := NewCodec(master)
	other, _ := NewCodec(bytes.Repeat([]byte{8}, 32))
	good, _ := c.Encode(Session{Kind: KindParticipant, Email: "a@x.org"})
	foreign, _ := other.Encode(Session{Kind: KindUser, UserID: "u1"})
	noUser, _ := c.Encode(Session{Kind: KindUser})
	badKind, _ := c.Encode(Session{Kind: "root", UserID: "u1"})

	tampered := []byte(good)
	tampered[len(tampered)-3] ^= 1
	for name, v := range map[string]string{
		"tampered": string(tampered), "foreign key": foreign, "garbage": "not-a-cookie",
		"empty": "", "no user": noUser, "bad kind": badKind,
	} {
		if _, err := c.Decode(v); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	now := time.Now()
	c.now = func() time.Time { return now }
	v, _ := c.Encode(Session{Kind: KindUser, UserID: "u1"})
	c.now = func() time.Time { return now.Add(UserSessionTTL) }
	if _, err := c.Decode(v); err == nil {
		t.Error("expired user session accepted")
	}
	c.now = func() time.Time { return now }
	v, _ = c.Encode(Session{Kind: KindParticipant, Email: "a@x.org"})
	c.now = func() time.Time { return now.Add(29 * 24 * time.Hour) }
	if _, err := c.Decode(v); err != nil {
		t.Error("participant sessions must last 30 days")
	}
}

func TestShortKeyRejected(t *testing.T) {
	if _, err := NewCodec([]byte("short")); err == nil {
		t.Fatal("short master key accepted")
	}
}

func TestMagicLinks(t *testing.T) {
	l := NewMagicLinks()
	now := time.Now()
	l.SetClock(func() time.Time { return now })

	tok, err := l.Issue(" Ada@Example.org ", KindUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 43 {
		t.Fatalf("token too short: %d chars", len(tok))
	}
	for k := range l.m {
		if bytes.Contains(k[:], []byte(tok)) {
			t.Fatal("token stored in clear")
		}
	}
	email, kind, ok := l.Consume(tok)
	if !ok || email != "ada@example.org" || kind != KindUser {
		t.Fatalf("Consume = %q %q %v", email, kind, ok)
	}
	if _, _, ok := l.Consume(tok); ok {
		t.Fatal("token worked twice")
	}

	tok, _ = l.Issue("b@x.org", KindParticipant)
	l.SetClock(func() time.Time { return now.Add(MagicLinkTTL) })
	if _, _, ok := l.Consume(tok); ok {
		t.Fatal("expired token accepted")
	}
	if _, _, ok := l.Consume("made-up"); ok {
		t.Fatal("unknown token accepted")
	}
}
