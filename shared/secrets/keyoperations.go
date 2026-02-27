package secrets

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/zalando/go-keyring"
	"os"
	"path/filepath"

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

// GenerateWindowsKey generates a key for Windows platforms
func (sm *SecretManager) GenerateWindowsKey() (string, error) {
	// Windows-specific key generation logic could go here
	// For now, we'll use the same approach as other platforms
	key, err := sm.GenerateMasterKey()
	if err != nil {
		return "", fmt.Errorf("failed to generate master key for Windows: %w", err)
	}
	// Convert to hex string for storage
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
	// Logout the derivePlatformKey
	SLogs.Debug("Derived key: %s", masterkey)
	return base64.StdEncoding.EncodeToString(masterkey), nil
}

func (sm *SecretManager) getOrCreateMasterKey() ([]byte, error) {
	// Try to load existing key
	key, err := sm.loadMasterKey()
	if err == nil {
		SLogs.Debug("Master key loaded successfully: %s", key)
		return key, nil
	}
	SLogs.Debug("Master key not found, generating new key")
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to load master key: %w", err)
	}
	// generate new key if none exists
	newKey, err := sm.GenerateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate new master key: %w", err)
	}
	return []byte(newKey), nil
}

func (sm *SecretManager) loadMasterKey() ([]byte, error) {
	// platform specifc secure storage
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
	// platform specific secure storage
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

	// Create application directory
	appDir := filepath.Join(homedir, ".nextdeploy", appname)
	if err := os.MkdirAll(appDir, 0700); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	// Set full key path
	keyPath := filepath.Join(appDir, "master.key")
	SLogs.Debug("Key path for master key: %s", keyPath)

	// Write key file with restricted permissions
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return fmt.Errorf("failed to write master key to file: %w", err)
	}

	SLogs.Success("Successfully stored master key at: %s", keyPath)
	return nil
}
func (sm *SecretManager) IsKeyExist() bool {
	// Check if the key exists in the keyring
	switch runtime.GOOS {
	case "linux", "darwin":
		return sm.isUnixKeyExist()
	case "windows":
		return sm.isWindowsKeyExist()
	default:
		return false
	}
}
func (sm *SecretManager) isUnixKeyExist() bool {
	const filename = "master.key"
	// Validate configuration
	if sm.cfg == nil || sm.cfg.App.Name == "" {
		SLogs.Error("Invalid configuration: app name not set")
		return false
	}
	// Setup paths
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
func (sm *SecretManager) isWindowsKeyExist() bool {
	// appname := sm.cfg.App.Name
	// _, err := keyring.Get(appname, "master_key")
	// if err != nil {
	// 	if errors.Is(err, keyring.ErrNotFound) {
	// 		SLogs.Debug("Master key not found in Windows keyring")
	// 		return false
	// 	}
	// 	SLogs.Error("Failed to check if master key exists in Windows keyring: %v", err)
	// 	return false
	// }
	// SLogs.Debug("Master key exists in Windows keyring")
	return true
}
func (sm *SecretManager) loadUnixKey() ([]byte, error) {
	const keyFilename = "master.key"

	// Validate configuration
	if sm.cfg == nil || sm.cfg.App.Name == "" {
		return nil, fmt.Errorf("invalid configuration: app name not set")
	}

	// Setup paths
	homedir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	appDir := filepath.Join(homedir, ".nextdeploy", sm.cfg.App.Name)
	keyPath := filepath.Join(appDir, keyFilename)
	SLogs.Debug("Attempting to load master key from: %s", keyPath)

	// #nosec G304
	// Try reading existing key
	keyData, err := os.ReadFile(keyPath)
	if err == nil {
		if len(keyData) == 0 {
			SLogs.Warn("Existing master key file is empty")
			return nil, fmt.Errorf("empty master key")
		}
		SLogs.Debug("Successfully loaded existing master key")
		return keyData, nil
	}

	// Handle key not found case
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}

	SLogs.Info("No master key found, generating new one")

	// Generate and store new key
	newKey, err := sm.GenerateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate master key: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(appDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create app directory: %w", err)
	}

	// Store the key before returning
	if err := os.WriteFile(keyPath, newKey, 0600); err != nil {
		return nil, fmt.Errorf("failed to store master key: %w", err)
	}

	SLogs.Info("Successfully generated and stored new master key at %s", keyPath)
	return newKey, nil
}
func (sm *SecretManager) storeWindowsKey(key []byte) error {
	appname := sm.cfg.App.Name
	// Optional: encrypt key here with DPAPI or AES

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

	// Optional: decrypt key here if you encrypted it
	return []byte(encryptedKey), nil
}

func (sm *SecretManager) GetKeyOsAgnosticPath() string {
	// platform agnostic key path
	home, _ := os.UserHomeDir()
	appname := sm.GetAppName()
	return home + "/.nextdeploy/" + appname + "/master.key"
}
