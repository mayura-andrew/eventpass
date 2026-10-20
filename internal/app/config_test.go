// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfigDefaults(t *testing.T) {
	c, err := LoadConfig(env(map[string]string{"EVENTPASS_DEMO": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage != "memory" || c.Addr != ":8080" || c.PublicURL != "http://localhost:8080" || c.QuotaPerMin != 50 {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	_, err := LoadConfig(env(map[string]string{"PUBLIC_URL": "localhost", "STORAGE": "mysql", "LOG_LEVEL": "loud"}))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"PUBLIC_URL", "STORAGE", "LOG_LEVEL", "EVENTPASS_SECRET_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestParseSecretKey(t *testing.T) {
	for _, s := range []string{
		"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8",
		"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
	} {
		if b, err := ParseSecretKey(s); err != nil || len(b) != 32 {
			t.Errorf("ParseSecretKey(%q) = %d bytes, %v", s, len(b), err)
		}
	}
	if _, err := ParseSecretKey("dG9vIHNob3J0"); err == nil {
		t.Error("short key accepted")
	}
}

func TestLoadDotEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("# comment\nEP_TEST_A=\"quoted value\"\nexport EP_TEST_B=2\nEP_TEST_C=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EP_TEST_C", "from-env")
	if err := LoadDotEnv(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Unsetenv("EP_TEST_A"); os.Unsetenv("EP_TEST_B") })
	if os.Getenv("EP_TEST_A") != "quoted value" || os.Getenv("EP_TEST_B") != "2" || os.Getenv("EP_TEST_C") != "from-env" {
		t.Fatalf("got A=%q B=%q C=%q", os.Getenv("EP_TEST_A"), os.Getenv("EP_TEST_B"), os.Getenv("EP_TEST_C"))
	}
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal("missing file must not be an error")
	}
}

func TestSecretsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	key := []byte("0123456789abcdef0123456789abcdef")
	if m, err := LoadSecrets(dir, key); err != nil || len(m) != 0 {
		t.Fatalf("missing file: %v %v", m, err)
	}
	if err := SaveSecrets(dir, key, map[string]string{SecretSMTPPass: "app-pass"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveSecrets(dir, key, map[string]string{SecretSystemSheet: "sheet"}); err != nil {
		t.Fatal(err)
	}
	m, err := LoadSecrets(dir, key)
	if err != nil || m[SecretSMTPPass] != "app-pass" || m[SecretSystemSheet] != "sheet" {
		t.Fatalf("round trip: %v %v", m, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "secrets.enc"))
	if strings.Contains(string(raw), "app-pass") {
		t.Fatal("secrets stored in clear")
	}
	if info, _ := os.Stat(filepath.Join(dir, "secrets.enc")); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	if _, err := LoadSecrets(dir, []byte("another-key-another-key-another-k")); err == nil || !strings.Contains(err.Error(), "EVENTPASS_SECRET_KEY") {
		t.Fatalf("wrong key: %v", err)
	}
}
