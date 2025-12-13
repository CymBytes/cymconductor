// Package main is the entry point for the CymBytes Agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"

	"cymbytes.com/cymconductor/internal/agent/client"
	"cymbytes.com/cymconductor/internal/agent/executor"
)

// Version information (set at build time)
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// Config holds the complete agent configuration.
type Config struct {
	Orchestrator OrchestratorConfig `yaml:"orchestrator"`
	Agent        AgentConfig        `yaml:"agent"`
	Heartbeat    HeartbeatConfig    `yaml:"heartbeat"`
	Actions      ActionsConfig      `yaml:"actions"`
	Logging      LoggingConfig      `yaml:"logging"`
}

// OrchestratorConfig holds orchestrator connection settings.
type OrchestratorConfig struct {
	URL            string        `yaml:"url"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

// AgentConfig holds agent identity settings.
type AgentConfig struct {
	ID        string            `yaml:"id"`
	LabHostID string            `yaml:"lab_host_id"`
	Hostname  string            `yaml:"hostname"`
	Labels    map[string]string `yaml:"labels"`
}

// HeartbeatConfig holds heartbeat settings.
type HeartbeatConfig struct {
	Interval       time.Duration `yaml:"interval"`
	MaxJobsPerPoll int           `yaml:"max_jobs_per_poll"`
}

// ActionsConfig holds action-specific settings.
type ActionsConfig struct {
	Browsing        BrowsingConfig        `yaml:"browsing"`
	FileActivity    FileActivityConfig    `yaml:"file_activity"`
	ProcessActivity ProcessActivityConfig `yaml:"process_activity"`
}

// BrowsingConfig holds browser automation settings.
type BrowsingConfig struct {
	BrowserPath string `yaml:"browser_path"`
	UserDataDir string `yaml:"user_data_dir"`
}

// FileActivityConfig holds file operation settings.
type FileActivityConfig struct {
	AllowedDirectories []string `yaml:"allowed_directories"`
}

// ProcessActivityConfig holds process spawn settings.
type ProcessActivityConfig struct {
	AllowedProcesses []string `yaml:"allowed_processes"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Path   string `yaml:"path"`
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() Config {
	osType := runtime.GOOS

	cfg := Config{
		Orchestrator: OrchestratorConfig{
			URL:            "http://10.0.0.254:8081",
			ConnectTimeout: 10 * time.Second,
			RequestTimeout: 30 * time.Second,
		},
		Agent: AgentConfig{
			ID:        "",
			LabHostID: "",
			Hostname:  "",
			Labels: map[string]string{
				"role": "workstation",
				"os":   osType,
			},
		},
		Heartbeat: HeartbeatConfig{
			Interval:       5 * time.Second,
			MaxJobsPerPoll: 3,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	// Platform-specific defaults
	if osType == "windows" {
		cfg.Actions = ActionsConfig{
			Browsing: BrowsingConfig{
				BrowserPath: `C:\Program Files\Google\Chrome\Application\chrome.exe`,
				UserDataDir: `C:\ProgramData\CymBytes\chrome-profile`,
			},
			FileActivity: FileActivityConfig{
				AllowedDirectories: []string{
					`C:\Users\cbadmin\Documents`,
					`C:\Users\cbadmin\Desktop`,
				},
			},
			ProcessActivity: ProcessActivityConfig{
				AllowedProcesses: []string{
					"notepad.exe",
					"calc.exe",
					"mspaint.exe",
					"explorer.exe",
				},
			},
		}
		cfg.Logging.Path = `C:\ProgramData\CymBytes\logs\agent.log`
	} else {
		cfg.Actions = ActionsConfig{
			Browsing: BrowsingConfig{
				BrowserPath: "/usr/bin/chromium-browser",
				UserDataDir: "/var/lib/cymbytes/chrome-profile",
			},
			FileActivity: FileActivityConfig{
				AllowedDirectories: []string{
					"/home/cbadmin/Documents",
					"/tmp/cymbytes-work",
				},
			},
			ProcessActivity: ProcessActivityConfig{
				AllowedProcesses: []string{
					"gedit",
					"gnome-calculator",
					"nautilus",
				},
			},
		}
		cfg.Logging.Path = "/var/log/cymbytes/agent.log"
	}

	return cfg
}

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Printf("CymBytes Agent\n")
		fmt.Printf("  Version:    %s\n", Version)
		fmt.Printf("  Build Time: %s\n", BuildTime)
		fmt.Printf("  Git Commit: %s\n", GitCommit)
		fmt.Printf("  OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
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

	// Auto-detect missing values
	autoDetect(&cfg)

	// Initialize logger
	logger := initLogger(cfg.Logging)
	logger.Info().
		Str("version", Version).
		Str("agent_id", cfg.Agent.ID).
		Str("orchestrator", cfg.Orchestrator.URL).
		Msg("Starting CymBytes Agent")

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize orchestrator client
	apiClient := client.New(client.Config{
		BaseURL:        cfg.Orchestrator.URL,
		ConnectTimeout: cfg.Orchestrator.ConnectTimeout,
		RequestTimeout: cfg.Orchestrator.RequestTimeout,
	}, logger)

	// Initialize executor
	exec := executor.New(executor.Config{
		Browsing: executor.BrowsingConfig{
			BrowserPath: cfg.Actions.Browsing.BrowserPath,
			UserDataDir: cfg.Actions.Browsing.UserDataDir,
		},
		FileActivity: executor.FileActivityConfig{
			AllowedDirectories: cfg.Actions.FileActivity.AllowedDirectories,
		},
		ProcessActivity: executor.ProcessActivityConfig{
			AllowedProcesses: cfg.Actions.ProcessActivity.AllowedProcesses,
		},
	}, logger)

	// Create and start the agent
	agent := &Agent{
		config:   cfg,
		client:   apiClient,
		executor: exec,
		logger:   logger,
	}

	// Start agent loop
	go agent.Run(ctx)

	logger.Info().Msg("Agent is running")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
	cancel()

	// Give agent time to clean up
	time.Sleep(2 * time.Second)
	logger.Info().Msg("Agent stopped")
}

// Agent represents the running agent instance.
type Agent struct {
	config   Config
	client   *client.Client
	executor *executor.Executor
	logger   zerolog.Logger
}

// Run starts the agent main loop.
func (a *Agent) Run(ctx context.Context) {
	// Register with orchestrator
	if err := a.register(ctx); err != nil {
		a.logger.Fatal().Err(err).Msg("Failed to register with orchestrator")
	}

	// Start heartbeat/job polling loop
	ticker := time.NewTicker(a.config.Heartbeat.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Msg("Agent loop stopping")
			return
		case <-ticker.C:
			a.poll(ctx)
		}
	}
}

// register registers the agent with the orchestrator.
func (a *Agent) register(ctx context.Context) error {
	a.logger.Info().Msg("Registering with orchestrator")

	resp, err := a.client.Register(ctx, client.RegisterRequest{
		AgentID:   a.config.Agent.ID,
		LabHostID: a.config.Agent.LabHostID,
		Hostname:  a.config.Agent.Hostname,
		IPAddress: getLocalIP(),
		Labels:    a.config.Agent.Labels,
		Version:   Version,
	})
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	a.logger.Info().
		Str("agent_id", resp.AgentID).
		Int("heartbeat_ms", resp.HeartbeatIntervalMs).
		Msg("Registered successfully")

	// Update heartbeat interval if recommended by orchestrator
	if resp.HeartbeatIntervalMs > 0 {
		a.config.Heartbeat.Interval = time.Duration(resp.HeartbeatIntervalMs) * time.Millisecond
	}

	return nil
}

// poll sends heartbeat and fetches/executes jobs.
func (a *Agent) poll(ctx context.Context) {
	// Send heartbeat
	_, err := a.client.Heartbeat(ctx, a.config.Agent.ID, client.HeartbeatRequest{
		Status: "online",
	})
	if err != nil {
		a.logger.Error().Err(err).Msg("Heartbeat failed")
		return
	}

	// Get next jobs
	jobs, err := a.client.GetJobs(ctx, a.config.Agent.ID, a.config.Heartbeat.MaxJobsPerPoll)
	if err != nil {
		a.logger.Error().Err(err).Msg("Failed to get jobs")
		return
	}

	if len(jobs) == 0 {
		return
	}

	a.logger.Debug().Int("count", len(jobs)).Msg("Received jobs")

	// Execute each job
	for _, job := range jobs {
		a.executeJob(ctx, job)
	}
}

// executeJob executes a single job and reports the result.
func (a *Agent) executeJob(ctx context.Context, job client.JobAssignment) {
	startTime := time.Now()
	a.logger.Info().
		Str("job_id", job.JobID).
		Str("action", job.ActionType).
		Msg("Executing job")

	// Execute the action
	result, err := a.executor.Execute(ctx, job.ActionType, job.Parameters)

	completedAt := time.Now()

	// Report result
	if err != nil {
		a.logger.Error().
			Err(err).
			Str("job_id", job.JobID).
			Msg("Job execution failed")

		_, reportErr := a.client.ReportResult(ctx, a.config.Agent.ID, job.JobID, client.JobResultRequest{
			Status:      "failed",
			StartedAt:   startTime,
			CompletedAt: completedAt,
			Error: &client.JobError{
				Code:      "EXECUTION_ERROR",
				Message:   err.Error(),
				Retryable: true,
			},
		})
		if reportErr != nil {
			a.logger.Error().Err(reportErr).Msg("Failed to report job failure")
		}
		return
	}

	a.logger.Info().
		Str("job_id", job.JobID).
		Dur("duration", completedAt.Sub(startTime)).
		Msg("Job completed successfully")

	_, reportErr := a.client.ReportResult(ctx, a.config.Agent.ID, job.JobID, client.JobResultRequest{
		Status:      "completed",
		StartedAt:   startTime,
		CompletedAt: completedAt,
		Result:      result,
	})
	if reportErr != nil {
		a.logger.Error().Err(reportErr).Msg("Failed to report job success")
	}
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
	if v := os.Getenv("ORCHESTRATOR_URL"); v != "" {
		cfg.Orchestrator.URL = v
	}
	if v := os.Getenv("AGENT_ID"); v != "" {
		cfg.Agent.ID = v
	}
	if v := os.Getenv("LAB_HOST_ID"); v != "" {
		cfg.Agent.LabHostID = v
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
}

func autoDetect(cfg *Config) {
	// Generate agent ID if not set
	if cfg.Agent.ID == "" {
		cfg.Agent.ID = uuid.New().String()
	}

	// Get hostname if not set
	if cfg.Agent.Hostname == "" {
		hostname, _ := os.Hostname()
		cfg.Agent.Hostname = hostname
	}

	// Use hostname as lab host ID if not set
	if cfg.Agent.LabHostID == "" {
		cfg.Agent.LabHostID = cfg.Agent.Hostname
	}
}

func initLogger(cfg LoggingConfig) zerolog.Logger {
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	var logger zerolog.Logger
	if cfg.Format == "console" {
		logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
			With().Timestamp().Logger()
	} else {
		logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}

	return logger
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "unknown"
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}

	return "unknown"
}
