// Package actions provides predefined action implementations for the agent.
package actions

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// ObserveProcessHandler handles observe_process_state actions.
// It checks whether a specific process is running or not.
type ObserveProcessHandler struct {
	logger zerolog.Logger
}

// NewObserveProcessHandler creates a new observe process state handler.
func NewObserveProcessHandler(logger zerolog.Logger) *ObserveProcessHandler {
	return &ObserveProcessHandler{
		logger: logger.With().Str("action", "observe_process_state").Logger(),
	}
}

// Execute checks the state of a process.
// Parameters:
//   - process_name: Name of the process to check (e.g., "malware.exe")
//   - expected_state: Expected state - "running" or "not_running"
//   - check_all_users: Whether to check processes from all users (default: true)
func (h *ObserveProcessHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting process state observation")

	// Parse parameters
	processName, ok := getString(params, "process_name")
	if !ok || processName == "" {
		return nil, fmt.Errorf("process_name parameter is required")
	}

	expectedState, _ := getString(params, "expected_state")
	if expectedState == "" {
		expectedState = "not_running" // Default: expect process to be killed
	}

	if expectedState != "running" && expectedState != "not_running" {
		return nil, fmt.Errorf("expected_state must be 'running' or 'not_running'")
	}

	// Check process state
	found, processInfo, err := h.checkProcessState(ctx, processName)
	if err != nil {
		return nil, fmt.Errorf("failed to check process state: %w", err)
	}

	actualState := "not_running"
	if found {
		actualState = "running"
	}

	stateMatches := actualState == expectedState

	duration := time.Since(startTime)
	h.logger.Info().
		Str("process", processName).
		Bool("found", found).
		Str("expected_state", expectedState).
		Str("actual_state", actualState).
		Bool("state_matches", stateMatches).
		Dur("duration", duration).
		Msg("Process state observation complete")

	return &Result{
		Data: map[string]interface{}{
			"process_name":   processName,
			"found":          found,
			"state":          actualState,
			"expected_state": expectedState,
			"state_matches":  stateMatches,
			"process_info":   processInfo,
			"checked_at":     time.Now().UTC().Format(time.RFC3339),
		},
		Summary:    fmt.Sprintf("Process '%s' is %s (expected: %s)", processName, actualState, expectedState),
		DurationMs: duration.Milliseconds(),
	}, nil
}

// checkProcessState checks if a process is running on the system.
// Returns: found (bool), processInfo (map with details), error
func (h *ObserveProcessHandler) checkProcessState(ctx context.Context, processName string) (bool, map[string]interface{}, error) {
	switch runtime.GOOS {
	case "windows":
		return h.checkProcessWindows(ctx, processName)
	default:
		return h.checkProcessUnix(ctx, processName)
	}
}

// checkProcessWindows uses tasklist to find processes on Windows.
func (h *ObserveProcessHandler) checkProcessWindows(ctx context.Context, processName string) (bool, map[string]interface{}, error) {
	// Use tasklist with filter for specific process name
	cmd := exec.CommandContext(ctx, "tasklist", "/FI", fmt.Sprintf("IMAGENAME eq %s", processName), "/FO", "CSV", "/NH")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// tasklist returns error if no matching process found, which is fine
		h.logger.Debug().Err(err).Str("stderr", stderr.String()).Msg("tasklist command result")
	}

	output := stdout.String()
	h.logger.Debug().Str("output", output).Msg("tasklist output")

	// Check if process name appears in output
	// tasklist CSV format: "process.exe","PID","Session Name","Session#","Mem Usage"
	lines := strings.Split(output, "\n")
	processInfo := make(map[string]interface{})

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "No tasks") || strings.Contains(line, "INFO:") {
			continue
		}

		// Parse CSV line
		parts := strings.Split(line, ",")
		if len(parts) >= 2 {
			// Remove quotes from process name
			foundName := strings.Trim(parts[0], "\"")
			if strings.EqualFold(foundName, processName) {
				pid := strings.Trim(parts[1], "\"")
				processInfo["pid"] = pid
				processInfo["name"] = foundName
				if len(parts) >= 5 {
					processInfo["memory"] = strings.Trim(parts[4], "\"")
				}
				return true, processInfo, nil
			}
		}
	}

	return false, processInfo, nil
}

// checkProcessUnix uses ps/pgrep to find processes on Linux/Mac.
func (h *ObserveProcessHandler) checkProcessUnix(ctx context.Context, processName string) (bool, map[string]interface{}, error) {
	// Use pgrep for more reliable process matching
	cmd := exec.CommandContext(ctx, "pgrep", "-x", processName)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err := cmd.Run()
	processInfo := make(map[string]interface{})

	if err != nil {
		// pgrep returns exit code 1 if no process found
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, processInfo, nil
		}
		// Try alternative: ps aux | grep
		return h.checkProcessPsAux(ctx, processName)
	}

	// Process found - parse PID(s)
	pids := strings.TrimSpace(stdout.String())
	if pids != "" {
		pidList := strings.Split(pids, "\n")
		processInfo["pids"] = pidList
		processInfo["count"] = len(pidList)
		return true, processInfo, nil
	}

	return false, processInfo, nil
}

// checkProcessPsAux is a fallback using ps aux.
func (h *ObserveProcessHandler) checkProcessPsAux(ctx context.Context, processName string) (bool, map[string]interface{}, error) {
	cmd := exec.CommandContext(ctx, "ps", "aux")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return false, nil, fmt.Errorf("ps aux failed: %w", err)
	}

	processInfo := make(map[string]interface{})
	lines := strings.Split(stdout.String(), "\n")

	for _, line := range lines {
		if strings.Contains(line, processName) && !strings.Contains(line, "grep") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				processInfo["pid"] = fields[1]
				processInfo["user"] = fields[0]
				return true, processInfo, nil
			}
		}
	}

	return false, processInfo, nil
}
