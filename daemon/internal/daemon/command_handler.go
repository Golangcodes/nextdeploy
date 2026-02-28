package daemon

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

type CommandHandler struct {
	config         *types.DaemonConfig
	caddyManager   *CaddyManager
	processManager *ProcessManager
}

func NewCommandHandler(config *types.DaemonConfig) *CommandHandler {
	return &CommandHandler{
		config:         config,
		caddyManager:   NewCaddyManager(),
		processManager: NewProcessManager(),
	}
}

var allowedCommands = map[string]struct{}{
	"setupCaddy":    {},
	"stopdaemon":    {},
	"restartDaemon": {},
	"ship":          {},
	"secrets":       {},
	"status":        {},
	"logs":          {},
}

func (ch *CommandHandler) ValidateCommand(cmd types.Command) error {
	if _, ok := allowedCommands[cmd.Type]; !ok {
		return fmt.Errorf("command not allowed: %s", cmd.Type)
	}
	return nil
}

// HandleCommand routes a validated command to the correct handler.
func (ch *CommandHandler) HandleCommand(cmd types.Command) types.Response {
	switch cmd.Type {
	case "setupCaddy":
		return ch.setUpCaddy(cmd.Args)
	case "stopdaemon":
		return ch.stopDaemon(cmd.Args)
	case "restartDaemon":
		return ch.restartDaemon(cmd.Args)
	case "ship":
		return ch.handleShip(cmd.Args)
	case "secrets":
		return ch.handleSecrets(cmd.Args)
	case "status":
		return ch.handleStatus(cmd.Args)
	case "logs":
		return ch.handleLogs(cmd.Args)
	default:
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", cmd.Type),
		}
	}
}

func (ch *CommandHandler) stopDaemon(args map[string]interface{}) types.Response {
	log.Println("Stopping daemon...")
	ch.Shutdown()
	return types.Response{Success: true, Message: "daemon stopped"}
}

func (ch *CommandHandler) restartDaemon(args map[string]interface{}) types.Response {
	log.Println("Restarting daemon...")

	execPath, err := os.Executable()
	if err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("failed to resolve executable path: %v", err),
		}
	}

	// #nosec G204
	cmd := exec.Command(execPath, "--foreground=true", "--config="+ch.config.ConfigPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("failed to start new daemon process: %v", err),
		}
	}

	log.Printf("New daemon process started (pid %d), shutting down current...", cmd.Process.Pid)

	time.Sleep(500 * time.Millisecond)
	ch.Shutdown()

	return types.Response{Success: true, Message: "daemon restarted"}
}

func (ch *CommandHandler) Shutdown() {
	log.Println("Shutting down daemon...")
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		log.Printf("Failed to find own process: %v", err)
		return
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		log.Printf("Failed to send interrupt: %v", err)
	}
}

// =============================================================================
func (ch *CommandHandler) setUpCaddy(args map[string]interface{}) types.Response {
	setup, ok := args["setup"].(bool)
	if !ok || !setup {
		return types.Response{
			Success: false,
			Message: "setupCaddy requires 'setup: true'",
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("failed to resolve home directory: %v", err),
		}
	}
	caddyfilePath := filepath.Join(homeDir, "app", ".nextdeploy", "caddy", "Caddyfile")

	log.Printf("Reading Caddyfile from: %s", caddyfilePath)
	// #nosec G304
	caddyfileContent, err := os.ReadFile(caddyfilePath)
	if err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("failed to read Caddyfile at %s: %v", caddyfilePath, err),
		}
	}

	if err := os.WriteFile("/etc/caddy/Caddyfile", caddyfileContent, 0600); err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("failed to write /etc/caddy/Caddyfile: %v", err),
		}
	}

	if out, err := exec.Command("caddy", "reload", "--config", "/etc/caddy/Caddyfile").CombinedOutput(); err != nil {
		log.Printf("caddy reload failed (%v: %s), attempting systemctl start...", err, string(out))
		if out2, err2 := exec.Command("systemctl", "start", "caddy").CombinedOutput(); err2 != nil {
			return types.Response{
				Success: false,
				Message: fmt.Sprintf("failed to start Caddy service: %v — %s", err2, string(out2)),
			}
		}
	}

	log.Println("Caddy configured and running.")
	return types.Response{Success: true, Message: "Caddy configured and running"}
}

func (ch *CommandHandler) handleShip(args map[string]interface{}) types.Response {
	tarballPath, ok := stringArg(args, "tarball")
	if !ok {
		return types.Response{Success: false, Message: "missing 'tarball' argument"}
	}

	log.Printf("[ship] Starting deployment from: %s", tarballPath)

	tmpDir, err := os.MkdirTemp("", "nextdeploy-unpack-*")
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to create temp dir: %v", err)}
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	log.Printf("[ship] Extracting to %s...", tmpDir)
	if err := extractTarGz(tarballPath, tmpDir); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("extraction failed: %v", err)}
	}

	meta, err := readMetadata(tmpDir)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("metadata error: %v", err)}
	}

	appName := coalesce(meta.AppName, "default-app")
	domain := coalesce(meta.Domain, "localhost")
	outputMode := string(meta.OutputMode)

	log.Printf("[ship] App=%s domain=%s mode=%s pkg=%s", appName, domain, outputMode, meta.PackageManager)

	timestamp := time.Now().Unix()
	releaseDir := filepath.Join("/opt/nextdeploy/apps", appName, "releases", fmt.Sprintf("%d", timestamp))
	currentSymlink := filepath.Join("/opt/nextdeploy/apps", appName, "current")

	if err := os.MkdirAll(filepath.Dir(releaseDir), 0750); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to create releases dir: %v", err)}
	}

	if err := os.Rename(tmpDir, releaseDir); err != nil {
		if isCrossDevice(err) {
			log.Printf("[ship] Cross-device rename, falling back to copy...")
			if err := copyDir(tmpDir, releaseDir); err != nil {
				return types.Response{Success: false, Message: fmt.Sprintf("failed to copy release: %v", err)}
			}
			_ = os.RemoveAll(tmpDir)
		} else {
			return types.Response{Success: false, Message: fmt.Sprintf("failed to move release: %v", err)}
		}
	}
	cleanupTmp = false

	port, err := findFreePort()
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to allocate port: %v", err)}
	}
	log.Printf("[ship] Allocated port %d for new release", port)

	// Read the existing symlink before replacing it, for rollback purposes
	var oldReleaseTarget string
	if target, err := os.Readlink(currentSymlink); err == nil {
		oldReleaseTarget = target
	}

	_ = os.Remove(currentSymlink)
	if err := os.Symlink(releaseDir, currentSymlink); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to update current symlink: %v", err)}
	}
	log.Printf("[ship] current → %s", releaseDir)

	dopplerToken, _ := stringArg(args, "dopplerToken")
	oldServiceName := ch.processManager.CurrentServiceName()

	serviceGenerated, err := ch.processManager.GenerateServiceFile(
		appName, currentSymlink, outputMode, dopplerToken, port, meta.PackageManager,
	)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to generate service file: %v", err)}
	}

	if serviceGenerated {
		if err := ch.processManager.StartService(appName); err != nil {
			return types.Response{Success: false, Message: fmt.Sprintf("failed to start service: %v", err)}
		}
	}

	log.Printf("[ship] Waiting for app to become healthy on port %d...", port)
	if err := waitForHealthy(port, 30*time.Second); err != nil {
		_ = ch.processManager.StopService(appName)
		log.Printf("[ship] Health check failed, rolling back: %v", err)

		// Rollback logic
		_ = os.Remove(currentSymlink)
		if oldReleaseTarget != "" {
			_ = os.Symlink(oldReleaseTarget, currentSymlink)
			if oldServiceName != "" {
				_ = ch.processManager.StartService(appName)
			}
			log.Printf("[ship] Rolled back current symlink to %s", oldReleaseTarget)
		}

		return types.Response{Success: false, Message: fmt.Sprintf("health check failed: %v", err)}
	}
	log.Printf("[ship] App is healthy on port %d", port)

	if err := ch.caddyManager.GenerateConfig(appName, domain, outputMode, port, currentSymlink); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to configure Caddy: %v", err)}
	}
	if err := ch.caddyManager.EnsureMainCaddyfile(); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to write main Caddyfile: %v", err)}
	}

	// Validate before reloading
	if err := ch.caddyManager.Validate(); err != nil {
		log.Printf("[ship] Caddy validation failed: %v", err)
		return types.Response{Success: false, Message: fmt.Sprintf("Caddy config validation failed: %v", err)}
	}

	if err := ch.caddyManager.Reload(); err != nil {
		log.Printf("[ship] Warning: Caddy reload failed: %v", err)
	}

	if oldServiceName != "" {
		log.Printf("[ship] Stopping old service: %s", oldServiceName)
		if err := ch.processManager.RemoveService(oldServiceName); err != nil {
			log.Printf("[ship] Warning: failed to stop old service %s: %v", oldServiceName, err)
		}
	}

	if err := pruneReleases(appName, 5); err != nil {
		log.Printf("[ship] Warning: failed to prune old releases: %v", err)
	}

	if err := os.Remove(tarballPath); err != nil {
		log.Printf("[ship] Warning: failed to remove tarball %s: %v", tarballPath, err)
	}

	log.Printf("[ship] %s deployed successfully to %s (port %d)", appName, domain, port)
	return types.Response{
		Success: true,
		Message: fmt.Sprintf("deployed %s to %s", appName, domain),
	}
}

func extractTarGz(src, dest string) error {
	// #nosec G304
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open tarball: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("create gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		// #nosec G305
		target := filepath.Join(dest, header.Name)
		if !strings.HasPrefix(target, cleanDest) {
			// #nosec G706
			log.Printf("[extract] Skipping unsafe path: %s", header.Name)
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// #nosec G703
			if err := os.MkdirAll(target, 0750); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}

		case tar.TypeReg:
			// #nosec G115
			mode := os.FileMode(header.Mode)
			if mode&^os.ModePerm != 0 {
				mode = mode & os.ModePerm
			}
			if mode == 0 {
				mode = 0600
			}

			// #nosec G703
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}

			// #nosec G304, G703
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}

			// #nosec G110
			_, copyErr := io.CopyBuffer(outFile, tr, make([]byte, 32*1024))
			_ = outFile.Close()
			if copyErr != nil {
				return fmt.Errorf("write %s: %w", target, copyErr)
			}

		case tar.TypeSymlink:
			// #nosec G305
			linkTarget := filepath.Join(filepath.Dir(target), header.Linkname)
			if !strings.HasPrefix(filepath.Clean(linkTarget), cleanDest) {
				// #nosec G706
				log.Printf("[extract] Skipping unsafe symlink: %s -> %s", header.Name, header.Linkname)
				continue
			}
			// #nosec G703
			_ = os.Remove(target) // remove existing before creating
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("symlink %s: %w", target, err)
			}
		}
	}
	return nil
}

func readMetadata(unpackDir string) (*nextcore.NextCorePayload, error) {
	candidates := []string{
		filepath.Join(unpackDir, ".nextdeploy", "metadata.json"),
		filepath.Join(unpackDir, "metadata.json"),
	}
	for _, path := range candidates {
		// #nosec G304
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var meta nextcore.NextCorePayload
		if err := json.Unmarshal(data, &meta); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		return &meta, nil
	}
	return nil, fmt.Errorf("metadata.json not found in tarball (checked %v)", candidates)
}

func findFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

func waitForHealthy(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("app did not become healthy on %s within %s", addr, timeout)
}

func pruneReleases(appName string, keep int) error {
	releasesDir := filepath.Join("/opt/nextdeploy/apps", appName, "releases")
	entries, err := os.ReadDir(releasesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read releases dir: %w", err)
	}

	if len(entries) <= keep {
		return nil
	}

	toDelete := entries[:len(entries)-keep]
	for _, entry := range toDelete {
		path := filepath.Join(releasesDir, entry.Name())
		log.Printf("[prune] Removing old release: %s", path)
		if err := os.RemoveAll(path); err != nil {
			log.Printf("[prune] Warning: failed to remove %s: %v", path, err)
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0750)
		}
		// Preserve symlinks rather than trying to copy them as regular files.
		// Without this, symlinked directories (common in node_modules/.pnpm)
		// cause "copy_file_range: is a directory" errors.
		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	// #nosec G304
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}
	// #nosec G304
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func isCrossDevice(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid cross-device link")
}

func stringArg(args map[string]interface{}, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
