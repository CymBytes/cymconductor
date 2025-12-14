package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"cymbytes.com/cymconductor/internal/orchestrator/registry"
	"cymbytes.com/cymconductor/internal/orchestrator/scheduler"
	"cymbytes.com/cymconductor/internal/orchestrator/storage"
	"cymbytes.com/cymconductor/pkg/protocol"
	"github.com/rs/zerolog"
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
