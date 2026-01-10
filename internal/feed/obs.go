package feed

// ExpandOBSFeedOptions expands an OBS FeedOptions into flat APT FeedOptions,
// one per distribution. OBS distributions are converted to APT prefix notation
// where each OBS dist becomes a prefix with a flat repo (/).
func ExpandOBSFeedOptions(options *FeedOptions) []*FeedOptions {
	// Convert OBS distributions to APT prefix notation: "Debian_12" -> "Debian_12/"
	aptOptions := &FeedOptions{
		Name:          options.Name,
		Type:          FeedTypeAPT,
		DownloadURL:   options.DownloadURL,
		ProjectURL:    options.ProjectURL,
		RelativePath:  options.RelativePath,
		FromSources:   options.FromSources,
		Packages:      options.Packages,
		Distributions: make([]DistributionMap, len(options.Distributions)),
	}

	for i, distMap := range options.Distributions {
		// Convert "Debian_12" -> "Debian_12/" (prefix with flat repo)
		aptOptions.Distributions[i] = DistributionMap{
			Feed:   distMap.Feed + "/",
			Target: distMap.Target,
		}
	}

	// Delegate to APT expansion which handles prefix logic
	return ExpandAptFeedOptions(aptOptions)
}

// TODO: Support Ubuntu Debug Packages (.ddeb files)
//
// Issue: https://github.com/openSUSE/open-build-service/issues/19057
//
// Problem:
// OBS builds Ubuntu debug packages with .ddeb extension but does NOT include them
// in the Packages index. This is inconsistent with Debian repositories where debug
// packages (.deb with -dbgsym suffix) ARE included in the Packages index.
//
// Example:
// - Debian (works): https://download.opensuse.org/repositories/home:/dionysius:/immich/Debian_13/Packages
//   Contains: libvips-tools-dbgsym_8.17.3-1_amd64.deb with full checksums
//
// - Ubuntu (broken): https://download.opensuse.org/repositories/home:/dionysius:/immich/xUbuntu_24.04/Packages
//   Missing: libvips-tools-dbgsym_8.17.3-1_amd64.ddeb (but file exists in amd64/ directory)
//
// Recommended Solution: Use OBS Public API
//
// The OBS API reliably lists ALL built files including .ddeb packages:
//
//   API URL Format:
//   https://api.opensuse.org/public/build/{project}/{repository}/{arch}/{package}
//
//   Example:
//   curl -fsSL "https://api.opensuse.org/public/build/home:dionysius:immich/xUbuntu_24.04/x86_64/vips"
//
//   Returns XML with:
//   <binarylist>
//     <binary filename="libvips-tools-dbgsym_8.17.3-1_amd64.ddeb" size="51610" mtime="1764605048"/>
//     <binary filename="libvips42t64-dbgsym_8.17.3-1_amd64.ddeb" size="3211272" mtime="1764605048"/>
//     ...
//   </binarylist>
//
// Advantages:
// - Lists ALL built files including .ddeb
// - Provides size and mtime metadata for verification
// - Structured XML format (programmatically reliable)
// - No authentication required for public projects
//
// Disadvantages:
// - No checksums (MD5/SHA256) in API response
// - Need to construct download URLs manually
// - Requires XML parsing
//
// Verification Strategy:
// 1. Query API to get expected file size
// 2. Download file from: https://download.opensuse.org/repositories/{project}/{repo}/{arch}/{filename}
// 3. Verify downloaded file size matches API metadata
// 4. Optionally compute local checksum for integrity
//
// Implementation Notes:
// - Parse XML response from OBS API
// - Filter for .ddeb files
// - Match with corresponding .deb packages
// - Store metadata for verification
// - Consider caching API responses to reduce load
