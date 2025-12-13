-- Migration: Add scoring engine integration support
-- This adds the scoring_run_id column to scenarios for event forwarding

-- Add scoring_run_id column to scenarios table
ALTER TABLE scenarios ADD COLUMN scoring_run_id TEXT;

-- Create index for quick lookup by scoring run ID
CREATE INDEX IF NOT EXISTS idx_scenarios_scoring_run_id ON scenarios(scoring_run_id);
