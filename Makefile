# CymConductor - Makefile

# Version information
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOMOD := $(GOCMD) mod
GOCLEAN := $(GOCMD) clean

# Build flags
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"

# Output directories
BIN_DIR := bin
DIST_DIR := dist

# Docker parameters
DOCKER_IMAGE := cymbytes/orchestrator
DOCKER_TAG ?= $(VERSION)

.PHONY: all build clean test lint docker help seed-users list-users

## all: Build everything
all: clean build

## build: Build orchestrator and agent binaries
build: build-orchestrator build-agent-linux build-agent-windows

## build-orchestrator: Build orchestrator for Linux
build-orchestrator:
	@echo "Building orchestrator..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BIN_DIR)/orchestrator ./cmd/orchestrator

## build-agent-linux: Build agent for Linux
build-agent-linux:
	@echo "Building agent for Linux..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BIN_DIR)/cymbytes-agent-linux-amd64 ./cmd/agent

## build-agent-windows: Build agent for Windows
build-agent-windows:
	@echo "Building agent for Windows..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BIN_DIR)/cymbytes-agent-windows-amd64.exe ./cmd/agent

## build-agent-darwin: Build agent for macOS (for testing)
build-agent-darwin:
	@echo "Building agent for macOS..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BIN_DIR)/cymbytes-agent-darwin-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BIN_DIR)/cymbytes-agent-darwin-arm64 ./cmd/agent

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race ./...

## test-coverage: Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

## lint: Run linters
lint:
	@echo "Running linters..."
	@which golangci-lint > /dev/null || (echo "golangci-lint not found, skipping" && exit 0)
	golangci-lint run ./...

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BIN_DIR) $(DIST_DIR)
	$(GOCLEAN)

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

## docker: Build Docker image
docker:
	@echo "Building Docker image..."
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) \
		-t $(DOCKER_IMAGE):latest \
		.

## docker-push: Push Docker image
docker-push: docker
	@echo "Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):latest

## run-orchestrator: Run orchestrator locally
run-orchestrator: build-orchestrator
	@echo "Running orchestrator..."
	DATABASE_PATH=./orchestrator.db LOG_LEVEL=debug $(BIN_DIR)/orchestrator

## run-agent: Run agent locally
run-agent: build-agent-darwin
	@echo "Running agent..."
	ORCHESTRATOR_URL=http://localhost:8081 LOG_LEVEL=debug $(BIN_DIR)/cymbytes-agent-darwin-$(shell uname -m | sed 's/x86_64/amd64/' | sed 's/arm64/arm64/')

## dist: Create distribution packages
dist: build
	@echo "Creating distribution packages..."
	@mkdir -p $(DIST_DIR)
	@cp $(BIN_DIR)/orchestrator $(DIST_DIR)/
	@cp $(BIN_DIR)/cymbytes-agent-* $(DIST_DIR)/
	@cp -r migrations $(DIST_DIR)/
	@cp configs/* $(DIST_DIR)/ 2>/dev/null || true
	@cd $(DIST_DIR) && tar -czf cymconductor-$(VERSION).tar.gz *
	@echo "Distribution package created: $(DIST_DIR)/cymconductor-$(VERSION).tar.gz"

## seed-users: Seed impersonation users to orchestrator
ORCHESTRATOR_URL ?= http://localhost:8081
seed-users:
	@echo "Seeding impersonation users to $(ORCHESTRATOR_URL)..."
	@curl -s -X POST "$(ORCHESTRATOR_URL)/api/users/bulk" \
		-H "Content-Type: application/json" \
		-d @configs/seed-users.json | jq .
	@echo "Seeding complete."

## list-users: List impersonation users from orchestrator
list-users:
	@echo "Listing impersonation users from $(ORCHESTRATOR_URL)..."
	@curl -s "$(ORCHESTRATOR_URL)/api/users" | jq .

## help: Show this help
help:
	@echo "CymConductor - Build System"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

# Default target
.DEFAULT_GOAL := help
