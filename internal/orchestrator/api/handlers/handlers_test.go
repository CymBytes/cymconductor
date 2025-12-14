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
