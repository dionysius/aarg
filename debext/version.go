package debext

import (
	"strings"
)

// VersionComponents represents the parsed components of a Debian package version
type VersionComponents struct {
	Epoch    string // Optional epoch (empty if not present)
	Upstream string // The upstream version
	Revision string // Optional Debian revision (empty if not present)
}

// ParseVersion parses a Debian package version string into its components.
// Debian version format: [epoch:]upstream-version[-debian-revision]
// The debian-revision is the portion after the last hyphen.
func ParseVersion(version string) VersionComponents {
	result := VersionComponents{}

	// Extract debian revision (everything after last "-")
	if idx := strings.LastIndex(version, "-"); idx != -1 {
		result.Revision = version[idx+1:]
		version = version[:idx]
	}

	// Extract epoch if present (everything before and including first ":")
	if idx := strings.Index(version, ":"); idx != -1 {
		result.Epoch = version[:idx]
		version = version[idx+1:]
	}

	// What remains is the upstream version
	result.Upstream = version

	return result
}
