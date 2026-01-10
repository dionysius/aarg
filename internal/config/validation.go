package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"

	"github.com/dionysius/aarg/internal/feed"
)

// reservedRepoNames contains repository names that cannot be used
// because they conflict with system paths or special directories
var reservedRepoNames = map[string]bool{
	"assets": true, // Web composer static assets directory (css, icons, etc.)
	"keys":   true, // Repository public keys
}

// repoNamePattern matches valid repository names (alphanumeric, dash, underscore)
var repoNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Validation errors
var (
	ErrNoRepositories         = errors.New("no repositories configured")
	ErrRepositoryNameEmpty    = errors.New("repository name is required")
	ErrRepositoryNameReserved = errors.New("repository name is reserved")
	ErrRepositoryNameInvalid  = errors.New("repository name is invalid")
	ErrNoFeeds                = errors.New("at least one feed is required")
	ErrFeedTypeEmpty          = errors.New("feed type not specified")
	ErrFeedTypeInvalid        = errors.New("invalid feed type")
	ErrFeedLocationEmpty      = errors.New("feed location not specified")
	ErrFeedLocationPort       = errors.New("feed location cannot contain port")
	ErrFeedLocationInvalid    = errors.New("feed location is not a valid path")
	ErrFeedLocationScheme     = errors.New("feed location should not include URL scheme")
	ErrFeedLocationQuery      = errors.New("feed location cannot contain query strings")
	ErrFeedLocationFragment   = errors.New("feed location cannot contain fragments")
	ErrPoolModeInvalid        = errors.New("pool mode must be either 'hierarchical' or 'redirect'")
	ErrNoChangesRequiresDist  = errors.New("no_changes requires distribution mappings to be configured")
)

// validate performs validation on the loaded configuration
func validate(cfg *Config) error {
	// Validate URL
	if cfg.URL != "" {
		u, err := url.Parse(cfg.URL)
		if err != nil {
			return fmt.Errorf("invalid URL: %w", err)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("URL must use http or https scheme, got: %s", u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("URL must have a host")
		}
	}

	// Validate pool mode
	if cfg.Generate.PoolMode != "hierarchical" && cfg.Generate.PoolMode != "redirect" {
		return fmt.Errorf("%w: %q", ErrPoolModeInvalid, cfg.Generate.PoolMode)
	}

	// Validate repositories
	if len(cfg.Repositories) == 0 {
		return ErrNoRepositories
	}

	for _, repo := range cfg.Repositories {
		if err := validateRepository(repo); err != nil {
			return fmt.Errorf("repository %s: %w", repo.Name, err)
		}
	}

	return nil
}

// validateRepository validates a single repository configuration
func validateRepository(repo *RepositoryConfig) error {
	// Repository name should already be set by loadRepositories
	if repo.Name == "" {
		return ErrRepositoryNameEmpty
	}

	// Check for reserved names
	if reservedRepoNames[repo.Name] {
		return fmt.Errorf("%w: %q (conflicts with system directories)", ErrRepositoryNameReserved, repo.Name)
	}

	// Repository names must contain only alphanumeric characters, dashes, and underscores
	if !repoNamePattern.MatchString(repo.Name) {
		return fmt.Errorf("%w: %q (must contain only letters, numbers, dashes, and underscores)", ErrRepositoryNameInvalid, repo.Name)
	}

	// Validate feeds
	if len(repo.Feeds) == 0 {
		return ErrNoFeeds
	}

	for i, feedOpts := range repo.Feeds {
		if err := validateFeed(feedOpts); err != nil {
			return fmt.Errorf("feed %d: %w", i, err)
		}
	}

	return nil
}

// validateFeed validates a feed configuration
func validateFeed(feedOpts *feed.FeedOptions) error {
	// Validate Type is set
	if feedOpts.Type == "" {
		return ErrFeedTypeEmpty
	}

	// Validate Type is valid
	feedType := feed.FeedType(feedOpts.Type)
	if feedType != feed.FeedTypeGitHub && feedType != feed.FeedTypeAPT && feedType != feed.FeedTypeOBS {
		return fmt.Errorf("%w: %s", ErrFeedTypeInvalid, feedOpts.Type)
	}

	// Validate Name is set
	if feedOpts.Name == "" {
		return ErrFeedLocationEmpty
	}

	// Validate name is safe to use as filesystem path by parsing as URL
	// and checking that scheme, port, query strings, and fragments are not set
	name := feedOpts.Name

	// Check for port specification in the host part (e.g., example.com:8080/path)
	// Match :digits only before the first slash (host part), not in the path
	// This allows :letters anywhere (OBS paths like home:user:project or paths like file:1234.deb)
	portPattern := regexp.MustCompile(`^[^/]*:\d+(/|$)`)
	if portPattern.MatchString(name) {
		return fmt.Errorf("%w: %s", ErrFeedLocationPort, name)
	}

	// Parse as URL (treating as path if no scheme)
	u, err := url.Parse(name)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrFeedLocationInvalid, name)
	}

	// Disallow any URL scheme (http://, https://, ftp://, etc.)
	// Exception: OBS feeds can have colons in project identifiers (home:user:project)
	// For OBS, only reject if it has :// which indicates a real URL scheme
	if u.Scheme != "" {
		// For OBS feeds, allow colons in project identifiers (e.g., home:dionysius:immich)
		// Only reject if it has :// which indicates a real URL scheme
		if feedType == feed.FeedTypeOBS {
			if len(name) > len(u.Scheme)+3 && name[len(u.Scheme):len(u.Scheme)+3] == "://" {
				return fmt.Errorf("%w: %s", ErrFeedLocationScheme, name)
			}
			// Otherwise, treat the "scheme" as part of the project identifier (e.g., home: in home:user:project)
			// Continue validation
		} else {
			return fmt.Errorf("%w: %s", ErrFeedLocationScheme, name)
		}
	}

	// Disallow query strings
	if u.RawQuery != "" {
		return fmt.Errorf("%w: %s", ErrFeedLocationQuery, name)
	}

	// Disallow fragments
	if u.Fragment != "" {
		return fmt.Errorf("%w: %s", ErrFeedLocationFragment, name)
	}

	// Validate GitHub-specific options
	if feedType == feed.FeedTypeGitHub {
		// no_changes requires distribution mappings
		if feedOpts.NoChanges && len(feedOpts.Distributions) == 0 {
			return fmt.Errorf("%w: %s", ErrNoChangesRequiresDist, name)
		}
	}

	return nil
}
