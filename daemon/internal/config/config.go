package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"github.com/Golangcodes/nextdeploy/shared/config"

	"gopkg.in/yaml.v3"
)

func LoadConfig(filePath string) (*types.DaemonConfig, error) {
	socketPath := "/var/run/nextdeployd.sock"
	logDir := "/var/log/nextdeploy"

	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			socketPath = home + "/.nextdeploy/daemon.sock"
			logDir = home + "/.nextdeploy/log"
		}
	}

	config := &types.DaemonConfig{
		SocketPath:      socketPath,
		SocketMode:      "0666",
		DockerSocket:    "/var/run/docker.sock",
		ContainerPrefix: "nextdeploy_",
		LogLevel:        "info",
		LogDir:          logDir,
		LogMaxSize:      10,
		LogMaxBackups:   5,
	}

	if filePath != "" {
		file, err := os.Open(filePath)
		if err == nil {
			defer file.Close()
			decoder := json.NewDecoder(file)
			if err := decoder.Decode(config); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return config, nil
}

func ReadConfigInServer(path string) (*config.NextDeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Config file not found: %w", err)
	}
	var cfg config.NextDeployConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("Invalid config format: %w", err)
	}
	return &cfg, nil
}
