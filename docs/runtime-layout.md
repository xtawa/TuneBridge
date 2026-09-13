# TuneBridge Runtime & Deployment Layout

This document outlines the container deployment model, persistent file layout, authentication policies, health checking mechanisms, and environment configuration for TuneBridge.

---

## 1. Image Architecture & Build Model

TuneBridge is packaged using a multi-stage Docker build:
- **Build Stage (`golang:1.24-alpine`)**: Compiles the statically-linked binary (`CGO_ENABLED=0`) with `-trimpath` and stripped symbols (`-ldflags="-s -w"`).
- **Runtime Stage (`alpine:3.21`)**: Provides a lightweight and secure base runtime. It installs CA certificates (`ca-certificates`) and timezone data (`tzdata`) while relying on Alpine's built-in BusyBox utilities for operational checks.
- **Binary Path**: The compiled binary is located at `/usr/local/bin/tunebridge` and configured as the container entrypoint.
- **Exposed Port**: Port `8080` is exposed for HTTP and WebDAV services.

---

## 2. Authentication Policy: WebDAV Is Never Anonymous

> **Important**: WebDAV authentication in TuneBridge is strictly enforced. **WebDAV is never anonymous.**

- TuneBridge does not support anonymous access. During initialization, the configuration validator verifies that both `TUNEBRIDGE_WEBDAV_USERNAME` and `TUNEBRIDGE_WEBDAV_PASSWORD` are non-empty strings. If either variable is missing or blank, the service aborts startup with the following error:
  ```text
  TUNEBRIDGE_WEBDAV_USERNAME and TUNEBRIDGE_WEBDAV_PASSWORD are required; anonymous WebDAV is not supported
  ```
- In `docker-compose.yml`, credentials must be supplied directly by the caller environment (`${TUNEBRIDGE_WEBDAV_USERNAME:?...}` and `${TUNEBRIDGE_WEBDAV_PASSWORD:?...}`). Docker Compose does not supply default credentials or mock secrets, ensuring that unconfigured deployments fail immediately and visibly.

---

## 3. Persistent Storage & Directory Layout

TuneBridge requires durable state across container restarts and updates. The base directory for persistent state is defined by `TUNEBRIDGE_DATA_DIR=/data`, which is marked as a `VOLUME` in the Dockerfile.

### Directory Structure Inside Container (`/data`)

```
/data/
├── tunebridge.db          # Primary SQLite database file
├── tunebridge.db-shm      # SQLite shared-memory index (during active writes)
├── tunebridge.db-wal      # SQLite write-ahead log (during active transactions)
└── cache/
    └── audio/             # Cached streaming and track audio files
```

### Path Descriptions

1. **SQLite Database (`/data/tunebridge.db`)**:
   - Stores schema migration states (`schema_migrations` table), application records, and AES-GCM encrypted session credentials (`source_sessions` table).
   - Automatically created and migrated on startup via the embedded SQL migration runner.
   - SQLite uses WAL/busy-timeout pragma configurations to ensure reliability.
2. **Audio Cache Directory (`/data/cache/audio/`)**:
   - Stores locally cached media streams fetched from music sources to minimize redundant upstream requests.
   - Governed by `TUNEBRIDGE_CACHE_MAX_BYTES` (default: 10 GB).
3. **Mount Options**:
   - **Named Volume (Default in compose)**: `tunebridge-data:/data` manages persistence within Docker storage volumes.
   - **Host Bind Mount**: You can map a host directory such as `-v /host/path/data:/data` or `./data:/data`.

---

## 4. Health Checks

TuneBridge exposes HTTP health and readiness probe endpoints:

- **Liveness Endpoint (`GET /healthz`)**:
  - Returns HTTP 200 with `{"status":"ok"}`.
  - Used by Docker and Docker Compose healthchecks to verify that the HTTP server is running and accepting connections.
- **Readiness Endpoint (`GET /readyz`)**:
  - Pings the underlying SQLite database.
  - Returns HTTP 200 with `{"status":"ready"}` if the database responds, or HTTP 503 with `{"status":"not_ready"}` if SQLite is inaccessible.

### In-Container Healthcheck Implementation

The container healthcheck is implemented without dependencies beyond the chosen Alpine base image:

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
```

Alpine's built-in `/bin/wget` (provided by BusyBox) sends an HTTP request to `/healthz`. It succeeds with exit code `0` on HTTP 200, and exits non-zero if the server is unreachable or reports an error. No external packages, host tools, or sidecars are required.

---

## 5. Optional Netease QR Login Integration & Encrypted Session Storage

TuneBridge includes an optional QR-code-based login flow for Netease Cloud Music. This functionality is dormant by default and is activated only when `TUNEBRIDGE_NETEASE_API_BASE_URL` is configured.

### Upstream Service Endpoint Requirement

- `TUNEBRIDGE_NETEASE_API_BASE_URL` must point to an absolute URL of a user-chosen or self-hosted service compatible with [NeteaseCloudMusicApi](https://github.com/Binaryify/NeteaseCloudMusicApi).
- **No public default endpoint is provided.** Users must host or provide their own upstream adapter service.
- If `TUNEBRIDGE_NETEASE_API_BASE_URL` is configured, `TUNEBRIDGE_SESSION_ENCRYPTION_KEY` is **strictly required**. The service fails validation and halts startup if the encryption key is absent.

### Session Encryption Key Requirement

- `TUNEBRIDGE_SESSION_ENCRYPTION_KEY` must decode to a 32-byte binary key (supporting standard or URL-safe base64 encoding).
- This 256-bit key is used by the internal cipher to encrypt and decrypt session payloads using AES-GCM.
- Generate a cryptographically secure key with:
  ```bash
  openssl rand -base64 32
  ```
  *(Never use hardcoded dummy secrets or commit real secrets to source control.)*

### Basic-Auth-Protected API Routes

When enabled, the server mounts two dedicated HTTP endpoints. Both endpoints are protected by the same HTTP Basic Authentication credentials configured for WebDAV (`TUNEBRIDGE_WEBDAV_USERNAME` and `TUNEBRIDGE_WEBDAV_PASSWORD`, realm: `"TuneBridge"`). Unauthorized requests receive `401 Unauthorized`.

1. **Initiate QR Login Session (`POST /api/sources/netease/login/qr`)**
   - Requests upstream `/login/qr/key` and `/login/qr/create`.
   - **Response (`201 Created`)**:
     ```json
     {
       "key": "example-qr-key",
       "url": "https://music.163.com/login?codekey=...",
       "image_data": "data:image/png;base64,..."
     }
     ```
   - If the upstream service is unreachable or returns an error, returns `502 Bad Gateway`.
   - Methods other than `POST` return `405 Method Not Allowed`.

2. **Check / Poll QR Login Status (`GET /api/sources/netease/login/qr/{key}`)**
   - Checks the authentication status for `{key}` via upstream `/login/qr/check`.
   - **Response (`200 OK`)**:
     ```json
     {
       "status": "waiting"
     }
     ```
   - An empty or malformed key (e.g. containing slashes) returns `400 Bad Request`.
   - Upstream communication failures return `502 Bad Gateway`.
   - Methods other than `GET` return `405 Method Not Allowed`.

### QR Status Meanings

The returned `status` string corresponds to upstream login progression:
- `waiting`: Waiting for the user to scan the QR code (upstream code 801).
- `awaiting_confirmation`: QR code has been scanned, awaiting user confirmation on the mobile app (upstream code 802).
- `authorized`: User confirmed authorization; login succeeded (upstream code 803). TuneBridge extracts the session cookies, encrypts them, and persists them to SQLite.
- `expired`: The QR code session expired before confirmation (upstream code 800).

### Encrypted SQLite Storage & Privacy Guarantee

- **AES-GCM Encryption**: When status reaches `authorized`, TuneBridge extracts the upstream session cookies (`MUSIC_U`, etc.) and encrypts the payload using AES-GCM with a fresh random nonce.
- **SQLite Persistence**: Encrypted session bytes are upserted into the `source_sessions` table in SQLite (`encrypted_payload BLOB`, `key_version INTEGER`, `expires_at TEXT`, `updated_at TEXT`).
- **Zero API Exposure**: The API response strictly returns `{"status":"authorized"}`. Session cookies are **never returned by the API** nor exposed in HTTP responses or request logs, preventing credential leakage to clients.

### Library Playback Disclaimer

> [!NOTE]
> Library playback and audio stream resolution are **not implemented** in the current release. The WebDAV interface provides a skeleton virtual directory structure, and requests to fetch track audio stream content return `503 Service Unavailable` (`stream resolver is not configured`).

---

## 6. Environment Variables Reference

| Variable | Required? | Default | Description |
|---|---|---|---|
| `TUNEBRIDGE_WEBDAV_USERNAME` | **Yes** | *(None)* | WebDAV HTTP Basic Auth username. Anonymous access is prohibited. |
| `TUNEBRIDGE_WEBDAV_PASSWORD` | **Yes** | *(None)* | WebDAV HTTP Basic Auth password. Anonymous access is prohibited. |
| `TUNEBRIDGE_DATA_DIR` | No | `/data` (in container) | Root directory for application state and cache storage. |
| `TUNEBRIDGE_LISTEN_ADDRESS` | No | `:8080` | Network address and TCP port to bind the HTTP server. |
| `TUNEBRIDGE_DATABASE_PATH` | No | `$TUNEBRIDGE_DATA_DIR/tunebridge.db` | Absolute or relative path to SQLite database. |
| `TUNEBRIDGE_AUDIO_CACHE_DIR` | No | `$TUNEBRIDGE_DATA_DIR/cache/audio` | Directory for cached audio tracks. |
| `TUNEBRIDGE_CACHE_MAX_BYTES` | No | `10737418240` (10 GB) | Maximum cache size in bytes before eviction. |
| `TUNEBRIDGE_PLAYLIST_TTL` | No | `5m` | In-memory cache duration for playlist metadata. |
| `TUNEBRIDGE_DAILY_RECOMMENDATION_TTL` | No | `1h` | In-memory cache duration for daily recommendation feeds. |
| `TUNEBRIDGE_NETEASE_API_BASE_URL` | Optional | *(None)* | Absolute URL pointing to a user-chosen or self-hosted NeteaseCloudMusicApi-compatible service. There is no public default endpoint. |
| `TUNEBRIDGE_SESSION_ENCRYPTION_KEY` | Optional* | *(None)* | Base64-encoded 32-byte key for AES-GCM session encryption. *Mandatory when `TUNEBRIDGE_NETEASE_API_BASE_URL` is configured. |

---

## 7. Execution Guide

### Using Docker Compose

1. Export the mandatory credentials (and optional Netease configuration) in your caller environment:
   ```bash
   export TUNEBRIDGE_WEBDAV_USERNAME="myuser"
   export TUNEBRIDGE_WEBDAV_PASSWORD="mysecretpassword"

   # Optional: Netease QR login integration
   # export TUNEBRIDGE_NETEASE_API_BASE_URL="http://netease-api:3000"
   # export TUNEBRIDGE_SESSION_ENCRYPTION_KEY="<base64-encoded-32-byte-key>"
   ```

2. Start the service:
   ```bash
   docker compose up -d
   ```

If mandatory variables are omitted from your environment, Docker Compose terminates immediately with an error indicating that the required variable is unset.

### Using Standalone Docker CLI

```bash
docker run -d \
  --name tunebridge \
  -p 8080:8080 \
  -v tunebridge-data:/data \
  -e TUNEBRIDGE_DATA_DIR=/data \
  -e TUNEBRIDGE_WEBDAV_USERNAME="myuser" \
  -e TUNEBRIDGE_WEBDAV_PASSWORD="mysecretpassword" \
  -e TUNEBRIDGE_NETEASE_API_BASE_URL="http://netease-api.local:3000" \
  -e TUNEBRIDGE_SESSION_ENCRYPTION_KEY="<base64-encoded-32-byte-key>" \
  tunebridge:latest
```
