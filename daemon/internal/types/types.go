package types

type Command struct {
	Type      string                 `json:"type"`
	Args      map[string]interface{} `json:"args"`
	Signature string                 `json:"signature,omitempty"`
}

const (
	CommandTypePull   = "pull"
	CommandTypeSwitch = "switch"
	CommandTypeCaddy  = "caddy"
)

type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type DaemonConfig struct {
	SocketPath      string   `json:"socket_path"`
	SocketMode      string   `json:"socket_mode"`
	DockerSocket    string   `json:"docker_socket"`
	ContainerPrefix string   `json:"container_prefix"`
	LogLevel        string   `json:"log_level"`
	LogDir          string   `json:"log_dir"`
	LogMaxSize      int      `json:"log_max_size"`
	LogMaxBackups   int      `json:"log_max_backups"`
	ConfigPath      string   `json:"config_path"`
	SecuritySecret  string   `json:"security_secret"`
	IPWhitelist     []string `json:"ip_whitelist"`
	RateLimitRate   float64  `json:"rate_limit_rate"`
	RateLimitBurst  float64  `json:"rate_limit_burst"`
	TLSCertFile     string   `json:"tls_cert_file"`
	TLSKeyFile      string   `json:"tls_key_file"`
	TLSCAFile       string   `json:"tls_ca_file"`
	TCPListenAddr   string   `json:"tcp_listen_addr"`
}

type LoggerConfig struct {
	LogDir      string `json:"log_dir"`
	LogFileName string `json:"log_file_name"`
	MaxFileSize int    `json:"max_file_size"`
	MaxBackups  int    `json:"max_backups"`
}

type ContainerInfo struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Image   string   `json:"image"`
	Ports   []string `json:"ports"`
	Created string   `json:"created"`
}
