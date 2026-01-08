package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/feed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectoriesConfig_GetDownloadsPath(t *testing.T) {
	tests := []struct {
		name      string
		root      string
		downloads string
		want      string
	}{
		{
			name:      "absolute path",
			root:      "/var/lib/aarg",
			downloads: "/tmp/downloads",
			want:      "/tmp/downloads",
		},
		{
			name:      "relative path",
			root:      "/var/lib/aarg",
			downloads: "downloads",
			want:      "/var/lib/aarg/downloads",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DirectoriesConfig{
				Root:      tt.root,
				Downloads: tt.downloads,
			}
			assert.Equal(t, tt.want, d.GetDownloadsPath())
		})
	}
}

func TestDirectoriesConfig_GetTrustedPath(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		trusted string
		want    string
	}{
		{
			name:    "absolute path",
			root:    "/var/lib/aarg",
			trusted: "/etc/trusted",
			want:    "/etc/trusted",
		},
		{
			name:    "relative path",
			root:    "/var/lib/aarg",
			trusted: "trusted",
			want:    "/var/lib/aarg/trusted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DirectoriesConfig{
				Root:    tt.root,
				Trusted: tt.trusted,
			}
			assert.Equal(t, tt.want, d.GetTrustedPath())
		})
	}
}

func TestDirectoriesConfig_GetStagingPath(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		staging string
		want    string
	}{
		{
			name:    "absolute path",
			root:    "/var/lib/aarg",
			staging: "/tmp/staging",
			want:    "/tmp/staging",
		},
		{
			name:    "relative path",
			root:    "/var/lib/aarg",
			staging: "staging",
			want:    "/var/lib/aarg/staging",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DirectoriesConfig{
				Root:    tt.root,
				Staging: tt.staging,
			}
			assert.Equal(t, tt.want, d.GetStagingPath())
		})
	}
}

func TestDirectoriesConfig_GetPublicPath(t *testing.T) {
	tests := []struct {
		name   string
		root   string
		public string
		want   string
	}{
		{
			name:   "absolute path",
			root:   "/var/lib/aarg",
			public: "/var/www/public",
			want:   "/var/www/public",
		},
		{
			name:   "relative path",
			root:   "/var/lib/aarg",
			public: "public",
			want:   "/var/lib/aarg/public",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DirectoriesConfig{
				Root:   tt.root,
				Public: tt.public,
			}
			assert.Equal(t, tt.want, d.GetPublicPath())
		})
	}
}

func TestSigningConfig_GetPrivateKeyPath(t *testing.T) {
	tests := []struct {
		name       string
		privateKey string
		configDir  string
		want       string
	}{
		{
			name:       "absolute path",
			privateKey: "/etc/keys/private.asc",
			configDir:  "/etc/aarg",
			want:       "/etc/keys/private.asc",
		},
		{
			name:       "relative path",
			privateKey: "keys/private.asc",
			configDir:  "/etc/aarg",
			want:       "/etc/aarg/keys/private.asc",
		},
		{
			name:       "empty path",
			privateKey: "",
			configDir:  "/etc/aarg",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &SigningConfig{
				PrivateKey: tt.privateKey,
			}
			assert.Equal(t, tt.want, s.GetPrivateKeyPath(tt.configDir))
		})
	}
}

func TestSigningConfig_GetPublicKeyPath(t *testing.T) {
	tests := []struct {
		name      string
		publicKey string
		configDir string
		want      string
	}{
		{
			name:      "absolute path",
			publicKey: "/etc/keys/public.asc",
			configDir: "/etc/aarg",
			want:      "/etc/keys/public.asc",
		},
		{
			name:      "relative path",
			publicKey: "keys/public.asc",
			configDir: "/etc/aarg",
			want:      "/etc/aarg/keys/public.asc",
		},
		{
			name:      "empty path",
			publicKey: "",
			configDir: "/etc/aarg",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &SigningConfig{
				PublicKey: tt.publicKey,
			}
			assert.Equal(t, tt.want, s.GetPublicKeyPath(tt.configDir))
		})
	}
}

func TestWebConfig_GetIconURLs(t *testing.T) {
	tests := []struct {
		name     string
		iconURLs map[string]string
		want     map[string]string
	}{
		{
			name:     "nil returns defaults",
			iconURLs: nil,
			want: map[string]string{
				"github": "https://cdn.simpleicons.org/github",
				"apt":    "https://cdn.simpleicons.org/debian",
				"obs":    "https://cdn.simpleicons.org/opensuse",
			},
		},
		{
			name:     "empty returns defaults",
			iconURLs: map[string]string{},
			want: map[string]string{
				"github": "https://cdn.simpleicons.org/github",
				"apt":    "https://cdn.simpleicons.org/debian",
				"obs":    "https://cdn.simpleicons.org/opensuse",
			},
		},
		{
			name: "custom overrides defaults",
			iconURLs: map[string]string{
				"github": "https://example.com/github.svg",
			},
			want: map[string]string{
				"github": "https://example.com/github.svg",
				"apt":    "https://cdn.simpleicons.org/debian",
				"obs":    "https://cdn.simpleicons.org/opensuse",
			},
		},
		{
			name: "custom adds new icon",
			iconURLs: map[string]string{
				"custom": "https://example.com/custom.svg",
			},
			want: map[string]string{
				"github": "https://cdn.simpleicons.org/github",
				"apt":    "https://cdn.simpleicons.org/debian",
				"obs":    "https://cdn.simpleicons.org/opensuse",
				"custom": "https://example.com/custom.svg",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &WebConfig{
				IconURLs: tt.iconURLs,
			}
			assert.Equal(t, tt.want, w.GetIconURLs())
		})
	}
}

func TestVerificationConfig_GetKeyringPath(t *testing.T) {
	tests := []struct {
		name      string
		keyring   string
		configDir string
		want      string
	}{
		{
			name:      "absolute path",
			keyring:   "/etc/keys/keyring.gpg",
			configDir: "/etc/aarg",
			want:      "/etc/keys/keyring.gpg",
		},
		{
			name:      "relative path",
			keyring:   "keys/keyring.gpg",
			configDir: "/etc/aarg",
			want:      "/etc/aarg/keys/keyring.gpg",
		},
		{
			name:      "empty path",
			keyring:   "",
			configDir: "/etc/aarg",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &VerificationConfig{
				Keyring: tt.keyring,
			}
			assert.Equal(t, tt.want, v.GetKeyringPath(tt.configDir))
		})
	}
}

func TestVerificationConfig_GetKeyPaths(t *testing.T) {
	tests := []struct {
		name      string
		keys      []string
		configDir string
		want      []string
	}{
		{
			name:      "absolute paths",
			keys:      []string{"/etc/keys/key1.asc", "/etc/keys/key2.asc"},
			configDir: "/etc/aarg",
			want:      []string{"/etc/keys/key1.asc", "/etc/keys/key2.asc"},
		},
		{
			name:      "relative paths",
			keys:      []string{"keys/key1.asc", "keys/key2.asc"},
			configDir: "/etc/aarg",
			want:      []string{"/etc/aarg/keys/key1.asc", "/etc/aarg/keys/key2.asc"},
		},
		{
			name:      "mixed paths",
			keys:      []string{"/etc/keys/key1.asc", "keys/key2.asc"},
			configDir: "/etc/aarg",
			want:      []string{"/etc/keys/key1.asc", "/etc/aarg/keys/key2.asc"},
		},
		{
			name:      "empty slice",
			keys:      []string{},
			configDir: "/etc/aarg",
			want:      []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &VerificationConfig{
				Keys: tt.keys,
			}
			assert.Equal(t, tt.want, v.GetKeyPaths(tt.configDir))
		})
	}
}

func TestConfig_defaults(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		checkFn func(*testing.T, *Config)
	}{
		{
			name: "applies directory defaults",
			cfg:  &Config{},
			checkFn: func(t *testing.T, c *Config) {
				assert.Equal(t, "/var/lib/aarg", c.Directories.Root)
				assert.Equal(t, "repos.d", c.Directories.Repositories)
				assert.Equal(t, "downloads", c.Directories.Downloads)
				assert.Equal(t, "trusted", c.Directories.Trusted)
				assert.Equal(t, "staging", c.Directories.Staging)
				assert.Equal(t, "public", c.Directories.Public)
			},
		},
		{
			name: "applies worker defaults",
			cfg:  &Config{},
			checkFn: func(t *testing.T, c *Config) {
				assert.Equal(t, uint(runtime.NumCPU()*10), c.Workers.Main)
				assert.Equal(t, uint(20), c.Workers.Download)
				assert.Equal(t, uint(runtime.NumCPU()), c.Workers.Compression)
			},
		},
		{
			name: "applies generate defaults",
			cfg:  &Config{},
			checkFn: func(t *testing.T, c *Config) {
				assert.Equal(t, "hierarchical", c.Generate.PoolMode)
				assert.Equal(t, []string{"apt"}, c.Generate.Compose)
				assert.Equal(t, 5, c.Generate.KeepLast)
			},
		},
		{
			name: "preserves existing values",
			cfg: &Config{
				Directories: DirectoriesConfig{
					Root:         "/custom/root",
					Repositories: "/custom/repos",
				},
				Workers: WorkersConfig{
					Main: 100,
				},
			},
			checkFn: func(t *testing.T, c *Config) {
				assert.Equal(t, "/custom/root", c.Directories.Root)
				assert.Equal(t, "/custom/repos", c.Directories.Repositories)
				assert.Equal(t, uint(100), c.Workers.Main)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.defaults()
			tt.checkFn(t, tt.cfg)
		})
	}
}

func TestRepositoryConfig_defaults(t *testing.T) {
	tests := []struct {
		name    string
		repo    *RepositoryConfig
		checkFn func(*testing.T, *RepositoryConfig)
	}{
		{
			name: "inherits architectures and retention",
			repo: &RepositoryConfig{
				Architectures: []string{"amd64", "arm64"},
				Retention: []common.RetentionPolicy{
					{
						RetentionRule: common.RetentionRule{
							Pattern: "*.*.*",
							Amount:  []int{3},
						},
					},
				},
				Packages: common.PackageOptions{
					Debug:  true,
					Source: false,
				},
				Feeds: []*feed.FeedOptions{
					{Type: "github", Name: "owner/repo"},
				},
			},
			checkFn: func(t *testing.T, r *RepositoryConfig) {
				require.Len(t, r.Feeds, 1)
				assert.Equal(t, []string{"amd64", "arm64"}, r.Feeds[0].Architectures)
				assert.Equal(t, r.Retention, r.Feeds[0].RetentionPolicies)
				assert.Equal(t, r.Packages, r.Feeds[0].Packages)
			},
		},
		{
			name: "sets default releases for github feeds",
			repo: &RepositoryConfig{
				Feeds: []*feed.FeedOptions{
					{Type: "github", Name: "owner/repo"},
				},
			},
			checkFn: func(t *testing.T, r *RepositoryConfig) {
				require.Len(t, r.Feeds, 1)
				assert.Equal(t, []feed.ReleaseType{feed.ReleaseTypeRelease}, r.Feeds[0].Releases)
			},
		},
		{
			name: "preserves existing releases for github feeds",
			repo: &RepositoryConfig{
				Feeds: []*feed.FeedOptions{
					{
						Type:     "github",
						Name:     "owner/repo",
						Releases: []feed.ReleaseType{feed.ReleaseTypePrerelease, feed.ReleaseTypeRelease},
					},
				},
			},
			checkFn: func(t *testing.T, r *RepositoryConfig) {
				require.Len(t, r.Feeds, 1)
				assert.Equal(t, []feed.ReleaseType{feed.ReleaseTypePrerelease, feed.ReleaseTypeRelease}, r.Feeds[0].Releases)
			},
		},
		{
			name: "does not set releases for non-github feeds",
			repo: &RepositoryConfig{
				Feeds: []*feed.FeedOptions{
					{Type: "apt", Name: "example.com/debian"},
				},
			},
			checkFn: func(t *testing.T, r *RepositoryConfig) {
				require.Len(t, r.Feeds, 1)
				assert.Nil(t, r.Feeds[0].Releases)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.repo.defaults()
			tt.checkFn(t, tt.repo)
		})
	}
}

func TestLoadRepositories(t *testing.T) {
	t.Run("returns error when repos dir doesn't exist", func(t *testing.T) {
		cfg := &Config{
			ConfigDir: "/nonexistent",
			Directories: DirectoriesConfig{
				Repositories: "repos.d",
			},
		}
		err := cfg.loadRepositories()
		require.Error(t, err)
	})

	t.Run("returns error when no yaml files found", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			ConfigDir: tmpDir,
			Directories: DirectoriesConfig{
				Repositories: filepath.Join(tmpDir, "repos.d"),
			},
		}

		// Create empty repos.d directory
		require.NoError(t, filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error { return err }))
		err := cfg.loadRepositories()
		require.Error(t, err)
	})
}
