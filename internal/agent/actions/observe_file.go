// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// ObserveFileHandler handles observe_file_state actions.
// It checks whether a file exists, has been deleted, or matches expected properties.
type ObserveFileHandler struct {
	logger zerolog.Logger
}

// NewObserveFileHandler creates a new observe file state handler.
func NewObserveFileHandler(logger zerolog.Logger) *ObserveFileHandler {
	return &ObserveFileHandler{
		logger: logger.With().Str("action", "observe_file_state").Logger(),
	}
}

// Execute checks the state of a file.
// Parameters:
//   - file_path: Full path to the file to check
//   - expected_state: Expected state - "exists", "deleted", or "modified"
//   - check_hash: Whether to compute and return file hash (default: false)
//   - expected_hash: Expected SHA256 hash if checking for specific content
func (h *ObserveFileHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting file state observation")

	// Parse parameters
	filePath, ok := getString(params, "file_path")
	if !ok || filePath == "" {
		return nil, fmt.Errorf("file_path parameter is required")
	}

	expectedState, _ := getString(params, "expected_state")
	if expectedState == "" {
		expectedState = "deleted" // Default: expect file to be removed
	}

	if expectedState != "exists" && expectedState != "deleted" && expectedState != "modified" {
		return nil, fmt.Errorf("expected_state must be 'exists', 'deleted', or 'modified'")
	}

	checkHash, _ := getBool(params, "check_hash")
	expectedHash, _ := getString(params, "expected_hash")

	// Check file state
	fileInfo, err := os.Stat(filePath)
	exists := err == nil
	isDir := exists && fileInfo.IsDir()

	fileDetails := make(map[string]interface{})
	actualState := "deleted"

	if exists {
		actualState = "exists"
		fileDetails["size_bytes"] = fileInfo.Size()
		fileDetails["modified_at"] = fileInfo.ModTime().UTC().Format(time.RFC3339)
		fileDetails["is_directory"] = isDir
		fileDetails["mode"] = fileInfo.Mode().String()

		// Compute hash if requested and it's a file (not directory)
		if (checkHash || expectedHash != "") && !isDir {
			hash, hashErr := h.computeFileHash(filePath)
			if hashErr != nil {
				h.logger.Warn().Err(hashErr).Msg("Failed to compute file hash")
			} else {
				fileDetails["sha256"] = hash

				// If we have an expected hash, check if file was modified
				if expectedHash != "" && hash != expectedHash {
					actualState = "modified"
				}
			}
		}
	}

	stateMatches := actualState == expectedState

	// Special handling for "modified" check
	if expectedState == "modified" && !exists {
		stateMatches = false // Can't be modified if it doesn't exist
	}

	duration := time.Since(startTime)
	h.logger.Info().
		Str("file_path", filePath).
		Bool("exists", exists).
		Str("expected_state", expectedState).
		Str("actual_state", actualState).
		Bool("state_matches", stateMatches).
		Dur("duration", duration).
		Msg("File state observation complete")

	return &Result{
		Data: map[string]interface{}{
			"file_path":      filePath,
			"exists":         exists,
			"state":          actualState,
			"expected_state": expectedState,
			"state_matches":  stateMatches,
			"file_info":      fileDetails,
			"checked_at":     time.Now().UTC().Format(time.RFC3339),
		},
		Summary:    fmt.Sprintf("File '%s' state is %s (expected: %s)", filePath, actualState, expectedState),
		DurationMs: duration.Milliseconds(),
	}, nil
}

// computeFileHash computes the SHA256 hash of a file.
func (h *ObserveFileHandler) computeFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
