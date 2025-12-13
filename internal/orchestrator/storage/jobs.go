// Package storage provides SQLite database access for the orchestrator.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Job represents a job record in the database.
type Job struct {
	ID             string
	ScenarioID     *string
	ScenarioStepID *string
	AgentID        string
	ActionType     string
	Parameters     map[string]interface{}
	Status         string
	Priority       int
	ScheduledAt    time.Time
	AssignedAt     *time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
	Result         map[string]interface{}
	ErrorMessage   *string
	RetryCount     int
	MaxRetries     int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// JobStatus constants
const (
	JobStatusPending   = "pending"
	JobStatusAssigned  = "assigned"
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
	JobStatusCancelled = "cancelled"
)

// CreateJob inserts a new job record.
func (d *DB) CreateJob(ctx context.Context, job *Job) error {
	params, err := json.Marshal(job.Parameters)
	if err != nil {
		return fmt.Errorf("failed to marshal parameters: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO jobs (id, scenario_id, scenario_step_id, agent_id, action_type, parameters,
		                  status, priority, scheduled_at, max_retries)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.ID, job.ScenarioID, job.ScenarioStepID, job.AgentID, job.ActionType,
		string(params), job.Status, job.Priority, job.ScheduledAt, job.MaxRetries)

	if err != nil {
		return fmt.Errorf("failed to insert job: %w", err)
	}

	d.logger.Debug().
		Str("job_id", job.ID).
		Str("agent_id", job.AgentID).
		Str("action", job.ActionType).
		Msg("Job created")

	return nil
}

// CreateJobBatch inserts multiple jobs in a single transaction.
func (d *DB) CreateJobBatch(ctx context.Context, jobs []*Job) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO jobs (id, scenario_id, scenario_step_id, agent_id, action_type, parameters,
		                  status, priority, scheduled_at, max_retries)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, job := range jobs {
		params, err := json.Marshal(job.Parameters)
		if err != nil {
			return fmt.Errorf("failed to marshal parameters for job %s: %w", job.ID, err)
		}

		_, err = stmt.ExecContext(ctx, job.ID, job.ScenarioID, job.ScenarioStepID, job.AgentID,
			job.ActionType, string(params), job.Status, job.Priority, job.ScheduledAt, job.MaxRetries)
		if err != nil {
			return fmt.Errorf("failed to insert job %s: %w", job.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	d.logger.Info().Int("count", len(jobs)).Msg("Jobs batch created")
	return nil
}

// GetJob retrieves a job by ID.
func (d *DB) GetJob(ctx context.Context, id string) (*Job, error) {
	var job Job
	var paramsJSON, resultJSON sql.NullString

	err := d.db.QueryRowContext(ctx, `
		SELECT id, scenario_id, scenario_step_id, agent_id, action_type, parameters,
		       status, priority, scheduled_at, assigned_at, started_at, completed_at,
		       result, error_message, retry_count, max_retries, created_at, updated_at
		FROM jobs WHERE id = ?
	`, id).Scan(
		&job.ID, &job.ScenarioID, &job.ScenarioStepID, &job.AgentID, &job.ActionType,
		&paramsJSON, &job.Status, &job.Priority, &job.ScheduledAt, &job.AssignedAt,
		&job.StartedAt, &job.CompletedAt, &resultJSON, &job.ErrorMessage,
		&job.RetryCount, &job.MaxRetries, &job.CreatedAt, &job.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	if paramsJSON.Valid {
		if err := json.Unmarshal([]byte(paramsJSON.String), &job.Parameters); err != nil {
			return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
		}
	}

	if resultJSON.Valid {
		if err := json.Unmarshal([]byte(resultJSON.String), &job.Result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal result: %w", err)
		}
	}

	return &job, nil
}

// GetNextJobsForAgent retrieves the next pending jobs for a specific agent.
// Jobs are selected based on:
// 1. status = 'pending'
// 2. scheduled_at <= now
// 3. Ordered by priority DESC, scheduled_at ASC
func (d *DB) GetNextJobsForAgent(ctx context.Context, agentID string, limit int) ([]*Job, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, scenario_id, scenario_step_id, agent_id, action_type, parameters,
		       status, priority, scheduled_at, assigned_at, started_at, completed_at,
		       result, error_message, retry_count, max_retries, created_at, updated_at
		FROM jobs
		WHERE agent_id = ? AND status = ? AND scheduled_at <= CURRENT_TIMESTAMP
		ORDER BY priority DESC, scheduled_at ASC
		LIMIT ?
	`, agentID, JobStatusPending, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to get next jobs: %w", err)
	}
	defer rows.Close()

	return d.scanJobs(rows)
}

// AssignJobs marks jobs as assigned to an agent (changes status from pending to assigned).
func (d *DB) AssignJobs(ctx context.Context, jobIDs []string) error {
	if len(jobIDs) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE jobs SET status = ?, assigned_at = CURRENT_TIMESTAMP WHERE id = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, id := range jobIDs {
		if _, err := stmt.ExecContext(ctx, JobStatusAssigned, id); err != nil {
			return fmt.Errorf("failed to assign job %s: %w", id, err)
		}
	}

	return tx.Commit()
}

// UpdateJobStarted marks a job as running with a start time.
func (d *DB) UpdateJobStarted(ctx context.Context, id string, startedAt time.Time) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE jobs SET status = ?, started_at = ? WHERE id = ?
	`, JobStatusRunning, startedAt, id)

	if err != nil {
		return fmt.Errorf("failed to update job started: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("job not found: %s", id)
	}

	return nil
}

// UpdateJobCompleted marks a job as completed with results.
func (d *DB) UpdateJobCompleted(ctx context.Context, id string, completedAt time.Time, result map[string]interface{}) error {
	var resultJSON *string
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("failed to marshal result: %w", err)
		}
		s := string(data)
		resultJSON = &s
	}

	res, err := d.db.ExecContext(ctx, `
		UPDATE jobs SET status = ?, completed_at = ?, result = ? WHERE id = ?
	`, JobStatusCompleted, completedAt, resultJSON, id)

	if err != nil {
		return fmt.Errorf("failed to update job completed: %w", err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("job not found: %s", id)
	}

	d.logger.Debug().Str("job_id", id).Msg("Job completed")
	return nil
}

// UpdateJobFailed marks a job as failed with an error message.
func (d *DB) UpdateJobFailed(ctx context.Context, id string, completedAt time.Time, errorMsg string, retry bool) error {
	var status string
	var retryIncrement int

	if retry {
		// Check if we should retry
		job, err := d.GetJob(ctx, id)
		if err != nil {
			return err
		}
		if job == nil {
			return fmt.Errorf("job not found: %s", id)
		}

		if job.RetryCount < job.MaxRetries {
			status = JobStatusPending // Reset to pending for retry
			retryIncrement = 1
		} else {
			status = JobStatusFailed
		}
	} else {
		status = JobStatusFailed
	}

	res, err := d.db.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, completed_at = ?, error_message = ?, retry_count = retry_count + ?
		WHERE id = ?
	`, status, completedAt, errorMsg, retryIncrement, id)

	if err != nil {
		return fmt.Errorf("failed to update job failed: %w", err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("job not found: %s", id)
	}

	d.logger.Debug().
		Str("job_id", id).
		Str("status", status).
		Bool("retry", retry && status == JobStatusPending).
		Msg("Job failed")

	return nil
}

// CancelJobsForScenario cancels all pending jobs for a scenario.
func (d *DB) CancelJobsForScenario(ctx context.Context, scenarioID string) (int, error) {
	result, err := d.db.ExecContext(ctx, `
		UPDATE jobs SET status = ? WHERE scenario_id = ? AND status IN (?, ?)
	`, JobStatusCancelled, scenarioID, JobStatusPending, JobStatusAssigned)

	if err != nil {
		return 0, fmt.Errorf("failed to cancel jobs: %w", err)
	}

	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// ListJobsByScenario retrieves all jobs for a scenario.
func (d *DB) ListJobsByScenario(ctx context.Context, scenarioID string) ([]*Job, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, scenario_id, scenario_step_id, agent_id, action_type, parameters,
		       status, priority, scheduled_at, assigned_at, started_at, completed_at,
		       result, error_message, retry_count, max_retries, created_at, updated_at
		FROM jobs WHERE scenario_id = ?
		ORDER BY scheduled_at ASC
	`, scenarioID)

	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	return d.scanJobs(rows)
}

// ListJobsByAgent retrieves jobs for a specific agent, optionally filtered by status.
func (d *DB) ListJobsByAgent(ctx context.Context, agentID string, status string, limit int) ([]*Job, error) {
	var query string
	var args []interface{}

	if status != "" {
		query = `
			SELECT id, scenario_id, scenario_step_id, agent_id, action_type, parameters,
			       status, priority, scheduled_at, assigned_at, started_at, completed_at,
			       result, error_message, retry_count, max_retries, created_at, updated_at
			FROM jobs WHERE agent_id = ? AND status = ?
			ORDER BY created_at DESC
			LIMIT ?
		`
		args = []interface{}{agentID, status, limit}
	} else {
		query = `
			SELECT id, scenario_id, scenario_step_id, agent_id, action_type, parameters,
			       status, priority, scheduled_at, assigned_at, started_at, completed_at,
			       result, error_message, retry_count, max_retries, created_at, updated_at
			FROM jobs WHERE agent_id = ?
			ORDER BY created_at DESC
			LIMIT ?
		`
		args = []interface{}{agentID, limit}
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	return d.scanJobs(rows)
}

// CountJobsByStatus returns job counts grouped by status.
func (d *DB) CountJobsByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM jobs GROUP BY status
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to count jobs: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}

	return counts, rows.Err()
}

// GetScenarioJobStats returns job statistics for a scenario.
func (d *DB) GetScenarioJobStats(ctx context.Context, scenarioID string) (total, completed, failed, running, pending int, err error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM jobs WHERE scenario_id = ? GROUP BY status
	`, scenarioID)
	if err != nil {
		return 0, 0, 0, 0, 0, fmt.Errorf("failed to get job stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return 0, 0, 0, 0, 0, err
		}
		total += count
		switch status {
		case JobStatusCompleted:
			completed = count
		case JobStatusFailed:
			failed = count
		case JobStatusRunning:
			running = count
		case JobStatusPending, JobStatusAssigned:
			pending += count
		}
	}

	return total, completed, failed, running, pending, rows.Err()
}

// scanJobs is a helper to scan multiple job rows.
func (d *DB) scanJobs(rows *sql.Rows) ([]*Job, error) {
	var jobs []*Job

	for rows.Next() {
		var job Job
		var paramsJSON, resultJSON sql.NullString

		if err := rows.Scan(
			&job.ID, &job.ScenarioID, &job.ScenarioStepID, &job.AgentID, &job.ActionType,
			&paramsJSON, &job.Status, &job.Priority, &job.ScheduledAt, &job.AssignedAt,
			&job.StartedAt, &job.CompletedAt, &resultJSON, &job.ErrorMessage,
			&job.RetryCount, &job.MaxRetries, &job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}

		if paramsJSON.Valid {
			if err := json.Unmarshal([]byte(paramsJSON.String), &job.Parameters); err != nil {
				return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
			}
		}

		if resultJSON.Valid {
			if err := json.Unmarshal([]byte(resultJSON.String), &job.Result); err != nil {
				return nil, fmt.Errorf("failed to unmarshal result: %w", err)
			}
		}

		jobs = append(jobs, &job)
	}

	return jobs, rows.Err()
}

// DeleteJob removes a job record.
func (d *DB) DeleteJob(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("job not found: %s", id)
	}

	return nil
}

// CleanupOldJobs removes completed/failed jobs older than the specified duration.
func (d *DB) CleanupOldJobs(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan)

	result, err := d.db.ExecContext(ctx, `
		DELETE FROM jobs
		WHERE status IN (?, ?, ?) AND completed_at < ?
	`, JobStatusCompleted, JobStatusFailed, JobStatusCancelled, cutoff)

	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old jobs: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		d.logger.Info().Int64("count", rows).Dur("older_than", olderThan).Msg("Cleaned up old jobs")
	}

	return int(rows), nil
}
