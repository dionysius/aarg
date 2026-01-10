package feed

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandAptFeedOptions(t *testing.T) {
	baseURL := mustParseURL("https://deb.debian.org/debian")
	projectURL := mustParseURL("https://www.debian.org/")

	tests := []struct {
		name  string
		dists []DistributionMap
		want  []struct {
			url   string
			rpath string
			dists []DistributionMap
		}
	}{
		{
			name:  "no prefix - passthrough",
			dists: []DistributionMap{{Feed: "bookworm", Target: "stable"}},
			want: []struct {
				url   string
				rpath string
				dists []DistributionMap
			}{
				{
					url:   "https://deb.debian.org/debian",
					rpath: "debian",
					dists: []DistributionMap{{Feed: "bookworm", Target: "stable"}},
				},
			},
		},
		{
			name:  "prefix - expands URL and RelativePath",
			dists: []DistributionMap{{Feed: "debian/trixie", Target: "trixie"}},
			want: []struct {
				url   string
				rpath string
				dists []DistributionMap
			}{
				{
					url:   "https://deb.debian.org/debian/debian",
					rpath: "debian/debian",
					dists: []DistributionMap{{Feed: "trixie", Target: "trixie"}},
				},
			},
		},
		{
			name:  "prefix with empty target - auto-maps to distname",
			dists: []DistributionMap{{Feed: "debian/trixie", Target: ""}},
			want: []struct {
				url   string
				rpath string
				dists []DistributionMap
			}{
				{
					url:   "https://deb.debian.org/debian/debian",
					rpath: "debian/debian",
					dists: []DistributionMap{{Feed: "trixie", Target: "trixie"}},
				},
			},
		},
		{
			name: "multiple prefixes - groups by prefix",
			dists: []DistributionMap{
				{Feed: "debian/trixie", Target: "trixie"},
				{Feed: "ubuntu/noble", Target: "noble"},
				{Feed: "bookworm", Target: "stable"},
			},
			want: []struct {
				url   string
				rpath string
				dists []DistributionMap
			}{
				{
					url:   "https://deb.debian.org/debian/debian",
					rpath: "debian/debian",
					dists: []DistributionMap{{Feed: "trixie", Target: "trixie"}},
				},
				{
					url:   "https://deb.debian.org/debian/ubuntu",
					rpath: "debian/ubuntu",
					dists: []DistributionMap{{Feed: "noble", Target: "noble"}},
				},
				{
					url:   "https://deb.debian.org/debian",
					rpath: "debian",
					dists: []DistributionMap{{Feed: "bookworm", Target: "stable"}},
				},
			},
		},
		{
			name:  "flat repo - unchanged",
			dists: []DistributionMap{{Feed: "/", Target: "stable"}},
			want: []struct {
				url   string
				rpath string
				dists []DistributionMap
			}{
				{
					url:   "https://deb.debian.org/debian",
					rpath: "debian",
					dists: []DistributionMap{{Feed: "/", Target: "stable"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &FeedOptions{
				Type:          FeedTypeAPT,
				DownloadURL:   baseURL,
				ProjectURL:    projectURL,
				RelativePath:  "debian",
				Distributions: tt.dists,
			}

			result := ExpandAptFeedOptions(input)
			require.Len(t, result, len(tt.want))

			for i, expected := range tt.want {
				actual := result[i]
				assert.Equal(t, expected.url, actual.DownloadURL.String(), "URL mismatch at index %d", i)
				assert.Equal(t, expected.rpath, actual.RelativePath, "RelativePath mismatch at index %d", i)
				assert.Equal(t, expected.dists, actual.Distributions, "Distributions mismatch at index %d", i)
			}
		})
	}
}

func mustParseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return u
}
