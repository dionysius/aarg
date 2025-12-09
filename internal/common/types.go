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
