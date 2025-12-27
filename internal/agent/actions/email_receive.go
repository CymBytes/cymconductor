// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// EmailBackend defines the interface for email retrieval.
type EmailBackend interface {
	// Connect establishes connection to the mail server.
	Connect(ctx context.Context, config *EmailBackendConfig) error

	// Disconnect closes the connection.
	Disconnect() error

	// ListEmails returns emails matching the filter.
	ListEmails(ctx context.Context, filter *EmailFilter) ([]*EmailHeader, error)

	// ReadEmail retrieves full email content.
	ReadEmail(ctx context.Context, messageID string) (*Email, error)

	// GetAttachment retrieves a specific attachment.
	GetAttachment(ctx context.Context, messageID, attachmentID string) (*Attachment, error)

	// Name returns the backend name for logging.
	Name() string
}

// EmailBackendConfig holds connection settings for email backends.
type EmailBackendConfig struct {
	// IMAP settings
	Server   string
	Port     int
	Username string
	Password string
	UseTLS   bool

	// For Outlook, this is ignored (uses current user profile)
}

// EmailFilter specifies search criteria for listing emails.
type EmailFilter struct {
	Folder        string    // "INBOX", "Sent", etc.
	Subject       string    // Substring match
	Sender        string    // Email address or partial match
	Since         time.Time // Emails after this date
	Before        time.Time // Emails before this date
	Unread        *bool     // Filter by read status
	HasAttachment *bool     // Filter for emails with attachments
	MaxResults    int       // Limit results
}

// EmailHeader contains summary info for listing.
type EmailHeader struct {
	MessageID     string
	Subject       string
	Sender        string
	ReceivedAt    time.Time
	HasAttachment bool
	IsRead        bool
}

// Email contains full email data.
type Email struct {
	EmailHeader
	Body        string
	BodyHTML    string
	Attachments []AttachmentInfo
}

// AttachmentInfo describes an attachment.
type AttachmentInfo struct {
	AttachmentID string
	Filename     string
	ContentType  string
	SizeBytes    int64
}

// Attachment contains actual attachment data.
type Attachment struct {
	AttachmentInfo
	Data []byte
}

// EmailReceiveHandler handles email_receive actions.
type EmailReceiveHandler struct {
	config         EmailReceiveConfig
	logger         zerolog.Logger
	imapBackend    EmailBackend
	outlookBackend EmailBackend
}

// NewEmailReceiveHandler creates a new email receive handler.
func NewEmailReceiveHandler(cfg EmailReceiveConfig, logger zerolog.Logger) *EmailReceiveHandler {
	h := &EmailReceiveHandler{
		config: cfg,
		logger: logger.With().Str("action", "email_receive").Logger(),
	}

	// IMAP backend always available
	h.imapBackend = NewIMAPBackend(logger)

	// Outlook backend only on Windows (returns nil on other platforms)
	h.outlookBackend = newOutlookBackend(cfg, logger)

	return h
}

// Execute handles the email_receive action.
func (h *EmailReceiveHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting email receive operation")

	// Parse common parameters
	operation, _ := getString(params, "operation")
	if operation == "" {
		operation = "list"
	}

	backendName, _ := getString(params, "backend")
	if backendName == "" {
		backendName = "auto"
	}

	// Select backend
	backend, err := h.selectBackend(backendName)
	if err != nil {
		return nil, err
	}

	// Build connection config from params (can override defaults)
	connConfig := h.buildConnectionConfig(params)

	// Connect
	if err := backend.Connect(ctx, connConfig); err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", backend.Name(), err)
	}
	defer backend.Disconnect()

	// Dispatch to operation handler
	var result map[string]interface{}
	var summary string

	switch operation {
	case "list":
		result, summary, err = h.handleList(ctx, backend, params)
	case "read":
		result, summary, err = h.handleRead(ctx, backend, params)
	case "extract":
		result, summary, err = h.handleExtract(ctx, backend, params)
	case "execute":
		result, summary, err = h.handleExecute(ctx, backend, params)
	default:
		return nil, fmt.Errorf("unknown operation: %s", operation)
	}

	if err != nil {
		return nil, err
	}

	result["operation"] = operation
	result["backend"] = backend.Name()

	duration := time.Since(startTime)
	h.logger.Info().
		Str("operation", operation).
		Str("backend", backend.Name()).
		Dur("duration", duration).
		Msg("Email receive operation complete")

	return &Result{
		Data:       result,
		Summary:    summary,
		DurationMs: duration.Milliseconds(),
	}, nil
}

func (h *EmailReceiveHandler) selectBackend(name string) (EmailBackend, error) {
	switch name {
	case "outlook":
		if h.outlookBackend == nil {
			return nil, fmt.Errorf("outlook backend not available (Windows only or not enabled)")
		}
		return h.outlookBackend, nil
	case "imap":
		return h.imapBackend, nil
	case "auto":
		// Prefer Outlook on Windows if available and enabled
		if runtime.GOOS == "windows" && h.outlookBackend != nil && h.config.OutlookEnabled {
			return h.outlookBackend, nil
		}
		return h.imapBackend, nil
	default:
		return nil, fmt.Errorf("unknown backend: %s", name)
	}
}

func (h *EmailReceiveHandler) buildConnectionConfig(params map[string]interface{}) *EmailBackendConfig {
	config := &EmailBackendConfig{
		Server:   h.config.IMAPServer,
		Port:     h.config.IMAPPort,
		Username: h.config.IMAPUsername,
		Password: h.config.IMAPPassword,
		UseTLS:   h.config.IMAPTLS,
	}

	// Allow params to override defaults
	if server, ok := getString(params, "server"); ok && server != "" {
		config.Server = server
	}
	if port, ok := getInt(params, "port"); ok && port > 0 {
		config.Port = port
	}
	if username, ok := getString(params, "username"); ok && username != "" {
		config.Username = username
	}
	if password, ok := getString(params, "password"); ok && password != "" {
		config.Password = password
	}
	if useTLS, ok := getBool(params, "use_tls"); ok {
		config.UseTLS = useTLS
	}

	return config
}

func (h *EmailReceiveHandler) buildFilter(params map[string]interface{}) *EmailFilter {
	filter := &EmailFilter{
		Folder:     "INBOX",
		MaxResults: 10,
	}

	if folder, ok := getString(params, "folder"); ok && folder != "" {
		filter.Folder = folder
	}
	if subject, ok := getString(params, "subject"); ok {
		filter.Subject = subject
	}
	if sender, ok := getString(params, "sender"); ok {
		filter.Sender = sender
	}
	if sinceStr, ok := getString(params, "since"); ok && sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			filter.Since = t
		}
	}
	if beforeStr, ok := getString(params, "before"); ok && beforeStr != "" {
		if t, err := time.Parse(time.RFC3339, beforeStr); err == nil {
			filter.Before = t
		}
	}
	if unread, ok := getBool(params, "unread"); ok {
		filter.Unread = &unread
	}
	if hasAttachment, ok := getBool(params, "has_attachment"); ok {
		filter.HasAttachment = &hasAttachment
	}
	if maxResults, ok := getInt(params, "max_results"); ok && maxResults > 0 {
		filter.MaxResults = maxResults
	}

	// Apply config limit
	if h.config.MaxEmailsPerQuery > 0 && filter.MaxResults > h.config.MaxEmailsPerQuery {
		filter.MaxResults = h.config.MaxEmailsPerQuery
	}

	return filter
}

func (h *EmailReceiveHandler) handleList(ctx context.Context, backend EmailBackend, params map[string]interface{}) (map[string]interface{}, string, error) {
	filter := h.buildFilter(params)

	emails, err := backend.ListEmails(ctx, filter)
	if err != nil {
		return nil, "", fmt.Errorf("list emails failed: %w", err)
	}

	h.logger.Info().Int("count", len(emails)).Msg("Listed emails")

	// Build email summaries for result
	emailSummaries := make([]map[string]interface{}, len(emails))
	for i, email := range emails {
		emailSummaries[i] = map[string]interface{}{
			"message_id":     email.MessageID,
			"subject":        email.Subject,
			"sender":         email.Sender,
			"received_at":    email.ReceivedAt.Format(time.RFC3339),
			"has_attachment": email.HasAttachment,
			"is_read":        email.IsRead,
		}
	}

	return map[string]interface{}{
			"emails_listed": len(emails),
			"emails":        emailSummaries,
		},
		fmt.Sprintf("Listed %d emails", len(emails)),
		nil
}

func (h *EmailReceiveHandler) handleRead(ctx context.Context, backend EmailBackend, params map[string]interface{}) (map[string]interface{}, string, error) {
	messageID, hasID := getString(params, "message_id")

	if !hasID || messageID == "" {
		// If no message_id, list and read first matching
		filter := h.buildFilter(params)
		filter.MaxResults = 1

		emails, err := backend.ListEmails(ctx, filter)
		if err != nil {
			return nil, "", fmt.Errorf("list emails failed: %w", err)
		}
		if len(emails) == 0 {
			return nil, "", fmt.Errorf("no emails match filter")
		}
		messageID = emails[0].MessageID
	}

	email, err := backend.ReadEmail(ctx, messageID)
	if err != nil {
		return nil, "", fmt.Errorf("read email failed: %w", err)
	}

	h.logger.Info().
		Str("subject", email.Subject).
		Int("attachments", len(email.Attachments)).
		Msg("Read email")

	// Build attachment info for result
	attachments := make([]map[string]interface{}, len(email.Attachments))
	for i, att := range email.Attachments {
		attachments[i] = map[string]interface{}{
			"attachment_id": att.AttachmentID,
			"filename":      att.Filename,
			"content_type":  att.ContentType,
			"size_bytes":    att.SizeBytes,
		}
	}

	return map[string]interface{}{
			"emails_read":       1,
			"message_id":        messageID,
			"subject":           email.Subject,
			"sender":            email.Sender,
			"attachments_found": len(email.Attachments),
			"attachments":       attachments,
		},
		fmt.Sprintf("Read email: %s (%d attachments)", email.Subject, len(email.Attachments)),
		nil
}

func (h *EmailReceiveHandler) handleExtract(ctx context.Context, backend EmailBackend, params map[string]interface{}) (map[string]interface{}, string, error) {
	// Get target directory
	targetDir, _ := getString(params, "save_directory")
	if targetDir == "" {
		return nil, "", fmt.Errorf("save_directory is required for extract operation")
	}

	// Security check
	if !h.isAllowedDirectory(targetDir) {
		return nil, "", fmt.Errorf("directory not allowed: %s", targetDir)
	}

	// Read email first
	readResult, _, err := h.handleRead(ctx, backend, params)
	if err != nil {
		return nil, "", err
	}

	messageID := readResult["message_id"].(string)
	email, err := backend.ReadEmail(ctx, messageID)
	if err != nil {
		return nil, "", fmt.Errorf("read email for extraction failed: %w", err)
	}

	// Ensure target directory exists
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create target directory: %w", err)
	}

	savedFiles := []string{}
	for _, attInfo := range email.Attachments {
		// Security check: file extension
		if !h.isAllowedAttachment(attInfo.Filename) {
			h.logger.Warn().Str("file", attInfo.Filename).Msg("Attachment type not allowed, skipping")
			continue
		}

		// Security check: file size
		if !h.isWithinSizeLimit(attInfo.SizeBytes) {
			h.logger.Warn().
				Str("file", attInfo.Filename).
				Int64("size", attInfo.SizeBytes).
				Msg("Attachment exceeds size limit, skipping")
			continue
		}

		att, err := backend.GetAttachment(ctx, messageID, attInfo.AttachmentID)
		if err != nil {
			h.logger.Warn().Err(err).Str("file", attInfo.Filename).Msg("Failed to get attachment")
			continue
		}

		savePath := filepath.Join(targetDir, att.Filename)
		if err := os.WriteFile(savePath, att.Data, 0644); err != nil {
			h.logger.Warn().Err(err).Str("path", savePath).Msg("Failed to save attachment")
			continue
		}

		savedFiles = append(savedFiles, savePath)
		h.logger.Info().Str("path", savePath).Int64("size", att.SizeBytes).Msg("Saved attachment")
	}

	// Merge read result with extract result
	readResult["attachments_saved"] = len(savedFiles)
	readResult["saved_files"] = savedFiles

	return readResult,
		fmt.Sprintf("Extracted %d attachments to %s", len(savedFiles), targetDir),
		nil
}

func (h *EmailReceiveHandler) handleExecute(ctx context.Context, backend EmailBackend, params map[string]interface{}) (map[string]interface{}, string, error) {
	// Check if execution is allowed
	if !h.config.AllowExecution {
		return nil, "", fmt.Errorf("attachment execution is disabled in configuration")
	}

	// Extract first
	extractResult, _, err := h.handleExtract(ctx, backend, params)
	if err != nil {
		return nil, "", err
	}

	savedFiles, ok := extractResult["saved_files"].([]string)
	if !ok || len(savedFiles) == 0 {
		extractResult["attachments_executed"] = 0
		extractResult["executed_files"] = []string{}
		extractResult["process_ids"] = []int{}
		return extractResult, "No attachments to execute", nil
	}

	executedFiles := []string{}
	processIDs := []int{}

	for _, filePath := range savedFiles {
		// Security check: is file allowed to be executed
		if !h.isAllowedExecutable(filePath) {
			h.logger.Warn().Str("file", filePath).Msg("File not allowed for execution, skipping")
			continue
		}

		pid, err := h.executeFile(ctx, filePath)
		if err != nil {
			h.logger.Warn().Err(err).Str("file", filePath).Msg("Failed to execute")
			continue
		}

		executedFiles = append(executedFiles, filePath)
		processIDs = append(processIDs, pid)
		h.logger.Info().Str("file", filePath).Int("pid", pid).Msg("Executed attachment")
	}

	extractResult["attachments_executed"] = len(executedFiles)
	extractResult["executed_files"] = executedFiles
	extractResult["process_ids"] = processIDs

	return extractResult,
		fmt.Sprintf("Executed %d attachments", len(executedFiles)),
		nil
}

// Security validation functions

func (h *EmailReceiveHandler) isAllowedDirectory(dir string) bool {
	// If no directories configured, deny all
	if len(h.config.AllowedSaveDirectories) == 0 {
		return false
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}

	for _, allowed := range h.config.AllowedSaveDirectories {
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

func (h *EmailReceiveHandler) isAllowedAttachment(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))

	// Check blocked list first
	for _, blocked := range h.config.BlockedFileExtensions {
		if strings.ToLower(blocked) == ext || "."+strings.ToLower(blocked) == ext {
			return false
		}
	}

	// If allowed list is specified, check it
	if len(h.config.AllowedFileExtensions) > 0 {
		for _, allowed := range h.config.AllowedFileExtensions {
			if strings.ToLower(allowed) == ext || "."+strings.ToLower(allowed) == ext {
				return true
			}
		}
		return false
	}

	return true
}

func (h *EmailReceiveHandler) isWithinSizeLimit(sizeBytes int64) bool {
	if h.config.MaxAttachmentSizeMB <= 0 {
		return true
	}
	maxBytes := int64(h.config.MaxAttachmentSizeMB) * 1024 * 1024
	return sizeBytes <= maxBytes
}

func (h *EmailReceiveHandler) isAllowedExecutable(filePath string) bool {
	if !h.config.AllowExecution {
		return false
	}

	filename := filepath.Base(filePath)

	// If allowed executables list is specified, check it
	if len(h.config.AllowedExecutables) > 0 {
		for _, allowed := range h.config.AllowedExecutables {
			if strings.EqualFold(allowed, filename) {
				return true
			}
			// Support wildcards like "*.exe"
			if matched, _ := filepath.Match(strings.ToLower(allowed), strings.ToLower(filename)); matched {
				return true
			}
		}
		return false
	}

	return true
}

func (h *EmailReceiveHandler) executeFile(ctx context.Context, filePath string) (int, error) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// Use 'start' to open file with default application
		// This creates realistic process telemetry (explorer.exe spawning app)
		cmd = exec.CommandContext(ctx, "cmd", "/C", "start", "", filePath)
	default:
		// On Linux/Mac, use xdg-open or direct execution
		ext := strings.ToLower(filepath.Ext(filePath))
		if ext == ".sh" || ext == "" {
			cmd = exec.CommandContext(ctx, "/bin/sh", filePath)
		} else {
			cmd = exec.CommandContext(ctx, "xdg-open", filePath)
		}
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to start: %w", err)
	}

	// Don't wait - let the process run independently
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	return pid, nil
}
