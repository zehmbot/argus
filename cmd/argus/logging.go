package main

import (
	"fmt"
	"log/slog"
	"os"
)

// Log formats accepted by --log-format.
const (
	logFormatText = "text"
	logFormatJSON = "json"
)

// setupLogging installs the default logger.
//
// Logs go to stderr, never stdout. Stdout carries a command's output — the
// tables, and list's JSON — and a log line mixed into that would break
// anything piping the result into another program.
func setupLogging(format string) error {
	var handler slog.Handler

	switch format {
	case logFormatText:
		handler = slog.NewTextHandler(os.Stderr, nil)
	case logFormatJSON:
		handler = slog.NewJSONHandler(os.Stderr, nil)
	default:
		return fmt.Errorf("unknown --log-format %q, want %s or %s", format, logFormatText, logFormatJSON)
	}

	slog.SetDefault(slog.New(handler))

	return nil
}
