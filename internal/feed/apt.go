package feed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/alitto/pond/v2"
	"github.com/aptly-dev/aptly/deb"
	"github.com/aptly-dev/aptly/utils"
	"github.com/dionysius/aarg/debext"
	"github.com/dionysius/aarg/internal/common"
)

// Apt handles APT repository downloads
type Apt struct {
	options    *FeedOptions
	repository *common.RepositoryOptions
	verifier   *debext.Verifier
	storage    *common.Storage
	pool       pond.Pool
}

// NewApt creates a single APT feed instance.
// Feed expansion should be done at the app level before calling this.
func NewApt(storage *common.Storage, verifier *debext.Verifier, options *FeedOptions, repository *common.RepositoryOptions, pool pond.Pool) (*Apt, error) {
	return &Apt{
		options:    options,
		repository: repository,
		verifier:   verifier,
		storage:    storage,
		pool:       pool,
	}, nil
}

// ExpandAptFeedOptions expands a single APT FeedOptions with prefix mappings
// into multiple FeedOptions, one per distribution.
func ExpandAptFeedOptions(options *FeedOptions) []*FeedOptions {
	var expandedOptions []*FeedOptions

	for _, distMap := range options.Distributions {
		// Parse distribution mapping for prefix
		// Format: "prefix/distname" or "/" for flat repo
		var prefix, distName, targetDist string

		if strings.Contains(distMap.Feed, "/") {
			parts := strings.SplitN(distMap.Feed, "/", 2)
			prefix = parts[0]
			if len(parts) == 2 {
				distName = parts[1]
			}
			// If no distName after "/", it's a flat repo
			if distName == "" {
				distName = "/"
			}
		} else {
			// No prefix, use distribution name as-is
			distName = distMap.Feed
		}

		// Determine target distribution
		// If explicit target mapping exists, use it; otherwise use distName
		if distMap.Target != "" {
			targetDist = distMap.Target
		} else {
			targetDist = distName
		}

		// Build download URL
		downloadURL := options.DownloadURL
		if prefix != "" {
			// Add prefix to URL path (even for flat repos with prefix like "Debian_12/")
			downloadURL = options.DownloadURL.JoinPath(prefix)
		}

		// Build relative path for storage
		relativePath := options.RelativePath
		if prefix != "" {
			// Add prefix to storage path to prevent collisions (even for flat repos)
			relativePath = relativePath + "/" + prefix
		}

		// Create feed options for this single distribution
		singleOptions := &FeedOptions{
			Name:          downloadURL.Host + downloadURL.Path,
			Type:          FeedTypeAPT,
			DownloadURL:   downloadURL,
			ProjectURL:    options.ProjectURL,
			RelativePath:  relativePath,
			Distributions: []DistributionMap{{Feed: distName, Target: targetDist}},
			FromSources:   options.FromSources,
			Packages:      options.Packages,
		}

		expandedOptions = append(expandedOptions, singleOptions)
	}

	return expandedOptions
}

// Run executes the complete download and verification process
func (s *Apt) Run(ctx context.Context) error {
	// Create subpool for distribution processing
	distPool := s.pool.NewSubpool(10)
	defer distPool.StopAndWait()

	group := distPool.NewGroup()

	// Process each dist
	for _, distMap := range s.options.Distributions {
		group.SubmitErr(func() error {
			return s.processDist(ctx, distMap)
		})
	}

	// Wait for all dists to complete and return first error if any
	return group.Wait()
}

func (s *Apt) processDist(ctx context.Context, distMap DistributionMap) error {
	// Track all downloaded files for this dist
	var allFiles []*common.FileForTrust

	// Determine if this is a flat repository (Feed == "/")
	isFlat := distMap.Feed == "/"
	localPath := distMap.Feed
	if isFlat {
		localPath = "."
	}

	var releaseURL string

	if isFlat {
		// Flat repository: Release file is at DownloadURL/InRelease
		releaseURL = s.options.DownloadURL.JoinPath("InRelease").String()
	} else {
		// Standard repository: Release file is at DownloadURL/dists/dist/InRelease
		releaseURL = s.options.DownloadURL.JoinPath("dists", distMap.Feed, "InRelease").String()
	}

	releasePath := s.storage.GetDownloadPath(localPath, "InRelease")

	// Submit download request and wait for completion
	req := &common.DownloadRequest{
		URL:         releaseURL,
		Destination: filepath.Join(localPath, "InRelease"),
	}
	group := s.storage.Download(ctx, req)
	_, err := group.Wait()
	if err != nil {
		return err
	}

	// Parse Release file to get index file checksums
	release, err := debext.ParseRelease(releasePath, s.verifier)
	if err != nil {
		return fmt.Errorf("failed to parse InRelease: %w", err)
	}

	// Construct distribution path infix for URL construction
	// Flat repos: "", Standard repos: "/dists/{dist}"
	var urlPath string
	if !isFlat {
		urlPath = fmt.Sprintf("/dists/%s", distMap.Feed)
	}

	// Find and process all package indices from the Release file
	packageFiles, err := s.processIndices(ctx, localPath, release, urlPath)
	if err != nil {
		return fmt.Errorf("failed to process indices: %w", err)
	}
	allFiles = append(allFiles, packageFiles...)

	// All verified, link to trusted
	return s.storage.LinkFilesToTrusted(ctx, allFiles)
}

func (s *Apt) processIndices(ctx context.Context, localPath string, release *debext.Release, urlPath string) ([]*common.FileForTrust, error) {
	// Find all unique package/source indices
	indices := s.findUniqueBaseIndices(release)

	// Process all indices in parallel
	// Create subpool for index processing
	indexPool := s.pool.NewSubpool(10)
	defer indexPool.StopAndWait()

	group := indexPool.NewGroup()
	var results [][]*common.FileForTrust

	for _, basePath := range indices {
		idx := len(results)
		results = append(results, nil)
		group.SubmitErr(func() error {
			files, err := s.processIndex(ctx, localPath, basePath, release, urlPath)
			if err != nil {
				return fmt.Errorf("failed to process %s: %w", basePath, err)
			}
			results[idx] = files
			return nil
		})
	}

	// Wait for all indices to complete
	if err := group.Wait(); err != nil {
		return nil, err
	}

	// Collect results
	var allFiles []*common.FileForTrust
	for _, files := range results {
		allFiles = append(allFiles, files...)
	}

	return allFiles, nil
}

func (s *Apt) processIndex(ctx context.Context, localPath string, indexPath string, release *debext.Release, urlPath string) ([]*common.FileForTrust, error) {
	isSource := filepath.Base(indexPath) == "Sources"
	var allFiles []*common.FileForTrust

	// Get uncompressed file info for hash verification
	uncompressedInfo, ok := release.Files[indexPath]
	if !ok {
		return nil, fmt.Errorf("file %s not found in Release", indexPath)
	}

	// Select smallest file variant (best compression if available)
	compressedPath, compressedInfo, err := selectSmallestFile(indexPath, release.Files)
	if err != nil {
		return nil, err
	}

	// Construct download URL
	downloadURL := s.options.DownloadURL.JoinPath(urlPath, compressedPath).String()
	var result string

	// Download and optionally decompress the index
	if compressedPath == indexPath {
		// Download uncompressed file since best variant
		result, err = s.storage.FileExistsOrDownload(ctx, "sha256", uncompressedInfo.SHA256, downloadURL, localPath, indexPath)
		if err != nil {
			return nil, fmt.Errorf("failed to download %s: %w", indexPath, err)
		}
	} else {
		// Download and decompress if needed
		compressionFormat := common.DetectCompressionFormat(compressedPath)
		result, err = s.storage.UncompressedFileExistsOrDownloadAndDecompress(
			ctx, "sha256", uncompressedInfo.SHA256, compressedInfo.SHA256, downloadURL, compressionFormat, localPath, indexPath,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to download and decompress %s (compressed: %s): %w", indexPath, compressedPath, err)
		}
	}

	uncompressedPath := result

	// Parse index
	pkgs, err := debext.ParsePackageIndex(uncompressedPath, isSource)
	if err != nil {
		return nil, err
	}

	// Download all packages for this index
	// Use localPath as the distribution for organizing downloaded files
	packageFiles, err := s.downloadPackageFiles(ctx, localPath, pkgs)
	if err != nil {
		return nil, fmt.Errorf("failed to download packages: %w", err)
	}

	allFiles = append(allFiles, packageFiles...)
	return allFiles, nil
}

func (s *Apt) downloadPackageFiles(ctx context.Context, dist string, packages []*deb.Package) ([]*common.FileForTrust, error) {
	// Collect packages and filter
	// Use NoMatchKeep to preserve packages with unexpected version formats
	collector := common.NewPackageRetentionCollector(s.repository.Retention)

	for _, pkg := range packages {
		// Filter by source first
		if !common.MatchesGlobPatterns(s.options.FromSources, debext.GetSourceNameFromPackage(pkg)) {
			continue
		}

		// Filter by package name
		if !common.MatchesGlobPatterns(s.options.Packages, pkg.Name) {
			continue
		}

		// Determine component based on package type
		component := common.MainComponent
		if debext.IsDebugPackage(pkg) {
			component = common.DebugComponent
		}

		if err := collector.Add(dist, component, pkg); err != nil {
			return nil, err
		}
	}

	// Get kept packages after retention filtering
	keptPackages, err := collector.Kept()
	if err != nil {
		return nil, err
	}

	// Download all files in parallel using pond group
	// Create subpool for package downloads
	pkgPool := s.pool.NewSubpool(10)
	defer pkgPool.StopAndWait()

	group := pkgPool.NewGroup()
	var downloadedFiles []*common.FileForTrust

	for _, pkg := range keptPackages {
		// Get source package name for FileForTrust
		sourcePkgName := debext.GetSourceNameFromPackage(pkg)

		for _, file := range pkg.Files() {
			relPath := file.DownloadURL()
			sha256 := file.Checksums.SHA256
			pkgURL := s.options.DownloadURL.JoinPath(relPath).String()

			// Allocate entry in results slice
			idx := len(downloadedFiles)
			downloadedFiles = append(downloadedFiles, &common.FileForTrust{
				Distribution: dist,
				Hash:         sha256,
				Source:       sourcePkgName,
				Redirect:     relPath,
			})

			// Submit download as parallel task
			group.SubmitErr(func() error {
				path, err := s.storage.FileExistsOrDownload(ctx, "sha256", sha256, pkgURL, relPath)
				if err != nil {
					return err
				}
				downloadedFiles[idx].Path = path

				// Verify .dsc file signatures after download
				if strings.HasSuffix(relPath, ".dsc") && s.repository.Packages.Source {
					if err := s.verifyDscFile(path, file.Filename); err != nil {
						return err
					}
				}

				return nil
			})
		}
	}

	// Wait for all downloads to complete
	if err := group.Wait(); err != nil {
		return nil, err
	}

	return downloadedFiles, nil
}

// selectSmallestFile selects the file with the smallest size from files matching the basePath prefix
// Returns the filename and ChecksumInfo of the smallest file, or an error if no matching files found
func selectSmallestFile(basePath string, filesMap map[string]utils.ChecksumInfo) (string, utils.ChecksumInfo, error) {
	var smallestPath string
	var smallestInfo utils.ChecksumInfo
	found := false

	for path, checksums := range filesMap {
		// Check if path starts with basePath (e.g., "main/binary-amd64/Packages")
		if !strings.HasPrefix(path, basePath) {
			continue
		}

		if !found || checksums.Size < smallestInfo.Size {
			smallestPath = path
			smallestInfo = checksums
			found = true
		}
	}

	if !found {
		return "", utils.ChecksumInfo{}, fmt.Errorf("no files found matching base path: %s", basePath)
	}

	return smallestPath, smallestInfo, nil
}

// findUniqueBaseIndices returns a deduplicated list of package/source index base paths from the release file
func (s *Apt) findUniqueBaseIndices(release *debext.Release) []string {
	var indices []string
	seen := make(map[string]bool)

	for path := range release.Files {
		// Strip compression extension to get base path
		basePath := strings.TrimSuffix(path, filepath.Ext(path))

		// Skip if we've already seen this base path
		if seen[basePath] {
			continue
		}

		baseName := filepath.Base(basePath)

		// Identify Packages or Sources indices
		if baseName == "Packages" {
			// Always include Packages indices
		} else if baseName == "Sources" {
			if !s.repository.Packages.Source {
				continue
			}
		} else {
			continue
		}

		seen[basePath] = true
		indices = append(indices, basePath)
	}

	return indices
}

// verifyDscFile verifies the signature of a .dsc file, accepting unsigned files with a warning
func (s *Apt) verifyDscFile(dscPath, filename string) error {
	// Try to parse with signature verification first
	_, err := debext.ParseSource(dscPath, s.verifier, "")
	if err != nil {
		// Check if this is a missing signature or signature verification error
		if errors.Is(err, debext.ErrMissingSignature) || errors.Is(err, debext.ErrSignatureVerificationFailed) {
			// Only retry with unsigned verifier for signature-related errors
			unsignedVerifier := &debext.Verifier{
				Verifier:         s.verifier.Verifier,
				AcceptUnsigned:   true,
				IgnoreSignatures: s.verifier.IgnoreSignatures,
			}

			_, err = debext.ParseSource(dscPath, unsignedVerifier, "")
			if err != nil {
				return err
			}

			// OBS doesn't sign them even when their input dsc is signed. Debian policy says "possibly surrounded", so optional:
			// https://www.debian.org/doc/debian-policy/ch-controlfields.html#debian-source-package-control-files-dsc
			slog.Warn("Accepting unsigned .dsc file since Release is signed", "file", filename)
		} else {
			// For non-signature errors, fail immediately
			return err
		}
	}

	return nil
}
