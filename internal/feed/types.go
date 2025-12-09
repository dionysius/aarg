package feed

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/dionysius/aarg/internal/common"
	"gopkg.in/yaml.v3"
)

// FeedType represents the type of feed source
type FeedType string

// Feed type constants
const (
	FeedTypeGitHub  FeedType = "github"
	FeedTypeAPT     FeedType = "apt"
	FeedTypeOBS     FeedType = "obs"
	FeedTypeUnknown FeedType = "unknown"
)

func (f FeedType) String() string {
	return string(f)
}

// ReleaseType represents a GitHub release type
type ReleaseType string

// Release type constants for GitHub feeds
const (
	ReleaseTypeRelease    ReleaseType = "release"     // Normal release (not draft, not prerelease)
	ReleaseTypePrerelease ReleaseType = "pre-release" // Pre-release
	ReleaseTypeDraft      ReleaseType = "draft"       // Draft release
)

// Feed represents any download source type
type Feed interface {
	// Run executes the complete download and verification process
	Run(ctx context.Context) error
}

// FeedOptions contains fully-resolved configuration for a feed source.
// All values are already inherited/merged from repository-level config.
type FeedOptions struct {
	// Feed type: github, apt, obs
	Type FeedType

	// Name identifies the feed source as configured. Format depends on feed type:
	// - GitHub: "owner/repo"
	// - APT: base URL without scheme (e.g., "deb.debext.org/debian")
	// - OBS: project identifier (e.g., "home:dionysius:immich")
	Name string

	// Derived URLs and paths (calculated during unmarshal)
	ProjectURL   *url.URL // URL to the project page (e.g., GitHub repo, OBS project)
	DownloadURL  *url.URL // Base URL for downloads (with https://)
	RelativePath string   // Relative path for downloads and trusted directory

	// GitHub-specific
	Releases []ReleaseType // Release types to include
	Tags     []string      // Tag name filters (glob patterns, ! prefix for negation)

	// Common to all feeds
	Distributions []DistributionMap // Distribution mappings from feed to target repository
	Architectures []string          // Architectures to fetch and process

	// Package source filtering - which packages to include
	Sources []string // Source name patterns (glob, ! for negation), empty = include all

	// Retention policies for version filtering - which versions to keep
	RetentionPolicies []common.RetentionPolicy // Applied to determine which versions to keep

	// Package filtering options
	Packages common.PackageOptions // IncludeDebug, IncludeSource
}

// DistributionMap represents a mapping from a feed's distribution name to the target repository distribution name.
// After config resolution, both Feed and Target are always set (identity mapping if no rename specified).
type DistributionMap struct {
	Feed   string // Distribution name in the feed (e.g., "/" for flat repos, "Debian_13", "noble")
	Target string // Distribution name in our repository (e.g., "noble", "trixie")
}

// UnmarshalYAML implements custom unmarshaling for DistributionMap to support both formats:
// - String: "noble" -> {Feed: "noble", Target: "noble"}
// - Map: {"focal": "stable"} -> {Feed: "focal", Target: "stable"}
func (d *DistributionMap) UnmarshalYAML(node *yaml.Node) error {
	// Try unmarshaling as string first
	if node.Kind == yaml.ScalarNode {
		var str string
		if err := node.Decode(&str); err != nil {
			return err
		}
		d.Feed = str
		d.Target = str
		return nil
	}

	// Try unmarshaling as map
	if node.Kind == yaml.MappingNode {
		var m map[string]string
		if err := node.Decode(&m); err != nil {
			return err
		}
		if len(m) != 1 {
			return fmt.Errorf("distribution mapping must have exactly one key-value pair")
		}
		for feed, target := range m {
			d.Feed = feed
			d.Target = target
		}
		return nil
	}

	return fmt.Errorf("distribution must be a string or map, got %v", node.Kind)
}

// MarshalYAML implements custom marshaling for DistributionMap:
// - If Feed == Target: output as string "noble"
// - If Feed != Target: output as map {"focal": "stable"}
func (d DistributionMap) MarshalYAML() (any, error) {
	if d.Feed == d.Target {
		return d.Feed, nil
	}
	return map[string]string{d.Feed: d.Target}, nil
}

// validateURLScheme validates that a URL has http/https scheme and no port
func validateURLScheme(u *url.URL, originalURL string) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https: %s", originalURL)
	}
	return nil
}

// UnmarshalYAML implements custom unmarshaling for FeedOptions to handle feed type fields implicitly.
// Detects feed type from github/apt/obs fields and sets Type and Location accordingly.
func (f *FeedOptions) UnmarshalYAML(node *yaml.Node) (err error) {
	// Create auxiliary struct with all fields as pointers/slices to detect what's set
	type feedOptionsAlias struct {
		GitHub        *string           `yaml:"github"`
		APT           *string           `yaml:"apt"`
		OBS           *string           `yaml:"obs"`
		Releases      []string          `yaml:"releases"`
		Tags          []string          `yaml:"tags"`
		Components    []string          `yaml:"components"`
		Distributions []DistributionMap `yaml:"distributions"`
		Sources       []string          `yaml:"sources"`
	}

	var aux feedOptionsAlias
	if err := node.Decode(&aux); err != nil {
		return err
	}

	// Determine feed type and location
	if aux.GitHub != nil {
		f.Type = FeedTypeGitHub
		f.Name = *aux.GitHub
		f.RelativePath = "github/" + *aux.GitHub

		f.ProjectURL, err = url.Parse("https://github.com/" + *aux.GitHub)
		if err != nil {
			return fmt.Errorf("failed to parse GitHub project URL: %w", err)
		}
		f.DownloadURL = f.ProjectURL.JoinPath("releases", "download")
	} else if aux.APT != nil {
		f.Type = FeedTypeAPT
		aptURL, err := url.Parse(*aux.APT)
		if err != nil {
			return fmt.Errorf("failed to parse APT URL: %w", err)
		}
		if err := validateURLScheme(aptURL, *aux.APT); err != nil {
			return fmt.Errorf("%s, %w", "apt", err)
		}
		f.Name = aptURL.Host + aptURL.Path
		f.RelativePath = aptURL.Host + aptURL.Path
		f.ProjectURL = aptURL
		f.DownloadURL = aptURL
	} else if aux.OBS != nil {
		f.Type = FeedTypeOBS
		f.Name = *aux.OBS

		// Check if location contains a domain (custom OBS instance)
		if strings.Contains(*aux.OBS, ".") {
			// Custom OBS instance - expect full URL with scheme
			obsURL, err := url.Parse(*aux.OBS)
			if err != nil {
				return fmt.Errorf("failed to parse custom OBS URL: %w", err)
			}
			if err := validateURLScheme(obsURL, *aux.OBS); err != nil {
				return fmt.Errorf("%s, %w", "obs", err)
			}
			f.Name = obsURL.Host + obsURL.Path
			f.RelativePath = obsURL.Host + obsURL.Path
			f.ProjectURL = obsURL
			f.DownloadURL = obsURL
		} else {
			// Project identifier format: home:dionysius:immich
			// Convert to download format: home:/dionysius:/immich
			downloadPath := strings.ReplaceAll(*aux.OBS, ":", ":/")
			f.RelativePath = "download.opensuse.org/repositories/" + downloadPath
			f.ProjectURL, err = url.Parse("https://build.opensuse.org/project/show/" + *aux.OBS)
			if err != nil {
				return fmt.Errorf("failed to parse OBS project URL: %w", err)
			}
			f.DownloadURL, err = url.Parse("https://download.opensuse.org/repositories/" + downloadPath)
			if err != nil {
				return fmt.Errorf("failed to parse OBS download URL: %w", err)
			}
		}
	} else {
		return fmt.Errorf("feed must specify one of: github, apt, obs")
	}

	// Convert release type strings to ReleaseType constants
	if len(aux.Releases) > 0 {
		f.Releases = make([]ReleaseType, 0, len(aux.Releases))
		for _, r := range aux.Releases {
			switch r {
			case "release":
				f.Releases = append(f.Releases, ReleaseTypeRelease)
			case "pre-release":
				f.Releases = append(f.Releases, ReleaseTypePrerelease)
			case "draft":
				f.Releases = append(f.Releases, ReleaseTypeDraft)
			}
		}
	}

	// Set other fields
	f.Tags = aux.Tags
	f.Distributions = aux.Distributions
	f.Sources = aux.Sources

	return nil
}

// MarshalYAML implements custom marshaling for FeedOptions to output feed type fields implicitly.
func (f FeedOptions) MarshalYAML() (any, error) {
	// Create a map to build the output
	output := make(map[string]any)

	// Add feed type field based on Type
	switch FeedType(f.Type) {
	case FeedTypeGitHub:
		output["github"] = f.Name
	case FeedTypeAPT:
		output["apt"] = f.DownloadURL.String()
	case FeedTypeOBS:
		// For custom OBS (contains dot in Name), output full URL with scheme
		// For project identifiers (home:user:project), output as-is
		if strings.Contains(f.Name, ".") {
			output["obs"] = f.DownloadURL.String()
		} else {
			output["obs"] = f.Name
		}
	}

	// Add common fields
	if len(f.Distributions) > 0 {
		output["distributions"] = f.Distributions
	}
	if len(f.Sources) > 0 {
		output["sources"] = f.Sources
	}
	if len(f.Releases) > 0 {
		releases := make([]string, len(f.Releases))
		for i, r := range f.Releases {
			releases[i] = string(r)
		}
		output["releases"] = releases
	}
	if len(f.Tags) > 0 {
		output["tags"] = f.Tags
	}

	return output, nil
}
