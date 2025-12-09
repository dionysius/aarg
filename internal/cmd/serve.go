package cmd

import (
	"fmt"

	"github.com/dionysius/aarg/internal/app"
	"github.com/dionysius/aarg/internal/config"
	"github.com/spf13/cobra"
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the public directory via HTTP",
	Long: `Serve the public directory via HTTP server for local testing and development.

The server will serve the contents of the public directory, making the
repository accessible via a web browser. This is useful for testing the
generated repository pages before deploying to production.`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
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

	// Execute serve
	return application.Serve(ctx)
}
