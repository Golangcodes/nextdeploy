package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func Save(cfg *NextDeployConfig, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
func Load() (*NextDeployConfig, error) {
	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("%s Config file not found: %w", EmojiWarning, err)
	}

	var cfg NextDeployConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s Invalid config format: %w", EmojiWarning, err)
	}

	fmt.Printf("%s Configuration loaded successfully\n", EmojiSuccess)
	return &cfg, nil
}
