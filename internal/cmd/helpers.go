package cmd

import (
	"fmt"

	"github.com/dionysius/aarg/internal/config"
	"github.com/spf13/cobra"
)

const (
	// Flag names and descriptions for consistent usage across commands
	allReposFlagName = "all"
	allReposFlagDesc = "operate on all repositories"
)

// addAllReposFlag adds the --all flag to a command
func addAllReposFlag(cmd *cobra.Command, target *bool) {
	cmd.Flags().BoolVar(target, allReposFlagName, false, allReposFlagDesc)
}

// validateRepoArgs validates repository arguments and --all flag usage
func validateRepoArgs(args []string, all bool) error {
	if !all && len(args) == 0 {
		return fmt.Errorf("specify repository names or use --%s flag", allReposFlagName)
	}
	if all && len(args) > 0 {
		return fmt.Errorf("cannot specify repository names when using --%s flag", allReposFlagName)
	}
	return nil
}

// selectRepositories selects which repositories to operate on
func selectRepositories(cfg *config.Config, names []string, all bool) ([]string, error) {
	if all {
		// Return all repository names
		repos := make([]string, 0, len(cfg.Repositories))
		for _, repo := range cfg.Repositories {
			repos = append(repos, repo.Name)
		}
		return repos, nil
	}

	// Validate that specified repositories exist
	for _, name := range names {
		found := false
		for _, repo := range cfg.Repositories {
			if repo.Name == name {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("repository not found: %s", name)
		}
	}

	return names, nil
}
