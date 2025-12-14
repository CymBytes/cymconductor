// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"runtime"
	"time"

	"github.com/rs/zerolog"
)

// ProcessActivityHandler handles simulate_process_activity actions.
type ProcessActivityHandler struct {
	config ProcessActivityConfig
	logger zerolog.Logger
}

// NewProcessActivityHandler creates a new process activity handler.
func NewProcessActivityHandler(cfg ProcessActivityConfig, logger zerolog.Logger) *ProcessActivityHandler {
	return &ProcessActivityHandler{
		config: cfg,
		logger: logger.With().Str("action", "process_activity").Logger(),
	}
}

// Execute simulates process/application activity.
func (h *ProcessActivityHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting process activity simulation")

	// Parse parameters
	allowedProcesses, _ := getStringSlice(params, "allowed_processes")
	if len(allowedProcesses) == 0 {
		return nil, fmt.Errorf("allowed_processes parameter is required")
	}

	spawnCount, _ := getInt(params, "spawn_count")
	if spawnCount == 0 {
		spawnCount = 3
	}

	durationSec, _ := getInt(params, "duration_seconds")
	if durationSec == 0 {
		durationSec = 60
	}

	// Validate all processes are in the allowed list
	for _, proc := range allowedProcesses {
		if !h.isAllowedProcess(proc) {
			return nil, fmt.Errorf("process not in allowed list: %s", proc)
		}
	}

	// Track spawned processes
	processesSpawned := 0
	processesTerminated := 0
	runningProcesses := []*exec.Cmd{}

	// Spawn processes
spawnLoop:
	for i := 0; i < spawnCount; i++ {
		select {
		case <-ctx.Done():
			break spawnLoop
		default:
		}

		proc := allowedProcesses[rand.Intn(len(allowedProcesses))]
		cmd, err := h.spawnProcess(proc)
		if err != nil {
			h.logger.Warn().Err(err).Str("process", proc).Msg("Failed to spawn process")
			continue
		}

		processesSpawned++
		runningProcesses = append(runningProcesses, cmd)
		h.logger.Debug().Str("process", proc).Int("pid", cmd.Process.Pid).Msg("Process spawned")

		// Random delay between spawns
		time.Sleep(time.Duration(500+rand.Intn(2000)) * time.Millisecond)
	}

	// Wait for duration (processes run in background)
	select {
	case <-ctx.Done():
		h.logger.Info().Msg("Process activity cancelled")
	case <-time.After(time.Duration(durationSec) * time.Second):
		h.logger.Debug().Msg("Duration elapsed")
	}

	// Terminate all processes
	for _, cmd := range runningProcesses {
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil {
				h.logger.Warn().Err(err).Int("pid", cmd.Process.Pid).Msg("Failed to kill process")
			} else {
				processesTerminated++
				h.logger.Debug().Int("pid", cmd.Process.Pid).Msg("Process terminated")
			}
		}
	}

	duration := time.Since(startTime)
	h.logger.Info().
		Int("spawned", processesSpawned).
		Int("terminated", processesTerminated).
		Dur("duration", duration).
		Msg("Process activity simulation complete")

	return &Result{
		Data: map[string]interface{}{
			"processes_spawned":    processesSpawned,
			"processes_terminated": processesTerminated,
			"total_runtime_ms":     duration.Milliseconds(),
		},
		Summary:    fmt.Sprintf("Spawned %d processes, terminated %d", processesSpawned, processesTerminated),
		DurationMs: duration.Milliseconds(),
	}, nil
}

func (h *ProcessActivityHandler) isAllowedProcess(proc string) bool {
	for _, allowed := range h.config.AllowedProcesses {
		if allowed == proc {
			return true
		}
	}
	return false
}

func (h *ProcessActivityHandler) spawnProcess(proc string) (*exec.Cmd, error) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// On Windows, use start to launch processes
		cmd = exec.Command("cmd", "/C", "start", "", proc)
	default:
		// On Linux/Mac, launch directly
		cmd = exec.Command(proc)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	return cmd, nil
}
