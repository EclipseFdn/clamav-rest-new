# =============================================================================
# ClamAV REST Service
#
# Multi-stage build:
# - Build stage: Official Go image for compiling
# - Runtime stage: Official ClamAV Debian image
#
# The clamav/clamav-debian image includes:
# - ClamAV daemon (clamd)
# - ClamAV scanner (clamdscan)
# - Freshclam for updates
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

# Install curl for healthcheck (may already be present)
# Version pinned for reproducible builds (Debian trixie/unstable)
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        curl=8.14.1-2+deb13u2 && \
    rm -rf /var/lib/apt/lists/*

# Create non-root user for running the REST service
# The REST server runs as this user, while clamd runs as clamav
RUN useradd -m -u 1001 -s /bin/false scanner

# Create directories and set permissions for clamd
RUN mkdir -p /var/run/clamav /var/lib/clamav /var/log/clamav && \
    chown -R clamav:clamav /var/run/clamav /var/lib/clamav /var/log/clamav && \
    chmod 750 /var/run/clamav

# Note: ClamAV config files (clamd.conf, freshclam.conf) are generated
# at runtime in /var/run/clamav by entrypoint.sh

WORKDIR /app

# Copy binary and entrypoint from builder
COPY --from=builder /app/clamav-rest .
COPY entrypoint.sh .

# Make entrypoint executable and set ownership
RUN chmod +x entrypoint.sh && \
    chown scanner:scanner clamav-rest

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

# ClamAV daemon max threads (default 10 is too low for high throughput)
ENV MAX_THREADS=20
# =============================================================================

EXPOSE 9000

# Health check - longer start period for clamd to load signatures
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s \
    CMD curl -sf http://localhost:${PORT}/health || exit 1

ENTRYPOINT ["./entrypoint.sh"]
