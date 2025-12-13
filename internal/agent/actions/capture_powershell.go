// Package actions provides predefined action implementations for the agent.
package actions

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// CapturePowerShellHandler handles capture_powershell_history actions.
// It reads PowerShell command history to detect response actions.
type CapturePowerShellHandler struct {
	logger zerolog.Logger
}

// NewCapturePowerShellHandler creates a new capture PowerShell history handler.
func NewCapturePowerShellHandler(logger zerolog.Logger) *CapturePowerShellHandler {
	return &CapturePowerShellHandler{
		logger: logger.With().Str("action", "capture_powershell_history").Logger(),
	}
}

// CapturedCommand represents a command found in history.
type CapturedCommand struct {
	Command   string `json:"command"`
	User      string `json:"user"`
	Timestamp string `json:"timestamp,omitempty"`
	LineNum   int    `json:"line_number"`
}

// Execute captures PowerShell command history.
// Parameters:
//   - command_pattern: Regex pattern to match commands (e.g., "Stop-Process|taskkill|Disable-ADAccount")
//   - scope: "current_user" or "all_users" (default: "all_users")
//   - max_lines: Maximum number of history lines to read per file (default: 1000)
//   - since_minutes: Only return commands from last N minutes (optional)
func (h *CapturePowerShellHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting PowerShell history capture")

	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("capture_powershell_history is only supported on Windows")
	}

	// Parse parameters
	commandPattern, _ := getString(params, "command_pattern")
	if commandPattern == "" {
		// Default pattern matches common response actions
		commandPattern = "(?i)(Stop-Process|taskkill|Kill|Disable-ADAccount|Set-ADUser|Remove-Item|Block-|Isolate-|Quarantine)"
	}

	scope, _ := getString(params, "scope")
	if scope == "" {
		scope = "all_users"
	}

	maxLines, _ := getInt(params, "max_lines")
	if maxLines == 0 {
		maxLines = 1000
	}

	// Compile regex pattern
	pattern, err := regexp.Compile(commandPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid command_pattern regex: %w", err)
	}

	// Find history files
	historyFiles, err := h.findHistoryFiles(scope)
	if err != nil {
		return nil, fmt.Errorf("failed to find history files: %w", err)
	}

	h.logger.Debug().Int("file_count", len(historyFiles)).Msg("Found history files")

	// Read and search history files
	var matchedCommands []CapturedCommand
	filesProcessed := 0
	totalLinesRead := 0

	for _, hf := range historyFiles {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		commands, linesRead, err := h.searchHistoryFile(hf.Path, hf.User, pattern, maxLines)
		if err != nil {
			h.logger.Warn().Err(err).Str("file", hf.Path).Msg("Failed to read history file")
			continue
		}

		matchedCommands = append(matchedCommands, commands...)
		filesProcessed++
		totalLinesRead += linesRead
	}

	duration := time.Since(startTime)
	matchCount := len(matchedCommands)
	hasMatches := matchCount > 0

	h.logger.Info().
		Int("files_processed", filesProcessed).
		Int("lines_read", totalLinesRead).
		Int("matches_found", matchCount).
		Dur("duration", duration).
		Msg("PowerShell history capture complete")

	return &Result{
		Data: map[string]interface{}{
			"commands_found":   matchedCommands,
			"match_count":      matchCount,
			"pattern":          commandPattern,
			"scope":            scope,
			"files_processed":  filesProcessed,
			"total_lines_read": totalLinesRead,
			"has_matches":      hasMatches,
			"checked_at":       time.Now().UTC().Format(time.RFC3339),
		},
		Summary:    fmt.Sprintf("Found %d matching commands in %d history files", matchCount, filesProcessed),
		DurationMs: duration.Milliseconds(),
	}, nil
}

// HistoryFile represents a PowerShell history file.
type HistoryFile struct {
	Path string
	User string
}

// findHistoryFiles locates PowerShell history files.
func (h *CapturePowerShellHandler) findHistoryFiles(scope string) ([]HistoryFile, error) {
	var files []HistoryFile

	if scope == "current_user" {
		// Just current user's history
		userProfile := os.Getenv("USERPROFILE")
		if userProfile == "" {
			return nil, fmt.Errorf("USERPROFILE environment variable not set")
		}

		historyPath := filepath.Join(userProfile, "AppData", "Roaming", "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt")
		if _, err := os.Stat(historyPath); err == nil {
			username := filepath.Base(userProfile)
			files = append(files, HistoryFile{Path: historyPath, User: username})
		}
	} else {
		// All users
		usersDir := "C:\\Users"
		entries, err := os.ReadDir(usersDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read Users directory: %w", err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			// Skip system folders
			name := entry.Name()
			if name == "Public" || name == "Default" || name == "Default User" || name == "All Users" {
				continue
			}

			historyPath := filepath.Join(usersDir, name, "AppData", "Roaming", "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt")
			if _, err := os.Stat(historyPath); err == nil {
				files = append(files, HistoryFile{Path: historyPath, User: name})
			}
		}
	}

	return files, nil
}

// searchHistoryFile reads a history file and returns matching commands.
func (h *CapturePowerShellHandler) searchHistoryFile(path, user string, pattern *regexp.Regexp, maxLines int) ([]CapturedCommand, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	var commands []CapturedCommand
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() && lineNum < maxLines {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		if pattern.MatchString(line) {
			commands = append(commands, CapturedCommand{
				Command: line,
				User:    user,
				LineNum: lineNum,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return commands, lineNum, fmt.Errorf("error reading file: %w", err)
	}

	return commands, lineNum, nil
}
