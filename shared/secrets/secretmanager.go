package secrets

import (
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"os"
	"path/filepath"
	"sync"
)

var (
	SLogs = shared.PackageLogger("Secrets::", "🔐 Secrets Manager::")
)

// SecretManager handles secure secret storage and retrieval
type SecretManager struct {
	keyPath  string                   // Path for key storage
	cfg      *config.NextDeployConfig // Application configuration
	secrets  map[string]*Secret       // Map of stored secrets
	keyCache map[string][]byte        // Cache for derived keys
	manager  *providerManager         // Manages different secret providers
	mu       sync.RWMutex             // Mutex for thread safety
}

// Secret represents a stored secret with metadata
type Secret struct {
	Value       string `json:"value"`        // The secret value (may be encrypted)
	Version     int    `json:"version"`      // Version for rotation
	CreatedAt   int64  `json:"created_at"`   // Creation timestamp
	ModifiedAt  int64  `json:"modified_at"`  // Last modification timestamp
	IsEncrypted bool   `json:"is_encrypted"` // Encryption status flag
}

// providerManager handles multiple secret providers
type providerManager struct {
	providers map[string]SecretProvider
}

// SecretProvider defines the interface for secret storage backends
type SecretProvider interface {
	GetSecret(key string) (string, error)
	SetSecret(key, value string) error
	DeleteSecret(key string) error
	ListSecrets() ([]string, error)
	Encrypt(data []byte, key string) ([]byte, error)
	Decrypt(data []byte, key string) ([]byte, error)
	GenerateMasterKey() (string, error)
	DeriveKey(key string) ([]byte, error)
	ValidateSecretFormat(secret string) error
}

// Option configures a SecretManager
type Option func(*SecretManager)

// WithConfig provides application configuration
func WithConfig(cfg *config.NextDeployConfig) Option {
	return func(sm *SecretManager) {
		sm.cfg = cfg
	}
}

// WithKeyPath sets a custom key storage path
func WithKeyPath(path string) Option {
	return func(sm *SecretManager) {
		if path != "" {
			sm.keyPath = path
			return
		}

		// Default path construction
		homedir, err := os.UserHomeDir()
		if err != nil {
			SLogs.Error("Failed to get home directory: %v", err)
			return
		}

		appName := "default"
		if sm.cfg != nil && sm.cfg.App.Name != "" {
			appName = sm.cfg.App.Name
		}

		sm.keyPath = filepath.Join(homedir, ".nextdeploy", appName)
	}
}

// WithProvider registers a new secret provider
func WithProvider(name string, provider SecretProvider) Option {
	return func(sm *SecretManager) {
		sm.ensureProviderManager()
		sm.manager.providers[name] = provider
	}
}

// NewSecretManager creates a new secret manager with options
func NewSecretManager(opts ...Option) (*SecretManager, error) {
	sm := &SecretManager{
		secrets:  make(map[string]*Secret),
		keyCache: make(map[string][]byte),
	}

	// Apply all options
	for _, opt := range opts {
		opt(sm)
	}

	// Load default config if none provided
	if sm.cfg == nil {
		cfg, err := config.Load()
		if err != nil {
			return nil, ErrConfigNotFound
		}
		sm.cfg = cfg
	}

	return sm, nil
}

// ensureProviderManager initializes the provider manager if nil
func (sm *SecretManager) ensureProviderManager() {
	if sm.manager == nil {
		sm.manager = &providerManager{
			providers: make(map[string]SecretProvider),
		}
	}
}
