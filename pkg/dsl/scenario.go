// Package dsl defines the Domain-Specific Language types for lab scenarios.
package dsl

import (
	"encoding/json"
	"time"
)

// SchemaVersion is the current DSL schema version.
const SchemaVersion = "cymbytes-scenario-v1"

// Scenario represents a complete lab simulation scenario.
// This is the validated output from the AI planner.
type Scenario struct {
	// Schema version for forward compatibility
	Schema string `json:"$schema" validate:"required,eq=cymbytes-scenario-v1"`

	// Unique identifier for this scenario
	ID string `json:"id" validate:"required,uuid4"`

	// Human-readable name
	Name string `json:"name" validate:"required,min=1,max=255"`

	// Optional description
	Description string `json:"description,omitempty" validate:"omitempty,max=2000"`

	// Tags for categorization
	Tags []string `json:"tags,omitempty" validate:"omitempty,max=20,dive,min=1,max=50"`

	// Schema version number (for migrations)
	Version int `json:"version" validate:"required,min=1"`

	// Ordered list of steps to execute
	Steps []Step `json:"steps" validate:"required,min=1,max=100,dive"`

	// Schedule configuration
	Schedule Schedule `json:"schedule" validate:"required"`
}

// Step represents a single action within a scenario.
type Step struct {
	// Unique identifier for this step
	ID string `json:"id" validate:"required,uuid4"`

	// Execution order (1-indexed, must be sequential)
	Order int `json:"order" validate:"required,min=1"`

	// Action type to execute (must be from approved list)
	ActionType ActionType `json:"action_type" validate:"required"`

	// Target selection criteria
	Target Target `json:"target" validate:"required"`

	// Action-specific parameters (validated based on action_type)
	Parameters json.RawMessage `json:"parameters" validate:"required"`

	// Timing configuration
	Timing Timing `json:"timing,omitempty"`

	// User impersonation configuration (optional)
	RunAs *RunAs `json:"run_as,omitempty"`

	// Optional condition for conditional execution
	Condition *Condition `json:"condition,omitempty"`
}

// RunAs specifies user impersonation for a step.
type RunAs struct {
	// User is the full username (DOMAIN\user)
	User string `json:"user" validate:"required"`

	// LogonType specifies the Windows logon type
	// Values: "interactive" (type 2), "network" (type 3), "batch" (type 4)
	LogonType string `json:"logon_type,omitempty" validate:"omitempty,oneof=interactive network batch"`

	// CreateLogonEvent controls whether a Windows logon event is generated
	CreateLogonEvent *bool `json:"create_logon_event,omitempty"`
}

// Target specifies which agents should receive this step.
type Target struct {
	// Label selector for matching agents
	Labels map[string]string `json:"labels" validate:"required"`

	// How many matching agents to target: "all", "any", or a number
	Count string `json:"count" validate:"required"`
}

// Timing configures delays and jitter for step execution.
type Timing struct {
	// Delay before executing this step (milliseconds)
	DelayBeforeMs int `json:"delay_before_ms,omitempty" validate:"omitempty,min=0,max=300000"`

	// Delay after executing this step (milliseconds)
	DelayAfterMs int `json:"delay_after_ms,omitempty" validate:"omitempty,min=0,max=300000"`

	// Random jitter range (+/- milliseconds)
	JitterMs int `json:"jitter_ms,omitempty" validate:"omitempty,min=0,max=60000"`

	// Relative time from scenario start (seconds)
	RelativeTimeSeconds int `json:"relative_time_seconds,omitempty" validate:"omitempty,min=0"`
}

// Schedule configures when and how the scenario runs.
type Schedule struct {
	// Schedule type: immediate, delayed, cron
	Type string `json:"type" validate:"required,oneof=immediate delayed cron"`

	// Start time for delayed schedules (ISO8601)
	StartAt *time.Time `json:"start_at,omitempty"`

	// Cron expression for recurring schedules
	CronExpression string `json:"cron_expression,omitempty"`

	// Whether to repeat the scenario
	Repeat bool `json:"repeat,omitempty"`

	// Number of repetitions (0 = infinite for cron)
	RepeatCount int `json:"repeat_count,omitempty" validate:"omitempty,min=0,max=1000"`

	// Delay between repetitions (seconds)
	RepeatIntervalSeconds int `json:"repeat_interval_seconds,omitempty" validate:"omitempty,min=0,max=86400"`
}

// Condition for conditional step execution (v2 feature, stubbed for now).
type Condition struct {
	// Type of condition: previous_success, time_window, agent_count
	Type string `json:"type" validate:"required,oneof=previous_success time_window agent_count"`

	// Condition-specific parameters
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// Intent represents the high-level lab intent submitted by users/API.
// This is the INPUT to the AI planner.
type Intent struct {
	// Lab type identifier
	LabType string `json:"lab_type" validate:"required"`

	// Lab duration in minutes
	DurationMinutes int `json:"duration_minutes" validate:"required,min=5,max=480"`

	// Difficulty level: easy, medium, hard
	Difficulty string `json:"difficulty" validate:"required,oneof=easy medium hard"`

	// Expected host configuration
	ExpectedHosts []ExpectedHost `json:"expected_hosts" validate:"required,min=1"`

	// Scenario goals/objectives
	Goals []string `json:"goals,omitempty"`

	// Additional context for the AI planner
	Context string `json:"context,omitempty" validate:"omitempty,max=5000"`

	// Noise intensity: low, medium, high
	NoiseIntensity string `json:"noise_intensity,omitempty" validate:"omitempty,oneof=low medium high"`
}

// ExpectedHost describes an expected host in the lab environment.
type ExpectedHost struct {
	// Role of the host: workstation, server, dc (domain controller), attacker
	Role string `json:"role" validate:"required,oneof=workstation server dc attacker router"`

	// Operating system: windows, linux
	OS string `json:"os" validate:"required,oneof=windows linux"`

	// Expected count of hosts with this configuration
	Count int `json:"count" validate:"required,min=1,max=50"`

	// Additional labels for targeting
	Labels map[string]string `json:"labels,omitempty"`
}

// ParseParameters extracts typed parameters from a step based on action type.
// Returns the appropriate parameter struct or an error.
func (s *Step) ParseParameters() (interface{}, error) {
	switch s.ActionType {
	case ActionSimulateBrowsing:
		var params SimulateBrowsingParams
		if err := json.Unmarshal(s.Parameters, &params); err != nil {
			return nil, err
		}
		return &params, nil

	case ActionSimulateFileActivity:
		var params SimulateFileActivityParams
		if err := json.Unmarshal(s.Parameters, &params); err != nil {
			return nil, err
		}
		return &params, nil

	case ActionSimulateEmailTraffic:
		var params SimulateEmailTrafficParams
		if err := json.Unmarshal(s.Parameters, &params); err != nil {
			return nil, err
		}
		return &params, nil

	case ActionSimulateProcessActivity:
		var params SimulateProcessActivityParams
		if err := json.Unmarshal(s.Parameters, &params); err != nil {
			return nil, err
		}
		return &params, nil

	default:
		return nil, nil
	}
}
