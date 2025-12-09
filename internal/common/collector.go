package common

import (
	"sync"

	"github.com/aptly-dev/aptly/deb"
)

// GenericRetentionCollector collects items with retention filtering per source package
// Structure: dist -> component -> packageName -> arch -> RetentionFilter
// Thread-safe for concurrent Add() and read operations
type GenericRetentionCollector[T any] struct {
	// filters maps: dist -> component -> packageName -> arch -> RetentionFilter
	filters map[string]map[string]map[string]map[string]*RetentionFilter[T]

	// Retention policies to apply
	retentionPolicies []RetentionPolicy

	// Function to extract metadata from items
	// Returns: sourceName, packageName, arch, version
	getMetadata func(T) (string, string, string, string)

	// mu protects the filters map from concurrent access
	mu sync.RWMutex
}

// NewGenericRetentionCollector creates a new collector with retention policies
// getMetadata should return: sourceName, packageName, arch, version
// Always uses NoMatchKeep behavior for items that don't match any retention pattern
func NewGenericRetentionCollector[T any](
	retentionPolicies []RetentionPolicy,
	getMetadata func(T) (string, string, string, string),
) *GenericRetentionCollector[T] {
	return &GenericRetentionCollector[T]{
		filters:           make(map[string]map[string]map[string]map[string]*RetentionFilter[T]),
		retentionPolicies: retentionPolicies,
		getMetadata:       getMetadata,
	}
}

// Add adds an item to the collector with retention filtering for a specific component
// Thread-safe for concurrent calls
func (c *GenericRetentionCollector[T]) Add(dist, component string, item T) error {
	sourceName, packageName, arch, _ := c.getMetadata(item)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Ensure distribution map exists
	if _, exists := c.filters[dist]; !exists {
		c.filters[dist] = make(map[string]map[string]map[string]*RetentionFilter[T])
	}

	// Ensure component map exists
	if _, exists := c.filters[dist][component]; !exists {
		c.filters[dist][component] = make(map[string]map[string]*RetentionFilter[T])
	}

	// Ensure package name map exists
	if _, exists := c.filters[dist][component][packageName]; !exists {
		c.filters[dist][component][packageName] = make(map[string]*RetentionFilter[T])
	}

	// Get or create retention filter for this dist+component+package+arch
	filter, exists := c.filters[dist][component][packageName][arch]
	if !exists {
		var err error
		retentionRules := FilterBySource(c.retentionPolicies, sourceName)
		filter, err = NewRetentionFilter(retentionRules, func(item T) string {
			_, _, _, v := c.getMetadata(item)
			return v
		}, NoMatchKeep)
		if err != nil {
			return err
		}
		c.filters[dist][component][packageName][arch] = filter
	}

	// Add to filter (retention happens internally)
	if err := filter.Add(item); err != nil {
		return err
	}
	return nil
}

// Kept returns all items that passed both source filtering and retention policies
// Thread-safe for concurrent access
func (c *GenericRetentionCollector[T]) Kept() ([]T, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var keptItems []T

	// Iterate through all distributions
	for _, components := range c.filters {
		// Iterate through all components
		for _, packages := range components {
			// Iterate through all package names
			for _, archFilters := range packages {
				// Iterate through all architectures
				for _, filter := range archFilters {
					// Add all kept items from this filter
					items, err := filter.Kept()
					if err != nil {
						return nil, err
					}
					keptItems = append(keptItems, items...)
				}
			}
		}
	}

	return keptItems, nil
}

// ForEachKept iterates through all kept items with their key information
// The callback receives: dist, component, packageName, arch, and the item
// Thread-safe for concurrent access
func (c *GenericRetentionCollector[T]) ForEachKept(fn func(dist, component, packageName, arch string, item T) error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Iterate through all distributions
	for dist, components := range c.filters {
		// Iterate through all components
		for component, packages := range components {
			// Iterate through all package names
			for packageName, archFilters := range packages {
				// Iterate through all architectures
				for arch, filter := range archFilters {
					// Process all kept items from this filter
					items, err := filter.Kept()
					if err != nil {
						return err
					}
					for _, item := range items {
						if err := fn(dist, component, packageName, arch, item); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	return nil
}

// NewPackageRetentionCollector creates a collector for *deb.Package items
// Package grouping: by package name and architecture
// Always uses NoMatchKeep behavior
func NewPackageRetentionCollector(
	retentionPolicies []RetentionPolicy,
) *GenericRetentionCollector[*deb.Package] {
	return NewGenericRetentionCollector(
		retentionPolicies,
		func(pkg *deb.Package) (string, string, string, string) {
			return pkg.Source, pkg.Name, pkg.Architecture, pkg.Version
		},
	)
}
