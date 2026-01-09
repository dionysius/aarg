package feed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/alitto/pond/v2"
	"github.com/aptly-dev/aptly/deb"
	"github.com/dionysius/aarg/debext"
	"github.com/dionysius/aarg/internal/common"
	"github.com/google/go-github/v80/github"
)

var (
	// githubNormalizeRegex matches characters that GitHub doesn't allow in filenames
	githubNormalizeRegex = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
)

// Github handles github release downloads
type Github struct {
	options    *FeedOptions
	repository *common.RepositoryOptions
	client     *github.Client
	owner      string
	repo       string
	verifier   *debext.Verifier
	storage    *common.Storage
	pool       pond.Pool
	collector  *common.GenericRetentionCollector[githubChanges]
}

// NewGithub creates a new Github feed
func NewGithub(storage *common.Storage, client *github.Client, verifier *debext.Verifier, options *FeedOptions, repository *common.RepositoryOptions, pool pond.Pool) (*Github, error) {
	// parse github repository
	parts := strings.SplitN(options.Name, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("repository must be in format 'owner/repo', got: %s", options.Name)
	}
	owner := parts[0]
	repo := parts[1]

	return &Github{
		options:    options,
		repository: repository,
		client:     client,
		owner:      owner,
		repo:       repo,
		verifier:   verifier,
		storage:    storage,
		pool:       pool,
		collector:  newGithubChangesRetentionCollector(repository.Retention),
	}, nil
}

// Run executes the complete download and verification process
func (s *Github) Run(ctx context.Context) error {
	// Create subpool for release processing (limit concurrent releases)
	releasePool := s.pool.NewSubpool(10)
	defer releasePool.StopAndWait()

	group := releasePool.NewGroup()

	// List all releases
	opt := &github.ListOptions{PerPage: 100}
	for {
		releases, resp, err := s.client.Repositories.ListReleases(ctx, s.owner, s.repo, opt)
		if err != nil {
			return err
		}

		// Process each release/tag
		for _, release := range releases {
			// Filter by release type
			if !s.matchesReleaseType(release) {
				continue
			}

			// Filter by tag pattern
			if !common.MatchesGlobPatterns(s.options.Tags, release.GetTagName()) {
				continue
			}

			group.SubmitErr(func() error {
				return s.processRelease(ctx, release)
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	// Wait for all releases to be processed (adds .changes files to collector)
	if err := group.Wait(); err != nil {
		return err
	}

	// Now process all kept .changes files according to retention policies
	changesPool := s.pool.NewSubpool(10)
	defer changesPool.StopAndWait()

	group = changesPool.NewGroup()
	keptChanges, err := s.collector.Kept()
	if err != nil {
		return err
	}
	for _, pkg := range keptChanges {
		group.SubmitErr(func() error {
			return s.processKeptChangesFile(ctx, pkg.changes, pkg.release)
		})
	}

	return group.Wait()
}

type githubChanges struct {
	changes *deb.Changes
	release *github.RepositoryRelease
}

func (s *Github) processRelease(ctx context.Context, release *github.RepositoryRelease) error {
	// Create subpool for processing .changes files in this release
	changesPool := s.pool.NewSubpool(10)
	defer changesPool.StopAndWait()

	group := changesPool.NewGroup()

	// Find .changes files in this release
	for _, asset := range release.Assets {
		if strings.HasSuffix(asset.GetName(), ".changes") {
			group.SubmitErr(func() error {
				return s.processChangesFile(ctx, asset, release)
			})
		}
	}

	return group.Wait()
}

func (s *Github) processChangesFile(ctx context.Context, changesAsset *github.ReleaseAsset, release *github.RepositoryRelease) error {
	tag := release.GetTagName()

	// Download .changes file if not already present
	// Use GitHub's digest for the .changes file itself
	algo, hash := ParseGitHubDigest(changesAsset.GetDigest())
	changesPath, err := s.storage.FileExistsOrDownload(ctx, algo, hash, changesAsset.GetBrowserDownloadURL(), tag, changesAsset.GetName())
	if err != nil {
		return err
	}

	changes, err := debext.ParseChanges(changesPath, s.verifier)
	if err != nil {
		return err
	}

	// Get distribution and source package from .changes file
	dist := changes.Distribution
	sourcePkgName := changes.Source

	// Check if distribution should be included based on configured mappings
	if !s.shouldIncludeDistribution(dist) {
		return nil
	}

	// Filter: Check if source should be included
	if !common.MatchesGlobPatterns(s.options.FromSources, sourcePkgName) {
		return nil
	}

	// Filter: Check if package should be included (check against source package name for .changes files)
	if !common.MatchesGlobPatterns(s.options.Packages, sourcePkgName) {
		return nil
	}

	// Add changes file to collector (changes files go in main component)
	if err := s.collector.Add(dist, common.MainComponent, githubChanges{changes: changes, release: release}); err != nil {
		return err
	}

	return nil
}

func (s *Github) processKeptChangesFile(ctx context.Context, changes *deb.Changes, release *github.RepositoryRelease) error {
	// Get distribution and source package from .changes file
	dist := changes.Distribution
	sourcePkgName := changes.Source

	group := s.pool.NewGroup()
	var fileResults [][]*common.FileForTrust

	for _, referencedFile := range changes.Files {
		// Add .dsc files if requested
		if strings.HasSuffix(referencedFile.Filename, ".dsc") && s.repository.Packages.Source {
			idx := len(fileResults)
			fileResults = append(fileResults, nil)
			group.SubmitErr(func() error {
				refFiles, err := s.processDscFile(ctx, referencedFile, release, dist, sourcePkgName)
				if err != nil {
					return err
				}
				fileResults[idx] = refFiles
				return nil
			})
		}

		// Add binary packages
		if strings.HasSuffix(referencedFile.Filename, ".deb") || strings.HasSuffix(referencedFile.Filename, ".ddeb") {
			// Skip debug packages if not included
			if debext.IsDebugByName(referencedFile.Filename) && !s.repository.Packages.Debug {
				continue
			}

			idx := len(fileResults)
			fileResults = append(fileResults, nil)
			group.SubmitErr(func() error {
				refFile, asset, err := s.downloadReferencedFileWithAsset(ctx, referencedFile, release)
				if err != nil {
					return err
				}

				assetURL := asset.GetBrowserDownloadURL()
				downloadURL := s.options.DownloadURL.String() + "/"

				if !strings.HasPrefix(assetURL, downloadURL) {
					return fmt.Errorf("asset URL %q does not start with expected download URL %q", assetURL, downloadURL)
				}
				relPath := strings.TrimPrefix(assetURL, downloadURL)
				fileResults[idx] = []*common.FileForTrust{{
					Path:         refFile,
					Distribution: dist,
					Hash:         referencedFile.Checksums.SHA256,
					Source:       sourcePkgName,
					Redirect:     relPath,
				}}
				return nil
			})
		}
	}

	// Wait for all files to be processed
	if err := group.Wait(); err != nil {
		return err
	}

	// Collect all downloaded files
	var downloadedFiles []*common.FileForTrust
	for _, files := range fileResults {
		downloadedFiles = append(downloadedFiles, files...)
	}

	return s.storage.LinkFilesToTrusted(ctx, downloadedFiles)
}

func (s *Github) processDscFile(ctx context.Context, file deb.PackageFile, release *github.RepositoryRelease, dist string, sourcePkg string) ([]*common.FileForTrust, error) {
	tag := release.GetTagName()

	asset, err := s.findFileInRelease(file, release)
	if err != nil {
		return nil, err
	}

	// Download .dsc file if not already present
	// Use checksum from .changes file (Debian chain of trust)
	dscPath, err := s.storage.FileExistsOrDownload(ctx, "sha256", file.Checksums.SHA256, asset.GetBrowserDownloadURL(), tag, file.Filename)
	if err != nil {
		return nil, err
	}

	// Parse .dsc file and get package with file list
	pkg, err := debext.ParseSource(dscPath, s.verifier, "")
	if err != nil {
		// Check if this is a missing signature or signature verification error
		if errors.Is(err, debext.ErrMissingSignature) || errors.Is(err, debext.ErrSignatureVerificationFailed) {
			// Only retry with unsigned verifier for signature-related errors
			unsignedVerifier := &debext.Verifier{
				Verifier:         s.verifier.Verifier,
				AcceptUnsigned:   true,
				IgnoreSignatures: s.verifier.IgnoreSignatures,
			}

			pkg, err = debext.ParseSource(dscPath, unsignedVerifier, "")
			if err != nil {
				return nil, err
			}

			// GitHub releases rely on checksum chain: .changes (signed) → .dsc
			slog.Warn("Accepting unsigned .dsc file since .changes is signed", "file", file.Filename)
		} else {
			// For non-signature errors, fail immediately
			return nil, err
		}
	}

	// Add .dsc file to results
	// Redirect uses original GitHub filename from asset
	assetURL := asset.GetBrowserDownloadURL()
	downloadURL := s.options.DownloadURL.String() + "/"

	if !strings.HasPrefix(assetURL, downloadURL) {
		return nil, fmt.Errorf("asset URL %q does not start with expected download URL %q", assetURL, downloadURL)
	}
	relPath := strings.TrimPrefix(assetURL, downloadURL)

	var downloadedFiles []*common.FileForTrust
	downloadedFiles = append(downloadedFiles, &common.FileForTrust{
		Path:         dscPath,
		Distribution: dist,
		Hash:         file.Checksums.SHA256,
		Source:       sourcePkg,
		Redirect:     relPath,
	})

	// Download all referenced files in parallel (excluding .dsc itself)
	group := s.pool.NewGroup()
	var additionalFiles []*common.FileForTrust

	for _, referencedFile := range pkg.Files() {
		if referencedFile.Filename == filepath.Base(dscPath) {
			continue
		}

		// Allocate entry in results slice
		idx := len(additionalFiles)
		additionalFiles = append(additionalFiles, &common.FileForTrust{
			Distribution: dist,
			Hash:         referencedFile.Checksums.SHA256,
			Source:       sourcePkg,
		})

		// Submit download as parallel task
		group.SubmitErr(func() error {
			refFile, refAsset, err := s.downloadReferencedFileWithAsset(ctx, referencedFile, release)
			if err != nil {
				return err
			}
			assetURL := refAsset.GetBrowserDownloadURL()
			downloadURL := s.options.DownloadURL.String() + "/"

			if !strings.HasPrefix(assetURL, downloadURL) {
				return fmt.Errorf("asset URL %q does not start with expected download URL %q", assetURL, downloadURL)
			}
			relPath := strings.TrimPrefix(assetURL, downloadURL)
			additionalFiles[idx].Path = refFile
			additionalFiles[idx].Redirect = relPath
			return nil
		})
	}

	// Wait for all downloads to complete
	if err := group.Wait(); err != nil {
		return nil, err
	}

	downloadedFiles = append(downloadedFiles, additionalFiles...)
	return downloadedFiles, nil
}

func (s *Github) downloadReferencedFileWithAsset(ctx context.Context, file deb.PackageFile, release *github.RepositoryRelease) (string, *github.ReleaseAsset, error) {
	tag := release.GetTagName()

	asset, err := s.findFileInRelease(file, release)
	if err != nil {
		return "", nil, err
	}

	// Download file if not already present
	// Use checksum from Debian metadata (.changes or .dsc file) to maintain chain of trust
	filePath, err := s.storage.FileExistsOrDownload(ctx, "sha256", file.Checksums.SHA256, asset.GetBrowserDownloadURL(), tag, file.Filename)
	return filePath, asset, err
}

// NormalizeGithubFilename converts Debian filename to GitHub's normalized form
// GitHub replaces special chars with dots, keeps alphanumeric, underscore, hyphen, dot
func NormalizeGithubFilename(name string) string {
	return githubNormalizeRegex.ReplaceAllString(name, ".")
}

// ParseGitHubDigest parses GitHub asset digest into algorithm and hash
// Returns algorithm (e.g., "sha256") and hex hash, or empty strings if digest is empty/invalid
func ParseGitHubDigest(digest string) (string, string) {
	if digest == "" {
		return "", ""
	}
	if idx := strings.Index(digest, ":"); idx >= 0 {
		return digest[:idx], digest[idx+1:]
	}
	// Assume sha256 if no prefix
	return "sha256", digest
}

func (s *Github) findFileInRelease(file deb.PackageFile, release *github.RepositoryRelease) (*github.ReleaseAsset, error) {
	githubFilename := NormalizeGithubFilename(file.Filename)
	var asset *github.ReleaseAsset

	// Find corresponding asset in release
	for _, a := range release.Assets {
		if a.GetName() == githubFilename {
			asset = a
			break
		}
	}
	if asset == nil {
		return nil, fmt.Errorf("could not find .dsc asset for file %s in release assets", file.Filename)
	}

	return asset, nil
}

// NewChangesRetentionCollector creates a collector for githubChanges items
// grouping by source name only and arch is always "source"
func newGithubChangesRetentionCollector(
	retention []common.RetentionPolicy,
) *common.GenericRetentionCollector[githubChanges] {
	return common.NewGenericRetentionCollector(
		retention,
		func(pkg githubChanges) (string, string, string, string) {
			return pkg.changes.Source, pkg.changes.Source, debext.SourceArchitecture, pkg.changes.GetField("Version")
		},
	)
}

// matchesReleaseType checks if a release matches the configured release type filters.
func (s *Github) matchesReleaseType(release *github.RepositoryRelease) bool {
	// Determine the release type
	var releaseType ReleaseType
	if release.GetDraft() {
		releaseType = ReleaseTypeDraft
	} else if release.GetPrerelease() {
		releaseType = ReleaseTypePrerelease
	} else {
		releaseType = ReleaseTypeRelease
	}

	// No filter = only normal releases
	if len(s.options.Releases) == 0 {
		return releaseType == ReleaseTypeRelease
	}

	return slices.Contains(s.options.Releases, releaseType)
}

// shouldIncludeDistribution checks if a distribution should be processed based on configured mappings.
// Returns true if no distributions are configured (discover mode) or if the distribution is in the feed mappings.
func (s *Github) shouldIncludeDistribution(dist string) bool {
	// No distributions configured = discover mode, include all
	if len(s.options.Distributions) == 0 {
		return true
	}

	// Check if distribution matches any feed distribution in mappings
	for _, distMap := range s.options.Distributions {
		if distMap.Feed == dist {
			return true
		}
	}

	return false
}
