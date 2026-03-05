package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

const (
	baseDir       = "/opt/nextdeploy"
	appsDir       = "/opt/nextdeploy/apps"
	uploadsDir    = "/opt/nextdeploy/uploads"
	workTmpDir    = "/opt/nextdeploy/tmp"
	nextdeployDir = ".nextdeploy"
)

type CommandHandler struct {
	config         *types.DaemonConfig
	caddyManager   *CaddyManager
	processManager *ProcessManager
	stateManager   *StateManager
	auditLogger    *AuditLogger
	rateLimiter    *RateLimiter
}

func NewCommandHandler(config *types.DaemonConfig) *CommandHandler {
	statePath := "/var/lib/nextdeployd/state.json"
	if os.Geteuid() != 0 {
		home, _ := os.UserHomeDir()
		statePath = filepath.Join(home, ".nextdeploy", "state.json")
	}

	auditPath := filepath.Join(config.LogDir, "audit.log")

	rate := config.RateLimitRate
	if rate == 0 {
		rate = 10 // default 10 requests per second
	}
	burst := config.RateLimitBurst
	if burst == 0 {
		burst = 20
	}

	return &CommandHandler{
		config:         config,
		caddyManager:   NewCaddyManager(),
		processManager: NewProcessManager(),
		stateManager:   NewStateManager(statePath),
		auditLogger:    NewAuditLogger(auditPath),
		rateLimiter:    NewRateLimiter(rate, burst),
	}
}

var allowedCommands = map[string]struct{}{
	"setupCaddy":    {},
	"stopdaemon":    {},
	"restartDaemon": {},
	"ship":          {},
	"rollback":      {},
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

func (ch *CommandHandler) HandleCommand(cmd types.Command, clientIdentity string) types.Response {
	// 1. Rate Limiting
	if !ch.rateLimiter.Allow(clientIdentity) {
		return types.Response{Success: false, Message: "rate limit exceeded"}
	}

	// 2. IP Whitelisting (for non-Unix socket identities)
	if !IsIPAllowed(clientIdentity, ch.config.IPWhitelist) {
		return types.Response{Success: false, Message: "IP not whitelisted"}
	}

	// 3. Signature Verification
	// We marshal the type and args to verify the signature
	payload, _ := json.Marshal(map[string]interface{}{
		"type": cmd.Type,
		"args": cmd.Args,
	})
	if !VerifySignature(payload, cmd.Signature, ch.config.SecuritySecret) {
		return types.Response{Success: false, Message: "invalid command signature"}
	}

	var resp types.Response
	switch cmd.Type {
	case "setupCaddy":
		resp = ch.setUpCaddy(cmd.Args)
	case "stopdaemon":
		resp = ch.stopDaemon(cmd.Args)
	case "restartDaemon":
		resp = ch.restartDaemon(cmd.Args)
	case "ship":
		resp = ch.handleShip(cmd.Args)
	case "rollback":
		resp = ch.handleRollback(cmd.Args)
	case "secrets":
		resp = ch.handleSecrets(cmd.Args)
	case "status":
		resp = ch.handleStatus(cmd.Args)
	case "logs":
		resp = ch.handleLogs(cmd.Args)
	default:
		resp = types.Response{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", cmd.Type),
		}
	}

	// 4. Audit Logging
	ch.auditLogger.Log(AuditEntry{
		CommandType:    cmd.Type,
		ClientIdentity: clientIdentity,
		Result:         fmt.Sprintf("%v", resp.Success),
		ErrorDetails:   resp.Message,
		Args:           cmd.Args,
	})

	return resp
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

	// Minimal delay to let the new process bind the socket if necessary
	time.Sleep(100 * time.Millisecond)
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
	caddyfilePath := filepath.Join(homeDir, "app", nextdeployDir, "caddy", "Caddyfile")

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

	systemctl := resolveTool("systemctl")
	if out, err := exec.Command("caddy", "reload", "--config", "/etc/caddy/Caddyfile").CombinedOutput(); err != nil {
		log.Printf("caddy reload failed (%v: %s), attempting systemctl start...", err, string(out))
		// #nosec G204
		if out2, err2 := exec.Command(systemctl, "start", "caddy").CombinedOutput(); err2 != nil {
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
	tarballPath, ok := StringArg(args, "tarball")
	if !ok {
		return types.Response{Success: false, Message: "missing 'tarball' argument"}
	}

	// Path sanitization
	tarballPath = filepath.Clean(tarballPath)
	if !strings.HasPrefix(tarballPath, uploadsDir) {
		return types.Response{Success: false, Message: "security error: tarball path must be within uploads directory"}
	}

	log.Printf("[ship] Starting deployment from: %s", tarballPath)

	// Ensure workTmpDir exists
	if err := os.MkdirAll(workTmpDir, 0750); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to ensure tmp dir: %v", err)}
	}

	tmpDir, err := os.MkdirTemp(workTmpDir, "unpack-*")
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to create temp dir in %s: %v", workTmpDir, err)}
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	log.Printf("[ship] Extracting to %s...", tmpDir)
	if err := shared.ExtractTarGz(tarballPath, tmpDir); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("extraction failed: %v", err)}
	}

	meta, err := readMetadata(tmpDir)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("metadata error: %v", err)}
	}

	appName := Coalesce(meta.AppName, "default-app")
	if err := validateAppName(appName); err != nil {
		return types.Response{Success: false, Message: err.Error()}
	}

	domain := Coalesce(meta.Domain, "localhost")
	if err := validateDomain(domain); err != nil {
		return types.Response{Success: false, Message: err.Error()}
	}

	outputMode := string(meta.OutputMode)

	log.Printf("[ship] App=%s domain=%s mode=%s pkg=%s", appName, domain, outputMode, meta.PackageManager)

	timestamp := time.Now().Unix()
	releaseID := fmt.Sprintf("%d", timestamp)
	releaseDir := filepath.Join(appsDir, appName, "releases", releaseID)

	// #nosec G301 G703
	if err := os.MkdirAll(filepath.Dir(releaseDir), 0750); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to create releases dir: %v", err)}
	}

	// Ensure the app directory is owned by nextdeploy
	ch.ensureAppDirOwnership(appName)

	log.Printf("[ship] Moving %s -> %s", tmpDir, releaseDir)
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

	// Fix permissions and ownership for the release directory
	ch.ensureDirPermissions(releaseDir)

	dopplerToken, _ := StringArg(args, "dopplerToken")
	log.Printf("[ship] %s extracted to %s, activating...", appName, releaseDir)

	ctx := ReleaseContext{
		AppName:          appName,
		Domain:           domain,
		ReleaseDir:       releaseDir,
		ReleaseID:        releaseID,
		OutputMode:       outputMode,
		DopplerToken:     dopplerToken,
		PackageManager:   meta.PackageManager,
		TarballPath:      tarballPath,
		DetectedFeatures: meta.DetectedFeatures,
		DistDir:          meta.DistDir,
		ExportDir:        meta.ExportDir,
	}
	return ch.activateRelease(ctx)
}

type ReleaseContext struct {
	AppName          string
	Domain           string
	ReleaseDir       string
	ReleaseID        string
	OutputMode       string
	DopplerToken     string
	PackageManager   string
	TarballPath      string
	DetectedFeatures *nextcore.DetectedFeatures
	DistDir          string
	ExportDir        string
}

func (ch *CommandHandler) activateRelease(ctx ReleaseContext) types.Response {
	currentSymlink := filepath.Join(appsDir, ctx.AppName, "current")

	// Port selection with persistence
	port := ch.stateManager.GetPort(ctx.AppName)
	if port == 0 || !isPortAvailable(port) {
		p, closePort, err := findFreePort()
		if err != nil {
			return types.Response{Success: false, Message: fmt.Sprintf("failed to allocate port: %v", err)}
		}
		_ = closePort()
		port = p
		ch.stateManager.SetPort(ctx.AppName, port)
		if err := ch.stateManager.Save(); err != nil {
			log.Printf("[activate] Warning: failed to save state: %v", err)
		}
	}

	log.Printf("[activate] Allocated port %d for release %s", port, ctx.ReleaseID)

	// Port file for Caddy or other discovery tools
	portFilePath := filepath.Join(appsDir, ctx.AppName, "port")
	if err := os.WriteFile(portFilePath, []byte(fmt.Sprintf("%d", port)), 0644); err != nil {
		log.Printf("[activate] Warning: failed to write port file to %s: %v", portFilePath, err)
	}

	serviceName, serviceGenerated, err := ch.processManager.GenerateServiceFile(
		ctx.AppName, ctx.ReleaseDir, ctx.OutputMode, ctx.DopplerToken, port, ctx.PackageManager, ctx.ReleaseID,
	)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to generate service file: %v", err)}
	}

	if serviceGenerated {
		if err := ch.processManager.StartService(serviceName); err != nil {
			return types.Response{Success: false, Message: fmt.Sprintf("failed to start service: %v", err)}
		}
	}

	log.Printf("[activate] Waiting for app to become healthy on port %d...", port)
	if err := waitForHealthy(port, 5*time.Minute); err != nil {
		if serviceGenerated {
			_ = ch.processManager.RemoveService(serviceName)
		}
		return types.Response{Success: false, Message: fmt.Sprintf("health check failed after 5m: %v", err)}
	}

	// Atomic symlink update
	tmpSymlink := currentSymlink + ".tmp"
	_ = os.Remove(tmpSymlink)
	if err := os.Symlink(ctx.ReleaseDir, tmpSymlink); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to create atomic symlink: %v", err)}
	}
	if err := os.Rename(tmpSymlink, currentSymlink); err != nil {
		_ = os.Remove(tmpSymlink)
		return types.Response{Success: false, Message: fmt.Sprintf("failed to rename atomic symlink: %v", err)}
	}

	if err := ch.caddyManager.GenerateConfig(ctx.AppName, ctx.Domain, ctx.OutputMode, port, currentSymlink, ctx.DetectedFeatures, ctx.DistDir, ctx.ExportDir); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to configure Caddy: %v", err)}
	}
	_ = ch.caddyManager.EnsureMainCaddyfile()
	if err := ch.caddyManager.Validate(); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("Caddy validation failed: %v", err)}
	}
	_ = ch.caddyManager.Reload()

	if services, err := ch.processManager.FindAppServices(ctx.AppName); err == nil {
		for _, s := range services {
			if s != serviceName {
				log.Printf("[activate] Cleaning up old service: %s", s)
				_ = ch.processManager.RemoveService(s)
			}
		}
	}

	if err := pruneReleases(ctx.AppName, 5); err != nil {
		log.Printf("[activate] Warning: failed to prune releases: %v", err)
	}

	if ctx.TarballPath != "" {
		_ = os.Remove(ctx.TarballPath)
	}

	return types.Response{
		Success: true,
		Message: fmt.Sprintf("Successfully activated release %s for %s", ctx.ReleaseID, ctx.AppName),
	}
}

func (ch *CommandHandler) handleRollback(args map[string]interface{}) types.Response {
	appName, ok := StringArg(args, "appName")
	if !ok {
		return types.Response{Success: false, Message: "missing 'appName' argument"}
	}
	if err := validateAppName(appName); err != nil {
		return types.Response{Success: false, Message: err.Error()}
	}

	releasesDir := filepath.Join(appsDir, appName, "releases")
	entries, err := os.ReadDir(releasesDir)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to read releases: %v", err)}
	}

	var releases []string
	for _, e := range entries {
		if e.IsDir() {
			releases = append(releases, e.Name())
		}
	}

	if len(releases) < 2 {
		return types.Response{Success: false, Message: "not enough releases to rollback"}
	}

	sort.Strings(releases)
	previousReleaseID := releases[len(releases)-2]
	previousReleaseDir := filepath.Join(releasesDir, previousReleaseID)

	meta, err := readMetadata(previousReleaseDir)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to read metadata of previous release: %v", err)}
	}

	domain := Coalesce(meta.Domain, "localhost")
	outputMode := string(meta.OutputMode)
	dopplerToken, _ := StringArg(args, "dopplerToken")

	log.Printf("[rollback] Reverting %s to release %s", appName, previousReleaseID)

	ctx := ReleaseContext{
		AppName:          appName,
		Domain:           domain,
		ReleaseDir:       previousReleaseDir,
		ReleaseID:        previousReleaseID,
		OutputMode:       outputMode,
		DopplerToken:     dopplerToken,
		PackageManager:   meta.PackageManager,
		TarballPath:      "",
		DetectedFeatures: meta.DetectedFeatures,
		DistDir:          meta.DistDir,
		ExportDir:        meta.ExportDir,
	}
	return ch.activateRelease(ctx)
}

// ExtractTarGz is now handled universally by shared.ExtractTarGz

func readMetadata(unpackDir string) (*nextcore.NextCorePayload, error) {
	candidates := []string{
		filepath.Join(unpackDir, nextdeployDir, "metadata.json"),
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

func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func findFreePort() (int, func() error, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, fmt.Errorf("failed to find free port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return port, ln.Close, nil
}

func waitForHealthy(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)

	backoff := 100 * time.Millisecond
	maxBackoff := 2 * time.Second

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}

		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return fmt.Errorf("app did not become healthy on %s within %s", addr, timeout)
}

func pruneReleases(appName string, keep int) error {
	releasesDir := filepath.Join(appsDir, appName, "releases")
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
			// #nosec G301 G703
			return os.MkdirAll(target, 0750)
		}
		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			// #nosec G301 G703
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

	// #nosec G301 G703
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}
	// #nosec G304 G703
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

func (ch *CommandHandler) ensureAppDirOwnership(appName string) {
	appDir := filepath.Join(appsDir, appName)
	ch.ensureDirPermissions(appDir)
}

func (ch *CommandHandler) ensureDirPermissions(root string) {
	findPath := resolveTool("find")
	chownPath := resolveTool("chown")
	chmodPath := resolveTool("chmod")

	// Optimization: Skip chown if already correct.
	// #nosec G204
	chownCmd := exec.Command(findPath, root, "(", "!", "-user", "nextdeploy", "-o", "!", "-group", "nextdeploy", ")", "-exec", chownPath, "nextdeploy:nextdeploy", "{}", "+")
	if out, err := chownCmd.CombinedOutput(); err != nil {
		log.Printf("[ship] Warning: optimized chown failed: %v - %s", err, string(out))
		// #nosec G204
		_ = exec.Command(chownPath, "-R", "nextdeploy:nextdeploy", root).Run()
	}

	// #nosec G204
	chmodDirCmd := exec.Command(findPath, root, "-type", "d", "!", "-perm", "0750", "-exec", chmodPath, "0750", "{}", "+")
	if out, err := chmodDirCmd.CombinedOutput(); err != nil {
		log.Printf("[ship] Warning: failed to chmod dirs in %s: %v - %s", root, err, string(out))
	}

	// #nosec G204
	chmodFileCmd := exec.Command(findPath, root, "-type", "f", "!", "-perm", "0644", "-exec", chmodPath, "0644", "{}", "+")
	if out, err := chmodFileCmd.CombinedOutput(); err != nil {
		log.Printf("[ship] Warning: failed to chmod files in %s: %v - %s", root, err, string(out))
	}
}
