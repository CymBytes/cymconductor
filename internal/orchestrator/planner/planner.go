// Package planner provides AI-powered scenario generation from lab intents.
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cymbytes.com/cymconductor/internal/orchestrator/registry"
	"cymbytes.com/cymconductor/internal/orchestrator/storage"
	"cymbytes.com/cymconductor/internal/orchestrator/validator"
	"cymbytes.com/cymconductor/pkg/dsl"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Planner generates scenarios from lab intents using AI.
type Planner struct {
	client    *Client
	validator *validator.Validator
	registry  *registry.Registry
	db        *storage.DB
	logger    zerolog.Logger
}

// Config holds planner configuration.
type Config struct {
	APIKey      string
	Model       string
	MaxTokens   int
	Temperature float64
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Model:       "claude-sonnet-4-20250514",
		MaxTokens:   4096,
		Temperature: 0.3,
	}
}

// New creates a new planner.
func New(cfg Config, reg *registry.Registry, db *storage.DB, logger zerolog.Logger) (*Planner, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	client := NewClient(cfg.APIKey, cfg.Model, cfg.MaxTokens, cfg.Temperature)
	val := validator.New()

	return &Planner{
		client:    client,
		validator: val,
		registry:  reg,
		db:        db,
		logger:    logger.With().Str("component", "planner").Logger(),
	}, nil
}

// PlanResult holds the result of scenario planning.
type PlanResult struct {
	Scenario     *dsl.Scenario
	RawAIOutput  string
	Validation   *validator.ValidationResult
	ErrorMessage string
}

// Plan generates a scenario from an intent.
func (p *Planner) Plan(ctx context.Context, intent *dsl.Intent) (*PlanResult, error) {
	p.logger.Info().
		Str("lab_type", intent.LabType).
		Int("duration_minutes", intent.DurationMinutes).
		Str("difficulty", intent.Difficulty).
		Msg("Planning scenario from intent")

	result := &PlanResult{}

	// Get current agent inventory
	agents, err := p.registry.GetOnlineAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent inventory: %w", err)
	}

	// Build inventory summary for AI
	inventory := p.buildInventorySummary(agents)

	// Build user context for AI (personas for impersonation)
	userContext := p.buildUserContext(ctx)

	// Generate scenario using AI
	prompt := p.buildPrompt(intent, inventory, userContext)
	aiResponse, err := p.client.Generate(ctx, prompt)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("AI generation failed: %v", err)
		p.logger.Error().Err(err).Msg("AI generation failed")
		return result, nil
	}

	result.RawAIOutput = aiResponse

	// Parse AI response as JSON
	scenario, err := p.parseAIResponse(aiResponse)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("Failed to parse AI response: %v", err)
		p.logger.Error().Err(err).Msg("Failed to parse AI response")
		return result, nil
	}

	// Validate the scenario
	validation := p.validator.ValidateScenario(scenario)
	result.Validation = validation

	if !validation.Valid {
		result.ErrorMessage = fmt.Sprintf("Validation failed with %d errors", len(validation.Errors))
		p.logger.Warn().
			Int("error_count", len(validation.Errors)).
			Interface("errors", validation.Errors).
			Msg("Scenario validation failed")
		return result, nil
	}

	result.Scenario = scenario
	p.logger.Info().
		Str("scenario_id", scenario.ID).
		Int("step_count", len(scenario.Steps)).
		Msg("Scenario planned successfully")

	return result, nil
}

// buildInventorySummary creates a summary of available agents for the AI.
func (p *Planner) buildInventorySummary(agents []*storage.Agent) string {
	if len(agents) == 0 {
		return "No agents currently available."
	}

	// Group agents by role
	byRole := make(map[string][]string)
	for _, agent := range agents {
		role := agent.Labels["role"]
		if role == "" {
			role = "unknown"
		}
		byRole[role] = append(byRole[role], fmt.Sprintf("%s (%s, %s)", agent.LabHostID, agent.Labels["os"], agent.IPAddress))
	}

	summary := fmt.Sprintf("Available agents (%d total):\n", len(agents))
	for role, hosts := range byRole {
		summary += fmt.Sprintf("- %s: %v\n", role, hosts)
	}

	return summary
}

// buildUserContext creates a summary of available users for impersonation.
func (p *Planner) buildUserContext(ctx context.Context) string {
	if p.db == nil {
		return ""
	}

	users, err := p.db.ListImpersonationUsers(ctx)
	if err != nil {
		p.logger.Warn().Err(err).Msg("Failed to fetch impersonation users")
		return ""
	}

	if len(users) == 0 {
		return ""
	}

	// Group users by department
	byDept := make(map[string][]*storage.ImpersonationUser)
	for _, user := range users {
		dept := user.Department
		if dept == "" {
			dept = "General"
		}
		byDept[dept] = append(byDept[dept], user)
	}

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("The following %d domain users are available for impersonation:\n\n", len(users)))

	for dept, deptUsers := range byDept {
		summary.WriteString(fmt.Sprintf("### %s\n", dept))
		for _, user := range deptUsers {
			summary.WriteString(fmt.Sprintf("- **%s** (%s)", user.Username, user.DisplayName))
			if user.Title != "" {
				summary.WriteString(fmt.Sprintf(" - %s", user.Title))
			}
			summary.WriteString("\n")

			// Include persona hints if available
			if user.Persona != nil {
				if len(user.Persona.TypicalApps) > 0 {
					summary.WriteString(fmt.Sprintf("  - Typical apps: %s\n", strings.Join(user.Persona.TypicalApps, ", ")))
				}
				if len(user.Persona.TypicalSites) > 0 {
					summary.WriteString(fmt.Sprintf("  - Typical sites: %s\n", strings.Join(user.Persona.TypicalSites, ", ")))
				}
				if len(user.Persona.FileTypes) > 0 {
					summary.WriteString(fmt.Sprintf("  - File types: %s\n", strings.Join(user.Persona.FileTypes, ", ")))
				}
				if user.Persona.WorkHours != nil {
					summary.WriteString(fmt.Sprintf("  - Work hours: %d:00 - %d:00\n", user.Persona.WorkHours.Start, user.Persona.WorkHours.End))
				}
			}
		}
		summary.WriteString("\n")
	}

	summary.WriteString("Use these users with run_as to simulate multi-user activity. Match activities to departments.\n")

	return summary.String()
}

// buildPrompt constructs the AI prompt from intent, inventory, and user context.
func (p *Planner) buildPrompt(intent *dsl.Intent, inventory, userContext string) string {
	goalsStr := ""
	if len(intent.Goals) > 0 {
		goalsStr = "\nScenario goals:\n"
		for _, goal := range intent.Goals {
			goalsStr += fmt.Sprintf("- %s\n", goal)
		}
	}

	contextStr := ""
	if intent.Context != "" {
		contextStr = fmt.Sprintf("\nAdditional context:\n%s\n", intent.Context)
	}

	noiseStr := "medium"
	if intent.NoiseIntensity != "" {
		noiseStr = intent.NoiseIntensity
	}

	userContextSection := ""
	if userContext != "" {
		userContextSection = fmt.Sprintf("\n## Available Users for Impersonation\n\n%s\n", userContext)
	}

	return fmt.Sprintf(`%s

## Lab Intent

Lab type: %s
Duration: %d minutes
Difficulty: %s
Noise intensity: %s
%s%s

## Current Agent Inventory

%s
%s
## Task

Generate a complete scenario that simulates realistic user activity for this lab.
The scenario should create plausible noise and log entries that would appear in a real network.
Use the available users for impersonation to create realistic multi-user activity patterns.
Match user activities to their department and role when possible.

Return ONLY a valid JSON object matching the schema, no additional text.
Generate a unique UUID v4 for the scenario ID and each step ID.`,
		SystemPrompt,
		intent.LabType,
		intent.DurationMinutes,
		intent.Difficulty,
		noiseStr,
		goalsStr,
		contextStr,
		inventory,
		userContextSection,
	)
}

// parseAIResponse extracts and parses the scenario JSON from AI response.
func (p *Planner) parseAIResponse(response string) (*dsl.Scenario, error) {
	// Try to find JSON in the response
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in AI response")
	}

	var scenario dsl.Scenario
	if err := json.Unmarshal([]byte(jsonStr), &scenario); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Ensure scenario has an ID
	if scenario.ID == "" {
		scenario.ID = uuid.New().String()
	}

	// Ensure schema version
	if scenario.Schema == "" {
		scenario.Schema = dsl.SchemaVersion
	}

	return &scenario, nil
}

// extractJSON attempts to find and extract JSON from a string.
func extractJSON(s string) string {
	// Look for JSON object
	start := -1
	depth := 0

	for i, c := range s {
		if c == '{' {
			if start == -1 {
				start = i
			}
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 && start != -1 {
				return s[start : i+1]
			}
		}
	}

	return ""
}
