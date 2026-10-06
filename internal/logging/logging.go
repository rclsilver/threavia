// Package logging builds the structured logger shared by the Core and backend
// binaries.
//
// Logs must never carry credentials, tokens or provider secrets
// (THREAVIA_SPEC_V1.md section 28): configuration values reach the logs only
// through explicit redacting helpers.
package logging

import (
	"fmt"
	"io"
	"log/slog"
)

// Levels accepted by New.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Formats accepted by New.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// ValidLevel reports whether level is accepted by New.
func ValidLevel(level string) bool {
	switch level {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
		return true
	default:
		return false
	}
}

// ValidFormat reports whether format is accepted by New.
func ValidFormat(format string) bool {
	switch format {
	case FormatText, FormatJSON:
		return true
	default:
		return false
	}
}

// New builds a logger writing to w. Unknown levels or formats are an error
// rather than a silent fallback.
func New(level, format string, w io.Writer) (*slog.Logger, error) {
	var slogLevel slog.Level
	switch level {
	case LevelDebug:
		slogLevel = slog.LevelDebug
	case LevelInfo:
		slogLevel = slog.LevelInfo
	case LevelWarn:
		slogLevel = slog.LevelWarn
	case LevelError:
		slogLevel = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q", level)
	}

	opts := &slog.HandlerOptions{Level: slogLevel}
	switch format {
	case FormatText:
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case FormatJSON:
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("unknown log format %q", format)
	}
}
