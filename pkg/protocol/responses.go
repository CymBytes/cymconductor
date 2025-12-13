// Package protocol defines the HTTP API request and response types.
package protocol

import "time"

// ============================================================
// Agent Registration Response
// ============================================================

// RegisterAgentResponse is returned after successful agent registration.
type RegisterAgentResponse struct {
	// Confirmed agent ID
	AgentID string `json:"agent_id"`

	// When the agent was registered
	RegisteredAt time.Time `json:"registered_at"`

	// Recommended heartbeat interval in milliseconds
	HeartbeatIntervalMs int `json:"heartbeat_interval_ms"`

	// Agent configuration from orchestrator
	Config *AgentConfig `json:"config,omitempty"`
}

// AgentConfig contains configuration sent to agents.
type AgentConfig struct {
	// Maximum concurrent jobs this agent should run
	MaxConcurrentJobs int `json:"max_concurrent_jobs"`

	// Log level for the agent
	LogLevel string `json:"log_level"`

	// Additional configuration key-value pairs
	Settings map[string]string `json:"settings,omitempty"`
}

// ============================================================
// Heartbeat Response
// ============================================================

// HeartbeatResponse is returned after a heartbeat.
type HeartbeatResponse struct {
	// Acknowledgment
	Acknowledged bool `json:"acknowledged"`

	// Server time for clock synchronization
	ServerTime time.Time `json:"server_time"`

	// Commands for the agent to execute (e.g., shutdown, reconfigure)
	Commands []AgentCommand `json:"commands,omitempty"`
}

// AgentCommand is a directive from orchestrator to agent.
type AgentCommand struct {
	// Command type: shutdown, reconfigure, cancel_job
	Type string `json:"type"`

	// Command-specific parameters
	Parameters map[string]string `json:"parameters,omitempty"`
}

// ============================================================
// Job Polling Response
// ============================================================

// GetJobsResponse is returned when agents poll for jobs.
type GetJobsResponse struct {
	// Jobs to execute
	Jobs []JobAssignment `json:"jobs"`

	// Whether there are more jobs available
	HasMore bool `json:"has_more"`

	// Next poll time suggestion (optional)
	NextPollMs int `json:"next_poll_ms,omitempty"`
}

// JobAssignment represents a job assigned to an agent.
type JobAssignment struct {
	// Job ID
	JobID string `json:"job_id"`

	// Action type to execute
	ActionType string `json:"action_type"`

	// Action parameters (JSON object)
	Parameters map[string]interface{} `json:"parameters"`

	// Job priority (higher = more urgent)
	Priority int `json:"priority"`

	// When this job was scheduled
	ScheduledAt time.Time `json:"scheduled_at"`

	// Maximum execution time in seconds (optional)
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// Scenario context (optional, for logging)
	ScenarioID string `json:"scenario_id,omitempty"`
	ScenarioName string `json:"scenario_name,omitempty"`
}

// ============================================================
// Job Result Response
// ============================================================

// JobResultResponse is returned after submitting a job result.
type JobResultResponse struct {
	// Acknowledgment
	Acknowledged bool `json:"acknowledged"`

	// Whether a retry has been scheduled (for failed jobs)
	RetryScheduled bool `json:"retry_scheduled,omitempty"`

	// When the retry is scheduled (if applicable)
	RetryAt *time.Time `json:"retry_at,omitempty"`
}

// ============================================================
// Scenario Responses
// ============================================================

// CreateScenarioResponse is returned after creating a scenario.
type CreateScenarioResponse struct {
	// Scenario ID
	ScenarioID string `json:"scenario_id"`

	// Scenario name
	Name string `json:"name"`

	// Current status
	Status string `json:"status"`

	// Number of steps in the scenario
	StepCount int `json:"step_count,omitempty"`

	// Number of jobs created
	JobCount int `json:"job_count,omitempty"`

	// When the scenario was created
	CreatedAt time.Time `json:"created_at"`

	// Estimated completion time (if known)
	EstimatedCompletionAt *time.Time `json:"estimated_completion_at,omitempty"`
}

// ScenarioStatusResponse provides status information for a scenario.
type ScenarioStatusResponse struct {
	// Scenario ID
	ScenarioID string `json:"scenario_id"`

	// Scenario name
	Name string `json:"name"`

	// Current status
	Status string `json:"status"`

	// Progress information
	Progress *ScenarioProgress `json:"progress,omitempty"`

	// Error message if failed
	ErrorMessage string `json:"error_message,omitempty"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ScenarioProgress contains progress information for a scenario.
type ScenarioProgress struct {
	// Total number of jobs
	TotalJobs int `json:"total_jobs"`

	// Jobs completed successfully
	CompletedJobs int `json:"completed_jobs"`

	// Jobs that failed
	FailedJobs int `json:"failed_jobs"`

	// Jobs currently running
	RunningJobs int `json:"running_jobs"`

	// Jobs pending
	PendingJobs int `json:"pending_jobs"`

	// Completion percentage
	PercentComplete float64 `json:"percent_complete"`
}

// ============================================================
// Error Response
// ============================================================

// ErrorResponse is the standard error format for all API errors.
type ErrorResponse struct {
	// Error code for programmatic handling
	Error string `json:"error"`

	// Human-readable error message
	Message string `json:"message"`

	// Additional error details
	Details map[string]interface{} `json:"details,omitempty"`

	// Request ID for debugging
	RequestID string `json:"request_id,omitempty"`
}

// ============================================================
// Health Check Response
// ============================================================

// HealthResponse is returned by the health check endpoint.
type HealthResponse struct {
	// Service status: healthy, degraded, unhealthy
	Status string `json:"status"`

	// Service version
	Version string `json:"version"`

	// Service uptime in seconds
	UptimeSeconds int64 `json:"uptime_seconds"`

	// Component health
	Components map[string]ComponentHealth `json:"components,omitempty"`
}

// ComponentHealth represents the health of a subsystem.
type ComponentHealth struct {
	// Component status
	Status string `json:"status"`

	// Last check time
	LastCheck time.Time `json:"last_check"`

	// Additional details
	Details string `json:"details,omitempty"`
}

// ============================================================
// Agent List Response (Admin endpoint)
// ============================================================

// ListAgentsResponse is returned when listing registered agents.
type ListAgentsResponse struct {
	// List of agents
	Agents []AgentInfo `json:"agents"`

	// Total count
	Total int `json:"total"`
}

// AgentInfo contains information about a registered agent.
type AgentInfo struct {
	// Agent ID
	AgentID string `json:"agent_id"`

	// Lab host ID
	LabHostID string `json:"lab_host_id"`

	// Hostname
	Hostname string `json:"hostname"`

	// IP address
	IPAddress string `json:"ip_address"`

	// Labels
	Labels map[string]string `json:"labels"`

	// Current status
	Status string `json:"status"`

	// Agent version
	Version string `json:"version"`

	// Last heartbeat time
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`

	// Registration time
	RegisteredAt time.Time `json:"registered_at"`

	// Current job count
	CurrentJobCount int `json:"current_job_count,omitempty"`
}

// ============================================================
// Impersonation User Responses
// ============================================================

// ImpersonationUserResponse represents a user in API responses.
type ImpersonationUserResponse struct {
	// User ID
	ID string `json:"id"`

	// Full username (DOMAIN\user)
	Username string `json:"username"`

	// Domain name
	Domain string `json:"domain"`

	// SAM account name
	SAMAccountName string `json:"sam_account_name"`

	// Display name
	DisplayName string `json:"display_name,omitempty"`

	// Department
	Department string `json:"department,omitempty"`

	// Job title
	Title string `json:"title,omitempty"`

	// Allowed lab hosts
	AllowedHosts []string `json:"allowed_hosts,omitempty"`

	// Persona information
	Persona *UserPersonaResponse `json:"persona,omitempty"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// UserPersonaResponse contains behavior hints in API responses.
type UserPersonaResponse struct {
	TypicalApps  []string            `json:"typical_apps,omitempty"`
	TypicalSites []string            `json:"typical_sites,omitempty"`
	FileTypes    []string            `json:"file_types,omitempty"`
	WorkHours    *WorkHoursResponse  `json:"work_hours,omitempty"`
}

// WorkHoursResponse contains work hours in API responses.
type WorkHoursResponse struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ListImpersonationUsersResponse is returned when listing users.
type ListImpersonationUsersResponse struct {
	Users []ImpersonationUserResponse `json:"users"`
	Total int                         `json:"total"`
}

// BulkCreateImpersonationUsersResponse is returned after bulk user creation.
type BulkCreateImpersonationUsersResponse struct {
	Created []ImpersonationUserResponse `json:"created"`
	Errors  []BulkUserError             `json:"errors,omitempty"`
	Total   int                         `json:"total"`
	Success int                         `json:"success"`
	Failed  int                         `json:"failed"`
}

// BulkUserError represents an error during bulk user creation.
type BulkUserError struct {
	Index    int    `json:"index"`
	Username string `json:"username"`
	Error    string `json:"error"`
}
