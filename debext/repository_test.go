package debext

import (
	"sort"
	"testing"

	"github.com/aptly-dev/aptly/deb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRepository(t *testing.T) {
	repo := NewRepository()
	assert.NotNil(t, repo)
	assert.Equal(t, 0, repo.NumPackages())
	assert.Empty(t, repo.GetDistributions())
}

func TestAddPackage(t *testing.T) {
	t.Run("Binary", func(t *testing.T) {
		repo := NewRepository()
		pkg := deb.NewPackageFromControlFile(deb.Stanza{
			"Package": "test-pkg", "Version": "1.0", "Architecture": "amd64",
		})

		err := repo.AddPackage(pkg, "noble", "")
		assert.NoError(t, err)
		assert.Equal(t, 1, repo.NumPackages())
		assert.Equal(t, []string{"main"}, repo.GetComponents("noble"))
	})

	t.Run("Source", func(t *testing.T) {
		repo := NewRepository()
		pkg, err := deb.NewSourcePackageFromControlFile(deb.Stanza{
			"Package": "test-src", "Version": "1.0",
		})
		require.NoError(t, err)

		err = repo.AddPackage(pkg, "noble", "main")
		assert.NoError(t, err)
		assert.Equal(t, 1, repo.NumPackages())
	})

	t.Run("MultipleArchitectures", func(t *testing.T) {
		repo := NewRepository()
		for _, arch := range []string{"amd64", "arm64", "all"} {
			pkg := deb.NewPackageFromControlFile(deb.Stanza{
				"Package": "test-pkg", "Version": "1.0", "Architecture": arch,
			})
			err := repo.AddPackage(pkg, "noble", "")
			require.NoError(t, err)
		}

		archs := repo.GetArchitectures("noble", "main", false)
		sort.Strings(archs)
		assert.Equal(t, []string{"amd64", "arm64"}, archs) // "all" excluded
	})

	t.Run("MultipleDistributions", func(t *testing.T) {
		repo := NewRepository()
		for _, dist := range []string{"noble", "jammy"} {
			pkg := deb.NewPackageFromControlFile(deb.Stanza{
				"Package": "test-pkg", "Version": "1.0", "Architecture": "amd64",
			})
			err := repo.AddPackage(pkg, dist, "")
			require.NoError(t, err)
		}

		assert.Equal(t, []string{"jammy", "noble"}, repo.GetDistributions())
	})

	t.Run("ExplicitComponent", func(t *testing.T) {
		repo := NewRepository()
		pkg := deb.NewPackageFromControlFile(deb.Stanza{
			"Package": "test-pkg", "Version": "1.0", "Architecture": "amd64",
		})

		err := repo.AddPackage(pkg, "noble", "contrib")
		require.NoError(t, err)
		assert.Equal(t, []string{"contrib"}, repo.GetComponents("noble"))
	})
}

func TestGetPackageList(t *testing.T) {
	repo := NewRepository()
	pkg := deb.NewPackageFromControlFile(deb.Stanza{
		"Package": "test-pkg", "Version": "1.0", "Architecture": "amd64",
	})
	err := repo.AddPackage(pkg, "noble", "main")
	require.NoError(t, err)

	list := repo.GetPackageList("noble", "main")
	assert.NotNil(t, list)
	assert.Equal(t, 1, list.Len())

	// Non-existent
	assert.Nil(t, repo.GetPackageList("nonexistent", "main"))
}

func TestGetArchitectures(t *testing.T) {
	repo := NewRepository()
	assert.Nil(t, repo.GetArchitectures("nonexistent", "main", false))

	pkg := deb.NewPackageFromControlFile(deb.Stanza{
		"Package": "test-pkg", "Version": "1.0", "Architecture": "amd64",
	})
	err := repo.AddPackage(pkg, "noble", "main")
	require.NoError(t, err)

	archs := repo.GetArchitectures("noble", "main", false)
	assert.Equal(t, []string{"amd64"}, archs)
}

func TestArchitectureAll(t *testing.T) {
	repo := NewRepository()

	// Add amd64 and "all" packages
	for _, arch := range []string{"amd64", "arm64", "all"} {
		pkg := deb.NewPackageFromControlFile(deb.Stanza{
			"Package": arch + "-pkg", "Version": "1.0", "Architecture": arch,
		})
		err := repo.AddPackage(pkg, "noble", "")
		require.NoError(t, err)
	}

	list := repo.GetPackageList("noble", "main")

	// Filter by architecture
	amd64List := deb.NewPackageList()
	_ = list.ForEach(func(p *deb.Package) error {
		if p.MatchesArchitecture("amd64") {
			return amd64List.Add(p)
		}
		return nil
	})
	assert.Equal(t, 2, amd64List.Len()) // amd64-pkg + all-pkg

	// "all" packages match all architectures
	allList := deb.NewPackageList()
	_ = list.ForEach(func(p *deb.Package) error {
		if p.Architecture == "all" {
			return allList.Add(p)
		}
		return nil
	})
	_ = allList.ForEach(func(pkg *deb.Package) error {
		assert.True(t, pkg.MatchesArchitecture("amd64"))
		assert.True(t, pkg.MatchesArchitecture("arm64"))
		return nil
	})
}
