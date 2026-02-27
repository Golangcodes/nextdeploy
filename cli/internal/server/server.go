package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	serverlogger = shared.PackageLogger("Server", "🅱)SERVERLOGGER")
)

type ServerStruct struct {
	config     *config.NextDeployConfig
	sshClients map[string]*SSHClient
	mu         sync.RWMutex
}

// SSHClient wraps SSH client and related configurations
type SSHClient struct {
	Client     *ssh.Client
	Config     *ssh.ClientConfig
	SFTPClient *sftp.Client
	LastUsed   time.Time
	mu         sync.Mutex // protects individual client operations
}

// ServerOption defines the functional option type
type ServerOption func(*ServerStruct) error

// New creates a new ServerStruct with provided options
func New(opts ...ServerOption) (*ServerStruct, error) {
	s := &ServerStruct{
		sshClients: make(map[string]*SSHClient),
	}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return s, nil
}

// WithConfig loads and applies the configuration
func WithConfig() ServerOption {
	return func(s *ServerStruct) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}
		s.config = cfg
		serverlogger.Info("Configuration loaded successfully")
		return nil
	}
}
func WithDaemon() ServerOption {
	return func(s *ServerStruct) error {
		serverlogger.Debug("Writing NextDeploy Agent connection here")
		return nil
	}
}

// WithSSH initializes SSH connections for all configured servers
func WithSSH() ServerOption {
	return func(s *ServerStruct) error {
		if s.config == nil || len(s.config.Servers) == 0 {
			return fmt.Errorf("the server configuration is not loaded or no servers configured")
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		var wg sync.WaitGroup
		errChan := make(chan error, len(s.config.Servers))
		serverlogger.Info("The config.Servers value at with withssh function is :%v", s.config.Servers)

		for _, serverCfg := range s.config.Servers {
			wg.Add(1)
			go func(cfg config.ServerConfig) {
				defer wg.Done()
				client, err := connectSSH(cfg)
				if err != nil {
					errChan <- fmt.Errorf("server %s: %w", cfg.Name, err)
					return
				}
				s.sshClients[cfg.Name] = client
				serverlogger.Info("Successfully connected to server %s (%s)", cfg.Name, cfg.Host)
			}(serverCfg)
		}

		wg.Wait()
		close(errChan)

		var errs []error
		for err := range errChan {
			errs = append(errs, err)
		}

		if len(errs) > 0 {
			serverlogger.Debug("errs look like this %s:", errs)
			return fmt.Errorf("failed to connect to some servers: %v", errs)
		}
		return nil
	}
}

func (s *ServerStruct) GetDeploymentServer() (string, error) {
	if s.config == nil || s.config.Deployment.Server.Host == "" {
		return "", fmt.Errorf("deployment server configuration is not set")
	}

	deploymentTarget := s.config.Deployment.Server.Host

	// find the matching server in servers list
	for _, server := range s.config.Servers {
		// Check if deployment target matches either server name OR host IP
		if server.Name == deploymentTarget || server.Host == deploymentTarget {
			_, err := connectSSH(server)
			if err != nil {
				return "", fmt.Errorf("failed to connect to deployment server %s (%s): %w",
					server.Name, server.Host, err)
			}
			return server.Name, nil
		}
	}
	return "", fmt.Errorf("deployment server %s not found in configuration (searched by name and host)",
		deploymentTarget)
}
func AddHostToKnownHosts(ip string, knownHostsPath string) error {
	// Validate IP/hostname
	if net.ParseIP(ip) == nil && !isValidHostname(ip) {
		return fmt.Errorf("invalid IP address or hostname: %s", ip)
	}

	// Set default known_hosts path if not provided
	if knownHostsPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		knownHostsPath = filepath.Join(home, ".ssh", "known_hosts")
	}

	// Create .ssh directory if it doesn't exist
	sshDir := filepath.Dir(knownHostsPath)
	if _, err := os.Stat(sshDir); os.IsNotExist(err) {
		if err := os.Mkdir(sshDir, 0700); err != nil {
			return fmt.Errorf("failed to create .ssh directory: %v", err)
		}
	}

	// #nosec G204
	// Get the host key using ssh-keyscan
	cmd := exec.Command("ssh-keyscan", ip)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("ssh-keyscan failed: %v", err)
	}

	hostKey := strings.TrimSpace(out.String())
	if hostKey == "" {
		return fmt.Errorf("no host key returned for %s", ip)
	}

	// #nosec G304
	// Append to known_hosts file
	f, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open known_hosts file: %v", err)
	}
	defer f.Close()

	if _, err := f.WriteString(hostKey + "\n"); err != nil {
		return fmt.Errorf("failed to write to known_hosts file: %v", err)
	}

	return nil
}

// Helper function to validate hostnames
func isValidHostname(hostname string) bool {
	if len(hostname) > 253 {
		return false
	}
	for _, part := range strings.Split(hostname, ".") {
		if len(part) > 63 {
			return false
		}
	}
	return true
}

// connectSSH establishes an SSH connection and initializes SFTP client
func connectSSH(cfg config.ServerConfig) (*SSHClient, error) {
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	authMethods, err := getAuthMethods(cfg)
	if err != nil {
		return nil, err
	}

	hostKeyCallback, err := getHostKeyCallback()
	if err != nil {
		return nil, err
	}

	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
		Config: ssh.Config{
			KeyExchanges: []string{"curve25519-sha256@libssh.org"},
			Ciphers:      []string{"chacha20-poly1305@openssh.com"},
		},
	}

	if len(authMethods) == 0 {
		sshConfig.Auth = []ssh.AuthMethod{authMethods[0]}
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to create SFTP client: %w", err)
	}

	return &SSHClient{
		Client:     client,
		Config:     sshConfig,
		SFTPClient: sftpClient,
		LastUsed:   time.Now(),
	}, nil
}

func getAuthMethods(cfg config.ServerConfig) ([]ssh.AuthMethod, error) {
	var authMethods []ssh.AuthMethod

	if cfg.KeyPath == "" {
		return nil, fmt.Errorf("no SSH key path provided for server %s", cfg.Name)
	}

	// Handle path expansion
	expandedPath := cfg.KeyPath
	if strings.HasPrefix(expandedPath, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		expandedPath = filepath.Join(home, expandedPath[1:])
	}
	expandedPath = os.ExpandEnv(expandedPath)

	serverlogger.Debug("Key path resolution: %s -> %s", cfg.KeyPath, expandedPath)

	// #nosec G304
	key, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key file %s (resolved to %s): %w",
			cfg.KeyPath, expandedPath, err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok && cfg.KeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(cfg.KeyPassphrase))
			if err != nil {
				return nil, fmt.Errorf("failed to parse SSH private key with passphrase: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to parse SSH private key: %w", err)
		}
	}
	authMethods = append(authMethods, ssh.PublicKeys(signer))

	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no authentication methods provided for server %s", cfg.Name)
	}

	return authMethods, nil
}
func getHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	knownHostsPath := filepath.Join(home, ".ssh", "known_hosts")

	// Create file if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0700); err != nil {
		return nil, err
	}
	// #nosec G304
	f, err := os.OpenFile(knownHostsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()

	initialCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create known hosts callback: %w", err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := initialCallback(hostname, remote, key)
		if err != nil {
			var keyErr *knownhosts.KeyError
			if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
				// Key is unknown. Trust on first use.
				serverlogger.Info("Adding unknown host %s to known_hosts automatically", hostname)
				// #nosec G304
				f, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					return err
				}
				defer f.Close()
				knownHostsLine := knownhosts.Line([]string{hostname}, key)
				if _, err := f.WriteString(knownHostsLine + "\n"); err != nil {
					return err
				}
				return nil
			}
			serverlogger.Error("Host key verification failed for %s: %v", hostname, err)
			return err
		}
		return nil
	}, nil
}

func (s *ServerStruct) BasicCaddySetup(ctx context.Context, serverName string, stream io.Writer) error {

	return nil

}

// ExecuteCommand runs a command on the specified server with context support and streaming
func (s *ServerStruct) ExecuteCommand(ctx context.Context, serverName, command string, stream io.Writer) (string, error) {
	client, err := s.getSSHClient(serverName)
	if err != nil {
		return "", err
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	session, err := client.Client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Set up output pipes
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	output := &bytes.Buffer{}

	var stdoutWriters []io.Writer
	stdoutWriters = append(stdoutWriters, output) // always capture stdout
	if stream != nil {
		stdoutWriters = append(stdoutWriters, stream)
	}
	stdoutMulti := io.MultiWriter(stdoutWriters...)

	var stderrDst io.Writer = io.Discard
	if stream != nil {
		stderrDst = stream
	}

	// Start command
	err = session.Start(command)
	if err != nil {
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	// Stream output in goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(stdoutMulti, stdoutPipe)
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(stderrDst, stderrPipe)
	}()

	// Set up context cancellation
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Signal(ssh.SIGKILL)
		case <-done:
		}
	}()

	// Wait for command completion
	err = session.Wait()
	close(done)
	wg.Wait()

	client.LastUsed = time.Now()

	if err != nil {
		return output.String(), fmt.Errorf("command failed: %w", err)
	}

	serverlogger.Debug("Executed command on %s: %s", serverName, command)
	return output.String(), nil
}

// UploadFile uploads a file to the remote server using SFTP
func (s *ServerStruct) UploadFile(ctx context.Context, serverName, localPath, remotePath string) error {
	client, err := s.getSSHClient(serverName)
	if err != nil {
		return err
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	// #nosec G304
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer localFile.Close()

	remoteFile, err := client.SFTPClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer remoteFile.Close()

	_, err = io.Copy(remoteFile, localFile)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	client.LastUsed = time.Now()
	serverlogger.Info("Uploaded %s to %s:%s", localPath, serverName, remotePath)
	return nil
}

// DownloadFile downloads a file from the remote server using SFTP
func (s *ServerStruct) DownloadFile(ctx context.Context, serverName, remotePath, localPath string) error {
	client, err := s.getSSHClient(serverName)
	if err != nil {
		return err
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	remoteFile, err := client.SFTPClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer remoteFile.Close()

	// #nosec G304
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer localFile.Close()

	_, err = io.Copy(localFile, remoteFile)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	client.LastUsed = time.Now()
	serverlogger.Info("Downloaded %s:%s to %s", serverName, remotePath, localPath)
	return nil
}

// PingServer checks if the server is reachable
func (s *ServerStruct) PingServer(serverName string) error {
	client, err := s.getSSHClient(serverName)
	if err != nil {
		return err
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	session, err := client.Client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	_, err = session.CombinedOutput("echo ping")
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}

	client.LastUsed = time.Now()
	return nil
}

// CloseSSHConnections closes all active SSH connections
func (s *ServerStruct) CloseSSHConnection() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	for name, client := range s.sshClients {
		client.mu.Lock()
		if client.SFTPClient != nil {
			if err := client.SFTPClient.Close(); err != nil {
				errs = append(errs, fmt.Errorf("error closing SFTP client for %s: %w", name, err))
			}
		}
		if err := client.Client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("error closing SSH client for %s: %w", name, err))
		}
		client.mu.Unlock()
		delete(s.sshClients, name)
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors while closing connections: %v", errs)
	}
	return nil
}

// getSSHClient safely retrieves an SSH client from the map
func (s *ServerStruct) getSSHClient(serverName string) (*SSHClient, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	client, ok := s.sshClients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %s not found", serverName)
	}
	return client, nil
}

// Reconnect re-establishes connection to a server
func (s *ServerStruct) Reconnect(serverName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.config == nil {
		return fmt.Errorf("configuration not loaded")
	}

	var serverCfg *config.ServerConfig
	for _, cfg := range s.config.Servers {
		if cfg.Name == serverName {
			serverCfg = &cfg
			break
		}
	}

	if serverCfg == nil {
		return fmt.Errorf("server configuration not found for %s", serverName)
	}

	// Close existing connection if it exists
	if oldClient, ok := s.sshClients[serverName]; ok {
		oldClient.mu.Lock()
		if oldClient.SFTPClient != nil {
			_ = oldClient.SFTPClient.Close()
		}
		_ = oldClient.Client.Close()
		oldClient.mu.Unlock()
	}

	client, err := connectSSH(*serverCfg)
	if err != nil {
		return fmt.Errorf("failed to reconnect to %s: %w", serverName, err)
	}

	s.sshClients[serverName] = client
	serverlogger.Info("Reconnected to server %s", serverName)
	return nil
}

// ListServers returns a list of configured server names
func (s *ServerStruct) ListServers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var servers []string
	for name := range s.sshClients {
		servers = append(servers, name)
	}
	return servers
}

// GetServerStatus returns connection status of a server
func (s *ServerStruct) GetServerStatus(serverName string, stream io.Writer) (string, error) {
	client, err := s.getSSHClient(serverName)
	if err != nil {
		return "", err
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	session, err := client.Client.NewSession()
	if err != nil {
		return "disconnected", nil
	}
	_ = session.Close()

	uptime, err := s.ExecuteCommand(context.Background(), serverName, "uptime", stream)
	if err != nil {
		return "connected but command failed", nil
	}

	return fmt.Sprintf("connected (uptime: %s)", uptime), nil
}
