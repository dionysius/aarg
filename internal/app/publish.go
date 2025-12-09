package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dionysius/aarg/internal/log"
	"github.com/dionysius/aarg/internal/provider"
)

// Publish uploads generated repository to configured hosting provider
func (a *Application) Publish(ctx context.Context) error {
	// Get the configured provider
	prov, err := a.getProvider()
	if err != nil {
		return fmt.Errorf("failed to get provider: %w", err)
	}

	slog.Info("Publishing repository", "provider", fmt.Sprintf("%T", prov))

	// Upload the public directory
	publicDir := a.Config.Directories.GetPublicPath()
	if err := prov.Publish(ctx, publicDir); err != nil {
		return fmt.Errorf("failed to publish: %w", err)
	}

	slog.Info("Publish complete", log.Success())

	return nil
}

// getProvider returns the configured deployment provider
func (a *Application) getProvider() (provider.Provider, error) {
	// Check for Cloudflare Pages configuration
	if a.Config.Cloudflare.APIToken != "" && a.Config.Cloudflare.AccountID != "" && a.Config.Cloudflare.ProjectName != "" {
		return provider.NewCloudflare(
			a.Config.Cloudflare.APIToken,
			a.Config.Cloudflare.AccountID,
			a.Config.Cloudflare.ProjectName,
			provider.CloudflareCleanupConfig{
				OlderThanDays: a.Config.Cloudflare.Cleanup.OlderThanDays,
				KeepLast:      a.Config.Cloudflare.Cleanup.KeepLast,
			},
			a.Config.Repositories,
			a.Config.Generate.PoolMode,
		)
	}

	// No provider configured
	return nil, fmt.Errorf("no deployment provider configured (check cloudflare settings in config)")
}
