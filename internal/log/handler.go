package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// ColorMode represents the color capability of the terminal
type ColorMode int

const (
	ColorModeNone ColorMode = iota
	ColorMode16
	ColorMode256
)

// Special attribute key for marking success messages
const SuccessKey = "_success"

// ANSI color codes for 256-color mode
const (
	color256Reset     = "\033[0m"
	color256Orange    = "\033[38;5;214m" // Brighter orange
	color256Red       = "\033[38;5;203m" // Lighter red
	color256Gray      = "\033[90m"
	color256Pink      = "\033[38;5;219m" // Medium pink
	color256LightBlue = "\033[38;5;117m"
	color256Green     = "\033[38;5;156m" // Light green
)

// ANSI color codes for 16-color mode
const (
	color16Reset     = "\033[0m"
	color16Orange    = "\033[33m" // Yellow as fallback
	color16Red       = "\033[31m"
	color16Gray      = "\033[90m" // Bright black
	color16Pink      = "\033[35m" // Magenta as fallback
	color16LightBlue = "\033[36m" // Cyan as fallback
	color16Green     = "\033[32m"
)

// detectColorMode detects the terminal's color capability based on TERM environment variable
func detectColorMode() ColorMode {
	term := os.Getenv("TERM")

	// No TERM set means no color support
	if term == "" {
		return ColorModeNone
	}

	// Check for 256-color support
	if strings.Contains(term, "256color") {
		return ColorMode256
	}

	// Any other non-empty TERM means basic ANSI color support
	return ColorMode16
}

// Handler is a custom slog handler that formats log output without timestamps or levels
type Handler struct {
	w         io.Writer
	level     slog.Leveler
	attrs     []slog.Attr
	group     string
	colorMode ColorMode
	mu        sync.Mutex
}

// NewHandler creates a new Handler
func NewHandler(w io.Writer, level slog.Leveler) *Handler {
	return &Handler{
		w:         w,
		level:     level,
		colorMode: detectColorMode(),
	}
}

// Enabled reports whether the handler handles records at the given level
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// Handle formats and writes a log record
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var prefix, color, reset string
	var keyColor, valueColor, successColor string

	// Select colors based on terminal capability
	switch h.colorMode {
	case ColorMode256:
		reset = color256Reset
		keyColor = color256Pink
		valueColor = color256LightBlue
		successColor = color256Green

		switch r.Level {
		case slog.LevelDebug:
			color = color256Gray
		case slog.LevelWarn:
			color = color256Orange
		case slog.LevelError:
			color = color256Red
		}
	case ColorMode16:
		reset = color16Reset
		keyColor = color16Pink
		valueColor = color16LightBlue
		successColor = color16Green

		switch r.Level {
		case slog.LevelDebug:
			color = color16Gray
		case slog.LevelWarn:
			color = color16Orange
		case slog.LevelError:
			color = color16Red
		}
	case ColorModeNone:
		// In no-color mode, use prefixes for all levels
		switch r.Level {
		case slog.LevelDebug:
			prefix = "debug: "
		case slog.LevelInfo:
			prefix = "info: "
		case slog.LevelWarn:
			prefix = "warning: "
		case slog.LevelError:
			prefix = "error: "
		}
	}
	// If colorMode is None, all color strings remain empty

	// Check attributes for success marker before writing message
	attrs := make([]slog.Attr, 0, r.NumAttrs())
	isSuccess := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == SuccessKey {
			isSuccess = true
			return true // Skip adding the success marker to output
		}
		attrs = append(attrs, a)
		return true
	})

	// Add handler-level attributes
	attrs = append(h.attrs, attrs...)

	// Apply success color to message if marked
	if isSuccess && h.colorMode != ColorModeNone {
		if color == "" {
			color = successColor
		}
	}

	// Write the message with optional prefix and color
	if color != "" {
		fmt.Fprintf(h.w, "%s%s%s%s", color, prefix, r.Message, reset)
	} else if prefix != "" {
		fmt.Fprintf(h.w, "%s%s", prefix, r.Message)
	} else {
		fmt.Fprint(h.w, r.Message)
	}

	// Format and write attributes
	for _, attr := range attrs {
		// Check if the value is an error type
		if attr.Value.Kind() == slog.KindAny {
			if _, isErr := attr.Value.Any().(error); isErr {
				if h.colorMode != ColorModeNone {
					errorColor := color256Red
					if h.colorMode == ColorMode16 {
						errorColor = color16Red
					}
					fmt.Fprintf(h.w, " %s%s=%q%s", errorColor, attr.Key, attr.Value, reset)
				} else {
					fmt.Fprintf(h.w, " %s=%q", attr.Key, attr.Value)
				}
				continue
			}
		}

		// Check if the value is numeric (int or float) to avoid quotes
		isNumeric := attr.Value.Kind() == slog.KindInt64 ||
			attr.Value.Kind() == slog.KindUint64 ||
			attr.Value.Kind() == slog.KindFloat64

		if h.colorMode != ColorModeNone {
			if isNumeric {
				fmt.Fprintf(h.w, " %s%s%s=%s%v%s", keyColor, attr.Key, reset, valueColor, attr.Value, reset)
			} else {
				fmt.Fprintf(h.w, " %s%s%s=%s%q%s", keyColor, attr.Key, reset, valueColor, attr.Value, reset)
			}
		} else {
			if isNumeric {
				fmt.Fprintf(h.w, " %s=%v", attr.Key, attr.Value)
			} else {
				fmt.Fprintf(h.w, " %s=%q", attr.Key, attr.Value)
			}
		}
	}

	// Newline
	fmt.Fprintln(h.w)

	return nil
}

// WithAttrs returns a new Handler with the given attributes
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{
		w:         h.w,
		level:     h.level,
		attrs:     append(h.attrs, attrs...),
		group:     h.group,
		colorMode: h.colorMode,
	}
}

// WithGroup returns a new Handler with the given group
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &Handler{
		w:         h.w,
		level:     h.level,
		attrs:     h.attrs,
		group:     h.group + name + ".",
		colorMode: h.colorMode,
	}
}

// Success returns an Attr that marks a log message as a success message
func Success() slog.Attr {
	return slog.Bool(SuccessKey, true)
}
