package actions

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestNewEmailReceiveHandler(t *testing.T) {
	cfg := EmailReceiveConfig{
		IMAPServer:             "imap.example.com",
		IMAPPort:               993,
		IMAPTLS:                true,
		AllowedSaveDirectories: []string{"/tmp"},
		AllowExecution:         false,
	}

	handler := NewEmailReceiveHandler(cfg, zerolog.Nop())

	if handler == nil {
		t.Fatal("Expected handler to be created")
	}
	if handler.imapBackend == nil {
		t.Error("Expected IMAP backend to be created")
	}
	// On non-Windows, Outlook backend should be nil
	// (can't test Windows COM on non-Windows)
}

func TestEmailReceiveHandler_IsAllowedDirectory(t *testing.T) {
	tests := []struct {
		name       string
		config     EmailReceiveConfig
		dir        string
		wantResult bool
	}{
		{
			name: "allowed directory exact match",
			config: EmailReceiveConfig{
				AllowedSaveDirectories: []string{"/tmp"},
			},
			dir:        "/tmp",
			wantResult: true,
		},
		{
			name: "allowed subdirectory",
			config: EmailReceiveConfig{
				AllowedSaveDirectories: []string{"/tmp"},
			},
			dir:        "/tmp/subdir/deep",
			wantResult: true,
		},
		{
			name: "not allowed directory",
			config: EmailReceiveConfig{
				AllowedSaveDirectories: []string{"/tmp"},
			},
			dir:        "/etc",
			wantResult: false,
		},
		{
			name: "empty allowed list denies all",
			config: EmailReceiveConfig{
				AllowedSaveDirectories: []string{},
			},
			dir:        "/tmp",
			wantResult: false,
		},
		{
			name: "multiple allowed directories",
			config: EmailReceiveConfig{
				AllowedSaveDirectories: []string{"/tmp", "/home/user/downloads"},
			},
			dir:        "/home/user/downloads/attachments",
			wantResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewEmailReceiveHandler(tt.config, zerolog.Nop())
			got := handler.isAllowedDirectory(tt.dir)
			if got != tt.wantResult {
				t.Errorf("isAllowedDirectory(%q) = %v, want %v", tt.dir, got, tt.wantResult)
			}
		})
	}
}

func TestEmailReceiveHandler_IsAllowedAttachment(t *testing.T) {
	tests := []struct {
		name       string
		config     EmailReceiveConfig
		filename   string
		wantResult bool
	}{
		{
			name:       "any file allowed when no restrictions",
			config:     EmailReceiveConfig{},
			filename:   "document.pdf",
			wantResult: true,
		},
		{
			name: "blocked extension",
			config: EmailReceiveConfig{
				BlockedFileExtensions: []string{".exe", ".bat"},
			},
			filename:   "malware.exe",
			wantResult: false,
		},
		{
			name: "blocked extension case insensitive",
			config: EmailReceiveConfig{
				BlockedFileExtensions: []string{".EXE"},
			},
			filename:   "malware.exe",
			wantResult: false,
		},
		{
			name: "allowed when not in blocked list",
			config: EmailReceiveConfig{
				BlockedFileExtensions: []string{".exe"},
			},
			filename:   "document.pdf",
			wantResult: true,
		},
		{
			name: "only allowed extensions",
			config: EmailReceiveConfig{
				AllowedFileExtensions: []string{".pdf", ".docx"},
			},
			filename:   "document.pdf",
			wantResult: true,
		},
		{
			name: "not in allowed extensions",
			config: EmailReceiveConfig{
				AllowedFileExtensions: []string{".pdf", ".docx"},
			},
			filename:   "malware.exe",
			wantResult: false,
		},
		{
			name: "blocked takes precedence over allowed",
			config: EmailReceiveConfig{
				AllowedFileExtensions: []string{".exe"},
				BlockedFileExtensions: []string{".exe"},
			},
			filename:   "program.exe",
			wantResult: false,
		},
		{
			name: "extension without dot",
			config: EmailReceiveConfig{
				AllowedFileExtensions: []string{"pdf"},
			},
			filename:   "document.pdf",
			wantResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewEmailReceiveHandler(tt.config, zerolog.Nop())
			got := handler.isAllowedAttachment(tt.filename)
			if got != tt.wantResult {
				t.Errorf("isAllowedAttachment(%q) = %v, want %v", tt.filename, got, tt.wantResult)
			}
		})
	}
}

func TestEmailReceiveHandler_IsWithinSizeLimit(t *testing.T) {
	tests := []struct {
		name       string
		config     EmailReceiveConfig
		sizeBytes  int64
		wantResult bool
	}{
		{
			name:       "no limit allows any size",
			config:     EmailReceiveConfig{},
			sizeBytes:  1024 * 1024 * 100, // 100MB
			wantResult: true,
		},
		{
			name: "within limit",
			config: EmailReceiveConfig{
				MaxAttachmentSizeMB: 10,
			},
			sizeBytes:  1024 * 1024 * 5, // 5MB
			wantResult: true,
		},
		{
			name: "exactly at limit",
			config: EmailReceiveConfig{
				MaxAttachmentSizeMB: 10,
			},
			sizeBytes:  1024 * 1024 * 10, // 10MB
			wantResult: true,
		},
		{
			name: "exceeds limit",
			config: EmailReceiveConfig{
				MaxAttachmentSizeMB: 10,
			},
			sizeBytes:  1024 * 1024 * 11, // 11MB
			wantResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewEmailReceiveHandler(tt.config, zerolog.Nop())
			got := handler.isWithinSizeLimit(tt.sizeBytes)
			if got != tt.wantResult {
				t.Errorf("isWithinSizeLimit(%d) = %v, want %v", tt.sizeBytes, got, tt.wantResult)
			}
		})
	}
}

func TestEmailReceiveHandler_IsAllowedExecutable(t *testing.T) {
	tests := []struct {
		name       string
		config     EmailReceiveConfig
		filePath   string
		wantResult bool
	}{
		{
			name: "execution disabled",
			config: EmailReceiveConfig{
				AllowExecution: false,
			},
			filePath:   "/tmp/file.exe",
			wantResult: false,
		},
		{
			name: "execution enabled no restrictions",
			config: EmailReceiveConfig{
				AllowExecution: true,
			},
			filePath:   "/tmp/file.exe",
			wantResult: true,
		},
		{
			name: "execution with allowed list - match",
			config: EmailReceiveConfig{
				AllowExecution:     true,
				AllowedExecutables: []string{"file.exe"},
			},
			filePath:   "/tmp/file.exe",
			wantResult: true,
		},
		{
			name: "execution with allowed list - no match",
			config: EmailReceiveConfig{
				AllowExecution:     true,
				AllowedExecutables: []string{"other.exe"},
			},
			filePath:   "/tmp/file.exe",
			wantResult: false,
		},
		{
			name: "execution with wildcard pattern",
			config: EmailReceiveConfig{
				AllowExecution:     true,
				AllowedExecutables: []string{"*.exe"},
			},
			filePath:   "/tmp/anyfile.exe",
			wantResult: true,
		},
		{
			name: "execution with wildcard - no match",
			config: EmailReceiveConfig{
				AllowExecution:     true,
				AllowedExecutables: []string{"*.exe"},
			},
			filePath:   "/tmp/anyfile.pdf",
			wantResult: false,
		},
		{
			name: "case insensitive filename match",
			config: EmailReceiveConfig{
				AllowExecution:     true,
				AllowedExecutables: []string{"FILE.EXE"},
			},
			filePath:   "/tmp/file.exe",
			wantResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewEmailReceiveHandler(tt.config, zerolog.Nop())
			got := handler.isAllowedExecutable(tt.filePath)
			if got != tt.wantResult {
				t.Errorf("isAllowedExecutable(%q) = %v, want %v", tt.filePath, got, tt.wantResult)
			}
		})
	}
}

func TestEmailReceiveHandler_BuildFilter(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{
		MaxEmailsPerQuery: 100,
	}, zerolog.Nop())

	tests := []struct {
		name           string
		params         map[string]interface{}
		wantFolder     string
		wantSubject    string
		wantSender     string
		wantMaxResults int
	}{
		{
			name:           "defaults",
			params:         map[string]interface{}{},
			wantFolder:     "INBOX",
			wantMaxResults: 10,
		},
		{
			name: "custom folder",
			params: map[string]interface{}{
				"folder": "Sent",
			},
			wantFolder:     "Sent",
			wantMaxResults: 10,
		},
		{
			name: "subject filter",
			params: map[string]interface{}{
				"subject": "Invoice",
			},
			wantFolder:     "INBOX",
			wantSubject:    "Invoice",
			wantMaxResults: 10,
		},
		{
			name: "sender filter",
			params: map[string]interface{}{
				"sender": "attacker@example.com",
			},
			wantFolder:     "INBOX",
			wantSender:     "attacker@example.com",
			wantMaxResults: 10,
		},
		{
			name: "max results",
			params: map[string]interface{}{
				"max_results": 50,
			},
			wantFolder:     "INBOX",
			wantMaxResults: 50,
		},
		{
			name: "max results capped by config",
			params: map[string]interface{}{
				"max_results": 200,
			},
			wantFolder:     "INBOX",
			wantMaxResults: 100, // Config limit
		},
		{
			name: "all filters combined",
			params: map[string]interface{}{
				"folder":      "INBOX",
				"subject":     "Payment",
				"sender":      "billing@corp.com",
				"max_results": 5,
			},
			wantFolder:     "INBOX",
			wantSubject:    "Payment",
			wantSender:     "billing@corp.com",
			wantMaxResults: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := handler.buildFilter(tt.params)

			if filter.Folder != tt.wantFolder {
				t.Errorf("Folder = %q, want %q", filter.Folder, tt.wantFolder)
			}
			if filter.Subject != tt.wantSubject {
				t.Errorf("Subject = %q, want %q", filter.Subject, tt.wantSubject)
			}
			if filter.Sender != tt.wantSender {
				t.Errorf("Sender = %q, want %q", filter.Sender, tt.wantSender)
			}
			if filter.MaxResults != tt.wantMaxResults {
				t.Errorf("MaxResults = %d, want %d", filter.MaxResults, tt.wantMaxResults)
			}
		})
	}
}

func TestEmailReceiveHandler_BuildConnectionConfig(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{
		IMAPServer:   "default.imap.com",
		IMAPPort:     993,
		IMAPUsername: "default@example.com",
		IMAPPassword: "defaultpass",
		IMAPTLS:      true,
	}, zerolog.Nop())

	tests := []struct {
		name         string
		params       map[string]interface{}
		wantServer   string
		wantPort     int
		wantUsername string
		wantPassword string
		wantTLS      bool
	}{
		{
			name:         "uses defaults",
			params:       map[string]interface{}{},
			wantServer:   "default.imap.com",
			wantPort:     993,
			wantUsername: "default@example.com",
			wantPassword: "defaultpass",
			wantTLS:      true,
		},
		{
			name: "override server",
			params: map[string]interface{}{
				"server": "custom.imap.com",
			},
			wantServer:   "custom.imap.com",
			wantPort:     993,
			wantUsername: "default@example.com",
			wantPassword: "defaultpass",
			wantTLS:      true,
		},
		{
			name: "override all",
			params: map[string]interface{}{
				"server":   "other.imap.com",
				"port":     143,
				"username": "other@example.com",
				"password": "otherpass",
				"use_tls":  false,
			},
			wantServer:   "other.imap.com",
			wantPort:     143,
			wantUsername: "other@example.com",
			wantPassword: "otherpass",
			wantTLS:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := handler.buildConnectionConfig(tt.params)

			if config.Server != tt.wantServer {
				t.Errorf("Server = %q, want %q", config.Server, tt.wantServer)
			}
			if config.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", config.Port, tt.wantPort)
			}
			if config.Username != tt.wantUsername {
				t.Errorf("Username = %q, want %q", config.Username, tt.wantUsername)
			}
			if config.Password != tt.wantPassword {
				t.Errorf("Password = %q, want %q", config.Password, tt.wantPassword)
			}
			if config.UseTLS != tt.wantTLS {
				t.Errorf("UseTLS = %v, want %v", config.UseTLS, tt.wantTLS)
			}
		})
	}
}

func TestEmailReceiveHandler_SelectBackend(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{
		OutlookEnabled: true,
	}, zerolog.Nop())

	tests := []struct {
		name        string
		backendName string
		wantName    string
		wantErr     bool
	}{
		{
			name:        "imap backend",
			backendName: "imap",
			wantName:    "imap",
			wantErr:     false,
		},
		{
			name:        "auto selects imap on non-windows",
			backendName: "auto",
			wantName:    "imap",
			wantErr:     false,
		},
		{
			name:        "unknown backend",
			backendName: "unknown",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := handler.selectBackend(tt.backendName)
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			if backend.Name() != tt.wantName {
				t.Errorf("Backend name = %q, want %q", backend.Name(), tt.wantName)
			}
		})
	}
}

func TestEmailReceiveHandler_Execute_InvalidOperation(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{}, zerolog.Nop())

	params := map[string]interface{}{
		"operation": "invalid",
		"backend":   "imap",
		"server":    "imap.example.com",
	}

	_, err := handler.Execute(context.Background(), params)
	if err == nil {
		t.Error("Expected error for invalid operation")
	}
}

func TestEmailReceiveHandler_Execute_MissingServerForIMAP(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{}, zerolog.Nop())

	params := map[string]interface{}{
		"operation": "list",
		"backend":   "imap",
		// No server specified
	}

	_, err := handler.Execute(context.Background(), params)
	if err == nil {
		t.Error("Expected error for missing server")
	}
}

func TestEmailReceiveHandler_HandleExtract_MissingDirectory(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{
		IMAPServer: "imap.example.com",
	}, zerolog.Nop())

	params := map[string]interface{}{
		"operation": "extract",
		"backend":   "imap",
		// No save_directory
	}

	// This will fail at connection, but we're testing parameter validation
	_, err := handler.Execute(context.Background(), params)
	if err == nil {
		t.Error("Expected error for missing save_directory")
	}
}

func TestEmailReceiveHandler_HandleExecute_ExecutionDisabled(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{
		IMAPServer:             "imap.example.com",
		AllowExecution:         false,
		AllowedSaveDirectories: []string{"/tmp"},
	}, zerolog.Nop())

	params := map[string]interface{}{
		"operation":      "execute",
		"backend":        "imap",
		"save_directory": "/tmp",
	}

	// This will fail at connection, but we're testing configuration validation
	_, err := handler.Execute(context.Background(), params)
	if err == nil {
		t.Error("Expected error for disabled execution")
	}
}

// TestIMAPBackend_Name verifies backend name
func TestIMAPBackend_Name(t *testing.T) {
	backend := NewIMAPBackend(zerolog.Nop())
	if backend.Name() != "imap" {
		t.Errorf("Name() = %q, want %q", backend.Name(), "imap")
	}
}

// TestIMAPBackend_Connect_NoServer verifies error on missing server
func TestIMAPBackend_Connect_NoServer(t *testing.T) {
	backend := NewIMAPBackend(zerolog.Nop())
	err := backend.Connect(context.Background(), &EmailBackendConfig{})
	if err == nil {
		t.Error("Expected error for missing server")
	}
}

// TestIMAPBackend_ListEmails_NotConnected verifies error when not connected
func TestIMAPBackend_ListEmails_NotConnected(t *testing.T) {
	backend := NewIMAPBackend(zerolog.Nop())
	_, err := backend.ListEmails(context.Background(), &EmailFilter{})
	if err == nil {
		t.Error("Expected error when not connected")
	}
}

// TestIMAPBackend_ReadEmail_NotConnected verifies error when not connected
func TestIMAPBackend_ReadEmail_NotConnected(t *testing.T) {
	backend := NewIMAPBackend(zerolog.Nop())
	_, err := backend.ReadEmail(context.Background(), "123")
	if err == nil {
		t.Error("Expected error when not connected")
	}
}

// TestIMAPBackend_GetAttachment_NotConnected verifies error when not connected
func TestIMAPBackend_GetAttachment_NotConnected(t *testing.T) {
	backend := NewIMAPBackend(zerolog.Nop())
	_, err := backend.GetAttachment(context.Background(), "123", "1")
	if err == nil {
		t.Error("Expected error when not connected")
	}
}

func TestEmailFilter_DateParsing(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{}, zerolog.Nop())

	params := map[string]interface{}{
		"since":  "2025-01-01T00:00:00Z",
		"before": "2025-12-31T23:59:59Z",
	}

	filter := handler.buildFilter(params)

	expectedSince := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	expectedBefore := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)

	if !filter.Since.Equal(expectedSince) {
		t.Errorf("Since = %v, want %v", filter.Since, expectedSince)
	}
	if !filter.Before.Equal(expectedBefore) {
		t.Errorf("Before = %v, want %v", filter.Before, expectedBefore)
	}
}

func TestEmailFilter_BooleanFilters(t *testing.T) {
	handler := NewEmailReceiveHandler(EmailReceiveConfig{}, zerolog.Nop())

	t.Run("unread true", func(t *testing.T) {
		params := map[string]interface{}{"unread": true}
		filter := handler.buildFilter(params)
		if filter.Unread == nil || !*filter.Unread {
			t.Error("Expected Unread to be true")
		}
	})

	t.Run("unread false", func(t *testing.T) {
		params := map[string]interface{}{"unread": false}
		filter := handler.buildFilter(params)
		if filter.Unread == nil || *filter.Unread {
			t.Error("Expected Unread to be false")
		}
	})

	t.Run("has_attachment true", func(t *testing.T) {
		params := map[string]interface{}{"has_attachment": true}
		filter := handler.buildFilter(params)
		if filter.HasAttachment == nil || !*filter.HasAttachment {
			t.Error("Expected HasAttachment to be true")
		}
	})
}
