// Package executor handles job execution on agents.
package executor

import (
	"context"
	"fmt"
	"time"

	"cymbytes.com/cymconductor/internal/agent/actions"
	"cymbytes.com/cymconductor/internal/agent/client"
	"cymbytes.com/cymconductor/internal/agent/impersonation"
	"github.com/rs/zerolog"
)

// Executor handles job execution.
type Executor struct {
	config        Config
	registry      *actions.Registry
	impersonation *impersonation.Manager
	logger        zerolog.Logger
}

// Config holds executor configuration.
type Config struct {
	Browsing        BrowsingConfig
	FileActivity    FileActivityConfig
	ProcessActivity ProcessActivityConfig
	Impersonation   ImpersonationConfig
}

// BrowsingConfig holds browser settings.
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

// ImpersonationConfig holds impersonation settings.
type ImpersonationConfig struct {
	Enabled            bool
	Password           string
	AllowedUsers       []string
	AllowedUserPattern string
}

// RunAsConfig specifies user impersonation for job execution.
type RunAsConfig struct {
	User      string // Full username (DOMAIN\user)
	LogonType string // interactive, network, batch
}

// New creates a new executor.
func New(cfg Config, logger zerolog.Logger) *Executor {
	logger = logger.With().Str("component", "executor").Logger()

	// Create action registry with configuration
	registry := actions.NewRegistry(actions.Config{
		Browsing: actions.BrowsingConfig{
			BrowserPath: cfg.Browsing.BrowserPath,
			UserDataDir: cfg.Browsing.UserDataDir,
		},
		FileActivity: actions.FileActivityConfig{
			AllowedDirectories: cfg.FileActivity.AllowedDirectories,
		},
		ProcessActivity: actions.ProcessActivityConfig{
			AllowedProcesses: cfg.ProcessActivity.AllowedProcesses,
		},
	}, logger)

	// Create impersonation manager
	impMgr := impersonation.NewManager(impersonation.Config{
		Enabled:            cfg.Impersonation.Enabled,
		Password:           cfg.Impersonation.Password,
		AllowedUsers:       cfg.Impersonation.AllowedUsers,
		AllowedUserPattern: cfg.Impersonation.AllowedUserPattern,
	}, logger)

	return &Executor{
		config:        cfg,
		registry:      registry,
		impersonation: impMgr,
		logger:        logger,
	}
}

// Execute runs a job action and returns the result.
// For backward compatibility, this executes without impersonation.
func (e *Executor) Execute(ctx context.Context, actionType string, params map[string]interface{}) (*client.JobResult, error) {
	return e.ExecuteAs(ctx, actionType, params, nil)
}

// ExecuteAs runs a job action as the specified user (if runAs is provided).
func (e *Executor) ExecuteAs(ctx context.Context, actionType string, params map[string]interface{}, runAs *RunAsConfig) (*client.JobResult, error) {
	startTime := time.Now()

	logEvent := e.logger.Info().
		Str("action", actionType).
		Interface("params", params)

	if runAs != nil && runAs.User != "" {
		logEvent = logEvent.Str("run_as", runAs.User)
	}
	logEvent.Msg("Executing action")

	// Get the action handler from registry
	handler, err := e.registry.GetHandler(actionType)
	if err != nil {
		return nil, fmt.Errorf("unknown action type: %s", actionType)
	}

	// Execute with or without impersonation
	var result *actions.Result
	if runAs != nil && runAs.User != "" {
		result, err = e.executeWithImpersonation(ctx, handler, params, runAs)
	} else {
		result, err = handler.Execute(ctx, params)
	}

	if err != nil {
		return nil, fmt.Errorf("action execution failed: %w", err)
	}

	// Add timing if not set by handler
	if result.DurationMs == 0 {
		result.DurationMs = time.Since(startTime).Milliseconds()
	}

	return &client.JobResult{
		Data:       result.Data,
		Summary:    result.Summary,
		DurationMs: result.DurationMs,
	}, nil
}

// executeWithImpersonation runs an action in the context of the specified user.
func (e *Executor) executeWithImpersonation(ctx context.Context, handler actions.Handler, params map[string]interface{}, runAs *RunAsConfig) (*actions.Result, error) {
	if !e.impersonation.IsEnabled() {
		return nil, fmt.Errorf("impersonation is disabled")
	}

	if !e.impersonation.IsUserAllowed(runAs.User) {
		return nil, fmt.Errorf("user %s is not in allowed impersonation list", runAs.User)
	}

	logonType := impersonation.ParseLogonType(runAs.LogonType)

	var result *actions.Result
	var execErr error

	err := e.impersonation.RunAs(ctx, runAs.User, logonType, func() error {
		result, execErr = handler.Execute(ctx, params)
		return execErr
	})

	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetImpersonationManager returns the impersonation manager for direct process creation.
func (e *Executor) GetImpersonationManager() *impersonation.Manager {
	return e.impersonation
}
