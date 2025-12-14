// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"crypto/rand"
	"fmt"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// FileActivityHandler handles simulate_file_activity actions.
type FileActivityHandler struct {
	config FileActivityConfig
	logger zerolog.Logger
}

// NewFileActivityHandler creates a new file activity handler.
func NewFileActivityHandler(cfg FileActivityConfig, logger zerolog.Logger) *FileActivityHandler {
	return &FileActivityHandler{
		config: cfg,
		logger: logger.With().Str("action", "file_activity").Logger(),
	}
}

// Execute simulates file system activity.
func (h *FileActivityHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting file activity simulation")

	// Parse parameters
	targetDir, _ := getString(params, "target_directory")
	if targetDir == "" {
		return nil, fmt.Errorf("target_directory parameter is required")
	}

	// Security check: ensure directory is in allowed list
	if !h.isAllowedDirectory(targetDir) {
		return nil, fmt.Errorf("directory not in allowed list: %s", targetDir)
	}

	operations, _ := getStringSlice(params, "operations")
	if len(operations) == 0 {
		operations = []string{"create", "read"}
	}

	fileCount, _ := getInt(params, "file_count")
	if fileCount == 0 {
		fileCount = 5
	}

	fileTypes, _ := getStringSlice(params, "file_types")
	if len(fileTypes) == 0 {
		fileTypes = []string{"txt"}
	}

	fileSizeMin, _ := getInt(params, "file_size_kb_min")
	if fileSizeMin == 0 {
		fileSizeMin = 1
	}

	fileSizeMax, _ := getInt(params, "file_size_kb_max")
	if fileSizeMax == 0 {
		fileSizeMax = 10
	}

	preserveFiles, _ := getBool(params, "preserve_files")

	// Ensure target directory exists
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target directory: %w", err)
	}

	// Track operations
	filesCreated := 0
	filesModified := 0
	filesRead := 0
	filesDeleted := 0
	filesRenamed := 0
	createdFiles := []string{}

	// Perform operations
fileLoop:
	for i := 0; i < fileCount; i++ {
		select {
		case <-ctx.Done():
			break fileLoop
		default:
		}

		op := operations[mathrand.Intn(len(operations))]
		ext := fileTypes[mathrand.Intn(len(fileTypes))]
		filename := fmt.Sprintf("cymbytes_sim_%d_%d.%s", time.Now().UnixNano(), i, ext)
		filePath := filepath.Join(targetDir, filename)

		switch op {
		case "create":
			size := (fileSizeMin + mathrand.Intn(fileSizeMax-fileSizeMin+1)) * 1024
			if err := h.createFile(filePath, size); err != nil {
				h.logger.Warn().Err(err).Str("file", filePath).Msg("Failed to create file")
			} else {
				filesCreated++
				createdFiles = append(createdFiles, filePath)
			}

		case "modify":
			// Modify an existing file or create if none exist
			if len(createdFiles) > 0 {
				targetFile := createdFiles[mathrand.Intn(len(createdFiles))]
				if err := h.modifyFile(targetFile); err != nil {
					h.logger.Warn().Err(err).Str("file", targetFile).Msg("Failed to modify file")
				} else {
					filesModified++
				}
			} else {
				// Create a file instead
				size := (fileSizeMin + mathrand.Intn(fileSizeMax-fileSizeMin+1)) * 1024
				if err := h.createFile(filePath, size); err == nil {
					filesCreated++
					createdFiles = append(createdFiles, filePath)
				}
			}

		case "read":
			if len(createdFiles) > 0 {
				targetFile := createdFiles[mathrand.Intn(len(createdFiles))]
				if err := h.readFile(targetFile); err != nil {
					h.logger.Warn().Err(err).Str("file", targetFile).Msg("Failed to read file")
				} else {
					filesRead++
				}
			}

		case "delete":
			if len(createdFiles) > 0 && !preserveFiles {
				idx := mathrand.Intn(len(createdFiles))
				targetFile := createdFiles[idx]
				if err := os.Remove(targetFile); err != nil {
					h.logger.Warn().Err(err).Str("file", targetFile).Msg("Failed to delete file")
				} else {
					filesDeleted++
					// Remove from tracking
					createdFiles = append(createdFiles[:idx], createdFiles[idx+1:]...)
				}
			}

		case "rename":
			if len(createdFiles) > 0 {
				idx := mathrand.Intn(len(createdFiles))
				oldPath := createdFiles[idx]
				newFilename := fmt.Sprintf("cymbytes_renamed_%d.%s", time.Now().UnixNano(), ext)
				newPath := filepath.Join(targetDir, newFilename)
				if err := os.Rename(oldPath, newPath); err != nil {
					h.logger.Warn().Err(err).Str("old", oldPath).Str("new", newPath).Msg("Failed to rename file")
				} else {
					filesRenamed++
					createdFiles[idx] = newPath
				}
			}
		}

		// Random pause between operations
		time.Sleep(time.Duration(100+mathrand.Intn(500)) * time.Millisecond)
	}

	// Cleanup created files if not preserving
	if !preserveFiles {
		for _, f := range createdFiles {
			os.Remove(f)
			filesDeleted++
		}
	}

	duration := time.Since(startTime)
	h.logger.Info().
		Int("created", filesCreated).
		Int("modified", filesModified).
		Int("read", filesRead).
		Int("deleted", filesDeleted).
		Int("renamed", filesRenamed).
		Dur("duration", duration).
		Msg("File activity simulation complete")

	return &Result{
		Data: map[string]interface{}{
			"files_created":  filesCreated,
			"files_modified": filesModified,
			"files_read":     filesRead,
			"files_deleted":  filesDeleted,
			"files_renamed":  filesRenamed,
		},
		Summary:    fmt.Sprintf("Created %d, modified %d, read %d, deleted %d, renamed %d files", filesCreated, filesModified, filesRead, filesDeleted, filesRenamed),
		DurationMs: duration.Milliseconds(),
	}, nil
}

func (h *FileActivityHandler) isAllowedDirectory(dir string) bool {
	// Normalize the path
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}

	for _, allowed := range h.config.AllowedDirectories {
		absAllowed, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		if strings.HasPrefix(absDir, absAllowed) {
			return true
		}
	}

	return false
}

func (h *FileActivityHandler) createFile(path string, size int) error {
	// Generate random content
	content := make([]byte, size)
	if _, err := rand.Read(content); err != nil {
		return fmt.Errorf("failed to generate random content: %w", err)
	}

	return os.WriteFile(path, content, 0644)
}

func (h *FileActivityHandler) modifyFile(path string) error {
	// Read existing content
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Append some random data
	extra := make([]byte, 100+mathrand.Intn(400))
	if _, err := rand.Read(extra); err != nil {
		return fmt.Errorf("failed to generate random data: %w", err)
	}
	content = append(content, extra...)

	return os.WriteFile(path, content, 0644)
}

func (h *FileActivityHandler) readFile(path string) error {
	_, err := os.ReadFile(path)
	return err
}
