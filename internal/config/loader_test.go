package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindConfigFile(t *testing.T) {
	t.Run("uses explicit path when provided", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte("test: value\n"), 0644))

		result, err := findConfigFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, cfgPath, result)
	})

	t.Run("returns error for non-existent explicit path", func(t *testing.T) {
		_, err := findConfigFile("/nonexistent/config.yaml")
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("searches standard locations when no path provided", func(t *testing.T) {
		_, err := findConfigFile("")
		// Will fail unless one of the standard locations exists
		// This test documents the behavior rather than asserting success
		if err != nil {
			assert.ErrorIs(t, err, os.ErrNotExist)
		}
	})
}

func TestFileExists(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "file exists",
			path: func() string {
				tmpDir := t.TempDir()
				path := filepath.Join(tmpDir, "test.txt")
				require.NoError(t, os.WriteFile(path, []byte("test"), 0644))
				return path
			}(),
			want: true,
		},
		{
			name: "file does not exist",
			path: "/nonexistent/file.txt",
			want: false,
		},
		{
			name: "directory exists but is not a file",
			path: func() string {
				return t.TempDir()
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, fileExists(tt.path))
		})
	}
}

func TestLoad(t *testing.T) {
	t.Run("loads valid config", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yaml")
		reposDir := filepath.Join(tmpDir, "repos.d")
		require.NoError(t, os.Mkdir(reposDir, 0755))

		// Create config file
		cfgContent := `directories:
  root: /tmp/aarg
  repositories: repos.d
signing:
  private_key: keys/private.asc
  public_key: keys/public.asc
`
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0644))

		// Create a repository config
		repoContent := `feeds:
  - github: owner/repo
`
		require.NoError(t, os.WriteFile(filepath.Join(reposDir, "test.yaml"), []byte(repoContent), 0644))

		cfg, err := Load(cfgPath)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, tmpDir, cfg.ConfigDir)
		assert.Equal(t, "/tmp/aarg", cfg.Directories.Root)
		assert.Len(t, cfg.Repositories, 1)
		assert.Equal(t, "test", cfg.Repositories[0].Name)
	})

	t.Run("applies defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yaml")
		reposDir := filepath.Join(tmpDir, "repos.d")
		require.NoError(t, os.Mkdir(reposDir, 0755))

		// Create minimal config
		cfgContent := `directories:
  repositories: repos.d
signing:
  private_key: keys/private.asc
  public_key: keys/public.asc
`
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0644))

		// Create a repository config
		repoContent := `feeds:
  - github: owner/repo
`
		require.NoError(t, os.WriteFile(filepath.Join(reposDir, "test.yaml"), []byte(repoContent), 0644))

		cfg, err := Load(cfgPath)
		require.NoError(t, err)

		// Check defaults were applied
		assert.Equal(t, "/var/lib/aarg", cfg.Directories.Root)
		assert.Equal(t, "downloads", cfg.Directories.Downloads)
		assert.Equal(t, "hierarchical", cfg.Generate.PoolMode)
	})

	t.Run("returns error for invalid config", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yaml")

		// Create invalid YAML
		require.NoError(t, os.WriteFile(cfgPath, []byte("invalid: [yaml"), 0644))

		_, err := Load(cfgPath)
		require.Error(t, err)
	})

	t.Run("returns error when config file not found", func(t *testing.T) {
		_, err := Load("/nonexistent/config.yaml")
		require.Error(t, err)
	})

	t.Run("returns error when repositories directory missing", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yaml")

		cfgContent := `directories:
  repositories: nonexistent
signing:
  private_key: keys/private.asc
  public_key: keys/public.asc
`
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0644))

		_, err := Load(cfgPath)
		require.Error(t, err)
	})

	t.Run("validates loaded config", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yaml")
		reposDir := filepath.Join(tmpDir, "repos.d")
		require.NoError(t, os.Mkdir(reposDir, 0755))

		cfgContent := `directories:
  repositories: repos.d
signing:
  private_key: keys/private.asc
  public_key: keys/public.asc
`
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0644))

		// Create repository with no feeds to trigger validation error
		repoContent := `feeds: []
`
		require.NoError(t, os.WriteFile(filepath.Join(reposDir, "test.yaml"), []byte(repoContent), 0644))

		_, err := Load(cfgPath)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoFeeds)
	})

	t.Run("loads multiple repositories", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yaml")
		reposDir := filepath.Join(tmpDir, "repos.d")
		require.NoError(t, os.Mkdir(reposDir, 0755))

		cfgContent := `directories:
  repositories: repos.d
signing:
  private_key: keys/private.asc
  public_key: keys/public.asc
`
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0644))

		// Create multiple repository configs
		repo1 := `feeds:
  - github: owner/repo1
`
		repo2 := `feeds:
  - apt: http://example.com/debian
`
		require.NoError(t, os.WriteFile(filepath.Join(reposDir, "repo1.yaml"), []byte(repo1), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(reposDir, "repo2.yaml"), []byte(repo2), 0644))

		cfg, err := Load(cfgPath)
		require.NoError(t, err)
		assert.Len(t, cfg.Repositories, 2)

		// Check that names are set correctly
		repoNames := []string{cfg.Repositories[0].Name, cfg.Repositories[1].Name}
		assert.Contains(t, repoNames, "repo1")
		assert.Contains(t, repoNames, "repo2")
	})

	t.Run("applies repository defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.yaml")
		reposDir := filepath.Join(tmpDir, "repos.d")
		require.NoError(t, os.Mkdir(reposDir, 0755))

		cfgContent := `directories:
  repositories: repos.d
signing:
  private_key: keys/private.asc
  public_key: keys/public.asc
`
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0644))

		// Create repository with architectures
		repoContent := `architectures:
  - amd64
  - arm64
feeds:
  - github: owner/repo
`
		require.NoError(t, os.WriteFile(filepath.Join(reposDir, "test.yaml"), []byte(repoContent), 0644))

		cfg, err := Load(cfgPath)
		require.NoError(t, err)
		require.Len(t, cfg.Repositories, 1)
		require.Len(t, cfg.Repositories[0].Feeds, 1)

		// Check that feed inherited architectures
		assert.Equal(t, []string{"amd64", "arm64"}, cfg.Repositories[0].Feeds[0].Architectures)
	})
}
