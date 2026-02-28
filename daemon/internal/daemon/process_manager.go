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

func (pm *ProcessManager) GenerateServiceFile(appName, projectDir, outputMode string, dopplerToken string, port int, packageManager string) (bool, error) {
	serviceName := fmt.Sprintf("nextdeploy-%s.service", appName)
	servicePath := filepath.Join(pm.systemdDir, serviceName)

	log.Printf("[process] Generating service file: %s (mode=%s, dir=%s, port=%d, pkg=%s)",
		servicePath, outputMode, projectDir, port, packageManager)

	var execStart string
	if outputMode == "standalone" {
		cmd := "node server.js"
		if packageManager == "bun" {
			cmd = "bun server.js"
		}
		if dopplerToken != "" {
			execStart = fmt.Sprintf("doppler run --token=%s -- %s", dopplerToken, cmd)
		} else {
			execStart = cmd
		}
	} else if outputMode == "default" {
		cmd := "npm start"
		if packageManager == "bun" {
			cmd = "bun run start"
		} else if packageManager == "yarn" {
			cmd = "yarn start"
		} else if packageManager == "pnpm" {
			cmd = "pnpm start"
		}
		if dopplerToken != "" {
			execStart = fmt.Sprintf("doppler run --token=%s -- %s", dopplerToken, cmd)
		} else {
			execStart = cmd
		}
	} else if outputMode == "export" {
		// export mode doesn't need a process
		log.Printf("[process] Export mode detected for %s; no systemd service needed.", appName)
		return false, nil
	} else {
		return false, fmt.Errorf("unknown or unsupported output mode: %q (check your next.config.js output setting)", outputMode)
	}

	serviceContent := fmt.Sprintf(`[Unit]
Description=NextDeploy Next.js Application (%s)
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
Environment=NODE_ENV=production
Environment=PORT=%d

[Install]
WantedBy=multi-user.target
`, appName, projectDir, execStart, port)

	log.Printf("[process] Writing service file to %s", servicePath)
	if err := os.MkdirAll(filepath.Dir(servicePath), 0755); err != nil {
		return false, fmt.Errorf("failed to create systemd dir: %w", err)
	}

	err := os.WriteFile(servicePath, []byte(serviceContent), 0644)
	if err != nil {
		return false, fmt.Errorf("failed to write systemd service file %s: %w", servicePath, err)
	}

	// Verify the file was actually written
	if _, statErr := os.Stat(servicePath); statErr != nil {
		return false, fmt.Errorf("service file written but not found on disk: %w", statErr)
	}

	log.Printf("[process] Created systemd service file %s for %s", serviceName, appName)

	// Reload systemd to recognize new service
	if err := pm.reloadDaemon(); err != nil {
		return false, fmt.Errorf("daemon-reload after writing %s: %w", serviceName, err)
	}

	// Give systemd a moment to fully index the new unit
	time.Sleep(500 * time.Millisecond)

	return true, nil
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

// StartService enables and starts the systemd service
func (pm *ProcessManager) StartService(appName string) error {
	serviceName := fmt.Sprintf("nextdeploy-%s.service", appName)

	log.Printf("[process] Enabling service %s", serviceName)

	// #nosec G204
	// Enable the service to start on boot
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

// StopService stops and disables the systemd service
func (pm *ProcessManager) StopService(appName string) error {
	serviceName := fmt.Sprintf("nextdeploy-%s.service", appName)

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

// RestartService restarts the systemd service
func (pm *ProcessManager) RestartService(appName string) error {
	serviceName := fmt.Sprintf("nextdeploy-%s.service", appName)
	// #nosec G204
	cmd := exec.Command("systemctl", "restart", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to restart service %s: %v - %s", serviceName, err, out)
	}
	log.Printf("Restarted systemd service %s", serviceName)
	return nil
}

// RemoveService stops, disables, and deletes the systemd service
func (pm *ProcessManager) RemoveService(appName string) error {
	serviceName := fmt.Sprintf("nextdeploy-%s.service", appName)

	_ = pm.StopService(appName)

	servicePath := filepath.Join(pm.systemdDir, serviceName)
	if err := os.Remove(servicePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove service file %s: %w", servicePath, err)
	}

	return pm.reloadDaemon()
}
