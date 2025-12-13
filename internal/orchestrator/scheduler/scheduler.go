// Package scheduler provides job scheduling and dispatch functionality.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cymbytes.com/cymconductor/internal/orchestrator/scoring"
	"cymbytes.com/cymconductor/internal/orchestrator/storage"
	"cymbytes.com/cymconductor/pkg/protocol"
	"github.com/rs/zerolog"
)

// Scheduler manages job scheduling and dispatch.
type Scheduler struct {
	db              *storage.DB
	logger          zerolog.Logger
	scoringForwarder *scoring.EventForwarder

	// Configuration
	pollInterval    time.Duration
	maxJobsPerAgent int

	// Background worker
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// Config holds scheduler configuration.
type Config struct {
	// PollInterval is how often to check for due jobs
	PollInterval time.Duration

	// MaxJobsPerAgent is the maximum jobs to assign per poll
	MaxJobsPerAgent int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		PollInterval:    time.Second,
		MaxJobsPerAgent: 5,
	}
}

// New creates a new scheduler.
func New(db *storage.DB, cfg Config, logger zerolog.Logger) *Scheduler {
	return &Scheduler{
		db:               db,
		logger:           logger.With().Str("component", "scheduler").Logger(),
		scoringForwarder: nil,
		pollInterval:     cfg.PollInterval,
		maxJobsPerAgent:  cfg.MaxJobsPerAgent,
		stopCh:           make(chan struct{}),
	}
}

// SetScoringForwarder sets the scoring event forwarder.
func (s *Scheduler) SetScoringForwarder(forwarder *scoring.EventForwarder) {
	s.scoringForwarder = forwarder
	s.logger.Info().Bool("enabled", forwarder != nil && forwarder.IsEnabled()).Msg("Scoring forwarder configured")
}

// Start begins the scheduler background loop.
func (s *Scheduler) Start(ctx context.Context) {
	s.logger.Info().
		Dur("poll_interval", s.pollInterval).
		Int("max_jobs_per_agent", s.maxJobsPerAgent).
		Msg("Starting scheduler")

	s.wg.Add(1)
	go s.schedulerLoop(ctx)
}

// Stop halts the scheduler.
func (s *Scheduler) Stop() {
	s.logger.Info().Msg("Stopping scheduler")
	close(s.stopCh)
	s.wg.Wait()
}

// schedulerLoop is the main background loop.
func (s *Scheduler) schedulerLoop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			// Check for completed scenarios
			if err := s.checkScenarioCompletion(ctx); err != nil {
				s.logger.Error().Err(err).Msg("Failed to check scenario completion")
			}
		}
	}
}

// checkScenarioCompletion checks if active scenarios are complete.
func (s *Scheduler) checkScenarioCompletion(ctx context.Context) error {
	scenarios, err := s.db.ListScenarios(ctx, storage.ScenarioStatusActive, 100)
	if err != nil {
		return err
	}

	for _, scenario := range scenarios {
		total, completed, failed, _, pending, err := s.db.GetScenarioJobStats(ctx, scenario.ID)
		if err != nil {
			s.logger.Error().Err(err).Str("scenario_id", scenario.ID).Msg("Failed to get job stats")
			continue
		}

		// Scenario is complete when all jobs are done (completed or failed)
		if pending == 0 && total > 0 && (completed+failed) == total {
			if err := s.db.UpdateScenarioCompleted(ctx, scenario.ID); err != nil {
				s.logger.Error().Err(err).Str("scenario_id", scenario.ID).Msg("Failed to complete scenario")
			} else {
				s.logger.Info().
					Str("scenario_id", scenario.ID).
					Int("completed", completed).
					Int("failed", failed).
					Msg("Scenario completed")
			}
		}
	}

	return nil
}

// GetNextJobsForAgent retrieves and assigns the next jobs for an agent.
func (s *Scheduler) GetNextJobsForAgent(ctx context.Context, agentID string, max int) ([]protocol.JobAssignment, bool, error) {
	if max > s.maxJobsPerAgent {
		max = s.maxJobsPerAgent
	}

	// Get pending jobs for this agent
	jobs, err := s.db.GetNextJobsForAgent(ctx, agentID, max+1) // +1 to check hasMore
	if err != nil {
		return nil, false, fmt.Errorf("failed to get jobs: %w", err)
	}

	if len(jobs) == 0 {
		return nil, false, nil
	}

	// Check if there are more
	hasMore := len(jobs) > max
	if hasMore {
		jobs = jobs[:max]
	}

	// Mark jobs as assigned
	jobIDs := make([]string, len(jobs))
	for i, job := range jobs {
		jobIDs[i] = job.ID
	}

	if err := s.db.AssignJobs(ctx, jobIDs); err != nil {
		return nil, false, fmt.Errorf("failed to assign jobs: %w", err)
	}

	// Convert to response format
	assignments := make([]protocol.JobAssignment, len(jobs))
	for i, job := range jobs {
		assignments[i] = protocol.JobAssignment{
			JobID:       job.ID,
			ActionType:  job.ActionType,
			Parameters:  job.Parameters,
			Priority:    job.Priority,
			ScheduledAt: job.ScheduledAt,
		}

		// Add scenario context if available
		if job.ScenarioID != nil {
			assignments[i].ScenarioID = *job.ScenarioID
			// Optionally fetch scenario name
			scenario, _ := s.db.GetScenario(ctx, *job.ScenarioID)
			if scenario != nil {
				assignments[i].ScenarioName = scenario.Name
			}
		}
	}

	s.logger.Debug().
		Str("agent_id", agentID).
		Int("count", len(assignments)).
		Bool("has_more", hasMore).
		Msg("Jobs assigned to agent")

	return assignments, hasMore, nil
}

// ProcessJobResult handles the result of a completed job.
func (s *Scheduler) ProcessJobResult(ctx context.Context, agentID, jobID string, req *protocol.JobResultRequest) (retryScheduled bool, retryAt *time.Time, err error) {
	// Get job to verify ownership and existence
	job, err := s.db.GetJob(ctx, jobID)
	if err != nil {
		return false, nil, fmt.Errorf("failed to get job: %w", err)
	}
	if job == nil {
		return false, nil, fmt.Errorf("job not found: %s", jobID)
	}

	// Verify agent owns this job
	if job.AgentID != agentID {
		return false, nil, fmt.Errorf("job %s not assigned to agent %s", jobID, agentID)
	}

	// Update job based on status
	switch req.Status {
	case "completed":
		var result map[string]interface{}
		if req.Result != nil {
			result = req.Result.Data
		}
		if err := s.db.UpdateJobCompleted(ctx, jobID, req.CompletedAt, result); err != nil {
			return false, nil, fmt.Errorf("failed to update job completed: %w", err)
		}

		s.logger.Info().
			Str("job_id", jobID).
			Str("agent_id", agentID).
			Str("action", job.ActionType).
			Msg("Job completed successfully")

		// Forward to scoring engine (async)
		s.forwardJobResultToScoring(ctx, job, req)

	case "failed":
		var errMsg string
		var retryable bool
		if req.Error != nil {
			errMsg = req.Error.Message
			retryable = req.Error.Retryable
		}

		if err := s.db.UpdateJobFailed(ctx, jobID, req.CompletedAt, errMsg, retryable); err != nil {
			return false, nil, fmt.Errorf("failed to update job failed: %w", err)
		}

		// Check if retry was scheduled
		if retryable && job.RetryCount < job.MaxRetries {
			retryScheduled = true
			t := time.Now().Add(time.Duration(job.RetryCount+1) * 30 * time.Second) // Exponential backoff
			retryAt = &t
		}

		s.logger.Warn().
			Str("job_id", jobID).
			Str("agent_id", agentID).
			Str("action", job.ActionType).
			Str("error", errMsg).
			Bool("retry_scheduled", retryScheduled).
			Msg("Job failed")

		// Forward failure to scoring engine (async)
		s.forwardJobResultToScoring(ctx, job, req)

	default:
		return false, nil, fmt.Errorf("invalid status: %s", req.Status)
	}

	return retryScheduled, retryAt, nil
}

// forwardJobResultToScoring forwards job results to the scoring engine asynchronously.
func (s *Scheduler) forwardJobResultToScoring(ctx context.Context, job *storage.Job, req *protocol.JobResultRequest) {
	if s.scoringForwarder == nil {
		return
	}

	// Get scenario to find scoring run ID
	var scoringRunID string
	if job.ScenarioID != nil {
		scenario, err := s.db.GetScenario(ctx, *job.ScenarioID)
		if err != nil {
			s.logger.Warn().Err(err).Str("scenario_id", *job.ScenarioID).Msg("Failed to get scenario for scoring")
			return
		}
		if scenario != nil && scenario.ScoringRunID != nil {
			scoringRunID = *scenario.ScoringRunID
		}
	}

	if scoringRunID == "" {
		s.logger.Debug().Str("job_id", job.ID).Msg("No scoring run ID, skipping event forwarding")
		return
	}

	// Build job info
	jobInfo := &scoring.JobInfo{
		JobID:       job.ID,
		AgentID:     job.AgentID,
		ActionType:  job.ActionType,
		Parameters:  job.Parameters,
		ScheduledAt: job.ScheduledAt,
	}
	if job.ScenarioID != nil {
		jobInfo.ScenarioID = *job.ScenarioID
	}

	// Build job result
	var resultData map[string]interface{}
	var durationMs int64
	var errMsg string

	if req.Result != nil {
		resultData = req.Result.Data
		durationMs = req.Result.DurationMs
	}
	if req.Error != nil {
		errMsg = req.Error.Message
	}

	jobResult := &scoring.JobResult{
		Status:      req.Status,
		StartedAt:   req.StartedAt,
		CompletedAt: req.CompletedAt,
		Result:      resultData,
		DurationMs:  durationMs,
		Error:       errMsg,
	}

	// Forward asynchronously
	go func() {
		forwardCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := s.scoringForwarder.ForwardJobResult(forwardCtx, scoringRunID, jobInfo, jobResult); err != nil {
			s.logger.Error().
				Err(err).
				Str("job_id", job.ID).
				Str("scoring_run_id", scoringRunID).
				Msg("Failed to forward job result to scoring engine")
		}
	}()
}

// CreateJob creates a new job.
func (s *Scheduler) CreateJob(ctx context.Context, job *storage.Job) error {
	return s.db.CreateJob(ctx, job)
}

// CreateJobs creates multiple jobs in a batch.
func (s *Scheduler) CreateJobs(ctx context.Context, jobs []*storage.Job) error {
	return s.db.CreateJobBatch(ctx, jobs)
}

// CancelScenarioJobs cancels all pending jobs for a scenario.
func (s *Scheduler) CancelScenarioJobs(ctx context.Context, scenarioID string) (int, error) {
	return s.db.CancelJobsForScenario(ctx, scenarioID)
}

// GetJobStats returns job statistics.
func (s *Scheduler) GetJobStats(ctx context.Context) (map[string]int, error) {
	return s.db.CountJobsByStatus(ctx)
}

// CleanupOldJobs removes old completed/failed jobs.
func (s *Scheduler) CleanupOldJobs(ctx context.Context, olderThan time.Duration) (int, error) {
	return s.db.CleanupOldJobs(ctx, olderThan)
}
