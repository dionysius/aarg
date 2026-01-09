package common

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// NoMatchBehavior defines how to handle items that don't match any retention pattern
type NoMatchBehavior int

const (
	NoMatchKeep   NoMatchBehavior = iota // Keep items that don't match any pattern (default, safe)
	NoMatchIgnore                        // Ignore/drop items that don't match any pattern
	NoMatchError                         // Return error when item doesn't match any pattern
)

// Errors
var (
	ErrEmptyPattern           = errors.New("empty pattern")
	ErrNoSegments             = errors.New("pattern must contain at least one segment (* or #)")
	ErrExpectedDelimiter      = errors.New("expected delimiter between segments")
	ErrAmountMismatch         = errors.New("amount count does not match tracked segment count")
	ErrVersionNotMatchPattern = errors.New("version does not match pattern")
	ErrNoMatchingPattern      = errors.New("item version does not match any retention pattern")
)

// RetentionRule defines pattern-based version retention policy
type RetentionRule struct {
	Pattern string `yaml:"pattern"`
	Amount  []int  `yaml:"amount"`
}

// RetentionPolicy defines retention rules with optional source filtering
type RetentionPolicy struct {
	RetentionRule `yaml:",inline"`
	FromSources   []string `yaml:"from_sources,omitempty"` // Optional: source name patterns (supports glob), empty = applies to all sources
}

// FilterBySource returns rules matching sourceName. Empty FromSources matches all.
// Supports glob patterns (* and ?) with ! prefix for negations (evaluated after positive matches).
func FilterBySource(policies []RetentionPolicy, sourceName string) []RetentionRule {
	var rules []RetentionRule
	for _, policy := range policies {
		if MatchesGlobPatterns(policy.FromSources, sourceName) {
			rules = append(rules, policy.RetentionRule)
		}
	}
	return rules
}

// RetentionFilter applies retention rules to items. Thread-safe for concurrent Add() and Kept().
type RetentionFilter[T any] struct {
	rules           []RetentionRule
	patterns        []pattern
	getVersion      func(T) string
	items           []T
	noMatchBehavior NoMatchBehavior
	mu              sync.Mutex // protects items slice
}

// pattern represents parsed pattern structure
type pattern struct {
	delimiters     []rune
	trackedIndices []int
	segmentCount   int
}

// version represents parsed version segments
type version struct {
	raw      string
	segments []string
}

// NewRetentionFilter creates a retention filter
func NewRetentionFilter[T any](rules []RetentionRule, getVersion func(T) string, noMatchBehavior NoMatchBehavior) (*RetentionFilter[T], error) {
	patterns := make([]pattern, len(rules))
	for i, rule := range rules {
		p, err := parsePattern(rule.Pattern)
		if err != nil {
			return nil, err
		}
		if len(rule.Amount) != len(p.trackedIndices) {
			return nil, ErrAmountMismatch
		}
		patterns[i] = p
	}

	return &RetentionFilter[T]{
		rules:           rules,
		patterns:        patterns,
		getVersion:      getVersion,
		items:           make([]T, 0),
		noMatchBehavior: noMatchBehavior,
	}, nil
}

// Filter returns items to keep based on retention rules
func (f *RetentionFilter[T]) Filter(items []T) ([]T, error) {
	// Group items by their applicable rules (most specific patterns)
	ruleGroups := make(map[int][]T) // ruleIdx -> items
	var noMatchItems []T            // items that don't match any pattern

	for _, item := range items {
		versionStr := f.getVersion(item)
		applicableRules := f.findApplicableRules(versionStr)

		if len(applicableRules) == 0 {
			// No patterns match this version
			switch f.noMatchBehavior {
			case NoMatchKeep:
				noMatchItems = append(noMatchItems, item)
			case NoMatchIgnore:
				// Skip this item
			case NoMatchError:
				return nil, fmt.Errorf("%w: version %q", ErrNoMatchingPattern, versionStr)
			}
			continue
		}

		// Add this item to all applicable rule groups
		for _, ruleIdx := range applicableRules {
			ruleGroups[ruleIdx] = append(ruleGroups[ruleIdx], item)
		}
	}

	keepSet := make(map[string]bool)

	// Apply retention for each rule group
	for ruleIdx, groupItems := range ruleGroups {
		rule := f.rules[ruleIdx]
		p := f.patterns[ruleIdx]

		var versions []version
		for _, item := range groupItems {
			v, err := parseVersion(f.getVersion(item), p)
			if err != nil {
				// Should not happen since findApplicableRules pre-filters
				continue
			}
			versions = append(versions, v)
		}

		for _, kept := range f.applyRetention(versions, p.trackedIndices, rule.Amount) {
			keepSet[kept] = true
		}
	}

	// Add all non-matching items to keep set
	for _, item := range noMatchItems {
		keepSet[f.getVersion(item)] = true
	}

	result := make([]T, 0, len(keepSet))
	for _, item := range items {
		if keepSet[f.getVersion(item)] {
			result = append(result, item)
		}
	}
	return result, nil
}

// findApplicableRules returns rule indices matching versionStr. Most segments win; ties return union.
func (f *RetentionFilter[T]) findApplicableRules(versionStr string) []int {
	var maxSegmentCount int
	var applicableRules []int

	// Find matching patterns and track maximum segment count
	for i, p := range f.patterns {
		_, err := parseVersion(versionStr, p)
		if err != nil {
			// Pattern doesn't match this version, skip silently
			continue
		}

		if p.segmentCount > maxSegmentCount {
			// Found a more specific pattern, reset
			maxSegmentCount = p.segmentCount
			applicableRules = []int{i}
		} else if p.segmentCount == maxSegmentCount {
			// Same specificity, add to union
			applicableRules = append(applicableRules, i)
		}
		// p.segmentCount < maxSegmentCount: ignore, less specific
	}

	return applicableRules
}

// Add adds an item. Thread-safe.
func (f *RetentionFilter[T]) Add(item T) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Handle NoMatchBehavior validation
	if f.noMatchBehavior == NoMatchError || f.noMatchBehavior == NoMatchIgnore {
		versionStr := f.getVersion(item)
		applicableRules := f.findApplicableRules(versionStr)
		if len(applicableRules) == 0 {
			if f.noMatchBehavior == NoMatchError {
				return fmt.Errorf("%w: version %q", ErrNoMatchingPattern, versionStr)
			}
			// NoMatchIgnore: skip this item silently
			return nil
		}
	}

	f.items = append(f.items, item)
	return nil
}

// Kept returns filtered items from Add() calls. Thread-safe.
func (f *RetentionFilter[T]) Kept() ([]T, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.Filter(f.items)
}

// applyRetention applies hierarchical filtering
func (f *RetentionFilter[T]) applyRetention(versions []version, trackedIndices, amounts []int) []string {
	if len(versions) == 0 {
		return nil
	}
	return f.retainLevel(versions, trackedIndices, amounts, 0)
}

// retainLevel recursively filters at hierarchy level
func (f *RetentionFilter[T]) retainLevel(versions []version, trackedIndices, amounts []int, level int) []string {
	if level >= len(trackedIndices) {
		if len(versions) == 0 {
			return nil
		}
		best := versions[0]
		for _, v := range versions[1:] {
			if f.compareVersions(v, best) > 0 {
				best = v
			}
		}
		return []string{best.raw}
	}

	idx := trackedIndices[level]
	amount := amounts[level]

	groups := make(map[string][]version)
	for _, v := range versions {
		key := strings.Join(v.segments[:idx], ":")
		groups[key] = append(groups[key], v)
	}

	var result []string
	for _, group := range groups {
		valueMap := make(map[string][]version)
		for _, v := range group {
			valueMap[v.segments[idx]] = append(valueMap[v.segments[idx]], v)
		}

		type pair struct {
			value    string
			versions []version
		}
		var pairs []pair
		for val, vers := range valueMap {
			pairs = append(pairs, pair{val, vers})
		}

		slices.SortFunc(pairs, func(a, b pair) int {
			return -compareSegments(a.value, b.value)
		})

		for i := 0; i < min(amount, len(pairs)); i++ {
			result = append(result, f.retainLevel(pairs[i].versions, trackedIndices, amounts, level+1)...)
		}
	}

	return result
}

// compareVersions compares versions segment-by-segment
func (f *RetentionFilter[T]) compareVersions(a, b version) int {
	for i := 0; i < len(a.segments); i++ {
		if cmp := compareSegments(a.segments[i], b.segments[i]); cmp != 0 {
			return cmp
		}
	}
	return 0
}

// parsePattern parses pattern string
func parsePattern(s string) (pattern, error) {
	if s == "" {
		return pattern{}, ErrEmptyPattern
	}

	var delimiters []rune
	var trackedIndices []int
	var delimBuf []rune
	segmentCount := 0

	for _, r := range s {
		if r == '*' || r == '#' {
			if segmentCount > 0 && len(delimBuf) == 0 {
				return pattern{}, ErrExpectedDelimiter
			}

			if segmentCount > 0 {
				delimiters = append(delimiters, delimBuf[0])
			}
			delimBuf = nil

			if r == '#' {
				trackedIndices = append(trackedIndices, segmentCount)
			}
			segmentCount++
		} else {
			delimBuf = append(delimBuf, r)
		}
	}

	if segmentCount == 0 {
		return pattern{}, ErrNoSegments
	}

	return pattern{
		delimiters:     delimiters,
		trackedIndices: trackedIndices,
		segmentCount:   segmentCount,
	}, nil
}

// parseVersion parses version per pattern
func parseVersion(s string, p pattern) (version, error) {
	if s == "" {
		return version{}, ErrVersionNotMatchPattern
	}

	var segments []string
	var buf strings.Builder

	for _, r := range s {
		if slices.Contains(p.delimiters, r) {
			if buf.Len() > 0 {
				segments = append(segments, buf.String())
				buf.Reset()
			}
		} else {
			buf.WriteRune(r)
		}
	}

	if buf.Len() > 0 {
		segments = append(segments, buf.String())
	}

	if len(segments) != p.segmentCount {
		return version{}, ErrVersionNotMatchPattern
	}

	return version{raw: s, segments: segments}, nil
}

// compareSegments compares segments using Debian rules
func compareSegments(a, b string) int {
	i, j := 0, 0

	for i < len(a) || j < len(b) {
		// Extract and compare non-digit parts
		nonDigitA, lenA := extractPart(a, i, false)
		nonDigitB, lenB := extractPart(b, j, false)

		if cmp := debianLexicalCompare(nonDigitA, nonDigitB); cmp != 0 {
			return cmp
		}
		i += lenA
		j += lenB

		// Extract and compare digit parts
		digitA, lenA := extractPart(a, i, true)
		digitB, lenB := extractPart(b, j, true)

		numA := parseInt(digitA)
		numB := parseInt(digitB)

		if numA != numB {
			if numA < numB {
				return -1
			}
			return 1
		}
		i += lenA
		j += lenB
	}

	return 0
}

// extractPart extracts digit/non-digit substring from position
func extractPart(s string, start int, isDigit bool) (string, int) {
	end := start
	for end < len(s) {
		r := rune(s[end])
		digit := r >= '0' && r <= '9'
		if digit != isDigit {
			break
		}
		end++
	}
	return s[start:end], end - start
}

// debianLexicalCompare implements Debian lexical ordering rules
func debianLexicalCompare(a, b string) int {
	i, j := 0, 0

	for i < len(a) || j < len(b) {
		var ca, cb rune
		if i < len(a) {
			ca = rune(a[i])
		}
		if j < len(b) {
			cb = rune(b[j])
		}

		// Tilde sorts before everything
		if ca == '~' && cb != '~' {
			return -1
		}
		if ca != '~' && cb == '~' {
			return 1
		}

		// End of string handling
		if ca == 0 {
			if cb == 0 {
				return 0
			}
			return -1
		}
		if cb == 0 {
			return 1
		}

		// Letters sort before non-letters
		aLetter := (ca >= 'A' && ca <= 'Z') || (ca >= 'a' && ca <= 'z')
		bLetter := (cb >= 'A' && cb <= 'Z') || (cb >= 'a' && cb <= 'z')

		if aLetter != bLetter {
			if aLetter {
				return -1
			}
			return 1
		}

		// ASCII comparison
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}

		i++
		j++
	}

	return 0
}

// parseInt converts string to int (empty = 0)
func parseInt(s string) int {
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}
