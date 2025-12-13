// Package registry provides agent registration and tracking functionality.
package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cymbytes.com/cymconductor/internal/orchestrator/storage"
	"cymbytes.com/cymconductor/pkg/protocol"
	"github.com/rs/zerolog"
)

// Registry manages registered agents and their status.
type Registry struct {
	db     *storage.DB
	logger zerolog.Logger

	// Configuration
	heartbeatTimeout time.Duration
	cleanupInterval  time.Duration

	// In-memory cache for quick lookups
	cache     map[string]*CachedAgent
	cacheMu   sync.RWMutex
	cacheTime time.Time

	// Shutdown
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// CachedAgent is a lightweight in-memory representation of an agent.
type CachedAgent struct {
	ID              string
	LabHostID       string
	Hostname        string
	IPAddress       string
	Labels          map[string]string
	Status          string
	LastHeartbeatAt time.Time
}

// Config holds registry configuration.
type Config struct {
	// HeartbeatTimeout is how long before an agent is considered offline
	HeartbeatTimeout time.Duration

	// CleanupInterval is how often to check for stale agents
	CleanupInterval time.Duration

	// CacheTTL is how long to cache agent data
	CacheTTL time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		HeartbeatTimeout: 30 * time.Second,
		CleanupInterval:  60 * time.Second,
		CacheTTL:         5 * time.Second,
	}
}

// New creates a new agent registry.
func New(db *storage.DB, cfg Config, logger zerolog.Logger) *Registry {
	return &Registry{
		db:               db,
		logger:           logger.With().Str("component", "registry").Logger(),
		heartbeatTimeout: cfg.HeartbeatTimeout,
		cleanupInterval:  cfg.CleanupInterval,
		cache:            make(map[string]*CachedAgent),
		stopCh:           make(chan struct{}),
	}
}

// Start begins background tasks (stale agent cleanup).
func (r *Registry) Start(ctx context.Context) {
	r.logger.Info().
		Dur("heartbeat_timeout", r.heartbeatTimeout).
		Dur("cleanup_interval", r.cleanupInterval).
		Msg("Starting agent registry")

	r.wg.Add(1)
	go r.cleanupLoop(ctx)
}

// Stop halts background tasks.
func (r *Registry) Stop() {
	r.logger.Info().Msg("Stopping agent registry")
	close(r.stopCh)
	r.wg.Wait()
}

// cleanupLoop periodically marks stale agents as offline.
func (r *Registry) cleanupLoop(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			if err := r.cleanupStaleAgents(ctx); err != nil {
				r.logger.Error().Err(err).Msg("Failed to cleanup stale agents")
			}
		}
	}
}

func (r *Registry) cleanupStaleAgents(ctx context.Context) error {
	count, err := r.db.MarkStaleAgentsOffline(ctx, r.heartbeatTimeout)
	if err != nil {
		return err
	}

	if count > 0 {
		// Invalidate cache
		r.cacheMu.Lock()
		r.cache = make(map[string]*CachedAgent)
		r.cacheMu.Unlock()
	}

	return nil
}

// RegisterAgent handles new agent registration.
func (r *Registry) RegisterAgent(ctx context.Context, req *protocol.RegisterAgentRequest) (*protocol.RegisterAgentResponse, error) {
	r.logger.Info().
		Str("agent_id", req.AgentID).
		Str("lab_host_id", req.LabHostID).
		Str("hostname", req.Hostname).
		Str("ip", req.IPAddress).
		Msg("Agent registration request")

	// Check if agent already exists
	existing, err := r.db.GetAgent(ctx, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing agent: %w", err)
	}

	now := time.Now()

	if existing != nil {
		// Agent re-registering (e.g., after restart)
		if err := r.db.UpdateAgentHeartbeat(ctx, req.AgentID, storage.AgentStatusOnline, req.IPAddress); err != nil {
			return nil, fmt.Errorf("failed to update agent: %w", err)
		}
		r.logger.Info().Str("agent_id", req.AgentID).Msg("Agent re-registered")
	} else {
		// New agent
		agent := &storage.Agent{
			ID:              req.AgentID,
			LabHostID:       req.LabHostID,
			Hostname:        req.Hostname,
			IPAddress:       req.IPAddress,
			Labels:          req.Labels,
			Version:         req.Version,
			Status:          storage.AgentStatusOnline,
			LastHeartbeatAt: now,
			RegisteredAt:    now,
		}

		if err := r.db.CreateAgent(ctx, agent); err != nil {
			return nil, fmt.Errorf("failed to create agent: %w", err)
		}
	}

	// Update cache
	r.updateCache(req.AgentID, &CachedAgent{
		ID:              req.AgentID,
		LabHostID:       req.LabHostID,
		Hostname:        req.Hostname,
		IPAddress:       req.IPAddress,
		Labels:          req.Labels,
		Status:          storage.AgentStatusOnline,
		LastHeartbeatAt: now,
	})

	return &protocol.RegisterAgentResponse{
		AgentID:             req.AgentID,
		RegisteredAt:        now,
		HeartbeatIntervalMs: int(r.heartbeatTimeout.Milliseconds() / 2), // Recommend heartbeat at half timeout
		Config: &protocol.AgentConfig{
			MaxConcurrentJobs: 3,
			LogLevel:          "info",
		},
	}, nil
}

// ProcessHeartbeat handles agent heartbeat.
func (r *Registry) ProcessHeartbeat(ctx context.Context, agentID string, req *protocol.HeartbeatRequest) (*protocol.HeartbeatResponse, error) {
	// Verify agent exists
	agent, err := r.db.GetAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	// Map request status to storage status
	status := storage.AgentStatusOnline
	if req.Status == "error" {
		status = storage.AgentStatusError
	}

	// Update heartbeat
	if err := r.db.UpdateAgentHeartbeat(ctx, agentID, status, agent.IPAddress); err != nil {
		return nil, fmt.Errorf("failed to update heartbeat: %w", err)
	}

	// Update cache
	r.updateCacheHeartbeat(agentID, status)

	return &protocol.HeartbeatResponse{
		Acknowledged: true,
		ServerTime:   time.Now(),
		Commands:     nil, // No commands for now
	}, nil
}

// GetAgent retrieves an agent by ID.
func (r *Registry) GetAgent(ctx context.Context, agentID string) (*storage.Agent, error) {
	return r.db.GetAgent(ctx, agentID)
}

// GetOnlineAgents returns all online agents.
func (r *Registry) GetOnlineAgents(ctx context.Context) ([]*storage.Agent, error) {
	return r.db.ListAgents(ctx, storage.AgentStatusOnline)
}

// GetAgentsByLabels returns agents matching the given labels.
func (r *Registry) GetAgentsByLabels(ctx context.Context, labels map[string]string) ([]*storage.Agent, error) {
	return r.db.ListAgentsByLabels(ctx, labels)
}

// ListAgents returns all agents.
func (r *Registry) ListAgents(ctx context.Context) ([]*storage.Agent, error) {
	return r.db.ListAgents(ctx, "")
}

// CountAgents returns agent counts.
func (r *Registry) CountAgents(ctx context.Context) (total, online, offline int, err error) {
	total, err = r.db.CountAgents(ctx, "")
	if err != nil {
		return 0, 0, 0, err
	}

	online, err = r.db.CountAgents(ctx, storage.AgentStatusOnline)
	if err != nil {
		return 0, 0, 0, err
	}

	offline, err = r.db.CountAgents(ctx, storage.AgentStatusOffline)
	if err != nil {
		return 0, 0, 0, err
	}

	return total, online, offline, nil
}

// AgentExists checks if an agent is registered.
func (r *Registry) AgentExists(ctx context.Context, agentID string) (bool, error) {
	return r.db.AgentExists(ctx, agentID)
}

// DeleteAgent removes an agent registration.
func (r *Registry) DeleteAgent(ctx context.Context, agentID string) error {
	if err := r.db.DeleteAgent(ctx, agentID); err != nil {
		return err
	}

	// Remove from cache
	r.cacheMu.Lock()
	delete(r.cache, agentID)
	r.cacheMu.Unlock()

	return nil
}

// ============================================================
// Cache operations
// ============================================================

func (r *Registry) updateCache(agentID string, agent *CachedAgent) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	r.cache[agentID] = agent
}

func (r *Registry) updateCacheHeartbeat(agentID string, status string) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if agent, ok := r.cache[agentID]; ok {
		agent.Status = status
		agent.LastHeartbeatAt = time.Now()
	}
}

// GetCachedAgent returns a cached agent or nil if not cached.
func (r *Registry) GetCachedAgent(agentID string) *CachedAgent {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	return r.cache[agentID]
}

// RefreshCache reloads all agents into the cache.
func (r *Registry) RefreshCache(ctx context.Context) error {
	agents, err := r.db.ListAgents(ctx, "")
	if err != nil {
		return err
	}

	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	r.cache = make(map[string]*CachedAgent)
	for _, agent := range agents {
		r.cache[agent.ID] = &CachedAgent{
			ID:              agent.ID,
			LabHostID:       agent.LabHostID,
			Hostname:        agent.Hostname,
			IPAddress:       agent.IPAddress,
			Labels:          agent.Labels,
			Status:          agent.Status,
			LastHeartbeatAt: agent.LastHeartbeatAt,
		}
	}
	r.cacheTime = time.Now()

	return nil
}
