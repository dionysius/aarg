// Package cache provides a lazily-populated, file-level cache for parsed package data.
//
// Cache files are stored under a cache directory that mirrors the structure of the
// trusted directory. For each cached source file the following sidecar files may exist:
//
//   - <relpath>.metadata.yaml  — inode, size and mtime_ns used for freshness checks
//   - <relpath>.checksums.yaml — MD5, SHA1, SHA256, SHA512 of the file
//   - <relpath>.control        — YAML-serialised control stanza (binary packages only)
//
// Freshness is determined entirely by comparing the stored inode, size and mtime_ns
// against a single os.Stat call — no file content is re-read just to validate the cache.
// Cache files are written atomically via a temporary file and rename.
package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/aptly-dev/aptly/deb"
	"github.com/aptly-dev/aptly/utils"
	"github.com/dionysius/aarg/debext"
	"gopkg.in/yaml.v3"
)

// FileMetadata stores stat information used to determine whether a cache entry is still fresh.
type FileMetadata struct {
	Inode   uint64 `yaml:"inode"`
	Size    int64  `yaml:"size"`
	MtimeNs int64  `yaml:"mtime_ns"`
}

// FileChecksums stores all checksums of a file.
type FileChecksums struct {
	MD5    string `yaml:"md5"`
	SHA1   string `yaml:"sha1"`
	SHA256 string `yaml:"sha256"`
	SHA512 string `yaml:"sha512"`
}

// Cache provides a lazily-populated, file-level cache rooted at cacheDir that mirrors
// the structure of trustedDir.
type Cache struct {
	trustedDir string
	cacheDir   string
}

// New returns a Cache that mirrors trustedDir under cacheDir.
// Returns nil if cacheDir is empty.
func New(trustedDir, cacheDir string) *Cache {
	return &Cache{trustedDir: trustedDir, cacheDir: cacheDir}
}

// cacheBasePath returns the base path (without sidecar extension) for relPath.
// Returns an error if relPath is not a valid local relative path.
func (c *Cache) cacheBasePath(relPath string) (string, error) {
	if !filepath.IsLocal(relPath) {
		return "", fmt.Errorf("cache: %q is not a valid relative path", relPath)
	}

	return filepath.Join(c.cacheDir, relPath), nil
}

// statFresh stats absPath and compares against the stored metadata.
// Returns (fresh, stat, error). stat is non-nil on a successful os.Stat even when not fresh.
func statFresh(absPath, metaPath string) (bool, os.FileInfo, error) {
	stat, err := os.Stat(absPath)
	if err != nil {
		return false, nil, err
	}

	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, stat, nil
		}

		return false, stat, err
	}

	var meta FileMetadata
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return false, stat, nil // corrupt metadata — treat as miss
	}

	sys, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return false, stat, nil // cannot verify inode
	}

	fresh := sys.Ino == meta.Inode &&
		stat.Size() == meta.Size &&
		stat.ModTime().UnixNano() == meta.MtimeNs

	return fresh, stat, nil
}

// writeMetadata atomically writes the .metadata.yaml sidecar for absPath.
func writeMetadata(absPath, base string, stat os.FileInfo) error {
	sys, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cache: cannot get inode for %s", absPath)
	}

	meta := FileMetadata{
		Inode:   sys.Ino,
		Size:    stat.Size(),
		MtimeNs: stat.ModTime().UnixNano(),
	}

	data, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}

	return writeAtomic(base+".metadata.yaml", data)
}

// GetChecksums returns checksums for relPath (relative to trustedDir), loading from cache
// when fresh or computing and writing the cache on a miss.
func (c *Cache) GetChecksums(relPath string) (utils.ChecksumInfo, error) {
	absPath := filepath.Join(c.trustedDir, relPath)
	base, err := c.cacheBasePath(relPath)
	if err != nil {
		return utils.ChecksumInfo{}, err
	}

	fresh, stat, err := statFresh(absPath, base+".metadata.yaml")
	if err != nil {
		return utils.ChecksumInfo{}, err
	}

	if fresh {
		if info, loadErr := loadChecksums(base); loadErr == nil {
			info.Size = stat.Size()
			return info, nil
		}
		// checksums file missing or corrupt despite fresh metadata — fall through to recompute
	}

	info, err := utils.ChecksumsForFile(absPath)
	if err != nil {
		return utils.ChecksumInfo{}, err
	}

	// Persist cache; errors are non-fatal
	if stat == nil {
		stat, _ = os.Stat(absPath)
	}

	if stat != nil {
		if mkErr := os.MkdirAll(filepath.Dir(base), 0755); mkErr == nil {
			_ = storeChecksums(base, info)
			_ = writeMetadata(absPath, base, stat)
		}
	}

	return info, nil
}

func loadChecksums(base string) (utils.ChecksumInfo, error) {
	data, err := os.ReadFile(base + ".checksums.yaml")
	if err != nil {
		return utils.ChecksumInfo{}, err
	}

	var fc FileChecksums
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return utils.ChecksumInfo{}, err
	}

	return utils.ChecksumInfo{
		MD5:    fc.MD5,
		SHA1:   fc.SHA1,
		SHA256: fc.SHA256,
		SHA512: fc.SHA512,
	}, nil
}

func storeChecksums(base string, info utils.ChecksumInfo) error {
	fc := FileChecksums{
		MD5:    info.MD5,
		SHA1:   info.SHA1,
		SHA256: info.SHA256,
		SHA512: info.SHA512,
	}

	data, err := yaml.Marshal(fc)
	if err != nil {
		return err
	}

	return writeAtomic(base+".checksums.yaml", data)
}

// GetBinaryControl returns the cached control stanza for a .deb file, or nil if not cached.
// relPath is relative to trustedDir. The returned stanza contains pure control fields only —
// without Filename, Size or checksum fields, which are added by ParseBinary.
func (c *Cache) GetBinaryControl(relPath string) (deb.Stanza, error) {
	absPath := filepath.Join(c.trustedDir, relPath)
	base, err := c.cacheBasePath(relPath)
	if err != nil {
		return nil, err
	}

	fresh, _, err := statFresh(absPath, base+".metadata.yaml")
	if err != nil {
		return nil, err
	}

	if !fresh {
		return nil, nil
	}

	data, err := os.ReadFile(base + ".control")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var m map[string]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, nil // corrupt — treat as miss
	}

	return deb.Stanza(m), nil
}

// StoreBinaryControl writes the control stanza cache for a .deb file.
// relPath is relative to trustedDir. Errors are non-fatal; callers may log or ignore them.
// The metadata.yaml is written by GetChecksums; StoreBinaryControl only writes .control.
func (c *Cache) StoreBinaryControl(relPath string, stanza deb.Stanza) error {
	base, err := c.cacheBasePath(relPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(base), 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(map[string]string(stanza))
	if err != nil {
		return err
	}

	return writeAtomic(base+".control", data)
}

// ParseBinary parses a .deb or .ddeb file at relPath (relative to trustedDir) using cached
// control data and checksums. On a cache miss the control data is extracted from the archive
// and stored for future runs.
func (c *Cache) ParseBinary(relPath string) (*deb.Package, error) {
	absPath := filepath.Join(c.trustedDir, relPath)

	stanza, err := c.GetBinaryControl(relPath)
	if err != nil {
		return nil, err
	}

	if stanza == nil {
		// Cache miss: extract control data from the archive
		stanza, err = deb.GetControlFileFromDeb(absPath)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", absPath, err)
		}
		// Store for future runs; errors are non-fatal
		_ = c.StoreBinaryControl(relPath, stanza)
	}

	// Get or compute checksums (also writes .checksums.yaml and .metadata.yaml on miss)
	checksums, err := c.GetChecksums(relPath)
	if err != nil {
		return nil, err
	}

	stanza["Filename"] = relPath
	stanza["Size"] = fmt.Sprintf("%d", checksums.Size)
	stanza["MD5sum"] = checksums.MD5
	stanza["SHA1"] = checksums.SHA1
	stanza["SHA256"] = checksums.SHA256
	stanza["SHA512"] = checksums.SHA512

	return deb.NewPackageFromControlFile(stanza), nil
}

// ParseSource parses a .dsc file at relPath (relative to trustedDir) and returns a source
// package with complete checksums for all referenced files.
// The .dsc is parsed and its signature verified on every call (the file is tiny).
// Checksums for the larger referenced source files (.orig.tar.*, .debian.tar.*, etc.) are
// obtained from the cache or computed on first encounter and cached for subsequent runs.
func (c *Cache) ParseSource(relPath string, verifier *debext.Verifier) (*deb.Package, error) {
	absPath := filepath.Join(c.trustedDir, relPath)

	pkg, err := debext.ParseSource(absPath, verifier, filepath.Dir(relPath))
	if err != nil {
		return nil, err
	}

	completeFiles := make([]deb.PackageFile, 0, len(pkg.Files()))

	for _, file := range pkg.Files() {
		fileRelPath := filepath.Join(pkg.Stanza()["Directory"], file.Filename)

		checksums, err := c.GetChecksums(fileRelPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get checksums for %s: %w", fileRelPath, err)
		}

		completeFiles = append(completeFiles, deb.PackageFile{
			Filename:  file.Filename,
			Checksums: checksums,
		})
	}

	pkg.UpdateFiles(completeFiles)

	return pkg, nil
}

// writeAtomic writes data to path atomically using a temporary file and rename.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".cache-tmp-*")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()

	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()

	if writeErr != nil {
		_ = os.Remove(tmpName)

		return writeErr
	}

	if closeErr != nil {
		_ = os.Remove(tmpName)

		return closeErr
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)

		return err
	}

	return nil
}
