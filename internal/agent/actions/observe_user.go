// Package actions provides predefined action implementations for the agent.
package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// ObserveUserHandler handles observe_user_state actions.
// It checks the state of an Active Directory user account.
type ObserveUserHandler struct {
	logger zerolog.Logger
}

// NewObserveUserHandler creates a new observe user state handler.
func NewObserveUserHandler(logger zerolog.Logger) *ObserveUserHandler {
	return &ObserveUserHandler{
		logger: logger.With().Str("action", "observe_user_state").Logger(),
	}
}

// Execute checks the state of an AD user account.
// Parameters:
//   - username: SAM account name of the user (e.g., "jsmith")
//   - domain: Domain name (optional, defaults to current domain)
//   - expected_state: Expected state - "enabled" or "disabled"
func (h *ObserveUserHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting user state observation")

	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("observe_user_state is only supported on Windows")
	}

	// Parse parameters
	username, ok := getString(params, "username")
	if !ok || username == "" {
		return nil, fmt.Errorf("username parameter is required")
	}

	domain, _ := getString(params, "domain")
	expectedState, _ := getString(params, "expected_state")
	if expectedState == "" {
		expectedState = "disabled" // Default: expect user to be disabled
	}

	if expectedState != "enabled" && expectedState != "disabled" {
		return nil, fmt.Errorf("expected_state must be 'enabled' or 'disabled'")
	}

	// Query AD user state
	userInfo, err := h.queryADUser(ctx, username, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to query AD user: %w", err)
	}

	// Determine actual state
	actualState := "enabled"
	if !userInfo.Enabled {
		actualState = "disabled"
	}

	stateMatches := actualState == expectedState

	duration := time.Since(startTime)
	h.logger.Info().
		Str("username", username).
		Str("domain", domain).
		Bool("enabled", userInfo.Enabled).
		Str("expected_state", expectedState).
		Str("actual_state", actualState).
		Bool("state_matches", stateMatches).
		Dur("duration", duration).
		Msg("User state observation complete")

	return &Result{
		Data: map[string]interface{}{
			"username":        username,
			"domain":          domain,
			"enabled":         userInfo.Enabled,
			"state":           actualState,
			"expected_state":  expectedState,
			"state_matches":   stateMatches,
			"display_name":    userInfo.DisplayName,
			"last_logon":      userInfo.LastLogon,
			"locked_out":      userInfo.LockedOut,
			"password_expired": userInfo.PasswordExpired,
			"checked_at":      time.Now().UTC().Format(time.RFC3339),
		},
		Summary:    fmt.Sprintf("User '%s' is %s (expected: %s)", username, actualState, expectedState),
		DurationMs: duration.Milliseconds(),
	}, nil
}

// ADUserInfo holds information about an AD user.
type ADUserInfo struct {
	Enabled         bool   `json:"Enabled"`
	DisplayName     string `json:"DisplayName"`
	LastLogon       string `json:"LastLogon"`
	LockedOut       bool   `json:"LockedOut"`
	PasswordExpired bool   `json:"PasswordExpired"`
	UserNotFound    bool   `json:"UserNotFound"`
}

// queryADUser queries Active Directory for user information.
func (h *ObserveUserHandler) queryADUser(ctx context.Context, username, domain string) (*ADUserInfo, error) {
	// Build PowerShell script to query AD
	var script string

	if domain != "" {
		// Query specific domain
		script = fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
try {
    $user = Get-ADUser -Identity '%s' -Server '%s' -Properties Enabled,DisplayName,LastLogonDate,LockedOut,PasswordExpired
    @{
        Enabled = $user.Enabled
        DisplayName = $user.DisplayName
        LastLogon = if ($user.LastLogonDate) { $user.LastLogonDate.ToString('o') } else { $null }
        LockedOut = $user.LockedOut
        PasswordExpired = $user.PasswordExpired
        UserNotFound = $false
    } | ConvertTo-Json -Compress
} catch [Microsoft.ActiveDirectory.Management.ADIdentityNotFoundException] {
    @{ UserNotFound = $true } | ConvertTo-Json -Compress
} catch {
    throw $_
}
`, username, domain)
	} else {
		// Query current domain
		script = fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
try {
    $user = Get-ADUser -Identity '%s' -Properties Enabled,DisplayName,LastLogonDate,LockedOut,PasswordExpired
    @{
        Enabled = $user.Enabled
        DisplayName = $user.DisplayName
        LastLogon = if ($user.LastLogonDate) { $user.LastLogonDate.ToString('o') } else { $null }
        LockedOut = $user.LockedOut
        PasswordExpired = $user.PasswordExpired
        UserNotFound = $false
    } | ConvertTo-Json -Compress
} catch [Microsoft.ActiveDirectory.Management.ADIdentityNotFoundException] {
    @{ UserNotFound = $true } | ConvertTo-Json -Compress
} catch {
    throw $_
}
`, username)
	}

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		h.logger.Error().
			Err(err).
			Str("stderr", stderr.String()).
			Msg("PowerShell command failed")
		return nil, fmt.Errorf("PowerShell command failed: %s", stderr.String())
	}

	// Parse JSON output
	output := strings.TrimSpace(stdout.String())
	h.logger.Debug().Str("output", output).Msg("PowerShell output")

	var userInfo ADUserInfo
	if err := json.Unmarshal([]byte(output), &userInfo); err != nil {
		return nil, fmt.Errorf("failed to parse PowerShell output: %w", err)
	}

	if userInfo.UserNotFound {
		return nil, fmt.Errorf("user '%s' not found in Active Directory", username)
	}

	return &userInfo, nil
}
