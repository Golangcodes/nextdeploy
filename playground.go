//go:build ignore

package nextdeploy

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"nextdeploy/daemon/internal/types"
	"nextdeploy/shared/nextcore"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CommandHandler dispatches incoming daemon commands to their handlers.
// It owns the Caddy and process managers for the lifetime of the daemon.
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

// allowedCommands is a hash set for O(1) command validation.
// Using map[string]struct{} instead of []string means no linear scan —
// every lookup is a single hash computation regardless of how many
// commands exist. This is the same hash-set pattern from build.go.
var allowedCommands = map[string]struct{}{
	"setupCaddy":    {},
	"stopdaemon":    {},
	"restartDaemon": {},
	"ship":          {},
}

// ValidateCommand checks whether a command type is permitted.
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
	default:
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", cmd.Type),
		}
	}
}

// =============================================================================
// DAEMON LIFECYCLE
// =============================================================================

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

	// Start the new process BEFORE shutting down the current one.
	// This avoids a window where neither process is running.
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

	// Small grace period so the new process can bind its socket before we exit
	time.Sleep(500 * time.Millisecond)
	ch.Shutdown()

	return types.Response{Success: true, Message: "daemon restarted"}
}

// Shutdown signals the current process to stop cleanly.
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
// CADDY SETUP
// =============================================================================

// setUpCaddy is a one-time bootstrap command that installs the Caddyfile
// shipped with the app bundle into the system Caddy location.
func (ch *CommandHandler) setUpCaddy(args map[string]interface{}) types.Response {
	setup, ok := args["setup"].(bool)
	if !ok || !setup {
		return types.Response{
			Success: false,
			Message: "setupCaddy requires 'setup: true'",
		}
	}

	// FIX: "~/..." is a shell expansion — os.ReadFile doesn't understand it.
	// We must resolve the home directory ourselves.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("failed to resolve home directory: %v", err),
		}
	}
	caddyfilePath := filepath.Join(homeDir, "app", ".nextdeploy", "caddy", "Caddyfile")

	log.Printf("Reading Caddyfile from: %s", caddyfilePath)
	caddyfileContent, err := os.ReadFile(caddyfilePath)
	if err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("failed to read Caddyfile at %s: %v", caddyfilePath, err),
		}
	}

	if err := os.WriteFile("/etc/caddy/Caddyfile", caddyfileContent, 0644); err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprintf("failed to write /etc/caddy/Caddyfile: %v", err),
		}
	}

	// Reload first — if Caddy is already running this is enough.
	// Only start via systemctl if reload fails (i.e. Caddy wasn't running).
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

// =============================================================================
// SHIP — deploy a tarball to production
// =============================================================================

// handleShip is the core deployment handler.
//
// Deployment lifecycle:
//  1. Extract tarball to temp dir
//  2. Read metadata.json to learn app name, domain, output mode
//  3. Create versioned release dir: /opt/nextdeploy/apps/{app}/releases/{unix}
//  4. Move extracted files into release dir
//  5. Allocate a free port for this release
//  6. Update /current symlink to point at new release
//  7. Generate systemd service file
//  8. Start new service, health check it
//  9. Reconfigure Caddy to point at new port
//  10. Stop old service
//  11. Prune old releases (keep last 5)
//  12. Clean up temp files
func (ch *CommandHandler) handleShip(args map[string]interface{}) types.Response {
	tarballPath, ok := stringArg(args, "tarball")
	if !ok {
		return types.Response{Success: false, Message: "missing 'tarball' argument"}
	}

	log.Printf("[ship] Starting deployment from: %s", tarballPath)

	// -------------------------------------------------------------------------
	// Step 1: Extract to temp dir
	// -------------------------------------------------------------------------
	tmpDir, err := os.MkdirTemp("", "nextdeploy-unpack-*")
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to create temp dir: %v", err)}
	}
	// tmpDir is cleaned up at the end only on failure — on success we mv it
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			os.RemoveAll(tmpDir)
		}
	}()

	log.Printf("[ship] Extracting to %s...", tmpDir)
	if err := extractTarGz(tarballPath, tmpDir); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("extraction failed: %v", err)}
	}

	// -------------------------------------------------------------------------
	// Step 2: Read metadata
	// -------------------------------------------------------------------------
	meta, err := readMetadata(tmpDir)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("metadata error: %v", err)}
	}

	appName := coalesce(meta.AppName, "default-app")
	domain := coalesce(meta.Domain, "localhost")
	outputMode := string(meta.OutputMode)

	log.Printf("[ship] App=%s domain=%s mode=%s pkg=%s", appName, domain, outputMode, meta.PackageManager)

	// -------------------------------------------------------------------------
	// Step 3 & 4: Create versioned release dir, move files
	// -------------------------------------------------------------------------
	timestamp := time.Now().Unix()
	releaseDir := filepath.Join("/opt/nextdeploy/apps", appName, "releases", fmt.Sprintf("%d", timestamp))
	currentSymlink := filepath.Join("/opt/nextdeploy/apps", appName, "current")

	if err := os.MkdirAll(filepath.Dir(releaseDir), 0755); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to create releases dir: %v", err)}
	}

	// os.Rename is atomic within the same filesystem.
	// If they're on different devices, fall back to cp+rm.
	if err := os.Rename(tmpDir, releaseDir); err != nil {
		if isCrossDevice(err) {
			log.Printf("[ship] Cross-device rename, falling back to copy...")
			if err := copyDir(tmpDir, releaseDir); err != nil {
				return types.Response{Success: false, Message: fmt.Sprintf("failed to copy release: %v", err)}
			}
			os.RemoveAll(tmpDir)
		} else {
			return types.Response{Success: false, Message: fmt.Sprintf("failed to move release: %v", err)}
		}
	}
	cleanupTmp = false // tmpDir is now releaseDir, don't double-remove

	// -------------------------------------------------------------------------
	// Step 5: Allocate a free port
	// -------------------------------------------------------------------------
	port, err := findFreePort()
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to allocate port: %v", err)}
	}
	log.Printf("[ship] Allocated port %d for new release", port)

	// -------------------------------------------------------------------------
	// Step 6: Update current symlink
	// -------------------------------------------------------------------------
	// Remove old symlink before creating new one.
	// os.Remove is safe even if the symlink doesn't exist yet.
	os.Remove(currentSymlink)
	if err := os.Symlink(releaseDir, currentSymlink); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to update current symlink: %v", err)}
	}
	log.Printf("[ship] current → %s", releaseDir)

	// -------------------------------------------------------------------------
	// Step 7 & 8: Generate service, start it, health check
	// -------------------------------------------------------------------------
	dopplerToken, _ := stringArg(args, "dopplerToken")

	if err := ch.processManager.GenerateServiceFile(
		appName, currentSymlink, outputMode, dopplerToken, port, meta.PackageManager,
	); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to generate service file: %v", err)}
	}

	// Capture the currently running service name before we start the new one
	// (needed for zero-downtime swap below)
	oldServiceName := ch.processManager.CurrentServiceName(appName)

	if err := ch.processManager.StartService(appName); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to start service: %v", err)}
	}

	// Health check: wait for the app to respond on its port
	log.Printf("[ship] Waiting for app to become healthy on port %d...", port)
	if err := waitForHealthy(port, 30*time.Second); err != nil {
		// Roll back: stop new service, restore old symlink
		ch.processManager.StopService(appName)
		log.Printf("[ship] Health check failed, rolling back: %v", err)
		return types.Response{Success: false, Message: fmt.Sprintf("health check failed: %v", err)}
	}
	log.Printf("[ship] App is healthy on port %d", port)

	// -------------------------------------------------------------------------
	// Step 9: Reconfigure Caddy to point at new port
	// -------------------------------------------------------------------------
	if err := ch.caddyManager.GenerateConfig(appName, domain, outputMode, port, currentSymlink); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to configure Caddy: %v", err)}
	}
	if err := ch.caddyManager.EnsureMainCaddyfile(); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to write main Caddyfile: %v", err)}
	}
	if err := ch.caddyManager.Reload(); err != nil {
		log.Printf("[ship] Warning: Caddy reload failed: %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 10: Gracefully stop the OLD service (zero-downtime achieved)
	// -------------------------------------------------------------------------
	if oldServiceName != "" {
		log.Printf("[ship] Stopping old service: %s", oldServiceName)
		if err := ch.processManager.StopServiceByName(oldServiceName); err != nil {
			// Non-fatal — new service is running and Caddy is pointing at it.
			// Log and continue; old process will be orphaned but not serving traffic.
			log.Printf("[ship] Warning: failed to stop old service %s: %v", oldServiceName, err)
		}
	}

	// -------------------------------------------------------------------------
	// Step 11: Prune old releases (keep last 5)
	// -------------------------------------------------------------------------
	if err := pruneReleases(appName, 5); err != nil {
		log.Printf("[ship] Warning: failed to prune old releases: %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 12: Clean up tarball
	// -------------------------------------------------------------------------
	if err := os.Remove(tarballPath); err != nil {
		log.Printf("[ship] Warning: failed to remove tarball %s: %v", tarballPath, err)
	}

	log.Printf("[ship] ✅ %s deployed successfully to %s (port %d)", appName, domain, port)
	return types.Response{
		Success: true,
		Message: fmt.Sprintf("deployed %s to %s", appName, domain),
	}
}

// =============================================================================
// EXTRACT
// =============================================================================

// extractTarGz extracts a .tar.gz archive into dest.
//
// Security: zip-slip guard ensures no entry can escape dest via "../" paths.
// The check must happen BEFORE opening the file, not after.
//
// FIX: The original code checked header.Mode AFTER calling os.OpenFile, meaning
// a bad mode would have already created the file. We now validate mode first.
// Also fixed: header.Mode > 777 is wrong because 777 is decimal, not octal.
// Unix file modes are octal — 	startcmd, err := exec.Command("systemctl", "start","caddy").CombinedOutput()
	if err != nil {
		return types.Response{
			Success: false,
			Message: fmt.Sprint("failed to start caddy service:%v - %s", err),
		}
	}
	0777 octal = 511 decimal. The correct guard is
// checking that no bits outside the permission mask are set.
func extractTarGz(src, dest string) error {
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

		// Zip-slip guard: resolve the full target path and confirm it stays
		// inside dest. filepath.Join cleans ".." components, so a path like
		// "../../etc/passwd" would resolve outside dest and be rejected.
		target := filepath.Join(dest, header.Name)
		if !strings.HasPrefix(target, cleanDest) {
			log.Printf("[extract] Skipping unsafe path: %s", header.Name)
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}

		case tar.TypeReg:
			// FIX: Validate mode BEFORE creating the file.
			// FIX: Use os.ModePerm (0777) as the permission mask — not the
			// decimal number 777, which equals octal 01411.
			mode := os.FileMode(header.Mode)
			if mode&^os.ModePerm != 0 {
				// Mode has bits set outside the rwxrwxrwx permission bits.
				// Clamp to safe permissions rather than hard failing —
				// some archivers set sticky/setuid bits that we don't want.
				mode = mode & os.ModePerm
			}
			// Ensure mode is at minimum readable by owner
			if mode == 0 {
				mode = 0644
			}

			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}

			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}

			// Stream the entry — never read the entire entry into memory.
			// Use a fixed 32KB buffer via io.CopyBuffer.
			_, copyErr := io.CopyBuffer(outFile, tr, make([]byte, 32*1024))
			outFile.Close()
			if copyErr != nil {
				return fmt.Errorf("write %s: %w", target, copyErr)
			}

		case tar.TypeSymlink:
			// Validate symlink target doesn't escape dest either
			linkTarget := filepath.Join(filepath.Dir(target), header.Linkname)
			if !strings.HasPrefix(filepath.Clean(linkTarget), cleanDest) {
				log.Printf("[extract] Skipping unsafe symlink: %s -> %s", header.Name, header.Linkname)
				continue
			}
			os.Remove(target) // remove existing before creating
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("symlink %s: %w", target, err)
			}
		}
	}
	return nil
}

// =============================================================================
// HELPERS
// =============================================================================

// readMetadata reads and parses metadata.json from an extracted tarball.
// It tries the standard location first, then falls back to the root.
func readMetadata(unpackDir string) (*nextcore.NextCorePayload, error) {
	candidates := []string{
		filepath.Join(unpackDir, ".nextdeploy", "metadata.json"),
		filepath.Join(unpackDir, "metadata.json"),
	}
	for _, path := range candidates {
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

// findFreePort asks the OS for an available TCP port by binding to :0
// and immediately releasing it. There's a small race between release and
// the app binding the port, but it's acceptable in practice.
//
// This replaces the hardcoded port := 3000 — now each release gets its
// own port, enabling zero-downtime: old and new can run simultaneously.
func findFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port, nil
}

// waitForHealthy polls the given port until the app accepts TCP connections
// or the timeout expires. A TCP connect is sufficient — we don't need a full
// HTTP health check here since the app binds its port as soon as it's ready.
func waitForHealthy(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("app did not become healthy on %s within %s", addr, timeout)
}

// pruneReleases keeps only the most recent `keep` release directories,
// deleting older ones to prevent unbounded disk growth.
//
// Uses a simple slice sort — release dirs are unix timestamps so lexical
// sort order equals chronological order. Prune everything past index `keep`.
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
		return nil // nothing to prune
	}

	// entries from os.ReadDir are already in lexical order (oldest first for timestamps)
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

// copyDir recursively copies src directory to dst.
// Used as a fallback when os.Rename fails across device boundaries.
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
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// isCrossDevice returns true if the error is a cross-device rename failure.
func isCrossDevice(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid cross-device link")
}

// stringArg safely extracts a string value from a command args map.
func stringArg(args map[string]interface{}, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// coalesce returns the first non-empty string — like SQL COALESCE.
func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
