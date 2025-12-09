package cmd

import (
	"fmt"

	"github.com/dionysius/aarg/internal/app"
	"github.com/dionysius/aarg/internal/config"
	"github.com/spf13/cobra"
)

var allRepos bool

// buildCmd represents the build command
var buildCmd = &cobra.Command{
	Use:   "build [repos...]",
	Short: "Complete build: download, generate, and publish",
	Long: `Execute the complete build pipeline: download packages, generate repositories, and publish.

This is equivalent to running download, generate, and publish commands in sequence.
It's the most common workflow for updating repositories.

Examples:
  aarg build vaultwarden              # Build vaultwarden repository
  aarg build example vaultwarden      # Build multiple repositories
  aarg build --all                    # Build all repositories`,
	RunE: runBuild,
}

func init() {
	addAllReposFlag(buildCmd, &allRepos)
}

func runBuild(cmd *cobra.Command, args []string) error {
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

	// Initialize application once for all phases
	application, err := app.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize application: %w", err)
	}
	defer application.Shutdown()

	// Execute fetch phase
	if err := application.Fetch(ctx, repoNames); err != nil {
		return fmt.Errorf("fetch phase failed: %w", err)
	}

	// Execute generate phase
	if err := application.Generate(ctx, repoNames); err != nil {
		return fmt.Errorf("generate phase failed: %w", err)
	}

	// Execute publish phase
	if err := application.Publish(ctx); err != nil {
		return fmt.Errorf("publish phase failed: %w", err)
	}

	return nil
}
