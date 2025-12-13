# CymConductor - Multi-stage Dockerfile
# Builds orchestrator and agent binaries, packages orchestrator with bundled agents

# =============================================================================
# Stage 1: Build
# =============================================================================
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata gcc musl-dev

WORKDIR /app

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build arguments for version info
ARG VERSION=dev
ARG BUILD_TIME
ARG GIT_COMMIT

# Build orchestrator for Linux (native arch)
RUN CGO_ENABLED=1 go build \
    -ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}" \
    -o /bin/orchestrator \
    ./cmd/orchestrator

# Build agent for Linux (native arch)
RUN CGO_ENABLED=0 go build \
    -ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}" \
    -o /bin/cymbytes-agent-linux \
    ./cmd/agent

# Build agent for Windows
RUN CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
    -ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}" \
    -o /bin/cymbytes-agent-windows-amd64.exe \
    ./cmd/agent

# =============================================================================
# Stage 2: Runtime
# =============================================================================
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata sqlite

# Create non-root user
RUN addgroup -g 1000 cymbytes && \
    adduser -u 1000 -G cymbytes -s /bin/sh -D cymbytes

# Create directories
RUN mkdir -p /data /etc/orchestrator /srv/downloads /srv/migrations && \
    chown -R cymbytes:cymbytes /data /etc/orchestrator /srv

# Copy binaries
COPY --from=builder /bin/orchestrator /usr/local/bin/
COPY --from=builder /bin/cymbytes-agent-linux /srv/downloads/
COPY --from=builder /bin/cymbytes-agent-windows-amd64.exe /srv/downloads/

# Copy migrations
COPY migrations /srv/migrations

# Copy web dashboard
COPY web /srv/web

# Set permissions
RUN chmod +x /usr/local/bin/orchestrator && \
    chmod +r /srv/downloads/* && \
    chmod -R +r /srv/migrations && \
    chmod -R +r /srv/web

# Environment variables
ENV DATABASE_PATH=/data/orchestrator.db
ENV SERVER_PORT=8081
ENV LOG_LEVEL=info
ENV WEB_DIR=/srv/web

# Expose port
EXPOSE 8081

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8081/health || exit 1

# Run as non-root user
USER cymbytes

# Volume for persistent data
VOLUME ["/data"]

# Entry point (runs with env vars, config optional)
ENTRYPOINT ["/usr/local/bin/orchestrator"]
CMD []
