// Package storage provides SQLite database access for the orchestrator.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Scenario represents a scenario record in the database.
type Scenario struct {
	ID           string
	Name         string
	Description  *string
	Intent       string // JSON
	Source       string
	Status       string
	AIOutput     *string // JSON
	ValidatedDSL *string // JSON
	ErrorMessage *string
	ScoringRunID *string // Scoring engine run ID for event forwarding
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CompletedAt  *time.Time
}

// ScenarioStep represents a step within a scenario.
type ScenarioStep struct {
	ID            string
	ScenarioID    string
	StepOrder     int
	ActionType    string
	TargetLabels  map[string]string
	TargetCount   string
	Parameters    map[string]interface{}
	DelayBeforeMs int
	DelayAfterMs  int
	JitterMs      int
	CreatedAt     time.Time
}

// ScenarioStatus constants
const (
	ScenarioStatusPending   = "pending"
	ScenarioStatusPlanning  = "planning"
	ScenarioStatusValidated = "validated"
	ScenarioStatusCompiled  = "compiled"
	ScenarioStatusActive    = "active"
	ScenarioStatusCompleted = "completed"
	ScenarioStatusFailed    = "failed"
)

// ScenarioSource constants
const (
	ScenarioSourceAPI  = "api"
	ScenarioSourceFile = "file"
)

// CreateScenario inserts a new scenario record.
func (d *DB) CreateScenario(ctx context.Context, scenario *Scenario) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO scenarios (id, name, description, intent, source, status)
		VALUES (?, ?, ?, ?, ?, ?)
	`, scenario.ID, scenario.Name, scenario.Description, scenario.Intent, scenario.Source, scenario.Status)

	if err != nil {
		return fmt.Errorf("failed to insert scenario: %w", err)
	}

	d.logger.Info().
		Str("scenario_id", scenario.ID).
		Str("name", scenario.Name).
		Str("source", scenario.Source).
		Msg("Scenario created")

	return nil
}

// GetScenario retrieves a scenario by ID.
func (d *DB) GetScenario(ctx context.Context, id string) (*Scenario, error) {
	var scenario Scenario

	err := d.db.QueryRowContext(ctx, `
		SELECT id, name, description, intent, source, status, ai_output, validated_dsl,
		       error_message, scoring_run_id, created_at, updated_at, completed_at
		FROM scenarios WHERE id = ?
	`, id).Scan(
		&scenario.ID, &scenario.Name, &scenario.Description, &scenario.Intent,
		&scenario.Source, &scenario.Status, &scenario.AIOutput, &scenario.ValidatedDSL,
		&scenario.ErrorMessage, &scenario.ScoringRunID, &scenario.CreatedAt, &scenario.UpdatedAt, &scenario.CompletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get scenario: %w", err)
	}

	return &scenario, nil
}

// SetScenarioScoringRunID sets the scoring engine run ID for a scenario.
func (d *DB) SetScenarioScoringRunID(ctx context.Context, scenarioID string, runID string) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE scenarios SET scoring_run_id = ? WHERE id = ?
	`, runID, scenarioID)

	if err != nil {
		return fmt.Errorf("failed to set scoring run ID: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("scenario not found: %s", scenarioID)
	}

	d.logger.Info().
		Str("scenario_id", scenarioID).
		Str("scoring_run_id", runID).
		Msg("Scoring run ID set for scenario")

	return nil
}

// UpdateScenarioStatus updates the scenario status.
func (d *DB) UpdateScenarioStatus(ctx context.Context, id string, status string) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE scenarios SET status = ? WHERE id = ?
	`, status, id)

	if err != nil {
		return fmt.Errorf("failed to update scenario status: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("scenario not found: %s", id)
	}

	return nil
}

// UpdateScenarioAIOutput stores the raw AI output.
func (d *DB) UpdateScenarioAIOutput(ctx context.Context, id string, aiOutput string) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE scenarios SET ai_output = ?, status = ? WHERE id = ?
	`, aiOutput, ScenarioStatusPlanning, id)

	if err != nil {
		return fmt.Errorf("failed to update AI output: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("scenario not found: %s", id)
	}

	return nil
}

// UpdateScenarioValidatedDSL stores the validated DSL.
func (d *DB) UpdateScenarioValidatedDSL(ctx context.Context, id string, validatedDSL string) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE scenarios SET validated_dsl = ?, status = ? WHERE id = ?
	`, validatedDSL, ScenarioStatusValidated, id)

	if err != nil {
		return fmt.Errorf("failed to update validated DSL: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("scenario not found: %s", id)
	}

	return nil
}

// UpdateScenarioCompiled marks a scenario as compiled.
func (d *DB) UpdateScenarioCompiled(ctx context.Context, id string) error {
	return d.UpdateScenarioStatus(ctx, id, ScenarioStatusCompiled)
}

// UpdateScenarioActive marks a scenario as active.
func (d *DB) UpdateScenarioActive(ctx context.Context, id string) error {
	return d.UpdateScenarioStatus(ctx, id, ScenarioStatusActive)
}

// UpdateScenarioCompleted marks a scenario as completed.
func (d *DB) UpdateScenarioCompleted(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE scenarios SET status = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?
	`, ScenarioStatusCompleted, id)

	if err != nil {
		return fmt.Errorf("failed to complete scenario: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("scenario not found: %s", id)
	}

	d.logger.Info().Str("scenario_id", id).Msg("Scenario completed")
	return nil
}

// UpdateScenarioFailed marks a scenario as failed with an error message.
func (d *DB) UpdateScenarioFailed(ctx context.Context, id string, errorMsg string) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE scenarios SET status = ?, error_message = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?
	`, ScenarioStatusFailed, errorMsg, id)

	if err != nil {
		return fmt.Errorf("failed to update scenario failed: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("scenario not found: %s", id)
	}

	d.logger.Warn().Str("scenario_id", id).Str("error", errorMsg).Msg("Scenario failed")
	return nil
}

// ListScenarios retrieves scenarios, optionally filtered by status.
func (d *DB) ListScenarios(ctx context.Context, status string, limit int) ([]*Scenario, error) {
	var query string
	var args []interface{}

	if status != "" {
		query = `
			SELECT id, name, description, intent, source, status, ai_output, validated_dsl,
			       error_message, scoring_run_id, created_at, updated_at, completed_at
			FROM scenarios WHERE status = ?
			ORDER BY created_at DESC
			LIMIT ?
		`
		args = []interface{}{status, limit}
	} else {
		query = `
			SELECT id, name, description, intent, source, status, ai_output, validated_dsl,
			       error_message, scoring_run_id, created_at, updated_at, completed_at
			FROM scenarios
			ORDER BY created_at DESC
			LIMIT ?
		`
		args = []interface{}{limit}
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list scenarios: %w", err)
	}
	defer rows.Close()

	var scenarios []*Scenario
	for rows.Next() {
		var scenario Scenario
		if err := rows.Scan(
			&scenario.ID, &scenario.Name, &scenario.Description, &scenario.Intent,
			&scenario.Source, &scenario.Status, &scenario.AIOutput, &scenario.ValidatedDSL,
			&scenario.ErrorMessage, &scenario.ScoringRunID, &scenario.CreatedAt, &scenario.UpdatedAt, &scenario.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan scenario: %w", err)
		}
		scenarios = append(scenarios, &scenario)
	}

	return scenarios, rows.Err()
}

// ============================================================
// Scenario Steps
// ============================================================

// CreateScenarioStep inserts a step for a scenario.
func (d *DB) CreateScenarioStep(ctx context.Context, step *ScenarioStep) error {
	targetLabels, err := json.Marshal(step.TargetLabels)
	if err != nil {
		return fmt.Errorf("failed to marshal target labels: %w", err)
	}

	params, err := json.Marshal(step.Parameters)
	if err != nil {
		return fmt.Errorf("failed to marshal parameters: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO scenario_steps (id, scenario_id, step_order, action_type, target_labels,
		                            target_count, parameters, delay_before_ms, delay_after_ms, jitter_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, step.ID, step.ScenarioID, step.StepOrder, step.ActionType, string(targetLabels),
		step.TargetCount, string(params), step.DelayBeforeMs, step.DelayAfterMs, step.JitterMs)

	if err != nil {
		return fmt.Errorf("failed to insert scenario step: %w", err)
	}

	return nil
}

// CreateScenarioStepsBatch inserts multiple steps in a single transaction.
func (d *DB) CreateScenarioStepsBatch(ctx context.Context, steps []*ScenarioStep) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO scenario_steps (id, scenario_id, step_order, action_type, target_labels,
		                            target_count, parameters, delay_before_ms, delay_after_ms, jitter_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, step := range steps {
		targetLabels, err := json.Marshal(step.TargetLabels)
		if err != nil {
			return fmt.Errorf("failed to marshal target labels: %w", err)
		}

		params, err := json.Marshal(step.Parameters)
		if err != nil {
			return fmt.Errorf("failed to marshal parameters: %w", err)
		}

		_, err = stmt.ExecContext(ctx, step.ID, step.ScenarioID, step.StepOrder, step.ActionType,
			string(targetLabels), step.TargetCount, string(params), step.DelayBeforeMs,
			step.DelayAfterMs, step.JitterMs)
		if err != nil {
			return fmt.Errorf("failed to insert step: %w", err)
		}
	}

	return tx.Commit()
}

// GetScenarioSteps retrieves all steps for a scenario.
func (d *DB) GetScenarioSteps(ctx context.Context, scenarioID string) ([]*ScenarioStep, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, scenario_id, step_order, action_type, target_labels, target_count,
		       parameters, delay_before_ms, delay_after_ms, jitter_ms, created_at
		FROM scenario_steps WHERE scenario_id = ?
		ORDER BY step_order ASC
	`, scenarioID)

	if err != nil {
		return nil, fmt.Errorf("failed to get scenario steps: %w", err)
	}
	defer rows.Close()

	var steps []*ScenarioStep
	for rows.Next() {
		var step ScenarioStep
		var targetLabelsJSON, paramsJSON string

		if err := rows.Scan(
			&step.ID, &step.ScenarioID, &step.StepOrder, &step.ActionType,
			&targetLabelsJSON, &step.TargetCount, &paramsJSON,
			&step.DelayBeforeMs, &step.DelayAfterMs, &step.JitterMs, &step.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan step: %w", err)
		}

		if err := json.Unmarshal([]byte(targetLabelsJSON), &step.TargetLabels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal target labels: %w", err)
		}

		if err := json.Unmarshal([]byte(paramsJSON), &step.Parameters); err != nil {
			return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
		}

		steps = append(steps, &step)
	}

	return steps, rows.Err()
}

// GetScenarioStep retrieves a specific step by ID.
func (d *DB) GetScenarioStep(ctx context.Context, id string) (*ScenarioStep, error) {
	var step ScenarioStep
	var targetLabelsJSON, paramsJSON string

	err := d.db.QueryRowContext(ctx, `
		SELECT id, scenario_id, step_order, action_type, target_labels, target_count,
		       parameters, delay_before_ms, delay_after_ms, jitter_ms, created_at
		FROM scenario_steps WHERE id = ?
	`, id).Scan(
		&step.ID, &step.ScenarioID, &step.StepOrder, &step.ActionType,
		&targetLabelsJSON, &step.TargetCount, &paramsJSON,
		&step.DelayBeforeMs, &step.DelayAfterMs, &step.JitterMs, &step.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get scenario step: %w", err)
	}

	if err := json.Unmarshal([]byte(targetLabelsJSON), &step.TargetLabels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal target labels: %w", err)
	}

	if err := json.Unmarshal([]byte(paramsJSON), &step.Parameters); err != nil {
		return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
	}

	return &step, nil
}

// DeleteScenarioSteps removes all steps for a scenario.
func (d *DB) DeleteScenarioSteps(ctx context.Context, scenarioID string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM scenario_steps WHERE scenario_id = ?", scenarioID)
	if err != nil {
		return fmt.Errorf("failed to delete scenario steps: %w", err)
	}
	return nil
}

// CountScenarioSteps returns the number of steps in a scenario.
func (d *DB) CountScenarioSteps(ctx context.Context, scenarioID string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM scenario_steps WHERE scenario_id = ?
	`, scenarioID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count scenario steps: %w", err)
	}
	return count, nil
}

// DeleteScenario removes a scenario and its steps (cascading).
func (d *DB) DeleteScenario(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM scenarios WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete scenario: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("scenario not found: %s", id)
	}

	d.logger.Info().Str("scenario_id", id).Msg("Scenario deleted")
	return nil
}

// CountScenarios returns the total number of scenarios, optionally filtered by status.
func (d *DB) CountScenarios(ctx context.Context, status string) (int, error) {
	var query string
	var args []interface{}

	if status != "" {
		query = "SELECT COUNT(*) FROM scenarios WHERE status = ?"
		args = []interface{}{status}
	} else {
		query = "SELECT COUNT(*) FROM scenarios"
	}

	var count int
	err := d.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count scenarios: %w", err)
	}

	return count, nil
}
