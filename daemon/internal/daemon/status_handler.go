package daemon

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
)

func (ch *CommandHandler) handleStatus(args map[string]interface{}) types.Response {
	appName, ok := stringArg(args, "appName")
	if !ok {
		return types.Response{Success: false, Message: "missing 'appName' argument"}
	}

	serviceName := fmt.Sprintf("nextdeploy-%s.service", appName)
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
		fmt.Sscanf(memory, "%d", &bytes)
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

func (ch *CommandHandler) handleLogs(args map[string]interface{}) types.Response {
	appName, ok := stringArg(args, "appName")
	if !ok {
		return types.Response{Success: false, Message: "missing 'appName' argument"}
	}

	serviceName := fmt.Sprintf("nextdeploy-%s.service", appName)
	cmd := exec.Command("systemctl", "list-unit-files", serviceName)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), serviceName) {
		return types.Response{Success: false, Message: fmt.Sprintf("application %s not found (service %s missing)", appName, serviceName)}
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
