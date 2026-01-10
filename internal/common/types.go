package common

const (
	MainComponent  = "main"
	DebugComponent = "debug"
)

// PackageOptions controls which package types to include.
type PackageOptions struct {
	// Primary if set indicates the primary package to use for distribution sorting
	Primary string `yaml:"primary,omitempty"`
	// Debug indicates whether to include debug packages
	Debug bool `yaml:"debug"`
	// Source indicates whether to include source packages
	Source bool `yaml:"source"`
}

// RepositoryConfig options which can be relevant for feeds to download only requested packages
type RepositoryOptions struct {
	// Packages controls which package types are included
	Packages PackageOptions `yaml:"packages,omitempty"`
	// Distributions to fetch and process
	Distributions []string `yaml:"distributions,omitempty"`
	// Architectures to fetch and process
	Architectures []string `yaml:"architectures,omitempty"`
	// Retention policies for version filtering - which versions to keep
	Retention []RetentionPolicy `yaml:"retention,omitempty"`
}
