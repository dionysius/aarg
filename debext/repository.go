package debext

import (
	"maps"
	"slices"

	"github.com/aptly-dev/aptly/deb"
)

const (
	AllArchitecture    = "all"
	SourceArchitecture = "source"
)

// Repository manages packages organized by distribution and component
type Repository struct {
	// packages[distribution][component] = PackageList
	// PackageList internally manages packages across all architectures
	packages map[string]map[string]*deb.PackageList

	// latest tracks the latest package for each package name, distribution and architecture combination
	latest map[string]map[string]map[string]*deb.Package
}

// NewRepository creates a new empty repository
func NewRepository() *Repository {
	return &Repository{
		packages: make(map[string]map[string]*deb.PackageList),
		latest:   make(map[string]map[string]map[string]*deb.Package),
	}
}

// AddPackage adds a package to the repository.
// The package's architecture is managed internally by the PackageList.
// If component is empty, it defaults to "main".
func (r *Repository) AddPackage(pkg *deb.Package, distribution string, component string) error {
	// Default empty component to "main"
	if component == "" {
		component = "main"
	}

	// Ensure the nested maps exist
	if r.packages[distribution] == nil {
		r.packages[distribution] = make(map[string]*deb.PackageList)
	}
	if r.packages[distribution][component] == nil {
		r.packages[distribution][component] = deb.NewPackageList()
	}

	// Add package to the appropriate list
	err := r.packages[distribution][component].Add(pkg)
	if err != nil {
		return err
	}

	// Update latest
	r.updateLatest(pkg, distribution, pkg.Architecture)

	return nil
}

// GetPackageList returns the complete package list for a specific distribution and component.
// Returns the full PackageList without any filtering. Callers should use aptly's Filter() method
// with appropriate PackageQuery to filter by architecture or other criteria.
// Returns nil if no packages exist for the given combination.
func (r *Repository) GetPackageList(distribution, component string) *deb.PackageList {
	if r.packages[distribution] == nil {
		return nil
	}
	if r.packages[distribution][component] == nil {
		return nil
	}

	return r.packages[distribution][component]
}

// GetArchitectures returns all architectures available for a given distribution and component.
// If includeSource is true, the "source" architecture will be included in the list.
// Following aptly's pattern, "all" architecture is always excluded from the list.
// Returns a sorted slice of architecture names.
func (r *Repository) GetArchitectures(distribution, component string, includeSource bool) []string {
	if r.packages[distribution] == nil || r.packages[distribution][component] == nil {
		return nil
	}

	// Use aptly's Architectures method
	archs := r.packages[distribution][component].Architectures(includeSource)
	return archs
}

// GetDistributions returns all distributions in the repository.
// Returns a sorted slice of distribution names.
func (r *Repository) GetDistributions() []string {
	return slices.Sorted(maps.Keys(r.packages))
}

// GetComponents returns all components available for a given distribution.
// Returns a sorted slice of component names.
func (r *Repository) GetComponents(distribution string) []string {
	if r.packages[distribution] == nil {
		return nil
	}

	return slices.Sorted(maps.Keys(r.packages[distribution]))
}

// NumPackages returns the total number of packages across all distributions and components.
func (r *Repository) NumPackages() int {
	total := 0
	for _, components := range r.packages {
		for _, list := range components {
			total += list.Len()
		}
	}
	return total
}

// updateLatest updates the matrix with the latest package by version.
func (r *Repository) updateLatest(pkg *deb.Package, distribution, arch string) {
	pkgName := pkg.Name

	// Initialize latest maps if needed
	if r.latest[pkgName] == nil {
		r.latest[pkgName] = make(map[string]map[string]*deb.Package)
	}
	if r.latest[pkgName][distribution] == nil {
		r.latest[pkgName][distribution] = make(map[string]*deb.Package)
	}

	// Check if we already have a version of this package
	existingPkg := r.latest[pkgName][distribution][arch]
	if existingPkg != nil {
		// Compare versions and only update if newer
		cmp := deb.CompareVersions(pkg.Version, existingPkg.Version)
		if cmp <= 0 {
			// Current package is older or equal, don't update tracking
			return
		}
	}

	// Update latest version tracking
	r.latest[pkgName][distribution][arch] = pkg
}

// GetLatest returns the latest package by version for a specific distribution and architecture.
// For non-source architectures, falls back to "all" architecture if no arch-specific package exists.
// Returns nil if no such package exists.
func (r *Repository) GetLatest(packageName, distribution, arch string) *deb.Package {
	if r.latest[packageName] == nil {
		return nil
	}
	if r.latest[packageName][distribution] == nil {
		return nil
	}

	// Try architecture-specific package first
	pkg := r.latest[packageName][distribution][arch]
	if pkg != nil {
		return pkg
	}

	// For non-source architectures, fall back to "all" architecture
	if arch != SourceArchitecture {
		return r.latest[packageName][distribution][AllArchitecture]
	}

	return nil
}

// GetPackageNamesForComponent returns unique package names that exist in the specified component
// across any distribution. Returns a sorted slice of package names.
func (r *Repository) GetPackageNames(component string) []string {
	packageSet := make(map[string]struct{})

	for _, distComponents := range r.packages {
		if list, exists := distComponents[component]; exists {
			_ = list.ForEach(func(p *deb.Package) error {
				packageSet[p.Name] = struct{}{}
				return nil
			})
		}
	}

	return slices.Sorted(maps.Keys(packageSet))
}
