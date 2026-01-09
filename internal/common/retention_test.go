package common

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterBySource(t *testing.T) {
	tests := []struct {
		name       string
		policies   []RetentionPolicy
		sourceName string
		wantCount  int
		wantRules  []RetentionRule
	}{
		{
			name: "empty sources matches all",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{},
				},
			},
			sourceName: "any-source",
			wantCount:  1,
		},
		{
			name: "exact match",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{"vaultwarden"},
				},
			},
			sourceName: "vaultwarden",
			wantCount:  1,
		},
		{
			name: "no match",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{"vaultwarden"},
				},
			},
			sourceName: "other-package",
			wantCount:  0,
		},
		{
			name: "wildcard match",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{"vault*"},
				},
			},
			sourceName: "vaultwarden",
			wantCount:  1,
		},
		{
			name: "negation excludes",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{"vault*", "!*-web-*"},
				},
			},
			sourceName: "vaultwarden-web-vault",
			wantCount:  0,
		},
		{
			name: "negation allows non-matching",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{"vault*", "!*-web-*"},
				},
			},
			sourceName: "vaultwarden",
			wantCount:  1,
		},
		{
			name: "multiple policies",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{"*", "!vaultwarden-web-vault"},
				},
				{
					RetentionRule: RetentionRule{Pattern: "*.#.#", Amount: []int{5, 2}},
					FromSources:     []string{"vaultwarden-web-vault"},
				},
			},
			sourceName: "vaultwarden-web-vault",
			wantCount:  1,
			wantRules: []RetentionRule{
				{Pattern: "*.#.#", Amount: []int{5, 2}},
			},
		},
		{
			name: "only negation excludes matched",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{"!excluded-*"},
				},
			},
			sourceName: "excluded-package",
			wantCount:  0,
		},
		{
			name: "only negation allows non-excluded",
			policies: []RetentionPolicy{
				{
					RetentionRule: RetentionRule{Pattern: "*.#.*", Amount: []int{3}},
					FromSources:     []string{"!excluded-*"},
				},
			},
			sourceName: "normal-package",
			wantCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterBySource(tt.policies, tt.sourceName)
			assert.Len(t, result, tt.wantCount)

			if tt.wantRules != nil {
				assert.Equal(t, tt.wantRules, result)
			}
		})
	}
}

func TestRetentionFilter_Filter(t *testing.T) {
	tests := []struct {
		name     string
		versions []string
		rules    []RetentionRule
		want     []string
	}{
		{
			name: "keep last 3 minors",
			versions: []string{
				"1.34.3-2",
				"1.34.2-2",
				"1.33.2-0",
				"1.32.7-0",
				"1.31.0-0",
			},
			rules: []RetentionRule{
				{Pattern: "*.#.*-*", Amount: []int{3}},
			},
			want: []string{
				"1.34.3-2", // highest of 34.x
				"1.33.2-0", // highest of 33.x
				"1.32.7-0", // highest of 32.x
			},
		},
		{
			name: "keep last 2 patches per minor",
			versions: []string{
				"1.34.3-2",
				"1.34.2-2",
				"1.34.1-1",
				"1.33.2-0",
			},
			rules: []RetentionRule{
				{Pattern: "*.*.#-*", Amount: []int{2}},
			},
			want: []string{
				"1.34.3-2", // group 1.34, patch 3
				"1.34.2-2", // group 1.34, patch 2
				"1.33.2-0", // group 1.33, patch 2 (only one in this group)
			},
		},
		{
			name: "hierarchical: 3 minors, 2 patches each",
			versions: []string{
				"1.34.3-2",
				"1.34.2-2",
				"1.34.1-1",
				"1.33.2-0",
				"1.33.1-0",
				"1.32.7-0",
				"1.32.6-0",
				"1.31.0-0",
			},
			rules: []RetentionRule{
				{Pattern: "*.#.#-*", Amount: []int{3, 2}},
			},
			want: []string{
				"1.34.3-2", // 34th minor, 3rd patch
				"1.34.2-2", // 34th minor, 2nd patch
				"1.33.2-0", // 33rd minor, 2nd patch
				"1.33.1-0", // 33rd minor, 1st patch
				"1.32.7-0", // 32nd minor, 7th patch
				"1.32.6-0", // 32nd minor, 6th patch
			},
		},
		{
			name: "combined rules (union)",
			versions: []string{
				"1.34.3-2",
				"1.34.2-2",
				"1.33.2-0",
				"1.32.7-0",
			},
			rules: []RetentionRule{
				{Pattern: "*.#.*-*", Amount: []int{3}}, // 3 minors
				{Pattern: "*.*.#-*", Amount: []int{2}}, // 2 patches
			},
			want: []string{
				"1.34.3-2", // matches both rules
				"1.34.2-2", // matches patch rule
				"1.33.2-0", // matches minor rule
				"1.32.7-0", // matches minor rule
			},
		},
		{
			name: "track last segment (alphanumeric)",
			versions: []string{
				"2.4.3-ubuntu3",
				"2.4.3-ubuntu2",
				"2.4.3-ubuntu1",
			},
			rules: []RetentionRule{
				{Pattern: "*.*.*-#", Amount: []int{2}},
			},
			want: []string{
				"2.4.3-ubuntu3",
				"2.4.3-ubuntu2",
			},
		},
		{
			name: "date-based versions",
			versions: []string{
				"20231215-1",
				"20231214-2",
				"20231213-1",
			},
			rules: []RetentionRule{
				{Pattern: "#-*", Amount: []int{2}},
			},
			want: []string{
				"20231215-1",
				"20231214-2",
			},
		},
		{
			name: "native packages (no revision)",
			versions: []string{
				"1.34.3",
				"1.33.2",
				"1.32.7",
			},
			rules: []RetentionRule{
				{Pattern: "*.#.*", Amount: []int{2}},
			},
			want: []string{
				"1.34.3",
				"1.33.2",
			},
		},
		{
			name: "two major versions",
			versions: []string{
				"3.5.1-0",
				"2.9.8-1",
				"2.9.7-0",
				"1.8.3-2",
			},
			rules: []RetentionRule{
				{Pattern: "#.*.*-*", Amount: []int{2}},
			},
			want: []string{
				"3.5.1-0",
				"2.9.8-1",
			},
		},
		{
			name: "flexible delimiters (leading/trailing/consecutive)",
			versions: []string{
				".1..2..3--4.",
				".1..2..4--5.",
				".1..3..0--1.",
			},
			rules: []RetentionRule{
				{Pattern: "*.#.*-*", Amount: []int{2}},
			},
			want: []string{
				".1..3..0--1.",
				".1..2..4--5.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create items with versions
			type item struct {
				version string
			}
			items := make([]item, len(tt.versions))
			for i, v := range tt.versions {
				items[i] = item{version: v}
			}

			// Create filter and apply
			filter, err := NewRetentionFilter(tt.rules, func(i item) string { return i.version }, NoMatchKeep)
			require.NoError(t, err)

			filtered, err := filter.Filter(items)
			require.NoError(t, err)

			// Extract versions
			got := make([]string, len(filtered))
			for i, item := range filtered {
				got[i] = item.version
			}

			// Sort for comparison
			sort.Strings(got)
			want := append([]string{}, tt.want...)
			sort.Strings(want)

			// Compare
			assert.Equal(t, want, got)
		})
	}
}

func TestRetentionFilter_NoMatchBehaviors(t *testing.T) {
	type item struct {
		version string
	}

	items := []item{
		{version: "1.2.3-4"},   // matches pattern
		{version: "1.2.3.4-5"}, // doesn't match pattern
	}

	t.Run("NoMatchKeep", func(t *testing.T) {
		filter, err := NewRetentionFilter([]RetentionRule{
			{Pattern: "*.*.#-*", Amount: []int{2}},
		}, func(i item) string { return i.version }, NoMatchKeep)
		require.NoError(t, err)

		result, err := filter.Filter(items)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("NoMatchKeep_Add", func(t *testing.T) {
		filter, err := NewRetentionFilter([]RetentionRule{
			{Pattern: "*.*.#-*", Amount: []int{2}},
		}, func(i item) string { return i.version }, NoMatchKeep)
		require.NoError(t, err)

		require.NoError(t, filter.Add(item{version: "1.2.3-4"}))
		require.NoError(t, filter.Add(item{version: "not-matching"}))
	})

	t.Run("NoMatchIgnore", func(t *testing.T) {
		filter, err := NewRetentionFilter([]RetentionRule{
			{Pattern: "*.*.#-*", Amount: []int{2}},
		}, func(i item) string { return i.version }, NoMatchIgnore)
		require.NoError(t, err)

		result, err := filter.Filter(items)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "1.2.3-4", result[0].version)
	})

	t.Run("NoMatchIgnore_Add", func(t *testing.T) {
		filter, err := NewRetentionFilter([]RetentionRule{
			{Pattern: "*.#.#-*", Amount: []int{2, 2}},
		}, func(i item) string { return i.version }, NoMatchIgnore)
		require.NoError(t, err)

		require.NoError(t, filter.Add(item{version: "1.34.3-2"}))
		require.NoError(t, filter.Add(item{version: "1.34.2-1"}))
		require.NoError(t, filter.Add(item{version: "invalid"}))
		require.NoError(t, filter.Add(item{version: "1.2"}))

		kept, err := filter.Kept()
		require.NoError(t, err)
		assert.Len(t, kept, 2)
	})

	t.Run("NoMatchError", func(t *testing.T) {
		filter, err := NewRetentionFilter([]RetentionRule{
			{Pattern: "*.*.#-*", Amount: []int{2}},
		}, func(i item) string { return i.version }, NoMatchError)
		require.NoError(t, err)

		result, err := filter.Filter(items)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrNoMatchingPattern)
		assert.Nil(t, result)
	})

	t.Run("NoMatchError_Add", func(t *testing.T) {
		filter, err := NewRetentionFilter([]RetentionRule{
			{Pattern: "*.*.#-*", Amount: []int{2}},
		}, func(i item) string { return i.version }, NoMatchError)
		require.NoError(t, err)

		require.NoError(t, filter.Add(item{version: "1.2.3-4"}))

		err = filter.Add(item{version: "1.2.3.4-5"})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrNoMatchingPattern)
	})

	t.Run("EmptyRules", func(t *testing.T) {
		tests := []struct {
			behavior NoMatchBehavior
			wantLen  int
			wantErr  bool
		}{
			{NoMatchKeep, 1, false},
			{NoMatchIgnore, 0, false},
			{NoMatchError, 0, true},
		}

		for _, tt := range tests {
			t.Run(fmt.Sprintf("%v", tt.behavior), func(t *testing.T) {
				filter, err := NewRetentionFilter([]RetentionRule{}, func(i item) string { return i.version }, tt.behavior)
				require.NoError(t, err)

				err = filter.Add(item{version: "1.2.3-4"})
				if tt.wantErr {
					assert.Error(t, err)
					assert.ErrorIs(t, err, ErrNoMatchingPattern)
				} else {
					require.NoError(t, err)
					result, err := filter.Kept()
					require.NoError(t, err)
					assert.Len(t, result, tt.wantLen)
				}
			})
		}
	})
}

func TestRetentionFilter_MostSegmentsWins(t *testing.T) {
	type item struct {
		version string
	}

	// Pattern with 4 segments should win over pattern with 3 segments
	filter, err := NewRetentionFilter([]RetentionRule{
		{Pattern: "*.#.#-*", Amount: []int{2, 2}}, // 4 segments: keep 2 major, 2 minor each
		{Pattern: "*.#.*", Amount: []int{5}},      // 3 segments: keep 5 major
	}, func(i item) string { return i.version }, NoMatchKeep)
	require.NoError(t, err)

	items := []item{
		{version: "1.1.0-1"},
		{version: "1.2.0-1"},
		{version: "1.3.0-1"},
		{version: "2.1.0-1"},
		{version: "2.2.0-1"},
		{version: "2.3.0-1"},
		{version: "3.1.0-1"},
		{version: "3.2.0-1"},
	}

	result, err := filter.Filter(items)
	require.NoError(t, err)

	// 4-segment pattern (*.#.#-*) tracks minor+patch
	// Amount [2,2] keeps 2 minors with 2 patches each per major
	// Expected: 6 items (3 majors × 2 minors)
	assert.Equal(t, 6, len(result))
	versions := make([]string, len(result))
	for i, r := range result {
		versions[i] = r.version
	}
	assert.Contains(t, versions, "1.2.0-1")
	assert.Contains(t, versions, "1.3.0-1")
	assert.Contains(t, versions, "2.2.0-1")
	assert.Contains(t, versions, "2.3.0-1")
	assert.Contains(t, versions, "3.1.0-1")
	assert.Contains(t, versions, "3.2.0-1")
}

func TestRetentionFilter_SameSegmentCountUnion(t *testing.T) {
	type item struct {
		version string
	}

	// Two patterns with same segment count - union behavior
	filter, err := NewRetentionFilter([]RetentionRule{
		{Pattern: "*.#.#-*", Amount: []int{1, 1}}, // Keep 1 major, 1 minor (tracks major+minor)
		{Pattern: "#.*.#-*", Amount: []int{1, 1}}, // Keep 1 major, 1 patch (tracks major+patch)
	}, func(i item) string { return i.version }, NoMatchKeep)
	require.NoError(t, err)

	items := []item{
		{version: "1.1.1-1"},
		{version: "1.1.2-1"},
		{version: "1.2.1-1"},
		{version: "1.2.2-1"},
		{version: "2.1.1-1"},
		{version: "2.1.2-1"},
		{version: "2.2.1-1"},
		{version: "2.2.2-1"},
	}

	result, err := filter.Filter(items)
	require.NoError(t, err)

	// First rule (*.#.#-*) keeps minor 2, patch 2: 1.2.2-1, 2.2.2-1
	// Second rule (#.*.#-*) keeps major 2, patch 2: 2.1.2-1, 2.2.2-1
	// Union: 1.2.2-1, 2.1.2-1, 2.2.2-1
	assert.Equal(t, 3, len(result))
	versions := make([]string, len(result))
	for i, r := range result {
		versions[i] = r.version
	}
	assert.Contains(t, versions, "1.2.2-1")
	assert.Contains(t, versions, "2.2.2-1")
	assert.Contains(t, versions, "2.1.2-1")
}

func TestRetentionFilter_MixedVersionFormats(t *testing.T) {
	type item struct {
		version string
	}

	// Support both Debian-style and semver in same repository
	filter, err := NewRetentionFilter([]RetentionRule{
		{Pattern: "*.*.#", Amount: []int{3}},   // Semver (3 seg): keep 3 patches
		{Pattern: "*.*.*-#", Amount: []int{2}}, // Debian (4 seg): keep 2 revisions
	}, func(i item) string { return i.version }, NoMatchKeep)
	require.NoError(t, err)

	items := []item{
		// Debian-style versions (4 segments)
		{version: "1.2.3-1"},
		{version: "1.2.3-2"},
		{version: "1.2.3-3"},
		// Semver versions (3 segments)
		{version: "2.0.2"},
		{version: "2.0.3"},
		{version: "2.0.4"},
	}

	result, err := filter.Filter(items)
	require.NoError(t, err)

	// Debian (4 seg, *.*.*-#): keep 2 revisions → 1.2.3-2, 1.2.3-3
	// Semver (3 seg, *.*.#): keep 3 patches → 2.0.2, 2.0.3, 2.0.4
	assert.Equal(t, 5, len(result))
	versions := make([]string, len(result))
	for i, r := range result {
		versions[i] = r.version
	}
	assert.Contains(t, versions, "1.2.3-2")
	assert.Contains(t, versions, "1.2.3-3")
	assert.Contains(t, versions, "2.0.2")
	assert.Contains(t, versions, "2.0.3")
	assert.Contains(t, versions, "2.0.4")
}

func TestParsePattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr error
	}{
		{"simple wildcard", "*.*.*-*", nil},
		{"one tracked", "*.#.*-*", nil},
		{"two tracked", "*.#.#-*", nil},
		{"all tracked", "#.#.#-#", nil},
		{"native package", "*.#.*", nil},
		{"leading delimiter", ".*.*.*", nil},
		{"trailing delimiter", "*.*.*.", nil},
		{"consecutive delimiters", "*..*.*", nil},
		{"consecutive segments", "*#.*", ErrExpectedDelimiter},
		{"consecutive wildcards", "**.*", ErrExpectedDelimiter},
		{"consecutive tracked", "##.*", ErrExpectedDelimiter},
		{"empty", "", ErrEmptyPattern},
		{"no segments", "...", ErrNoSegments},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePattern(tt.pattern)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewRetentionFilter(t *testing.T) {
	tests := []struct {
		name    string
		rules   []RetentionRule
		wantErr error
	}{
		{
			name: "valid rules",
			rules: []RetentionRule{
				{Pattern: "*.#.*-*", Amount: []int{3}},
			},
			wantErr: nil,
		},
		{
			name:    "empty rules - valid (keeps everything)",
			rules:   []RetentionRule{},
			wantErr: nil,
		},
		{
			name: "invalid pattern",
			rules: []RetentionRule{
				{Pattern: "", Amount: []int{1}},
			},
			wantErr: ErrEmptyPattern,
		},
		{
			name: "amount mismatch",
			rules: []RetentionRule{
				{Pattern: "*.#.#-*", Amount: []int{3}}, // 2 tracked segments but 3 amounts
			},
			wantErr: ErrAmountMismatch,
		},
		{
			name: "consecutive segments",
			rules: []RetentionRule{
				{Pattern: "*#.*", Amount: []int{1}},
			},
			wantErr: ErrExpectedDelimiter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type item struct {
				version string
			}
			_, err := NewRetentionFilter(tt.rules, func(i item) string { return i.version }, NoMatchKeep)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestCompareSegments(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		// Basic numeric
		{"numeric ascending", "1", "2", -1},
		{"numeric descending", "10", "9", 1},
		{"numeric equal", "10", "10", 0},
		{"numeric multi-digit", "2", "10", -1},

		// Alphanumeric (Debian rules)
		{"alphanumeric ascending", "ubuntu3", "ubuntu2", 1},
		{"alphanumeric equal", "ubuntu3", "ubuntu3", 0},
		{"alphanumeric different prefix", "debian1", "ubuntu1", -1},

		// Tilde rules (~ sorts before everything)
		{"tilde vs empty", "~", "", -1},
		{"empty vs tilde", "", "~", 1},
		{"tilde vs letter", "~", "a", -1},
		{"tilde vs number", "~1", "1", -1},
		{"double tilde", "~~", "~", -1},
		{"tilde in version", "1.0~rc1", "1.0", -1},
		{"tilde in version 2", "1.0~rc2", "1.0~rc1", 1},

		// Letters vs non-letters (letters sort first)
		{"letter vs dot", "a", ".", -1},
		{"letter vs plus", "a", "+", -1},
		{"dot vs plus", ".", "+", 1}, // both non-letters, ASCII order ('.' = 46, '+' = 43)

		// Complex Debian versions
		{"debian revision 1", "1ubuntu1", "1ubuntu2", -1},
		{"debian revision 2", "1.0-1", "1.0-2", -1},
		{"upstream vs debian", "1.0", "1.0-1", -1}, // empty debian_revision = 0
		{"complex 1", "1:1.0-1", "1:1.0-2", -1},
		{"complex 2", "2.4.7-1ubuntu1", "2.4.7-1ubuntu2", -1},

		// Edge cases
		{"empty strings", "", "", 0},
		{"empty vs non-empty", "", "a", -1},
		{"leading zeros", "01", "1", 0}, // numeric comparison
		{"mixed alpha-num", "1a2b3", "1a2b4", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareSegments(tt.a, tt.b)
			assert.Equal(t, tt.want, got, "compareSegments(%q, %q)", tt.a, tt.b)
		})
	}
}

func TestRetentionFilter_EdgeCases(t *testing.T) {
	type item struct {
		version string
		name    string
	}

	t.Run("Kept_Empty", func(t *testing.T) {
		filter, err := NewRetentionFilter([]RetentionRule{
			{Pattern: "*.#.*-*", Amount: []int{2}},
		}, func(i item) string { return i.version }, NoMatchKeep)
		require.NoError(t, err)

		// No items added
		kept, err := filter.Kept()
		require.NoError(t, err)
		assert.Empty(t, kept)
	})

	t.Run("IncrementalFiltering", func(t *testing.T) {
		filter, err := NewRetentionFilter([]RetentionRule{
			{Pattern: "*.#.*-*", Amount: []int{2}}, // Keep only 2 minors
		}, func(i item) string { return i.version }, NoMatchKeep)
		require.NoError(t, err)

		// Add 2 versions with different minors
		filter.Add(item{version: "1.34.0-1", name: "newest"})
		filter.Add(item{version: "1.33.0-1", name: "middle"})

		// Add an older version that will be filtered out
		filter.Add(item{version: "1.32.0-1", name: "too old"})

		// Add a newer version
		filter.Add(item{version: "1.35.0-1", name: "even newer"})

		// Verify only the 2 newest minors are kept
		kept, err := filter.Kept()
		require.NoError(t, err)
		assert.Len(t, kept, 2)

		keptVersions := make([]string, len(kept))
		for i, item := range kept {
			keptVersions[i] = item.version
		}
		sort.Strings(keptVersions)

		expected := []string{"1.34.0-1", "1.35.0-1"}
		sort.Strings(expected)
		assert.Equal(t, expected, keptVersions)
	})
}
