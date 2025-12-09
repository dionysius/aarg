package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchesGlobPatterns(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		value    string
		want     bool
	}{
		{
			name:     "empty patterns matches all",
			patterns: []string{},
			value:    "anything",
			want:     true,
		},
		{
			name:     "exact match",
			patterns: []string{"vaultwarden"},
			value:    "vaultwarden",
			want:     true,
		},
		{
			name:     "no match",
			patterns: []string{"vaultwarden"},
			value:    "other-package",
			want:     false,
		},
		{
			name:     "wildcard match",
			patterns: []string{"vault*"},
			value:    "vaultwarden",
			want:     true,
		},
		{
			name:     "wildcard no match",
			patterns: []string{"vault*"},
			value:    "apache",
			want:     false,
		},
		{
			name:     "question mark wildcard",
			patterns: []string{"vault?"},
			value:    "vault1",
			want:     true,
		},
		{
			name:     "negation excludes",
			patterns: []string{"vault*", "!*-web-*"},
			value:    "vaultwarden-web-vault",
			want:     false,
		},
		{
			name:     "negation allows non-matching",
			patterns: []string{"vault*", "!*-web-*"},
			value:    "vaultwarden",
			want:     true,
		},
		{
			name:     "only negation defaults to match",
			patterns: []string{"!excluded-*"},
			value:    "normal-package",
			want:     true,
		},
		{
			name:     "only negation excludes matched",
			patterns: []string{"!excluded-*"},
			value:    "excluded-package",
			want:     false,
		},
		{
			name:     "multiple patterns one matches",
			patterns: []string{"foo", "bar", "vaultwarden"},
			value:    "vaultwarden",
			want:     true,
		},
		{
			name:     "multiple patterns none match",
			patterns: []string{"foo", "bar", "baz"},
			value:    "vaultwarden",
			want:     false,
		},
		{
			name:     "multiple negations",
			patterns: []string{"*", "!excluded1", "!excluded2"},
			value:    "excluded1",
			want:     false,
		},
		{
			name:     "match all except negations",
			patterns: []string{"*", "!test-*"},
			value:    "prod-package",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesGlobPatterns(tt.patterns, tt.value)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEnsureHardlink(t *testing.T) {
	// Create temporary directory for tests
	tmpDir := t.TempDir()

	t.Run("create new hardlink", func(t *testing.T) {
		src := filepath.Join(tmpDir, "source1.txt")
		dst := filepath.Join(tmpDir, "dest1.txt")

		// Create source file
		require.NoError(t, os.WriteFile(src, []byte("test content"), 0644))

		// Create hardlink
		require.NoError(t, EnsureHardlink(src, dst))

		// Verify destination exists
		dstContent, err := os.ReadFile(dst)
		require.NoError(t, err)
		assert.Equal(t, "test content", string(dstContent))

		// Verify they are the same file (same inode)
		srcInfo, err := os.Lstat(src)
		require.NoError(t, err)
		dstInfo, err := os.Lstat(dst)
		require.NoError(t, err)
		assert.True(t, os.SameFile(srcInfo, dstInfo))
	})

	t.Run("hardlink already exists to same file", func(t *testing.T) {
		src := filepath.Join(tmpDir, "source2.txt")
		dst := filepath.Join(tmpDir, "dest2.txt")

		// Create source file
		require.NoError(t, os.WriteFile(src, []byte("test content"), 0644))

		// Create hardlink first time
		require.NoError(t, EnsureHardlink(src, dst))

		// Call again - should not fail and should still be the same file
		require.NoError(t, EnsureHardlink(src, dst))

		srcInfo, err := os.Lstat(src)
		require.NoError(t, err)
		dstInfo, err := os.Lstat(dst)
		require.NoError(t, err)
		assert.True(t, os.SameFile(srcInfo, dstInfo))
	})

	t.Run("replace existing different file", func(t *testing.T) {
		src := filepath.Join(tmpDir, "source3.txt")
		dst := filepath.Join(tmpDir, "dest3.txt")

		// Create source file
		require.NoError(t, os.WriteFile(src, []byte("new content"), 0644))

		// Create different destination file
		require.NoError(t, os.WriteFile(dst, []byte("old content"), 0644))

		// Verify they are different files
		srcInfo1, err := os.Lstat(src)
		require.NoError(t, err)
		dstInfo1, err := os.Lstat(dst)
		require.NoError(t, err)
		assert.False(t, os.SameFile(srcInfo1, dstInfo1))

		// Create hardlink (should replace)
		require.NoError(t, EnsureHardlink(src, dst))

		// Verify destination now has new content
		dstContent, err := os.ReadFile(dst)
		require.NoError(t, err)
		assert.Equal(t, "new content", string(dstContent))

		// Verify they are now the same file
		srcInfo2, err := os.Lstat(src)
		require.NoError(t, err)
		dstInfo2, err := os.Lstat(dst)
		require.NoError(t, err)
		assert.True(t, os.SameFile(srcInfo2, dstInfo2))
	})

	t.Run("source file does not exist", func(t *testing.T) {
		src := filepath.Join(tmpDir, "nonexistent.txt")
		dst := filepath.Join(tmpDir, "dest4.txt")

		err := EnsureHardlink(src, dst)
		assert.Error(t, err)
	})

	t.Run("concurrent hardlink creation", func(t *testing.T) {
		src := filepath.Join(tmpDir, "source5.txt")
		dst := filepath.Join(tmpDir, "dest5.txt")

		// Create source file
		require.NoError(t, os.WriteFile(src, []byte("concurrent test"), 0644))

		// Try to create hardlink multiple times concurrently
		done := make(chan error, 3)
		for i := 0; i < 3; i++ {
			go func() {
				done <- EnsureHardlink(src, dst)
			}()
		}

		// All should succeed
		for i := 0; i < 3; i++ {
			require.NoError(t, <-done)
		}

		// Verify hardlink is correct
		srcInfo, err := os.Lstat(src)
		require.NoError(t, err)
		dstInfo, err := os.Lstat(dst)
		require.NoError(t, err)
		assert.True(t, os.SameFile(srcInfo, dstInfo))
	})
}
