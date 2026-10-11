# Phase 3 — password-protected web dashboard and reports

Status: **development preview**. No installation release, remote shutdown or Wake-on-LAN actions are enabled.

## Components

- `internal/web`: embedded responsive HTML, CSS and vanilla JavaScript with one standard-library HTTP server.
- `internal/auth`: account password hashing, authentication, short-lived in-memory sessions and brute-force limits. Only `admin` is supported for now.
- `internal/config`: JSON file containing three safe monitoring preferences. Updates use an atomic rename and are applied to the polling loop without restarting.
- `internal/events/read.go`: scans bounded JSONL event history and produces counts, observed durations and CSV exports without a database.
- `cmd/apc-ups-bridge`: CLI to initialize the admin password and run the monitor/web server together.

## Running

Build from repository root with `go build -o apc-ups-bridge ./cmd/apc-ups-bridge`. Initialize using `--set-password-stdin` and `--auth-file`; see README for a shell recipe that avoids putting the password in process arguments.

The server refuses to start unless an admin password hash already exists. The initial bind address `127.0.0.1:8080` is loopback-only; users must explicitly enable a LAN listener with `--listen 0.0.0.0:8080`. All browser pages except the sign-in page, and all API endpoints, require an authenticated session. The CSS used to render the sign-in page is public.

For a private LAN, standard HTTP is a deliberate size/complexity trade-off. It does not prevent network interception. Use a trusted VPN or HTTPS reverse proxy for sensitive access, without port forwarding direct HTTP to the internet.

## Routes

| Path | Method | Purpose |
|---|---|---|
| `/login` | GET/POST | Admin login |
| `/` | GET | Current UPS metrics, health and latest events |
| `/events` | GET | Event history and aggregate outage report |
| `/settings` | GET | Edit monitoring preferences and password |
| `/api/status` | GET | Cached monitor status; hardware is never polled on web requests |
| `/api/events?days=30&limit=100` | GET | Latest event records; max 500 |
| `/api/report?days=30` | GET | Outage and failure summary calculated from retained logs |
| `/api/events.csv?days=30&limit=500` | GET | CSV export, latest max 500 event records |
| `/api/settings` | GET/POST | Read/update monitoring preferences; POST requires CSRF header |
| `/change-password` | POST | Password rotation; revokes active sessions |
| `/logout` | POST | Revoke the current session |

For API queries: `days=0` means all retained history; accepted range 0–36500. Only event changes generate disk writes, so no samples are available for continuous battery charts. Metrics absent from an older UPS are shown as "Not reported" rather than misleading zero values.

## Security notes and limitations

- Password file is created mode 0600, with a salted PBKDF2-HMAC-SHA256 hash (240,000 iterations). The server has no third-party authentication service.
- Browser sessions use 32-byte crypto-random tokens kept as hashes in memory, with 8-hour expiry. A restart signs users out. Cookies are HttpOnly and SameSite Strict; Secure is set for directly TLS-terminated requests.
- Login attempts are limited per network address. Settings changes and password changes require a per-session CSRF token.
- Security headers prevent framing, reduce content-type sniffing and disallow external JS/CSS resources. The interface loads no CDN assets.
- LAN HTTP remains unencrypted. Terminate HTTPS at a trusted proxy or tunnel if remote clients are used.
- TCP web-port selection, CLI reset of admin password, and the `apcaccess` command path currently require process restart. Monitoring threshold, poll interval (5-60 s) and retention update live.
- Event history is read on demand, bounded to 500 returned records. It is not a time-series database. An incomplete outage or communication interruption does not yield guessed downtime.
- This release does not provide actual remote SSH shutdown, Wake-on-LAN recovery, a systemd service or UPS firmware updating.

## Local verification

Run `go test ./...`, `go test -race ./...` and `go vet ./...`. The tests cover parser fixtures, monitoring transitions, event logging and reports, password lifecycle, sessions, CSRF rules, route authentication, settings validation and atomic saving. A real APC UPS and target network must still be tested before a public release.
