package common

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dionysius/aarg/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

// readRedirectMap reads and unmarshals a feed's redirects.yaml, failing the test if it doesn't
// parse (which is exactly how a torn concurrent write surfaces: duplicate mapping keys).
func readRedirectMap(t *testing.T, trustedDir string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(trustedDir, "redirects.yaml"))
	require.NoError(t, err)

	var m map[string]string
	require.NoError(t, yaml.Unmarshal(data, &m), "redirects.yaml did not parse")
	return m
}

func TestStorage_LinkFilesToTrusted_redirectMapMerges(t *testing.T) {
	downloadDir := t.TempDir()
	trustedDir := t.TempDir()
	srcPath := filepath.Join(downloadDir, "pkg.deb")
	require.NoError(t, os.WriteFile(srcPath, []byte("contents"), 0644))

	storage := NewStorage(nil, downloadDir, trustedDir)

	require.NoError(t, storage.LinkFilesToTrusted(context.Background(), []*FileForTrust{
		{Path: srcPath, Distribution: "noble", Source: "pkg-a", Hash: "hash-a", Redirect: "redirect-a"},
		{Path: srcPath, Distribution: "noble", Source: "pkg-b", Hash: "hash-b", Redirect: "redirect-b"},
	}))

	redirects := readRedirectMap(t, trustedDir)
	assert.Equal(t, map[string]string{
		filepath.Join("noble", "pkg-a", "pkg.deb"): "redirect-a",
		filepath.Join("noble", "pkg-b", "pkg.deb"): "redirect-b",
	}, redirects)

	// A second, later call must merge in rather than clobber the first batch.
	require.NoError(t, storage.LinkFilesToTrusted(context.Background(), []*FileForTrust{
		{Path: srcPath, Distribution: "noble", Source: "pkg-c", Hash: "hash-c", Redirect: "redirect-c"},
	}))

	redirects = readRedirectMap(t, trustedDir)
	assert.Equal(t, map[string]string{
		filepath.Join("noble", "pkg-a", "pkg.deb"): "redirect-a",
		filepath.Join("noble", "pkg-b", "pkg.deb"): "redirect-b",
		filepath.Join("noble", "pkg-c", "pkg.deb"): "redirect-c",
	}, redirects)
}

// TestStorage_LinkFilesToTrusted_concurrentStoragesDoNotCorruptRedirectMap is a regression test
// for a bug where separate *Storage instances that share the same trustedDir (as happens when one
// apt feed expands into several per-distribution feeds, all writing the same redirects.yaml) each
// held their own unshared mutex, letting concurrent read-modify-write cycles interleave and
// corrupt the file with duplicate mapping keys. The fix moved the lock to a package-level
// redirectFileMu shared by every Storage instance.
func TestStorage_LinkFilesToTrusted_concurrentStoragesDoNotCorruptRedirectMap(t *testing.T) {
	downloadDir := t.TempDir()
	trustedDir := t.TempDir()
	srcPath := filepath.Join(downloadDir, "pkg.deb")
	require.NoError(t, os.WriteFile(srcPath, []byte("contents"), 0644))

	const writers = 32

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, writers)

	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine builds its own Storage instance, mirroring how fetch.go
			// constructs a fresh *Storage per expanded (per-distribution) feed.
			storage := NewStorage(nil, downloadDir, trustedDir)
			<-start // line up goroutines to maximize overlap
			errs[i] = storage.LinkFilesToTrusted(context.Background(), []*FileForTrust{
				{
					Path:         srcPath,
					Distribution: "noble",
					Source:       fmt.Sprintf("pkg-%02d", i),
					Hash:         fmt.Sprintf("hash-%02d", i),
					Redirect:     fmt.Sprintf("redirect-%02d", i),
				},
			})
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "writer %d", i)
	}

	redirects := readRedirectMap(t, trustedDir)
	require.Len(t, redirects, writers)
	for i := range writers {
		key := filepath.Join("noble", fmt.Sprintf("pkg-%02d", i), "pkg.deb")
		assert.Equal(t, fmt.Sprintf("redirect-%02d", i), redirects[key])
	}
}
