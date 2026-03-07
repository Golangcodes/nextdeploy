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

	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

type CaddyManager struct {
	adminAPI string
	client   *http.Client
}

func New(adminAPI string) *CaddyManager {
	return &CaddyManager{
		adminAPI: strings.TrimSuffix(adminAPI, "/"),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type Config struct {
	Content string
	Format  string
}

func GenerateCaddyfile(appName, domain, outputMode string, port int, appDir string, features *nextcore.DetectedFeatures, distDir, exportDir string) string {
	if distDir == "" {
		distDir = ".next"
	}
	if exportDir == "" {
		exportDir = "out"
	}

	csp := nextcore.BuildCSP(features)
	commonHeaders := fmt.Sprintf(`
	encode zstd gzip
	header {
		Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
		X-Content-Type-Options "nosniff"
		X-Frame-Options "SAMEORIGIN"
		X-XSS-Protection "1; mode=block"
		Referrer-Policy "strict-origin-when-cross-origin"
		Permissions-Policy "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()"
		X-Permitted-Cross-Domain-Policies "none"
		Content-Security-Policy "%s"
	}
	coraza_waf {
		load_owasp_crs
		directives "
			SecRuleEngine On
			SecRequestBodyAccess On
			SecAuditLog /var/log/caddy/audit.log
			SecAuditLogType Serial
			SecDebugLog /var/log/caddy/debug.log
			SecDebugLogLevel 3
		"
	}`, csp)

	sDomain := domain
	sDomain = strings.TrimPrefix(sDomain, "https://")
	sDomain = strings.TrimPrefix(sDomain, "http://")
	sDomain = strings.TrimSuffix(sDomain, "/")

	if sDomain == "" {
		sDomain = "localhost"
	}

	domainList := sDomain
	if strings.HasPrefix(sDomain, "www.") {
		root := strings.TrimPrefix(sDomain, "www.")
		domainList = fmt.Sprintf("%s, %s", sDomain, root)
	} else if !strings.Contains(sDomain, "localhost") && !strings.Contains(sDomain, "127.0.0.1") && !strings.Contains(sDomain, "::1") {
		domainList = fmt.Sprintf("%s, www.%s", sDomain, sDomain)
	}

	if outputMode == "export" {
		staticDir := filepath.Join(appDir, exportDir)
		return fmt.Sprintf(`%s {%s
	root * %s
	file_server
}`, domainList, commonHeaders, staticDir)
	}

	sharedStaticDir := filepath.Join(filepath.Dir(appDir), "shared_static")

	return fmt.Sprintf(`%s {%s
	log {
		output file /var/log/caddy/access.log
		format json
	}
	handle_path /_next/static/* {
		root * %s
		header Cache-Control "public, max-age=31536000, immutable"
		file_server
	}
	handle {
		reverse_proxy localhost:%d
	}
}`, domainList, commonHeaders, sharedStaticDir, port)
}

func (cm *CaddyManager) GetConfig(ctx context.Context) (*Config, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", cm.adminAPI+"/config/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// #nosec G704
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

	req, err := http.NewRequestWithContext(ctx, "PUT", url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if config.Format == "caddyfile" {
		req.Header.Set("Content-Type", "text/caddyfile")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}

	// #nosec G704
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
	var req *http.Request
	var err error

	switch config.Format {
	case "caddyfile":
		req, err = http.NewRequestWithContext(ctx, "POST", cm.adminAPI+"/adapt", strings.NewReader(config.Content))
		req.Header.Set("Content-Type", "text/caddyfile")
	case "json":
		req, err = http.NewRequestWithContext(ctx, "POST", cm.adminAPI+"/load", strings.NewReader(config.Content))
		req.Header.Set("Content-Type", "application/json")
	default:
		return fmt.Errorf("unsupported config format: %s", config.Format)
	}

	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

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

func (cm *CaddyManager) LoadConfig(filePath string) (*Config, error) {
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

func (cm *CaddyManager) SaveConfig(config *Config, filePath string) error {
	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(config.Content), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filePath)
}
