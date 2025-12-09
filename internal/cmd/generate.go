package cmd

import (
	"fmt"

	"github.com/dionysius/aarg/internal/app"
	"github.com/dionysius/aarg/internal/config"
	"github.com/spf13/cobra"
)

// generateCmd represents the generate command
var generateCmd = &cobra.Command{
	Use:   "generate [repos...]",
	Short: "Generate APT repository and web pages",
	Long: `Generate APT repository structures and optional web pages for browsing.

This command reads verified packages from the trusted directory, applies retention
policies, generates APT repository structure (Packages, Sources, Release files),
and optionally creates static HTML pages for browsing.

Examples:
  aarg generate vaultwarden              # Generate vaultwarden repository
  aarg generate example vaultwarden      # Generate multiple repositories
  aarg generate --all                    # Generate all repositories`,
	RunE: runGenerate,
}

func init() {
	addAllReposFlag(generateCmd, &allRepos)
}

func runGenerate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Validate arguments
	if err := validateRepoArgs(args, allRepos); err != nil {
		return err
	}

	// Load configuration
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	// Select repositories
	repoNames, err := selectRepositories(cfg, args, allRepos)
	if err != nil {
		return err
	}

	// Initialize application
	application, err := app.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize application: %w", err)
	}
	defer application.Shutdown()

	// Execute generate
	return application.Generate(ctx, repoNames)
}
