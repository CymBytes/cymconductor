// Package dsl defines the Domain-Specific Language types for lab scenarios.
// These types are shared between the orchestrator (for validation/compilation)
// and the agent (for execution).
package dsl

// ActionType represents the type of action an agent can execute.
// This is a closed enumeration - only these action types are allowed.
type ActionType string

const (
	// ActionSimulateBrowsing simulates web browsing activity
	ActionSimulateBrowsing ActionType = "simulate_browsing"

	// ActionSimulateFileActivity simulates file operations (create, modify, read, delete)
	ActionSimulateFileActivity ActionType = "simulate_file_activity"

	// ActionSimulateEmailTraffic simulates sending/receiving emails
	ActionSimulateEmailTraffic ActionType = "simulate_email_traffic"

	// ActionSimulateProcessActivity simulates running processes from an approved list
	ActionSimulateProcessActivity ActionType = "simulate_process_activity"
)

// AllowedActions is the complete list of approved action types.
// The validator uses this to reject any unknown action types from AI output.
var AllowedActions = []ActionType{
	ActionSimulateBrowsing,
	ActionSimulateFileActivity,
	ActionSimulateEmailTraffic,
	ActionSimulateProcessActivity,
}

// IsValidAction checks if an action type is in the approved list.
func IsValidAction(action ActionType) bool {
	for _, a := range AllowedActions {
		if a == action {
			return true
		}
	}
	return false
}

// SimulateBrowsingParams defines parameters for the browsing action.
type SimulateBrowsingParams struct {
	// URLs to visit (required, 1-20 URLs)
	URLs []string `json:"urls" validate:"required,min=1,max=20,dive,url"`

	// Duration in seconds to simulate browsing (10-3600)
	DurationSeconds int `json:"duration_seconds" validate:"required,min=10,max=3600"`

	// Whether to click links on visited pages
	ClickLinks bool `json:"click_links"`

	// Scroll behavior: none, natural, aggressive
	ScrollBehavior string `json:"scroll_behavior,omitempty" validate:"omitempty,oneof=none natural aggressive"`

	// Maximum number of tabs to open (1-10)
	MaxTabs int `json:"max_tabs,omitempty" validate:"omitempty,min=1,max=10"`

	// User agent string override (optional)
	UserAgent string `json:"user_agent,omitempty"`
}

// SimulateFileActivityParams defines parameters for file activity simulation.
type SimulateFileActivityParams struct {
	// Target directory for file operations (required)
	TargetDirectory string `json:"target_directory" validate:"required"`

	// Operations to perform: create, modify, read, delete, rename
	Operations []string `json:"operations" validate:"required,min=1,dive,oneof=create modify read delete rename"`

	// Number of files to operate on (1-100)
	FileCount int `json:"file_count" validate:"required,min=1,max=100"`

	// File types to create/modify: txt, docx, xlsx, pdf, json, csv
	FileTypes []string `json:"file_types,omitempty" validate:"omitempty,dive,oneof=txt docx xlsx pdf json csv"`

	// Minimum file size in KB
	FileSizeKBMin int `json:"file_size_kb_min,omitempty" validate:"omitempty,min=1"`

	// Maximum file size in KB (max 10MB)
	FileSizeKBMax int `json:"file_size_kb_max,omitempty" validate:"omitempty,max=10240"`

	// Whether to preserve files after scenario (default: false = cleanup)
	PreserveFiles bool `json:"preserve_files,omitempty"`
}

// SimulateEmailTrafficParams defines parameters for email simulation.
type SimulateEmailTrafficParams struct {
	// Protocol: smtp or imap
	Protocol string `json:"protocol" validate:"required,oneof=smtp imap"`

	// Mail server hostname
	Server string `json:"server" validate:"required,hostname|ip"`

	// Mail server port
	Port int `json:"port" validate:"required,min=1,max=65535"`

	// Username for authentication
	Username string `json:"username" validate:"required"`

	// Password for authentication
	Password string `json:"password" validate:"required"`

	// Use TLS/SSL
	UseTLS bool `json:"use_tls"`

	// Actions to perform: send, receive, list, read
	Actions []string `json:"actions" validate:"required,min=1,dive,oneof=send receive list read"`

	// Number of emails to send/receive
	EmailCount int `json:"email_count,omitempty" validate:"omitempty,min=1,max=50"`

	// Recipients for sending (required if send is in actions)
	Recipients []string `json:"recipients,omitempty" validate:"omitempty,dive,email"`

	// Subject template for emails
	SubjectTemplate string `json:"subject_template,omitempty"`

	// Body template for emails
	BodyTemplate string `json:"body_template,omitempty"`
}

// SimulateProcessActivityParams defines parameters for process simulation.
type SimulateProcessActivityParams struct {
	// Processes to spawn from the approved list (required)
	// Windows: notepad, calc, mspaint, explorer, cmd, powershell
	// Linux: gedit, gnome-calculator, nautilus, xterm
	AllowedProcesses []string `json:"allowed_processes" validate:"required,min=1"`

	// Number of processes to spawn (1-20)
	SpawnCount int `json:"spawn_count" validate:"required,min=1,max=20"`

	// Duration in seconds to keep processes alive
	DurationSeconds int `json:"duration_seconds" validate:"required,min=5,max=600"`

	// CPU intensity: low, medium, high
	CPUIntensity string `json:"cpu_intensity,omitempty" validate:"omitempty,oneof=low medium high"`

	// Whether to interact with spawned processes (click, type, etc.)
	Interact bool `json:"interact,omitempty"`
}

// ApprovedProcessesWindows is the whitelist of processes that can be spawned on Windows.
var ApprovedProcessesWindows = []string{
	"notepad.exe",
	"calc.exe",
	"mspaint.exe",
	"explorer.exe",
	"cmd.exe",
	"powershell.exe",
	"wordpad.exe",
	"write.exe",
}

// ApprovedProcessesLinux is the whitelist of processes that can be spawned on Linux.
var ApprovedProcessesLinux = []string{
	"gedit",
	"gnome-calculator",
	"nautilus",
	"xterm",
	"gnome-terminal",
	"firefox",
	"evince",
}

// IsApprovedProcess checks if a process name is in the approved list for the given OS.
func IsApprovedProcess(process string, os string) bool {
	var approved []string
	switch os {
	case "windows":
		approved = ApprovedProcessesWindows
	case "linux":
		approved = ApprovedProcessesLinux
	default:
		return false
	}

	for _, p := range approved {
		if p == process {
			return true
		}
	}
	return false
}
