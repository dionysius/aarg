package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dionysius/aarg/internal/cmd"
)

func main() {
	// Create context with graceful shutdown handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Track if we've received first signal
	firstSignal := false

	go func() {
		for sig := range sigChan {
			if !firstSignal {
				// First signal: trigger graceful shutdown
				slog.Warn("Received signal, initiating graceful shutdown", "signal", sig)
				firstSignal = true
				cancel() // Cancel context to trigger graceful shutdown
			} else {
				// Second signal: force exit
				slog.Warn("Received second signal, forcing exit", "signal", sig)
				os.Exit(130) // Exit code 128 + SIGINT(2) = 130
			}
		}
	}()

	if err := cmd.ExecuteContext(ctx); err != nil {
		slog.Error("Command failed", "error", err)
		os.Exit(1)
	}
}
