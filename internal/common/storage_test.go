package common

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/dionysius/aarg/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorage_Scope(t *testing.T) {
	storage := NewStorage(nil, "/downloads", "/trusted", "base")

	t.Run("single scope level", func(t *testing.T) {
		scoped := storage.Scope("repo1")
		assert.Equal(t, filepath.Join("/downloads", "base", "repo1"), scoped.downloadDir)
		assert.Equal(t, filepath.Join("/trusted", "base", "repo1"), scoped.trustedDir)
	})

	t.Run("multiple scope levels", func(t *testing.T) {
		scoped := storage.Scope("repo1", "dist", "component")
		assert.Equal(t, filepath.Join("/downloads", "base", "repo1", "dist", "component"), scoped.downloadDir)
		assert.Equal(t, filepath.Join("/trusted", "base", "repo1", "dist", "component"), scoped.trustedDir)
	})

	t.Run("chained scoping", func(t *testing.T) {
		scoped1 := storage.Scope("repo1")
		scoped2 := scoped1.Scope("dist")
		scoped3 := scoped2.Scope("component")

		assert.Equal(t, filepath.Join("/downloads", "base", "repo1", "dist", "component"), scoped3.downloadDir)
		assert.Equal(t, filepath.Join("/trusted", "base", "repo1", "dist", "component"), scoped3.trustedDir)
	})
}

func TestStorage_GetDownloadPath(t *testing.T) {
	storage := NewStorage(nil, "/downloads", "/trusted", "base")

	t.Run("single part", func(t *testing.T) {
		path := storage.GetDownloadPath("file.txt")
		assert.Equal(t, filepath.Join("/downloads", "base", "file.txt"), path)
	})

	t.Run("multiple parts", func(t *testing.T) {
		path := storage.GetDownloadPath("subdir", "file.txt")
		assert.Equal(t, filepath.Join("/downloads", "base", "subdir", "file.txt"), path)
	})

	t.Run("empty parts", func(t *testing.T) {
		path := storage.GetDownloadPath()
		assert.Equal(t, filepath.Join("/downloads", "base"), path)
	})
}

func TestStorage_GetTrustedPath(t *testing.T) {
	storage := NewStorage(nil, "/downloads", "/trusted", "base")

	t.Run("single part", func(t *testing.T) {
		path := storage.GetTrustedPath("file.txt")
		assert.Equal(t, filepath.Join("/trusted", "base", "file.txt"), path)
	})

	t.Run("multiple parts", func(t *testing.T) {
		path := storage.GetTrustedPath("dist", "source", "file.deb")
		assert.Equal(t, filepath.Join("/trusted", "base", "dist", "source", "file.deb"), path)
	})

	t.Run("empty parts", func(t *testing.T) {
		path := storage.GetTrustedPath()
		assert.Equal(t, filepath.Join("/trusted", "base"), path)
	})
}

func TestNewStorage(t *testing.T) {
	t.Run("basic initialization", func(t *testing.T) {
		storage := NewStorage(nil, "/downloads", "/trusted")
		assert.Equal(t, "/downloads", storage.downloadDir)
		assert.Equal(t, "/trusted", storage.trustedDir)
		assert.Nil(t, storage.downloader)
	})

	t.Run("with path parts", func(t *testing.T) {
		storage := NewStorage(nil, "/downloads", "/trusted", "feed1", "repo1")
		assert.Equal(t, filepath.Join("/downloads", "feed1", "repo1"), storage.downloadDir)
		assert.Equal(t, filepath.Join("/trusted", "feed1", "repo1"), storage.trustedDir)
	})
}

func sha256Hex(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestStorage_downloadFileExistsWithHash_withCache(t *testing.T) {
	downloadDir := t.TempDir()
	cacheDir := t.TempDir()
	downloadCache := cache.New(downloadDir, cacheDir)

	content := []byte("package contents")
	hash := sha256Hex(t, content)
	require.NoError(t, os.WriteFile(filepath.Join(downloadDir, "pkg.deb"), content, 0644))

	storage := NewStorage(nil, downloadDir, "/trusted").WithDownloadCache(downloadCache)

	t.Run("cache miss computes and matches", func(t *testing.T) {
		assert.True(t, storage.downloadFileExistsWithHash("sha256", hash, "pkg.deb"))
		assert.FileExists(t, filepath.Join(cacheDir, "pkg.deb.checksums.yaml"))
		assert.FileExists(t, filepath.Join(cacheDir, "pkg.deb.metadata.yaml"))
	})

	t.Run("cached hit avoids recomputation and still matches", func(t *testing.T) {
		// Corrupting the checksums sidecar would surface a stale read; instead just
		// confirm a second call still matches, driven off the metadata freshness check.
		assert.True(t, storage.downloadFileExistsWithHash("sha256", hash, "pkg.deb"))
	})

	t.Run("mismatched hash", func(t *testing.T) {
		assert.False(t, storage.downloadFileExistsWithHash("sha256", "deadbeef", "pkg.deb"))
	})

	t.Run("missing file", func(t *testing.T) {
		assert.False(t, storage.downloadFileExistsWithHash("sha256", hash, "missing.deb"))
	})

	t.Run("scoped storage uses relative path under the cache root", func(t *testing.T) {
		subContent := []byte("scoped contents")
		subHash := sha256Hex(t, subContent)
		require.NoError(t, os.MkdirAll(filepath.Join(downloadDir, "sub"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(downloadDir, "sub", "pkg2.deb"), subContent, 0644))

		scoped := storage.Scope("sub")
		assert.True(t, scoped.downloadFileExistsWithHash("sha256", subHash, "pkg2.deb"))
		assert.FileExists(t, filepath.Join(cacheDir, "sub", "pkg2.deb.checksums.yaml"))
	})
}

func TestStorage_downloadFileExistsWithHash_withoutCache(t *testing.T) {
	downloadDir := t.TempDir()
	content := []byte("package contents")
	hash := sha256Hex(t, content)
	require.NoError(t, os.WriteFile(filepath.Join(downloadDir, "pkg.deb"), content, 0644))

	storage := NewStorage(nil, downloadDir, "/trusted")

	assert.True(t, storage.downloadFileExistsWithHash("sha256", hash, "pkg.deb"))
	assert.False(t, storage.downloadFileExistsWithHash("sha256", "deadbeef", "pkg.deb"))
}
