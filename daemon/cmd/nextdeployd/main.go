//go:build !windows

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	daemoniclient "github.com/Golangcodes/nextdeploy/daemon/internal/client"
	"github.com/Golangcodes/nextdeploy/daemon/internal/daemon"
	daemontypes "github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/updater"
	"github.com/gofrs/flock"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("nextdeployd %s\n", shared.Version)
			return
		case "update":
			if err := updater.SelfUpdateDaemon(shared.Version); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "ship":
			handleShipSubcommand()
			return
		case "secrets":
			handleSecretsSubcommand()
			return
		case "status":
			handleStatusSubcommand()
			return
		case "logs":
			handleLogsSubcommand()
			return
		case "rollback":
			handleRollbackSubcommand()
			return
		}
	}

	defaultConfig := "/etc/nextdeployd/config.json"
	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			defaultConfig = home + "/.nextdeploy/config.json"
		}
	}

	configPath := flag.String("config", defaultConfig, "Path to config file")
	foreground := flag.Bool("foreground", false, "Run in foreground")
	flag.Parse()

	go updater.CheckAndPrint(shared.Version)

	if !*foreground {
		daemonize()
		return
	}

	runDaemon(*configPath)
}

func getSocketPath() string {
	socketPath := "/var/run/nextdeployd.sock"
	if _, err := os.Stat(socketPath); os.IsNotExist(err) && os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			socketPath = filepath.Join(home, ".nextdeploy", "daemon.sock")
		}
	}
	return socketPath
}

func sendDaemonCommand(cmd daemontypes.Command) {
	socketPath := getSocketPath()
	resp, err := daemoniclient.SendCommand(socketPath, cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error contacting daemon: %v (Is nextdeployd running?)\n", err)
		os.Exit(1)
	}
	if resp != nil {
		if !resp.Success {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Message)
			os.Exit(1)
		}
		fmt.Print(resp.Message)
		if !strings.HasSuffix(resp.Message, "\n") {
			fmt.Println()
		}
	}
}

func handleShipSubcommand() {
	tarball := ""
	dopplerToken := ""
	for _, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--tarball=") {
			tarball = strings.TrimPrefix(arg, "--tarball=")
			tarball = strings.Trim(tarball, "\"'")
		} else if strings.HasPrefix(arg, "--dopplerToken=") {
			dopplerToken = strings.TrimPrefix(arg, "--dopplerToken=")
		}
	}
	if tarball == "" {
		fmt.Fprintln(os.Stderr, "Error: --tarball is required")
		os.Exit(1)
	}
	args := map[string]interface{}{"tarball": tarball}
	if dopplerToken != "" {
		args["dopplerToken"] = dopplerToken
	}
	sendDaemonCommand(daemontypes.Command{Type: "ship", Args: args})
}

func handleSecretsSubcommand() {
	action := ""
	appName := ""
	key := ""
	value := ""
	for _, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--action=") {
			action = strings.TrimPrefix(arg, "--action=")
		} else if strings.HasPrefix(arg, "--appName=") {
			appName = strings.TrimPrefix(arg, "--appName=")
		} else if strings.HasPrefix(arg, "--key=") {
			key = strings.TrimPrefix(arg, "--key=")
		} else if strings.HasPrefix(arg, "--value=") {
			value = strings.TrimPrefix(arg, "--value=")
			value = strings.Trim(value, "\"'")
		}
	}
	args := map[string]interface{}{
		"action":  action,
		"appName": appName,
		"key":     key,
		"value":   value,
	}
	sendDaemonCommand(daemontypes.Command{Type: "secrets", Args: args})
}

func handleStatusSubcommand() {
	appName := ""
	for _, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--appName=") {
			appName = strings.TrimPrefix(arg, "--appName=")
		}
	}
	sendDaemonCommand(daemontypes.Command{Type: "status", Args: map[string]interface{}{"appName": appName}})
}

func handleLogsSubcommand() {
	appName := ""
	for _, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--appName=") {
			appName = strings.TrimPrefix(arg, "--appName=")
		}
	}
	sendDaemonCommand(daemontypes.Command{Type: "logs", Args: map[string]interface{}{"appName": appName}})
}

func handleRollbackSubcommand() {
	appName := ""
	dopplerToken := ""
	for _, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--appName=") {
			appName = strings.TrimPrefix(arg, "--appName=")
		} else if strings.HasPrefix(arg, "--dopplerToken=") {
			dopplerToken = strings.TrimPrefix(arg, "--dopplerToken=")
		}
	}
	if appName == "" {
		fmt.Fprintln(os.Stderr, "Error: --appName is required")
		os.Exit(1)
	}
	args := map[string]interface{}{"appName": appName}
	if dopplerToken != "" {
		args["dopplerToken"] = dopplerToken
	}
	sendDaemonCommand(daemontypes.Command{Type: "rollback", Args: args})
}

func daemonize() {
	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("Error getting executable path: %v", err)
	}
	args := []string{"--foreground=true"}
	if len(os.Args) > 1 {
		for _, arg := range os.Args[1:] {
			if arg != "--foreground" && !strings.HasPrefix(arg, "--foreground=") {
				args = append(args, arg)
			}
		}
	}
	cmd := exec.Command(execPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logDir := "/var/log/nextdeployd"
	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			logDir = home + "/.nextdeploy/log"
		}
	}
	if err := os.MkdirAll(logDir, 0750); err != nil {
		log.Fatalf("Error creating log directory: %v", err)
	}
	logFilePath := filepath.Join(logDir, "nextdeployd.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.Fatalf("Error opening log file: %v", err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		log.Fatalf("Error starting daemon: %v", err)
	}
	fmt.Printf("Daemon started with PID %d\n", cmd.Process.Pid)
	fmt.Printf("Logs are being written to %s\n", logFilePath)
	os.Exit(0)
}

func acquireLock() error {
	lockPath := "/var/run/nextdeployd.lock"
	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			lockPath = filepath.Join(home, ".nextdeploy", "nextdeployd.lock")
		} else {
			lockPath = filepath.Join(os.TempDir(), "nextdeployd.lock")
		}
	}

	if err := os.MkdirAll(filepath.Dir(lockPath), 0750); err != nil {
		return err
	}

	fileLock := flock.New(lockPath)
	locked, err := fileLock.TryLock()
	if err != nil {
		return fmt.Errorf("error acquiring lock: %v", err)
	}
	if !locked {
		return fmt.Errorf("another instance of nextdeployd is already running")
	}

	pidPath := strings.TrimSuffix(lockPath, ".lock") + ".pid"
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
		fileLock.Unlock()
		return fmt.Errorf("error writing PID file: %v", err)
	}

	return nil
}

func runDaemon(configPath string) {
	if err := acquireLock(); err != nil {
		log.Fatalf("Lock error: %v", err)
	}
	daemon.StartMetricsServer("127.0.0.1:6060")
	d, err := daemon.NewNextDeployDaemon(configPath)
	if err != nil {
		log.Fatalf("Error initializing daemon: %v", err)
	}
	if err := d.Start(); err != nil {
		log.Fatalf("Error starting daemon: %v", err)
	}
}

func isRoot() bool {
	return os.Geteuid() == 0
}

func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true,
	}
}
