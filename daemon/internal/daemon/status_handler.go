package daemon

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
)

func (ch *CommandHandler) handleStatus(args map[string]interface{}) types.Response {
	appName, ok := StringArg(args, "appName")
	if !ok {
		return types.Response{Success: false, Message: "missing 'appName' argument"}
	}

	serviceName, err := ch.findActiveService(appName)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("Application '%s' has not been deployed yet. Please run 'nextdeploy ship' first.", appName)}
	}

	// #nosec G204
	cmd := exec.Command("systemctl", "show", serviceName, "--property=ActiveState,MainPID,MemoryCurrent,SubState")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to get service status: %v", err)}
	}
	props := parseProps(string(out))
	status := "Offline"
	if props["ActiveState"] == "active" {
		status = "Online"
	} else if props["ActiveState"] == "failed" {
		status = "Failed"
	} else if props["ActiveState"] == "activating" {
		status = "Starting..."
	}

	pid := props["MainPID"]
	if pid == "0" {
		pid = "N/A"
	}

	memory := props["MemoryCurrent"]
	if memory == "[not set]" || memory == "0" || memory == "" {
		memory = "0MB"
	} else {
		var bytes int64
		_, _ = fmt.Sscanf(memory, "%d", &bytes) // #nosec G104
		memory = fmt.Sprintf("%.2fMB", float64(bytes)/(1024*1024))
	}
	msg := fmt.Sprintf("Status: %s\nPID: %s\nMemory: %s", status, pid, memory)
	return types.Response{
		Success: true,
		Message: msg,
		Data: map[string]interface{}{
			"status": status,
			"pid":    pid,
			"memory": memory,
		},
	}
}

func (ch *CommandHandler) findActiveService(appName string) (string, error) {
	services, err := ch.processManager.FindAppServices(appName)
	if err != nil {
		return "", err
	}
	if len(services) == 0 {
		return "", fmt.Errorf("no services found for app %s", appName)
	}

	// Sort to find the latest timestamped service
	sort.Strings(services)

	// Search for the first one that is active or activating (checking from latest to oldest)
	for i := len(services) - 1; i >= 0; i-- {
		s := services[i]
		// #nosec G204
		cmd := exec.Command("systemctl", "is-active", s)
		out, _ := cmd.CombinedOutput()
		state := strings.TrimSpace(string(out))
		if state == "active" || state == "activating" {
			return s, nil
		}
	}

	// If none are active, return the most recent one
	return services[len(services)-1], nil
}

func (ch *CommandHandler) handleLogs(args map[string]interface{}) types.Response {
	appName, ok := StringArg(args, "appName")
	if !ok {
		return types.Response{Success: false, Message: "missing 'appName' argument"}
	}

	serviceName, err := ch.findActiveService(appName)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("Application '%s' has not been deployed yet. Please run 'nextdeploy ship' first.", appName)}
	}
	return types.Response{
		Success: true,
		Message: serviceName,
	}
}

func parseProps(input string) map[string]string {
	props := make(map[string]string)
	lines := strings.Split(input, "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			props[parts[0]] = parts[1]
		}
	}
	return props
}
