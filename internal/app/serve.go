package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Serve starts an HTTP server to serve the public directory
func (a *Application) Serve(ctx context.Context) error {
	// Get host and port from config with defaults
	host := a.Config.Serve.Host
	if host == "" {
		host = "localhost"
	}

	port := a.Config.Serve.Port
	if port == 0 {
		port = 8080
	}

	// Get public directory path
	publicDir := a.Config.Directories.GetPublicPath()

	// Check if public directory exists
	if _, err := os.Stat(publicDir); os.IsNotExist(err) {
		return fmt.Errorf("public directory does not exist: %s (run 'generate' first)", publicDir)
	}

	// Resolve to absolute path for display
	absPublicDir, err := filepath.Abs(publicDir)
	if err != nil {
		absPublicDir = publicDir
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	slog.Info("Starting HTTP server",
		"address", addr,
		"directory", absPublicDir,
	)

	// Track current symlink target
	var currentTarget string
	var mu sync.RWMutex

	// Function to resolve symlink target
	resolveTarget := func() (string, error) {
		target, err := filepath.EvalSymlinks(publicDir)
		if err != nil {
			return "", err
		}
		return target, nil
	}

	// Initialize current target
	target, err := resolveTarget()
	if err != nil {
		return fmt.Errorf("failed to resolve public directory: %w", err)
	}
	currentTarget = target

	// Dynamic handler that resolves symlink on each request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		target := currentTarget
		mu.RUnlock()

		// Serve from the resolved target directory
		fs := http.FileServer(http.Dir(target))
		fs.ServeHTTP(w, r)
	})

	mux := http.NewServeMux()
	mux.Handle("/", handler)

	// Create server with configured address and handler
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Channel to capture server errors
	serverErr := make(chan error, 1)

	// Start fsnotify watcher for symlink changes
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	// Watch the parent directory to detect symlink changes
	// (watching a symlink directly doesn't detect when it's replaced)
	parentDir := filepath.Dir(publicDir)
	if err := watcher.Add(parentDir); err != nil {
		watcher.Close()
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	// Start watcher goroutine
	go func() {
		defer watcher.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Check if the event is for our symlink
				if filepath.Base(event.Name) == filepath.Base(publicDir) {
					// On any event (Create, Write, Remove), re-resolve the symlink
					newTarget, err := resolveTarget()
					if err != nil {
						slog.Warn("Failed to resolve public directory after change", "error", err)
						continue
					}

					mu.RLock()
					changed := newTarget != currentTarget
					mu.RUnlock()

					if changed {
						mu.Lock()
						oldTarget := currentTarget
						currentTarget = newTarget
						mu.Unlock()

						slog.Info("Public directory changed, reloading",
							"old", filepath.Base(oldTarget),
							"new", filepath.Base(newTarget),
						)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Warn("Watcher error", "error", err)
			}
		}
	}()

	// Start server in goroutine
	go func() {
		slog.Info("Server is ready", "url", fmt.Sprintf("http://%s", addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- fmt.Errorf("failed to start server: %w", err)
		}
		close(serverErr)
	}()

	// Wait for context cancellation or server error
	select {
	case err := <-serverErr:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		slog.Info("Shutting down server...")

		// Create shutdown context with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Attempt graceful shutdown
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}
		slog.Info("Server stopped gracefully")
	}

	return nil
}
