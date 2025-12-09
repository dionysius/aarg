package compose

import (
	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/feed"
	"github.com/google/go-github/v80/github"
)

// ComposeOptions contains common configuration shared by all composers
type ComposeOptions struct {
	// Target is the path to the output directory
	Target string

	// Name is the repository name
	Name string

	// PackageOptions contains package filtering options (debug, source inclusion)
	PackageOptions *common.PackageOptions

	// Distributions is the list of distributions
	Distributions []string

	// Feeds contains feed information
	Feeds []*feed.FeedOptions
}

// AptComposeOptions contains configuration for APT repository composition
type AptComposeOptions struct {
	ComposeOptions

	// Trusted is the path to the trusted directory containing verified packages
	Trusted string

	// RetentionPolicies defines package retention strategies
	RetentionPolicies []common.RetentionPolicy

	// PoolMode is the pool organization mode: "hierarchical" or "redirect"
	PoolMode string
}

// WebComposeOptions contains configuration for web page generation
type WebComposeOptions struct {
	ComposeOptions

	// BaseURL is the base URL where the repository will be served
	BaseURL string

	// Downloads is the root downloads directory for caching assets
	Downloads string

	// PrimaryPackage is the primary package name used for distribution sorting
	PrimaryPackage string

	// IconURLs maps feed type names to their icon SVG URLs
	IconURLs map[string]string

	// GitHubClient is the GitHub API client for fetching releases
	GitHubClient *github.Client

	// TailwindRelease is the specific Tailwind CSS release to use (empty = latest)
	TailwindRelease string

	// RepositoryConfig is the original repository configuration (used for YAML export)
	RepositoryConfig any
}
