package config

import (
	"bufio"
	"strconv"

	"github.com/Golangcodes/nextdeploy/shared"

	"github.com/spf13/cobra"
)

var (
	skipPrompts bool
)

func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func HandleConfigSetup(cmd *cobra.Command, reader *bufio.Reader) error {
	defaultConfig := true
	plog := shared.PackageLogger("Init", "NextDeploy")

	if defaultConfig {
		err := GenerateSampleConfig()
		if err != nil {
			plog.Error("failed to generate sample config: %v", err)
			return nil
		}
		plog.Success("nextdeploy.yml created")
		return nil
	}

	if !skipPrompts && PromptYesNo(reader, "Create customized nextdeploy.yml?") {
		cfg, err := PromptForConfig(reader)
		if err != nil {
			plog.Error("failed to get configuration: %v", err)
			return nil
		}

		plog.Debug("Generated configuration: %+v", cfg)
		if cfg.Database != nil && cfg.Database.Port != "" {
			port, err := strconv.Atoi(cfg.Database.Port)
			if err != nil {
				plog.Warn("Invalid port number: %s, using default 5432\n", cfg.Database.Port)
				cfg.Database.Port = "5432"
			} else if port < 1 || port > 65535 {
				plog.Warn("Port %d out of range, using default 5432\n", port)
				cfg.Database.Port = "5432"
			}
		} else if cfg.Database != nil {
			cfg.Database.Port = "5432"
		}

		if err := WriteConfig("nextdeploy.yml", cfg); err != nil {
			plog.Error("failed to write configuration: %v", err)
			return nil
		}
		plog.Success("nextdeploy.yml created with your settings")

	} else {
		if PromptYesNo(reader, "Generate sample configuration file for reference?") {
			if err := GenerateSampleConfig(); err != nil {
				plog.Error("failed to generate sample config: %v", err)
				return nil
			}
			plog.Success("sample.nextdeploy.yml created")
		}
	}

	return nil
}
