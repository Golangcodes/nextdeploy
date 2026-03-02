package client

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/daemon/internal/client"
	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
)

var (
	socketPath = "/var/run/nextdeployd.sock"
)

func init() {
	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			socketPath = home + "/.nextdeploy/daemon.sock"
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(1)
	}

	command := os.Args[1]

	if command == "daemon" {
		handleDaemonCommand()
		return
	}

	if !isDaemonRunning() {
		fmt.Printf("NextDeploy daemon is not running\n")
		fmt.Printf("   Start it with: nextdeployd daemon\n")
		fmt.Printf("   Or run: sudo systemctl start nextdeployd\n")
		os.Exit(1)
	}

	sendCommandToDaemon(command)
}

func isDaemonRunning() bool {
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		return false
	}

	conn, err := client.SendCommand(socketPath, types.Command{
		Type: "status",
		Args: map[string]interface{}{},
	})

	return err == nil && conn != nil
}

func handleDaemonCommand() {
	configPath := "/etc/nextdeployd/config.json"
	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			configPath = home + "/.nextdeploy/config.json"
		}
	}
	foreground := false

	for _, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--config=") {
			configPath = strings.TrimPrefix(arg, "--config=")
		} else if arg == "--foreground" {
			foreground = true
		}
	}

	if foreground {
		runDaemonDirectly(configPath)
	} else {
		startDaemonProcess(configPath)
	}
}

func runDaemonDirectly(configPath string) {
	fmt.Println("Starting NextDeploy daemon in foreground...")

	// #nosec G204 G702
	cmd := exec.Command("nextdeploy-daemon", "--foreground=true", "--config="+configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to start daemon: %v\n", err)
		os.Exit(1)
	}
}

func startDaemonProcess(configPath string) {
	if isDaemonRunning() {
		fmt.Println("NextDeploy daemon is already running")
		return
	}

	fmt.Println("Starting NextDeploy daemon...")
	// #nosec G204 G702
	cmd := exec.Command("nextdeploy-daemon", "--config="+configPath)
	if err := cmd.Start(); err != nil {
		fmt.Printf("Failed to start daemon: %v\n", err)
		os.Exit(1)
	}
	time.Sleep(1 * time.Second)

	if isDaemonRunning() {
		fmt.Printf("Daemon started successfully with PID %d\n", cmd.Process.Pid)
	} else {
		fmt.Println("Daemon started but may not be responding")
		fmt.Println("Check logs: /var/log/nextdeployd/daemon.out")
	}
}

func sendCommandToDaemon(command string) {
	args := make(map[string]interface{})
	for i := 2; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "--") {
			parts := strings.SplitN(arg[2:], "=", 2)
			if len(parts) == 2 {
				key := parts[0]
				value := parts[1]
				switch value {
				case "true":
					args[key] = true
				case "false":
					args[key] = false
				default:
					if intVal, err := strconv.Atoi(value); err == nil {
						args[key] = intVal
					} else {
						args[key] = value
					}
				}
			} else if len(parts) == 1 {
				args[parts[0]] = true
			}
		}
	}

	cmd := types.Command{
		Type: command,
		Args: args,
	}

	response, err := client.SendCommand(socketPath, cmd)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		if strings.Contains(err.Error(), "connect") {
			fmt.Println("   The daemon may not be running or is not responding")
			fmt.Println("   Start it with: nextdeployd daemon")
		}
		os.Exit(1)
	}
	displayResponse(response)
}

func displayResponse(response *types.Response) {
	if response.Success {
		fmt.Printf("Success: %s\n", response.Message)
		if response.Data != nil {
			switch data := response.Data.(type) {
			case []interface{}:
				for _, item := range data {
					if itemMap, ok := item.(map[string]interface{}); ok {
						// format container list nicely
						for key, value := range itemMap {
							fmt.Printf("%s: %v\t", key, value)
						}
						fmt.Println()
					} else {
						fmt.Printf("%v\n", item)
					}
				}
			case map[string]interface{}:
				for key, value := range data {
					fmt.Printf("%s: %v\n", key, value)
				}
			case []map[string]string:
				// handle container list format
				if len(data) > 0 {
					fmt.Printf("\n%-12s %-20s %-30s %-15s %-20s\n", "ID", "NAME", "IMAGE", "STATUS", "PORTS")
					fmt.Println(strings.Repeat("-", 100))
					for _, container := range data {
						fmt.Printf("%-12s %-20s %-30s %-15s %-20s\n",
							truncate(container["id"], 12),
							truncate(container["name"], 20),
							truncate(container["image"], 30),
							container["status"],
							truncate(container["ports"], 20))
					}
				}

			case string:
				fmt.Printf(" %s\n", data)
			default:
				if jsonData, err := json.MarshalIndent(data, "", "  "); err == nil {
					fmt.Printf("Data: %s\n", string(jsonData))
				} else {
					fmt.Printf("Data: %v\n", data)
				}
			}
		}
	} else {
		fmt.Printf("Error: %s\n", response.Message)
		os.Exit(1)
	}
}
