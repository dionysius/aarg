package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/config"
	"github.com/dionysius/aarg/internal/feed"
	"github.com/dionysius/aarg/internal/log"
)

// Fetch downloads and verifies packages from configured feeds for specified repositories
func (a *Application) Fetch(ctx context.Context, repoNames []string) error {
	// Process all feeds from all repositories in parallel using main worker pool
	group := a.MainPool.NewGroup()

	for _, name := range repoNames {
		// Find repository by name
		var repo *config.RepositoryConfig
		for _, r := range a.Config.Repositories {
			if r.Name == name {
				repo = r
				break
			}
		}
		if repo == nil {
			return fmt.Errorf("repository not found: %s", name)
		}

		// Initialize verifier for this repository
		verifier, err := a.initializeVerifier(repo)
		if err != nil {
			return fmt.Errorf("failed to initialize verifier for %s: %w", repo.Name, err)
		}

		// Submit each feed as a separate task to the pool
		for _, feedOpts := range repo.Feeds {
			// Capture for closure
			opts := feedOpts
			feedVerifier := verifier

			slog.Info("Fetching", "repository", repo.Name, "feed", string(opts.Type)+":"+opts.Name)

			group.SubmitErr(func() error {
				feedType := feed.FeedType(opts.Type)
				location := opts.Name

				// Create scoped storage for this feed
				storage := common.NewStorage(
					a.Downloader,
					a.Config.Directories.GetDownloadsPath(),
					a.Config.Directories.GetTrustedPath(),
					opts.RelativePath,
				)

				// Create feed instance based on type
				var feedInst feed.Feed
				var err error

				switch feedType {
				case feed.FeedTypeGitHub:
					feedInst, err = feed.NewGithub(storage, a.GitHubClient, feedVerifier, opts, &repo.RepositoryOptions, a.MainPool)
				case feed.FeedTypeAPT:
					feedInst, err = feed.NewApt(storage, feedVerifier, opts, &repo.RepositoryOptions, a.MainPool)
				case feed.FeedTypeOBS:
					feedInst, err = feed.NewOBS(storage, feedVerifier, opts, &repo.RepositoryOptions, a.MainPool)
				default:
					return fmt.Errorf("unsupported feed type: %s", feedType)
				}
				if err != nil {
					return fmt.Errorf("failed to create feed %s: %w", location, err)
				}

				// Run feed download
				if err := feedInst.Run(ctx); err != nil {
					return fmt.Errorf("failed to run feed %s: %w", location, err)
				}

				return nil
			})
		}
	}

	// Wait for all feeds from all repositories to complete
	if err := group.Wait(); err != nil {
		return err
	}

	slog.Info("Fetch complete", log.Success())

	return nil
}
