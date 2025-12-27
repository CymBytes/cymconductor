// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
)

// Result represents the outcome of an action execution.
type Result struct {
	Data       map[string]interface{}
	Summary    string
	DurationMs int64
}

// Handler is the interface that all actions must implement.
type Handler interface {
	// Execute runs the action with the given parameters.
	Execute(ctx context.Context, params map[string]interface{}) (*Result, error)
}

// Registry holds all available action handlers.
type Registry struct {
	handlers map[string]Handler
	logger   zerolog.Logger
}

// Config holds configuration for all actions.
type Config struct {
	Browsing        BrowsingConfig
	FileActivity    FileActivityConfig
	ProcessActivity ProcessActivityConfig
	EmailTraffic    EmailTrafficConfig
	EmailReceive    EmailReceiveConfig
}

// BrowsingConfig holds browser automation settings.
type BrowsingConfig struct {
	BrowserPath string
	UserDataDir string
}

// FileActivityConfig holds file operation settings.
type FileActivityConfig struct {
	AllowedDirectories []string
}

// ProcessActivityConfig holds process spawn settings.
type ProcessActivityConfig struct {
	AllowedProcesses []string
}

// EmailTrafficConfig holds email simulation settings.
type EmailTrafficConfig struct {
	DefaultServer   string
	DefaultPort     int
	DefaultUsername string
	DefaultPassword string
}

// EmailReceiveConfig holds email receive and attachment handling settings.
type EmailReceiveConfig struct {
	// IMAP settings
	IMAPServer   string
	IMAPPort     int
	IMAPUsername string
	IMAPPassword string
	IMAPTLS      bool

	// Outlook settings (Windows only)
	OutlookEnabled bool

	// Security controls
	AllowedSaveDirectories []string
	AllowedFileExtensions  []string
	BlockedFileExtensions  []string
	AllowExecution         bool
	AllowedExecutables     []string

	// Limits
	MaxAttachmentSizeMB int
	MaxEmailsPerQuery   int
}

// NewRegistry creates a new action registry with all handlers registered.
func NewRegistry(cfg Config, logger zerolog.Logger) *Registry {
	r := &Registry{
		handlers: make(map[string]Handler),
		logger:   logger,
	}

	// Register simulation action handlers (noise generation)
	r.handlers["simulate_browsing"] = NewBrowsingHandler(cfg.Browsing, logger)
	r.handlers["simulate_file_activity"] = NewFileActivityHandler(cfg.FileActivity, logger)
	r.handlers["simulate_process_activity"] = NewProcessActivityHandler(cfg.ProcessActivity, logger)
	r.handlers["simulate_email_traffic"] = NewEmailTrafficHandler(cfg.EmailTraffic, logger)
	r.handlers["email_receive"] = NewEmailReceiveHandler(cfg.EmailReceive, logger)

	// Register observation action handlers (for scoring/verification)
	r.handlers["observe_process_state"] = NewObserveProcessHandler(logger)
	r.handlers["observe_file_state"] = NewObserveFileHandler(logger)
	r.handlers["observe_user_state"] = NewObserveUserHandler(logger)
	r.handlers["capture_powershell_history"] = NewCapturePowerShellHandler(logger)

	logger.Info().
		Int("action_count", len(r.handlers)).
		Msg("Action registry initialized")

	return r
}

// GetHandler returns the handler for the given action type.
func (r *Registry) GetHandler(actionType string) (Handler, error) {
	handler, ok := r.handlers[actionType]
	if !ok {
		return nil, fmt.Errorf("unknown action type: %s", actionType)
	}
	return handler, nil
}

// ListActions returns all registered action types.
func (r *Registry) ListActions() []string {
	actions := make([]string, 0, len(r.handlers))
	for action := range r.handlers {
		actions = append(actions, action)
	}
	return actions
}
