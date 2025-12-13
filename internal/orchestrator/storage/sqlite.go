// Package storage provides SQLite database access for the orchestrator.
package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"

	"cymbytes.com/cymconductor/migrations"
)

// DB wraps the SQLite database connection with orchestrator-specific methods.
type DB struct {
	db     *sql.DB
	logger zerolog.Logger
}

// Config holds database configuration options.
type Config struct {
	// Path to the SQLite database file
	Path string

	// Maximum number of open connections
	MaxOpenConns int

	// Maximum number of idle connections
	MaxIdleConns int

	// Connection lifetime
	ConnMaxLifetime time.Duration

	// Enable Write-Ahead Logging (recommended for concurrent access)
	EnableWAL bool
}

// DefaultConfig returns sensible defaults for the database.
func DefaultConfig() Config {
	return Config{
		Path:            "orchestrator.db",
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		EnableWAL:       true,
	}
}

// New creates a new database connection and runs migrations.
func New(ctx context.Context, cfg Config, logger zerolog.Logger) (*DB, error) {
	logger = logger.With().Str("component", "storage").Logger()

	// Build connection string with options
	dsn := cfg.Path
	if cfg.EnableWAL {
		dsn += "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
	} else {
		dsn += "?_busy_timeout=5000"
	}

	logger.Info().Str("path", cfg.Path).Bool("wal", cfg.EnableWAL).Msg("Opening database")

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	// Verify connection
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	storage := &DB{
		db:     db,
		logger: logger,
	}

	// Run migrations
	if err := storage.Migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	logger.Info().Msg("Database initialized successfully")
	return storage, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	d.logger.Info().Msg("Closing database connection")
	return d.db.Close()
}

// Migrate runs all pending database migrations.
func (d *DB) Migrate(ctx context.Context) error {
	d.logger.Info().Msg("Running database migrations")

	// Get list of migration files
	migrations, err := d.loadMigrations()
	if err != nil {
		return fmt.Errorf("failed to load migrations: %w", err)
	}

	if len(migrations) == 0 {
		d.logger.Warn().Msg("No migration files found")
		return nil
	}

	// Get applied migrations
	applied, err := d.getAppliedMigrations(ctx)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Apply pending migrations
	for _, m := range migrations {
		checksum := sha256Checksum(m.content)

		if existingChecksum, ok := applied[m.version]; ok {
			if existingChecksum != checksum {
				return fmt.Errorf("migration %d (%s) has been modified after being applied", m.version, m.filename)
			}
			d.logger.Debug().Int("version", m.version).Str("filename", m.filename).Msg("Migration already applied")
			continue
		}

		d.logger.Info().Int("version", m.version).Str("filename", m.filename).Msg("Applying migration")

		if err := d.applyMigration(ctx, m, checksum); err != nil {
			return fmt.Errorf("failed to apply migration %d (%s): %w", m.version, m.filename, err)
		}
	}

	d.logger.Info().Int("total", len(migrations)).Msg("Migrations complete")
	return nil
}

type migration struct {
	version  int
	filename string
	content  string
}

func (d *DB) loadMigrations() ([]migration, error) {
	var migrationList []migration

	err := fs.WalkDir(migrations.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".sql") {
			return nil
		}

		// Parse version from filename (e.g., "001_initial_schema.sql")
		var version int
		_, err = fmt.Sscanf(entry.Name(), "%d_", &version)
		if err != nil {
			return fmt.Errorf("invalid migration filename: %s", entry.Name())
		}

		content, err := migrations.FS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read migration: %w", err)
		}

		migrationList = append(migrationList, migration{
			version:  version,
			filename: entry.Name(),
			content:  string(content),
		})
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort by version
	sort.Slice(migrationList, func(i, j int) bool {
		return migrationList[i].version < migrationList[j].version
	})

	return migrationList, nil
}

func (d *DB) getAppliedMigrations(ctx context.Context) (map[int]string, error) {
	// Create schema_version table if it doesn't exist
	_, err := d.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			filename TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return nil, err
	}

	rows, err := d.db.QueryContext(ctx, "SELECT version, checksum FROM schema_version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]string)
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, err
		}
		applied[version] = checksum
	}

	return applied, rows.Err()
}

func (d *DB) applyMigration(ctx context.Context, m migration, checksum string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Execute migration SQL
	if _, err := tx.ExecContext(ctx, m.content); err != nil {
		return fmt.Errorf("migration SQL failed: %w", err)
	}

	// Record migration
	_, err = tx.ExecContext(ctx,
		"INSERT INTO schema_version (version, filename, checksum) VALUES (?, ?, ?)",
		m.version, m.filename, checksum)
	if err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}

func sha256Checksum(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// ============================================================
// Transaction support
// ============================================================

// Tx represents a database transaction.
type Tx struct {
	tx *sql.Tx
}

// Begin starts a new database transaction.
func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx}, nil
}

// Commit commits the transaction.
func (t *Tx) Commit() error {
	return t.tx.Commit()
}

// Rollback rolls back the transaction.
func (t *Tx) Rollback() error {
	return t.tx.Rollback()
}

// ============================================================
// Utility methods
// ============================================================

// Exec executes a query without returning rows.
func (d *DB) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return d.db.ExecContext(ctx, query, args...)
}

// Query executes a query that returns rows.
func (d *DB) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

// QueryRow executes a query that returns at most one row.
func (d *DB) QueryRow(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

// Stats returns database statistics.
func (d *DB) Stats() sql.DBStats {
	return d.db.Stats()
}

// GetDB returns the underlying *sql.DB for advanced use cases.
// Use with caution - prefer the wrapper methods when possible.
func (d *DB) GetDB() *sql.DB {
	return d.db
}

// ============================================================
// Health check
// ============================================================

// Ping verifies the database connection is alive.
func (d *DB) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// Health returns the health status of the database.
func (d *DB) Health(ctx context.Context) (string, error) {
	if err := d.Ping(ctx); err != nil {
		return "unhealthy", err
	}

	// Check if we can query
	var count int
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_version").Scan(&count)
	if err != nil {
		return "degraded", err
	}

	return "healthy", nil
}

// DatabasePath returns the path to the database file.
func DatabasePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return path
}
