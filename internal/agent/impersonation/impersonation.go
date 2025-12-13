// Package impersonation provides user impersonation functionality for CymConductor agents.
// This package enables actions to be executed as different domain users.
package impersonation

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// LogonType constants matching Windows LOGON32_LOGON_* values
const (
	LogonInteractive = 2 // LOGON32_LOGON_INTERACTIVE - For interactive sessions
	LogonNetwork     = 3 // LOGON32_LOGON_NETWORK - For network access
	LogonBatch       = 4 // LOGON32_LOGON_BATCH - For batch jobs
)

// Config holds impersonation configuration.
type Config struct {
	// Enabled controls whether impersonation is available
	Enabled bool

	// Password is the shared password for all impersonation users
	Password string

	// AllowedUsers is the list of users this agent can impersonate
	AllowedUsers []string

	// AllowedUserPattern is an optional pattern for matching allowed users (e.g., "CYMBYTES\\*")
	AllowedUserPattern string
}

// Credential holds user credentials for impersonation.
type Credential struct {
	Domain   string
	Username string
	Password string
}

// Context represents an active impersonation session.
type Context struct {
	User      string
	LogonType int
	revertFn  func() error
}

// Revert ends the impersonation and returns to the service account.
func (c *Context) Revert() error {
	if c.revertFn != nil {
		return c.revertFn()
	}
	return nil
}

// Manager handles user impersonation operations.
type Manager struct {
	config Config
	logger zerolog.Logger
}

// NewManager creates a new impersonation manager.
func NewManager(cfg Config, logger zerolog.Logger) *Manager {
	return &Manager{
		config: cfg,
		logger: logger.With().Str("component", "impersonation").Logger(),
	}
}

// IsEnabled returns whether impersonation is enabled.
func (m *Manager) IsEnabled() bool {
	return m.config.Enabled
}

// IsUserAllowed checks if the given user is in the allowed list.
func (m *Manager) IsUserAllowed(user string) bool {
	if !m.config.Enabled {
		return false
	}

	// Normalize user to uppercase for comparison
	normalizedUser := strings.ToUpper(user)

	// Check explicit allowed list
	for _, allowed := range m.config.AllowedUsers {
		if strings.ToUpper(allowed) == normalizedUser {
			return true
		}
	}

	// Check pattern match (e.g., "CYMBYTES\\*")
	if m.config.AllowedUserPattern != "" {
		pattern := strings.ToUpper(m.config.AllowedUserPattern)
		if strings.HasSuffix(pattern, "\\*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(normalizedUser, prefix) {
				return true
			}
		}
	}

	return false
}

// ParseLogonType converts a string logon type to the Windows constant.
func ParseLogonType(logonType string) int {
	switch strings.ToLower(logonType) {
	case "network":
		return LogonNetwork
	case "batch":
		return LogonBatch
	case "interactive", "":
		return LogonInteractive
	default:
		return LogonInteractive
	}
}

// ParseDomainUser splits a full username into domain and username parts.
// Accepts formats: "DOMAIN\user" or "user@domain"
func ParseDomainUser(fullUsername string) (domain, username string) {
	// Check for DOMAIN\user format
	if idx := strings.Index(fullUsername, "\\"); idx > 0 {
		return fullUsername[:idx], fullUsername[idx+1:]
	}

	// Check for user@domain format
	if idx := strings.Index(fullUsername, "@"); idx > 0 {
		return fullUsername[idx+1:], fullUsername[:idx]
	}

	// No domain specified, return empty domain
	return "", fullUsername
}

// GetCredential returns credentials for the specified user.
func (m *Manager) GetCredential(user string) (*Credential, error) {
	if !m.IsUserAllowed(user) {
		return nil, fmt.Errorf("user %s is not in allowed list", user)
	}

	domain, username := ParseDomainUser(user)

	return &Credential{
		Domain:   domain,
		Username: username,
		Password: m.config.Password,
	}, nil
}

// RunAs executes a function in the context of the specified user.
// This is the main entry point for impersonation.
func (m *Manager) RunAs(ctx context.Context, user string, logonType int, fn func() error) error {
	if !m.config.Enabled {
		return fmt.Errorf("impersonation is disabled")
	}

	cred, err := m.GetCredential(user)
	if err != nil {
		return err
	}

	m.logger.Info().
		Str("user", user).
		Int("logon_type", logonType).
		Msg("Starting impersonation")

	// Platform-specific impersonation
	impCtx, err := m.impersonate(ctx, cred, logonType)
	if err != nil {
		m.logger.Error().Err(err).Str("user", user).Msg("Impersonation failed")
		return fmt.Errorf("impersonation failed: %w", err)
	}
	defer func() {
		if revertErr := impCtx.Revert(); revertErr != nil {
			m.logger.Error().Err(revertErr).Msg("Failed to revert impersonation")
		}
		m.logger.Info().Str("user", user).Msg("Impersonation reverted")
	}()

	// Execute the function as the impersonated user
	return fn()
}
