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
)

func main() {
	// Handle subcommands before flag parsing so they work cleanly.
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
			// Parse --tarball and --dopplerToken from remaining args and forward
			// the ship command over the Unix socket to the running daemon.
			tarball := ""
			dopplerToken := ""
			for _, arg := range os.Args[2:] {
				if strings.HasPrefix(arg, "--tarball=") {
					tarball = strings.TrimPrefix(arg, "--tarball=")
					// strip surrounding quotes if present
					tarball = strings.Trim(tarball, "\"'")
				} else if strings.HasPrefix(arg, "--dopplerToken=") {
					dopplerToken = strings.TrimPrefix(arg, "--dopplerToken=")
				}
			}
			if tarball == "" {
				fmt.Fprintln(os.Stderr, "Error: --tarball is required")
				os.Exit(1)
			}
			socketPath := "/var/run/nextdeployd.sock"
			if os.Geteuid() != 0 {
				home, err := os.UserHomeDir()
				if err == nil {
					socketPath = home + "/.nextdeploy/daemon.sock"
				}
			}
			args := map[string]interface{}{"tarball": tarball}
			if dopplerToken != "" {
				args["dopplerToken"] = dopplerToken
			}
			resp, err := daemoniclient.SendCommand(socketPath, daemontypes.Command{
				Type: "ship",
				Args: args,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error contacting daemon: %v\n", err)
				os.Exit(1)
			}
			if resp != nil && !resp.Success {
				fmt.Fprintf(os.Stderr, "Deployment failed: %s\n", resp.Message)
				os.Exit(1)
			}
			if resp != nil {
				fmt.Printf("✅ %s\n", resp.Message)
			}
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

	// Background update hint — never blocks startup.
	go updater.CheckAndPrint(shared.Version)

	if !*foreground {
		daemonize()
		return
	}

	runDaemon(*configPath)
}

func daemonize() {
	// get the executable path
	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("Error getting executable path: %v", err)
	}
	// return ourselves in foreground
	args := []string{"--foreground=true"}
	if len(os.Args) > 1 {
		// preserver other args like config path
		for _, arg := range os.Args[2:] {
			if arg != "--foreground" && !strings.HasPrefix(arg, "--foreground=") {
				args = append(args, arg)
			}
		}
	}
	// #nosec G204, G702
	cmd := exec.Command(execPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	// redirect output to log file
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
	// #nosec G304
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

var lockFile *os.File

func acquireLock() error {
	lockPath := "/var/run/nextdeployd.pid"
	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			lockPath = filepath.Join(home, ".nextdeploy", "nextdeployd.pid")
		} else {
			lockPath = "/tmp/nextdeployd.pid"
		}
	}

	if err := os.MkdirAll(filepath.Dir(lockPath), 0750); err != nil {
		return err
	}

	// #nosec G304
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}

	// #nosec G115
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return fmt.Errorf("another instance of nextdeployd is already running")
	}

	lockFile = f
	_ = f.Truncate(0)
	_, _ = f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	return nil
}

func runDaemon(configPath string) {
	if err := acquireLock(); err != nil {
		log.Fatalf("Lock error: %v", err)
	}

	// Start the expvar/pprof metrics server on localhost only.
	daemon.StartMetricsServer("127.0.0.1:6060")

	d, err := daemon.NewNextDeployDaemon(configPath)
	if err != nil {
		log.Fatalf("Error initializing daemon: %v", err)
	}
	if err := d.Start(); err != nil {
		log.Fatalf("Error starting daemon: %v", err)
	}

}
