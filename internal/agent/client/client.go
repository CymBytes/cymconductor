// Package client provides the HTTP client for communicating with the orchestrator.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Client is the orchestrator API client.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     zerolog.Logger
}

// Config holds client configuration.
type Config struct {
	BaseURL        string
	ConnectTimeout time.Duration
	RequestTimeout time.Duration
}

// New creates a new orchestrator client.
func New(cfg Config, logger zerolog.Logger) *Client {
	return &Client{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
		logger: logger.With().Str("component", "client").Logger(),
	}
}

// ============================================================
// Request/Response types
// ============================================================

// RegisterRequest is sent when registering with the orchestrator.
type RegisterRequest struct {
	AgentID   string            `json:"agent_id"`
	LabHostID string            `json:"lab_host_id"`
	Hostname  string            `json:"hostname"`
	IPAddress string            `json:"ip_address"`
	Labels    map[string]string `json:"labels"`
	Version   string            `json:"version"`
}

// RegisterResponse is returned after registration.
type RegisterResponse struct {
	AgentID             string `json:"agent_id"`
	RegisteredAt        string `json:"registered_at"`
	HeartbeatIntervalMs int    `json:"heartbeat_interval_ms"`
}

// HeartbeatRequest is sent periodically.
type HeartbeatRequest struct {
	Status      string   `json:"status"`
	CurrentJobs []string `json:"current_jobs,omitempty"`
}

// HeartbeatResponse is returned after heartbeat.
type HeartbeatResponse struct {
	Acknowledged bool   `json:"acknowledged"`
	ServerTime   string `json:"server_time"`
}

// JobAssignment represents a job to execute.
type JobAssignment struct {
	JobID       string                 `json:"job_id"`
	ActionType  string                 `json:"action_type"`
	Parameters  map[string]interface{} `json:"parameters"`
	Priority    int                    `json:"priority"`
	ScheduledAt time.Time              `json:"scheduled_at"`
	ScenarioID  string                 `json:"scenario_id,omitempty"`
	RunAs       *RunAsConfig           `json:"run_as,omitempty"`
}

// RunAsConfig specifies user impersonation for a job.
type RunAsConfig struct {
	User      string `json:"user"`                 // Full username (DOMAIN\user)
	LogonType string `json:"logon_type,omitempty"` // interactive, network, batch
}

// GetJobsResponse is returned when polling for jobs.
type GetJobsResponse struct {
	Jobs    []JobAssignment `json:"jobs"`
	HasMore bool            `json:"has_more"`
}

// JobResultRequest is sent after executing a job.
type JobResultRequest struct {
	Status      string      `json:"status"`
	StartedAt   time.Time   `json:"started_at"`
	CompletedAt time.Time   `json:"completed_at"`
	Result      *JobResult  `json:"result,omitempty"`
	Error       *JobError   `json:"error,omitempty"`
}

// JobResult contains execution results.
type JobResult struct {
	Data       map[string]interface{} `json:"data,omitempty"`
	Summary    string                 `json:"summary,omitempty"`
	DurationMs int64                  `json:"duration_ms,omitempty"`
}

// JobError contains error details.
type JobError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Details   string `json:"details,omitempty"`
}

// JobResultResponse is returned after reporting a result.
type JobResultResponse struct {
	Acknowledged   bool   `json:"acknowledged"`
	RetryScheduled bool   `json:"retry_scheduled,omitempty"`
	RetryAt        string `json:"retry_at,omitempty"`
}

// ErrorResponse represents an API error.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ============================================================
// API methods
// ============================================================

// Register registers the agent with the orchestrator.
func (c *Client) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
	c.logger.Debug().
		Str("agent_id", req.AgentID).
		Str("lab_host_id", req.LabHostID).
		Msg("Registering with orchestrator")

	var resp RegisterResponse
	err := c.post(ctx, "/api/agents/register", req, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// Heartbeat sends a heartbeat to the orchestrator.
func (c *Client) Heartbeat(ctx context.Context, agentID string, req HeartbeatRequest) (*HeartbeatResponse, error) {
	var resp HeartbeatResponse
	err := c.post(ctx, fmt.Sprintf("/api/agents/%s/heartbeat", agentID), req, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// GetJobs polls for available jobs.
func (c *Client) GetJobs(ctx context.Context, agentID string, max int) ([]JobAssignment, error) {
	url := fmt.Sprintf("/api/agents/%s/jobs/next?max=%d", agentID, max)

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// No content means no jobs
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var jobsResp GetJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return jobsResp.Jobs, nil
}

// ReportResult reports the result of a job execution.
func (c *Client) ReportResult(ctx context.Context, agentID, jobID string, req JobResultRequest) (*JobResultResponse, error) {
	url := fmt.Sprintf("/api/agents/%s/jobs/%s/result", agentID, jobID)

	var resp JobResultResponse
	err := c.post(ctx, url, req, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// ============================================================
// Helper methods
// ============================================================

func (c *Client) post(ctx context.Context, path string, body interface{}, response interface{}) error {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return c.parseError(resp)
	}

	if response != nil {
		if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

func (c *Client) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != "" {
		return fmt.Errorf("API error (%d): %s - %s", resp.StatusCode, errResp.Error, errResp.Message)
	}

	return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
}
