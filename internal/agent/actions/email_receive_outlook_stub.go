//go:build !windows
// +build !windows

// Package actions provides predefined action implementations for the agent.
package actions

import "github.com/rs/zerolog"

// newOutlookBackend returns nil on non-Windows platforms.
// Outlook COM automation is only available on Windows.
func newOutlookBackend(cfg EmailReceiveConfig, logger zerolog.Logger) EmailBackend {
	return nil
}
