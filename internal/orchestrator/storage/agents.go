// Package storage provides SQLite database access for the orchestrator.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Agent represents an agent record in the database.
type Agent struct {
	ID              string
	LabHostID       string
	Hostname        string
	IPAddress       string
	Labels          map[string]string
	Version         string
	Status          string
	LastHeartbeatAt time.Time
	RegisteredAt    time.Time
	UpdatedAt       time.Time
}

// AgentStatus constants
const (
	AgentStatusOnline  = "online"
	AgentStatusOffline = "offline"
	AgentStatusError   = "error"
)

// CreateAgent inserts a new agent record.
func (d *DB) CreateAgent(ctx context.Context, agent *Agent) error {
	labels, err := json.Marshal(agent.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO agents (id, lab_host_id, hostname, ip_address, labels, version, status, last_heartbeat_at, registered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, agent.ID, agent.LabHostID, agent.Hostname, agent.IPAddress, string(labels),
		agent.Version, agent.Status, agent.LastHeartbeatAt, agent.RegisteredAt)

	if err != nil {
		return fmt.Errorf("failed to insert agent: %w", err)
	}

	d.logger.Info().
		Str("agent_id", agent.ID).
		Str("lab_host_id", agent.LabHostID).
		Str("hostname", agent.Hostname).
		Msg("Agent created")

	return nil
}

// GetAgent retrieves an agent by ID.
func (d *DB) GetAgent(ctx context.Context, id string) (*Agent, error) {
	var agent Agent
	var labelsJSON string

	err := d.db.QueryRowContext(ctx, `
		SELECT id, lab_host_id, hostname, ip_address, labels, version, status,
		       last_heartbeat_at, registered_at, updated_at
		FROM agents WHERE id = ?
	`, id).Scan(
		&agent.ID, &agent.LabHostID, &agent.Hostname, &agent.IPAddress,
		&labelsJSON, &agent.Version, &agent.Status,
		&agent.LastHeartbeatAt, &agent.RegisteredAt, &agent.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent: %w", err)
	}

	if err := json.Unmarshal([]byte(labelsJSON), &agent.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
	}

	return &agent, nil
}

// GetAgentByLabHostID retrieves an agent by lab host ID.
func (d *DB) GetAgentByLabHostID(ctx context.Context, labHostID string) (*Agent, error) {
	var agent Agent
	var labelsJSON string

	err := d.db.QueryRowContext(ctx, `
		SELECT id, lab_host_id, hostname, ip_address, labels, version, status,
		       last_heartbeat_at, registered_at, updated_at
		FROM agents WHERE lab_host_id = ?
	`, labHostID).Scan(
		&agent.ID, &agent.LabHostID, &agent.Hostname, &agent.IPAddress,
		&labelsJSON, &agent.Version, &agent.Status,
		&agent.LastHeartbeatAt, &agent.RegisteredAt, &agent.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent by lab_host_id: %w", err)
	}

	if err := json.Unmarshal([]byte(labelsJSON), &agent.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
	}

	return &agent, nil
}

// UpdateAgentHeartbeat updates the agent's heartbeat timestamp and status.
func (d *DB) UpdateAgentHeartbeat(ctx context.Context, id string, status string, ipAddress string) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE agents
		SET last_heartbeat_at = CURRENT_TIMESTAMP,
		    status = ?,
		    ip_address = ?
		WHERE id = ?
	`, status, ipAddress, id)

	if err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("agent not found: %s", id)
	}

	d.logger.Debug().
		Str("agent_id", id).
		Str("status", status).
		Msg("Agent heartbeat updated")

	return nil
}

// UpdateAgentStatus updates the agent's status.
func (d *DB) UpdateAgentStatus(ctx context.Context, id string, status string) error {
	result, err := d.db.ExecContext(ctx, `
		UPDATE agents SET status = ? WHERE id = ?
	`, status, id)

	if err != nil {
		return fmt.Errorf("failed to update agent status: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("agent not found: %s", id)
	}

	return nil
}

// ListAgents retrieves all agents, optionally filtered by status.
func (d *DB) ListAgents(ctx context.Context, status string) ([]*Agent, error) {
	var query string
	var args []interface{}

	if status != "" {
		query = `
			SELECT id, lab_host_id, hostname, ip_address, labels, version, status,
			       last_heartbeat_at, registered_at, updated_at
			FROM agents WHERE status = ?
			ORDER BY registered_at DESC
		`
		args = []interface{}{status}
	} else {
		query = `
			SELECT id, lab_host_id, hostname, ip_address, labels, version, status,
			       last_heartbeat_at, registered_at, updated_at
			FROM agents
			ORDER BY registered_at DESC
		`
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		var agent Agent
		var labelsJSON string

		if err := rows.Scan(
			&agent.ID, &agent.LabHostID, &agent.Hostname, &agent.IPAddress,
			&labelsJSON, &agent.Version, &agent.Status,
			&agent.LastHeartbeatAt, &agent.RegisteredAt, &agent.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan agent: %w", err)
		}

		if err := json.Unmarshal([]byte(labelsJSON), &agent.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}

		agents = append(agents, &agent)
	}

	return agents, rows.Err()
}

// ListAgentsByLabels retrieves agents matching the given label selector.
func (d *DB) ListAgentsByLabels(ctx context.Context, labels map[string]string) ([]*Agent, error) {
	// Get all online agents first
	agents, err := d.ListAgents(ctx, AgentStatusOnline)
	if err != nil {
		return nil, err
	}

	// Filter by labels
	var matched []*Agent
	for _, agent := range agents {
		if matchLabels(agent.Labels, labels) {
			matched = append(matched, agent)
		}
	}

	return matched, nil
}

// matchLabels checks if agent labels match the selector.
// All selector labels must be present in agent labels with matching values.
func matchLabels(agentLabels, selector map[string]string) bool {
	for key, value := range selector {
		if agentLabels[key] != value {
			return false
		}
	}
	return true
}

// MarkStaleAgentsOffline marks agents as offline if they haven't sent a heartbeat recently.
func (d *DB) MarkStaleAgentsOffline(ctx context.Context, timeout time.Duration) (int, error) {
	cutoff := time.Now().Add(-timeout)

	result, err := d.db.ExecContext(ctx, `
		UPDATE agents
		SET status = ?
		WHERE status = ? AND last_heartbeat_at < ?
	`, AgentStatusOffline, AgentStatusOnline, cutoff)

	if err != nil {
		return 0, fmt.Errorf("failed to mark stale agents offline: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		d.logger.Info().
			Int64("count", rows).
			Dur("timeout", timeout).
			Msg("Marked stale agents as offline")
	}

	return int(rows), nil
}

// DeleteAgent removes an agent record.
func (d *DB) DeleteAgent(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM agents WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete agent: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("agent not found: %s", id)
	}

	d.logger.Info().Str("agent_id", id).Msg("Agent deleted")
	return nil
}

// CountAgents returns the total number of agents, optionally filtered by status.
func (d *DB) CountAgents(ctx context.Context, status string) (int, error) {
	var query string
	var args []interface{}

	if status != "" {
		query = "SELECT COUNT(*) FROM agents WHERE status = ?"
		args = []interface{}{status}
	} else {
		query = "SELECT COUNT(*) FROM agents"
	}

	var count int
	err := d.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count agents: %w", err)
	}

	return count, nil
}

// AgentExists checks if an agent with the given ID exists.
func (d *DB) AgentExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := d.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agents WHERE id = ?)", id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check agent existence: %w", err)
	}
	return exists, nil
}
