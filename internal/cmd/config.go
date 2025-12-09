package cmd

import (
	"fmt"

	"github.com/dionysius/aarg/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// configCmd represents the config command
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management commands",
	Long:  `Commands for viewing and managing configuration.`,
}

// configShowCmd shows the current configuration
var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current configuration",
	Long: `Display the currently loaded configuration including all repositories.

The configuration is loaded from the main config file and all repository
definitions from the configured repositories directory.

Examples:
  aarg config show              # Show parsed configuration in YAML format`,
	RunE: runConfigShow,
}

func init() {
	configCmd.AddCommand(configShowCmd)
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	// Redact sensitive fields for display
	if cfg.Signing.Passphrase != "" {
		cfg.Signing.Passphrase = "***REDACTED***"
	}
	if cfg.GitHub.Token != "" {
		cfg.GitHub.Token = "***REDACTED***"
	}
	if cfg.Cloudflare.APIToken != "" {
		cfg.Cloudflare.APIToken = "***REDACTED***"
	}

	// Format output
	var output []byte
	output, err = yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config to YAML: %w", err)
	}

	fmt.Fprintln(realStdout, string(output))
	return nil
}
