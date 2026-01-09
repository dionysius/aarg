package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aptly-dev/aptly/pgp"
	"github.com/dionysius/aarg/debext"
	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/compose"
	"github.com/dionysius/aarg/internal/config"
	"github.com/dionysius/aarg/internal/feed"
	"github.com/dionysius/aarg/internal/log"
)

// Generate generates APT repository structures and web page for specified repositories
func (a *Application) Generate(ctx context.Context, repoNames []string) (err error) {
	// Create timestamped staging directory
	timestamp := time.Now().Format("20060102-150405")
	stagingPath := filepath.Join(a.Config.Directories.GetStagingPath(), timestamp)

	if err := os.MkdirAll(stagingPath, 0755); err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}

	// Track error for cleanup
	defer func() {
		// Clean up staging directory on failure
		if err != nil {
			if rmErr := os.RemoveAll(stagingPath); rmErr != nil {
				slog.Error("Failed to remove staging directory", "dir", stagingPath, "error", rmErr)
			}
		}
	}()

	// Process all repositories in parallel
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
			err = fmt.Errorf("repository not found: %s", name)
			return err
		}

		// Capture loop variables for goroutine
		repoToGenerate := repo
		group.SubmitErr(func() error {
			return a.generateRepository(ctx, repoToGenerate, stagingPath)
		})
	}

	// Wait for all repositories to complete
	if err = group.Wait(); err != nil {
		return err
	}

	// Call Index on composers that need post-processing
	for _, composerName := range a.Config.Generate.Compose {
		switch composerName {
		case "apt":
			// APT composer Index copies signing keys to staging directory
			if err = a.copySigningKeys(stagingPath); err != nil {
				return err
			}
		case "web":
			if err = a.webIndex(ctx, stagingPath); err != nil {
				return err
			}
		}
	}

	// Generate 404.html to prevent Cloudflare Pages SPA behavior (required for _redirects to work)
	if err = compose.Generate404HTML(stagingPath); err != nil {
		return fmt.Errorf("failed to generate 404.html: %w", err)
	}

	// Atomically swap staging to public via symlink
	if err = a.swapPublicSymlink(stagingPath); err != nil {
		return err
	}

	// Clean up old staging directories
	if err := a.cleanupOldStaging(); err != nil {
		return err
	}

	slog.Info("Generate complete", log.Success())

	return nil
}

// generateRepository generates a single repository (APT + web)
func (a *Application) generateRepository(ctx context.Context, repo *config.RepositoryConfig, stagingPath string) error {
	slog.Info("Generating repository", "repository", repo.Name)

	// Expand OBS feeds into APT feeds for APT composition
	// Web composition will use the original feed list (repo.Feeds)
	var expandedFeeds []*feed.FeedOptions
	for _, feedOpts := range repo.Feeds {
		if feed.FeedType(feedOpts.Type) == feed.FeedTypeOBS {
			aptFeeds := feed.ExpandOBSFeed(feedOpts)
			expandedFeeds = append(expandedFeeds, aptFeeds...)
		} else {
			expandedFeeds = append(expandedFeeds, feedOpts)
		}
	}

	// Build APT compose options
	aptOptions := &compose.AptComposeOptions{
		ComposeOptions: compose.ComposeOptions{
			Target: filepath.Join(stagingPath, repo.Name),
			Name:   repo.Name,
			Feeds:  expandedFeeds,
		},
		Repository: &repo.RepositoryOptions,
		Trusted:    a.Config.Directories.GetTrustedPath(),
		PoolMode:   a.Config.Generate.PoolMode,
	}

	// Create verifier for compose phase - trust files in trusted storage
	// Files were already verified during fetch, so we accept unsigned and ignore signatures
	verifier := &debext.Verifier{
		Verifier:         &pgp.GoVerifier{}, // Empty verifier, won't be used
		AcceptUnsigned:   true,
		IgnoreSignatures: true,
	}

	// Create APT composer
	aptComposer := compose.NewApt(aptOptions, verifier, a.Signer, a.DeCompressor, a.MainPool)

	// Compose APT repository
	repository, err := aptComposer.Compose(ctx)
	if err != nil {
		return fmt.Errorf("failed to compose APT repository for %s: %w", repo.Name, err)
	}

	// Collect statistics for logging
	dists := repository.GetDistributions()
	var totalArchs, totalComps, totalPkgs int
	archSet := make(map[string]struct{})
	compSet := make(map[string]struct{})
	for _, dist := range dists {
		for _, comp := range repository.GetComponents(dist) {
			compSet[comp] = struct{}{}
			for _, arch := range repository.GetArchitectures(dist, comp, false) {
				archSet[arch] = struct{}{}
			}
		}
		totalPkgs += len(repository.GetPackageNames(common.MainComponent))
	}
	totalArchs = len(archSet)
	totalComps = len(compSet)

	slog.Info("APT repository generated",
		"repository", repo.Name,
		"distributions", len(dists),
		"components", totalComps,
		"architectures", totalArchs,
		"packages", totalPkgs)

	// Run composers in configured order
	for _, composerName := range a.Config.Generate.Compose {
		switch composerName {
		case "apt":
			// Already done above - APT must always be first
			continue
		case "web":
			if err := a.generateWeb(ctx, repo, repository, stagingPath); err != nil {
				return err
			}
		default:
			slog.Warn("Unknown composer", "name", composerName, "repository", repo.Name)
		}
	}

	return nil
}

// generateWeb generates web page for a repository
func (a *Application) generateWeb(ctx context.Context, repo *config.RepositoryConfig, repository *debext.Repository, stagingPath string) error {
	webOptions := &compose.WebComposeOptions{
		ComposeOptions: compose.ComposeOptions{
			Target: stagingPath,
			Name:   repo.Name,
			Feeds:  repo.Feeds,
		},
		Repository:       &repo.RepositoryOptions,
		BaseURL:          a.Config.URL,
		Downloads:        a.Config.Directories.GetDownloadsPath(),
		PrimaryPackage:   repo.Packages.Primary,
		IconURLs:         a.Config.Web.GetIconURLs(),
		GitHubClient:     a.GitHubClient,
		TailwindRelease:  a.Config.Web.Tailwind.Release,
		RepositoryConfig: repo,
	}

	webComposer, err := compose.NewWeb(webOptions, a.Downloader)
	if err != nil {
		return fmt.Errorf("failed to initialize web composer for %s: %w", repo.Name, err)
	}

	if err := webComposer.Compose(ctx, repository); err != nil {
		return fmt.Errorf("failed to compose web page for %s: %w", repo.Name, err)
	}

	slog.Info("Web page generated", "repository", repo.Name)
	return nil
}

// webIndex generates the index.html overview page
func (a *Application) webIndex(ctx context.Context, stagingPath string) error {
	webOptions := &compose.WebComposeOptions{
		ComposeOptions: compose.ComposeOptions{
			Target: stagingPath,
		},
		BaseURL:         a.Config.URL,
		Downloads:       a.Config.Directories.GetDownloadsPath(),
		IconURLs:        a.Config.Web.GetIconURLs(),
		GitHubClient:    a.GitHubClient,
		TailwindRelease: a.Config.Web.Tailwind.Release,
	}

	webComposer, err := compose.NewWeb(webOptions, a.Downloader)
	if err != nil {
		return err
	}

	return webComposer.Index(ctx)
}

// swapPublicSymlink atomically updates the public symlink to point to the new staging directory
func (a *Application) swapPublicSymlink(newStagingPath string) error {
	publicPath := a.Config.Directories.GetPublicPath()

	// Create temporary symlink in staging directory
	tmpSymlink := newStagingPath + ".symlink"

	// Create symlink to new staging directory
	if err := os.Symlink(newStagingPath, tmpSymlink); err != nil {
		return fmt.Errorf("failed to create temporary symlink: %w", err)
	}

	// Atomically rename temporary symlink to final public path
	if err := os.Rename(tmpSymlink, publicPath); err != nil {
		_ = os.Remove(tmpSymlink) // Clean up temp symlink on error
		return fmt.Errorf("failed to swap symlink: %w", err)
	}

	return nil
}

// cleanupOldStaging removes old staging directories beyond keep_last limit
func (a *Application) cleanupOldStaging() error {
	if a.Config.Generate.KeepLast <= 0 {
		return nil // Cleanup disabled
	}

	stagingBase := a.Config.Directories.GetStagingPath()

	// Read all entries in staging directory
	entries, err := os.ReadDir(stagingBase)
	if err != nil {
		return fmt.Errorf("failed to read staging directory: %w", err)
	}

	// Filter to only directories with timestamp format
	var stagingDirs []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			// Basic check for timestamp format (YYYYMMDD-HHMMSS)
			name := entry.Name()
			if len(name) == 15 && name[8] == '-' {
				stagingDirs = append(stagingDirs, entry)
			}
		}
	}

	// Sort by name (timestamp) in descending order (newest first)
	sort.Slice(stagingDirs, func(i, j int) bool {
		return stagingDirs[i].Name() > stagingDirs[j].Name()
	})

	// Delete directories beyond keep_last
	if len(stagingDirs) > a.Config.Generate.KeepLast {
		toDelete := stagingDirs[a.Config.Generate.KeepLast:]
		for _, dir := range toDelete {
			dirPath := filepath.Join(stagingBase, dir.Name())
			if err := os.RemoveAll(dirPath); err != nil {
				slog.Warn("Failed to delete old staging directory", "path", dirPath, "error", err)
			} else {
				slog.Debug("Deleted old staging directory", "path", dirPath)
			}
		}
		slog.Info("Cleaned up old staging directories", "deleted", len(toDelete), "kept", a.Config.Generate.KeepLast)
	}

	return nil
}

// copySigningKeys copies both ASCII and binary GPG signing keys to the staging directory
func (a *Application) copySigningKeys(stagingPath string) error {
	if len(a.PublicKeyASCII) == 0 {
		return nil
	}

	keysDir := filepath.Join(stagingPath, "keys")
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(keysDir, "signing-key.asc"), a.PublicKeyASCII, 0644); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(keysDir, "signing-key.gpg"), a.PublicKeyBinary, 0644); err != nil {
		return err
	}

	return nil
}
