package compose

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"github.com/aptly-dev/aptly/deb"
	"github.com/dionysius/aarg/debext"
	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/feed"
	"github.com/google/go-github/v80/github"
	"gopkg.in/yaml.v3"
)

//go:embed templates/*.html templates/*.css
var templatesFS embed.FS

const (
	assetCacheDir = "assets"
	cssFilename   = "tailwind.css"
)

var defaultIconURLs = map[string]string{
	"github": "https://cdn.simpleicons.org/github",
	"apt":    "https://cdn.simpleicons.org/debian",
	"obs":    "https://cdn.simpleicons.org/opensuse",
}

// getNewestUpstreamVersion finds the newest upstream version for a package across specified distributions and architectures
// This is a generalized version that works for all table types based on configuration
func getNewestUpstreamVersion(repo *debext.Repository, packageName string, distributions []string, component string, archMode string) string {
	var newest string

	for _, dist := range distributions {
		if archMode == "source" {
			// Source mode: only check source architecture
			if pkg := repo.GetLatest(packageName, dist, debext.SourceArchitecture); pkg != nil {
				upstream := debext.ParseVersion(pkg.Version).Upstream
				if newest == "" || deb.CompareVersions(upstream, newest) > 0 {
					newest = upstream
				}
			}
		} else {
			// Multi-arch mode: check all architectures
			for _, arch := range repo.GetArchitectures(dist, component, false) {
				if pkg := repo.GetLatest(packageName, dist, arch); pkg != nil {
					upstream := debext.ParseVersion(pkg.Version).Upstream
					if newest == "" || deb.CompareVersions(upstream, newest) > 0 {
						newest = upstream
					}
				}
			}
		}
	}

	return newest
}

// findPrimaryPackage determines the primary package name using the following order:
// 1. Explicitly provided primary package name
// 2. Repository name itself
// 3. First package alphabetically that starts with repository name
// 4. Empty string if none found (will fall back to alphabetical distribution sorting)
func findPrimaryPackage(repo *debext.Repository, repoName, explicitPrimary string) string {
	if explicitPrimary != "" {
		return explicitPrimary
	}

	allPackages := repo.GetPackageNames(common.MainComponent)

	// Check if repository name exists as a package
	if slices.Contains(allPackages, repoName) {
		return repoName
	}

	// Find first package that starts with repository name (alphabetically)
	var matchingPackages []string
	for _, pkg := range allPackages {
		if strings.HasPrefix(pkg, repoName) {
			matchingPackages = append(matchingPackages, pkg)
		}
	}
	if len(matchingPackages) > 0 {
		slices.Sort(matchingPackages)
		return matchingPackages[0]
	}

	return ""
}

// sortDistributionsByPrimaryPackage sorts distributions by the version of the primary package
// If no primary package is found, sorts alphabetically
func sortDistributionsByPrimaryPackage(repo *debext.Repository, distributions []string, repoName, explicitPrimary string) []string {
	primaryPackage := findPrimaryPackage(repo, repoName, explicitPrimary)

	if primaryPackage == "" {
		// No primary package found, sort alphabetically
		slices.Sort(distributions)
		return distributions
	}

	slices.SortFunc(distributions, func(a, b string) int {
		newestA := getNewestVersionForPackageInDistribution(repo, primaryPackage, a)
		newestB := getNewestVersionForPackageInDistribution(repo, primaryPackage, b)

		// If either has no version, sort alphabetically
		if newestA == "" && newestB == "" {
			return strings.Compare(a, b)
		}
		if newestA == "" {
			return 1
		}
		if newestB == "" {
			return -1
		}

		// Compare versions (newer first, so negate)
		return -deb.CompareVersions(newestA, newestB)
	})
	return distributions
}

// getNewestVersionForPackageInDistribution finds the highest upstream version for a specific package in a distribution
func getNewestVersionForPackageInDistribution(repo *debext.Repository, packageName, distribution string) string {
	var newest string

	components := repo.GetComponents(distribution)
	for _, component := range components {
		archs := repo.GetArchitectures(distribution, component, false)
		for _, arch := range archs {
			if pkg := repo.GetLatest(packageName, distribution, arch); pkg != nil {
				upstream := debext.ParseVersion(pkg.Version).Upstream
				if newest == "" || deb.CompareVersions(upstream, newest) > 0 {
					newest = upstream
				}
			}
		}
	}

	return newest
}

// stripDistributionSuffix removes the distribution suffix from a version string
// Strips everything after the first non-alphanumeric character following the debian revision minus sign
// For example: "2025.12.1.0-1~noble" -> "2025.12.1.0-1", "1.0.0-2.1+deb12u1" -> "1.0.0-2"
func stripDistributionSuffix(version string) string {
	// Find the debian revision (after the last dash in upstream version)
	idx := strings.LastIndex(version, "-")
	if idx == -1 {
		return version
	}

	// Look for the first non-alphanumeric character after the dash
	for i := idx + 1; i < len(version); i++ {
		c := version[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return version[:i]
		}
	}

	return version
}

// PackageTableConfig defines the configuration for rendering a package table
type PackageTableConfig struct {
	ID               string   // HTML element ID
	Distributions    []string // List of distributions to display
	ArchitectureMode string   // "multi" for multiple architectures, "source" for source only
	Component        string   // Component name
}

// TableHeaderColumn represents a column in the table header
type TableHeaderColumn struct {
	Name    string // Display name (distribution or architecture)
	Colspan int    // Number of columns this header spans (for distribution headers)
}

// TableCell represents a single cell in the table body
type TableCell struct {
	Version      string // Full version string
	ShortVersion string // Version without distribution suffix
	IsNewest     bool   // Whether this is the newest version
	HasPackage   bool   // Whether a package exists for this cell
}

// TableRow represents a row in the table body
type TableRow struct {
	PackageName string
	Cells       []TableCell
}

// PreparedPackageTable contains all the pre-computed data for rendering a table
type PreparedPackageTable struct {
	ID           string
	DistHeaders  []TableHeaderColumn // First header row (distributions)
	ArchHeaders  []TableHeaderColumn // Second header row (architectures), empty for source mode
	Rows         []TableRow
	HasArchRow   bool   // Whether to show the architecture row
	IsEmpty      bool   // Whether the table has no packages
	EmptyMessage string // Message to show when empty
}

// getPackageTableConfig returns the configuration for a specific table type
func getPackageTableConfig(tableType string, repo *debext.Repository, repoName string, primaryPackage string) PackageTableConfig {
	allDists := repo.GetDistributions()
	sortedDists := sortDistributionsByPrimaryPackage(repo, allDists, repoName, primaryPackage)

	configs := map[string]PackageTableConfig{
		"packages": {
			ID:               "packages",
			Distributions:    sortedDists,
			ArchitectureMode: "multi",
			Component:        common.MainComponent,
		},
		"debug": {
			ID:               "debug",
			Distributions:    sortedDists,
			ArchitectureMode: "multi",
			Component:        common.DebugComponent,
		},
		"sources": {
			ID:               "sources",
			Distributions:    sortedDists,
			ArchitectureMode: "source",
			Component:        common.MainComponent,
		},
	}

	return configs[tableType]
}

// getTableConfigs returns all table configurations
func getTableConfigs(repo *debext.Repository, repoName string, primaryPackage string) []PackageTableConfig {
	return []PackageTableConfig{
		getPackageTableConfig("packages", repo, repoName, primaryPackage),
		getPackageTableConfig("debug", repo, repoName, primaryPackage),
		getPackageTableConfig("sources", repo, repoName, primaryPackage),
	}
}

// preparePackageTable pre-computes all table data based on configuration
func preparePackageTable(repo *debext.Repository, config PackageTableConfig, allPackages []string) PreparedPackageTable {
	table := PreparedPackageTable{
		ID:         config.ID,
		HasArchRow: config.ArchitectureMode != "source",
	}

	// Return early if no distributions
	if len(config.Distributions) == 0 {
		table.IsEmpty = true
		table.EmptyMessage = "No packages available for this view."
		return table
	}

	// Build header columns
	for _, dist := range config.Distributions {
		if config.ArchitectureMode == "source" {
			// Source mode: single header with distribution name
			table.DistHeaders = append(table.DistHeaders, TableHeaderColumn{
				Name:    dist,
				Colspan: 1,
			})
		} else {
			// Multi-arch mode: distribution header spanning all architectures
			archs := repo.GetArchitectures(dist, config.Component, false)
			table.DistHeaders = append(table.DistHeaders, TableHeaderColumn{
				Name:    dist,
				Colspan: len(archs),
			})
			// Add architecture sub-headers
			for _, arch := range archs {
				table.ArchHeaders = append(table.ArchHeaders, TableHeaderColumn{
					Name:    arch,
					Colspan: 1,
				})
			}
		}
	}

	// Build rows
	for _, pkgName := range allPackages {
		row := buildTableRow(repo, pkgName, config)
		if len(row.Cells) > 0 && hasAnyPackage(row.Cells) {
			table.Rows = append(table.Rows, row)
		}
	}

	table.IsEmpty = len(table.Rows) == 0
	if table.IsEmpty {
		table.EmptyMessage = "No packages available for this view."
	}

	return table
}

// buildTableCell creates a table cell for a package in a specific distribution/architecture
func buildTableCell(pkg *deb.Package, newestUpstream string) TableCell {
	cell := TableCell{}
	if pkg != nil {
		cell.HasPackage = true
		cell.Version = pkg.Version
		cell.ShortVersion = stripDistributionSuffix(pkg.Version)
		upstream := debext.ParseVersion(pkg.Version).Upstream
		cell.IsNewest = (upstream == newestUpstream)
	}
	return cell
}

// buildTableRow builds a complete row for a package across all distributions/architectures
func buildTableRow(repo *debext.Repository, pkgName string, config PackageTableConfig) TableRow {
	row := TableRow{PackageName: pkgName}
	newestUpstream := getNewestUpstreamVersion(repo, pkgName, config.Distributions, config.Component, config.ArchitectureMode)

	for _, dist := range config.Distributions {
		if config.ArchitectureMode == "source" {
			pkg := repo.GetLatest(pkgName, dist, debext.SourceArchitecture)
			row.Cells = append(row.Cells, buildTableCell(pkg, newestUpstream))
		} else {
			for _, arch := range repo.GetArchitectures(dist, config.Component, false) {
				pkg := repo.GetLatest(pkgName, dist, arch)
				row.Cells = append(row.Cells, buildTableCell(pkg, newestUpstream))
			}
		}
	}

	return row
}

// hasAnyPackage checks if any cell in the slice has a package
func hasAnyPackage(cells []TableCell) bool {
	for _, cell := range cells {
		if cell.HasPackage {
			return true
		}
	}
	return false
}

// prepareAllPackageTables prepares all package tables for rendering
func prepareAllPackageTables(repo *debext.Repository, repoName string, primaryPackage string) []PreparedPackageTable {
	configs := getTableConfigs(repo, repoName, primaryPackage)

	tables := make([]PreparedPackageTable, len(configs))
	for i, config := range configs {
		// Get packages for the specific component of this table
		allPackages := repo.GetPackageNames(config.Component)
		tables[i] = preparePackageTable(repo, config, allPackages)
	}

	return tables
}

// parseTemplates loads and parses all HTML templates with sprig functions
func parseTemplates() (*template.Template, error) {
	funcs := sprig.FuncMap()
	// No custom functions needed - all logic is now in Go
	tmpl := template.New("").Funcs(funcs)
	return tmpl.ParseFS(templatesFS, "templates/*.html")
}

// Web generates static HTML pages for repository browsing
type Web struct {
	opts         *WebComposeOptions
	downloader   *common.Downloader
	githubClient *github.Client
	tmpl         *template.Template
}

// RepositoryInfo contains information about a repository for template rendering
type RepositoryInfo struct {
	Name          string
	Component     string
	Distributions []string
	Feeds         []FeedInfo
}

// FeedInfo contains information about a feed for template rendering
type FeedInfo struct {
	Type        feed.FeedType
	Path        string   // Storage path (used internally)
	DisplayPath string   // Path to display to users (shortened for OBS)
	URL         string   // URL to the feed source
	Icon        string   // Icon filename without extension
	Details     []string // Formatted details to display (distributions, filters, etc.)
}

// IndexData contains data for the root index page
type IndexData struct {
	Repositories []RepositoryLink
	AssetsPath   string // Relative path to assets directory
	PageTitle    string // Title for the navigation bar
}

// RepositoryLink contains minimal info for linking to a repository
type RepositoryLink struct {
	Name string
	Path string // Relative path to repository HTML file
}

// RepositoryPageData contains data for a repository detail page
type RepositoryPageData struct {
	ComposeOptions *ComposeOptions
	BaseURL        string // Base URL for the repository
	Repository     *debext.Repository
	AssetsPath     string                 // Relative path to assets directory
	Tables         []PreparedPackageTable // Pre-computed package tables
	Feeds          []FeedInfo             // Feed display info with icons
	PageTitle      string                 // Title for the navigation bar
	KeyringName    string                 // Keyring filename (sanitized domain)
}

// DirectoryListingData contains data for a directory browsing page
type DirectoryListingData struct {
	CurrentPath string
	ParentPath  string
	Entries     []DirectoryEntry
	AssetsPath  string
	PageTitle   string
}

// DirectoryEntry represents a file or directory in the listing
type DirectoryEntry struct {
	Name        string
	URL         string
	IsDirectory bool
	Size        string
	Modified    string
}

// NewWeb creates a new web composer
func NewWeb(options *WebComposeOptions, downloader *common.Downloader) (*Web, error) {
	// Parse templates
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	return &Web{
		opts:         options,
		downloader:   downloader,
		githubClient: options.GitHubClient,
		tmpl:         tmpl,
	}, nil
}

// Compose generates the static HTML page for a single repository
func (w *Web) Compose(ctx context.Context, repo *debext.Repository) error {
	// Ensure icons are available
	if err := w.ensureIcons(ctx); err != nil {
		return err
	}

	// Use distributions directly from repo
	composeOpts := w.opts.ComposeOptions

	keyringName := GenerateKeyringName(w.opts.BaseURL)

	// Prepare tables first to get sorted distributions
	tables := prepareAllPackageTables(repo, w.opts.Name, w.opts.PrimaryPackage)

	// Use sorted distributions from tables (all tables use the same sorting)
	if len(tables) > 0 && len(tables[0].DistHeaders) > 0 {
		sortedDists := make([]string, 0, len(tables[0].DistHeaders))
		for _, header := range tables[0].DistHeaders {
			sortedDists = append(sortedDists, header.Name)
		}
		composeOpts.Distributions = sortedDists
	} else {
		composeOpts.Distributions = repo.GetDistributions()
	}

	data := RepositoryPageData{
		ComposeOptions: &composeOpts,
		BaseURL:        w.opts.BaseURL,
		Repository:     repo,
		AssetsPath:     "../", // Repository pages are in subdirectories, so need ../ to reach assets
		Tables:         tables,
		Feeds:          w.prepareFeedInfo(),
		PageTitle:      "APT Repositories",
		KeyringName:    keyringName,
	}

	// Render template by executing base.html which will use the repository.html blocks
	var buf bytes.Buffer
	// Clone template to avoid concurrent execution issues
	tmpl := template.Must(w.tmpl.Clone())
	// Parse repository.html and all section templates to define their blocks
	if _, err := tmpl.ParseFS(templatesFS,
		"templates/nav.html",
		"templates/repository.html",
		"templates/repo-header.html",
		"templates/repo-feeds.html",
		"templates/repo-installation.html",
		"templates/repo-packages.html",
	); err != nil {
		return err
	}
	if err := tmpl.ExecuteTemplate(&buf, "base.html", data); err != nil {
		return err
	}

	// Write to file in repository subdirectory
	repoDir := filepath.Join(w.opts.Target, w.opts.Name)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return err
	}
	repoPath := filepath.Join(repoDir, "index.html")
	if err := os.WriteFile(repoPath, buf.Bytes(), 0644); err != nil {
		return err
	}

	// Generate install.sh script for this repository
	installScript, err := GenerateInstallScript(InstallScriptOptions{
		RepoName:      w.opts.Name,
		BaseURL:       w.opts.BaseURL,
		Distributions: repo.GetDistributions(),
		KeyringName:   keyringName,
	})
	if err != nil {
		return err
	}
	installPath := filepath.Join(repoDir, "install.sh")
	if err := os.WriteFile(installPath, []byte(installScript), 0644); err != nil {
		return err
	}

	// Export repository config as YAML
	configYAML, err := yaml.Marshal(w.opts.RepositoryConfig)
	if err != nil {
		return err
	}
	configPath := filepath.Join(repoDir, w.opts.Name+".yaml")
	if err := os.WriteFile(configPath, configYAML, 0644); err != nil {
		return err
	}

	// Generate directory indexes for browsing dists/ and subdirectories
	if err := w.GenerateDirectoryIndexes(ctx, w.opts.Name); err != nil {
		return err
	}

	return nil
}

// Index scans for repository subdirectories and generates the root index.html
func (w *Web) Index(ctx context.Context) error {

	// Scan for repository subdirectories in TargetDir
	entries, err := os.ReadDir(w.opts.Target)
	if err != nil {
		return err
	}

	var repositories []RepositoryLink
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Check if directory contains an index.html file (indicating it's a repository)
		indexPath := filepath.Join(w.opts.Target, name, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			repositories = append(repositories, RepositoryLink{
				Name: name,
				Path: name + "/", // Link to directory
			})
		}
	}

	data := IndexData{
		Repositories: repositories,
		AssetsPath:   "", // Root index is at same level as assets directory
		PageTitle:    "APT Repositories",
	}

	// Render template by executing base.html which will use the index.html blocks
	var buf bytes.Buffer
	// Clone template to avoid concurrent execution issues
	tmpl := template.Must(w.tmpl.Clone())
	// Parse index.html to define its blocks
	if _, err := tmpl.ParseFS(templatesFS, "templates/nav.html", "templates/index.html"); err != nil {
		return err
	}
	if err := tmpl.ExecuteTemplate(&buf, "base.html", data); err != nil {
		return err
	}

	// Write to file
	indexPath := filepath.Join(w.opts.Target, "index.html")
	if err := os.WriteFile(indexPath, buf.Bytes(), 0644); err != nil {
		return err
	}

	// Build Tailwind CSS after all HTML files have been generated
	// This allows Tailwind to scan all HTML and extract the classes actually used
	if err := w.BuildTailwindCSS(ctx); err != nil {
		return err
	}

	return nil
}

// prepareFeedInfo extracts feed information for template rendering
func (w *Web) prepareFeedInfo() []FeedInfo {
	feeds := make([]FeedInfo, 0, len(w.opts.Feeds))
	for _, feedOpts := range w.opts.Feeds {
		// Map to icon using configured icon URLs
		icon := ""
		if _, exists := w.opts.IconURLs[string(feedOpts.Type)]; exists {
			icon = string(feedOpts.Type)
		}

		// Build details list
		details := []string{} // Add distribution mappings if present
		if len(feedOpts.Distributions) > 0 {
			distStrs := make([]string, len(feedOpts.Distributions))
			for i, dm := range feedOpts.Distributions {
				if dm.Feed == dm.Target {
					distStrs[i] = dm.Target
				} else {
					distStrs[i] = dm.Feed + " -> " + dm.Target
				}
			}
			details = append(details, "Distributions: "+strings.Join(distStrs, ", "))
		}

		// GitHub-specific details
		if feedOpts.Type == "github" {
			if len(feedOpts.Tags) > 0 {
				tagStrs := make([]string, len(feedOpts.Tags))
				copy(tagStrs, feedOpts.Tags)
				details = append(details, "Tags: "+strings.Join(tagStrs, ", "))
			}
			if len(feedOpts.Releases) > 0 {
				releaseStrs := make([]string, len(feedOpts.Releases))
				for i, rt := range feedOpts.Releases {
					releaseStrs[i] = string(rt)
				}
				details = append(details, "Releases: "+strings.Join(releaseStrs, ", "))
			}
		}

		// Source filtering
		if len(feedOpts.Sources) > 0 {
			details = append(details, "Sources: "+strings.Join(feedOpts.Sources, ", "))
		}

		feeds = append(feeds, FeedInfo{
			Type:        feedOpts.Type,
			Path:        feedOpts.RelativePath,
			DisplayPath: feedOpts.Name,
			URL:         feedOpts.ProjectURL.String(),
			Icon:        icon,
			Details:     details,
		})
	}
	return feeds
}

// BuildTailwindCSS builds optimized CSS from templates using Tailwind CLI
// This should be called after all HTML files have been generated
func (w *Web) BuildTailwindCSS(ctx context.Context) error {
	// Output CSS path in PublicDir
	publicCSSDir := filepath.Join(w.opts.Target, "assets", "css")
	if err := os.MkdirAll(publicCSSDir, 0755); err != nil {
		return fmt.Errorf("creating CSS directory: %w", err)
	}
	outputCSS := filepath.Join(publicCSSDir, cssFilename)

	// Extract input.css from embedded templates and render with target directory
	inputCSSTemplate, err := templatesFS.ReadFile("templates/input.css")
	if err != nil {
		return fmt.Errorf("reading input.css: %w", err)
	}

	// Parse and execute template
	tmpl, err := template.New("input.css").Parse(string(inputCSSTemplate))
	if err != nil {
		return fmt.Errorf("parsing input.css template: %w", err)
	}

	var inputCSSBuf bytes.Buffer
	if err := tmpl.Execute(&inputCSSBuf, struct{ TargetDir string }{TargetDir: w.opts.Target}); err != nil {
		return fmt.Errorf("executing input.css template: %w", err)
	}

	// Write input.css to temp file
	tempDir := filepath.Join(w.opts.Downloads, "temp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	inputCSS := filepath.Join(tempDir, "input.css")
	if err := os.WriteFile(inputCSS, inputCSSBuf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing input.css: %w", err)
	}
	defer os.Remove(inputCSS)

	// Create Tailwind CLI and build CSS
	// Pass Downloads directory as cwd so @source paths resolve correctly
	// Store Tailwind binary in assets subdirectory of downloads
	assetsDir := filepath.Join(w.opts.Downloads, assetCacheDir)
	tailwind := NewTailwindCLI(w.downloader, w.githubClient, assetsDir, w.opts.TailwindRelease)
	if err := tailwind.Build(ctx, inputCSS, outputCSS, w.opts.Downloads); err != nil {
		return fmt.Errorf("building CSS: %w", err)
	}

	return nil
}

// ensureIcons downloads feed type icons to cache and hardlinks them to PublicDir
func (w *Web) ensureIcons(ctx context.Context) error {
	// Merge default and configured icon URLs
	iconURLs := make(map[string]string)
	maps.Copy(iconURLs, defaultIconURLs)
	maps.Copy(iconURLs, w.opts.IconURLs)

	// Download each icon
	for feedType, iconURL := range iconURLs {
		if err := w.ensureIcon(ctx, feedType, iconURL); err != nil {
			return err
		}
	}

	return nil
}

// ensureIcon downloads a single icon to cache and hardlinks it to PublicDir
func (w *Web) ensureIcon(ctx context.Context, feedType, iconURL string) error {
	// Determine cache path in downloads folder
	cacheDir := filepath.Join(w.opts.Downloads, assetCacheDir, "icons")
	iconFilename := feedType + ".svg"
	cachedIconPath := filepath.Join(cacheDir, iconFilename)

	// Check if cached file exists
	if _, err := os.Stat(cachedIconPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}

		// Download icon to cache
		if err := w.downloadAsset(ctx, iconURL, cachedIconPath); err != nil {
			return err
		}
	}

	// Hardlink to PublicDir/assets/icons/
	publicIconDir := filepath.Join(w.opts.Target, "assets", "icons")
	if err := os.MkdirAll(publicIconDir, 0755); err != nil {
		return err
	}

	publicIconPath := filepath.Join(publicIconDir, iconFilename)

	// Create hardlink
	if err := common.EnsureHardlink(cachedIconPath, publicIconPath); err != nil {
		return err
	}

	return nil
}

// downloadAsset downloads an asset from the given URL to the destination path
func (w *Web) downloadAsset(ctx context.Context, url, destPath string) error {
	// Create cache directory
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	// Download using downloader
	req := &common.DownloadRequest{
		URL:         url,
		Destination: destPath,
	}

	group := w.downloader.Download(ctx, req)
	_, err := group.Wait()
	return err
}

// GenerateDirectoryIndexes creates browsable index.html files for dists/ and subdirectories
func (w *Web) GenerateDirectoryIndexes(ctx context.Context, repoName string) error {
	repoDir := filepath.Join(w.opts.Target, repoName)
	distsDir := filepath.Join(repoDir, "dists")

	// Check if dists directory exists
	if _, err := os.Stat(distsDir); os.IsNotExist(err) {
		return nil // No dists directory, nothing to do
	}

	// Generate indexes for dists/ and all subdirectories
	return w.generateDirectoryIndex(ctx, distsDir, repoName, "dists")
}

// generateDirectoryIndex recursively generates index.html for a directory and its subdirectories
func (w *Web) generateDirectoryIndex(ctx context.Context, dirPath, repoName, relativePath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("reading directory %s: %w", dirPath, err)
	}

	var dirEntries []DirectoryEntry

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Skip index.html files
		if entry.Name() == "index.html" {
			continue
		}

		isDir := entry.IsDir()

		// Format size
		size := "-"
		if !isDir {
			size = formatFileSize(info.Size())
		}

		// Format modified time
		modified := info.ModTime().Format("2006-01-02 15:04:05")

		// Create URL
		url := entry.Name()
		if isDir {
			url += "/"
		}

		dirEntries = append(dirEntries, DirectoryEntry{
			Name:        entry.Name(),
			URL:         url,
			IsDirectory: isDir,
			Size:        size,
			Modified:    modified,
		})

		// Recursively generate index for subdirectories
		if isDir {
			subDirPath := filepath.Join(dirPath, entry.Name())
			subRelativePath := filepath.Join(relativePath, entry.Name())
			if err := w.generateDirectoryIndex(ctx, subDirPath, repoName, subRelativePath); err != nil {
				return err
			}
		}
	}

	// Determine parent path
	parentPath := ""
	if relativePath != "dists" {
		parentPath = "../"
	} else {
		// Link back to repository page
		parentPath = "../"
	}

	// Calculate assets path - need to go up based on depth
	// Count forward slashes in relativePath (e.g., "dists/noble/main" has 2 slashes = 3 directories)
	// Plus 1 for the repository directory itself (e.g., "immich")
	depth := strings.Count(relativePath, "/") + 1
	assetsPath := strings.Repeat("../", depth+1) // +1 to reach staging root where assets is

	data := DirectoryListingData{
		CurrentPath: "/" + repoName + "/" + relativePath,
		ParentPath:  parentPath,
		Entries:     dirEntries,
		AssetsPath:  assetsPath,
		PageTitle:   "APT Repositories",
	}

	// Render template
	var buf bytes.Buffer
	tmpl := template.Must(w.tmpl.Clone())
	if _, err := tmpl.ParseFS(templatesFS,
		"templates/nav.html",
		"templates/directory.html",
		"templates/directory-listing.html",
	); err != nil {
		return fmt.Errorf("parsing directory templates: %w", err)
	}

	if err := tmpl.ExecuteTemplate(&buf, "base.html", data); err != nil {
		return fmt.Errorf("executing directory template: %w", err)
	}

	// Write index.html to directory
	indexPath := filepath.Join(dirPath, "index.html")
	if err := os.WriteFile(indexPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing directory index: %w", err)
	}

	return nil
}

// formatFileSize formats a file size in bytes to a human-readable string
func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
