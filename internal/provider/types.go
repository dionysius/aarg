package provider

import "context"

// Provider defines the interface for deployment providers
type Provider interface {
	// Publish uploads the repository contents to the provider
	// outputDir is the path to the directory containing the files to publish
	Publish(ctx context.Context, outputDir string) error
}
