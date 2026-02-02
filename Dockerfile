# =============================================================================
# ClamAV REST Service
#
# Multi-stage build:
# - Build stage: Official Go image for compiling
# - Runtime stage: Official ClamAV Debian image
#
# OpenShift compatible:
# - Runs as non-root user (UID 1001)
# - Uses GID 0 pattern for OpenShift random UID support
# - No privilege escalation required
# =============================================================================

# Build stage - use official Go image (Debian-based)
FROM golang:1.23-bookworm AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod ./

# Download dependencies (none currently)
RUN go mod download

# Copy source code
COPY *.go ./

# Build the binary - static linking for portability
RUN CGO_ENABLED=0 GOOS=linux go build -o clamav-rest .

# =============================================================================
# Runtime stage - use official ClamAV Debian image
#
# clamav/clamav-debian includes:
# - ClamAV daemon (clamd)
# - ClamAV scanner (clamdscan)
# - Freshclam for updates
# - Debian-based for compatibility
#
# Pinned to version 1.5.1 for reproducible builds
# =============================================================================
FROM clamav/clamav-debian:1.5.1

# Install curl for healthcheck
# Versions pinned for reproducible builds (Debian trixie)
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        curl=8.14.1-2+deb13u2 && \
    rm -rf /var/lib/apt/lists/*

# Create directories with GID 0 ownership for OpenShift compatibility
RUN mkdir -p /var/run/clamav /var/lib/clamav /var/log/clamav /tmp/scans && \
    chown -R 1001:0 /var/run/clamav /var/lib/clamav /var/log/clamav /tmp/scans && \
    chmod -R ug+rwx /var/run/clamav /var/lib/clamav /var/log/clamav /tmp/scans

# Fix /etc/clamav permissions for certificates (read-only is fine)
RUN chown -R 1001:0 /etc/clamav && \
    chmod -R ug+rx /etc/clamav

WORKDIR /app

# Copy binary and entrypoint from builder
COPY --from=builder /app/clamav-rest .
COPY entrypoint.sh .

# Set ownership and permissions for app directory
RUN chown -R 1001:0 /app && \
    chmod -R ug+rwx /app

# =============================================================================
# Environment Variables
# =============================================================================
# Server settings
ENV PORT=9000
ENV LOG_LEVEL=info

# HTTP timeouts (prevent slow-loris attacks)
ENV READ_TIMEOUT_SECONDS=30
ENV WRITE_TIMEOUT_SECONDS=300
ENV IDLE_TIMEOUT_SECONDS=60

# Upload limit
ENV MAX_UPLOAD_SIZE_MB=512

# Zip bomb protection limits
# These also configure clamd.conf at startup
ENV MAX_EXTRACTED_SIZE_MB=1024
ENV MAX_FILE_COUNT=100000
ENV MAX_SINGLE_FILE_MB=256
ENV MAX_RECURSION=16

# Scan timeout
ENV SCAN_TIMEOUT_MINUTES=5

# ClamAV daemon max threads
ENV MAX_THREADS=10
# =============================================================================

EXPOSE 9000

# Health check - longer start period for clamd to load signatures
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s \
    CMD curl -sf http://localhost:${PORT}/health || exit 1

# Run as non-root user (OpenShift will override with random UID in GID 0)
USER 1001

ENTRYPOINT ["./entrypoint.sh"]
