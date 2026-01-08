package common

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alitto/pond/v2"
	"gopkg.in/yaml.v3"
)

// FileForTrust represents a file to be moved to trusted storage with its metadata
type FileForTrust struct {
	Path         string
	Distribution string
	Hash         string // SHA256 hash
	Source       string // Source package name for grouping
	Redirect     string // Relative redirect suffix (original file source) relative to the feed base URL
}

// Storage handles file storage and downloads in downloads/, trusted/, and public/ directories
type Storage struct {
	downloadDir   string
	trustedDir    string
	downloader    *Downloader
	redirectMapMu sync.Mutex // Protects redirects.yaml read-modify-write operations
}

// NewStorage creates a new storage manager
// downloadDir and trustedDir should be absolute paths to the base directories
func NewStorage(downloader *Downloader, downloadDir, trustedDir string, pathParts ...string) *Storage {
	// Build scoped paths within downloads and trusted directories
	scopedDownloadDir := filepath.Join(append([]string{downloadDir}, pathParts...)...)
	scopedTrustedDir := filepath.Join(append([]string{trustedDir}, pathParts...)...)

	return &Storage{
		downloadDir: scopedDownloadDir,
		trustedDir:  scopedTrustedDir,
		downloader:  downloader,
	}
}

// Scope creates a new Storage instance scoped to additional path parts
func (m *Storage) Scope(pathParts ...string) *Storage {
	// Append path parts to current directories
	return &Storage{
		downloadDir: filepath.Join(append([]string{m.downloadDir}, pathParts...)...),
		trustedDir:  filepath.Join(append([]string{m.trustedDir}, pathParts...)...),
		downloader:  m.downloader,
	}
}

// GetDownloadPath returns the full path for a file in the download directory
func (m *Storage) GetDownloadPath(pathParts ...string) string {
	return filepath.Join(append([]string{m.downloadDir}, pathParts...)...)
}

// getTrustedPath returns the full path for a file in the trusted directory
func (m *Storage) getTrustedPath(pathParts ...string) string {
	return filepath.Join(append([]string{m.trustedDir}, pathParts...)...)
}

// GetTrustedPath returns the full path for a file in the trusted directory (public method)
func (m *Storage) GetTrustedPath(pathParts ...string) string {
	return m.getTrustedPath(pathParts...)
}

// ensureTrustedDir creates the trusted directory structure
func (m *Storage) ensureTrustedDir(pathParts ...string) error {
	dir := m.getTrustedPath(pathParts...)
	return os.MkdirAll(dir, 0755)
}

// LinkFilesToTrusted creates hardlinks from downloads to trusted with distribution-based organization
func (m *Storage) LinkFilesToTrusted(ctx context.Context, files []*FileForTrust) error {
	seen := make(map[string]string)      // filepath -> hash for deduplication
	redirects := make(map[string]string) // relative path -> redirect suffix

	for _, file := range files {
		// Build destination path in trusted
		filename := filepath.Base(file.Path)
		relPath := filepath.Join(file.Distribution, file.Source, filename)
		dstPath := m.getTrustedPath(relPath)

		// Check for duplicates within this batch
		if existingHash, exists := seen[dstPath]; exists {
			if existingHash != file.Hash {
				return fmt.Errorf("conflict: different files want to be placed at %s", dstPath)
			}
			// Same hash, skip duplicate
			continue
		}
		seen[dstPath] = file.Hash

		// Create target directory
		if err := m.ensureTrustedDir(file.Distribution, file.Source); err != nil {
			return err
		}

		// Create hardlink from download to trusted
		if err := EnsureHardlink(file.Path, dstPath); err != nil {
			return err
		}

		// Collect redirect suffix with relative path as key
		redirects[relPath] = file.Redirect
	}

	if len(redirects) > 0 {
		// Write redirect map if any redirects were collected
		if err := m.writeRedirectMap(redirects); err != nil {
			return err
		}
	}

	return nil
}

// fileExistsWithHash checks if a file exists with the expected hash (hashMethod: "sha256")
func fileExistsWithHash(path, hashMethod, expectedHash string) bool {
	if expectedHash == "" {
		return false
	}

	if hashMethod != "sha256" {
		return false
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}

	actualHash := hex.EncodeToString(h.Sum(nil))

	return strings.EqualFold(actualHash, expectedHash)
}

// downloadFileExistsWithHash checks if a file exists in downloads folder with expected hash
func (m *Storage) downloadFileExistsWithHash(hashMethod, expectedHash string, pathParts ...string) bool {
	exists := fileExistsWithHash(m.GetDownloadPath(pathParts...), hashMethod, expectedHash)

	if exists {
		slog.Debug("Match exists, download skipped", "file", filepath.Join(pathParts...), "sha256", expectedHash)
	} else {
		slog.Debug("Doesn't match or exist, downloading", "file", filepath.Join(pathParts...), "sha256", expectedHash)
	}

	return exists
}

// Download downloads files to the downloads directory (destinations are relative paths)
func (m *Storage) Download(ctx context.Context, requests ...*DownloadRequest) pond.ResultTaskGroup[Result] {
	// Convert relative destinations to absolute paths
	for _, req := range requests {
		req.Destination = m.GetDownloadPath(req.Destination)
	}
	return m.downloader.Download(ctx, requests...)
}

// DownloadAndDecompress downloads and decompresses files based on extension (destinations are relative paths)
func (m *Storage) DownloadAndDecompress(ctx context.Context, requests ...*DownloadRequest) pond.ResultTaskGroup[Result] {
	// Convert relative destinations to absolute paths
	for _, req := range requests {
		req.Destination = m.GetDownloadPath(req.Destination)
	}
	return m.downloader.DownloadAndDecompress(ctx, requests...)
}

// FileExistsOrDownload returns path to file with expected hash, downloading if necessary
func (m *Storage) FileExistsOrDownload(ctx context.Context, hashMethod, expectedHash, downloadURL string, pathParts ...string) (string, error) {
	// Check if file exists in downloads with correct hash
	if m.downloadFileExistsWithHash(hashMethod, expectedHash, pathParts...) {
		return m.GetDownloadPath(pathParts...), nil
	}

	// Download file if not already present with correct digest
	group := m.Download(ctx, &DownloadRequest{
		URL:         downloadURL,
		Destination: filepath.Join(pathParts...),
		Checksum:    expectedHash,
	})
	results, err := group.Wait()
	if err != nil {
		return "", err
	}
	return results[0].Destination(), nil
}

// UncompressedFileExistsOrDownloadAndDecompress returns path to uncompressed file with expected hash, downloading and decompressing if necessary
func (m *Storage) UncompressedFileExistsOrDownloadAndDecompress(ctx context.Context, hashMethod, uncompressedHash, compressedHash, downloadURL string, compressionFormat CompressionFormat, pathParts ...string) (string, error) {
	// Check if uncompressed file exists in downloads
	if m.downloadFileExistsWithHash(hashMethod, uncompressedHash, pathParts...) {
		return m.GetDownloadPath(pathParts...), nil
	}

	// Check if compressed file exists with correct hash
	compressedParts := append([]string{}, pathParts...)
	compressedParts[len(compressedParts)-1] += compressionFormat.Extension()

	if compressionFormat != CompressionNone && m.downloadFileExistsWithHash(hashMethod, compressedHash, compressedParts...) {
		// Compressed file exists, decompress it
		compressedPath := m.GetDownloadPath(compressedParts...)
		group := m.downloader.decompressor.Decompress(ctx, compressedPath)
		results, err := group.Wait()
		if err != nil {
			return "", err
		}
		return results[0].Destination(), nil
	}

	// Download and decompress file
	compressedFilePath := filepath.Join(pathParts...) + compressionFormat.Extension()

	group := m.DownloadAndDecompress(ctx, &DownloadRequest{
		URL:         downloadURL,
		Destination: compressedFilePath,
		Checksum:    compressedHash,
	})
	results, err := group.Wait()
	if err != nil {
		return "", err
	}
	return results[0].Destination(), nil
}

// writeRedirectMap writes the redirects.yaml file at the feed scope
// The map uses relative paths from the feed's trusted directory as keys,
// and redirect targets relative to the feed's base URL as values.
// Merges with existing redirects to support incremental updates.
func (m *Storage) writeRedirectMap(redirects map[string]string) error {
	// Protect read-modify-write with mutex to prevent concurrent updates
	m.redirectMapMu.Lock()
	defer m.redirectMapMu.Unlock()

	mapFile := filepath.Join(m.trustedDir, "redirects.yaml")

	// Load existing redirects if file exists
	existingRedirects := make(map[string]string)
	if data, err := os.ReadFile(mapFile); err == nil {
		// File exists, unmarshal it
		if err := yaml.Unmarshal(data, &existingRedirects); err != nil {
			return fmt.Errorf("failed to unmarshal existing redirect map: %w", err)
		}
	} else if !os.IsNotExist(err) {
		// Error other than file not existing
		return fmt.Errorf("failed to read existing redirect map: %w", err)
	}

	// Merge new redirects into existing ones
	maps.Copy(existingRedirects, redirects)

	// Write back merged redirects
	data, err := yaml.Marshal(existingRedirects)
	if err != nil {
		return fmt.Errorf("failed to marshal redirect map: %w", err)
	}

	if err := os.WriteFile(mapFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write redirect map %s: %w", mapFile, err)
	}

	return nil
}
