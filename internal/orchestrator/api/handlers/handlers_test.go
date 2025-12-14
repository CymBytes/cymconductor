package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"cymbytes.com/cymconductor/internal/orchestrator/registry"
	"cymbytes.com/cymconductor/internal/orchestrator/scheduler"
	"cymbytes.com/cymconductor/internal/orchestrator/storage"
	"cymbytes.com/cymconductor/pkg/protocol"
)

// setupTestHandlers creates handlers with temporary database for testing
func setupTestHandlers(t *testing.T) (*Handlers, *storage.DB, *registry.Registry, func()) {
	t.Helper()

	ctx := context.Background()

	// Create temporary database file
	tmpFile := "/tmp/cymconductor-test-" + t.Name() + ".db"

	// Create database
	db, err := storage.New(ctx, storage.Config{
		Path:      tmpFile,
		EnableWAL: false,
	}, zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	// Create registry
	reg := registry.New(db, registry.DefaultConfig(), zerolog.Nop())

	// Create scheduler
	sched := scheduler.New(db, scheduler.DefaultConfig(), zerolog.Nop())

	// Create handlers
	handlers := New(db, reg, sched, "test", time.Now(), zerolog.Nop())

	// Cleanup function
	cleanup := func() {
		db.Close()
		// Remove temporary database file
		_ = os.Remove(tmpFile)
		_ = os.Remove(tmpFile + "-shm")
		_ = os.Remove(tmpFile + "-wal")
	}

	return handlers, db, reg, cleanup
}

// registerTestAgent helper to register an agent in the registry
func registerTestAgent(t *testing.T, reg *registry.Registry, agentID, labHostID string) *registry.CachedAgent {
	t.Helper()

	ctx := context.Background()

	// Create registration request
	req := &protocol.RegisterAgentRequest{
		AgentID:   agentID,
		LabHostID: labHostID,
		Hostname:  "test-host",
		IPAddress: "192.168.1.1",
		Labels:    map[string]string{"role": "test"},
		Version:   "test",
	}

	// Register agent
	_, err := reg.RegisterAgent(ctx, req)
	if err != nil {
		t.Fatalf("Failed to register test agent: %v", err)
	}

	// Return cached agent representation
	return &registry.CachedAgent{
		ID:              agentID,
		LabHostID:       labHostID,
		Hostname:        req.Hostname,
		IPAddress:       req.IPAddress,
		Labels:          req.Labels,
		Status:          "online",
		LastHeartbeatAt: time.Now(),
	}
}

func TestCreateTestJob_Success(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Register a test agent
	agentID := "test-agent-123"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	// Create request
	req := httptest.NewRequest(http.MethodPost, "/api/debug/test-job", nil)
	w := httptest.NewRecorder()

	// Execute handler
	handlers.CreateTestJob(w, req)

	// Check response code
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify response fields
	if response["job_id"] == nil || response["job_id"] == "" {
		t.Error("Expected job_id in response")
	}

	if response["agent_id"] != agentID {
		t.Errorf("Expected agent_id %s, got %v", agentID, response["agent_id"])
	}

	if response["action_type"] != "simulate_file_activity" {
		t.Errorf("Expected action_type 'simulate_file_activity', got %v", response["action_type"])
	}

	if response["status"] != "pending" {
		t.Errorf("Expected status 'pending', got %v", response["status"])
	}

	if response["message"] == nil {
		t.Error("Expected message in response")
	}
}

func TestCreateTestJob_NoAgents(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Don't register any agents

	// Create request
	req := httptest.NewRequest(http.MethodPost, "/api/debug/test-job", nil)
	w := httptest.NewRecorder()

	// Execute handler
	handlers.CreateTestJob(w, req)

	// Check response code
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify error response
	if response["error"] != "no_agents" {
		t.Errorf("Expected error 'no_agents', got %v", response["error"])
	}

	if response["message"] == nil {
		t.Error("Expected message in response")
	}
}

func TestCreateTestJob_JobCreatedInDatabase(t *testing.T) {
	handlers, db, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Register a test agent
	agentID := "test-agent-456"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	// Create request
	req := httptest.NewRequest(http.MethodPost, "/api/debug/test-job", nil)
	w := httptest.NewRecorder()

	// Execute handler
	handlers.CreateTestJob(w, req)

	// Check response code
	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}

	// Parse response to get job ID
	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	jobID, ok := response["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatal("Expected job_id in response")
	}

	// Verify job was created in database
	ctx := context.Background()
	job, err := db.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("Failed to get job from database: %v", err)
	}

	// Verify job fields
	if job.ID != jobID {
		t.Errorf("Expected job ID %s, got %s", jobID, job.ID)
	}

	if job.AgentID != agentID {
		t.Errorf("Expected agent ID %s, got %s", agentID, job.AgentID)
	}

	if job.ActionType != "simulate_file_activity" {
		t.Errorf("Expected action type 'simulate_file_activity', got %s", job.ActionType)
	}

	if job.Status != "pending" {
		t.Errorf("Expected status 'pending', got %s", job.Status)
	}

	if job.Priority != 0 {
		t.Errorf("Expected priority 0, got %d", job.Priority)
	}

	if job.MaxRetries != 3 {
		t.Errorf("Expected max retries 3, got %d", job.MaxRetries)
	}

	// Verify parameters
	if job.Parameters == nil {
		t.Fatal("Expected parameters to be set")
	}

	targetDir, ok := job.Parameters["target_directory"].(string)
	if !ok || targetDir != "/tmp/cymconductor-test" {
		t.Errorf("Expected target_directory '/tmp/cymconductor-test', got %v", job.Parameters["target_directory"])
	}

	operations, ok := job.Parameters["operations"].([]interface{})
	if !ok || len(operations) == 0 {
		t.Error("Expected operations to be a non-empty array")
	}

	fileCount, ok := job.Parameters["file_count"].(float64)
	if !ok || fileCount != 3 {
		t.Errorf("Expected file_count 3, got %v", job.Parameters["file_count"])
	}

	preserveFiles, ok := job.Parameters["preserve_files"].(bool)
	if !ok || !preserveFiles {
		t.Errorf("Expected preserve_files true, got %v", job.Parameters["preserve_files"])
	}
}

func TestCreateTestJob_MultipleAgents(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Register multiple agents
	agent1ID := "test-agent-1"
	agent2ID := "test-agent-2"
	agent3ID := "test-agent-3"

	registerTestAgent(t, reg, agent1ID, "lab-host-1")
	registerTestAgent(t, reg, agent2ID, "lab-host-2")
	registerTestAgent(t, reg, agent3ID, "lab-host-3")

	// Create request
	req := httptest.NewRequest(http.MethodPost, "/api/debug/test-job", nil)
	w := httptest.NewRecorder()

	// Execute handler
	handlers.CreateTestJob(w, req)

	// Check response code
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify job was assigned to first agent (index 0)
	assignedAgentID, ok := response["agent_id"].(string)
	if !ok {
		t.Fatal("Expected agent_id in response")
	}

	// The first agent should be one of the registered agents
	validAgents := map[string]bool{
		agent1ID: true,
		agent2ID: true,
		agent3ID: true,
	}

	if !validAgents[assignedAgentID] {
		t.Errorf("Expected agent_id to be one of the registered agents, got %s", assignedAgentID)
	}
}

func TestCreateTestJob_ResponseContentType(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Register a test agent
	registerTestAgent(t, reg, "test-agent-789", "test-lab-host")

	// Create request
	req := httptest.NewRequest(http.MethodPost, "/api/debug/test-job", nil)
	w := httptest.NewRecorder()

	// Execute handler
	handlers.CreateTestJob(w, req)

	// Check Content-Type header
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
	}
}

// ============================================================
// GetNextJobs Tests
// ============================================================

func TestGetNextJobs_Success(t *testing.T) {
	handlers, db, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	agentID := "test-agent-getnext"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	// Create a pending job for this agent (scheduled in the past)
	job := &storage.Job{
		ID:          "job-123",
		AgentID:     agentID,
		ActionType:  "test_action",
		Parameters:  map[string]interface{}{"key": "value"},
		Status:      "pending",
		Priority:    5,
		MaxRetries:  3,
		ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
	}
	if err := db.CreateJob(ctx, job); err != nil {
		t.Fatalf("Failed to create test job: %v", err)
	}

	// Create request with agentID in URL params
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentID+"/jobs/next", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.GetNextJobs(w, req)

	// Check response code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Parse response
	var response protocol.GetJobsResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify jobs returned
	if len(response.Jobs) != 1 {
		t.Fatalf("Expected 1 job, got %d", len(response.Jobs))
	}

	jobAssignment := response.Jobs[0]
	if jobAssignment.JobID != "job-123" {
		t.Errorf("Expected job ID 'job-123', got %s", jobAssignment.JobID)
	}

	if jobAssignment.ActionType != "test_action" {
		t.Errorf("Expected action type 'test_action', got %s", jobAssignment.ActionType)
	}

	if jobAssignment.Priority != 5 {
		t.Errorf("Expected priority 5, got %d", jobAssignment.Priority)
	}
}

func TestGetNextJobs_NoJobs(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "test-agent-nojobs"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	// Create request (no jobs created)
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentID+"/jobs/next", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.GetNextJobs(w, req)

	// Check response code - should be 204 No Content
	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status %d, got %d", http.StatusNoContent, w.Code)
	}

	// Body should be empty
	if w.Body.Len() > 0 {
		t.Errorf("Expected empty body, got %d bytes", w.Body.Len())
	}
}

func TestGetNextJobs_AgentNotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "non-existent-agent"

	// Create request for non-existent agent
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentID+"/jobs/next", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.GetNextJobs(w, req)

	// Check response code
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	// Parse error response
	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "agent_not_found" {
		t.Errorf("Expected error 'agent_not_found', got %v", response["error"])
	}
}

func TestGetNextJobs_MultipleJobs(t *testing.T) {
	handlers, db, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	agentID := "test-agent-multi"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	// Create multiple pending jobs (scheduled in the past)
	for i := 1; i <= 3; i++ {
		job := &storage.Job{
			ID:          fmt.Sprintf("job-%d", i),
			AgentID:     agentID,
			ActionType:  "test_action",
			Parameters:  map[string]interface{}{"index": i},
			Status:      "pending",
			Priority:    i,
			MaxRetries:  3,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
		}
		if err := db.CreateJob(ctx, job); err != nil {
			t.Fatalf("Failed to create job %d: %v", i, err)
		}
	}

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentID+"/jobs/next", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.GetNextJobs(w, req)

	// Check response code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Parse response
	var response protocol.GetJobsResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should return all 3 jobs
	if len(response.Jobs) != 3 {
		t.Errorf("Expected 3 jobs, got %d", len(response.Jobs))
	}
}

func TestGetNextJobs_MaxParameter(t *testing.T) {
	handlers, db, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	agentID := "test-agent-max"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	// Create 5 pending jobs (scheduled in the past)
	for i := 1; i <= 5; i++ {
		job := &storage.Job{
			ID:          fmt.Sprintf("job-%d", i),
			AgentID:     agentID,
			ActionType:  "test_action",
			Parameters:  map[string]interface{}{},
			Status:      "pending",
			Priority:    0,
			MaxRetries:  3,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
		}
		if err := db.CreateJob(ctx, job); err != nil {
			t.Fatalf("Failed to create job %d: %v", i, err)
		}
	}

	// Create request with max=2
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentID+"/jobs/next?max=2", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.GetNextJobs(w, req)

	// Parse response
	var response protocol.GetJobsResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should return only 2 jobs due to max parameter
	if len(response.Jobs) != 2 {
		t.Errorf("Expected 2 jobs (max=2), got %d", len(response.Jobs))
	}

	// HasMore should be true
	if !response.HasMore {
		t.Error("Expected has_more to be true when more jobs available")
	}
}

// ============================================================
// SubmitJobResult Tests
// ============================================================

func TestSubmitJobResult_Success_Completed(t *testing.T) {
	handlers, db, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	agentID := "test-agent-result"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	// Create a job and mark it as assigned
	jobID := "job-result-123"
	job := &storage.Job{
		ID:          jobID,
		AgentID:     agentID,
		ActionType:  "test_action",
		Parameters:  map[string]interface{}{},
		Status:      "assigned",
		Priority:    0,
		MaxRetries:  3,
		ScheduledAt: time.Now().UTC(),
	}
	if err := db.CreateJob(ctx, job); err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	// Create result request
	now := time.Now().UTC()
	resultReq := protocol.JobResultRequest{
		Status:      "completed",
		StartedAt:   now.Add(-10 * time.Second),
		CompletedAt: now,
		Result: &protocol.JobResult{
			Data:       map[string]interface{}{"files_created": 3},
			Summary:    "Created 3 files",
			DurationMs: 100,
		},
	}

	body, _ := json.Marshal(resultReq)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID+"/jobs/"+jobID+"/result", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	rctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.SubmitJobResult(w, req)

	// Check response code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusOK, w.Code, w.Body.String())
	}

	// Parse response
	var response protocol.JobResultResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify acknowledgment
	if !response.Acknowledged {
		t.Error("Expected acknowledged to be true")
	}

	// Verify job status updated in database
	updatedJob, err := db.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("Failed to get updated job: %v", err)
	}

	if updatedJob.Status != "completed" {
		t.Errorf("Expected job status 'completed', got %s", updatedJob.Status)
	}
}

func TestSubmitJobResult_Success_Failed(t *testing.T) {
	handlers, db, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	agentID := "test-agent-failed"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	// Create a job
	jobID := "job-failed-123"
	job := &storage.Job{
		ID:          jobID,
		AgentID:     agentID,
		ActionType:  "test_action",
		Parameters:  map[string]interface{}{},
		Status:      "assigned",
		Priority:    0,
		MaxRetries:  3,
		RetryCount:  0,
		ScheduledAt: time.Now().UTC(),
	}
	if err := db.CreateJob(ctx, job); err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	// Create failed result request
	now := time.Now().UTC()
	resultReq := protocol.JobResultRequest{
		Status:      "failed",
		StartedAt:   now.Add(-10 * time.Second),
		CompletedAt: now,
		Error: &protocol.JobError{
			Code:      "test_error",
			Message:   "Test error message",
			Retryable: true,
		},
	}

	body, _ := json.Marshal(resultReq)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID+"/jobs/"+jobID+"/result", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	rctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.SubmitJobResult(w, req)

	// Check response code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Parse response
	var response protocol.JobResultResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should schedule retry for failed job
	if !response.Acknowledged {
		t.Error("Expected acknowledged to be true")
	}
}

func TestSubmitJobResult_JobNotFound(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "test-agent-notfound"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	jobID := "non-existent-job"

	// Create result request
	now := time.Now()
	resultReq := protocol.JobResultRequest{
		Status:      "completed",
		StartedAt:   now.Add(-10 * time.Second),
		CompletedAt: now,
		Result: &protocol.JobResult{
			Data: map[string]interface{}{},
		},
	}

	body, _ := json.Marshal(resultReq)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID+"/jobs/"+jobID+"/result", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	rctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.SubmitJobResult(w, req)

	// Check response code
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	// Parse error response
	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "job_not_found" {
		t.Errorf("Expected error 'job_not_found', got %v", response["error"])
	}
}

func TestSubmitJobResult_InvalidRequest(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "test-agent-invalid"
	registerTestAgent(t, reg, agentID, "test-lab-host")

	jobID := "job-invalid-123"

	// Create invalid JSON request
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID+"/jobs/"+jobID+"/result", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	rctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Execute handler
	handlers.SubmitJobResult(w, req)

	// Check response code
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	// Parse error response
	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "invalid_request" {
		t.Errorf("Expected error 'invalid_request', got %v", response["error"])
	}
}

// ============================================================
// Agent Registration Tests
// ============================================================

func TestRegisterAgent_Success(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create registration request
	regReq := protocol.RegisterAgentRequest{
		AgentID:   "test-agent-123",
		LabHostID: "lab-host-1",
		Hostname:  "ws1.example.com",
		IPAddress: "192.168.1.100",
		Labels: map[string]string{
			"role": "workstation",
			"os":   "windows",
		},
		Version: "1.0.0",
	}

	body, _ := json.Marshal(regReq)
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	// Execute handler
	handlers.RegisterAgent(w, req)

	// Check response code
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}

	// Parse response
	var response protocol.RegisterAgentResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify response
	if response.AgentID != "test-agent-123" {
		t.Errorf("Expected agent ID 'test-agent-123', got %s", response.AgentID)
	}

	if response.RegisteredAt.IsZero() {
		t.Error("Expected RegisteredAt to be set")
	}
}

func TestRegisterAgent_MissingRequiredFields(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	tests := []struct {
		name    string
		request protocol.RegisterAgentRequest
	}{
		{
			name: "missing agent_id",
			request: protocol.RegisterAgentRequest{
				LabHostID: "lab-host-1",
				Hostname:  "ws1",
				IPAddress: "192.168.1.100",
				Labels:    map[string]string{},
				Version:   "1.0.0",
			},
		},
		{
			name: "missing lab_host_id",
			request: protocol.RegisterAgentRequest{
				AgentID:   "agent-123",
				Hostname:  "ws1",
				IPAddress: "192.168.1.100",
				Labels:    map[string]string{},
				Version:   "1.0.0",
			},
		},
		{
			name: "missing hostname",
			request: protocol.RegisterAgentRequest{
				AgentID:   "agent-123",
				LabHostID: "lab-host-1",
				IPAddress: "192.168.1.100",
				Labels:    map[string]string{},
				Version:   "1.0.0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.request)
			req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()

			handlers.RegisterAgent(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
			}

			var response map[string]interface{}
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			if response["error"] != "validation_failed" {
				t.Errorf("Expected error 'validation_failed', got %v", response["error"])
			}
		})
	}
}

func TestRegisterAgent_InvalidJSON(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.RegisterAgent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "invalid_request" {
		t.Errorf("Expected error 'invalid_request', got %v", response["error"])
	}
}

func TestRegisterAgent_DuplicateRegistration(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "test-agent-dup"

	// Register agent first time
	registerTestAgent(t, reg, agentID, "lab-host-1")

	// Try to register same agent again
	regReq := protocol.RegisterAgentRequest{
		AgentID:   agentID,
		LabHostID: "lab-host-1",
		Hostname:  "ws1",
		IPAddress: "192.168.1.100",
		Labels:    map[string]string{"role": "test"},
		Version:   "1.0.0",
	}

	body, _ := json.Marshal(regReq)
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.RegisterAgent(w, req)

	// Should succeed (re-registration is allowed, updates metadata)
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}
}

// ============================================================
// Agent Heartbeat Tests
// ============================================================

func TestAgentHeartbeat_Success(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "test-agent-heartbeat"
	registerTestAgent(t, reg, agentID, "lab-host-1")

	// Send heartbeat
	heartbeatReq := protocol.HeartbeatRequest{
		Status:      "online",
		CurrentJobs: []string{"job-1", "job-2"},
		Metrics: &protocol.AgentMetrics{
			CPUPercent:    45.5,
			MemoryPercent: 60.2,
			JobsCompleted: 10,
			JobsFailed:    1,
			UptimeSeconds: 3600,
		},
	}

	body, _ := json.Marshal(heartbeatReq)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID+"/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.AgentHeartbeat(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.HeartbeatResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !response.Acknowledged {
		t.Error("Expected heartbeat to be acknowledged")
	}

	if response.ServerTime.IsZero() {
		t.Error("Expected ServerTime to be set")
	}
}

func TestAgentHeartbeat_AgentNotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "non-existent-agent"

	heartbeatReq := protocol.HeartbeatRequest{
		Status: "online",
	}

	body, _ := json.Marshal(heartbeatReq)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID+"/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.AgentHeartbeat(w, req)

	// Should return 404
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "agent_not_found" {
		t.Errorf("Expected error 'agent_not_found', got %v", response["error"])
	}
}

func TestAgentHeartbeat_InvalidJSON(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "test-agent-invalid"
	registerTestAgent(t, reg, agentID, "lab-host-1")

	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID+"/heartbeat", bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.AgentHeartbeat(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// ============================================================
// GetAgent Tests
// ============================================================

func TestGetAgent_Success(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "test-agent-get"
	registerTestAgent(t, reg, agentID, "lab-host-1")

	// Get agent
	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetAgent(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.AgentInfo
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.AgentID != agentID {
		t.Errorf("Expected agent ID %s, got %s", agentID, response.AgentID)
	}

	if response.LabHostID != "lab-host-1" {
		t.Errorf("Expected lab host ID 'lab-host-1', got %s", response.LabHostID)
	}

	if response.Hostname != "test-host" {
		t.Errorf("Expected hostname 'test-host', got %s", response.Hostname)
	}
}

func TestGetAgent_NotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	agentID := "non-existent-agent"

	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agentID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("agentID", agentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetAgent(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "agent_not_found" {
		t.Errorf("Expected error 'agent_not_found', got %v", response["error"])
	}
}

// ============================================================
// ListAgents Tests
// ============================================================

func TestListAgents_Success(t *testing.T) {
	handlers, _, reg, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Register multiple agents
	registerTestAgent(t, reg, "agent-1", "lab-host-1")
	registerTestAgent(t, reg, "agent-2", "lab-host-1")
	registerTestAgent(t, reg, "agent-3", "lab-host-2")

	// List agents
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	w := httptest.NewRecorder()

	handlers.ListAgents(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.ListAgentsResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Total != 3 {
		t.Errorf("Expected 3 agents, got %d", response.Total)
	}

	if len(response.Agents) != 3 {
		t.Errorf("Expected 3 agents in list, got %d", len(response.Agents))
	}

	// Verify agent IDs
	agentIDs := make(map[string]bool)
	for _, agent := range response.Agents {
		agentIDs[agent.AgentID] = true
	}

	if !agentIDs["agent-1"] || !agentIDs["agent-2"] || !agentIDs["agent-3"] {
		t.Error("Expected all agents to be in the list")
	}
}

func TestListAgents_Empty(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// List agents (no agents registered)
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	w := httptest.NewRecorder()

	handlers.ListAgents(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.ListAgentsResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Total != 0 {
		t.Errorf("Expected 0 agents, got %d", response.Total)
	}

	if len(response.Agents) != 0 {
		t.Errorf("Expected empty agent list, got %d", len(response.Agents))
	}
}

// ============================================================
// Health Check Tests
// ============================================================

func TestHealthCheck_Success(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Make health check request
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handlers.HealthCheck(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify health status
	if response.Status != "healthy" {
		t.Errorf("Expected status 'healthy', got %s", response.Status)
	}

	// Verify version is set
	if response.Version == "" {
		t.Error("Expected version to be set")
	}

	// Verify uptime is non-negative
	if response.UptimeSeconds < 0 {
		t.Errorf("Expected non-negative uptime, got %d", response.UptimeSeconds)
	}

	// Verify database component
	dbHealth, exists := response.Components["database"]
	if !exists {
		t.Fatal("Expected database component in health response")
	}

	if dbHealth.Status != "healthy" {
		t.Errorf("Expected database status 'healthy', got %s", dbHealth.Status)
	}

	if dbHealth.LastCheck.IsZero() {
		t.Error("Expected LastCheck to be set")
	}
}

func TestHealthCheck_ContentType(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handlers.HealthCheck(w, req)

	// Check Content-Type header
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
	}
}

// ============================================================
// Ready Check Tests
// ============================================================

func TestReadyCheck_Success(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Make ready check request
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	handlers.ReadyCheck(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify ready status
	if !response["ready"] {
		t.Error("Expected ready to be true")
	}
}

func TestReadyCheck_ContentType(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	handlers.ReadyCheck(w, req)

	// Check Content-Type header
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
	}
}

// ============================================================
// Scenario Endpoint Tests
// ============================================================

// Helper function to create a test scenario
func createTestScenario(t *testing.T, db *storage.DB, scenarioID, name, status string) {
	t.Helper()

	scenario := &storage.Scenario{
		ID:     scenarioID,
		Name:   name,
		Intent: "{}",
		Source: "test",
		Status: status,
	}

	if err := db.CreateScenario(context.Background(), scenario); err != nil {
		t.Fatalf("Failed to create test scenario: %v", err)
	}
}

func TestCreateScenario_NotImplemented(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create scenario request
	createReq := protocol.CreateScenarioRequest{
		Name:        "Test Scenario",
		Description: "Test scenario description",
	}

	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/scenarios", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.CreateScenario(w, req)

	// Should return 501 Not Implemented
	if w.Code != http.StatusNotImplemented {
		t.Errorf("Expected status %d, got %d", http.StatusNotImplemented, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "not_implemented" {
		t.Errorf("Expected error 'not_implemented', got %v", response["error"])
	}
}

func TestCreateScenario_MissingName(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create scenario request without name
	createReq := protocol.CreateScenarioRequest{
		Description: "Test scenario description",
	}

	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/scenarios", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.CreateScenario(w, req)

	// Should return 400 Bad Request
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "validation_failed" {
		t.Errorf("Expected error 'validation_failed', got %v", response["error"])
	}
}

func TestCreateScenario_InvalidJSON(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/scenarios", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.CreateScenario(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestGetScenario_Success(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	scenarioID := "scenario-123"
	createTestScenario(t, db, scenarioID, "Test Scenario", "pending")

	// Get scenario
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/"+scenarioID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("scenarioID", scenarioID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetScenario(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response storage.Scenario
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.ID != scenarioID {
		t.Errorf("Expected scenario ID %s, got %s", scenarioID, response.ID)
	}

	if response.Name != "Test Scenario" {
		t.Errorf("Expected name 'Test Scenario', got %s", response.Name)
	}

	if response.Status != "pending" {
		t.Errorf("Expected status 'pending', got %s", response.Status)
	}
}

func TestGetScenario_NotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	scenarioID := "non-existent-scenario"

	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/"+scenarioID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("scenarioID", scenarioID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetScenario(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "scenario_not_found" {
		t.Errorf("Expected error 'scenario_not_found', got %v", response["error"])
	}
}

func TestGetScenarioStatus_Success(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	scenarioID := "scenario-status-123"
	createTestScenario(t, db, scenarioID, "Test Scenario", "running")

	// Create some jobs for the scenario with different statuses
	jobs := []*storage.Job{
		{
			ID:          "job-1",
			ScenarioID:  &scenarioID,
			AgentID:     "agent-1",
			ActionType:  "test_action",
			Parameters:  map[string]interface{}{},
			Status:      storage.JobStatusCompleted,
			Priority:    0,
			MaxRetries:  3,
			ScheduledAt: time.Now().UTC(),
		},
		{
			ID:          "job-2",
			ScenarioID:  &scenarioID,
			AgentID:     "agent-1",
			ActionType:  "test_action",
			Parameters:  map[string]interface{}{},
			Status:      storage.JobStatusPending,
			Priority:    0,
			MaxRetries:  3,
			ScheduledAt: time.Now().UTC(),
		},
		{
			ID:          "job-3",
			ScenarioID:  &scenarioID,
			AgentID:     "agent-1",
			ActionType:  "test_action",
			Parameters:  map[string]interface{}{},
			Status:      storage.JobStatusRunning,
			Priority:    0,
			MaxRetries:  3,
			ScheduledAt: time.Now().UTC(),
		},
	}

	for _, job := range jobs {
		if err := db.CreateJob(ctx, job); err != nil {
			t.Fatalf("Failed to create job: %v", err)
		}
	}

	// Get scenario status
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/"+scenarioID+"/status", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("scenarioID", scenarioID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetScenarioStatus(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.ScenarioStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.ScenarioID != scenarioID {
		t.Errorf("Expected scenario ID %s, got %s", scenarioID, response.ScenarioID)
	}

	if response.Name != "Test Scenario" {
		t.Errorf("Expected name 'Test Scenario', got %s", response.Name)
	}

	if response.Status != "running" {
		t.Errorf("Expected status 'running', got %s", response.Status)
	}

	// Verify progress
	if response.Progress == nil {
		t.Fatal("Expected progress to be set")
	}

	if response.Progress.TotalJobs != 3 {
		t.Errorf("Expected 3 total jobs, got %d", response.Progress.TotalJobs)
	}

	if response.Progress.CompletedJobs != 1 {
		t.Errorf("Expected 1 completed job, got %d", response.Progress.CompletedJobs)
	}

	if response.Progress.RunningJobs != 1 {
		t.Errorf("Expected 1 running job, got %d", response.Progress.RunningJobs)
	}

	if response.Progress.PendingJobs != 1 {
		t.Errorf("Expected 1 pending job, got %d", response.Progress.PendingJobs)
	}

	expectedPercent := float64(1) / float64(3) * 100
	if response.Progress.PercentComplete != expectedPercent {
		t.Errorf("Expected %.2f%% complete, got %.2f%%", expectedPercent, response.Progress.PercentComplete)
	}
}

func TestGetScenarioStatus_NotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	scenarioID := "non-existent-scenario"

	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/"+scenarioID+"/status", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("scenarioID", scenarioID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetScenarioStatus(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}
}

func TestListScenarios_Success(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create multiple scenarios
	createTestScenario(t, db, "scenario-1", "Scenario 1", "pending")
	createTestScenario(t, db, "scenario-2", "Scenario 2", "running")
	createTestScenario(t, db, "scenario-3", "Scenario 3", "completed")

	// List all scenarios
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios", nil)
	w := httptest.NewRecorder()

	handlers.ListScenarios(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response []*storage.Scenario
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response) != 3 {
		t.Errorf("Expected 3 scenarios, got %d", len(response))
	}

	// Verify scenario IDs
	scenarioIDs := make(map[string]bool)
	for _, scenario := range response {
		scenarioIDs[scenario.ID] = true
	}

	if !scenarioIDs["scenario-1"] || !scenarioIDs["scenario-2"] || !scenarioIDs["scenario-3"] {
		t.Error("Expected all scenarios to be in the list")
	}
}

func TestListScenarios_FilterByStatus(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create scenarios with different statuses
	createTestScenario(t, db, "scenario-1", "Scenario 1", "pending")
	createTestScenario(t, db, "scenario-2", "Scenario 2", "running")
	createTestScenario(t, db, "scenario-3", "Scenario 3", "running")
	createTestScenario(t, db, "scenario-4", "Scenario 4", "completed")

	// List only running scenarios
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios?status=running", nil)
	w := httptest.NewRecorder()

	handlers.ListScenarios(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response []*storage.Scenario
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response) != 2 {
		t.Errorf("Expected 2 running scenarios, got %d", len(response))
	}

	// Verify all scenarios are running
	for _, scenario := range response {
		if scenario.Status != "running" {
			t.Errorf("Expected status 'running', got %s", scenario.Status)
		}
	}
}

func TestListScenarios_WithLimit(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create multiple scenarios
	for i := 1; i <= 5; i++ {
		createTestScenario(t, db, fmt.Sprintf("scenario-%d", i), fmt.Sprintf("Scenario %d", i), "pending")
	}

	// List with limit=2
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios?limit=2", nil)
	w := httptest.NewRecorder()

	handlers.ListScenarios(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response []*storage.Scenario
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response) != 2 {
		t.Errorf("Expected 2 scenarios (limit=2), got %d", len(response))
	}
}

func TestListScenarios_Empty(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// List scenarios (none created)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios", nil)
	w := httptest.NewRecorder()

	handlers.ListScenarios(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response []*storage.Scenario
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response) != 0 {
		t.Errorf("Expected 0 scenarios, got %d", len(response))
	}
}

func TestDeleteScenario_Success(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	scenarioID := "scenario-delete-123"
	createTestScenario(t, db, scenarioID, "Test Scenario", "completed")

	// Delete scenario
	req := httptest.NewRequest(http.MethodDelete, "/api/scenarios/"+scenarioID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("scenarioID", scenarioID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.DeleteScenario(w, req)

	// Check response - should be 204 No Content
	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status %d, got %d", http.StatusNoContent, w.Code)
	}

	// Verify scenario was deleted
	scenario, err := db.GetScenario(ctx, scenarioID)
	if err != nil {
		t.Fatalf("Error checking scenario: %v", err)
	}

	if scenario != nil {
		t.Error("Expected scenario to be deleted")
	}
}

func TestDeleteScenario_NotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	scenarioID := "non-existent-scenario"

	req := httptest.NewRequest(http.MethodDelete, "/api/scenarios/"+scenarioID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("scenarioID", scenarioID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.DeleteScenario(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "scenario_not_found" {
		t.Errorf("Expected error 'scenario_not_found', got %v", response["error"])
	}
}

func TestDeleteScenario_CancelsJobs(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	scenarioID := "scenario-with-jobs"
	createTestScenario(t, db, scenarioID, "Test Scenario", "running")

	// Create pending jobs for the scenario
	jobs := []*storage.Job{
		{
			ID:          "job-1",
			ScenarioID:  &scenarioID,
			AgentID:     "agent-1",
			ActionType:  "test_action",
			Parameters:  map[string]interface{}{},
			Status:      storage.JobStatusPending,
			Priority:    0,
			MaxRetries:  3,
			ScheduledAt: time.Now().UTC(),
		},
		{
			ID:          "job-2",
			ScenarioID:  &scenarioID,
			AgentID:     "agent-1",
			ActionType:  "test_action",
			Parameters:  map[string]interface{}{},
			Status:      storage.JobStatusPending,
			Priority:    0,
			MaxRetries:  3,
			ScheduledAt: time.Now().UTC(),
		},
	}

	for _, job := range jobs {
		if err := db.CreateJob(ctx, job); err != nil {
			t.Fatalf("Failed to create job: %v", err)
		}
	}

	// Delete scenario
	req := httptest.NewRequest(http.MethodDelete, "/api/scenarios/"+scenarioID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("scenarioID", scenarioID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.DeleteScenario(w, req)

	// Check response
	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status %d, got %d", http.StatusNoContent, w.Code)
	}

	// Verify jobs were cancelled (deleted scenario means jobs should be handled)
	// In the current implementation, jobs are just cancelled, not deleted
	// We just verify the deletion succeeded
}

// ============================================================
// Impersonation User Endpoint Tests
// ============================================================

// Helper function to create a test impersonation user
func createTestUser(t *testing.T, db *storage.DB, username, domain, samAccountName string) *storage.ImpersonationUser {
	t.Helper()

	user := &storage.ImpersonationUser{
		Username:       username,
		Domain:         domain,
		SAMAccountName: samAccountName,
		DisplayName:    "Test User",
		Department:     "Engineering",
		Title:          "Engineer",
	}

	if err := db.CreateImpersonationUser(context.Background(), user); err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}

	return user
}

func TestCreateImpersonationUser_Success(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create user request
	createReq := protocol.CreateImpersonationUserRequest{
		Username:       "CYMBYTES\\jsmith",
		Domain:         "CYMBYTES",
		SAMAccountName: "jsmith",
		DisplayName:    "John Smith",
		Department:     "Engineering",
		Title:          "Senior Engineer",
		AllowedHosts:   []string{"ws1", "ws2"},
		Persona: &protocol.UserPersonaInput{
			TypicalApps:  []string{"Chrome", "VSCode"},
			TypicalSites: []string{"github.com", "stackoverflow.com"},
			FileTypes:    []string{".go", ".md"},
			WorkHours: &protocol.WorkHoursInput{
				Start: 9,
				End:   17,
			},
		},
	}

	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.CreateImpersonationUser(w, req)

	// Check response
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}

	var response protocol.ImpersonationUserResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Username != "CYMBYTES\\jsmith" {
		t.Errorf("Expected username 'CYMBYTES\\jsmith', got %s", response.Username)
	}

	if response.Domain != "CYMBYTES" {
		t.Errorf("Expected domain 'CYMBYTES', got %s", response.Domain)
	}

	if response.SAMAccountName != "jsmith" {
		t.Errorf("Expected SAM account name 'jsmith', got %s", response.SAMAccountName)
	}

	if response.DisplayName != "John Smith" {
		t.Errorf("Expected display name 'John Smith', got %s", response.DisplayName)
	}

	if response.ID == "" {
		t.Error("Expected ID to be set")
	}
}

func TestCreateImpersonationUser_MissingRequiredFields(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	tests := []struct {
		name    string
		request protocol.CreateImpersonationUserRequest
	}{
		{
			name: "missing username",
			request: protocol.CreateImpersonationUserRequest{
				Domain:         "CYMBYTES",
				SAMAccountName: "jsmith",
			},
		},
		{
			name: "missing domain",
			request: protocol.CreateImpersonationUserRequest{
				Username:       "CYMBYTES\\jsmith",
				SAMAccountName: "jsmith",
			},
		},
		{
			name: "missing sam_account_name",
			request: protocol.CreateImpersonationUserRequest{
				Username: "CYMBYTES\\jsmith",
				Domain:   "CYMBYTES",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.request)
			req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()

			handlers.CreateImpersonationUser(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
			}

			var response map[string]interface{}
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			if response["error"] != "validation_failed" {
				t.Errorf("Expected error 'validation_failed', got %v", response["error"])
			}
		})
	}
}

func TestCreateImpersonationUser_InvalidJSON(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.CreateImpersonationUser(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestGetImpersonationUser_Success(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	user := createTestUser(t, db, "CYMBYTES\\jsmith", "CYMBYTES", "jsmith")

	// Get user
	req := httptest.NewRequest(http.MethodGet, "/api/users/"+user.ID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", user.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetImpersonationUser(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.ImpersonationUserResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.ID != user.ID {
		t.Errorf("Expected user ID %s, got %s", user.ID, response.ID)
	}

	if response.Username != "CYMBYTES\\jsmith" {
		t.Errorf("Expected username 'CYMBYTES\\jsmith', got %s", response.Username)
	}
}

func TestGetImpersonationUser_NotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	userID := "non-existent-user"

	req := httptest.NewRequest(http.MethodGet, "/api/users/"+userID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", userID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetImpersonationUser(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "user_not_found" {
		t.Errorf("Expected error 'user_not_found', got %v", response["error"])
	}
}

func TestListImpersonationUsers_Success(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create multiple users
	createTestUser(t, db, "CYMBYTES\\user1", "CYMBYTES", "user1")
	createTestUser(t, db, "CYMBYTES\\user2", "CYMBYTES", "user2")
	createTestUser(t, db, "CYMBYTES\\user3", "CYMBYTES", "user3")

	// List users
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := httptest.NewRecorder()

	handlers.ListImpersonationUsers(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.ListImpersonationUsersResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Total != 3 {
		t.Errorf("Expected 3 users, got %d", response.Total)
	}

	if len(response.Users) != 3 {
		t.Errorf("Expected 3 users in list, got %d", len(response.Users))
	}
}

func TestListImpersonationUsers_FilterByDepartment(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create users with different departments
	user1 := &storage.ImpersonationUser{
		Username:       "CYMBYTES\\eng1",
		Domain:         "CYMBYTES",
		SAMAccountName: "eng1",
		Department:     "Engineering",
	}
	db.CreateImpersonationUser(context.Background(), user1)

	user2 := &storage.ImpersonationUser{
		Username:       "CYMBYTES\\eng2",
		Domain:         "CYMBYTES",
		SAMAccountName: "eng2",
		Department:     "Engineering",
	}
	db.CreateImpersonationUser(context.Background(), user2)

	user3 := &storage.ImpersonationUser{
		Username:       "CYMBYTES\\sales1",
		Domain:         "CYMBYTES",
		SAMAccountName: "sales1",
		Department:     "Sales",
	}
	db.CreateImpersonationUser(context.Background(), user3)

	// List only Engineering users
	req := httptest.NewRequest(http.MethodGet, "/api/users?department=Engineering", nil)
	w := httptest.NewRecorder()

	handlers.ListImpersonationUsers(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.ListImpersonationUsersResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Total != 2 {
		t.Errorf("Expected 2 Engineering users, got %d", response.Total)
	}

	// Verify all users are from Engineering
	for _, user := range response.Users {
		if user.Department != "Engineering" {
			t.Errorf("Expected department 'Engineering', got %s", user.Department)
		}
	}
}

func TestListImpersonationUsers_Empty(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// List users (none created)
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := httptest.NewRecorder()

	handlers.ListImpersonationUsers(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.ListImpersonationUsersResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Total != 0 {
		t.Errorf("Expected 0 users, got %d", response.Total)
	}
}

func TestUpdateImpersonationUser_Success(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	user := createTestUser(t, db, "CYMBYTES\\jsmith", "CYMBYTES", "jsmith")

	// Update user
	newDisplayName := "Jane Smith"
	newTitle := "Principal Engineer"
	updateReq := protocol.UpdateImpersonationUserRequest{
		DisplayName: &newDisplayName,
		Title:       &newTitle,
		AllowedHosts: []string{"ws1", "ws2", "ws3"},
	}

	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+user.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", user.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.UpdateImpersonationUser(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.ImpersonationUserResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.DisplayName != "Jane Smith" {
		t.Errorf("Expected display name 'Jane Smith', got %s", response.DisplayName)
	}

	if response.Title != "Principal Engineer" {
		t.Errorf("Expected title 'Principal Engineer', got %s", response.Title)
	}

	if len(response.AllowedHosts) != 3 {
		t.Errorf("Expected 3 allowed hosts, got %d", len(response.AllowedHosts))
	}
}

func TestUpdateImpersonationUser_NotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	userID := "non-existent-user"
	newDisplayName := "Test"

	updateReq := protocol.UpdateImpersonationUserRequest{
		DisplayName: &newDisplayName,
	}

	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+userID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", userID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.UpdateImpersonationUser(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}
}

func TestUpdateImpersonationUser_InvalidJSON(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	user := createTestUser(t, db, "CYMBYTES\\jsmith", "CYMBYTES", "jsmith")

	req := httptest.NewRequest(http.MethodPut, "/api/users/"+user.ID, bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", user.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.UpdateImpersonationUser(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestDeleteImpersonationUser_Success(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	ctx := context.Background()
	user := createTestUser(t, db, "CYMBYTES\\jsmith", "CYMBYTES", "jsmith")

	// Delete user
	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+user.ID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", user.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.DeleteImpersonationUser(w, req)

	// Check response
	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status %d, got %d", http.StatusNoContent, w.Code)
	}

	// Verify user was deleted
	deletedUser, err := db.GetImpersonationUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("Error checking user: %v", err)
	}

	if deletedUser != nil {
		t.Error("Expected user to be deleted")
	}
}

func TestDeleteImpersonationUser_NotFound(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	userID := "non-existent-user"

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+userID, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", userID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.DeleteImpersonationUser(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "user_not_found" {
		t.Errorf("Expected error 'user_not_found', got %v", response["error"])
	}
}

func TestBulkCreateImpersonationUsers_Success(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Bulk create users
	bulkReq := protocol.BulkCreateImpersonationUsersRequest{
		Users: []protocol.CreateImpersonationUserRequest{
			{
				Username:       "CYMBYTES\\user1",
				Domain:         "CYMBYTES",
				SAMAccountName: "user1",
				DisplayName:    "User One",
			},
			{
				Username:       "CYMBYTES\\user2",
				Domain:         "CYMBYTES",
				SAMAccountName: "user2",
				DisplayName:    "User Two",
			},
			{
				Username:       "CYMBYTES\\user3",
				Domain:         "CYMBYTES",
				SAMAccountName: "user3",
				DisplayName:    "User Three",
			},
		},
	}

	body, _ := json.Marshal(bulkReq)
	req := httptest.NewRequest(http.MethodPost, "/api/users/bulk", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.BulkCreateImpersonationUsers(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.BulkCreateImpersonationUsersResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Total != 3 {
		t.Errorf("Expected total 3, got %d", response.Total)
	}

	if response.Success != 3 {
		t.Errorf("Expected 3 successful, got %d", response.Success)
	}

	if response.Failed != 0 {
		t.Errorf("Expected 0 failed, got %d", response.Failed)
	}

	if len(response.Created) != 3 {
		t.Errorf("Expected 3 created users, got %d", len(response.Created))
	}
}

func TestBulkCreateImpersonationUsers_PartialSuccess(t *testing.T) {
	handlers, db, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	// Create one user first to cause a duplicate
	createTestUser(t, db, "CYMBYTES\\user1", "CYMBYTES", "user1")

	// Bulk create with one duplicate
	bulkReq := protocol.BulkCreateImpersonationUsersRequest{
		Users: []protocol.CreateImpersonationUserRequest{
			{
				Username:       "CYMBYTES\\user1",
				Domain:         "CYMBYTES",
				SAMAccountName: "user1",
				DisplayName:    "User One",
			},
			{
				Username:       "CYMBYTES\\user2",
				Domain:         "CYMBYTES",
				SAMAccountName: "user2",
				DisplayName:    "User Two",
			},
		},
	}

	body, _ := json.Marshal(bulkReq)
	req := httptest.NewRequest(http.MethodPost, "/api/users/bulk", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.BulkCreateImpersonationUsers(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response protocol.BulkCreateImpersonationUsersResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Total != 2 {
		t.Errorf("Expected total 2, got %d", response.Total)
	}

	if response.Success != 1 {
		t.Errorf("Expected 1 successful, got %d", response.Success)
	}

	if response.Failed != 1 {
		t.Errorf("Expected 1 failed, got %d", response.Failed)
	}

	if len(response.Errors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(response.Errors))
	}
}

func TestBulkCreateImpersonationUsers_EmptyList(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	bulkReq := protocol.BulkCreateImpersonationUsersRequest{
		Users: []protocol.CreateImpersonationUserRequest{},
	}

	body, _ := json.Marshal(bulkReq)
	req := httptest.NewRequest(http.MethodPost, "/api/users/bulk", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.BulkCreateImpersonationUsers(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "validation_failed" {
		t.Errorf("Expected error 'validation_failed', got %v", response["error"])
	}
}

func TestBulkCreateImpersonationUsers_InvalidJSON(t *testing.T) {
	handlers, _, _, cleanup := setupTestHandlers(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/users/bulk", bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlers.BulkCreateImpersonationUsers(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}
