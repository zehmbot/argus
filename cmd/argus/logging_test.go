package main

import "testing"

func TestSetupLogging(t *testing.T) {
	for _, format := range []string{logFormatText, logFormatJSON} {
		t.Run(format, func(t *testing.T) {
			if err := setupLogging(format); err != nil {
				t.Errorf("setupLogging(%q) error = %v", format, err)
			}
		})
	}
}

// A mistyped format must be rejected rather than silently falling back, or a
// cron job configured for JSON would quietly emit text that nothing parses.
func TestSetupLogging_UnknownFormat(t *testing.T) {
	for _, format := range []string{"", "JSON", "logfmt", "pretty"} {
		t.Run(format, func(t *testing.T) {
			if err := setupLogging(format); err == nil {
				t.Errorf("setupLogging(%q) error = nil, want error", format)
			}
		})
	}
}
