package common

import (
	"testing"

	"github.com/aptly-dev/aptly/deb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// item is a simple test type for collector tests
// Fields match getMetadata return order: source, pkg, arch, version
type item struct {
	source  string
	pkg     string
	arch    string
	version string
}

// newTestCollector creates a collector with standard accessor functions for item type
func newTestCollector(policies []RetentionPolicy) *GenericRetentionCollector[item] {
	return NewGenericRetentionCollector(
		policies,
		func(i item) (string, string, string, string) {
			return i.source, i.pkg, i.arch, i.version
		},
	)
}

func TestGenericRetentionCollector(t *testing.T) {
	t.Run("no_retention_keeps_all", func(t *testing.T) {
		collector := newTestCollector([]RetentionPolicy{})

		require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-1"}))
		require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-2"}))
		require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.26.0-1"}))

		kept, err := collector.Kept()
		require.NoError(t, err)
		assert.Len(t, kept, 3, "without retention policies, all versions are kept")
	})

	t.Run("source_specific_retention", func(t *testing.T) {
		t.Run("different_rules_per_source", func(t *testing.T) {
			collector := newTestCollector(
				[]RetentionPolicy{
					{
						RetentionRule: RetentionRule{Pattern: "*.*.*-#", Amount: []int{2}},
						Sources:       []string{"nginx"},
					},
					{
						RetentionRule: RetentionRule{Pattern: "*.*.*-#", Amount: []int{1}},
						Sources:       []string{"php"},
					},
				},
			)

			// nginx: keep last 2 Debian revisions
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-3"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-1"}))

			// php: keep last 1 Debian revision
			require.NoError(t, collector.Add("noble", "main", item{"php", "php8.3", "amd64", "8.3.14-3"}))
			require.NoError(t, collector.Add("noble", "main", item{"php", "php8.3", "amd64", "8.3.14-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"php", "php8.3", "amd64", "8.3.14-1"}))

			kept, err := collector.Kept()
			require.NoError(t, err)
			assert.Len(t, kept, 3, "should keep 2 nginx + 1 php")

			counts := make(map[string]int)
			for _, i := range kept {
				counts[i.source]++
			}
			assert.Equal(t, 2, counts["nginx"], "nginx keeps last 2 revisions")
			assert.Equal(t, 1, counts["php"], "php keeps last 1 revision")
		})

		t.Run("no_matching_rules_keeps_all", func(t *testing.T) {
			collector := newTestCollector(
				[]RetentionPolicy{
					{
						RetentionRule: RetentionRule{Pattern: "*.*.*-#", Amount: []int{1}},
						Sources:       []string{"nginx"}, // only nginx has retention
					},
				},
			)

			// nginx: has retention rule (keep 1)
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-3"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-1"}))

			// apache: no retention rules (keep all)
			require.NoError(t, collector.Add("noble", "main", item{"apache", "apache2", "amd64", "2.4.62-1"}))
			require.NoError(t, collector.Add("noble", "main", item{"apache", "apache2", "amd64", "2.4.61-1"}))
			require.NoError(t, collector.Add("noble", "main", item{"apache", "apache2", "amd64", "2.4.60-1"}))

			kept, err := collector.Kept()
			require.NoError(t, err)
			assert.Len(t, kept, 4, "1 nginx (with retention) + 3 apache (no retention)")

			counts := make(map[string]int)
			for _, i := range kept {
				counts[i.source]++
			}
			assert.Equal(t, 1, counts["nginx"])
			assert.Equal(t, 3, counts["apache"])
		})
	})

	t.Run("grouping_independence", func(t *testing.T) {
		t.Run("per_distribution", func(t *testing.T) {
			collector := newTestCollector(
				[]RetentionPolicy{
					{RetentionRule: RetentionRule{Pattern: "*.*.*-#", Amount: []int{1}}},
				},
			)

			// Same package in different distributions - should filter independently
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-1"}))
			require.NoError(t, collector.Add("trixie", "main", item{"nginx", "nginx", "amd64", "1.26.0-2"}))
			require.NoError(t, collector.Add("trixie", "main", item{"nginx", "nginx", "amd64", "1.26.0-1"}))

			kept, err := collector.Kept()
			require.NoError(t, err)
			assert.Len(t, kept, 2, "1 per dist")

			versions := make(map[string]string)
			for _, i := range kept {
				// Store dist as key for this test
				versions[i.version] = i.version
			}
			assert.Contains(t, versions, "1.24.0-2", "noble keeps latest revision")
			assert.Contains(t, versions, "1.26.0-2", "trixie keeps latest revision")
		})

		t.Run("per_architecture", func(t *testing.T) {
			collector := newTestCollector(
				[]RetentionPolicy{
					{RetentionRule: RetentionRule{Pattern: "*.*.*-#", Amount: []int{1}}},
				},
			)

			// Same package in different architectures - should filter independently
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-1"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "arm64", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "arm64", "1.24.0-1"}))

			kept, err := collector.Kept()
			require.NoError(t, err)
			assert.Len(t, kept, 2, "1 per arch")

			arches := make(map[string]string)
			for _, i := range kept {
				arches[i.arch] = i.version
			}
			assert.Equal(t, "1.24.0-2", arches["amd64"])
			assert.Equal(t, "1.24.0-2", arches["arm64"])
		})

		t.Run("per_binary_package", func(t *testing.T) {
			collector := newTestCollector(
				[]RetentionPolicy{
					{RetentionRule: RetentionRule{Pattern: "*.*.*-#", Amount: []int{1}}},
				},
			)

			// Multiple binary packages from same source - should filter independently
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-1"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx-extras", "amd64", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx-extras", "amd64", "1.24.0-1"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx-common", "all", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx-common", "all", "1.24.0-1"}))

			kept, err := collector.Kept()
			require.NoError(t, err)
			assert.Len(t, kept, 3, "1 per binary package")

			packages := make(map[string]string)
			for _, i := range kept {
				packages[i.pkg] = i.version
			}
			assert.Equal(t, "1.24.0-2", packages["nginx"])
			assert.Equal(t, "1.24.0-2", packages["nginx-extras"])
			assert.Equal(t, "1.24.0-2", packages["nginx-common"])
		})

		t.Run("arch_all_vs_arch_specific", func(t *testing.T) {
			collector := newTestCollector(
				[]RetentionPolicy{
					{RetentionRule: RetentionRule{Pattern: "*.*.*-#", Amount: []int{1}}},
				},
			)

			// Architecture "all" packages are independent from arch-specific ones
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx-common", "all", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx-common", "all", "1.24.0-1"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-2"}))
			require.NoError(t, collector.Add("noble", "main", item{"nginx", "nginx", "amd64", "1.24.0-1"}))

			kept, err := collector.Kept()
			require.NoError(t, err)
			assert.Len(t, kept, 2, "1 per arch")

			arches := make(map[string]string)
			for _, i := range kept {
				arches[i.arch] = i.version
			}
			assert.Equal(t, "1.24.0-2", arches["all"])
			assert.Equal(t, "1.24.0-2", arches["amd64"])
		})
	})
}

func TestSpecializedConstructors(t *testing.T) {
	t.Run("NewPackageRetentionCollector", func(t *testing.T) {
		collector := NewPackageRetentionCollector(
			[]RetentionPolicy{
				{RetentionRule: RetentionRule{Pattern: "*.#", Amount: []int{2}}},
			},
		)

		pkg1 := &deb.Package{Name: "test-pkg", Source: "test-src", Version: "1.0", Architecture: "amd64"}
		pkg2 := &deb.Package{Name: "test-pkg", Source: "test-src", Version: "1.1", Architecture: "amd64"}
		pkg3 := &deb.Package{Name: "test-pkg", Source: "test-src", Version: "1.2", Architecture: "amd64"}

		assert.NoError(t, collector.Add("stable", "main", pkg1))
		assert.NoError(t, collector.Add("stable", "main", pkg2))
		assert.NoError(t, collector.Add("stable", "main", pkg3))

		kept, err := collector.Kept()
		require.NoError(t, err)
		assert.Len(t, kept, 2, "should keep last 2 patch versions")
	})
}
