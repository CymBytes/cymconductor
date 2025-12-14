# CymConductor Development Guide

## Project Overview

CymConductor is a distributed lab orchestration system for cybersecurity training environments. It coordinates agents across lab hosts to execute realistic user activity scenarios, enabling security teams to generate authentic telemetry for detection engineering and training.

**Repository:** https://github.com/CymBytes/cymconductor

## Architecture

### Components

1. **Orchestrator** (Go service)
   - Central control server running on router VM
   - REST API for agent registration and job dispatch
   - AI planner integration (Claude) for scenario generation
   - SQLite database for persistence
   - Web dashboard for monitoring

2. **Agent** (Go binary)
   - Lightweight daemon running on lab VMs (Windows/Linux)
   - Polls orchestrator for jobs via heartbeat
   - Executes predefined actions from finite catalog
   - Supports user impersonation on Windows

3. **Web Dashboard**
   - Real-time monitoring interface
   - CRT/terminal aesthetic with phosphor green accents
   - Shows agents, users, scenarios, system health
   - Auto-refreshes every 5 seconds

## Development Setup

### Prerequisites

```bash
# Install Go 1.22+
brew install go

# Install Docker
brew install docker

# Install golangci-lint (for linting)
brew install golangci-lint
```

### Local Development

```bash
# Clone repository
git clone https://github.com/CymBytes/cymconductor.git
cd cymconductor

# Build
go build -o bin/orchestrator ./cmd/orchestrator
go build -o bin/agent ./cmd/agent

# Run orchestrator locally
DATABASE_PATH=./orchestrator.db WEB_DIR=./web go run ./cmd/orchestrator

# Seed users
cat configs/seed-users.json | curl -X POST http://localhost:8081/api/users/bulk \
  -H "Content-Type: application/json" -d @-

# Access dashboard
open http://localhost:8081
```

### Docker Development

```bash
# Build Docker image
docker build -t cymconductor:latest .

# Run container
docker run -d \
  --name cymconductor \
  -p 8081:8081 \
  -v cymconductor-data:/data \
  cymconductor:latest

# View logs
docker logs -f cymconductor

# Seed users in container
cat configs/seed-users.json | curl -X POST http://localhost:8081/api/users/bulk \
  -H "Content-Type: application/json" -d @-

# Stop and remove
docker stop cymconductor
docker rm cymconductor
```

## Project Structure

```
cymconductor/
├── cmd/
│   ├── orchestrator/          # Orchestrator entry point
│   └── agent/                 # Agent entry point
├── internal/
│   ├── orchestrator/
│   │   ├── api/               # HTTP server and handlers
│   │   ├── storage/           # SQLite persistence layer
│   │   ├── registry/          # Agent registry with heartbeat
│   │   ├── scheduler/         # Job dispatcher
│   │   ├── validator/         # DSL validation
│   │   ├── compiler/          # DSL to jobs compiler
│   │   └── planner/           # AI integration (Claude)
│   └── agent/
│       ├── client/            # Orchestrator API client
│       ├── executor/          # Job executor
│       ├── actions/           # Action implementations
│       ├── impersonation/     # Windows user impersonation
│       └── audit/             # Audit logging
├── pkg/
│   ├── dsl/                   # DSL type definitions
│   └── protocol/              # Shared API types
├── migrations/                # Database migrations (SQLite)
├── configs/                   # Example configurations
├── web/                       # Web dashboard static files
├── docs/                      # Documentation and screenshots
├── .github/workflows/         # CI/CD workflows
├── Dockerfile                 # Multi-stage Docker build
├── Makefile                   # Build targets
└── .golangci.yml             # Linter configuration
```

## Key Files

- **`cmd/orchestrator/main.go`** - Orchestrator entry point with config loading
- **`internal/orchestrator/api/server.go`** - HTTP server setup and routes
- **`internal/orchestrator/storage/sqlite.go`** - Database initialization and migrations
- **`internal/agent/actions/`** - Predefined action catalog (browsing, files, email, processes)
- **`internal/agent/impersonation/impersonation_windows.go`** - Windows user impersonation
- **`web/index.html`** - Web dashboard (standalone HTML/CSS/JS)
- **`Dockerfile`** - Multi-stage build (builds orchestrator + agents, includes web dashboard)

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_PATH` | `/data/orchestrator.db` | SQLite database path |
| `SERVER_PORT` | `8081` | HTTP server port |
| `LOG_LEVEL` | `info` | Logging level (debug, info, warn, error) |
| `WEB_DIR` | `/srv/web` | Web dashboard directory |
| `SCORING_ENABLED` | `false` | Enable scoring engine integration |
| `SCORING_ENGINE_URL` | `http://localhost:8083` | Scoring engine endpoint |
| `AZURE_KEY_VAULT_URL` | - | Azure Key Vault for API keys |

### Configuration File

Optional YAML configuration file (pass via `--config` flag):

```yaml
server:
  host: "0.0.0.0"
  port: 8081
  web_dir: "/srv/web"
  downloads_dir: "/srv/downloads"

database:
  path: "/data/orchestrator.db"
  enable_wal: true

registry:
  heartbeat_timeout: 30s
  cleanup_interval: 60s

scheduler:
  poll_interval: 1s
  max_jobs_per_agent: 5

logging:
  level: "info"
  format: "json"
```

## API Endpoints

### Core APIs

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/api/agents` | GET/POST | List/register agents |
| `/api/agents/{id}/heartbeat` | POST | Agent heartbeat |
| `/api/agents/{id}/jobs/next` | GET | Poll for jobs |
| `/api/scenarios` | GET/POST | List/create scenarios |
| `/api/users` | GET/POST | List/create users |
| `/api/users/bulk` | POST | Bulk create users |

### Testing APIs

```bash
# Health check
curl http://localhost:8081/health

# List agents
curl http://localhost:8081/api/agents

# List users
curl http://localhost:8081/api/users

# Create user
curl -X POST http://localhost:8081/api/users \
  -H "Content-Type: application/json" \
  -d '{
    "username": "CYMBYTES\\testuser",
    "domain": "CYMBYTES",
    "sam_account_name": "testuser",
    "display_name": "Test User",
    "department": "Engineering",
    "title": "Engineer"
  }'
```

## CI/CD

### GitHub Actions Workflow

Located at `.github/workflows/ci.yml`, runs on push/PR to main:

1. **Lint** - golangci-lint with custom config
2. **Test** - Run tests with race detection and coverage
3. **Build** - Build orchestrator + agents (Linux/Windows)
4. **Docker** - Build Docker image

**Status:** https://github.com/CymBytes/cymconductor/actions

### Running Lint Locally

```bash
# Install golangci-lint
brew install golangci-lint

# Run linter
golangci-lint run

# Fix auto-fixable issues
golangci-lint run --fix
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests with race detection
go test -race ./...
```

## Database

### Schema

CymConductor uses SQLite with WAL mode enabled for persistence:

- **agents** - Registered agents with labels and status
- **jobs** - Job queue and execution history
- **scenarios** - Scenario definitions
- **impersonation_users** - Windows domain users for impersonation
- **file_activities** - File action audit log
- **audit_events** - General audit trail

### Migrations

Migrations are embedded in the binary at `migrations/*.sql` and applied automatically on startup.

To add a new migration:

1. Create `migrations/NNN_description.sql`
2. Migrations run in alphabetical order
3. They're applied once (tracked in `schema_migrations` table)

### Accessing Database

```bash
# Local development
sqlite3 ./orchestrator.db

# Docker container
docker exec -it cymconductor sqlite3 /data/orchestrator.db

# Query examples
sqlite> SELECT * FROM agents;
sqlite> SELECT * FROM impersonation_users;
sqlite> SELECT * FROM jobs ORDER BY created_at DESC LIMIT 10;
```

## Web Dashboard

### Overview

The web dashboard is a single-page application (SPA) built with vanilla HTML/CSS/JavaScript:

- **Location:** `web/index.html`
- **Style:** CRT/terminal aesthetic with phosphor green (#00ff88)
- **Features:** Real-time monitoring, auto-refresh, responsive design
- **No build required** - Pure HTML/CSS/JS served statically

### Customization

Edit `web/index.html` to customize:

```javascript
// Change refresh interval (default 5s)
setInterval(refreshData, 5000); // milliseconds

// Change API base URL
const API_BASE = 'http://custom-host:8081';
```

### Screenshots

Dashboard screenshot: `docs/dashboard.png`

## Deployment

### Docker Deployment (Production)

```bash
# Build with version tags
docker build \
  --build-arg VERSION=v1.0.0 \
  --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  --build-arg GIT_COMMIT=$(git rev-parse HEAD) \
  -t cymconductor:v1.0.0 .

# Run in production
docker run -d \
  --name cymconductor \
  --restart unless-stopped \
  -p 8081:8081 \
  -v /opt/cymconductor/data:/data \
  -e LOG_LEVEL=info \
  -e AZURE_KEY_VAULT_URL=https://kv-prod.vault.azure.net \
  cymconductor:v1.0.0
```

### Ansible Deployment

The orchestrator is deployed via Ansible role in the router repository:

```bash
cd ubuntu-server-22-lts-prepared-router-master

# Enable orchestrator in inventory
echo "deploy_orchestrator: true" >> ansible/inventory/group_vars/router.yml

# Deploy
afb exec ansible-playbook playbook.yml -vv --tags orchestrator
```

## Development Workflow

### Making Changes

```bash
# 1. Create feature branch
git checkout -b feature/my-feature

# 2. Make changes and test locally
go run ./cmd/orchestrator

# 3. Run tests
go test ./...

# 4. Run linter
golangci-lint run

# 5. Commit with conventional commit message
git commit -m "feat: add new feature"

# 6. Push and create PR
git push origin feature/my-feature
gh pr create
```

### Commit Message Convention

Use conventional commits:

- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation only
- `refactor:` - Code refactoring
- `test:` - Add/update tests
- `ci:` - CI/CD changes
- `chore:` - Maintenance tasks

### Code Review Checklist

- [ ] Tests pass locally (`go test ./...`)
- [ ] Linter passes (`golangci-lint run`)
- [ ] Documentation updated (README, CLAUDE.md)
- [ ] API changes documented
- [ ] Database migrations added if needed
- [ ] CI passes on GitHub

## Troubleshooting

### Build Issues

```bash
# Clear Go module cache
go clean -modcache

# Re-download dependencies
go mod download

# Verify go.mod/go.sum
go mod tidy
go mod verify
```

### Database Issues

```bash
# Reset database (WARNING: data loss)
rm orchestrator.db orchestrator.db-shm orchestrator.db-wal

# Check database integrity
sqlite3 orchestrator.db "PRAGMA integrity_check;"

# View migrations status
sqlite3 orchestrator.db "SELECT * FROM schema_migrations;"
```

### Docker Issues

```bash
# Remove all containers/images
docker stop cymconductor
docker rm cymconductor
docker rmi cymconductor:latest

# Clean rebuild
docker build --no-cache -t cymconductor:latest .

# Check logs
docker logs -f cymconductor

# Shell into container
docker exec -it cymconductor /bin/sh
```

### Agent Connection Issues

1. Verify orchestrator is reachable: `curl http://orchestrator:8081/health`
2. Check firewall allows port 8081
3. Verify agent config has correct orchestrator URL
4. Check agent logs for connection errors
5. Verify DNS resolves orchestrator hostname

## Recent Changes

### Phase 3: User Impersonation (Completed)

- ✅ Added `impersonation_users` table with personas
- ✅ User management API endpoints (CRUD + bulk)
- ✅ AI planner integration with user context
- ✅ User seeding from Ansible inventory via API
- ✅ Web dashboard with real-time user display

### Web Dashboard (Completed)

- ✅ Single-page dashboard with CRT aesthetic
- ✅ Real-time agent/user/scenario monitoring
- ✅ Auto-refresh every 5 seconds
- ✅ Responsive design
- ✅ Docker deployment

### CI/CD (Completed)

- ✅ GitHub Actions workflow
- ✅ Lint, test, build, docker jobs
- ✅ golangci-lint configuration
- ✅ Build artifacts uploaded
- ✅ Coverage reporting

### Docker Improvements (Completed)

- ✅ Multi-architecture build (native ARM/AMD64)
- ✅ Web dashboard included in image
- ✅ Environment variable configuration
- ✅ Volume mount for data persistence
- ✅ Health check endpoint

## Next Steps

### Planned Features

- [ ] Agent auto-registration from Ansible inventory
- [ ] Scenario templates library
- [ ] Real-time scenario execution monitoring
- [ ] Agent metrics and telemetry
- [ ] Multi-tenant support
- [ ] RBAC for API access

### Future Improvements

- [ ] Prometheus metrics export
- [ ] Grafana dashboards
- [ ] Distributed tracing (OpenTelemetry)
- [ ] Agent binary auto-update
- [ ] Web UI for scenario creation
- [ ] API authentication/authorization

## Resources

- **Repository:** https://github.com/CymBytes/cymconductor
- **CI/CD:** https://github.com/CymBytes/cymconductor/actions
- **Issues:** https://github.com/CymBytes/cymconductor/issues

## Support

For questions or issues:

1. Check this documentation first
2. Search existing GitHub issues
3. Create new issue with:
   - Description of problem
   - Steps to reproduce
   - Environment details
   - Logs/error messages
