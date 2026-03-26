package secrets

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
)

var (
	SLogs = shared.PackageLogger("Secrets::", "🔐 Secrets Manager::")
)

type SecretManager struct {
	keyPath  string
	cfg      *config.NextDeployConfig
	secrets  map[string]*Secret
	keyCache map[string][]byte
	manager  *providerManager
	mu       sync.RWMutex
}

type Secret struct {
	Value       string `json:"value"`
	Version     int    `json:"version"`
	CreatedAt   int64  `json:"created_at"`
	ModifiedAt  int64  `json:"modified_at"`
	IsEncrypted bool   `json:"is_encrypted"`
}

type providerManager struct {
	providers map[string]SecretProvider
}

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

type Option func(*SecretManager)

func WithConfig(cfg *config.NextDeployConfig) Option {
	return func(sm *SecretManager) {
		sm.cfg = cfg
	}
}

func WithKeyPath(path string) Option {
	return func(sm *SecretManager) {
		if path != "" {
			sm.keyPath = path
			return
		}

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

func WithProvider(name string, provider SecretProvider) Option {
	return func(sm *SecretManager) {
		sm.ensureProviderManager()
		sm.manager.providers[name] = provider
	}
}

func NewSecretManager(opts ...Option) (*SecretManager, error) {
	sm := &SecretManager{
		secrets:  make(map[string]*Secret),
		keyCache: make(map[string][]byte),
	}

	for _, opt := range opts {
		opt(sm)
	}

	if sm.cfg == nil {
		//TODO: the secrets env should be loaded here not config
		cfg, err := config.Load()
		if err != nil {
			return nil, ErrConfigNotFound
		}
		sm.cfg = cfg
	}

	return sm, nil
}

func (sm *SecretManager) ensureProviderManager() {
	if sm.manager == nil {
		sm.manager = &providerManager{
			providers: make(map[string]SecretProvider),
		}
	}
}
