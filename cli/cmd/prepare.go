package cmd

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

//go:embed ansible/playbooks/prepare.yml
var preparePlaybookYAML string

var (
	PrepLogs   = shared.PackageLogger("prepare", "🔧 PREPARE")
	verbose    bool
	timeout    time.Duration
	streamMode bool
)

var prepareCmd = &cobra.Command{
	Use:   "prepare",
	Short: "Prepare target server with required tools",
	Long: `Provisions the target server via Ansible: installs Node.js, Caddy, Doppler
and other required tools.

Steps:
  1. Verify server connectivity / system info
  2. Install base packages + Node.js + Bun + Caddy + Doppler
  3. Install nextdeployd daemon control plane
  4. Validate the full installation

Requires ansible-playbook to be installed on the local machine.
See: https://docs.ansible.com/ansible/latest/installation_guide/`,
	Run: runPrepare,
}

func init() {
	prepareCmd.Flags().BoolVar(&verbose, "verbose", false, "enable verbose Ansible output (-v)")
	prepareCmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "overall timeout for the preparation")
	prepareCmd.Flags().BoolVar(&streamMode, "stream", false, "stream raw Ansible output (no colour filter)")
	rootCmd.AddCommand(prepareCmd)
}

func runPrepare(cmd *cobra.Command, args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
		PrepLogs.Warn("Received interrupt signal, cancelling preparation...")
	}()

	out := cmd.OutOrStdout()

	if err := ensureAnsible(out); err != nil {
		PrepLogs.Error("Ansible setup failed: %v", err)
		os.Exit(1)
	}

	serverName, serverCfg, err := resolveTargetServer()
	if err != nil {
		PrepLogs.Error("Failed to resolve target server: %v", err)
		os.Exit(1)
	}

	_, _ = color.New(color.FgCyan).Fprintf(out, "\n Preparing server: %s (%s)\n\n", serverName, serverCfg.Host)
	PrepLogs.Info("Starting Ansible-based preparation of server %s", serverName)

	tmpDir, err := os.MkdirTemp("", "nextdeploy-prepare-*")
	if err != nil {
		PrepLogs.Error("Failed to create temp dir: %v", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	inventoryPath, err := writeInventory(tmpDir, serverName, serverCfg)
	if err != nil {
		PrepLogs.Error("Failed to write Ansible inventory: %v", err)
		os.Exit(1)
	}

	playbookPath := filepath.Join(tmpDir, "prepare.yml")
	if err := os.WriteFile(playbookPath, []byte(preparePlaybookYAML), 0600); err != nil {
		PrepLogs.Error("Failed to write playbook: %v", err)
		os.Exit(1)
	}

	if err := runAnsible(ctx, inventoryPath, playbookPath, out, verbose); err != nil {
		PrepLogs.Error("Preparation failed: %v", err)
		os.Exit(1)
	}

	_, _ = color.New(color.FgGreen, color.Bold).Fprintf(out, "\n✨  Server %s prepared successfully!\n", serverName)
	PrepLogs.Success("Server %s prepared successfully!", serverName)
}

type ansibleInstallMethod struct {
	label  string
	prereq string
	bin    string
	args   []string
}

func ansibleInstallMethods() []ansibleInstallMethod {
	methods := []ansibleInstallMethod{
		{"pipx install ansible", "pipx", "pipx", []string{"install", "ansible"}},
	}

	switch runtime.GOOS {
	case "linux":
		methods = append(methods,
			ansibleInstallMethod{"sudo apt-get install -y ansible", "apt-get", "sudo", []string{"apt-get", "install", "-y", "ansible"}},
			ansibleInstallMethod{"sudo dnf install -y ansible", "dnf", "sudo", []string{"dnf", "install", "-y", "ansible"}},
			ansibleInstallMethod{"sudo yum install -y ansible", "yum", "sudo", []string{"yum", "install", "-y", "ansible"}},
			ansibleInstallMethod{"pip3 install --user --break-system-packages ansible", "pip3", "pip3", []string{"install", "--user", "--break-system-packages", "ansible"}},
			ansibleInstallMethod{"pip install --user --break-system-packages ansible", "pip", "pip", []string{"install", "--user", "--break-system-packages", "ansible"}},
		)
	case "darwin":
		methods = append(methods,
			ansibleInstallMethod{"brew install ansible", "brew", "brew", []string{"install", "ansible"}},
			ansibleInstallMethod{"pip3 install --user ansible", "pip3", "pip3", []string{"install", "--user", "ansible"}},
		)
	default:
		methods = append(methods,
			ansibleInstallMethod{"pip3 install --user ansible", "pip3", "pip3", []string{"install", "--user", "ansible"}},
			ansibleInstallMethod{"pip install --user ansible", "pip", "pip", []string{"install", "--user", "ansible"}},
		)
	}

	return methods
}

func ensureAnsible(out io.Writer) error {
	if _, err := exec.LookPath("ansible-playbook"); err == nil {
		return nil
	}

	bold := color.New(color.Bold)
	yellow := color.New(color.FgYellow)
	red := color.New(color.FgRed)
	green := color.New(color.FgGreen)

	_, _ = bold.Fprintln(out, "⚠  ansible-playbook not found — it is required to prepare the server.")

	var chosen *ansibleInstallMethod
	for _, m := range ansibleInstallMethods() {
		m := m
		if _, err := exec.LookPath(m.prereq); err == nil {
			chosen = &m
			break
		}
	}

	if chosen == nil {
		_, _ = red.Fprintln(out, "Could not find pipx, apt-get, dnf, yum, pip3, or brew to install Ansible.")
		_, _ = yellow.Fprintln(out, "Please install Ansible manually and re-run:")
		_, _ = yellow.Fprintln(out, "https://docs.ansible.com/ansible/latest/installation_guide/")
		return fmt.Errorf("no suitable Ansible installer found")
	}

	_, _ = yellow.Fprintf(out, "   Suggested install command: %s\n\n", chosen.label)

	fmt.Fprint(out, "   Install it now? [Y/n] ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer != "" && answer != "y" && answer != "yes" {
		_, _ = red.Fprintln(out, "   Aborted. Install Ansible manually and re-run nextdeploy prepare.")
		return fmt.Errorf("user declined Ansible installation")
	}

	fmt.Fprintln(out)
	_, _ = bold.Fprintf(out, "▶  Running: %s %s\n\n", chosen.bin, strings.Join(chosen.args, " "))

	// #nosec G204
	installCmd := exec.Command(chosen.bin, chosen.args...)
	installCmd.Stdout = out
	installCmd.Stderr = out
	if err := installCmd.Run(); err != nil {
		_, _ = red.Fprintf(out, "\n Installation failed: %v\n", err)
		_, _ = yellow.Fprintln(out, "   Try running the command above manually, then re-run nextdeploy prepare.")
		return fmt.Errorf("ansible installation failed: %w", err)
	}

	if home, err := os.UserHomeDir(); err == nil {
		localBin := filepath.Join(home, ".local", "bin")
		_ = os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+localBin)
	}

	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		_, _ = red.Fprintln(out, "Ansible was installed but ansible-playbook is still not in PATH.")
		_, _ = yellow.Fprintln(out, "   Open a new terminal (or run: source ~/.bashrc) and try again.")
		return fmt.Errorf("ansible-playbook not in PATH after install")
	}

	_, _ = green.Fprintln(out, "Ansible installed successfully.")
	return nil
}

func resolveTargetServer() (string, config.ServerConfig, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", config.ServerConfig{}, fmt.Errorf("failed to load config: %w", err)
	}

	if len(cfg.Servers) == 0 {
		return "", config.ServerConfig{}, fmt.Errorf("no servers configured in nextdeploy.yml")
	}

	// Always use the first server for now as the primary target
	first := cfg.Servers[0]
	PrepLogs.Debug("Using primary server: %s (%s)", first.Name, first.Host)
	return first.Name, first, nil
}

func writeInventory(tmpDir, serverName string, cfg config.ServerConfig) (string, error) {
	port := cfg.Port
	if port == 0 {
		port = 22
	}

	var sb strings.Builder
	sb.WriteString("[target]\n")

	line := fmt.Sprintf("%s ansible_host=%s ansible_port=%d ansible_user=%s",
		serverName, cfg.Host, port, cfg.Username)

	switch {
	case cfg.KeyPath != "":
		line += fmt.Sprintf(" ansible_ssh_private_key_file=%s", cfg.KeyPath)

	case cfg.SSHKey != "":
		keyFile := filepath.Join(tmpDir, "id_ansible")
		if err := os.WriteFile(keyFile, []byte(cfg.SSHKey), 0600); err != nil {
			return "", fmt.Errorf("failed to write inline SSH key: %w", err)
		}
		line += fmt.Sprintf(" ansible_ssh_private_key_file=%s", keyFile)

	case cfg.Password != "":
		line += fmt.Sprintf(" ansible_ssh_pass=%s", cfg.Password)
	}

	sb.WriteString(line + "\n")

	inventoryPath := filepath.Join(tmpDir, "inventory.ini")
	return inventoryPath, os.WriteFile(inventoryPath, []byte(sb.String()), 0600)
}

func runAnsible(ctx context.Context, inventoryPath, playbookPath string, out io.Writer, verbose bool) error {
	args := []string{
		"-i", inventoryPath,
		playbookPath,
		"--ssh-extra-args=-o StrictHostKeyChecking=accept-new",
	}

	if verbose {
		args = append(args, "-v")
	}

	env := append(os.Environ(), "ANSIBLE_FORCE_COLOR=1")

	// #nosec G204
	ap := exec.CommandContext(ctx, "ansible-playbook", args...)
	ap.Stdout = out
	ap.Stderr = out
	ap.Env = env

	PrepLogs.Debug("Running: ansible-playbook %s", strings.Join(args, " "))

	if err := ap.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("preparation timed out or was cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("ansible-playbook exited with error: %w", err)
	}

	return nil
}

func CreateAppDirectory(ctx context.Context, serverName string, cfg config.ServerConfig, out io.Writer) error {
	const appDir = "/opt/nextjs-app"

	if _, err := exec.LookPath("ansible"); err != nil {
		return fmt.Errorf("ansible not found in PATH: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "nextdeploy-mkdir-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	inventoryPath, err := writeInventory(tmpDir, serverName, cfg)
	if err != nil {
		return fmt.Errorf("failed to write inventory: %w", err)
	}

	args := []string{
		"-i", inventoryPath,
		"target",
		"-m", "ansible.builtin.file",
		"-a", fmt.Sprintf("path=%s state=directory owner={{ ansible_user_id }} mode=0750", appDir),
		"--become",
		"--ssh-extra-args=-o StrictHostKeyChecking=accept-new",
	}

	env := append(os.Environ(), "ANSIBLE_FORCE_COLOR=1")
	// #nosec G204
	ap := exec.CommandContext(ctx, "ansible", args...)
	ap.Stdout = out
	ap.Stderr = out
	ap.Env = env

	if err := ap.Run(); err != nil {
		return fmt.Errorf("failed to create application directory %s: %w", appDir, err)
	}

	PrepLogs.Success("Application directory ready at %s", appDir)
	return nil
}
