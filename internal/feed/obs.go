package feed

import (
	"context"

	"github.com/alitto/pond/v2"
	"github.com/dionysius/aarg/debext"
	"github.com/dionysius/aarg/internal/common"
)

// OBS handles OpenSUSE Build Service repository downloads
// OBS repositories are structured as flat APT repositories per distribution
type OBS struct {
	options  *FeedOptions
	verifier *debext.Verifier
	storage  *common.Storage
	pool     pond.Pool
}

// NewOBS creates a new OBS feed
func NewOBS(storage *common.Storage, verifier *debext.Verifier, options *FeedOptions, pool pond.Pool) (*OBS, error) {
	return &OBS{
		options:  options,
		verifier: verifier,
		storage:  storage,
		pool:     pool,
	}, nil
}

// Run executes the complete download and verification process
// For each distribution, create a flat APT feed instance
func (s *OBS) Run(ctx context.Context) error {
	// Create subpool for OBS distribution processing
	obsPool := s.pool.NewSubpool(10)
	defer obsPool.StopAndWait()

	group := obsPool.NewGroup()

	// Expand OBS feed into APT feeds for each distribution
	aptFeeds := ExpandOBSFeed(s.options)

	// Submit tasks for each APT feed
	for i, aptOptions := range aptFeeds {
		// Capture for closure
		opts := aptOptions
		distMap := s.options.Distributions[i]

		group.SubmitErr(func() error {
			// Create and run an APT feed for this distribution
			aptFeed, err := NewApt(s.storage.Scope(distMap.Feed), s.verifier, opts, obsPool)
			if err != nil {
				return err
			}

			return aptFeed.Run(ctx)
		})
	}

	// Wait for all tasks and return first error if any
	return group.Wait()
}

// ExpandOBSFeed expands a single OBS feed into multiple APT feed configurations,
// one for each distribution.
func ExpandOBSFeed(obsFeed *FeedOptions) []*FeedOptions {
	var aptFeeds []*FeedOptions

	for _, distMap := range obsFeed.Distributions {
		// Build download URL from parent's DownloadURL by appending distribution
		downloadURL := obsFeed.DownloadURL.JoinPath(distMap.Feed)

		// Build relative path from parent's RelativePath
		relativePath := obsFeed.RelativePath + "/" + distMap.Feed

		// Name is URL without scheme (host + path)
		name := downloadURL.Host + downloadURL.Path

		aptOptions := &FeedOptions{
			Name:              name,
			Type:              FeedTypeAPT,
			DownloadURL:       downloadURL,
			ProjectURL:        obsFeed.ProjectURL, // Keep original OBS project URL
			RelativePath:      relativePath,
			Distributions:     []DistributionMap{{Feed: "/", Target: distMap.Target}},
			Architectures:     obsFeed.Architectures,
			RetentionPolicies: obsFeed.RetentionPolicies,
			Packages:          obsFeed.Packages,
			Sources:           obsFeed.Sources,
		}

		aptFeeds = append(aptFeeds, aptOptions)
	}

	return aptFeeds
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
