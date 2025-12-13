-- CymConductor - Initial Schema
-- Version: 001
-- Description: Create core tables for orchestrator

-- ============================================================
-- Table: agents
-- Tracks registered lab VM agents and their status
-- ============================================================
CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,                              -- UUID v4
    lab_host_id TEXT NOT NULL,                        -- VM identifier (e.g., "ws1", "dc1")
    hostname TEXT NOT NULL,                           -- Machine hostname
    ip_address TEXT NOT NULL,                         -- IP address for logging/debugging
    labels TEXT NOT NULL DEFAULT '{}',                -- JSON: {"role": "workstation", "os": "windows"}
    version TEXT NOT NULL DEFAULT '',                 -- Agent version string
    status TEXT NOT NULL DEFAULT 'online',            -- online, offline, error
    last_heartbeat_at DATETIME NOT NULL,              -- Last successful heartbeat
    registered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- Table: scenarios
-- AI-generated scenario definitions from lab intents
-- ============================================================
CREATE TABLE IF NOT EXISTS scenarios (
    id TEXT PRIMARY KEY,                              -- UUID v4
    name TEXT NOT NULL,                               -- Human-readable scenario name
    description TEXT,                                 -- Optional description
    intent TEXT NOT NULL,                             -- Original user intent (JSON)
    source TEXT NOT NULL DEFAULT 'api',               -- api, file
    status TEXT NOT NULL DEFAULT 'pending',           -- pending, planning, validated, compiled, active, completed, failed
    ai_output TEXT,                                   -- Raw AI response (JSON) for debugging
    validated_dsl TEXT,                               -- Validated DSL scenario (JSON)
    error_message TEXT,                               -- Error details if status=failed
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME                             -- When scenario finished executing
);

-- ============================================================
-- Table: scenario_steps
-- Individual steps within a validated scenario
-- ============================================================
CREATE TABLE IF NOT EXISTS scenario_steps (
    id TEXT PRIMARY KEY,                              -- UUID v4
    scenario_id TEXT NOT NULL,                        -- FK to scenarios
    step_order INTEGER NOT NULL,                      -- Execution order (1-indexed)
    action_type TEXT NOT NULL,                        -- simulate_browsing, simulate_file_activity, etc.
    target_labels TEXT NOT NULL DEFAULT '{}',         -- JSON: {"role": "workstation"} for agent selection
    target_count TEXT NOT NULL DEFAULT 'all',         -- all, any, or specific number
    parameters TEXT NOT NULL DEFAULT '{}',            -- JSON: action-specific parameters
    delay_before_ms INTEGER NOT NULL DEFAULT 0,       -- Delay before executing this step
    delay_after_ms INTEGER NOT NULL DEFAULT 0,        -- Delay after executing this step
    jitter_ms INTEGER NOT NULL DEFAULT 0,             -- Random jitter range (+/-)
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (scenario_id) REFERENCES scenarios(id) ON DELETE CASCADE
);

-- ============================================================
-- Table: jobs
-- Concrete job assignments to specific agents
-- ============================================================
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,                              -- UUID v4
    scenario_id TEXT,                                 -- FK to scenarios (nullable for ad-hoc jobs)
    scenario_step_id TEXT,                            -- FK to scenario_steps (nullable)
    agent_id TEXT NOT NULL,                           -- FK to agents
    action_type TEXT NOT NULL,                        -- Action to execute
    parameters TEXT NOT NULL DEFAULT '{}',            -- JSON: action parameters
    status TEXT NOT NULL DEFAULT 'pending',           -- pending, assigned, running, completed, failed, cancelled
    priority INTEGER NOT NULL DEFAULT 0,              -- Higher = more urgent
    scheduled_at DATETIME NOT NULL,                   -- When job should be available
    assigned_at DATETIME,                             -- When job was assigned to agent
    started_at DATETIME,                              -- When agent started execution
    completed_at DATETIME,                            -- When execution finished
    result TEXT,                                      -- JSON: execution result
    error_message TEXT,                               -- Error details if failed
    retry_count INTEGER NOT NULL DEFAULT 0,           -- Number of retry attempts
    max_retries INTEGER NOT NULL DEFAULT 3,           -- Maximum retry attempts
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (scenario_id) REFERENCES scenarios(id) ON DELETE SET NULL,
    FOREIGN KEY (scenario_step_id) REFERENCES scenario_steps(id) ON DELETE SET NULL,
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
);

-- ============================================================
-- Table: audit_log
-- Audit trail for debugging and compliance
-- ============================================================
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,                        -- agent, scenario, job
    entity_id TEXT NOT NULL,                          -- ID of the entity
    action TEXT NOT NULL,                             -- created, updated, deleted, status_changed
    actor TEXT,                                       -- Who/what performed the action (agent_id, system, api)
    old_value TEXT,                                   -- JSON: previous state
    new_value TEXT,                                   -- JSON: new state
    metadata TEXT,                                    -- JSON: additional context
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- Table: schema_version
-- Track applied migrations
-- ============================================================
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    filename TEXT NOT NULL,
    checksum TEXT NOT NULL,                           -- SHA256 of migration file
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================
-- Indexes for common query patterns
-- ============================================================

-- Agent queries
CREATE INDEX IF NOT EXISTS idx_agents_status ON agents(status);
CREATE INDEX IF NOT EXISTS idx_agents_lab_host ON agents(lab_host_id);
CREATE INDEX IF NOT EXISTS idx_agents_heartbeat ON agents(last_heartbeat_at);

-- Scenario queries
CREATE INDEX IF NOT EXISTS idx_scenarios_status ON scenarios(status);
CREATE INDEX IF NOT EXISTS idx_scenarios_created ON scenarios(created_at DESC);

-- Scenario step queries
CREATE INDEX IF NOT EXISTS idx_steps_scenario ON scenario_steps(scenario_id, step_order);

-- Job queries (critical for scheduler performance)
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_agent_status ON jobs(agent_id, status);
CREATE INDEX IF NOT EXISTS idx_jobs_scheduled ON jobs(scheduled_at, status);
CREATE INDEX IF NOT EXISTS idx_jobs_scenario ON jobs(scenario_id);
CREATE INDEX IF NOT EXISTS idx_jobs_priority ON jobs(priority DESC, scheduled_at ASC);

-- Audit log queries
CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_log(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at DESC);

-- ============================================================
-- Triggers for automatic timestamp updates
-- ============================================================

-- Update agents.updated_at on modification
CREATE TRIGGER IF NOT EXISTS trg_agents_updated_at
AFTER UPDATE ON agents
FOR EACH ROW
BEGIN
    UPDATE agents SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

-- Update scenarios.updated_at on modification
CREATE TRIGGER IF NOT EXISTS trg_scenarios_updated_at
AFTER UPDATE ON scenarios
FOR EACH ROW
BEGIN
    UPDATE scenarios SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

-- Update jobs.updated_at on modification
CREATE TRIGGER IF NOT EXISTS trg_jobs_updated_at
AFTER UPDATE ON jobs
FOR EACH ROW
BEGIN
    UPDATE jobs SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
