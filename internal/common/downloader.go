package common

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/alitto/pond/v2"
	"github.com/cavaliergopher/grab/v3"
)

// NewDownloader creates and initializes a new download manager with a worker pool
func NewDownloader(ctx context.Context, httpClient *http.Client, maxParallel int, decompressor *DeCompressor) *Downloader {
	pool := pond.NewResultPool[Result](maxParallel, pond.WithContext(ctx), pond.WithoutPanicRecovery())

	grabClient := &grab.Client{
		HTTPClient: httpClient,
	}

	return &Downloader{
		pool:         pool,
		client:       grabClient,
		decompressor: decompressor,
		inflight:     sync.Map{},
	}
}

// Downloader handles parallel downloads with configurable settings
type Downloader struct {
	pool         pond.ResultPool[Result]
	client       *grab.Client  // grab HTTP client for downloads
	decompressor *DeCompressor // DeCompressor for parallel decompression operations

	// Download deduplication: tracks in-flight downloads by destination path
	inflight sync.Map // map[string]*downloadWaiter for concurrent access
}

// downloadWaiter allows multiple goroutines to wait for the same download
type downloadWaiter struct {
	done     chan struct{}
	result   *DownloadResult
	err      error
	url      string // Source URL for this download
	checksum string // Expected checksum for this download
}

// DownloadRequest represents one or more files to download to a destination
type DownloadRequest struct {
	URL         string // URL to download
	Destination string // Full file path where file will be saved
	Checksum    string // Optional SHA256 checksum (hex-encoded) for verification during download
}

// DownloadResult contains the outcome of a single download job
type DownloadResult struct {
	*DownloadRequest       // The request that was downloaded
	Size             int64 // Bytes downloaded
}

func (d *DownloadResult) Destination() string {
	return d.DownloadRequest.Destination
}

func (m *Downloader) download(ctx context.Context, req *DownloadRequest) (*DownloadResult, error) {
	// Create grab request
	grabReq, err := grab.NewRequest(req.Destination, req.URL)
	if err != nil {
		return nil, err
	}

	// Apply context to grab request
	grabReq = grabReq.WithContext(ctx)

	// Configure checksum verification if provided
	if req.Checksum != "" {
		// Decode hex checksum
		expectedSum, err := hex.DecodeString(req.Checksum)
		if err != nil {
			return nil, err
		}
		// Set checksum with SHA256, delete file on validation failure
		grabReq.SetChecksum(sha256.New(), expectedSum, true)
	}

	// Start download
	resp := m.client.Do(grabReq)

	// Wait for completion
	<-resp.Done

	if resp.Err() != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(req.Destination), resp.Err())
	}

	// Log successful download
	slog.Debug("Downloaded", "file", filepath.Base(req.Destination), "bytes", resp.Size())

	return &DownloadResult{
		DownloadRequest: req,
		Size:            resp.Size(),
	}, nil
}

// Shutdown gracefully stops the download manager
func (m *Downloader) Shutdown() {
	m.pool.StopAndWait()
}

// Download downloads one or more files in parallel using a task group
// Returns a task group that can be waited on. Call Wait() to get results and any error.
// This method is thread-safe and can be called concurrently.
// If multiple goroutines request the same destination file with the same URL and checksum,
// only one download occurs and all waiters share the result.
func (m *Downloader) Download(ctx context.Context, requests ...*DownloadRequest) pond.ResultTaskGroup[Result] {
	group := m.pool.NewGroupContext(ctx)

	for _, req := range requests {
		// Capture for closure
		group.SubmitErr(func() (Result, error) {
			return m.downloadWithDedup(ctx, req)
		})
	}

	return group
}

// downloadWithDedup ensures only one download happens per destination path
// Validates that concurrent requests to the same destination have matching checksums or url
func (m *Downloader) downloadWithDedup(ctx context.Context, req *DownloadRequest) (*DownloadResult, error) {
	// Try to register this download as in-flight
	waiter := &downloadWaiter{
		done:     make(chan struct{}),
		url:      req.URL,
		checksum: req.Checksum,
	}

	// LoadOrStore atomically checks if a download exists and stores if not
	actual, loaded := m.inflight.LoadOrStore(req.Destination, waiter)

	if loaded {
		// Download already in progress - validate consistency
		existingWaiter := actual.(*downloadWaiter)

		// Check checksum matches if both are specified otherwise check URL matches
		if req.Checksum != "" && req.Checksum != existingWaiter.checksum {
			return nil, fmt.Errorf("checksum conflict for %s: existing download expects %s but new request expects %s",
				req.Destination, existingWaiter.checksum, req.Checksum)
		} else if req.URL != existingWaiter.url {
			return nil, fmt.Errorf("URL conflict for %s: existing download from %s but new request from %s",
				req.Destination, existingWaiter.url, req.URL)
		}

		// Wait for existing download to complete
		<-existingWaiter.done
		return existingWaiter.result, existingWaiter.err
	}

	// Download registered, prepare destination directory
	if err := os.MkdirAll(filepath.Dir(req.Destination), 0755); err != nil {
		waiter.err = err
		close(waiter.done)
		m.inflight.Delete(req.Destination)
		return nil, err
	}

	// Ensure cleanup happens even if panic occurs
	defer func() {
		// Clean up inflight tracking after notifying waiters
		m.inflight.Delete(req.Destination)
	}()

	// Perform the actual download
	result, err := m.download(ctx, req)

	// Store result and notify waiters before cleanup
	waiter.result = result
	waiter.err = err
	close(waiter.done)

	return result, err
}

// DownloadAndDecompress downloads compressed files and automatically decompresses them
// The compression format is detected from the file extension (.gz, .xz, .bz2).
// Original compressed files are kept. Returns results with paths pointing to decompressed files.
// Returns an error if the file has an unsupported or no compression extension.
// This method is thread-safe and can be called concurrently.
func (m *Downloader) DownloadAndDecompress(ctx context.Context, requests ...*DownloadRequest) pond.ResultTaskGroup[Result] {
	group := m.pool.NewGroupContext(ctx)

	for _, req := range requests {
		// Capture for closure
		group.SubmitErr(func() (Result, error) {
			// Validate compression format from destination filename
			format := DetectCompressionFormat(req.Destination)
			if format == CompressionNone {
				return nil, fmt.Errorf("file must have a compression extension (.gz, .xz, .bz2): %s", req.Destination)
			}

			// Download using worker pool
			downloadGroup := m.Download(ctx, req)
			downloadResults, err := downloadGroup.Wait()
			if err != nil {
				return nil, err
			}

			downloadResult, ok := downloadResults[0].(*DownloadResult)
			if !ok {
				return nil, fmt.Errorf("invalid download result type")
			}

			// Decompress using worker pool
			decompGroup := m.decompressor.Decompress(ctx, downloadResult.Destination())
			decompResults, err := decompGroup.Wait()
			if err != nil {
				return nil, err
			}

			// Update result to point to decompressed file
			downloadResult.DownloadRequest.Destination = decompResults[0].Destination()
			return downloadResult, nil
		})
	}

	return group
}
