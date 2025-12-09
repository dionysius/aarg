package common

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var hardlinkMutex sync.Mutex

// EnsureHardlink creates a hardlink from src to dst with force behavior
// If dst exists and points to a different file, it will be removed first
// If dst exists and points to the same file (same inode), nothing is done
// This function is safe for concurrent use when multiple goroutines might
// try to create the same hardlink simultaneously.
func EnsureHardlink(src, dst string) error {
	hardlinkMutex.Lock()
	defer hardlinkMutex.Unlock()

	// Check if destination already exists
	dstInfo, err := os.Lstat(dst)
	if err == nil {
		// Destination exists - check if it's already the same file (same inode)
		srcInfo, err := os.Lstat(src)
		if err != nil {
			return err
		}
		// If same inode, already correctly linked
		if os.SameFile(srcInfo, dstInfo) {
			return nil
		}
		// Different file, remove it (force behavior)
		if err := os.Remove(dst); err != nil {
			return err
		}
	}

	// Create hardlink
	return os.Link(src, dst)
}

// MatchesGlobPatterns checks if a value matches the given glob patterns.
// Empty patterns list means match all.
// Patterns support wildcards (* and ?).
// Patterns prefixed with ! are negations and exclude matching values.
// Negations are evaluated after positive matches.
func MatchesGlobPatterns(patterns []string, value string) bool {
	// No filter = include all
	if len(patterns) == 0 {
		return true
	}

	// Separate positive and negative patterns
	var positivePatterns, negativePatterns []string
	for _, pattern := range patterns {
		if after, ok := strings.CutPrefix(pattern, "!"); ok {
			negativePatterns = append(negativePatterns, after)
		} else {
			positivePatterns = append(positivePatterns, pattern)
		}
	}

	// Check positive matches (default to match all if no positive patterns)
	matched := len(positivePatterns) == 0
	for _, pattern := range positivePatterns {
		if m, _ := filepath.Match(pattern, value); m {
			matched = true
			break
		}
	}

	// If matched, check if any negation pattern excludes it
	if matched {
		for _, pattern := range negativePatterns {
			if m, _ := filepath.Match(pattern, value); m {
				matched = false
				break
			}
		}
	}

	return matched
}
