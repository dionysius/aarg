package config

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/feed"
	"gopkg.in/yaml.v3"
)

// Config represents the complete application configuration
type Config struct {
	Directories  DirectoriesConfig   `yaml:"directories"`
	HTTP         HTTPConfig          `yaml:"http,omitempty"`
	Signing      SigningConfig       `yaml:"signing"`
	GitHub       GitHubConfig        `yaml:"github,omitempty"`
	Cloudflare   CloudflareConfig    `yaml:"cloudflare,omitempty"`
	URL          string              `yaml:"url"`
	Generate     GenerateConfig      `yaml:"generate,omitempty"`
	Web          WebConfig           `yaml:"web,omitempty"`
	Serve        ServeConfig         `yaml:"serve,omitempty"`
	Workers      WorkersConfig       `yaml:"workers"`
	Repositories []*RepositoryConfig `yaml:"repositories"` // Loaded from Directories.Repositories/*.yaml
	ConfigDir    string              `yaml:"-"`            // Directory containing config.yaml (set during Load)
}

// DirectoriesConfig defines directory paths
type DirectoriesConfig struct {
	Root         string `yaml:"root"`
	Repositories string `yaml:"repositories"` // Relative to config dir if not absolute
	Downloads    string `yaml:"downloads"`    // Relative to Root if not absolute
	Trusted      string `yaml:"trusted"`      // Relative to Root if not absolute
	Staging      string `yaml:"staging"`      // Relative to Root if not absolute, contains timestamped build directories
	Public       string `yaml:"public"`       // Relative to Root if not absolute
}

// GetDownloadsPath returns the absolute path to the downloads directory
func (d *DirectoriesConfig) GetDownloadsPath() string {
	if filepath.IsAbs(d.Downloads) {
		return d.Downloads
	}
	return filepath.Join(d.Root, d.Downloads)
}

// GetTrustedPath returns the absolute path to the trusted directory
func (d *DirectoriesConfig) GetTrustedPath() string {
	if filepath.IsAbs(d.Trusted) {
		return d.Trusted
	}
	return filepath.Join(d.Root, d.Trusted)
}

// GetStagingPath returns the absolute path to the staging directory
func (d *DirectoriesConfig) GetStagingPath() string {
	if filepath.IsAbs(d.Staging) {
		return d.Staging
	}
	return filepath.Join(d.Root, d.Staging)
}

// GetPublicPath returns the absolute path to the public directory
func (d *DirectoriesConfig) GetPublicPath() string {
	if filepath.IsAbs(d.Public) {
		return d.Public
	}
	return filepath.Join(d.Root, d.Public)
}

// SigningConfig contains GPG signing configuration
type SigningConfig struct {
	PrivateKey string `yaml:"private_key"`
	PublicKey  string `yaml:"public_key"`
	Passphrase string `yaml:"passphrase,omitempty"` // Optional passphrase for the private key
}

// GetPrivateKeyPath returns the absolute path to the private key
func (s *SigningConfig) GetPrivateKeyPath(configDir string) string {
	if s.PrivateKey == "" || filepath.IsAbs(s.PrivateKey) {
		return s.PrivateKey
	}
	return filepath.Join(configDir, s.PrivateKey)
}

// GetPublicKeyPath returns the absolute path to the public key
func (s *SigningConfig) GetPublicKeyPath(configDir string) string {
	if s.PublicKey == "" || filepath.IsAbs(s.PublicKey) {
		return s.PublicKey
	}
	return filepath.Join(configDir, s.PublicKey)
}

// HTTPConfig contains HTTP client configuration
type HTTPConfig struct {
	UserAgent       string `yaml:"user_agent,omitempty"`         // Custom User-Agent header
	Timeout         int    `yaml:"timeout"`                      // Request timeout in seconds
	MaxIdleConns    int    `yaml:"max_idle_conns,omitempty"`     // Maximum idle connections
	MaxConnsPerHost int    `yaml:"max_conns_per_host,omitempty"` // Maximum connections per host
}

// GitHubConfig contains GitHub API configuration
type GitHubConfig struct {
	Token string `yaml:"token,omitempty"` // GitHub personal access token
}

// CloudflareConfig contains Cloudflare Pages deployment configuration
type CloudflareConfig struct {
	APIToken    string        `yaml:"api_token,omitempty"`
	AccountID   string        `yaml:"account_id,omitempty"`
	ProjectName string        `yaml:"project_name,omitempty"`
	Cleanup     CleanupConfig `yaml:"cleanup,omitempty"`
}

// CleanupConfig contains deployment cleanup settings
type CleanupConfig struct {
	OlderThanDays int `yaml:"older_than_days"`
	KeepLast      int `yaml:"keep_last"`
}

// GenerateConfig contains repository generation configuration
type GenerateConfig struct {
	PoolMode string   `yaml:"pool_mode,omitempty"` // "hierarchical" or "redirect"
	Compose  []string `yaml:"compose,omitempty"`   // List of composers to run
	KeepLast int      `yaml:"keep_last"`           // Number of staging builds to keep
}

// TailwindConfig contains Tailwind CSS configuration
type TailwindConfig struct {
	Release string `yaml:"release,omitempty"` // Specific release version to use (e.g., "v4.1.0"), empty = latest
}

// WebConfig contains web composer configuration
type WebConfig struct {
	Tailwind TailwindConfig    `yaml:"tailwind,omitempty"`
	IconURLs map[string]string `yaml:"icon_urls,omitempty"`
}

// ServeConfig contains HTTP server configuration
type ServeConfig struct {
	Host string `yaml:"host,omitempty"` // Host to bind to (default: localhost)
	Port int    `yaml:"port,omitempty"` // Port to listen on (default: 8080)
}

// GetIconURLs returns the icon URLs with defaults applied
func (w *WebConfig) GetIconURLs() map[string]string {
	defaults := map[string]string{
		"github": "https://cdn.simpleicons.org/github",
		"apt":    "https://cdn.simpleicons.org/debian",
		"obs":    "https://cdn.simpleicons.org/opensuse",
	}

	if w.IconURLs == nil {
		return defaults
	}

	// Merge with defaults
	result := make(map[string]string)
	maps.Copy(result, defaults)
	maps.Copy(result, w.IconURLs)
	return result
}

// WorkersConfig defines worker pool sizes
type WorkersConfig struct {
	Main        uint `yaml:"main"`
	Download    uint `yaml:"download"`
	Compression uint `yaml:"compression"`
}

// RepositoryConfig represents a single repository configuration
type RepositoryConfig struct {
	Name          string                   `yaml:"-"` // Derived from filename
	Packages      common.PackageOptions    `yaml:"packages"`
	Distributions []string                 `yaml:"distributions,omitempty"`
	Architectures []string                 `yaml:"architectures,omitempty"`
	Retention     []common.RetentionPolicy `yaml:"retention,omitempty"`
	Verification  VerificationConfig       `yaml:"verification,omitempty"`
	Feeds         []*feed.FeedOptions      `yaml:"feeds"`
}

// VerificationConfig contains package verification settings
type VerificationConfig struct {
	Keyring string   `yaml:"keyring,omitempty"`
	Keys    []string `yaml:"keys,omitempty"`
}

// GetKeyringPath returns the absolute path to the keyring
func (v *VerificationConfig) GetKeyringPath(configDir string) string {
	if v.Keyring == "" || filepath.IsAbs(v.Keyring) {
		return v.Keyring
	}
	return filepath.Join(configDir, v.Keyring)
}

// GetKeyPaths returns absolute paths for all keys
func (v *VerificationConfig) GetKeyPaths(configDir string) []string {
	paths := make([]string, len(v.Keys))
	for i, key := range v.Keys {
		if filepath.IsAbs(key) {
			paths[i] = key
		} else {
			paths[i] = filepath.Join(configDir, key)
		}
	}
	return paths
}

// defaults applies default values to the configuration
func (c *Config) defaults() {
	// Load environment variables
	if c.GitHub.Token == "" {
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			c.GitHub.Token = token
		}
	}

	// Directories defaults
	if c.Directories.Root == "" {
		c.Directories.Root = "/var/lib/aarg"
	}
	if c.Directories.Repositories == "" {
		c.Directories.Repositories = "repos.d"
	}
	if c.Directories.Downloads == "" {
		c.Directories.Downloads = "downloads"
	}
	if c.Directories.Trusted == "" {
		c.Directories.Trusted = "trusted"
	}
	if c.Directories.Staging == "" {
		c.Directories.Staging = "staging"
	}
	if c.Directories.Public == "" {
		c.Directories.Public = "public"
	}

	// Worker pool defaults
	if c.Workers.Main == 0 {
		c.Workers.Main = uint(runtime.NumCPU() * 10)
	}
	// Enforce minimum of 80 workers to avoid deadlock with subpool nesting
	if c.Workers.Main < 80 {
		c.Workers.Main = 80
	}
	if c.Workers.Download == 0 {
		c.Workers.Download = 20
	}
	if c.Workers.Compression == 0 {
		c.Workers.Compression = uint(runtime.NumCPU())
	}

	// Generate defaults
	if c.Generate.PoolMode == "" {
		c.Generate.PoolMode = "hierarchical"
	}
	if len(c.Generate.Compose) == 0 {
		c.Generate.Compose = []string{"apt"}
	}
	if c.Generate.KeepLast == 0 {
		c.Generate.KeepLast = 5
	}
}

// loadRepositories loads all repository configurations from the repositories directory
func (c *Config) loadRepositories() error {
	// Resolve repositories directory path (relative to config dir)
	reposDir := c.Directories.Repositories
	if !filepath.IsAbs(reposDir) {
		reposDir = filepath.Join(c.ConfigDir, reposDir)
	}

	// Check if repos directory exists
	info, err := os.Stat(reposDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.ErrNotExist
	}

	// Read all .yaml files
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		return err
	}

	repos := make([]*RepositoryConfig, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		// Repository name is the filename without extension
		repoName := strings.TrimSuffix(entry.Name(), ".yaml")
		repoPath := filepath.Join(reposDir, entry.Name())

		// Load repository config
		data, err := os.ReadFile(repoPath)
		if err != nil {
			return err
		}

		var repo RepositoryConfig
		if err := yaml.Unmarshal(data, &repo); err != nil {
			return err
		}
		repo.Name = repoName

		repos = append(repos, &repo)
	}

	if len(repos) == 0 {
		return os.ErrNotExist
	}

	c.Repositories = repos
	return nil
}

// defaults applies default values to a repository configuration
func (r *RepositoryConfig) defaults() {
	for _, feedOpts := range r.Feeds {
		// Architectures, RetentionPolicies, and Packages are always inherited from repository
		feedOpts.Architectures = r.Architectures
		feedOpts.RetentionPolicies = r.Retention
		feedOpts.Packages = r.Packages

		if feedOpts.Type == feed.FeedTypeGitHub {
			if len(feedOpts.Releases) == 0 {
				feedOpts.Releases = []feed.ReleaseType{feed.ReleaseTypeRelease}
			}
		}
	}
}
