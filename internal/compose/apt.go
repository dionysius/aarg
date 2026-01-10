package compose

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alitto/pond/v2"
	"github.com/aptly-dev/aptly/deb"
	"github.com/aptly-dev/aptly/pgp"
	"github.com/aptly-dev/aptly/utils"
	"github.com/dionysius/aarg/debext"
	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/feed"
	"gopkg.in/yaml.v3"
)

// Apt composes Debian repository structure from trusted files
type Apt struct {
	options      *AptComposeOptions                              // Configuration options
	collector    *common.GenericRetentionCollector[*deb.Package] // Collects packages with retention filtering
	verifier     *debext.Verifier                                // Verifier for package signatures
	signer       pgp.Signer                                      // Signer for Release files
	decompressor *common.DeCompressor                            // Decompressor for package files
	pool         pond.Pool                                       // Coordination pool for parallel operations
	redirectMaps map[string]map[string]string                    // Redirect maps per feed (feedRelPath -> map[relPath]redirect), immutable after loading
}

// NewApt creates a new Apt composer
func NewApt(options *AptComposeOptions, verifier *debext.Verifier, signer pgp.Signer, decompressor *common.DeCompressor, pool pond.Pool) *Apt {
	return &Apt{
		options:      options,
		collector:    common.NewPackageRetentionCollector(options.Repository.Retention),
		verifier:     verifier,
		signer:       signer,
		decompressor: decompressor,
		pool:         pool,
		redirectMaps: make(map[string]map[string]string),
	}
}

// Compose generates the apt repository structure and returns the repository object
func (a *Apt) Compose(ctx context.Context) (*debext.Repository, error) {
	// Load redirect maps if in redirect mode
	if a.options.PoolMode == "redirect" {
		if err := a.loadRedirectMaps(); err != nil {
			return nil, err
		}
	}

	// Process all feeds in parallel
	// Create subpool for feed processing
	feedPool := a.pool.NewSubpool(10)
	defer feedPool.StopAndWait()

	group := feedPool.NewGroup()
	for _, feed := range a.options.Feeds {
		group.SubmitErr(func() error {
			return a.processFeed(feed)
		})
	}

	if err := group.Wait(); err != nil {
		return nil, err
	}

	repo := a.buildRepository()
	if err := a.generateRepository(ctx, repo); err != nil {
		return nil, err
	}

	return repo, nil
}

func (a *Apt) generateRepository(ctx context.Context, repository *debext.Repository) error {
	// Parallelize distribution generation
	// Create subpool for distribution generation
	distPool := a.pool.NewSubpool(10)
	defer distPool.StopAndWait()

	group := distPool.NewGroup()

	for _, dist := range repository.GetDistributions() {
		group.SubmitErr(func() error {
			return a.generateDistribution(ctx, repository, dist)
		})
	}

	return group.Wait()
}

// generateDistribution generates repository structure for a single distribution
func (a *Apt) generateDistribution(ctx context.Context, repo *debext.Repository, dist string) error {
	comps := []string{common.MainComponent}

	// Include debug component if enabled
	if a.options.Repository.Packages.Debug {
		comps = append(comps, common.DebugComponent)
	}

	// Collect index files from all components
	var allIndexFiles sync.Map

	// Parallelize components since each component has its own PackageList
	// Create subpool for component processing
	compPool := a.pool.NewSubpool(10)
	defer compPool.StopAndWait()

	group := compPool.NewGroup()

	for _, comp := range comps {
		group.SubmitErr(func() error {
			arches := repo.GetArchitectures(dist, comp, a.options.Repository.Packages.Source)

			// Process architectures sequentially - PackageList is not thread-safe
			for _, arch := range arches {
				files, err := a.generatePackageIndex(ctx, repo, dist, comp, arch)
				if err != nil {
					return err
				}

				for k, v := range files {
					allIndexFiles.Store(k, v)
				}

				if err := a.linkPackagesToPool(repo, dist, comp, arch); err != nil {
					return err
				}
			}

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return err
	}

	// Convert sync.Map to regular map for generateRelease
	indexFilesMap := make(map[string]utils.ChecksumInfo)
	allIndexFiles.Range(func(key, value any) bool {
		indexFilesMap[key.(string)] = value.(utils.ChecksumInfo)
		return true
	})

	// Generate distribution-level Release file if there are any index files
	if len(indexFilesMap) > 0 {
		if err := a.generateRelease(repo, dist, indexFilesMap); err != nil {
			return err
		}
	}

	return nil
}

func (a *Apt) generatePackageIndex(ctx context.Context, repo *debext.Repository, dist, comp, arch string) (map[string]utils.ChecksumInfo, error) {
	indexFiles := make(map[string]utils.ChecksumInfo)

	isSource := arch == debext.SourceArchitecture
	archDirname := "binary-" + arch
	if isSource {
		archDirname = debext.SourceArchitecture
	}

	relArchDirpath := filepath.Join(comp, archDirname)
	archDirPath := filepath.Join(a.options.Target, "dists", dist, relArchDirpath)

	// Get the full package list for this distribution and component
	allPackages := repo.GetPackageList(dist, comp)
	allPackages.PrepareIndex()

	// Filter packages by architecture using aptly's query
	pkgList, err := allPackages.Filter(deb.FilterOptions{
		Queries: []deb.PackageQuery{&deb.FieldQuery{Field: "$Architecture", Relation: deb.VersionEqual, Value: arch}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to filter packages for architecture %s: %w", arch, err)
	}

	if pkgList == nil && isSource && a.options.Repository.Packages.Source {
		// Special case: create empty Sources index if source packages are enabled
		// Debug component typically has no sources, so no warning needed
		if comp == common.MainComponent {
			slog.Warn("empty source package list but sources are enabled", "dist", dist, "component", comp)
		}

		pkgList = deb.NewPackageList()
	}

	if pkgList != nil {
		if err := os.MkdirAll(archDirPath, 0755); err != nil {
			return nil, err
		}

		indexFilename := "Packages"
		if isSource {
			indexFilename = "Sources"
		}

		targetFilepath := filepath.Join(archDirPath, indexFilename)

		f, err := os.Create(targetFilepath)
		if err != nil {
			return nil, err
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}()

		if err := debext.GeneratePackageIndex(f, pkgList, isSource); err != nil {
			return nil, err
		}

		indexFiles[relArchDirpath+"/"+indexFilename], err = utils.ChecksumsForFile(targetFilepath)
		if err != nil {
			return nil, err
		}

		// Compress index file with all formats in parallel
		group := a.decompressor.Compress(ctx, targetFilepath,
			common.CompressionGzip,
			common.CompressionBzip2,
			common.CompressionXZ,
		)
		results, err := group.Wait()
		if err != nil {
			return nil, err
		}

		// Generate checksums for compressed files
		for _, result := range results {
			compressedFilepath := result.Destination()
			compressedFilename := filepath.Base(compressedFilepath)

			indexFiles[relArchDirpath+"/"+compressedFilename], err = utils.ChecksumsForFile(compressedFilepath)
			if err != nil {
				return nil, err
			}
		}
	}

	return indexFiles, nil
}

// linkPackagesToPool creates hardlinks for all package files from trusted storage to output pool
func (a *Apt) linkPackagesToPool(repo *debext.Repository, dist, comp, arch string) error {
	// In redirect mode no hardlinks to public pool needed
	if a.options.PoolMode == "redirect" {
		return nil
	}

	// Get the full package list for this distribution and component
	allPackages := repo.GetPackageList(dist, comp)
	allPackages.PrepareIndex()

	// Filter and link packages by architecture using aptly's query
	pkgList, err := allPackages.Filter(deb.FilterOptions{
		Queries: []deb.PackageQuery{&deb.FieldQuery{Field: "$Architecture", Relation: deb.VersionEqual, Value: arch}},
	})
	if err != nil {
		return fmt.Errorf("failed to filter packages for architecture %s: %w", arch, err)
	}

	if err := pkgList.ForEach(func(pkg *deb.Package) error {
		return a.linkPackageFile(pkg, comp)
	}); err != nil {
		return err
	}

	return nil
}

// linkPackageFile creates hardlinks for a package and referenced files (hierarchical mode)
// or skips hardlink creation (redirect mode - paths already updated during collection)
func (a *Apt) linkPackageFile(pkg *deb.Package, comp string) error {
	source := debext.GetSourceNameFromPackage(pkg)
	relTargetDir := debext.GetPoolPath(comp, source)

	var relOrigDir string
	if pkg.IsSource {
		relOrigDir = pkg.Stanza()["Directory"]
	} else {
		relOrigFilename := pkg.Stanza()["Filename"]
		relOrigDir = filepath.Dir(relOrigFilename)
	}

	if err := os.MkdirAll(filepath.Join(a.options.Target, relTargetDir), 0755); err != nil {
		return err
	}

	if pkg.IsSource {
		// Update Directory field to new relative pool path
		pkg.Stanza()["Directory"] = relTargetDir
	} else {
		relOrigFilename := pkg.Stanza()["Filename"]
		filename := filepath.Base(relOrigFilename)

		// Update Filename field to new relative pool path
		pkg.Stanza()["Filename"] = filepath.Join(relTargetDir, filename)
	}

	for _, file := range pkg.Files() {
		sourcePath := filepath.Join(a.options.Trusted, relOrigDir, file.Filename)
		targetPath := filepath.Join(a.options.Target, relTargetDir, file.Filename)
		if err := common.EnsureHardlink(sourcePath, targetPath); err != nil {
			return err
		}
	}

	return nil
}

// generateRelease generates the distribution-level Release file
func (a *Apt) generateRelease(repo *debext.Repository, dist string, files map[string]utils.ChecksumInfo) error {
	// Collect all unique architectures from all components
	archSet := make(map[string]struct{})
	for _, comp := range repo.GetComponents(dist) {
		for _, arch := range repo.GetArchitectures(dist, comp, false) {
			archSet[arch] = struct{}{}
		}
	}

	// Convert to sorted slice
	arches := make([]string, 0, len(archSet))
	for arch := range archSet {
		arches = append(arches, arch)
	}
	slices.Sort(arches)

	release := debext.Release{
		Origin:        a.options.Name + " " + dist,
		Label:         a.options.Name + " " + dist,
		Suite:         dist,
		Codename:      dist,
		Date:          time.Now(),
		Architectures: arches,
		Components:    repo.GetComponents(dist),
		Description:   "Generated by aarg",
		Files:         files,
	}

	targetDirpath := filepath.Join(a.options.Target, "dists", dist)
	releaseFilepath := filepath.Join(targetDirpath, "Release")

	f, err := os.Create(releaseFilepath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if err := debext.GenerateRelease(f, release); err != nil {
		return err
	}

	inReleasePath := filepath.Join(targetDirpath, "InRelease")
	if err := a.signer.ClearSign(releaseFilepath, inReleasePath); err != nil {
		return err
	}

	releaseGpgPath := releaseFilepath + ".gpg"
	if err := a.signer.DetachedSign(releaseFilepath, releaseGpgPath); err != nil {
		return err
	}

	return nil
}

// processFeed processes a single feed for all relevant distributions
func (a *Apt) processFeed(feedOpts *feed.FeedOptions) error {
	// Build list of distributions to process
	var distsToProcess []feed.DistributionMap

	if len(feedOpts.Distributions) == 0 {
		// Feed has no distribution mappings: discover all available distributions from trusted dir
		feedBasePath := filepath.Join(a.options.Trusted, feedOpts.RelativePath)
		entries, err := os.ReadDir(feedBasePath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // No feed directory means no packages
			}
			return err
		}

		// Use directory names as both feed and target distributions (identity mapping)
		for _, entry := range entries {
			if entry.IsDir() {
				distName := entry.Name()
				// If repository has explicit distributions, filter by them
				if len(a.options.Repository.Distributions) == 0 || slices.Contains(a.options.Repository.Distributions, distName) {
					distsToProcess = append(distsToProcess, feed.DistributionMap{Feed: distName, Target: distName})
				}
			}
		}
	} else {
		// Feed has explicit distribution mappings
		for _, distMap := range feedOpts.Distributions {
			// If repository has explicit distributions, only include mappings that target them
			if len(a.options.Repository.Distributions) == 0 || slices.Contains(a.options.Repository.Distributions, distMap.Target) {
				distsToProcess = append(distsToProcess, distMap)
			}
		}
	}

	// Process all distributions in parallel
	// Create subpool for feed distribution processing
	feedDistPool := a.pool.NewSubpool(10)
	defer feedDistPool.StopAndWait()

	group := feedDistPool.NewGroup()
	for _, distMap := range distsToProcess {
		group.SubmitErr(func() error {
			return a.processFeedDist(feedOpts, distMap.Feed, distMap.Target)
		})
	}

	return group.Wait()
}

// processFeedDist processes packages from a specific feed distribution directory
func (a *Apt) processFeedDist(feedOpts *feed.FeedOptions, feedDist, targetDist string) error {
	distPath := filepath.Join(a.options.Trusted, feedOpts.RelativePath, feedDist)

	// Walk directory and process each file
	return filepath.Walk(distPath, func(absPath string, info os.FileInfo, err error) error {
		if err != nil {
			// If the path does not exist, skip it (no packages for this dist)
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Compute relative path from TrustedDir for package metadata
		relPath, err := filepath.Rel(a.options.Trusted, absPath)
		if err != nil {
			return err
		}

		return a.processFeedPackageFile(feedOpts, relPath, targetDist)
	})
}

// processFeedPackageFile parses and filters a single package file
func (a *Apt) processFeedPackageFile(feedOpts *feed.FeedOptions, relPath string, dist string) error {
	// Parse the file using relative path
	pkg, err := a.parseFile(relPath)
	if err != nil {
		return err
	}
	if pkg == nil {
		return nil
	}

	component := common.MainComponent

	// Filter whether source or debug packages are included
	if pkg.IsSource && !a.options.Repository.Packages.Source {
		return nil
	}

	if debext.IsDebugPackage(pkg) {
		if !a.options.Repository.Packages.Debug {
			return nil
		}

		component = common.DebugComponent
	}

	// Filter by architecture if specified
	if len(a.options.Repository.Architectures) > 0 {
		allowedArchs := append(a.options.Repository.Architectures, debext.AllArchitecture)
		// Always allow source architecture if source packages are enabled
		if a.options.Repository.Packages.Source {
			allowedArchs = append(allowedArchs, debext.SourceArchitecture)
		}
		if !slices.Contains(allowedArchs, pkg.Architecture) {
			return nil
		}
	}

	// Filter by source name patterns if specified
	if len(feedOpts.FromSources) > 0 {
		sourceName := debext.GetSourceNameFromPackage(pkg)
		if !common.MatchesGlobPatterns(feedOpts.FromSources, sourceName) {
			return nil // Skip packages not matching source patterns
		}
	}

	// Filter by package name patterns if specified
	if len(feedOpts.Packages) > 0 {
		if !common.MatchesGlobPatterns(feedOpts.Packages, pkg.Name) {
			return nil // Skip packages not matching package name patterns
		}
	}

	// If in redirect mode, update package metadata with redirect paths for feeds with modified filenames
	if a.options.PoolMode == "redirect" {
		// For GitHub feeds with source packages, normalize and write to public/dsc/
		if pkg.IsSource && feed.FeedType(feedOpts.Type) == feed.FeedTypeGitHub {
			if err := a.normalizeGithubSourcePackage(&pkg, relPath, feedOpts.RelativePath); err != nil {
				return err
			}
			// Normalized package already has final Directory path set, skip redirect application
		} else {
			// Apply redirects to non-normalized packages
			if err := a.applyRedirectsToPackage(&pkg, feedOpts.RelativePath, relPath); err != nil {
				return err
			}
		}
	}

	// Add to collector with the appropriate component
	return a.collector.Add(dist, component, pkg)
}

// parseFile parses a package file into a deb.Package
// relPath is relative to TrustedDir and is stored in package metadata
func (a *Apt) parseFile(relPath string) (*deb.Package, error) {
	absPath := filepath.Join(a.options.Trusted, relPath)
	filename := filepath.Base(relPath)
	ext := strings.ToLower(filepath.Ext(relPath))

	// Skip debug packages if not included
	if !a.options.Repository.Packages.Debug && debext.IsDebugByName(filename) {
		return nil, nil
	}

	// Parse binary packages
	if ext == ".deb" || ext == ".ddeb" {
		return debext.ParseBinary(absPath, filepath.Dir(relPath))
	}

	// Parse source packages
	if ext == ".dsc" {
		if !a.options.Repository.Packages.Source {
			return nil, nil
		}

		// Parse .dsc file
		// Files in trusted storage were already verified during fetch
		pkg, err := debext.ParseSource(absPath, a.verifier, filepath.Dir(relPath))
		if err != nil {
			return nil, err
		}

		// Calculate complete checksums for all files (including SHA512)
		// Original .dsc may only contain MD5, SHA1, SHA256
		completeFiles := make([]deb.PackageFile, 0, len(pkg.Files()))
		for _, file := range pkg.Files() {
			filePath := filepath.Join(a.options.Trusted, pkg.Stanza()["Directory"], file.Filename)
			checksums, err := utils.ChecksumsForFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to calculate checksums for %s: %w", filePath, err)
			}
			completeFiles = append(completeFiles, deb.PackageFile{
				Filename:  file.Filename,
				Checksums: checksums,
			})
		}
		pkg.UpdateFiles(completeFiles)

		return pkg, nil
	}

	return nil, nil
}

// buildRepository builds a debext.Repository from all retained packages in the collector
func (a *Apt) buildRepository() *debext.Repository {
	repo := debext.NewRepository()

	// Add all kept packages to the repository with their distribution and component information
	_ = a.collector.ForEachKept(func(dist, component, _, _ string, pkg *deb.Package) error {
		return repo.AddPackage(pkg, dist, component)
	})

	return repo
}

// loadRedirectMaps loads redirects.yaml for each feed in redirect mode
func (a *Apt) loadRedirectMaps() error {
	for _, feedOpts := range a.options.Feeds {
		redirectMapPath := filepath.Join(a.options.Trusted, feedOpts.RelativePath, "redirects.yaml")

		// Check if redirect map exists
		if _, err := os.Stat(redirectMapPath); os.IsNotExist(err) {
			continue // No redirect map for this feed, skip
		} else if err != nil {
			return err
		}

		// Read and parse redirect map
		data, err := os.ReadFile(redirectMapPath)
		if err != nil {
			return err
		}

		var redirectMap map[string]string
		if err := yaml.Unmarshal(data, &redirectMap); err != nil {
			return err
		}

		// Store in redirectMaps
		a.redirectMaps[feedOpts.RelativePath] = redirectMap
	}

	return nil
}

// getRedirectTarget looks up the redirect target for a file path
// relPath is relative to trusted directory
// Returns error if redirect map exists but file not found in it
func (a *Apt) getRedirectTarget(relPath, feedRelPath string) (string, error) {
	fileRelPath := strings.TrimPrefix(relPath, feedRelPath+string(filepath.Separator))

	redirectMap, exists := a.redirectMaps[feedRelPath]
	if !exists {
		return "", fmt.Errorf("no redirect map found for feed %s", feedRelPath)
	}

	redirect, exists := redirectMap[fileRelPath]
	if !exists {
		return "", fmt.Errorf("no redirect target found for %s in feed %s", fileRelPath, feedRelPath)
	}

	return redirect, nil
}

// normalizeGithubSourcePackage normalizes a GitHub source package for GitHub URL compatibility.
//
// GitHub artifact download URLs don't support special chars in filenames, forcing us to use dots
// instead (e.g., "immich_2.4.1-0alpha3~noble.dsc" becomes "immich_2.4.1-0alpha3.noble.dsc").
// Since apt clients expect version strings to match filenames when downloading source packages,
// we must also normalize the Version field to match the renamed files. This enables wildcard
// redirects to work correctly. Additionally, we must host a normalized .dsc file ourselves
// since the original .dsc references the old filenames with tildes, which wouldn't match the
// actual downloadable files. This function:
//
// 1. Reads the original signed .dsc file and strips the signature
// 2. Normalizes all filenames and version strings (tildes → dots) in the .dsc content
// 3. Writes the normalized .dsc to public/dsc/{feedRelPath}/{redirectTargetDir}/
// 4. Updates the package object in place with:
//   - Normalized version string
//   - Normalized filenames in Files list (with updated .dsc checksums)
//   - Final Directory path pointing to pool redirect target
func (a *Apt) normalizeGithubSourcePackage(pkg **deb.Package, relPath, feedRelPath string) error {
	originalDscPath := filepath.Join(a.options.Trusted, relPath)

	// Read and strip signature from original .dsc file
	dscFile, err := os.Open(originalDscPath)
	if err != nil {
		return err
	}
	defer dscFile.Close()

	clearedReader, _, err := a.verifier.VerifyAndClear(dscFile)
	if err != nil {
		return fmt.Errorf("failed to clear signature: %w", err)
	}
	defer clearedReader.Close()

	dscContent, err := io.ReadAll(clearedReader)
	if err != nil {
		return fmt.Errorf("failed to read cleared content: %w", err)
	}

	dscText := string(dscContent)
	originalDscFilename := filepath.Base(relPath)
	normalizedDscFilename := feed.NormalizeGithubFilename(originalDscFilename)
	stanza := (*pkg).Stanza()
	originalVersion := stanza["Version"]
	normalizedVersion := feed.NormalizeGithubFilename(originalVersion)

	// Normalize version string in .dsc content
	if originalVersion != normalizedVersion {
		dscText = strings.ReplaceAll(dscText, originalVersion, normalizedVersion)
	}

	// Build normalized file list and replace filenames in .dsc content
	normalizedFiles := make([]deb.PackageFile, 0, len((*pkg).Files()))
	for _, file := range (*pkg).Files() {
		normalizedFilename := feed.NormalizeGithubFilename(file.Filename)

		if file.Filename != normalizedFilename {
			dscText = strings.ReplaceAll(dscText, file.Filename, normalizedFilename)
		}

		normalizedFiles = append(normalizedFiles, deb.PackageFile{
			Filename:  normalizedFilename,
			Checksums: file.Checksums, // Original checksums, will update .dsc below
		})
	}

	// Determine output directory from redirect map
	redirectTarget, err := a.getRedirectTarget(relPath, feedRelPath)
	if err != nil {
		return err
	}
	targetDir := filepath.Dir(redirectTarget)
	dscPath := filepath.Join(a.options.Target, "dsc", feedRelPath, targetDir, normalizedDscFilename)

	// Write normalized .dsc file
	if err := os.MkdirAll(filepath.Dir(dscPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(dscPath, []byte(dscText), 0644); err != nil {
		return err
	}

	// Calculate checksums for the new normalized .dsc file
	dscChecksums, err := utils.ChecksumsForFile(dscPath)
	if err != nil {
		return fmt.Errorf("failed to calculate checksums for %s: %w", dscPath, err)
	}

	// Update .dsc entry with new checksums
	for i := range normalizedFiles {
		if normalizedFiles[i].Filename == normalizedDscFilename {
			normalizedFiles[i].Checksums = dscChecksums
			break
		}
	}

	// Update package object: files first, then version and directory
	(*pkg).UpdateFiles(normalizedFiles)

	if err := debext.ModifyPackageStanza(pkg, "Version", normalizedVersion); err != nil {
		return err
	}

	finalDir := filepath.Join("pool", feedRelPath, targetDir)
	if err := debext.ModifyPackageStanza(pkg, "Directory", finalDir); err != nil {
		return err
	}

	return nil
}

// applyRedirectsToPackage updates package metadata with redirect targets using feed's RelativePath
func (a *Apt) applyRedirectsToPackage(pkg **deb.Package, feedRelPath string, relPath string) error {
	if (*pkg).IsSource {
		// For source packages: use relPath (which points to .dsc) to determine redirect directory
		redirectTarget, err := a.getRedirectTarget(relPath, feedRelPath)
		if err != nil {
			return err
		}
		newPath := filepath.Join("pool", feedRelPath, filepath.Dir(redirectTarget))
		return debext.ModifyPackageStanza(pkg, "Directory", newPath)
	} else {
		// For binary packages: use relPath (which points to .deb) directly
		redirectTarget, err := a.getRedirectTarget(relPath, feedRelPath)
		if err != nil {
			return err
		}
		newPath := filepath.Join("pool", feedRelPath, redirectTarget)
		return debext.ModifyPackageStanza(pkg, "Filename", newPath)
	}
}
