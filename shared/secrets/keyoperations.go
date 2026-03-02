package secrets

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"

	"crypto/rand"
	"runtime"
)

func (sm *SecretManager) getCachedKey(name string) (string, error) {
	if key, exists := sm.keyCache[name]; exists {
		return string(key), nil
	}

	key, err := sm.GeneratePlatformKey()
	if err != nil {
		return "", err
	}

	sm.keyCache[name] = []byte(key)
	return key, nil
}
func (sm *SecretManager) GenerateMasterKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyGenerationFailed, err)
	}
	return key, nil
}

func (sm *SecretManager) GenerateWindowsKey() (string, error) {
	key, err := sm.GenerateMasterKey()
	if err != nil {
		return "", fmt.Errorf("failed to generate master key for Windows: %w", err)
	}
	return string(key), nil
}

func (sm *SecretManager) GeneratePlatformKey() (string, error) {
	currentOS := runtime.GOOS
	SLogs.Debug("Generating platform key for OS: %s", currentOS)
	switch currentOS {
	case "linux", "darwin": // Linux or macOS
		return sm.derivePlatformKey("linux-mac-salt")
	case "windows": // Windows
		return sm.derivePlatformKey("windows-salt")
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedPlatform, currentOS)
	}
}

func (sm *SecretManager) derivePlatformKey(salt string) (string, error) {
	masterkey, err := sm.getOrCreateMasterKey()
	SLogs.Debug("Using master key: %s", masterkey)
	if err != nil {
		return "", fmt.Errorf("failed to get or create master key: %w", err)
	}
	SLogs.Debug("Derived key: %s", masterkey)
	return base64.StdEncoding.EncodeToString(masterkey), nil
}

func (sm *SecretManager) getOrCreateMasterKey() ([]byte, error) {
	key, err := sm.loadMasterKey()
	if err == nil {
		SLogs.Debug("Master key loaded successfully: %s", key)
		return key, nil
	}
	SLogs.Debug("Master key not found, generating new key")
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to load master key: %w", err)
	}
	newKey, err := sm.GenerateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate new master key: %w", err)
	}
	return []byte(newKey), nil
}

func (sm *SecretManager) loadMasterKey() ([]byte, error) {
	switch runtime.GOOS {
	case "linux", "darwin":
		return sm.loadUnixKey()
	case "windows":
		key, err := sm.loadWindowsKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate master key for Windows: %w", err)
		}

		return []byte(key), nil
	default:
		return nil, ErrUnsupportedPlatform
	}
}

func (sm *SecretManager) storeMasterKey(key []byte) error {
	switch runtime.GOOS {
	case "linux", "darwin":
		SLogs.Debug("The key that is supposed to stored is :%s", key)
		return sm.storeUnixKey(key)
	case "windows":
		return sm.storeWindowsKey(key)
	default:
		return ErrUnsupportedPlatform
	}
}

func (sm *SecretManager) storeUnixKey(key []byte) error {
	appname := sm.cfg.App.Name

	// get home directory
	homedir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	SLogs.Debug("Storing master key in keyring for app: %s", appname)
	appDir := filepath.Join(homedir, ".nextdeploy", appname)
	if err := os.MkdirAll(appDir, 0700); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	keyPath := filepath.Join(appDir, "master.key")
	SLogs.Debug("Key path for master key: %s", keyPath)
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return fmt.Errorf("failed to write master key to file: %w", err)
	}

	SLogs.Success("Successfully stored master key at: %s", keyPath)
	return nil
}
func (sm *SecretManager) IsKeyExist() bool {
	switch runtime.GOOS {
	case "linux", "darwin":
		return sm.isUnixKeyExist()
	case "windows":
		return sm.isWindowsKeyExist()
	default:
		return false
	}
}
func (sm *SecretManager) isWindowsKeyExist() bool {
	if sm.cfg == nil || sm.cfg.App.Name == "" {
		SLogs.Error("Invalid configuration: app name not set")
		return false
	}
	appname := sm.cfg.App.Name
	_, err := keyring.Get(appname, "master_key")
	if err != nil {
		SLogs.Debug("Master key not found in Windows keyring or error occurred: %v", err)
		return false
	}
	return true
}
func (sm *SecretManager) isUnixKeyExist() bool {
	const filename = "master.key"
	if sm.cfg == nil || sm.cfg.App.Name == "" {
		SLogs.Error("Invalid configuration: app name not set")
		return false
	}
	homedir, _ := os.UserHomeDir()
	filePath := homedir + "/.nextdeploy/" + sm.cfg.App.Name + "/" + filename
	file, err := os.Stat(filePath)
	if err != nil {
		SLogs.Error("Failed to check if master key exists: %v", err)
		return false
	}
	if file != nil {
		return true
	}
	if os.IsNotExist(err) {
		SLogs.Debug("Master key does not exist at: %s", filePath)
		return false
	} else {
		SLogs.Error("Error checking master key existence: %v", err)
		return false
	}
}
func (sm *SecretManager) loadUnixKey() ([]byte, error) {
	const keyFilename = "master.key"
	if sm.cfg == nil || sm.cfg.App.Name == "" {
		return nil, fmt.Errorf("invalid configuration: app name not set")
	}
	homedir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	appDir := filepath.Join(homedir, ".nextdeploy", sm.cfg.App.Name)
	keyPath := filepath.Join(appDir, keyFilename)
	SLogs.Debug("Attempting to load master key from: %s", keyPath)
	// #nosec G304
	keyData, err := os.ReadFile(keyPath)
	if err == nil {
		if len(keyData) == 0 {
			SLogs.Warn("Existing master key file is empty")
			return nil, fmt.Errorf("empty master key")
		}
		SLogs.Debug("Successfully loaded existing master key")
		return keyData, nil
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}
	SLogs.Info("No master key found, generating new one")
	newKey, err := sm.GenerateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate master key: %w", err)
	}

	if err := os.MkdirAll(appDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.WriteFile(keyPath, newKey, 0600); err != nil {
		return nil, fmt.Errorf("failed to store master key: %w", err)
	}

	SLogs.Info("Successfully generated and stored new master key at %s", keyPath)
	return newKey, nil
}
func (sm *SecretManager) storeWindowsKey(key []byte) error {
	appname := sm.cfg.App.Name
	err := keyring.Set(appname, "master_key", string(key))
	if err != nil {
		return fmt.Errorf("failed to store master key in Windows keyring: %w", err)
	}
	return nil
}

func (sm *SecretManager) loadWindowsKey() ([]byte, error) {
	appname := sm.cfg.App.Name
	encryptedKey, err := keyring.Get(appname, "master_key")
	if err != nil {
		return nil, fmt.Errorf("failed to load master key from Windows keyring: %w", err)
	}
	return []byte(encryptedKey), nil
}

func (sm *SecretManager) GetKeyOsAgnosticPath() string {
	home, _ := os.UserHomeDir()
	appname := sm.GetAppName()
	return home + "/.nextdeploy/" + appname + "/master.key"
}
