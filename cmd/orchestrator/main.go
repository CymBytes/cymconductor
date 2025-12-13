// Package main is the entry point for the CymBytes Orchestrator.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"

	"cymbytes.com/cymconductor/internal/orchestrator/api"
	"cymbytes.com/cymconductor/internal/orchestrator/registry"
	"cymbytes.com/cymconductor/internal/orchestrator/scheduler"
	"cymbytes.com/cymconductor/internal/orchestrator/scoring"
	"cymbytes.com/cymconductor/internal/orchestrator/storage"
)

// Version information (set at build time)
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// Config holds the complete orchestrator configuration.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Registry  RegistryConfig  `yaml:"registry"`
	Scheduler SchedulerConfig `yaml:"scheduler"`
	Scoring   ScoringConfig   `yaml:"scoring"`
	Azure     AzureConfig     `yaml:"azure"`
	Logging   LoggingConfig   `yaml:"logging"`
	Intents   IntentsConfig   `yaml:"intents"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	DownloadsDir string        `yaml:"downloads_dir"`
}

// DatabaseConfig holds SQLite settings.
type DatabaseConfig struct {
	Path         string `yaml:"path"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
	EnableWAL    bool   `yaml:"enable_wal"`
}

// RegistryConfig holds agent registry settings.
type RegistryConfig struct {
	HeartbeatTimeout time.Duration `yaml:"heartbeat_timeout"`
	CleanupInterval  time.Duration `yaml:"cleanup_interval"`
}

// SchedulerConfig holds job scheduler settings.
type SchedulerConfig struct {
	PollInterval    time.Duration `yaml:"poll_interval"`
	MaxJobsPerAgent int           `yaml:"max_jobs_per_agent"`
}

// ScoringConfig holds scoring engine integration settings.
type ScoringConfig struct {
	Enabled    bool          `yaml:"enabled"`
	EngineURL  string        `yaml:"engine_url"`
	RetryCount int           `yaml:"retry_count"`
	RetryDelay time.Duration `yaml:"retry_delay"`
	Timeout    time.Duration `yaml:"timeout"`
}

// AzureConfig holds Azure Key Vault settings.
type AzureConfig struct {
	KeyVaultURL      string `yaml:"key_vault_url"`
	APIKeySecretName string `yaml:"api_key_secret_name"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// IntentsConfig holds intent file watching settings.
type IntentsConfig struct {
	WatchDirectory string        `yaml:"watch_directory"`
	PollInterval   time.Duration `yaml:"poll_interval"`
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         8081,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			DownloadsDir: "/srv/downloads",
		},
		Database: DatabaseConfig{
			Path:         "/data/orchestrator.db",
			MaxOpenConns: 10,
			MaxIdleConns: 5,
			EnableWAL:    true,
		},
		Registry: RegistryConfig{
			HeartbeatTimeout: 30 * time.Second,
			CleanupInterval:  60 * time.Second,
		},
		Scheduler: SchedulerConfig{
			PollInterval:    time.Second,
			MaxJobsPerAgent: 5,
		},
		Scoring: ScoringConfig{
			Enabled:    false,
			EngineURL:  "http://localhost:8083",
			RetryCount: 3,
			RetryDelay: time.Second,
			Timeout:    10 * time.Second,
		},
		Azure: AzureConfig{
			KeyVaultURL:      "",
			APIKeySecretName: "anthropic-api-key",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Intents: IntentsConfig{
			WatchDirectory: "/opt/labs/orchestrator/intents",
			PollInterval:   10 * time.Second,
		},
	}
}

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Printf("CymBytes Orchestrator\n")
		fmt.Printf("  Version:    %s\n", Version)
		fmt.Printf("  Build Time: %s\n", BuildTime)
		fmt.Printf("  Git Commit: %s\n", GitCommit)
		os.Exit(0)
	}

	// Load configuration
	cfg := DefaultConfig()
	if *configPath != "" {
		if err := loadConfig(*configPath, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
			os.Exit(1)
		}
	}

	// Apply environment variable overrides
	applyEnvOverrides(&cfg)

	// Initialize logger
	logger := initLogger(cfg.Logging)
	logger.Info().
		Str("version", Version).
		Str("build_time", BuildTime).
		Msg("Starting CymBytes Orchestrator")

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize database
	db, err := storage.New(ctx, storage.Config{
		Path:         cfg.Database.Path,
		MaxOpenConns: cfg.Database.MaxOpenConns,
		MaxIdleConns: cfg.Database.MaxIdleConns,
		EnableWAL:    cfg.Database.EnableWAL,
	}, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to initialize database")
	}
	defer db.Close()

	// Initialize registry
	reg := registry.New(db, registry.Config{
		HeartbeatTimeout: cfg.Registry.HeartbeatTimeout,
		CleanupInterval:  cfg.Registry.CleanupInterval,
	}, logger)
	reg.Start(ctx)
	defer reg.Stop()

	// Initialize scheduler
	sched := scheduler.New(db, scheduler.Config{
		PollInterval:    cfg.Scheduler.PollInterval,
		MaxJobsPerAgent: cfg.Scheduler.MaxJobsPerAgent,
	}, logger)
	sched.Start(ctx)
	defer sched.Stop()

	// Initialize scoring forwarder (if enabled)
	if cfg.Scoring.Enabled {
		scoringForwarder := scoring.NewEventForwarder(scoring.Config{
			Enabled:    cfg.Scoring.Enabled,
			EngineURL:  cfg.Scoring.EngineURL,
			RetryCount: cfg.Scoring.RetryCount,
			RetryDelay: cfg.Scoring.RetryDelay,
			Timeout:    cfg.Scoring.Timeout,
		}, logger)
		sched.SetScoringForwarder(scoringForwarder)
		logger.Info().
			Str("engine_url", cfg.Scoring.EngineURL).
			Msg("Scoring engine integration enabled")
	}

	// Initialize API server
	server := api.New(api.Config{
		Host:         cfg.Server.Host,
		Port:         cfg.Server.Port,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		DownloadsDir: cfg.Server.DownloadsDir,
	}, api.Dependencies{
		DB:        db,
		Registry:  reg,
		Scheduler: sched,
		Version:   Version,
		StartTime: time.Now(),
	}, logger)

	// Start server in background
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("Server failed")
		}
	}()

	logger.Info().
		Str("host", cfg.Server.Host).
		Int("port", cfg.Server.Port).
		Msg("Orchestrator is ready")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Info().Str("signal", sig.String()).Msg("Received shutdown signal")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("Server shutdown error")
	}

	logger.Info().Msg("Orchestrator stopped")
}

func loadConfig(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	return nil
}

func applyEnvOverrides(cfg *Config) {
	// Database path
	if v := os.Getenv("DATABASE_PATH"); v != "" {
		cfg.Database.Path = v
	}

	// Server port
	if v := os.Getenv("SERVER_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			cfg.Server.Port = port
		}
	}

	// Azure Key Vault
	if v := os.Getenv("AZURE_KEY_VAULT_URL"); v != "" {
		cfg.Azure.KeyVaultURL = v
	}

	// Log level
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}

	// Scoring engine
	if v := os.Getenv("SCORING_ENABLED"); v == "true" || v == "1" {
		cfg.Scoring.Enabled = true
	}
	if v := os.Getenv("SCORING_ENGINE_URL"); v != "" {
		cfg.Scoring.EngineURL = v
	}
}

func initLogger(cfg LoggingConfig) zerolog.Logger {
	// Set log level
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// Create logger
	var logger zerolog.Logger
	if cfg.Format == "console" {
		logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
			With().Timestamp().Logger()
	} else {
		logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}

	return logger
}
