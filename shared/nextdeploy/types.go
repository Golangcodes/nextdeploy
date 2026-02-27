package nextdeploy

type Config struct {
	Version    string       `yaml:"version"`
	App        AppConfig    `yaml:"app"`
	Repository Repository   `yaml:"repository"`
	Docker     DockerConfig `yaml:"docker"`
	Deployment Deployment   `yaml:"deployment"`
	Database   Database     `yaml:"database"`
	Logging    Logging      `yaml:"logging"`
	Monitoring Monitoring   `yaml:"monitoring"`
	Backup     Backup       `yaml:"backup"`
	SSL        SSL          `yaml:"ssl"`
	Webhook    Webhook      `yaml:"webhook"`
}

// AppConfig represents application metadata
type AppConfig struct {
	Name        string `yaml:"name"`
	Environment string `yaml:"environment"`
	Domain      string `yaml:"domain"`
	Port        int    `yaml:"port"`
}

// Repository represents Git repository settings
type Repository struct {
	URL           string `yaml:"url"`
	Branch        string `yaml:"branch"`
	AutoDeploy    bool   `yaml:"auto_deploy"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// DockerConfig represents Docker build configuration
type DockerConfig struct {
	Build    DockerBuild `yaml:"build"`
	Image    string      `yaml:"image"`
	Registry string      `yaml:"registry"`
	Push     bool        `yaml:"push"`
}

type DockerBuild struct {
	Context    string            `yaml:"context"`
	Dockerfile string            `yaml:"dockerfile"`
	Args       map[string]string `yaml:"args"`
	NoCache    bool              `yaml:"no_cache"`
}

// Deployment represents deployment target configuration
type Deployment struct {
	Server    Server    `yaml:"server"`
	Container Container `yaml:"container"`
}

type Server struct {
	Host    string `yaml:"host"`
	User    string `yaml:"user"`
	SSHKey  string `yaml:"ssh_key"`
	UseSudo bool   `yaml:"use_sudo"`
}

type Container struct {
	Name        string               `yaml:"name"`
	Restart     string               `yaml:"restart"`
	EnvFile     string               `yaml:"env_file"`
	Volumes     []string             `yaml:"volumes"`
	Ports       []string             `yaml:"ports"`
	Healthcheck ContainerHealthcheck `yaml:"healthcheck"`
}

type ContainerHealthcheck struct {
	Path     string `yaml:"path"`
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
	Retries  int    `yaml:"retries"`
}

// Database represents database configuration
type Database struct {
	Type            string `yaml:"type"`
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	Username        string `yaml:"username"`
	// #nosec G117
	Password        string `yaml:"password"`
	Name            string `yaml:"name"`
	MigrateOnDeploy bool   `yaml:"migrate_on_deploy"`
}

// Logging represents logging configuration
type Logging struct {
	Enabled    bool   `yaml:"enabled"`
	Provider   string `yaml:"provider"`
	StreamLogs bool   `yaml:"stream_logs"`
	LogPath    string `yaml:"log_path"`
}

// Monitoring represents monitoring configuration
type Monitoring struct {
	Enabled         bool  `yaml:"enabled"`
	CPUThreshold    int   `yaml:"cpu_threshold"`
	MemoryThreshold int   `yaml:"memory_threshold"`
	DiskThreshold   int   `yaml:"disk_threshold"`
	Alert           Alert `yaml:"alert"`
}

type Alert struct {
	Email        string   `yaml:"email"`
	SlackWebhook string   `yaml:"slack_webhook"`
	NotifyOn     []string `yaml:"notify_on"`
}

// Backup represents backup configuration
type Backup struct {
	Enabled       bool    `yaml:"enabled"`
	Frequency     string  `yaml:"frequency"`
	RetentionDays int     `yaml:"retention_days"`
	Storage       Storage `yaml:"storage"`
}

type Storage struct {
	Provider  string `yaml:"provider"`
	Bucket    string `yaml:"bucket"`
	Region    string `yaml:"region"`
	// #nosec G117
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
}

// SSL represents SSL configuration
type SSL struct {
	Enabled   bool   `yaml:"enabled"`
	Provider  string `yaml:"provider"`
	Email     string `yaml:"email"`
	AutoRenew bool   `yaml:"auto_renew"`
}

// Webhook represents webhook configuration
type Webhook struct {
	OnSuccess []string `yaml:"on_success"`
	OnFailure []string `yaml:"on_failure"`
}
