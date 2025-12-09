package cmd

import (
	"fmt"

	"github.com/dionysius/aarg/internal/app"
	"github.com/dionysius/aarg/internal/config"
	"github.com/spf13/cobra"
)

// publishCmd represents the publish command
var publishCmd = &cobra.Command{
	Use:   "publish [repos...]",
	Short: "Upload repository to configured provider",
	Long: `Upload the generated repository to a configured provider such as Cloudflare Pages.

The public directory will be uploaded to the configured provider (currently supports
Cloudflare Pages). Configure the provider in config.yaml:

cloudflare:
  api_token: "your-cloudflare-api-token"
  account_id: "your-account-id"
  project_name: "your-project-name"
  cleanup:
    older_than_days: 30  # Delete deployments older than 30 days
    keep_last: 10        # Keep only the last 10 deployments

Examples:
  aarg publish                           # Publish all generated content`,
	RunE: runPublish,
}

func runPublish(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Load configuration
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	// Initialize application
	application, err := app.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize application: %w", err)
	}
	defer application.Shutdown()

	// Execute publish
	return application.Publish(ctx)
}
