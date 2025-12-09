package common

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alitto/pond/v2"
	"github.com/dsnet/compress/bzip2"
	"github.com/ulikunitz/xz"
)

// CompressionFormat represents a supported compression format
type CompressionFormat string

const (
	CompressionNone  CompressionFormat = ""
	CompressionGzip  CompressionFormat = "gz"
	CompressionBzip2 CompressionFormat = "bz2"
	CompressionXZ    CompressionFormat = "xz"
)

// DetectCompressionFormat returns the compression format based on file extension
func DetectCompressionFormat(filename string) CompressionFormat {
	ext := filepath.Ext(filename)
	switch ext {
	case ".gz":
		return CompressionGzip
	case ".bz2":
		return CompressionBzip2
	case ".xz":
		return CompressionXZ
	default:
		return CompressionNone
	}
}

// Extension returns the file extension for the compression format
func (f CompressionFormat) Extension() string {
	return "." + string(f)
}

// NewDeCompressor creates and initializes a new decompressor with a worker pool
func NewDeCompressor(ctx context.Context, maxConcurrency int) *DeCompressor {
	pool := pond.NewResultPool[Result](maxConcurrency, pond.WithContext(ctx), pond.WithoutPanicRecovery())

	return &DeCompressor{
		pool: pool,
	}
}

// DeCompressor handles parallel compression/decompression operations
type DeCompressor struct {
	pool pond.ResultPool[Result]
}

// DeCompressResult contains the outcome of a single download job
type DeCompressResult string

func (r *DeCompressResult) Destination() string {
	return string(*r)
}

func (d *DeCompressor) decompressSingle(sourcePath string) (*DeCompressResult, error) {
	format := DetectCompressionFormat(sourcePath)
	if format == CompressionNone {
		return nil, fmt.Errorf("unknown compression format for file: %s", sourcePath)
	}

	// Derive destination path by removing compression extension
	destPath := strings.TrimSuffix(sourcePath, format.Extension())

	// Open compressed file
	compressedFile, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = compressedFile.Close() }()

	// Get decompressed reader based on format
	reader, err := getDecompressor(format, compressedFile)
	if err != nil {
		return nil, err
	}

	// Create uncompressed file
	uncompressedFile, err := os.Create(destPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := uncompressedFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	// Copy decompressed data
	_, err = io.Copy(uncompressedFile, reader)
	if err != nil {
		return nil, err
	}

	result := DeCompressResult(destPath)
	return &result, nil
}

func (d *DeCompressor) compressSingle(sourcePath string, format CompressionFormat) (*DeCompressResult, error) {
	if format == CompressionNone {
		return nil, fmt.Errorf("compression format required")
	}

	// Derive destination path from source + format
	destPath := sourcePath + format.Extension()

	// Open source file
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sourceFile.Close() }()

	// Create compressed file
	compressedFile, err := os.Create(destPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := compressedFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	// Get compressed writer based on format
	writer, err := getCompressor(format, compressedFile)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := writer.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	// Copy data with compression
	_, err = io.Copy(writer, sourceFile)
	if err != nil {
		return nil, err
	}

	result := DeCompressResult(destPath)
	return &result, nil
}

// Shutdown gracefully stops the decompressor
func (d *DeCompressor) Shutdown() {
	d.pool.StopAndWait()
}

// Decompress decompresses one or more files in parallel using a task group
// Destination paths are automatically derived by removing the compression extension.
// Returns a task group that can be waited on. Call Wait() to get results and any error.
// This method is thread-safe and can be called concurrently.
func (d *DeCompressor) Decompress(ctx context.Context, sourcePaths ...string) pond.ResultTaskGroup[Result] {
	group := d.pool.NewGroupContext(ctx)

	for _, sourcePath := range sourcePaths {
		group.SubmitErr(func() (Result, error) {
			return d.decompressSingle(sourcePath)
		})
	}

	return group
}

// Compress compresses a file into multiple formats in parallel using a task group
// Destination paths are automatically derived by appending the compression format extension.
// Returns a task group that can be waited on. Call Wait() to get results and any error.
// This method is thread-safe and can be called concurrently.
func (d *DeCompressor) Compress(ctx context.Context, sourcePath string, formats ...CompressionFormat) pond.ResultTaskGroup[Result] {
	group := d.pool.NewGroupContext(ctx)

	for _, format := range formats {
		group.SubmitErr(func() (Result, error) {
			return d.compressSingle(sourcePath, format)
		})
	}

	return group
}

// getDecompressor returns a Reader for the given compression format
func getDecompressor(format CompressionFormat, r io.Reader) (io.Reader, error) {
	switch format {
	case CompressionGzip:
		return gzip.NewReader(r)
	case CompressionBzip2:
		return bzip2.NewReader(r, nil)
	case CompressionXZ:
		return xz.NewReader(r)
	default:
		return nil, fmt.Errorf("unsupported decompression format: %s", format)
	}
}

// getCompressor returns a WriteCloser for the given compression format
func getCompressor(format CompressionFormat, w io.Writer) (io.WriteCloser, error) {
	switch format {
	case CompressionGzip:
		return gzip.NewWriter(w), nil
	case CompressionBzip2:
		return bzip2.NewWriter(w, nil)
	case CompressionXZ:
		return xz.NewWriter(w)
	default:
		return nil, fmt.Errorf("unsupported compression format: %s", format)
	}
}
