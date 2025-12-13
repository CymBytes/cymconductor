-- CymConductor - Impersonation Users Schema
-- Version: 002
-- Description: Add impersonation users table and update jobs for run_as support

-- ============================================================
-- Table: impersonation_users
-- Domain users available for agent impersonation
-- ============================================================
CREATE TABLE IF NOT EXISTS impersonation_users (
    id TEXT PRIMARY KEY,                              -- UUID v4
    username TEXT NOT NULL,                           -- Full username (DOMAIN\user)
    domain TEXT NOT NULL,                             -- Domain name (e.g., "CYMBYTES")
    sam_account_name TEXT NOT NULL,                   -- SAM account name (e.g., "jsmith")
    display_name TEXT,                                -- Display name for logging
    department TEXT,                                  -- Department for realistic grouping
    title TEXT,                                       -- Job title
    allowed_hosts TEXT NOT NULL DEFAULT '[]',         -- JSON array of allowed lab_host_ids
    persona TEXT,                                     -- JSON: behavior hints for AI planner
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(username)
);

-- ============================================================
-- Add run_as columns to scenario_steps
-- ============================================================
ALTER TABLE scenario_steps ADD COLUMN run_as_user TEXT;           -- Username to impersonate
ALTER TABLE scenario_steps ADD COLUMN run_as_logon_type TEXT;     -- interactive, network, batch

-- ============================================================
-- Add run_as columns to jobs
-- ============================================================
ALTER TABLE jobs ADD COLUMN run_as_user TEXT;                     -- Username to impersonate
ALTER TABLE jobs ADD COLUMN run_as_logon_type TEXT DEFAULT 'interactive';  -- Logon type

-- ============================================================
-- Indexes
-- ============================================================
CREATE INDEX IF NOT EXISTS idx_impersonation_users_domain ON impersonation_users(domain);
CREATE INDEX IF NOT EXISTS idx_impersonation_users_department ON impersonation_users(department);
CREATE INDEX IF NOT EXISTS idx_jobs_run_as ON jobs(run_as_user);

-- ============================================================
-- Trigger for impersonation_users.updated_at
-- ============================================================
CREATE TRIGGER IF NOT EXISTS trg_impersonation_users_updated_at
AFTER UPDATE ON impersonation_users
FOR EACH ROW
BEGIN
    UPDATE impersonation_users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
