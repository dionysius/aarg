package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/dionysius/aarg/internal/log"
	"github.com/spf13/cobra"
)

var (
	cfgFile    string
	verbose    bool
	realStdout *os.File // Real stdout saved before redirection
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "aarg",
	Short: "Another APT Repository Generator",
	Long: `aarg aggregates packages from multiple sources into APT repositories.

It downloads packages from GitHub releases, APT repositories, and OBS builds,
verifies signatures, applies retention policies, and generates APT repository
structures with optional static web page for browsing.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Save the real stdout before redirecting
		realStdout = os.Stdout

		// Redirect os.Stdout to discard to suppress unwanted library output (e.g., aptly's fmt.Printf)
		os.Stdout, _ = os.Open(os.DevNull)

		// Configure logging based on verbose flag
		level := slog.LevelInfo
		if verbose {
			level = slog.LevelDebug
		}

		handler := log.NewHandler(realStdout, level)
		slog.SetDefault(slog.New(handler))

		// Set Cobra's output to real stdout (not redirected)
		cmd.SetOut(realStdout)
		cmd.SetErr(realStdout)
	},
}

// ExecuteContext runs the root command with context
func ExecuteContext(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.config/aarg/config.yaml or /etc/aarg/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "", "v", false, "enable debug logging")

	// Add subcommands
	rootCmd.AddCommand(fetchCmd)
	rootCmd.AddCommand(generateCmd)
	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(configCmd)
}
