package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ProcessManager struct {
	systemdDir  string
	serviceName string
}

func NewProcessManager() *ProcessManager {
	return &ProcessManager{
		systemdDir:  "/etc/systemd/system/",
		serviceName: "nextdeploy.service",
	}
}

func (pm *ProcessManager) GenerateServiceFile(appName, projectDir, outputMode string, dopplerToken string, port int, packageManager string, releaseID string) (string, bool, error) {
	serviceName := fmt.Sprintf("nextdeploy-%s-%s.service", appName, releaseID)
	servicePath := filepath.Join(pm.systemdDir, serviceName)

	log.Printf("[process] Generating service file: %s (mode=%s, dir=%s, port=%d, pkg=%s)",
		servicePath, outputMode, projectDir, port, packageManager)

	var execStart string
	if outputMode == "standalone" {
		bin := resolveBinary("node")
		if packageManager == "bun" {
			bin = resolveBinary("bun")
		}
		cmd := fmt.Sprintf("%s server.js", bin)

		if dopplerToken != "" {
			execStart = fmt.Sprintf("%s run -- %s", resolveBinary("doppler"), cmd)
		} else {
			execStart = cmd
		}
	} else if outputMode == "default" {
		bin := resolveBinary("npm")
		args := "start"
		if packageManager == "bun" {
			bin = resolveBinary("bun")
			args = "run start"
		} else if packageManager == "yarn" {
			bin = resolveBinary("yarn")
			args = "start"
		} else if packageManager == "pnpm" {
			bin = resolveBinary("pnpm")
			args = "start"
		}
		cmd := fmt.Sprintf("%s %s", bin, args)

		if dopplerToken != "" {
			execStart = fmt.Sprintf("%s run -- %s", resolveBinary("doppler"), cmd)
		} else {
			execStart = cmd
		}
	} else if outputMode == "export" {
		log.Printf("[process] Export mode detected for %s; no systemd service needed.", appName)
		return "", false, nil
	} else {
		return "", false, fmt.Errorf("unknown or unsupported output mode: %q (check your next.config.js output setting)", outputMode)
	}

	serviceContent := fmt.Sprintf(`[Unit]
Description=NextDeploy Next.js Application (%s)
After=network.target

[Service]
Type=simple
User=nextdeploy
Group=nextdeploy
WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
Environment=NODE_ENV=production
Environment=PORT=%d
EnvironmentFile=-%s/.env.nextdeploy

# Security Sandboxing
ProtectSystem=full
ProtectHome=read-only
PrivateTmp=yes
NoNewPrivileges=yes
ReadOnlyPaths=/
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, appName, projectDir, execStart, port, projectDir, projectDir)

	if dopplerToken != "" {
		envFilePath := filepath.Join(projectDir, ".env.nextdeploy")
		envContent := fmt.Sprintf("DOPPLER_TOKEN=%s\n", dopplerToken)
		if err := os.WriteFile(envFilePath, []byte(envContent), 0600); err != nil {
			log.Printf("[process] Warning: failed to write environment file: %v", err)
		}
		_ = os.Chmod(envFilePath, 0600)
	}

	log.Printf("[process] Writing service file to %s", servicePath)
	// #nosec G301
	if err := os.MkdirAll(filepath.Dir(servicePath), 0755); err != nil {
		return "", false, fmt.Errorf("failed to create systemd dir: %w", err)
	}

	// #nosec G306
	err := os.WriteFile(servicePath, []byte(serviceContent), 0644)
	if err != nil {
		return "", false, fmt.Errorf("failed to write systemd service file %s: %w", servicePath, err)
	}

	if _, statErr := os.Stat(servicePath); statErr != nil {
		return "", false, fmt.Errorf("service file written but not found on disk: %w", statErr)
	}

	log.Printf("[process] Created systemd service file %s for %s", serviceName, appName)

	if err := pm.reloadDaemon(); err != nil {
		return "", false, fmt.Errorf("daemon-reload after writing %s: %w", serviceName, err)
	}

	time.Sleep(500 * time.Millisecond)

	return serviceName, true, nil
}

func resolveBinary(name string) string {
	candidates := map[string]string{
		"node":    "/usr/local/bin/node",
		"bun":     "/usr/local/bin/bun",
		"npm":     "/usr/local/bin/npm",
		"yarn":    "/usr/local/bin/yarn",
		"pnpm":    "/usr/local/bin/pnpm",
		"doppler": "/usr/local/bin/doppler",
	}

	if path, ok := candidates[name]; ok {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	if path, err := exec.LookPath(name); err == nil {
		log.Printf("[process] Resolved %s via PATH: %s", name, path)
		return path
	}

	log.Printf("[process] Warning: could not resolve binary %q, using name directly", name)
	return name
}

func (pm *ProcessManager) reloadDaemon() error {
	log.Printf("[process] Running systemctl daemon-reload")
	cmd := exec.Command("systemctl", "daemon-reload")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to reload systemd daemon: %v - %s", err, out)
	}
	log.Printf("[process] systemctl daemon-reload succeeded")
	return nil
}

func (pm *ProcessManager) StartService(serviceName string) error {
	log.Printf("[process] Enabling service %s", serviceName)
	// #nosec G204
	cmd := exec.Command("systemctl", "enable", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[process] Warning: failed to enable service %s: %v - %s", serviceName, err, string(out))
	}

	log.Printf("[process] Starting service %s", serviceName)
	// #nosec G204
	cmd = exec.Command("systemctl", "start", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start service %s: %v - %s", serviceName, err, string(out))
	}

	log.Printf("[process] Started systemd service %s", serviceName)
	return nil
}

func (pm *ProcessManager) StopService(serviceName string) error {
	// #nosec G204
	cmd := exec.Command("systemctl", "stop", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "not loaded") {
		log.Printf("Warning: failed to stop service %s: %s", serviceName, out)
	}
	// #nosec G204
	cmd = exec.Command("systemctl", "disable", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "not loaded") {
		log.Printf("Warning: failed to disable service %s: %s", serviceName, out)
	}

	return nil
}

func (pm *ProcessManager) CurrentServiceName() string {
	return pm.serviceName
}

func (pm *ProcessManager) RestartService(serviceName string) error {
	// #nosec G204
	cmd := exec.Command("systemctl", "restart", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to restart service %s: %v - %s", serviceName, err, out)
	}
	log.Printf("Restarted systemd service %s", serviceName)
	return nil
}

func (pm *ProcessManager) RemoveService(serviceName string) error {
	_ = pm.StopService(serviceName)
	servicePath := filepath.Join(pm.systemdDir, serviceName)
	if err := os.Remove(servicePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove service file %s: %w", servicePath, err)
	}

	return pm.reloadDaemon()
}

func (pm *ProcessManager) FindAppServices(appName string) ([]string, error) {
	files, err := os.ReadDir(pm.systemdDir)
	if err != nil {
		return nil, err
	}

	var services []string
	prefix := fmt.Sprintf("nextdeploy-%s-", appName)
	legacyName := fmt.Sprintf("nextdeploy-%s.service", appName)

	for _, f := range files {
		name := f.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".service") {
			services = append(services, name)
		} else if name == legacyName {
			services = append(services, name)
		}
	}
	return services, nil
}
