// Package compiler converts validated DSL scenarios into concrete jobs.
package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"cymbytes.com/cymconductor/internal/orchestrator/registry"
	"cymbytes.com/cymconductor/internal/orchestrator/storage"
	"cymbytes.com/cymconductor/pkg/dsl"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Compiler converts scenarios into jobs.
type Compiler struct {
	registry *registry.Registry
	logger   zerolog.Logger
}

// CompileResult holds the result of scenario compilation.
type CompileResult struct {
	ScenarioID string
	Jobs       []*storage.Job
	Steps      []*storage.ScenarioStep
	Errors     []string
}

// New creates a new compiler.
func New(reg *registry.Registry, logger zerolog.Logger) *Compiler {
	return &Compiler{
		registry: reg,
		logger:   logger.With().Str("component", "compiler").Logger(),
	}
}

// Compile converts a validated scenario into concrete jobs for specific agents.
func (c *Compiler) Compile(ctx context.Context, scenario *dsl.Scenario, labStartTime time.Time) (*CompileResult, error) {
	c.logger.Info().
		Str("scenario_id", scenario.ID).
		Str("scenario_name", scenario.Name).
		Int("step_count", len(scenario.Steps)).
		Msg("Compiling scenario")

	result := &CompileResult{
		ScenarioID: scenario.ID,
		Jobs:       make([]*storage.Job, 0),
		Steps:      make([]*storage.ScenarioStep, 0),
	}

	// Get all online agents
	agents, err := c.registry.GetOnlineAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get agents: %w", err)
	}

	if len(agents) == 0 {
		return nil, fmt.Errorf("no online agents available")
	}

	c.logger.Debug().Int("agent_count", len(agents)).Msg("Found online agents")

	// Process each step
	for _, step := range scenario.Steps {
		// Convert DSL step to storage step
		storageStep := &storage.ScenarioStep{
			ID:            step.ID,
			ScenarioID:    scenario.ID,
			StepOrder:     step.Order,
			ActionType:    string(step.ActionType),
			TargetLabels:  step.Target.Labels,
			TargetCount:   step.Target.Count,
			Parameters:    rawJSONToMap(step.Parameters),
			DelayBeforeMs: step.Timing.DelayBeforeMs,
			DelayAfterMs:  step.Timing.DelayAfterMs,
			JitterMs:      step.Timing.JitterMs,
		}
		result.Steps = append(result.Steps, storageStep)

		// Find matching agents
		matchingAgents := c.findMatchingAgents(agents, step.Target.Labels)
		if len(matchingAgents) == 0 {
			c.logger.Warn().
				Str("step_id", step.ID).
				Interface("labels", step.Target.Labels).
				Msg("No agents match step target labels")
			result.Errors = append(result.Errors, fmt.Sprintf("Step %d: no matching agents for labels %v", step.Order, step.Target.Labels))
			continue
		}

		// Select target agents based on count
		selectedAgents, err := c.selectTargetAgents(matchingAgents, step.Target.Count)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Step %d: %v", step.Order, err))
			continue
		}

		// Calculate scheduled time
		baseTime := labStartTime.Add(time.Duration(step.Timing.RelativeTimeSeconds) * time.Second)
		baseTime = baseTime.Add(time.Duration(step.Timing.DelayBeforeMs) * time.Millisecond)

		// Create jobs for each selected agent
		for _, agent := range selectedAgents {
			// Apply jitter
			jitter := time.Duration(0)
			if step.Timing.JitterMs > 0 {
				jitter = time.Duration(rand.Intn(step.Timing.JitterMs*2)-step.Timing.JitterMs) * time.Millisecond
			}

			scheduledAt := baseTime.Add(jitter)

			job := &storage.Job{
				ID:             uuid.New().String(),
				ScenarioID:     &scenario.ID,
				ScenarioStepID: &step.ID,
				AgentID:        agent.ID,
				ActionType:     string(step.ActionType),
				Parameters:     rawJSONToMap(step.Parameters),
				Status:         storage.JobStatusPending,
				Priority:       0, // Default priority
				ScheduledAt:    scheduledAt,
				MaxRetries:     3,
			}

			result.Jobs = append(result.Jobs, job)

			c.logger.Debug().
				Str("job_id", job.ID).
				Str("agent_id", agent.ID).
				Str("action", job.ActionType).
				Time("scheduled_at", scheduledAt).
				Msg("Created job")
		}
	}

	c.logger.Info().
		Str("scenario_id", scenario.ID).
		Int("total_jobs", len(result.Jobs)).
		Int("total_steps", len(result.Steps)).
		Int("errors", len(result.Errors)).
		Msg("Scenario compilation complete")

	return result, nil
}

// findMatchingAgents returns agents that match all the given labels.
func (c *Compiler) findMatchingAgents(agents []*storage.Agent, labels map[string]string) []*storage.Agent {
	var matching []*storage.Agent

	for _, agent := range agents {
		if matchesLabels(agent.Labels, labels) {
			matching = append(matching, agent)
		}
	}

	return matching
}

// matchesLabels checks if agent labels satisfy the selector.
func matchesLabels(agentLabels, selector map[string]string) bool {
	for key, value := range selector {
		if agentLabels[key] != value {
			return false
		}
	}
	return true
}

// selectTargetAgents selects agents based on the target count specification.
func (c *Compiler) selectTargetAgents(agents []*storage.Agent, count string) ([]*storage.Agent, error) {
	if len(agents) == 0 {
		return nil, fmt.Errorf("no agents available")
	}

	switch count {
	case "all":
		return agents, nil

	case "any":
		// Select a random single agent
		idx := rand.Intn(len(agents))
		return []*storage.Agent{agents[idx]}, nil

	default:
		// Parse as number
		n, err := strconv.Atoi(count)
		if err != nil {
			return nil, fmt.Errorf("invalid count: %s", count)
		}
		if n < 1 {
			return nil, fmt.Errorf("count must be positive: %d", n)
		}
		if n > len(agents) {
			c.logger.Warn().
				Int("requested", n).
				Int("available", len(agents)).
				Msg("Requested more agents than available, using all")
			return agents, nil
		}

		// Randomly select n agents
		shuffled := make([]*storage.Agent, len(agents))
		copy(shuffled, agents)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		return shuffled[:n], nil
	}
}

// rawJSONToMap converts json.RawMessage to map[string]interface{}.
func rawJSONToMap(raw []byte) map[string]interface{} {
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return make(map[string]interface{})
	}
	return result
}
