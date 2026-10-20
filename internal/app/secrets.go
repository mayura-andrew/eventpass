// SPDX-License-Identifier: Apache-2.0

package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mayura-andrew/eventpass/internal/auth"
)

// secretsFile holds settings entered in the setup wizard (spreadsheet ID,
// SMTP password, service account key), sealed with AES-256-GCM under a key
// derived from EVENTPASS_SECRET_KEY. Secrets never go into a spreadsheet
// or to the browser. Environment variables override every value.
const secretsFile = "secrets.enc"

var secretsAAD = []byte("eventpass-secrets-v1")

func secretsAEAD(master []byte) (cipher.AEAD, error) {
	key, err := auth.DeriveKey(master, "secrets")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// LoadSecrets reads DATA_DIR/secrets.enc. A missing file is an empty map.
func LoadSecrets(dataDir string, master []byte) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, secretsFile)) //nolint:gosec // G304: inside DATA_DIR
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	aead, err := secretsAEAD(master)
	if err != nil {
		return nil, err
	}
	n := aead.NonceSize()
	if len(b) < n {
		return nil, errors.New("secrets.enc is corrupt")
	}
	plain, err := aead.Open(nil, b[:n], b[n:], secretsAAD)
	if err != nil {
		return nil, fmt.Errorf("can't decrypt %s: EVENTPASS_SECRET_KEY differs from the one used to write it", secretsFile)
	}
	out := map[string]string{}
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, fmt.Errorf("secrets.enc is corrupt: %w", err)
	}
	return out, nil
}

// SaveSecrets merges values into DATA_DIR/secrets.enc atomically.
func SaveSecrets(dataDir string, master []byte, values map[string]string) error {
	cur, err := LoadSecrets(dataDir, master)
	if err != nil {
		return err
	}
	for k, v := range values {
		cur[k] = v
	}
	plain, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	aead, err := secretsAEAD(master)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dataDir, secretsFile+".tmp")
	if err := os.WriteFile(tmp, aead.Seal(nonce, nonce, plain, secretsAAD), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dataDir, secretsFile))
}

// Secret keys in secrets.enc mirror the environment variable names. They are
// names, not credentials.
//
//nolint:gosec // G101: these are key names
const (
	SecretSystemSheet = "SYSTEM_SHEET_ID"
	SecretGoogleJSON  = "GOOGLE_CREDENTIALS_JSON"
	SecretSMTPHost    = "SMTP_HOST"
	SecretSMTPPort    = "SMTP_PORT"
	SecretSMTPUser    = "SMTP_USERNAME"
	SecretSMTPPass    = "SMTP_PASSWORD"
	SecretSMTPFrom    = "SMTP_FROM"
)
