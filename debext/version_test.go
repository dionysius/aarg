package debext

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		version  string
		epoch    string
		upstream string
		revision string
	}{
		{"1.2.3", "", "1.2.3", ""},
		{"1.2.3-4", "", "1.2.3", "4"},
		{"2:1.2.3-4", "2", "1.2.3", "4"},
		{"2:1.2.3", "2", "1.2.3", ""},
		{"1.0-rc1-2", "", "1.0-rc1", "2"},
		{"1.35.1-1~noble", "", "1.35.1", "1~noble"},
		{"3:1.0~beta1~svn1245-1", "3", "1.0~beta1~svn1245", "1"},
		{"1.0-0ubuntu1", "", "1.0", "0ubuntu1"},
		{"1.2.3-4~bpo11+1", "", "1.2.3", "4~bpo11+1"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			result := ParseVersion(tt.version)
			assert.Equal(t, tt.epoch, result.Epoch)
			assert.Equal(t, tt.upstream, result.Upstream)
			assert.Equal(t, tt.revision, result.Revision)

			// Test that String() reconstructs the original version
			assert.Equal(t, tt.version, result.String())
		})
	}
}
