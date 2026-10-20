// SPDX-License-Identifier: Apache-2.0

package pass

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignVerify(t *testing.T) {
	kr, err := LoadKeyRing(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tok := kr.Sign("event-1", "participant-1", "EP-7KQ4-M2XD")
	if !strings.HasPrefix(tok, "EP1."+kr.Active()+"."+EventCode("event-1")+".EP-7KQ4-M2XD.") {
		t.Fatalf("token format: %s", tok)
	}
	parsed, err := Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Verify(parsed, "event-1", "participant-1"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := kr.Verify(parsed, "event-1", "participant-2"); !errors.Is(err, ErrBadSig) {
		t.Fatalf("wrong participant: %v", err)
	}
	if err := kr.Verify(parsed, "event-2", "participant-1"); !errors.Is(err, ErrBadSig) {
		t.Fatalf("wrong event: %v", err)
	}
	// Changing the pass ID breaks the signature.
	forged, _ := Parse(strings.Replace(tok, "EP-7KQ4-M2XD", "EP-7KQ4-M2XE", 1))
	if err := kr.Verify(forged, "event-1", "participant-1"); !errors.Is(err, ErrBadSig) {
		t.Fatalf("forged pass ID: %v", err)
	}
	if strings.Contains(tok, "participant-1") {
		t.Fatal("token must not contain the participant ID")
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "EP1.a.b.c", "EP2.a.b.c.AAAA", "EP1.a.b.c.not-base64!", "EP1.a.b.c.AAAA", "{\"id\":1}"} {
		if _, err := Parse(s); !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%q) = %v", s, err)
		}
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	kr, err := LoadKeyRing(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := kr.Active()
	oldTok, _ := Parse(kr.Sign("e", "p", "EP-2222-3333"))
	newID, err := kr.Rotate()
	if err != nil || newID == old {
		t.Fatalf("Rotate = %q, %v", newID, err)
	}
	// Reload from disk: both keys present, new one active.
	kr2, err := LoadKeyRing(dir)
	if err != nil {
		t.Fatal(err)
	}
	if kr2.Active() != newID || len(kr2.IDs()) != 2 || len(kr2.PublicKeys()) != 2 {
		t.Fatalf("after reload: active %s, ids %v", kr2.Active(), kr2.IDs())
	}
	if err := kr2.Verify(oldTok, "e", "p"); err != nil {
		t.Fatalf("old tokens must keep verifying: %v", err)
	}
	// Removing the old key file revokes its tokens.
	if err := os.Remove(filepath.Join(dir, "pass-"+old+".key")); err != nil {
		t.Fatal(err)
	}
	kr3, _ := LoadKeyRing(dir)
	if err := kr3.Verify(oldTok, "e", "p"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("removed key: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "pass-"+newID+".key")); info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", info.Mode().Perm())
	}
}

func TestLoadKeyRingErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pass-bad.key"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyRing(dir); err == nil {
		t.Fatal("corrupt key accepted")
	}
}

func TestRender(t *testing.T) {
	kr, _ := LoadKeyRing(t.TempDir())
	c := Card{
		ProductName: "EventPass", EventName: "DevFest Colombo 2026 with a very long name that must be shortened",
		When: "3 Nov 2026, 09:00 GMT+5:30", Venue: "BMICH", FullName: "Ada Lovelace", PassID: "EP-7KQ4-M2XD",
		Token: kr.Sign("e", "p", "EP-7KQ4-M2XD"), Lines: []Line{{"College", "UCSC"}}, Primary: "#1B4DDB",
	}
	b, err := CardPNG(c)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 600 || img.Bounds().Dy() != 900 {
		t.Fatalf("PNG: %v %v", img, err)
	}
	c.Inactive, c.StatusLabel = true, "Cancelled"
	if _, err := CardPNG(c); err != nil {
		t.Fatal(err)
	}
	pdf, err := CardPDF(c)
	if err != nil {
		t.Fatal(err)
	}
	// A6 is 105 mm = 297.6 pt wide.
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf, []byte("/MediaBox [0 0 297.6")) {
		t.Fatalf("not an A6 PDF: %q", pdf[:min(200, len(pdf))])
	}
	qr, err := QRPNG(c.Token, 300)
	if err != nil || !bytes.HasPrefix(qr, []byte("\x89PNG")) {
		t.Fatalf("QR: %v", err)
	}
}
