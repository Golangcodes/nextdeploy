// Package credstore stores cloud-provider credentials at rest, encrypted with
// a per-machine master key.
//
// Design follows the same headless-friendly pattern as shared/secrets:
// Linux/macOS uses a 0600 file in $HOME (works over SSH, in CI, in containers
// with no graphical session); Windows uses the Credential Manager via go-keyring.
//
// Storage layout (Linux/macOS):
//
//	~/.nextdeploy/credstore/master.key      # 32-byte random AES-256 key, mode 0600
//	~/.nextdeploy/credstore/<provider>.enc  # AES-GCM(payload), mode 0600
//
// Each provider entry is a JSON map of credential field → value, encrypted as a
// single blob. Adding/removing fields is a re-encrypt of the whole entry.
//
// All values returned by Load are auto-registered with the sensitive package
// so log redaction kicks in without callers having to remember.
package credstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zalando/go-keyring"

	"github.com/Golangcodes/nextdeploy/shared/sensitive"
)

const (
	keyringService = "nextdeploy-credstore"
	keyringMaster  = "master.key"
	dirName        = "credstore"
	masterFileName = "master.key"
)

// ErrNotFound signals that no entry exists for the requested provider.
var ErrNotFound = errors.New("credstore: entry not found")

// Load returns the decrypted credentials for provider, or ErrNotFound.
// Every field value is registered with the sensitive scrubber before return.
func Load(provider string) (map[string]string, error) {
	key, err := loadMasterKey()
	if err != nil {
		return nil, err
	}
	path, err := entryPath(provider)
	if err != nil {
		return nil, err
	}
	cipherBytes, err := os.ReadFile(path) // #nosec G304 -- path is derived from $HOME
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read entry: %w", err)
	}
	plain, err := decrypt(cipherBytes, key)
	if err != nil {
		return nil, fmt.Errorf("decrypt entry: %w", err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, fmt.Errorf("parse entry: %w", err)
	}
	for _, v := range out {
		sensitive.Register(v)
	}
	return out, nil
}

// Save encrypts and writes the credentials for provider, replacing any existing
// entry. Field values are auto-registered with the sensitive scrubber.
func Save(provider string, fields map[string]string) error {
	key, err := loadOrCreateMasterKey()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	cipherBytes, err := encrypt(plain, key)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}
	path, err := entryPath(provider)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	if err := writeFile0600(path, cipherBytes); err != nil {
		return err
	}
	for _, v := range fields {
		sensitive.Register(v)
	}
	return nil
}

// Delete removes the entry for provider. Returns nil if the entry did not exist.
func Delete(provider string) error {
	path, err := entryPath(provider)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove entry: %w", err)
	}
	return nil
}

// List returns the provider names that have stored entries.
func List() ([]string, error) {
	dir, err := credstoreDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir: %w", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".enc" {
			continue
		}
		out = append(out, name[:len(name)-4])
	}
	return out, nil
}

// ── internals ────────────────────────────────────────────────────────────────

func credstoreDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home: %w", err)
	}
	return filepath.Join(home, ".nextdeploy", dirName), nil
}

func entryPath(provider string) (string, error) {
	if provider == "" {
		return "", errors.New("provider is required")
	}
	dir, err := credstoreDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, provider+".enc"), nil
}

// loadMasterKey returns an existing master key, or ErrNotFound if none exists.
// On Linux/macOS the key is a file in $HOME; on Windows it's a keyring entry.
func loadMasterKey() ([]byte, error) {
	if runtime.GOOS == "windows" {
		v, err := keyring.Get(keyringService, keyringMaster)
		if err != nil {
			if errors.Is(err, keyring.ErrNotFound) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("keyring get: %w", err)
		}
		return []byte(v), nil
	}
	dir, err := credstoreDir()
	if err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, masterFileName)
	b, err := os.ReadFile(keyPath) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("master key has wrong length: %d (want 32)", len(b))
	}
	return b, nil
}

// loadOrCreateMasterKey loads an existing key or generates a new one.
func loadOrCreateMasterKey() ([]byte, error) {
	key, err := loadMasterKey()
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}

	if runtime.GOOS == "windows" {
		if err := keyring.Set(keyringService, keyringMaster, string(newKey)); err != nil {
			return nil, fmt.Errorf("keyring set: %w", err)
		}
		return newKey, nil
	}

	dir, err := credstoreDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create credstore dir: %w", err)
	}
	keyPath := filepath.Join(dir, masterFileName)
	if err := writeFile0600(keyPath, newKey); err != nil {
		return nil, err
	}
	return newKey, nil
}

func encrypt(plain, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func decrypt(blob, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// writeFile0600 writes data atomically via a temp file in the same dir, then
// renames into place — guarantees the target either exists fully or not at all.
func writeFile0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
