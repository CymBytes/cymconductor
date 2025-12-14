// Package storage provides SQLite database access for the orchestrator.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ImpersonationUser represents a domain user available for agent impersonation.
type ImpersonationUser struct {
	ID             string       `json:"id"`
	Username       string       `json:"username"`         // Full username (DOMAIN\user)
	Domain         string       `json:"domain"`           // Domain name
	SAMAccountName string       `json:"sam_account_name"` // SAM account name
	DisplayName    string       `json:"display_name,omitempty"`
	Department     string       `json:"department,omitempty"`
	Title          string       `json:"title,omitempty"`
	AllowedHosts   []string     `json:"allowed_hosts,omitempty"` // Lab host IDs where user can be impersonated
	Persona        *UserPersona `json:"persona,omitempty"`       // Behavior hints for AI
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// UserPersona contains behavior hints for the AI planner.
type UserPersona struct {
	WorkHours    *WorkHours `json:"work_hours,omitempty"`
	TypicalApps  []string   `json:"typical_apps,omitempty"`
	TypicalSites []string   `json:"typical_sites,omitempty"`
	FileTypes    []string   `json:"file_types,omitempty"`
}

// WorkHours defines when a user typically works.
type WorkHours struct {
	Start int `json:"start"` // Hour (0-23)
	End   int `json:"end"`   // Hour (0-23)
}

// CreateImpersonationUser creates a new impersonation user.
func (d *DB) CreateImpersonationUser(ctx context.Context, user *ImpersonationUser) error {
	if user.ID == "" {
		user.ID = uuid.New().String()
	}

	allowedHostsJSON, err := json.Marshal(user.AllowedHosts)
	if err != nil {
		return fmt.Errorf("failed to marshal allowed_hosts: %w", err)
	}

	var personaJSON []byte
	if user.Persona != nil {
		personaJSON, err = json.Marshal(user.Persona)
		if err != nil {
			return fmt.Errorf("failed to marshal persona: %w", err)
		}
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO impersonation_users (id, username, domain, sam_account_name, display_name, department, title, allowed_hosts, persona)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, user.ID, user.Username, user.Domain, user.SAMAccountName, user.DisplayName, user.Department, user.Title, string(allowedHostsJSON), string(personaJSON))

	if err != nil {
		return fmt.Errorf("failed to create impersonation user: %w", err)
	}

	d.logger.Info().
		Str("user_id", user.ID).
		Str("username", user.Username).
		Msg("Created impersonation user")

	return nil
}

// GetImpersonationUser retrieves an impersonation user by ID.
func (d *DB) GetImpersonationUser(ctx context.Context, id string) (*ImpersonationUser, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT id, username, domain, sam_account_name, display_name, department, title, allowed_hosts, persona, created_at, updated_at
		FROM impersonation_users
		WHERE id = ?
	`, id)

	return d.scanImpersonationUser(row)
}

// GetImpersonationUserByUsername retrieves an impersonation user by username.
func (d *DB) GetImpersonationUserByUsername(ctx context.Context, username string) (*ImpersonationUser, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT id, username, domain, sam_account_name, display_name, department, title, allowed_hosts, persona, created_at, updated_at
		FROM impersonation_users
		WHERE username = ?
	`, username)

	return d.scanImpersonationUser(row)
}

// ListImpersonationUsers returns all impersonation users.
func (d *DB) ListImpersonationUsers(ctx context.Context) ([]*ImpersonationUser, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, username, domain, sam_account_name, display_name, department, title, allowed_hosts, persona, created_at, updated_at
		FROM impersonation_users
		ORDER BY display_name, username
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list impersonation users: %w", err)
	}
	defer rows.Close()

	var users []*ImpersonationUser
	for rows.Next() {
		user, err := d.scanImpersonationUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	return users, rows.Err()
}

// ListImpersonationUsersByDepartment returns users in a specific department.
func (d *DB) ListImpersonationUsersByDepartment(ctx context.Context, department string) ([]*ImpersonationUser, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, username, domain, sam_account_name, display_name, department, title, allowed_hosts, persona, created_at, updated_at
		FROM impersonation_users
		WHERE department = ?
		ORDER BY display_name, username
	`, department)
	if err != nil {
		return nil, fmt.Errorf("failed to list impersonation users by department: %w", err)
	}
	defer rows.Close()

	var users []*ImpersonationUser
	for rows.Next() {
		user, err := d.scanImpersonationUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	return users, rows.Err()
}

// UpdateImpersonationUser updates an existing impersonation user.
func (d *DB) UpdateImpersonationUser(ctx context.Context, user *ImpersonationUser) error {
	allowedHostsJSON, err := json.Marshal(user.AllowedHosts)
	if err != nil {
		return fmt.Errorf("failed to marshal allowed_hosts: %w", err)
	}

	var personaJSON []byte
	if user.Persona != nil {
		personaJSON, err = json.Marshal(user.Persona)
		if err != nil {
			return fmt.Errorf("failed to marshal persona: %w", err)
		}
	}

	result, err := d.db.ExecContext(ctx, `
		UPDATE impersonation_users
		SET display_name = ?, department = ?, title = ?, allowed_hosts = ?, persona = ?
		WHERE id = ?
	`, user.DisplayName, user.Department, user.Title, string(allowedHostsJSON), string(personaJSON), user.ID)

	if err != nil {
		return fmt.Errorf("failed to update impersonation user: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("impersonation user not found: %s", user.ID)
	}

	return nil
}

// DeleteImpersonationUser deletes an impersonation user.
func (d *DB) DeleteImpersonationUser(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM impersonation_users WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete impersonation user: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("impersonation user not found: %s", id)
	}

	d.logger.Info().Str("user_id", id).Msg("Deleted impersonation user")
	return nil
}

// CountImpersonationUsers returns the total number of impersonation users.
func (d *DB) CountImpersonationUsers(ctx context.Context) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM impersonation_users").Scan(&count)
	return count, err
}

// Helper functions for scanning

func (d *DB) scanImpersonationUser(row *sql.Row) (*ImpersonationUser, error) {
	var user ImpersonationUser
	var allowedHostsJSON, personaJSON sql.NullString
	var displayName, department, title sql.NullString

	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Domain,
		&user.SAMAccountName,
		&displayName,
		&department,
		&title,
		&allowedHostsJSON,
		&personaJSON,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan impersonation user: %w", err)
	}

	user.DisplayName = displayName.String
	user.Department = department.String
	user.Title = title.String

	if allowedHostsJSON.Valid && allowedHostsJSON.String != "" {
		if err := json.Unmarshal([]byte(allowedHostsJSON.String), &user.AllowedHosts); err != nil {
			d.logger.Warn().Err(err).Str("user_id", user.ID).Msg("Failed to unmarshal allowed_hosts")
		}
	}

	if personaJSON.Valid && personaJSON.String != "" {
		user.Persona = &UserPersona{}
		if err := json.Unmarshal([]byte(personaJSON.String), user.Persona); err != nil {
			d.logger.Warn().Err(err).Str("user_id", user.ID).Msg("Failed to unmarshal persona")
		}
	}

	return &user, nil
}

func (d *DB) scanImpersonationUserRow(rows *sql.Rows) (*ImpersonationUser, error) {
	var user ImpersonationUser
	var allowedHostsJSON, personaJSON sql.NullString
	var displayName, department, title sql.NullString

	err := rows.Scan(
		&user.ID,
		&user.Username,
		&user.Domain,
		&user.SAMAccountName,
		&displayName,
		&department,
		&title,
		&allowedHostsJSON,
		&personaJSON,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan impersonation user row: %w", err)
	}

	user.DisplayName = displayName.String
	user.Department = department.String
	user.Title = title.String

	if allowedHostsJSON.Valid && allowedHostsJSON.String != "" {
		if err := json.Unmarshal([]byte(allowedHostsJSON.String), &user.AllowedHosts); err != nil {
			d.logger.Warn().Err(err).Str("user_id", user.ID).Msg("Failed to unmarshal allowed_hosts")
		}
	}

	if personaJSON.Valid && personaJSON.String != "" {
		user.Persona = &UserPersona{}
		if err := json.Unmarshal([]byte(personaJSON.String), user.Persona); err != nil {
			d.logger.Warn().Err(err).Str("user_id", user.ID).Msg("Failed to unmarshal persona")
		}
	}

	return &user, nil
}
