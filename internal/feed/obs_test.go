package feed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandOBSFeedOptions(t *testing.T) {
	baseURL := mustParseURL("https://download.opensuse.org/repositories/home:/user:/project")
	projectURL := mustParseURL("https://build.opensuse.org/project/show/home:user:project")

	tests := []struct {
		name  string
		dists []DistributionMap
		want  []struct {
			url   string
			rpath string
		}
	}{
		{
			name:  "single distribution - appends to URL and path",
			dists: []DistributionMap{{Feed: "Debian_12", Target: "bookworm"}},
			want: []struct {
				url   string
				rpath string
			}{
				{
					url:   "https://download.opensuse.org/repositories/home:/user:/project/Debian_12",
					rpath: "obs/home-user-project/Debian_12",
				},
			},
		},
		{
			name: "multiple distributions - one APT feed per OBS dist",
			dists: []DistributionMap{
				{Feed: "Debian_12", Target: "bookworm"},
				{Feed: "Debian_13", Target: "trixie"},
				{Feed: "xUbuntu_24.04", Target: "noble"},
			},
			want: []struct {
				url   string
				rpath string
			}{
				{
					url:   "https://download.opensuse.org/repositories/home:/user:/project/Debian_12",
					rpath: "obs/home-user-project/Debian_12",
				},
				{
					url:   "https://download.opensuse.org/repositories/home:/user:/project/Debian_13",
					rpath: "obs/home-user-project/Debian_13",
				},
				{
					url:   "https://download.opensuse.org/repositories/home:/user:/project/xUbuntu_24.04",
					rpath: "obs/home-user-project/xUbuntu_24.04",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &FeedOptions{
				Type:          FeedTypeOBS,
				DownloadURL:   baseURL,
				ProjectURL:    projectURL,
				RelativePath:  "obs/home-user-project",
				Distributions: tt.dists,
			}

			result := ExpandOBSFeedOptions(input)
			require.Len(t, result, len(tt.want))

			for i, expected := range tt.want {
				actual := result[i]
				assert.Equal(t, FeedTypeAPT, actual.Type, "Type should be APT at index %d", i)
				assert.Equal(t, expected.url, actual.DownloadURL.String(), "URL mismatch at index %d", i)
				assert.Equal(t, expected.rpath, actual.RelativePath, "RelativePath mismatch at index %d", i)
				// OBS always converts to flat APT repos
				assert.Equal(t, "/", actual.Distributions[0].Feed, "Feed should be / (flat) at index %d", i)
				assert.Equal(t, tt.dists[i].Target, actual.Distributions[0].Target, "Target mismatch at index %d", i)
			}
		})
	}
}
