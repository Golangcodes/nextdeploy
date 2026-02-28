package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CaddyManager provides simplified Caddy server configuration management
type CaddyManager struct {
	adminAPI string       // Caddy admin API endpoint (e.g., "http://localhost:2019")
	client   *http.Client // HTTP client for API requests
}

// New creates a new CaddyManager instance
func New(adminAPI string) *CaddyManager {
	return &CaddyManager{
		adminAPI: strings.TrimSuffix(adminAPI, "/"),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type Config struct {
	Content string // The configuration content
	Format  string // "json" or "caddyfile"
}

func GenerateCaddyfile(appName, domain, outputMode string, port int, appDir string) string {
	commonHeaders := `
	encode zstd gzip
	header {
		Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
		X-Content-Type-Options "nosniff"
		X-Frame-Options "SAMEORIGIN"
		X-XSS-Protection "1; mode=block"
		Referrer-Policy "strict-origin-when-cross-origin"
		Permissions-Policy "accelerometer=(), camera=(), geolocaton=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()"
	}`

	sDomain := domain
	sDomain = strings.TrimPrefix(sDomain, "https://")
	sDomain = strings.TrimPrefix(sDomain, "http://")
	sDomain = strings.TrimSuffix(sDomain, "/")

	domainList := sDomain
	if strings.HasPrefix(sDomain, "www.") {
		root := strings.TrimPrefix(sDomain, "www.")
		domainList = fmt.Sprintf("%s, %s", sDomain, root)
	} else if !strings.Contains(sDomain, "localhost") && !strings.Contains(sDomain, "127.0.0.1") && !strings.Contains(sDomain, "::1") {
		domainList = fmt.Sprintf("%s, www.%s", sDomain, sDomain)
	}

	if outputMode == "export" {
		// Static site hosting
		staticDir := filepath.Join(appDir, "out")
		return fmt.Sprintf(`%s {%s
	root * %s
	file_server
}`, domainList, commonHeaders, staticDir)
	}

	nextStaticDir := filepath.Join(appDir, ".next", "static")

	return fmt.Sprintf(`%s {%s
	
	handle_path /_next/static/* {
		root * %s
		header Cache-Control "public, max-age=31536000, immutable"
		file_server
	}

	handle {
		reverse_proxy localhost:%d
	}
}`, domainList, commonHeaders, nextStaticDir, port)
}

func (cm *CaddyManager) GetConfig(ctx context.Context) (*Config, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", cm.adminAPI+"/config/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := cm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return &Config{
		Content: string(body),
		Format:  "json",
	}, nil
}

func (cm *CaddyManager) ApplyConfig(ctx context.Context, config *Config) error {
	var (
		url  string
		body io.Reader
	)

	switch config.Format {
	case "json":
		url = cm.adminAPI + "/config/"
		body = strings.NewReader(config.Content)
	case "caddyfile":
		url = cm.adminAPI + "/load"
		body = strings.NewReader(config.Content)
	default:
		return fmt.Errorf("unsupported config format: %s", config.Format)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if config.Format == "caddyfile" {
		req.Header.Set("Content-Type", "text/caddyfile")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := cm.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to apply config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to apply config (status %d): %s", resp.StatusCode, string(errorBody))
	}

	return nil
}

func (cm *CaddyManager) ValidateConfig(ctx context.Context, config *Config) error {
	if config.Format != "caddyfile" {
		return fmt.Errorf("validation is only supported for caddyfile format")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cm.adminAPI+"/validate", strings.NewReader(config.Content))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "text/caddyfile")

	// #nosec G704
	resp, err := cm.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("config validation failed (status %d): %s", resp.StatusCode, string(errorBody))
	}

	return nil
}

func (cm *CaddyManager) PatchConfig(ctx context.Context, path string, config interface{}) error {
	jsonData, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PATCH", cm.adminAPI+path, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// #nosec G704
	resp, err := cm.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to patch config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to patch config (status %d): %s", resp.StatusCode, string(errorBody))
	}

	return nil
}

// LoadConfig loads a configuration from a file
func (cm *CaddyManager) LoadConfig(ctx context.Context, filePath string) (*Config, error) {
	// #nosec G304
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	format := "caddyfile"
	if strings.HasSuffix(filePath, ".json") {
		format = "json"
	}

	return &Config{
		Content: string(content),
		Format:  format,
	}, nil
}

func (cm *CaddyManager) SaveConfig(ctx context.Context, config *Config, filePath string) error {
	return os.WriteFile(filePath, []byte(config.Content), 0600)
}
