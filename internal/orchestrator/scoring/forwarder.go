// Package scoring provides integration with the CymBytes Scoring Engine.
package scoring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// EventForwarder forwards job results to the scoring engine.
type EventForwarder struct {
	engineURL  string
	httpClient *http.Client
	logger     zerolog.Logger
	enabled    bool
	retryCount int
	retryDelay time.Duration
}

// Config holds event forwarder configuration.
type Config struct {
	// Enabled controls whether events are forwarded
	Enabled bool

	// EngineURL is the base URL of the scoring engine
	EngineURL string

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
		Enabled:    false,
		EngineURL:  "http://localhost:8083",
		RetryCount: 3,
		RetryDelay: time.Second,
		Timeout:    10 * time.Second,
	}
}

// NewEventForwarder creates a new event forwarder.
func NewEventForwarder(cfg Config, logger zerolog.Logger) *EventForwarder {
	return &EventForwarder{
		engineURL: cfg.EngineURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger:     logger.With().Str("component", "scoring_forwarder").Logger(),
		enabled:    cfg.Enabled,
		retryCount: cfg.RetryCount,
		retryDelay: cfg.RetryDelay,
	}
}

// ScoringEvent is the event payload sent to the scoring engine.
type ScoringEvent struct {
	EventID   string                 `json:"event_id"`
	EventType string                 `json:"event_type"`
	Timestamp time.Time              `json:"timestamp"`
	Source    string                 `json:"source"`
	Payload   map[string]interface{} `json:"payload"`
}

// JobInfo contains job information for event forwarding.
type JobInfo struct {
	JobID       string
	AgentID     string
	ActionType  string
	Parameters  map[string]interface{}
	ScenarioID  string
	ScheduledAt time.Time
}

// JobResult contains job result information for event forwarding.
type JobResult struct {
	Status      string
	StartedAt   time.Time
	CompletedAt time.Time
	Result      map[string]interface{}
	DurationMs  int64
	Error       string
}

// ForwardJobResult sends a job result to the scoring engine.
// This is called asynchronously after a job completes.
func (f *EventForwarder) ForwardJobResult(ctx context.Context, runID string, job *JobInfo, result *JobResult) error {
	if !f.enabled {
		f.logger.Debug().Msg("Scoring forwarder disabled, skipping")
		return nil
	}

	if runID == "" {
		f.logger.Debug().Str("job_id", job.JobID).Msg("No scoring run ID, skipping")
		return nil
	}

	// Only forward observation actions
	if !isObservationAction(job.ActionType) {
		f.logger.Debug().
			Str("job_id", job.JobID).
			Str("action_type", job.ActionType).
			Msg("Not an observation action, skipping")
		return nil
	}

	// Map action result to scoring event type
	eventType := mapActionToEventType(job.ActionType, result)

	event := ScoringEvent{
		EventID:   fmt.Sprintf("job-%s-%d", job.JobID, time.Now().UnixNano()),
		EventType: eventType,
		Timestamp: result.CompletedAt,
		Source:    "orchestrator",
		Payload: map[string]interface{}{
			"job_id":       job.JobID,
			"agent_id":     job.AgentID,
			"action_type":  job.ActionType,
			"parameters":   job.Parameters,
			"scenario_id":  job.ScenarioID,
			"result":       result.Result,
			"status":       result.Status,
			"duration_ms":  result.DurationMs,
			"scheduled_at": job.ScheduledAt.Format(time.RFC3339),
			"started_at":   result.StartedAt.Format(time.RFC3339),
			"completed_at": result.CompletedAt.Format(time.RFC3339),
		},
	}

	if result.Error != "" {
		event.Payload["error"] = result.Error
	}

	return f.sendEvent(ctx, runID, event)
}

// isObservationAction checks if an action type is an observation action.
func isObservationAction(actionType string) bool {
	observationActions := map[string]bool{
		"observe_process_state":      true,
		"observe_file_state":         true,
		"observe_user_state":         true,
		"observe_registry_state":     true,
		"capture_siem_query":         true,
		"verify_network_isolation":   true,
		"capture_powershell_history": true,
	}
	return observationActions[actionType]
}

// mapActionToEventType maps an action type and result to a scoring event type.
func mapActionToEventType(actionType string, result *JobResult) string {
	if result.Status == "failed" {
		return "orchestrator.verification_failed"
	}

	switch actionType {
	case "observe_process_state":
		// Check if state matched
		if result.Result != nil {
			if matches, ok := result.Result["state_matches"].(bool); ok && matches {
				return "state.verified"
			}
		}
		return "state.check_failed"

	case "observe_file_state":
		if result.Result != nil {
			if matches, ok := result.Result["state_matches"].(bool); ok && matches {
				return "state.verified"
			}
		}
		return "state.check_failed"

	case "observe_user_state":
		if result.Result != nil {
			if matches, ok := result.Result["state_matches"].(bool); ok && matches {
				return "state.verified"
			}
		}
		return "state.check_failed"

	case "capture_siem_query":
		if result.Result != nil {
			if hasMatches, ok := result.Result["has_matches"].(bool); ok && hasMatches {
				return "query.executed"
			}
		}
		return "query.no_match"

	case "capture_powershell_history":
		if result.Result != nil {
			if hasMatches, ok := result.Result["has_matches"].(bool); ok && hasMatches {
				return "response.action_applied"
			}
		}
		return "response.no_action_detected"

	case "verify_network_isolation":
		if result.Result != nil {
			if matches, ok := result.Result["state_matches"].(bool); ok && matches {
				return "state.verified"
			}
		}
		return "state.check_failed"

	default:
		return "orchestrator.job_completed"
	}
}

// sendEvent sends an event to the scoring engine with retries.
func (f *EventForwarder) sendEvent(ctx context.Context, runID string, event ScoringEvent) error {
	url := fmt.Sprintf("%s/api/v1/runs/%s/events", f.engineURL, runID)

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

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
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
				Str("run_id", runID).
				Msg("Failed to forward event, retrying")
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode < 300 {
			f.logger.Info().
				Str("event_id", event.EventID).
				Str("event_type", event.EventType).
				Str("run_id", runID).
				Int("status_code", resp.StatusCode).
				Msg("Event forwarded to scoring engine")
			return nil
		}

		// Handle duplicate (200 OK with status: duplicate)
		if resp.StatusCode == 200 {
			f.logger.Debug().
				Str("event_id", event.EventID).
				Str("run_id", runID).
				Msg("Duplicate event detected")
			return nil
		}

		lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		f.logger.Warn().
			Int("status_code", resp.StatusCode).
			Int("attempt", attempt+1).
			Str("run_id", runID).
			Msg("Scoring engine returned error, retrying")
	}

	f.logger.Error().
		Err(lastErr).
		Str("event_id", event.EventID).
		Str("run_id", runID).
		Msg("Failed to forward event after all retries")

	return fmt.Errorf("failed to forward event after %d attempts: %w", f.retryCount+1, lastErr)
}

// IsEnabled returns whether the forwarder is enabled.
func (f *EventForwarder) IsEnabled() bool {
	return f.enabled
}
