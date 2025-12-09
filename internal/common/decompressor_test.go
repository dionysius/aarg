package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectCompressionFormat(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     CompressionFormat
	}{
		{
			name:     "gzip extension",
			filename: "file.tar.gz",
			want:     CompressionGzip,
		},
		{
			name:     "bzip2 extension",
			filename: "file.tar.bz2",
			want:     CompressionBzip2,
		},
		{
			name:     "xz extension",
			filename: "file.tar.xz",
			want:     CompressionXZ,
		},
		{
			name:     "no compression",
			filename: "file.tar",
			want:     CompressionNone,
		},
		{
			name:     "unknown extension",
			filename: "file.zip",
			want:     CompressionNone,
		},
		{
			name:     "gz only",
			filename: "Packages.gz",
			want:     CompressionGzip,
		},
		{
			name:     "multiple dots",
			filename: "my.package.file.bz2",
			want:     CompressionBzip2,
		},
		{
			name:     "no extension",
			filename: "README",
			want:     CompressionNone,
		},
		{
			name:     "empty filename",
			filename: "",
			want:     CompressionNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectCompressionFormat(tt.filename)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCompressionFormat_Extension(t *testing.T) {
	tests := []struct {
		name   string
		format CompressionFormat
		want   string
	}{
		{
			name:   "gzip",
			format: CompressionGzip,
			want:   ".gz",
		},
		{
			name:   "bzip2",
			format: CompressionBzip2,
			want:   ".bz2",
		},
		{
			name:   "xz",
			format: CompressionXZ,
			want:   ".xz",
		},
		{
			name:   "none",
			format: CompressionNone,
			want:   ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.format.Extension()
			assert.Equal(t, tt.want, got)
		})
	}
}
