# ClamAV REST

[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/EclipseFdn/clamav-rest-new)](go.mod)
[![License](https://img.shields.io/badge/License-EPL2.0-blue.svg)](LICENSE.md)
[![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker&logoColor=white)](Dockerfile)

A lightweight REST API for ClamAV virus scanning. Designed for **internal use only** as part of backend file processing pipelines.

> **Warning:** This service has no authentication. Do not expose it to the public internet. Deploy behind a firewall or internal network only.

## Features

- **Simple REST API** — Upload files via HTTP, get JSON results
- **Fast scanning** — Uses clamd daemon with signatures pre-loaded in memory
- **Archive support** — Automatically extracts and scans ZIP archive contents
- **Zip bomb protection** — Configurable limits on file size, file count, and total extracted size
- **Security hardened** — Runs as non-root, supports read-only filesystem
- **Health checks** — Built-in endpoint for liveness/readiness probes

## Quick Start

```bash
docker build -t clamav-rest .
docker run -p 9000:9000 clamav-rest

# Wait ~90s for startup, then:
curl http://localhost:9000/health
curl -X POST -F "file=@myfile.zip" http://localhost:9000/scan
```

## API

### `POST /scan`

Upload a file for scanning. Supports both single files and ZIP archives. Archives are automatically extracted and scanned.

```bash
# Scan a single file
curl -X POST -F "file=@document.pdf" http://localhost:9000/scan

# Scan a ZIP archive (contents are extracted and scanned)
curl -X POST -F "file=@archive.zip" http://localhost:9000/scan
```

**Response (infected):**
```json
{
  "status": "infected",
  "threats": [
    {
      "name": "Win.Test.EICAR_HDB-1",
      "file": "test/eicar.txt",
      "file_hash": "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
      "severity": "critical"
    }
  ],
  "scanned_files": 1,
  "scan_time_ms": 45
}
```

**Response (clean):**
```json
{
  "status": "clean",
  "threats": [],
  "scanned_files": 142,
  "scan_time_ms": 156
}
```

### `GET /health`

Health check endpoint.

```json
{
  "status": "ok",
  "clamav_version": "1.5.1",
  "db_version": "27234"
}
```

## Configuration

All settings via environment variables.

### Server Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `9000` | HTTP server port |
| `LOG_LEVEL` | `info` | Log level (`info` or `debug`) |

### HTTP Timeouts

Prevents slowloris attacks and resource exhaustion from slow clients.

| Variable | Default | Description |
|----------|---------|-------------|
| `READ_TIMEOUT_SECONDS` | `30` | Max time to read entire request |
| `WRITE_TIMEOUT_SECONDS` | `300` | Max time to write response |
| `IDLE_TIMEOUT_SECONDS` | `60` | Max idle time for keep-alive |

### Size Limits

| Variable | Default | Description |
|----------|---------|-------------|
| `MAX_UPLOAD_SIZE_MB` | `512` | Max upload size (multipart form) |
| `MAX_EXTRACTED_SIZE_MB` | `1024` | Max total extracted size |
| `MAX_FILE_COUNT` | `100000` | Max files in archive |
| `MAX_SINGLE_FILE_MB` | `256` | Max single file size |
| `MAX_RECURSION` | `16` | Max depth for nested archive scanning |

### Scan Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `SCAN_TIMEOUT_MINUTES` | `5` | Max time for ClamAV scan |

### Virus Definition Updates

| Variable | Default | Description |
|----------|---------|-------------|
| `FRESHCLAM_CHECKS` | `24` | Times per day to check for updates (24=hourly, 12=every 2h, 1=daily) |

Freshclam runs as a daemon and automatically updates virus definitions. When updates are found, clamd reloads them without restart.

## Deployment

### Requirements

- **Memory:** 3-4 GB minimum (per [official ClamAV docs](https://docs.clamav.net/manual/Installing/Docker.html))
  - ~1.2 GB for virus signatures in memory
  - Spikes to ~2.4 GB during daily signature updates (concurrent reload)
- **Startup time:** 60-90 seconds (clamd loads signatures)
- **File size limit:** ClamAV has a hard 2 GB limit per file

### Writable Directories

Supports **read-only root filesystem**. These directories need write access:

| Directory | Purpose | Size | Storage |
|-----------|---------|------|---------|
| `/tmp/scans` | Scan temp files | See below | emptyDir or tmpfs |
| `/var/run/clamav` | clamd pid + configs | 10MB | emptyDir or tmpfs |
| `/var/log/clamav` | Logs (stdout in container) | 50MB | emptyDir or tmpfs |
| `/var/lib/clamav` | Virus signatures | ~2GB | **Persistent volume** |

> **Important:** Do NOT mount a volume over `/etc/clamav`. It contains certificates required by ClamAV 1.5+ for signature verification. Config files are generated in `/var/run/clamav` instead.

### Sizing `/tmp`

Each concurrent scan needs temporary space for the uploaded file plus extracted archive contents:

```
Per scan = MAX_SINGLE_FILE_MB + MAX_EXTRACTED_SIZE_MB
         = 256 MB + 1024 MB = ~1.3 GB (with defaults)

Total    = per scan × MAX_THREADS (concurrent scans)
         = 1.3 GB × 20 = ~26 GB (worst case with defaults)
```

In practice, most scans use far less. A reasonable starting point is **5-10 GB**.

> **Important:** Use a disk volume for `/tmp`, not tmpfs. tmpfs consumes RAM and ClamAV already needs 3-4 GB for signatures.

### OpenShift / Kubernetes Compatibility

This image is **OpenShift compatible**:
- Runs as non-root user (UID 1001, GID 0)
- Works with OpenShift's random UID assignment
- No privilege escalation required

All writable directories use the GID 0 pattern (`chown 1001:0`, `chmod ug+rwx`), allowing any UID in GID 0 to write.

### Example Docker Compose

```yaml
read_only: true
security_opt:
  - no-new-privileges: true
tmpfs:
  - /var/run/clamav:size=10M,mode=755
  - /var/log/clamav:size=50M,mode=755
  - /tmp/scans:size=5G,mode=755
volumes:
  - clamav-db:/var/lib/clamav
```

### Example Kubernetes/OpenShift Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: clamav-rest
spec:
  replicas: 1
  selector:
    matchLabels:
      app: clamav-rest
  template:
    metadata:
      labels:
        app: clamav-rest
    spec:
      containers:
        - name: clamav-rest
          image: clamav-rest:latest
          ports:
            - containerPort: 9000
          resources:
            requests:
              memory: "3Gi"
            limits:
              memory: "4Gi"
          volumeMounts:
            - name: clamav-db
              mountPath: /var/lib/clamav
            - name: run-clamav
              mountPath: /var/run/clamav
            - name: tmp-scans
              mountPath: /tmp/scans
      volumes:
        - name: clamav-db
          persistentVolumeClaim:
            claimName: clamav-db
        - name: run-clamav
          emptyDir: {}
        - name: tmp-scans
          emptyDir: {}
```

## Development

### Prerequisites

- Go 1.21+
- Docker (for containerized builds)
- make (optional, for convenience commands)

### Build & Test

```bash
# Using make
make build      # Build binary
make test       # Run tests
make coverage   # Run tests with coverage report
make docker     # Build Docker image
make fmt        # Format code
make vet        # Check for issues
make help       # Show all commands

# Or using Go directly
go build -o clamav-rest .
go test -v ./...
go test -coverprofile=coverage.out ./...
```

### Project Structure

```
clamav-rest/
├── main.go           # HTTP server and handlers
├── scanner.go        # ClamAV scanning logic
├── config.go         # Configuration loading
├── *_test.go         # Unit tests
├── Dockerfile        # Container build
├── entrypoint.sh     # Container entrypoint
├── Makefile          # Build commands
└── README.md
```

### Running Locally

Requires a local clamd daemon running:

```bash
./clamav-rest
```

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

## License

This project is licensed under the MIT License — see [LICENSE.md](LICENSE.md) for details.
