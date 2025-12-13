// Package audit provides audit logging for CymConductor agent security events.
package audit

import (
	"encoding/json"
	"time"

	"github.com/rs/zerolog"
)

// EventType represents the type of audit event.
type EventType string

const (
	// EventImpersonationStart is logged when impersonation begins.
	EventImpersonationStart EventType = "impersonation_start"

	// EventImpersonationEnd is logged when impersonation ends.
	EventImpersonationEnd EventType = "impersonation_end"

	// EventImpersonationDenied is logged when impersonation is denied.
	EventImpersonationDenied EventType = "impersonation_denied"

	// EventProcessCreated is logged when a process is created as another user.
	EventProcessCreated EventType = "process_created_as_user"

	// EventActionExecuted is logged when an action completes.
	EventActionExecuted EventType = "action_executed"
)

// Event represents a security audit event.
type Event struct {
	Timestamp   time.Time              `json:"timestamp"`
	EventType   EventType              `json:"event_type"`
	AgentID     string                 `json:"agent_id"`
	LabHostID   string                 `json:"lab_host_id"`
	JobID       string                 `json:"job_id,omitempty"`
	ScenarioID  string                 `json:"scenario_id,omitempty"`
	TargetUser  string                 `json:"target_user,omitempty"`
	ActionType  string                 `json:"action_type,omitempty"`
	LogonType   string                 `json:"logon_type,omitempty"`
	Result      string                 `json:"result"` // success, failure, denied
	DurationMs  int                    `json:"duration_ms,omitempty"`
	ErrorMsg    string                 `json:"error,omitempty"`
	ProcessID   uint32                 `json:"process_id,omitempty"`
	CommandLine string                 `json:"command_line,omitempty"`
	Details     map[string]interface{} `json:"details,omitempty"`
}

// Logger handles audit event logging.
type Logger struct {
	agentID   string
	labHostID string
	logger    zerolog.Logger
}

// NewLogger creates a new audit logger.
func NewLogger(agentID, labHostID string, logger zerolog.Logger) *Logger {
	return &Logger{
		agentID:   agentID,
		labHostID: labHostID,
		logger:    logger.With().Str("component", "audit").Logger(),
	}
}

// Log writes an audit event.
func (l *Logger) Log(event *Event) {
	event.Timestamp = time.Now()
	event.AgentID = l.agentID
	event.LabHostID = l.labHostID

	// Serialize to JSON for structured logging
	eventJSON, _ := json.Marshal(event)

	logEvent := l.logger.Info().
		Str("event_type", string(event.EventType)).
		Str("result", event.Result)

	if event.TargetUser != "" {
		logEvent = logEvent.Str("target_user", event.TargetUser)
	}
	if event.ActionType != "" {
		logEvent = logEvent.Str("action_type", event.ActionType)
	}
	if event.JobID != "" {
		logEvent = logEvent.Str("job_id", event.JobID)
	}
	if event.ErrorMsg != "" {
		logEvent = logEvent.Str("error", event.ErrorMsg)
	}

	logEvent.RawJSON("audit_event", eventJSON).Msg("Audit event")
}

// LogImpersonationStart logs the start of an impersonation session.
func (l *Logger) LogImpersonationStart(jobID, scenarioID, targetUser, logonType, actionType string) {
	l.Log(&Event{
		EventType:  EventImpersonationStart,
		JobID:      jobID,
		ScenarioID: scenarioID,
		TargetUser: targetUser,
		LogonType:  logonType,
		ActionType: actionType,
		Result:     "success",
	})
}

// LogImpersonationEnd logs the end of an impersonation session.
func (l *Logger) LogImpersonationEnd(jobID, scenarioID, targetUser string, durationMs int, err error) {
	event := &Event{
		EventType:  EventImpersonationEnd,
		JobID:      jobID,
		ScenarioID: scenarioID,
		TargetUser: targetUser,
		DurationMs: durationMs,
		Result:     "success",
	}

	if err != nil {
		event.Result = "failure"
		event.ErrorMsg = err.Error()
	}

	l.Log(event)
}

// LogImpersonationDenied logs a denied impersonation attempt.
func (l *Logger) LogImpersonationDenied(jobID, scenarioID, targetUser, reason string) {
	l.Log(&Event{
		EventType:  EventImpersonationDenied,
		JobID:      jobID,
		ScenarioID: scenarioID,
		TargetUser: targetUser,
		Result:     "denied",
		ErrorMsg:   reason,
	})
}

// LogProcessCreated logs process creation as another user.
func (l *Logger) LogProcessCreated(jobID, scenarioID, targetUser, cmdLine string, processID uint32) {
	l.Log(&Event{
		EventType:   EventProcessCreated,
		JobID:       jobID,
		ScenarioID:  scenarioID,
		TargetUser:  targetUser,
		CommandLine: cmdLine,
		ProcessID:   processID,
		Result:      "success",
	})
}

// LogActionExecuted logs action execution completion.
func (l *Logger) LogActionExecuted(jobID, scenarioID, actionType, targetUser string, durationMs int, err error) {
	event := &Event{
		EventType:  EventActionExecuted,
		JobID:      jobID,
		ScenarioID: scenarioID,
		ActionType: actionType,
		TargetUser: targetUser,
		DurationMs: durationMs,
		Result:     "success",
	}

	if err != nil {
		event.Result = "failure"
		event.ErrorMsg = err.Error()
	}

	l.Log(event)
}
