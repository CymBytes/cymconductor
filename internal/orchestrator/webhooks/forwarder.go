// Package webhooks provides integration with external services via webhooks.
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Forwarder forwards events to webhook endpoints (e.g., messenger service).
type Forwarder struct {
	messengerURL string
	httpClient   *http.Client
	logger       zerolog.Logger
	enabled      bool
	retryCount   int
	retryDelay   time.Duration
}

// Config holds webhook forwarder configuration.
type Config struct {
	// Enabled controls whether webhooks are sent
	Enabled bool

	// MessengerURL is the webhook endpoint URL for messenger service
	MessengerURL string

	// RetryCount is how many times to retry failed requests
	RetryCount int

	// RetryDelay is how long to wait between retries
	RetryDelay time.Duration

	// Timeout for HTTP requests
	Timeout time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:      false,
		MessengerURL: "http://localhost:8085/api/webhooks/orchestrator",
		RetryCount:   3,
		RetryDelay:   time.Second,
		Timeout:      10 * time.Second,
	}
}

// NewForwarder creates a new webhook forwarder.
func NewForwarder(cfg Config, logger zerolog.Logger) *Forwarder {
	return &Forwarder{
		messengerURL: cfg.MessengerURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger:     logger.With().Str("component", "webhook_forwarder").Logger(),
		enabled:    cfg.Enabled,
		retryCount: cfg.RetryCount,
		retryDelay: cfg.RetryDelay,
	}
}

// WebhookEvent is the event payload sent to webhook endpoints.
type WebhookEvent struct {
	EventType  string                 `json:"event_type"`
	EventID    string                 `json:"event_id"`
	ScenarioID string                 `json:"scenario_id"`
	Timestamp  time.Time              `json:"timestamp"`
	Source     string                 `json:"source"`
	Payload    map[string]interface{} `json:"payload"`
}

// JobInfo contains job information for webhook forwarding.
type JobInfo struct {
	JobID       string
	AgentID     string
	ActionType  string
	Parameters  map[string]interface{}
	ScenarioID  string
	ScheduledAt time.Time
}

// JobResult contains job result information for webhook forwarding.
type JobResult struct {
	Status      string
	StartedAt   time.Time
	CompletedAt time.Time
	Result      map[string]interface{}
	DurationMs  int64
	Error       string
}

// ForwardJobCompleted sends a job completion event to the messenger.
func (f *Forwarder) ForwardJobCompleted(ctx context.Context, job *JobInfo, result *JobResult) error {
	if !f.enabled {
		f.logger.Debug().Msg("Webhook forwarder disabled, skipping")
		return nil
	}

	eventType := "job.completed"
	if result.Status == "failed" {
		eventType = "job.failed"
	}

	event := WebhookEvent{
		EventType:  eventType,
		EventID:    fmt.Sprintf("job-%s-%d", job.JobID, time.Now().UnixNano()),
		ScenarioID: job.ScenarioID,
		Timestamp:  result.CompletedAt,
		Source:     "orchestrator",
		Payload: map[string]interface{}{
			"job_id":       job.JobID,
			"agent_id":     job.AgentID,
			"action_type":  job.ActionType,
			"parameters":   job.Parameters,
			"status":       result.Status,
			"duration_ms":  result.DurationMs,
			"scheduled_at": job.ScheduledAt.Format(time.RFC3339),
			"started_at":   result.StartedAt.Format(time.RFC3339),
			"completed_at": result.CompletedAt.Format(time.RFC3339),
		},
	}

	if result.Result != nil {
		event.Payload["result"] = result.Result
	}
	if result.Error != "" {
		event.Payload["error"] = result.Error
	}

	return f.sendEvent(ctx, event)
}

// ForwardScenarioCompleted sends a scenario completion event to the messenger.
func (f *Forwarder) ForwardScenarioCompleted(ctx context.Context, scenarioID, scenarioName string, completed, failed int) error {
	if !f.enabled {
		f.logger.Debug().Msg("Webhook forwarder disabled, skipping")
		return nil
	}

	event := WebhookEvent{
		EventType:  "scenario.completed",
		EventID:    fmt.Sprintf("scenario-%s-%d", scenarioID, time.Now().UnixNano()),
		ScenarioID: scenarioID,
		Timestamp:  time.Now(),
		Source:     "orchestrator",
		Payload: map[string]interface{}{
			"scenario_id":    scenarioID,
			"scenario_name":  scenarioName,
			"jobs_completed": completed,
			"jobs_failed":    failed,
			"status":         "completed",
		},
	}

	return f.sendEvent(ctx, event)
}

// ForwardScenarioStarted sends a scenario start event to the messenger.
func (f *Forwarder) ForwardScenarioStarted(ctx context.Context, scenarioID, scenarioName string, totalJobs int) error {
	if !f.enabled {
		f.logger.Debug().Msg("Webhook forwarder disabled, skipping")
		return nil
	}

	event := WebhookEvent{
		EventType:  "scenario.started",
		EventID:    fmt.Sprintf("scenario-%s-%d", scenarioID, time.Now().UnixNano()),
		ScenarioID: scenarioID,
		Timestamp:  time.Now(),
		Source:     "orchestrator",
		Payload: map[string]interface{}{
			"scenario_id":   scenarioID,
			"scenario_name": scenarioName,
			"total_jobs":    totalJobs,
			"status":        "active",
		},
	}

	return f.sendEvent(ctx, event)
}

// sendEvent sends an event to the messenger webhook endpoint with retries.
func (f *Forwarder) sendEvent(ctx context.Context, event WebhookEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= f.retryCount; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(f.retryDelay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", f.messengerURL, bytes.NewReader(body))
		if err != nil {
			lastErr = fmt.Errorf("failed to create request: %w", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := f.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			f.logger.Warn().
				Err(err).
				Int("attempt", attempt+1).
				Str("event_type", event.EventType).
				Msg("Failed to forward webhook, retrying")
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode < 300 {
			f.logger.Info().
				Str("event_id", event.EventID).
				Str("event_type", event.EventType).
				Str("scenario_id", event.ScenarioID).
				Int("status_code", resp.StatusCode).
				Msg("Webhook forwarded to messenger")
			return nil
		}

		lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		f.logger.Warn().
			Int("status_code", resp.StatusCode).
			Int("attempt", attempt+1).
			Str("event_type", event.EventType).
			Msg("Messenger returned error, retrying")
	}

	f.logger.Error().
		Err(lastErr).
		Str("event_id", event.EventID).
		Str("event_type", event.EventType).
		Msg("Failed to forward webhook after all retries")

	return fmt.Errorf("failed to forward webhook after %d attempts: %w", f.retryCount+1, lastErr)
}

// IsEnabled returns whether the forwarder is enabled.
func (f *Forwarder) IsEnabled() bool {
	return f.enabled
}
