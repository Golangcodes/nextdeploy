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
	Provider          string `yaml:"provider"` // "aws" or "cloudflare"
	Region            string `yaml:"region"`
	CloudFrontId      string `yaml:"cloudfront_id,omitempty"`
	IAMRole           string `yaml:"iam_role,omitempty"`           // IAM Role ARN for Lambda
	Handler           string `yaml:"handler,omitempty"`            // Lambda handler (defaults to server.handler)
	Runtime           string `yaml:"runtime,omitempty"`            // Lambda runtime (defaults to nodejs20.x)
	MemorySize        int32  `yaml:"memory_size,omitempty"`        // Lambda memory size in MB (defaults to 1024)
	Timeout           int32  `yaml:"timeout,omitempty"`            // Lambda timeout in seconds (defaults to 30)
	Profile           string `yaml:"profile,omitempty"`            // AWS CLI profile name
	IsrRevalidation   bool   `yaml:"isr_revalidation,omitempty"`   // Deploy ISR Revalidation Lambda + SQS
	ImageOptimization bool   `yaml:"image_optimization,omitempty"` // Deploy Image Optimizer Lambda + CF Behavior
	Warmer            bool   `yaml:"warmer,omitempty"`             // Deploy EventBridge warmer cron

	// AllowSecretsInEnv opts in to the insecure fallback that injects every
	// secret directly into the Lambda's environment variables when the IAM
	// principal lacks lambda:GetLayerVersion (and therefore cannot use the
	// Secrets Extension layer). Default false; deploys fail loudly with IAM
	// guidance instead. Only set this to true if you accept that secrets will
	// be visible in the Lambda console, CloudTrail, and persisted in every
	// published Lambda version.
	AllowSecretsInEnv bool `yaml:"allow_secrets_in_env,omitempty"`

	// KmsKeyId selects a customer-managed KMS key for Secrets Manager
	// encryption. Accepts a key ID, key ARN, or alias (e.g. "alias/prod-secrets").
	// Empty uses the AWS-managed `aws/secretsmanager` key. Required by many
	// multi-account and compliance setups where the default key can't be
	// shared across boundaries.
	KmsKeyId string `yaml:"kms_key_id,omitempty"`

	// Cloudflare-specific config. Ignored when Provider != "cloudflare".
	// Each field maps directly to a Cloudflare API call (or a chunk of one)
	// to keep translation from wrangler.toml mechanical.
	Cloudflare *CloudflareConfig `yaml:"cloudflare,omitempty"`
}

// CloudflareConfig holds everything that ends up in a Workers script upload
// (workers.ScriptUpdateParamsMetadata) plus post-upload calls (cron triggers,
// custom domains, routes). Standalone resource provisioning (Hyperdrive,
// Queues, Vectorize, AI Gateway, DNS, Zone settings) lives in
// CloudflareConfig.Resources and is consumed by the plan/apply pipeline.
type CloudflareConfig struct {
	// Worker runtime
	CompatibilityDate  string   `yaml:"compatibility_date,omitempty"`  // default: "2025-04-01"
	CompatibilityFlags []string `yaml:"compatibility_flags,omitempty"` // default: ["nodejs_compat_v2"]

	// Edge attachment
	CustomDomains []CFCustomDomain `yaml:"custom_domains,omitempty"` // preferred over routes
	Routes        []CFRoute        `yaml:"routes,omitempty"`         // legacy zone-routes

	// Triggers (separate post-upload call: Workers.Scripts.Schedules.Update)
	Triggers *CFTriggers `yaml:"triggers,omitempty"`

	// Bindings — one entry per binding type; each translates 1:1 to a
	// workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKind* struct.
	Bindings *CFBindings `yaml:"bindings,omitempty"`

	// Durable Object class migrations. Required when adding/renaming/deleting
	// DO classes; otherwise the upload is rejected. Tags are applied in order.
	Migrations []CFMigration `yaml:"migrations,omitempty"`

	// Resources is the standalone IaC layer (Hyperdrive configs, Queues,
	// Vectorize indexes, AI Gateway slugs, DNS records, Zone settings).
	// Populated by the user; consumed by `nextdeploy plan` and `apply`.
	Resources *CFResources `yaml:"resources,omitempty"`
}

type CFCustomDomain struct {
	Hostname string `yaml:"hostname"`
	ZoneID   string `yaml:"zone_id,omitempty"` // optional; resolved from hostname if blank
}

type CFRoute struct {
	Pattern string `yaml:"pattern"`         // e.g. "*.example.com/*"
	Zone    string `yaml:"zone,omitempty"`  // domain name; resolved to zone ID
}

type CFTriggers struct {
	Crons []string `yaml:"crons,omitempty"` // standard cron expressions
}

type CFBindings struct {
	R2             []CFR2Binding         `yaml:"r2,omitempty"`
	Hyperdrive     []CFHyperdriveBinding `yaml:"hyperdrive,omitempty"`
	Queues         *CFQueueBindings      `yaml:"queues,omitempty"`
	Vectorize      []CFVectorizeBinding  `yaml:"vectorize,omitempty"`
	AI             []CFAIBinding         `yaml:"ai,omitempty"`
	DurableObjects []CFDOBinding         `yaml:"durable_objects,omitempty"`
	KV             []CFKVBinding         `yaml:"kv,omitempty"`
	PlainText      []CFPlainTextBinding  `yaml:"plain_text,omitempty"`
}

type CFR2Binding struct {
	Name   string `yaml:"name"`             // JS variable name (e.g. "ASSETS")
	Bucket string `yaml:"bucket,omitempty"` // R2 bucket name; auto-derived if blank
}

type CFHyperdriveBinding struct {
	Name string `yaml:"name"`           // JS variable name (e.g. "HYPERDRIVE_DB")
	ID   string `yaml:"id,omitempty"`   // CF Hyperdrive config UUID
	Ref  string `yaml:"ref,omitempty"`  // OR: name of a resource in resources.hyperdrive
}

type CFQueueBindings struct {
	Producers []CFQueueProducer `yaml:"producers,omitempty"`
	Consumers []CFQueueConsumer `yaml:"consumers,omitempty"`
}

type CFQueueProducer struct {
	Name  string `yaml:"name"`  // JS variable name
	Queue string `yaml:"queue"` // queue name
}

// CFQueueConsumer is the consumer-side wiring; lives inside the Worker upload
// metadata, not in standalone resources. The dead_letter_queue field is the
// name of another queue, NOT a separate binding.
type CFQueueConsumer struct {
	Queue            string `yaml:"queue"`
	MaxRetries       int    `yaml:"max_retries,omitempty"`
	MaxBatchSize     int    `yaml:"max_batch_size,omitempty"`
	MaxBatchTimeout  int    `yaml:"max_batch_timeout,omitempty"` // seconds
	DeadLetterQueue  string `yaml:"dead_letter_queue,omitempty"`
}

type CFVectorizeBinding struct {
	Name  string `yaml:"name"`  // JS variable name
	Index string `yaml:"index"` // Vectorize index name
}

type CFAIBinding struct {
	Name    string         `yaml:"name"`              // JS variable name (typically "AI")
	Gateway *CFAIGatewayID `yaml:"gateway,omitempty"` // optional AI Gateway routing
}

type CFAIGatewayID struct {
	ID string `yaml:"id"` // AI Gateway slug
}

type CFDOBinding struct {
	Name      string `yaml:"name"`             // JS variable name
	ClassName string `yaml:"class"`            // exported DO class
	Script    string `yaml:"script,omitempty"` // script name; defaults to self
}

type CFKVBinding struct {
	Name        string `yaml:"name"`
	NamespaceID string `yaml:"namespace_id"`
}

type CFPlainTextBinding struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type CFMigration struct {
	Tag              string         `yaml:"tag"`
	NewSQLiteClasses []string       `yaml:"new_sqlite_classes,omitempty"`
	NewClasses       []string       `yaml:"new_classes,omitempty"`
	DeletedClasses   []string       `yaml:"deleted_classes,omitempty"`
	RenamedClasses   []CFRenamedDO  `yaml:"renamed_classes,omitempty"`
	TransferredClasses []CFTransferredDO `yaml:"transferred_classes,omitempty"`
}

type CFRenamedDO struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type CFTransferredDO struct {
	From       string `yaml:"from"`
	FromScript string `yaml:"from_script"`
	To         string `yaml:"to"`
}

// CFResources is the IaC standalone layer. Each entry is a desired-state
// declaration. The plan/apply pipeline lists existing CF resources by name,
// creates missing ones, updates drifted ones (mutable fields only), and
// errors on immutable drift (e.g. Vectorize dims).
type CFResources struct {
	Hyperdrive   []CFHyperdriveResource `yaml:"hyperdrive,omitempty"`
	Queues       []CFQueueResource      `yaml:"queues,omitempty"`
	Vectorize    []CFVectorizeResource  `yaml:"vectorize,omitempty"`
	AIGateway    []CFAIGatewayResource  `yaml:"ai_gateway,omitempty"`
	DNS          []CFDNSRecord          `yaml:"dns,omitempty"`
	ZoneSettings *CFZoneSettings        `yaml:"zone_settings,omitempty"`
}

type CFHyperdriveResource struct {
	Name   string `yaml:"name"`             // pesastream-db
	Origin string `yaml:"origin,omitempty"` // postgres://… (read from env to avoid committing)
	OriginEnv string `yaml:"origin_env,omitempty"` // env var name to read origin from
}

type CFQueueResource struct {
	Name string `yaml:"name"` // whatsapp-inbound, whatsapp-inbound-dlq
}

type CFVectorizeResource struct {
	Name       string `yaml:"name"`       // openclaw-memory
	Dimensions int    `yaml:"dimensions"` // immutable after create
	Metric     string `yaml:"metric"`     // cosine|euclidean|dot-product; immutable
}

type CFAIGatewayResource struct {
	Slug string `yaml:"slug"` // pesastream
}

type CFDNSRecord struct {
	Zone    string `yaml:"zone"`              // zone name (e.g. "example.com")
	Name    string `yaml:"name"`              // "@" for apex, "*" for wildcard, or full FQDN
	Type    string `yaml:"type"`              // A | AAAA | CNAME | TXT | MX
	Content string `yaml:"content"`           // value
	TTL     int    `yaml:"ttl,omitempty"`     // seconds; 1 = auto
	Proxied bool   `yaml:"proxied,omitempty"` // CF orange-cloud
}

type CFZoneSettings struct {
	Zone string `yaml:"zone"` // zone name (e.g. "example.com")
	// MinTTL lowers the TTL of every existing DNS record in the zone whose
	// current TTL is higher than this value. Used during cutovers to reduce
	// DNS propagation time. Records with TTL=1 (CF "automatic") are skipped.
	// CF has no zone-level TTL setting, so this is implemented by iterating
	// records — affects records NOT managed by NextDeploy too.
	MinTTL int `yaml:"min_ttl,omitempty"`
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
	Profile   string `yaml:"profile,omitempty"`   // AWS CLI profile name
	AccountID string `yaml:"account_id,omitempty"` // Cloudflare Account ID
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

// ResolveTargetType returns the effective target type (vps or serverless)
// by checking the explicit field, the serverless config block, and an optional metadata fallback.
func (cfg *NextDeployConfig) ResolveTargetType(metaTarget string) string {
	if cfg.TargetType != "" {
		return cfg.TargetType
	}
	// If serverless block is present, assume serverless
	if cfg.Serverless != nil {
		return "serverless"
	}
	// Fallback to metadata if provided
	if metaTarget != "" {
		return metaTarget
	}
	// Default to VPS
	return "vps"
}
