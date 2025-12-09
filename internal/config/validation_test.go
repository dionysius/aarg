package config

import (
	"testing"

	"github.com/dionysius/aarg/internal/feed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFeed(t *testing.T) {
	tests := []struct {
		name    string
		feed    *feed.FeedOptions
		wantErr error
	}{
		{
			name: "valid github feed",
			feed: &feed.FeedOptions{
				Type: "github",
				Name: "owner/repo",
			},
		},
		{
			name: "valid apt feed",
			feed: &feed.FeedOptions{
				Type: "apt",
				Name: "deb.debian.org/debian",
			},
		},
		{
			name: "valid obs feed with colons",
			feed: &feed.FeedOptions{
				Type: "obs",
				Name: "download.opensuse.org/repositories/home:/dionysius:/test",
			},
		},
		{
			name: "valid obs feed with project identifier",
			feed: &feed.FeedOptions{
				Type: "obs",
				Name: "home:dionysius:test",
			},
		},
		{
			name: "valid path with subdirectories",
			feed: &feed.FeedOptions{
				Type: "apt",
				Name: "example.com/debian/dists/stable",
			},
		},
		{
			name: "port at end of path is allowed",
			feed: &feed.FeedOptions{
				Type: "apt",
				Name: "example.com/debian:443",
			},
		},
		{
			name: "colon with digits in path is allowed",
			feed: &feed.FeedOptions{
				Type: "apt",
				Name: "example.com/pool/main/v/vaultwarden/vaultwarden_1.32.6:1234_amd64.deb",
			},
		},
		{
			name: "missing type",
			feed: &feed.FeedOptions{
				Type: "",
				Name: "owner/repo",
			},
			wantErr: ErrFeedTypeEmpty,
		},
		{
			name: "missing location",
			feed: &feed.FeedOptions{
				Type: "github",
				Name: "",
			},
			wantErr: ErrFeedLocationEmpty,
		},
		{
			name: "invalid type",
			feed: &feed.FeedOptions{
				Type: "invalid",
				Name: "somewhere",
			},
			wantErr: ErrFeedTypeInvalid,
		},
		{
			name: "https scheme",
			feed: &feed.FeedOptions{
				Type: "apt",
				Name: "https://example.com/debian",
			},
			wantErr: ErrFeedLocationScheme,
		},
		{
			name: "http scheme",
			feed: &feed.FeedOptions{
				Type: "apt",
				Name: "http://example.com/debian",
			},
			wantErr: ErrFeedLocationScheme,
		},
		{
			name: "port in host",
			feed: &feed.FeedOptions{
				Type: "apt",
				Name: "example.com:8080/debian",
			},
			wantErr: ErrFeedLocationPort,
		},
		{
			name: "query string",
			feed: &feed.FeedOptions{
				Type: "github",
				Name: "owner/repo?param=value",
			},
			wantErr: ErrFeedLocationQuery,
		},
		{
			name: "fragment",
			feed: &feed.FeedOptions{
				Type: "github",
				Name: "owner/repo#anchor",
			},
			wantErr: ErrFeedLocationFragment,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFeed(tt.feed)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		wantErr   error
		errSubstr string
	}{
		{
			name: "valid config",
			cfg: &Config{
				URL: "https://apt.example.com",
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "test",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
		},
		{
			name: "valid config with http URL",
			cfg: &Config{
				URL: "http://apt.example.com",
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "test",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
		},
		{
			name: "invalid URL scheme",
			cfg: &Config{
				URL: "ftp://apt.example.com",
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "test",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
			errSubstr: "URL must use http or https scheme",
		},
		{
			name: "URL without host",
			cfg: &Config{
				URL: "https://",
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "test",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
			errSubstr: "URL must have a host",
		},
		{
			name: "no repositories",
			cfg: &Config{
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{},
			},
			wantErr: ErrNoRepositories,
		},
		{
			name: "invalid pool mode",
			cfg: &Config{
				Generate: GenerateConfig{
					PoolMode: "invalid",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "test",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
			wantErr:   ErrPoolModeInvalid,
			errSubstr: "invalid",
		},
		{
			name: "repository without name",
			cfg: &Config{
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
			wantErr: ErrRepositoryNameEmpty,
		},
		{
			name: "repository without feeds",
			cfg: &Config{
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name:  "test",
						Feeds: []*feed.FeedOptions{},
					},
				},
			},
			wantErr: ErrNoFeeds,
		},
		{
			name: "repository with invalid feed",
			cfg: &Config{
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "test",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo?query=bad"},
						},
					},
				},
			},
			wantErr: ErrFeedLocationQuery,
		},
		{
			name: "repository with reserved name",
			cfg: &Config{
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "assets",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
			wantErr:   ErrRepositoryNameReserved,
			errSubstr: "assets",
		},
		{
			name: "repository name with invalid characters",
			cfg: &Config{
				Generate: GenerateConfig{
					PoolMode: "hierarchical",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "my.repo",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
			wantErr:   ErrRepositoryNameInvalid,
			errSubstr: "must contain only",
		},
		{
			name: "valid repository name with dash underscore and numbers",
			cfg: &Config{
				Generate: GenerateConfig{
					PoolMode: "redirect",
				},
				Repositories: []*RepositoryConfig{
					{
						Name: "my_repo-123",
						Feeds: []*feed.FeedOptions{
							{Type: "github", Name: "owner/repo"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.cfg)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				if tt.errSubstr != "" {
					assert.Contains(t, err.Error(), tt.errSubstr)
				}
			} else if tt.errSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateRepository(t *testing.T) {
	tests := []struct {
		name      string
		repo      *RepositoryConfig
		wantErr   error
		errSubstr string
	}{
		{
			name: "valid repository",
			repo: &RepositoryConfig{
				Name: "test",
				Feeds: []*feed.FeedOptions{
					{Type: "github", Name: "owner/repo"},
				},
			},
		},
		{
			name: "empty name",
			repo: &RepositoryConfig{
				Name: "",
				Feeds: []*feed.FeedOptions{
					{Type: "github", Name: "owner/repo"},
				},
			},
			wantErr: ErrRepositoryNameEmpty,
		},
		{
			name: "reserved name - assets",
			repo: &RepositoryConfig{
				Name: "assets",
				Feeds: []*feed.FeedOptions{
					{Type: "github", Name: "owner/repo"},
				},
			},
			wantErr:   ErrRepositoryNameReserved,
			errSubstr: "assets",
		},
		{
			name: "reserved name - keys",
			repo: &RepositoryConfig{
				Name: "keys",
				Feeds: []*feed.FeedOptions{
					{Type: "github", Name: "owner/repo"},
				},
			},
			wantErr:   ErrRepositoryNameReserved,
			errSubstr: "keys",
		},
		{
			name: "invalid characters",
			repo: &RepositoryConfig{
				Name: "my@repo!",
				Feeds: []*feed.FeedOptions{
					{Type: "github", Name: "owner/repo"},
				},
			},
			wantErr:   ErrRepositoryNameInvalid,
			errSubstr: "must contain only",
		},
		{
			name: "no feeds",
			repo: &RepositoryConfig{
				Name:  "test",
				Feeds: []*feed.FeedOptions{},
			},
			wantErr: ErrNoFeeds,
		},
		{
			name: "invalid feed",
			repo: &RepositoryConfig{
				Name: "test",
				Feeds: []*feed.FeedOptions{
					{Type: "invalid", Name: "somewhere"},
				},
			},
			wantErr: ErrFeedTypeInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRepository(tt.repo)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				if tt.errSubstr != "" {
					assert.Contains(t, err.Error(), tt.errSubstr)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}
