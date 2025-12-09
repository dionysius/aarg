package cmd

import (
	"fmt"

	"github.com/dionysius/aarg/internal/app"
	"github.com/dionysius/aarg/internal/config"
	"github.com/spf13/cobra"
)

// fetchCmd represents the fetch command
var fetchCmd = &cobra.Command{
	Use:   "fetch [repos...]",
	Short: "Download and verify packages from configured feeds",
	Long: `Download and verify packages from all configured feeds for the specified repositories.

Feeds are activated and packages are downloaded and verified according to the
repository configuration. Downloaded packages are stored in the downloads directory
and verified packages are moved to the trusted directory.

Examples:
  aarg fetch vaultwarden              # Download and verify vaultwarden repository
  aarg fetch example vaultwarden      # Download and verify multiple repositories
  aarg fetch --all                    # Download and verify all repositories`,
	RunE: runFetch,
}

func init() {
	addAllReposFlag(fetchCmd, &allRepos)
}

func runFetch(cmd *cobra.Command, args []string) error {
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

	// Execute fetch
	return application.Fetch(ctx, repoNames)
}
