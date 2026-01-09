package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load loads the configuration from the specified path or searches default locations
func Load(configPath string) (*Config, error) {
	// Find config file
	cfgFile, err := findConfigFile(configPath)
	if err != nil {
		return nil, err
	}

	// Get config directory from file path
	configDir := filepath.Dir(cfgFile)
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return nil, err
	}

	// Unmarshal main config
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Store config directory
	cfg.ConfigDir = configDir

	// Apply defaults (includes environment variables and directory path resolution)
	cfg.defaults()

	// Load repository configurations
	if err := cfg.loadRepositories(); err != nil {
		return nil, err
	}

	// Validate configuration
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// findConfigFile searches for the configuration file in standard locations
func findConfigFile(explicitPath string) (string, error) {
	// If explicit path provided, use it
	if explicitPath != "" {
		if !fileExists(explicitPath) {
			return "", os.ErrNotExist
		}
		return explicitPath, nil
	}

	// Try standard locations by priority
	candidates := []string{}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "aarg", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "aarg", "config.yaml"))
	}
	candidates = append(candidates, "/etc/aarg/config.yaml")

	// Find first existing file
	for _, file := range candidates {
		if fileExists(file) {
			return file, nil
		}
	}

	return "", os.ErrNotExist
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
