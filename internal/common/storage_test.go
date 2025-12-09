package common

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
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
