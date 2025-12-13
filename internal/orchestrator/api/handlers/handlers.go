// Package handlers provides HTTP request handlers for the orchestrator API.
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"cymbytes.com/cymconductor/internal/orchestrator/registry"
	"cymbytes.com/cymconductor/internal/orchestrator/scheduler"
	"cymbytes.com/cymconductor/internal/orchestrator/storage"
	"cymbytes.com/cymconductor/pkg/protocol"
)

// Handlers contains all API handlers.
type Handlers struct {
	db        *storage.DB
	registry  *registry.Registry
	scheduler *scheduler.Scheduler
	version   string
	startTime time.Time
	logger    zerolog.Logger
}

// New creates a new Handlers instance.
func New(db *storage.DB, reg *registry.Registry, sched *scheduler.Scheduler, version string, startTime time.Time, logger zerolog.Logger) *Handlers {
	return &Handlers{
		db:        db,
		registry:  reg,
		scheduler: sched,
		version:   version,
		startTime: startTime,
		logger:    logger.With().Str("component", "handlers").Logger(),
	}
}

// ============================================================
// Agent Handlers
// ============================================================

// RegisterAgent handles POST /api/agents/register
func (h *Handlers) RegisterAgent(w http.ResponseWriter, r *http.Request) {
	var req protocol.RegisterAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "Failed to parse request body")
		return
	}

	// Basic validation
	if req.AgentID == "" || req.LabHostID == "" || req.Hostname == "" {
		h.writeError(w, r, http.StatusBadRequest, "validation_failed", "Missing required fields")
		return
	}

	resp, err := h.registry.RegisterAgent(r.Context(), &req)
	if err != nil {
		h.logger.Error().Err(err).Str("agent_id", req.AgentID).Msg("Failed to register agent")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to register agent")
		return
	}

	h.writeJSON(w, http.StatusCreated, resp)
}

// AgentHeartbeat handles POST /api/agents/{agentID}/heartbeat
func (h *Handlers) AgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")

	var req protocol.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "Failed to parse request body")
		return
	}

	resp, err := h.registry.ProcessHeartbeat(r.Context(), agentID, &req)
	if err != nil {
		if err.Error() == "agent not found: "+agentID {
			h.writeError(w, r, http.StatusNotFound, "agent_not_found", "Agent must register before sending heartbeats")
			return
		}
		h.logger.Error().Err(err).Str("agent_id", agentID).Msg("Failed to process heartbeat")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to process heartbeat")
		return
	}

	h.writeJSON(w, http.StatusOK, resp)
}

// GetAgent handles GET /api/agents/{agentID}
func (h *Handlers) GetAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")

	agent, err := h.registry.GetAgent(r.Context(), agentID)
	if err != nil {
		h.logger.Error().Err(err).Str("agent_id", agentID).Msg("Failed to get agent")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to get agent")
		return
	}

	if agent == nil {
		h.writeError(w, r, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}

	h.writeJSON(w, http.StatusOK, protocol.AgentInfo{
		AgentID:         agent.ID,
		LabHostID:       agent.LabHostID,
		Hostname:        agent.Hostname,
		IPAddress:       agent.IPAddress,
		Labels:          agent.Labels,
		Status:          agent.Status,
		Version:         agent.Version,
		LastHeartbeatAt: agent.LastHeartbeatAt,
		RegisteredAt:    agent.RegisteredAt,
	})
}

// ListAgents handles GET /api/agents
func (h *Handlers) ListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.registry.ListAgents(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to list agents")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to list agents")
		return
	}

	var agentInfos []protocol.AgentInfo
	for _, agent := range agents {
		agentInfos = append(agentInfos, protocol.AgentInfo{
			AgentID:         agent.ID,
			LabHostID:       agent.LabHostID,
			Hostname:        agent.Hostname,
			IPAddress:       agent.IPAddress,
			Labels:          agent.Labels,
			Status:          agent.Status,
			Version:         agent.Version,
			LastHeartbeatAt: agent.LastHeartbeatAt,
			RegisteredAt:    agent.RegisteredAt,
		})
	}

	h.writeJSON(w, http.StatusOK, protocol.ListAgentsResponse{
		Agents: agentInfos,
		Total:  len(agentInfos),
	})
}

// ============================================================
// Job Handlers
// ============================================================

// GetNextJobs handles GET /api/agents/{agentID}/jobs/next
func (h *Handlers) GetNextJobs(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")

	// Parse max parameter (default: 5)
	maxStr := r.URL.Query().Get("max")
	max := 5
	if maxStr != "" {
		if m, err := strconv.Atoi(maxStr); err == nil && m > 0 && m <= 10 {
			max = m
		}
	}

	// Verify agent exists
	exists, err := h.registry.AgentExists(r.Context(), agentID)
	if err != nil {
		h.logger.Error().Err(err).Str("agent_id", agentID).Msg("Failed to check agent")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to check agent")
		return
	}
	if !exists {
		h.writeError(w, r, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}

	// Get next jobs from scheduler
	jobs, hasMore, err := h.scheduler.GetNextJobsForAgent(r.Context(), agentID, max)
	if err != nil {
		h.logger.Error().Err(err).Str("agent_id", agentID).Msg("Failed to get jobs")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to get jobs")
		return
	}

	if len(jobs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.writeJSON(w, http.StatusOK, protocol.GetJobsResponse{
		Jobs:    jobs,
		HasMore: hasMore,
	})
}

// SubmitJobResult handles POST /api/agents/{agentID}/jobs/{jobID}/result
func (h *Handlers) SubmitJobResult(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	jobID := chi.URLParam(r, "jobID")

	var req protocol.JobResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "Failed to parse request body")
		return
	}

	// Process the result through the scheduler
	retryScheduled, retryAt, err := h.scheduler.ProcessJobResult(r.Context(), agentID, jobID, &req)
	if err != nil {
		if err.Error() == "job not found: "+jobID {
			h.writeError(w, r, http.StatusNotFound, "job_not_found", "Job not found")
			return
		}
		h.logger.Error().Err(err).
			Str("agent_id", agentID).
			Str("job_id", jobID).
			Msg("Failed to process job result")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to process job result")
		return
	}

	resp := protocol.JobResultResponse{
		Acknowledged:   true,
		RetryScheduled: retryScheduled,
	}
	if retryAt != nil {
		resp.RetryAt = retryAt
	}

	h.writeJSON(w, http.StatusOK, resp)
}

// GetJobStats handles GET /api/jobs/stats
func (h *Handlers) GetJobStats(w http.ResponseWriter, r *http.Request) {
	counts, err := h.db.CountJobsByStatus(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to get job stats")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to get job stats")
		return
	}

	h.writeJSON(w, http.StatusOK, counts)
}

// ============================================================
// Scenario Handlers
// ============================================================

// CreateScenario handles POST /api/scenarios
func (h *Handlers) CreateScenario(w http.ResponseWriter, r *http.Request) {
	var req protocol.CreateScenarioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "Failed to parse request body")
		return
	}

	if req.Name == "" {
		h.writeError(w, r, http.StatusBadRequest, "validation_failed", "Name is required")
		return
	}

	// For now, return not implemented
	// This will be connected to the planner/compiler pipeline later
	h.writeError(w, r, http.StatusNotImplemented, "not_implemented", "Scenario creation via API coming soon")
}

// GetScenario handles GET /api/scenarios/{scenarioID}
func (h *Handlers) GetScenario(w http.ResponseWriter, r *http.Request) {
	scenarioID := chi.URLParam(r, "scenarioID")

	scenario, err := h.db.GetScenario(r.Context(), scenarioID)
	if err != nil {
		h.logger.Error().Err(err).Str("scenario_id", scenarioID).Msg("Failed to get scenario")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to get scenario")
		return
	}

	if scenario == nil {
		h.writeError(w, r, http.StatusNotFound, "scenario_not_found", "Scenario not found")
		return
	}

	h.writeJSON(w, http.StatusOK, scenario)
}

// GetScenarioStatus handles GET /api/scenarios/{scenarioID}/status
func (h *Handlers) GetScenarioStatus(w http.ResponseWriter, r *http.Request) {
	scenarioID := chi.URLParam(r, "scenarioID")

	scenario, err := h.db.GetScenario(r.Context(), scenarioID)
	if err != nil {
		h.logger.Error().Err(err).Str("scenario_id", scenarioID).Msg("Failed to get scenario")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to get scenario")
		return
	}

	if scenario == nil {
		h.writeError(w, r, http.StatusNotFound, "scenario_not_found", "Scenario not found")
		return
	}

	// Get job stats for progress
	total, completed, failed, running, pending, err := h.db.GetScenarioJobStats(r.Context(), scenarioID)
	if err != nil {
		h.logger.Error().Err(err).Str("scenario_id", scenarioID).Msg("Failed to get job stats")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to get job stats")
		return
	}

	var percentComplete float64
	if total > 0 {
		percentComplete = float64(completed+failed) / float64(total) * 100
	}

	var errMsg string
	if scenario.ErrorMessage != nil {
		errMsg = *scenario.ErrorMessage
	}

	resp := protocol.ScenarioStatusResponse{
		ScenarioID:   scenario.ID,
		Name:         scenario.Name,
		Status:       scenario.Status,
		ErrorMessage: errMsg,
		CreatedAt:    scenario.CreatedAt,
		UpdatedAt:    scenario.UpdatedAt,
		CompletedAt:  scenario.CompletedAt,
		Progress: &protocol.ScenarioProgress{
			TotalJobs:       total,
			CompletedJobs:   completed,
			FailedJobs:      failed,
			RunningJobs:     running,
			PendingJobs:     pending,
			PercentComplete: percentComplete,
		},
	}

	h.writeJSON(w, http.StatusOK, resp)
}

// ListScenarios handles GET /api/scenarios
func (h *Handlers) ListScenarios(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")

	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	scenarios, err := h.db.ListScenarios(r.Context(), status, limit)
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to list scenarios")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to list scenarios")
		return
	}

	h.writeJSON(w, http.StatusOK, scenarios)
}

// DeleteScenario handles DELETE /api/scenarios/{scenarioID}
func (h *Handlers) DeleteScenario(w http.ResponseWriter, r *http.Request) {
	scenarioID := chi.URLParam(r, "scenarioID")

	// Cancel any pending jobs first
	_, err := h.db.CancelJobsForScenario(r.Context(), scenarioID)
	if err != nil {
		h.logger.Error().Err(err).Str("scenario_id", scenarioID).Msg("Failed to cancel jobs")
	}

	if err := h.db.DeleteScenario(r.Context(), scenarioID); err != nil {
		if err.Error() == "scenario not found: "+scenarioID {
			h.writeError(w, r, http.StatusNotFound, "scenario_not_found", "Scenario not found")
			return
		}
		h.logger.Error().Err(err).Str("scenario_id", scenarioID).Msg("Failed to delete scenario")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to delete scenario")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Health Handlers
// ============================================================

// HealthCheck handles GET /health
func (h *Handlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	// Check database health
	dbStatus, dbErr := h.db.Health(r.Context())

	status := "healthy"
	if dbErr != nil {
		status = "unhealthy"
	}

	resp := protocol.HealthResponse{
		Status:        status,
		Version:       h.version,
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
		Components: map[string]protocol.ComponentHealth{
			"database": {
				Status:    dbStatus,
				LastCheck: time.Now(),
			},
		},
	}

	if dbErr != nil {
		resp.Components["database"] = protocol.ComponentHealth{
			Status:    "unhealthy",
			LastCheck: time.Now(),
			Details:   dbErr.Error(),
		}
	}

	statusCode := http.StatusOK
	if status != "healthy" {
		statusCode = http.StatusServiceUnavailable
	}

	h.writeJSON(w, statusCode, resp)
}

// ReadyCheck handles GET /ready
func (h *Handlers) ReadyCheck(w http.ResponseWriter, r *http.Request) {
	// Check if we can handle requests
	if err := h.db.Ping(r.Context()); err != nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "not_ready", "Database not available")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]bool{"ready": true})
}

// ============================================================
// Helper methods
// ============================================================

func (h *Handlers) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error().Err(err).Msg("Failed to encode JSON response")
	}
}

func (h *Handlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	resp := protocol.ErrorResponse{
		Error:     code,
		Message:   message,
		RequestID: middleware.GetReqID(r.Context()),
	}
	h.writeJSON(w, status, resp)
}

// ============================================================
// User Management Handlers (Impersonation Users)
// ============================================================

// CreateImpersonationUser handles POST /api/users
func (h *Handlers) CreateImpersonationUser(w http.ResponseWriter, r *http.Request) {
	var req protocol.CreateImpersonationUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "Failed to parse request body")
		return
	}

	// Validation
	if req.Username == "" || req.Domain == "" || req.SAMAccountName == "" {
		h.writeError(w, r, http.StatusBadRequest, "validation_failed", "Username, domain, and sam_account_name are required")
		return
	}

	user := &storage.ImpersonationUser{
		Username:       req.Username,
		Domain:         req.Domain,
		SAMAccountName: req.SAMAccountName,
		DisplayName:    req.DisplayName,
		Department:     req.Department,
		Title:          req.Title,
		AllowedHosts:   req.AllowedHosts,
	}

	if req.Persona != nil {
		user.Persona = &storage.UserPersona{
			TypicalApps:  req.Persona.TypicalApps,
			TypicalSites: req.Persona.TypicalSites,
			FileTypes:    req.Persona.FileTypes,
		}
		if req.Persona.WorkHours != nil {
			user.Persona.WorkHours = &storage.WorkHours{
				Start: req.Persona.WorkHours.Start,
				End:   req.Persona.WorkHours.End,
			}
		}
	}

	if err := h.db.CreateImpersonationUser(r.Context(), user); err != nil {
		h.logger.Error().Err(err).Str("username", req.Username).Msg("Failed to create impersonation user")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to create user")
		return
	}

	h.writeJSON(w, http.StatusCreated, protocol.ImpersonationUserResponse{
		ID:             user.ID,
		Username:       user.Username,
		Domain:         user.Domain,
		SAMAccountName: user.SAMAccountName,
		DisplayName:    user.DisplayName,
		Department:     user.Department,
		Title:          user.Title,
		AllowedHosts:   user.AllowedHosts,
		CreatedAt:      user.CreatedAt,
	})
}

// GetImpersonationUser handles GET /api/users/{userID}
func (h *Handlers) GetImpersonationUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")

	user, err := h.db.GetImpersonationUser(r.Context(), userID)
	if err != nil {
		h.logger.Error().Err(err).Str("user_id", userID).Msg("Failed to get impersonation user")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to get user")
		return
	}

	if user == nil {
		h.writeError(w, r, http.StatusNotFound, "user_not_found", "User not found")
		return
	}

	h.writeJSON(w, http.StatusOK, h.userToResponse(user))
}

// ListImpersonationUsers handles GET /api/users
func (h *Handlers) ListImpersonationUsers(w http.ResponseWriter, r *http.Request) {
	department := r.URL.Query().Get("department")

	var users []*storage.ImpersonationUser
	var err error

	if department != "" {
		users, err = h.db.ListImpersonationUsersByDepartment(r.Context(), department)
	} else {
		users, err = h.db.ListImpersonationUsers(r.Context())
	}

	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to list impersonation users")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to list users")
		return
	}

	var userResponses []protocol.ImpersonationUserResponse
	for _, user := range users {
		userResponses = append(userResponses, h.userToResponse(user))
	}

	h.writeJSON(w, http.StatusOK, protocol.ListImpersonationUsersResponse{
		Users: userResponses,
		Total: len(userResponses),
	})
}

// UpdateImpersonationUser handles PUT /api/users/{userID}
func (h *Handlers) UpdateImpersonationUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")

	var req protocol.UpdateImpersonationUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "Failed to parse request body")
		return
	}

	// Get existing user
	user, err := h.db.GetImpersonationUser(r.Context(), userID)
	if err != nil {
		h.logger.Error().Err(err).Str("user_id", userID).Msg("Failed to get impersonation user")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to get user")
		return
	}

	if user == nil {
		h.writeError(w, r, http.StatusNotFound, "user_not_found", "User not found")
		return
	}

	// Update fields
	if req.DisplayName != nil {
		user.DisplayName = *req.DisplayName
	}
	if req.Department != nil {
		user.Department = *req.Department
	}
	if req.Title != nil {
		user.Title = *req.Title
	}
	if req.AllowedHosts != nil {
		user.AllowedHosts = req.AllowedHosts
	}
	if req.Persona != nil {
		user.Persona = &storage.UserPersona{
			TypicalApps:  req.Persona.TypicalApps,
			TypicalSites: req.Persona.TypicalSites,
			FileTypes:    req.Persona.FileTypes,
		}
		if req.Persona.WorkHours != nil {
			user.Persona.WorkHours = &storage.WorkHours{
				Start: req.Persona.WorkHours.Start,
				End:   req.Persona.WorkHours.End,
			}
		}
	}

	if err := h.db.UpdateImpersonationUser(r.Context(), user); err != nil {
		h.logger.Error().Err(err).Str("user_id", userID).Msg("Failed to update impersonation user")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to update user")
		return
	}

	h.writeJSON(w, http.StatusOK, h.userToResponse(user))
}

// DeleteImpersonationUser handles DELETE /api/users/{userID}
func (h *Handlers) DeleteImpersonationUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")

	if err := h.db.DeleteImpersonationUser(r.Context(), userID); err != nil {
		if err.Error() == "impersonation user not found: "+userID {
			h.writeError(w, r, http.StatusNotFound, "user_not_found", "User not found")
			return
		}
		h.logger.Error().Err(err).Str("user_id", userID).Msg("Failed to delete impersonation user")
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to delete user")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// BulkCreateImpersonationUsers handles POST /api/users/bulk
func (h *Handlers) BulkCreateImpersonationUsers(w http.ResponseWriter, r *http.Request) {
	var req protocol.BulkCreateImpersonationUsersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "Failed to parse request body")
		return
	}

	if len(req.Users) == 0 {
		h.writeError(w, r, http.StatusBadRequest, "validation_failed", "At least one user is required")
		return
	}

	var created []protocol.ImpersonationUserResponse
	var errors []protocol.BulkUserError

	for i, userReq := range req.Users {
		user := &storage.ImpersonationUser{
			Username:       userReq.Username,
			Domain:         userReq.Domain,
			SAMAccountName: userReq.SAMAccountName,
			DisplayName:    userReq.DisplayName,
			Department:     userReq.Department,
			Title:          userReq.Title,
			AllowedHosts:   userReq.AllowedHosts,
		}

		if userReq.Persona != nil {
			user.Persona = &storage.UserPersona{
				TypicalApps:  userReq.Persona.TypicalApps,
				TypicalSites: userReq.Persona.TypicalSites,
				FileTypes:    userReq.Persona.FileTypes,
			}
			if userReq.Persona.WorkHours != nil {
				user.Persona.WorkHours = &storage.WorkHours{
					Start: userReq.Persona.WorkHours.Start,
					End:   userReq.Persona.WorkHours.End,
				}
			}
		}

		if err := h.db.CreateImpersonationUser(r.Context(), user); err != nil {
			errors = append(errors, protocol.BulkUserError{
				Index:    i,
				Username: userReq.Username,
				Error:    err.Error(),
			})
			continue
		}

		created = append(created, h.userToResponse(user))
	}

	h.writeJSON(w, http.StatusOK, protocol.BulkCreateImpersonationUsersResponse{
		Created: created,
		Errors:  errors,
		Total:   len(req.Users),
		Success: len(created),
		Failed:  len(errors),
	})
}

// userToResponse converts a storage user to a protocol response.
func (h *Handlers) userToResponse(user *storage.ImpersonationUser) protocol.ImpersonationUserResponse {
	resp := protocol.ImpersonationUserResponse{
		ID:             user.ID,
		Username:       user.Username,
		Domain:         user.Domain,
		SAMAccountName: user.SAMAccountName,
		DisplayName:    user.DisplayName,
		Department:     user.Department,
		Title:          user.Title,
		AllowedHosts:   user.AllowedHosts,
		CreatedAt:      user.CreatedAt,
		UpdatedAt:      user.UpdatedAt,
	}

	if user.Persona != nil {
		resp.Persona = &protocol.UserPersonaResponse{
			TypicalApps:  user.Persona.TypicalApps,
			TypicalSites: user.Persona.TypicalSites,
			FileTypes:    user.Persona.FileTypes,
		}
		if user.Persona.WorkHours != nil {
			resp.Persona.WorkHours = &protocol.WorkHoursResponse{
				Start: user.Persona.WorkHours.Start,
				End:   user.Persona.WorkHours.End,
			}
		}
	}

	return resp
}
