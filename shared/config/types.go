package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type NextDeployConfig struct {
	Version       string               `yaml:"version"`
	TargetType    string               `yaml:"target_type"` // e.g., "vps", "serverless"
	App           AppConfig            `yaml:"app"`
	Repository    Repository           `yaml:"repository"`
	Docker        *DockerConfig        `yaml:"docker,omitempty"`
	Serverless    *ServerlessConfig    `yaml:"serverless,omitempty"`
	Database      *Database            `yaml:"database,omitempty"`
	Monitoring    *Monitoring          `yaml:"monitoring,omitempty"`
	Secrets       SecretsConfig        `yaml:"secrets"`
	Logging       Logging              `yaml:"logging,omitempty"`
	Backup        *Backup              `yaml:"backup,omitempty"`
	SSL           *SSL                 `yaml:"ssl,omitempty"`
	Webhook       *WebhookConfig       `yaml:"webhook,omitempty"`
	Environment   []EnvVariable        `yaml:"environment,omitempty"`
	Servers       []ServerConfig       `yaml:"servers,omitempty"`
	SSLConfig     *SSLConfig           `yaml:"ssl_config,omitempty"`
	CloudProvider *CloudProviderStruct `yaml:"CloudProvider,omitempty"`
}

type SafeConfig struct {
	AppName     string `json:"app_name"`
	Domain      string `json:"domain"`
	Port        int    `json:"port"`
	Environment string `json:"environment"`
	TargetType  string `json:"target_type"`
}

type ServerlessConfig struct {
	Provider     string `yaml:"provider"` // e.g., "aws"
	Region       string `yaml:"region"`
	CloudFrontId string `yaml:"cloudfront_id,omitempty"`
	IAMRole      string `yaml:"iam_role,omitempty"`    // IAM Role ARN for Lambda
	Handler      string `yaml:"handler,omitempty"`     // Lambda handler (defaults to server.handler)
	Runtime      string `yaml:"runtime,omitempty"`     // Lambda runtime (defaults to nodejs20.x)
	MemorySize   int32  `yaml:"memory_size,omitempty"` // Lambda memory size in MB (defaults to 1024)
	Timeout      int32  `yaml:"timeout,omitempty"`     // Lambda timeout in seconds (defaults to 30)
	Profile      string `yaml:"profile,omitempty"`     // AWS CLI profile name
}

type WebServer struct {
	Type          string `yaml:"type"`
	ConfigPath    string `yaml:"config_path,omitempty"`
	SSL_Enabled   bool   `yaml:"ssl_enabled,omitempty"`
	SSL_Cert_Path string `yaml:"ssl_cert_path,omitempty"`
	SSL_Key_Path  string `yaml:"ssl_key_path,omitempty"`
}
type SSLConfig struct {
	Domain      string `yaml:"domain"`
	Email       string `yaml:"email"`
	Staging     bool   `yaml:"staging"`
	Wildcard    bool   `yaml:"wildcard"`
	DNSProvider string `yaml:"dns_provider"`
	Force       bool   `yaml:"force"`
	SSL         struct {
		Enabled   bool   `yaml:"enabled"`
		Provider  string `yaml:"provider"`
		Email     string `yaml:"email"`
		AutoRenew bool   `yaml:"auto_renew"`
	} `yaml:"ssl"`
}

type CloudProviderStruct struct {
	Name   string `yaml:"name"`
	Region string `yaml:"region"`
	// #nosec G117
	AccessKey string `yaml:"access_key,omitempty"`
	SecretKey string `yaml:"secret_key,omitempty"`
	Profile   string `yaml:"profile,omitempty"` // AWS CLI profile name
}
type ServerConfig struct {
	WebServer *WebServer `yaml:"web_server,omitempty"`
	Name      string     `yaml:"name"`
	Host      string     `yaml:"host"`
	Port      int        `yaml:"port"`
	Username  string     `yaml:"username"`
	// #nosec G117
	Password      string `yaml:"password"`
	KeyPath       string `yaml:"key_path"`
	SSHKey        string `yaml:"ssh_key,omitempty"`
	KeyPassphrase string `yaml:"key_passphrase,omitempty"`
}

type AppConfig struct {
	Name        string         `yaml:"name"`
	Port        int            `yaml:"port"`
	Environment string         `yaml:"environment"`
	Domain      string         `yaml:"domain,omitempty"`
	CDNEnabled  bool           `yaml:"cdn_enabled,omitempty"`
	Secrets     *SecretsConfig `yaml:"secrets,omitempty"`
}

type Repository struct {
	URL           string `yaml:"url"`
	Branch        string `yaml:"branch"`
	AutoDeploy    bool   `yaml:"autoDeploy"`
	WebhookSecret string `yaml:"webhookSecret,omitempty"`
}
type DockerConfig struct {
	Image          string      `yaml:"image"`
	Registry       string      `yaml:"registry,omitempty"`
	RegistryRegion string      `yaml:"registryregion,omitempty"`
	Build          DockerBuild `yaml:"build"`
	Push           bool        `yaml:"push"`
	Username       string      `yaml:"username,omitempty"`
	// #nosec G117
	Password     string `yaml:"password,omitempty"`
	AlwaysPull   bool   `yaml:"alwaysPull,omitempty"`
	Strategy     string `yaml:"strategy,omitempty"`
	AutoPush     bool   `yaml:"autoPush,omitempty"`
	Platform     string `yaml:"platform,omitempty"`
	NoCache      bool   `yaml:"noCache,omitempty"`
	BuildContext string `yaml:"buildContext,omitempty"`
	Target       string `yaml:"target,omitempty"`
}

type DockerBuild struct {
	Context    string            `yaml:"context"`
	Dockerfile string            `yaml:"dockerfile"`
	NoCache    bool              `yaml:"noCache"`
	Args       map[string]string `yaml:"args,omitempty"`
}

type Database struct {
	Type     string `yaml:"type"`
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Username string `yaml:"username"`
	// #nosec G117
	Password        string `yaml:"password"`
	Name            string `yaml:"name"`
	MigrateOnDeploy bool   `yaml:"migrate_on_deploy,omitempty"`
}

type Monitoring struct {
	Enabled         bool   `yaml:"enabled"`
	Type            string `yaml:"type"`
	Endpoint        string `yaml:"endpoint"`
	CPUThreshold    int    `yaml:"cpu_threshold,omitempty"`
	MemoryThreshold int    `yaml:"memory_threshold,omitempty"`
	DiskThreshold   int    `yaml:"disk_threshold,omitempty"`
	Alert           *Alert `yaml:"alert,omitempty"`
}

type Alert struct {
	Email        string   `yaml:"email,omitempty"`
	SlackWebhook string   `yaml:"slack_webhook,omitempty"`
	NotifyOn     []string `yaml:"notify_on,omitempty"`
}

type SecretsConfig struct {
	Provider string         `yaml:"provider"`
	Doppler  *DopplerConfig `yaml:"doppler,omitempty"`
	Vault    *VaultConfig   `yaml:"vault,omitempty"`
	Files    []SecretFile   `yaml:"files,omitempty"`
	Project  string         `yaml:"project,omitempty"`
	Config   string         `yaml:"config,omitempty"`
	token    string         `yaml:"token,omitempty"`
}

type DopplerConfig struct {
	Project string `yaml:"project"`
	Config  string `yaml:"config"`
	Token   string `yaml:"token,omitempty"`
}

type VaultConfig struct {
	Address string `yaml:"address"`
	Token   string `yaml:"token"`
	Path    string `yaml:"path"`
}

type SecretFile struct {
	Path string `yaml:"path"`
	// #nosec G117
	Secret string `yaml:"secret"`
}

type Logging struct {
	Enabled    bool   `yaml:"enabled"`
	Provider   string `yaml:"provider"`
	StreamLogs bool   `yaml:"stream_logs"`
	LogPath    string `yaml:"log_path"`
}

type Backup struct {
	Enabled       bool    `yaml:"enabled"`
	Frequency     string  `yaml:"frequency,omitempty"`
	RetentionDays int     `yaml:"retention_days,omitempty"`
	Storage       Storage `yaml:"storage"`
}

type Storage struct {
	Type     string `yaml:"type"`
	Endpoint string `yaml:"endpoint,omitempty"`
	Bucket   string `yaml:"bucket"`
	// #nosec G117
	AccessKey string `yaml:"accessKey,omitempty"`
	SecretKey string `yaml:"secretKey,omitempty"`
}

type SSL struct {
	Enabled     bool     `yaml:"enabled"`
	Provider    string   `yaml:"provider"`
	Domains     []string `yaml:"domains"`
	Email       string   `yaml:"email"`
	Wildcard    bool     `yaml:"wildcard"`
	DNSProvider string   `yaml:"dns_provider"`
	Staging     bool     `yaml:"staging"`
	Force       bool     `yaml:"force"`
	AutoRenew   bool     `yaml:"auto_renew"`
	Domain      string   `yaml:"domain,omitempty"`
}

type WebhookConfig struct {
	OnSuccess []string `yaml:"on_success,omitempty"`
	OnFailure []string `yaml:"on_failure,omitempty"`
}

type EnvVariable struct {
	Name   string `yaml:"name"`
	Value  string `yaml:"value"`
	Secret bool   `yaml:"secret,omitempty"`
}

func SaveConfig(path string, cfg *NextDeployConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
