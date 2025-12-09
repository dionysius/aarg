package feed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestFeedOptions_UnmarshalYAML_APT(t *testing.T) {
	tests := []struct {
		name        string
		yamlInput   string
		wantName    string
		wantScheme  string
		wantHost    string
		wantPath    string
		wantErr     bool
		errContains string
	}{
		{
			name: "apt with https",
			yamlInput: `
apt: "https://deb.debian.org/debian"
`,
			wantName:   "deb.debian.org/debian",
			wantScheme: "https",
			wantHost:   "deb.debian.org",
			wantPath:   "/debian",
			wantErr:    false,
		},
		{
			name: "apt with http",
			yamlInput: `
apt: "http://archive.ubuntu.com/ubuntu"
`,
			wantName:   "archive.ubuntu.com/ubuntu",
			wantScheme: "http",
			wantHost:   "archive.ubuntu.com",
			wantPath:   "/ubuntu",
			wantErr:    false,
		},
		{
			name: "apt without scheme",
			yamlInput: `
apt: "deb.debian.org/debian"
`,
			wantErr:     true,
			errContains: "must be http or https",
		},
		{
			name: "apt with ftp scheme",
			yamlInput: `
apt: "ftp://ftp.debian.org/debian"
`,
			wantErr:     true,
			errContains: "must be http or https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts FeedOptions
			err := yaml.Unmarshal([]byte(tt.yamlInput), &opts)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, FeedTypeAPT, opts.Type)
			assert.Equal(t, tt.wantName, opts.Name)
			assert.Equal(t, tt.wantName, opts.RelativePath)

			require.NotNil(t, opts.DownloadURL)
			assert.Equal(t, tt.wantScheme, opts.DownloadURL.Scheme)
			assert.Equal(t, tt.wantHost, opts.DownloadURL.Host)
			assert.Equal(t, tt.wantPath, opts.DownloadURL.Path)

			require.NotNil(t, opts.ProjectURL)
			assert.Equal(t, tt.wantScheme, opts.ProjectURL.Scheme)
			assert.Equal(t, tt.wantHost, opts.ProjectURL.Host)
			assert.Equal(t, tt.wantPath, opts.ProjectURL.Path)
		})
	}
}

func TestFeedOptions_MarshalYAML_APT(t *testing.T) {
	tests := []struct {
		name      string
		inputYAML string
		wantYAML  string
	}{
		{
			name:      "apt with https round-trips correctly",
			inputYAML: `apt: "https://deb.debian.org/debian"`,
			wantYAML:  "apt: https://deb.debian.org/debian",
		},
		{
			name:      "apt with http round-trips correctly",
			inputYAML: `apt: "http://example.com/debian"`,
			wantYAML:  "apt: http://example.com/debian",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Unmarshal the input
			var opts FeedOptions
			err := yaml.Unmarshal([]byte(tt.inputYAML), &opts)
			require.NoError(t, err)

			// Marshal back to YAML
			data, err := yaml.Marshal(&opts)
			require.NoError(t, err)

			yamlStr := string(data)
			assert.Contains(t, yamlStr, tt.wantYAML)
		})
	}
}

func TestFeedOptions_UnmarshalYAML_OBS(t *testing.T) {
	tests := []struct {
		name             string
		yamlInput        string
		wantName         string
		wantRelativePath string
		wantScheme       string
		wantHost         string
		wantPath         string
		wantErr          bool
		errContains      string
	}{
		{
			name: "obs project identifier",
			yamlInput: `
obs: "home:dionysius:vaultwarden"
`,
			wantName:         "home:dionysius:vaultwarden",
			wantRelativePath: "download.opensuse.org/repositories/home:/dionysius:/vaultwarden",
			wantScheme:       "https",
			wantHost:         "download.opensuse.org",
			wantPath:         "/repositories/home:/dionysius:/vaultwarden",
			wantErr:          false,
		},
		{
			name: "custom obs with https",
			yamlInput: `
obs: "https://obs.example.com/repositories/myproject"
`,
			wantName:         "obs.example.com/repositories/myproject",
			wantRelativePath: "obs.example.com/repositories/myproject",
			wantScheme:       "https",
			wantHost:         "obs.example.com",
			wantPath:         "/repositories/myproject",
			wantErr:          false,
		},
		{
			name: "custom obs with http",
			yamlInput: `
obs: "http://obs.internal.corp/repos/test"
`,
			wantName:         "obs.internal.corp/repos/test",
			wantRelativePath: "obs.internal.corp/repos/test",
			wantScheme:       "http",
			wantHost:         "obs.internal.corp",
			wantPath:         "/repos/test",
			wantErr:          false,
		},
		{
			name: "custom obs without scheme",
			yamlInput: `
obs: "obs.example.com/repositories/myproject"
`,
			wantErr:     true,
			errContains: "must be http or https",
		},
		{
			name: "custom obs with ftp scheme",
			yamlInput: `
obs: "ftp://obs.example.com/repositories/myproject"
`,
			wantErr:     true,
			errContains: "must be http or https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts FeedOptions
			err := yaml.Unmarshal([]byte(tt.yamlInput), &opts)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, FeedTypeOBS, opts.Type)
			assert.Equal(t, tt.wantName, opts.Name)
			assert.Equal(t, tt.wantRelativePath, opts.RelativePath)

			require.NotNil(t, opts.DownloadURL)
			assert.Equal(t, tt.wantScheme, opts.DownloadURL.Scheme)
			assert.Equal(t, tt.wantHost, opts.DownloadURL.Host)
			assert.Equal(t, tt.wantPath, opts.DownloadURL.Path)

			require.NotNil(t, opts.ProjectURL)
			assert.Equal(t, tt.wantScheme, opts.ProjectURL.Scheme)
		})
	}
}

func TestFeedOptions_MarshalYAML_OBS(t *testing.T) {
	tests := []struct {
		name      string
		inputYAML string
		wantYAML  string
	}{
		{
			name:      "obs project identifier round-trips correctly",
			inputYAML: `obs: "home:dionysius:vaultwarden"`,
			wantYAML:  "obs: home:dionysius:vaultwarden",
		},
		{
			name:      "custom obs with https round-trips correctly",
			inputYAML: `obs: "https://obs.example.com/repositories/myproject"`,
			wantYAML:  "obs: https://obs.example.com/repositories/myproject",
		},
		{
			name:      "custom obs with http round-trips correctly",
			inputYAML: `obs: "http://obs.internal.corp/repos/test"`,
			wantYAML:  "obs: http://obs.internal.corp/repos/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Unmarshal the input
			var opts FeedOptions
			err := yaml.Unmarshal([]byte(tt.inputYAML), &opts)
			require.NoError(t, err)

			// Marshal back to YAML
			data, err := yaml.Marshal(&opts)
			require.NoError(t, err)

			yamlStr := string(data)
			assert.Contains(t, yamlStr, tt.wantYAML)
		})
	}
}
