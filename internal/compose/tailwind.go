package compose

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/feed"
	"github.com/google/go-github/v80/github"
)

// TailwindCLI manages the Tailwind CSS CLI binary
type TailwindCLI struct {
	downloader   *common.Downloader
	githubClient *github.Client
	assetsDir    string // Directory where Tailwind binary is cached
	release      string // Specific release to use (empty = latest)
	binaryPath   string
}

// NewTailwindCLI creates a new Tailwind CLI manager
// Downloads and caches the Tailwind binary in the assets directory
// If release is empty, uses the latest release
func NewTailwindCLI(downloader *common.Downloader, githubClient *github.Client, assetsDir string, release string) *TailwindCLI {
	return &TailwindCLI{
		downloader:   downloader,
		githubClient: githubClient,
		assetsDir:    assetsDir,
		release:      release,
	}
}

// Build runs the Tailwind CLI to generate CSS from input file
// Tailwind v4 automatically scans files based on @source directives in the input CSS
func (t *TailwindCLI) Build(ctx context.Context, inputCSS, outputCSS, cwd string) error {
	// Get or download the Tailwind binary
	binary, err := t.getTailwindBinary(ctx)
	if err != nil {
		return fmt.Errorf("failed to get Tailwind binary: %w", err)
	}

	// Build command arguments
	args := []string{
		"-i", inputCSS,
		"-o", outputCSS,
		"--minify",
		"--cwd", cwd,
	}

	// Run command
	cmd := exec.CommandContext(ctx, binary, args...)
	// Suppress Tailwind CLI output (errors will be in the error message)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running Tailwind CLI: %w", err)
	}

	return nil
}

// getTailwindBinary returns the path to the Tailwind binary, downloading it if necessary
func (t *TailwindCLI) getTailwindBinary(ctx context.Context) (string, error) {
	// Return cached path if available
	if t.binaryPath != "" {
		return t.binaryPath, nil
	}

	// Build path in assets directory
	tailwindDir := filepath.Join(t.assetsDir, "tailwindcss")
	binaryName := "tailwindcss"
	binaryPath := filepath.Join(tailwindDir, binaryName)

	// Check if binary already exists
	if _, err := os.Stat(binaryPath); err == nil {
		// Make sure it's executable
		if err := os.Chmod(binaryPath, 0700); err != nil {
			return "", fmt.Errorf("could not make binary executable: %w", err)
		}
		t.binaryPath = binaryPath
		return binaryPath, nil
	}

	// Download on first use
	if err := t.downloadTailwind(ctx, binaryPath); err != nil {
		return "", err
	}

	t.binaryPath = binaryPath
	return binaryPath, nil
}

// downloadTailwind downloads the Tailwind CLI binary using the downloader
func (t *TailwindCLI) downloadTailwind(ctx context.Context, binaryPath string) error {
	// Create cache directory
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0755); err != nil {
		return fmt.Errorf("could not create cache directory: %w", err)
	}

	// Get release from GitHub API
	var release *github.RepositoryRelease
	var err error
	if t.release != "" {
		// Use configured release
		slog.Info("Downloading Tailwind CSS CLI", "version", t.release)
		release, _, err = t.githubClient.Repositories.GetReleaseByTag(ctx, "tailwindlabs", "tailwindcss", t.release)
		if err != nil {
			return fmt.Errorf("could not get release %s: %w", t.release, err)
		}
	} else {
		// Get latest release
		release, _, err = t.githubClient.Repositories.GetLatestRelease(ctx, "tailwindlabs", "tailwindcss")
		if err != nil {
			return fmt.Errorf("could not get latest release: %w", err)
		}
		slog.Info("Downloading Tailwind CSS CLI", "version", release.GetTagName())
	}

	// Find the asset for current platform
	filename := getTailwindFilename()
	var assetURL string
	var assetChecksum string

	for _, asset := range release.Assets {
		if asset.GetName() == filename {
			assetURL = asset.GetBrowserDownloadURL()
			// Check if asset has a digest in the API response
			if digest := asset.GetDigest(); digest != "" {
				// Parse digest using feed.ParseGitHubDigest (handles "sha256:hash" format)
				algo, hash := feed.ParseGitHubDigest(digest)
				if algo == "sha256" {
					assetChecksum = hash
				}
			}
			break
		}
	}

	if assetURL == "" {
		return fmt.Errorf("could not find asset %s in release %s", filename, release.GetTagName())
	}

	if assetChecksum == "" {
		slog.Warn("No checksum available for Tailwind binary, downloading without verification", "version", release.GetTagName())
	}

	// Download using the downloader
	group := t.downloader.Download(ctx, &common.DownloadRequest{
		URL:         assetURL,
		Destination: binaryPath,
		Checksum:    assetChecksum,
	})
	_, err = group.Wait()
	if err != nil {
		return fmt.Errorf("could not download Tailwind: %w", err)
	}

	// Make executable
	if err := os.Chmod(binaryPath, 0700); err != nil {
		return fmt.Errorf("could not make binary executable: %w", err)
	}

	return nil
}

// getTailwindFilename returns the appropriate Tailwind binary filename for the current platform
func getTailwindFilename() string {
	arch := ""
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	default:
		arch = runtime.GOARCH
	}

	return "tailwindcss-" + runtime.GOOS + "-" + arch
}
