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

			// Expand feed options based on type (OBS/APT with multiple distributions)
			var expandedFeedOpts []*feed.FeedOptions
			feedType := feed.FeedType(opts.Type)
			switch feedType {
			case feed.FeedTypeOBS:
				expandedFeedOpts = feed.ExpandOBSFeedOptions(opts)
			case feed.FeedTypeAPT:
				expandedFeedOpts = feed.ExpandAptFeedOptions(opts)
			case feed.FeedTypeGitHub:
				// GitHub feeds don't expand
				expandedFeedOpts = []*feed.FeedOptions{opts}
			default:
				return fmt.Errorf("unsupported feed type: %s", feedType)
			}

			// Create and submit a feed task for each expanded feed option
			for _, expandedOpts := range expandedFeedOpts {
				// Capture for closure
				feedOpt := expandedOpts

				group.SubmitErr(func() error {
					// Create scoped storage for this expanded feed
					storage := common.NewStorage(
						a.Downloader,
						a.Config.Directories.GetDownloadsPath(),
						a.Config.Directories.GetTrustedPath(),
						feedOpt.RelativePath,
					)

					// Create feed instance based on type (after expansion, OBS becomes APT)
					var feedInst feed.Feed
					var err error

					switch feed.FeedType(feedOpt.Type) {
					case feed.FeedTypeGitHub:
						feedInst, err = feed.NewGithub(storage, a.GitHubClient, feedVerifier, feedOpt, &repo.RepositoryOptions, a.MainPool)
					case feed.FeedTypeAPT:
						feedInst, err = feed.NewApt(storage, feedVerifier, feedOpt, &repo.RepositoryOptions, a.MainPool)
					default:
						return fmt.Errorf("unsupported expanded feed type: %s", feedOpt.Type)
					}
					if err != nil {
						return fmt.Errorf("failed to create feed %s: %w", feedOpt.Name, err)
					}

					// Run feed download
					if err := feedInst.Run(ctx); err != nil {
						return fmt.Errorf("failed to run feed %s: %w", feedOpt.Name, err)
					}

					return nil
				})
			}
		}
	}

	// Wait for all feeds from all repositories to complete
	if err := group.Wait(); err != nil {
		return err
	}

	slog.Info("Fetch complete", log.Success())

	return nil
}
