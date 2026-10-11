# Architecture proposal — v1

Status: **proposal, not implemented**. This document records intended scope and design decisions for review.

## Product goal
Turn an older USB-connected APC UPS into a LAN-accessible UPS status and protection gateway using a Raspberry Pi or another supported Linux host, without a database or cloud account.

## Supported v1 environment
- Linux with systemd; initially target Raspberry Pi OS, Debian and Ubuntu (ARMv6/ARMv7, ARM64, x86-64).
- One USB-connected APC UPS supported by apcupsd/apcaccess.
- apcupsd keeps ownership of USB communication. No custom USB driver.
- A private LAN is the intended deployment environment.

## Design goals
1. One self-contained Go executable using standard-library HTTP and embedded HTML/CSS/JS.
2. No database, external web framework, cloud service or telemetry.
3. No regular disk writes for normal UPS polls; significant events only.
4. Works unattended without a browser session.
5. Safe defaults, especially around shutdown and waking remote devices.
6. The installation script builds the binary from source on the target with a temporary Go toolchain (no release binaries are published).

## Proposed source tree
```text
cmd/apc-ups-bridge/main.go
internal/apc/             # Read and parse apcaccess data, unit tests and fixtures
internal/monitor/         # Polling, cached snapshot, health
internal/power/           # Explicit outage/recovery state machine, tests
internal/remote/          # SSH and WoL actions with strict timeouts
internal/events/          # Event-only JSON Lines, retention, report scans
internal/config/          # Read/validate/atomic-write config
internal/auth/            # Password setup, sessions, CSRF protection
internal/web/             # HTTP routes, embedded static assets and templates
web/                      # Small HTML, CSS and JS sources
scripts/install.sh
scripts/uninstall.sh
packaging/systemd/
docs/
tests/fixtures/           # Example apcaccess outputs and state transitions
```

## Runtime design
- Go process polls apcaccess (through apcupsd) on a configurable interval (5-60 seconds), defaults proposed at 10 seconds. External commands have timeouts.
- Cache readings in memory; serve status to browser from cache. Never poll on each web request.
- Serve login, dashboard, event reports and admin settings on one configurable HTTP port (proposed 8080).
- Configure apcupsd NIS on 127.0.0.1:3551 when compatible with the existing setup. Don't unnecessarily expose NIS.
- Prefer browser polling over WebSockets for v1.
- Treat unsupported UPS metrics as unknown, not zero.

## Protection state and event model
- States: ONLINE, ON_BATTERY, SHUTDOWN_REQUIRED, WAITING_FOR_RECOVERY, RECOVERED; plus communication fault handling.
- Store only the minimal state required to avoid repeated shutdown or wake actions after crashes/restarts.
- Recover stale state by reconciling current UPS status with persisted state.
- Never attempt wake-ups until mains power is confirmed stable and battery charge meets the configured threshold.
- Configurable remote devices: name, address, MAC, enable/disable shutdown and wake, action thresholds/order, SSH user and port.
- SSH: key-based authentication with known-host checking; no web-entered arbitrary shell command.
- Use Go UDP for Wake-on-LAN; preserve broadcast/interface customization where necessary.
- Local Linux host protection needs coordination with apcupsd's native shutdown behavior. Never enable competing shutdown mechanisms by default.
- UPS on-battery, low-battery and communication-failure transitions require hardware tests before release.

## Storage
```text
/etc/apc-ups-bridge/config.json
/etc/apc-ups-bridge/auth.json
/var/lib/apc-ups-bridge/state.json
/var/log/apc-ups-bridge/events-YYYY-MM.jsonl
```
- Each event contains UTC timestamp, type, level, device identifier, and event-specific fields.
- Do not log regular UPS samples. No event = no new log file.
- Monthly JSON Lines files are created on demand, with configurable retention (default proposal: 365 days; longer or unlimited allowed).
- Optional maximum log budget; documented oldest-first retention and careful handling of current file.
- Repeated communication failures are consolidated, and recovery logged once.
- Use atomic state/config writes and permissions that protect password material.

## Authentication and security
- First-run setup requires creation of an administrator password.
- Store password hashes with a salted deliberately slow KDF, and session tokens securely; no plaintext passwords.
- HttpOnly, SameSite cookies; session expiry; CSRF protection for changes; rate-limited login.
- Unauthenticated API and configuration writes are disabled by default; optional status-only read access is a separate future decision.
- On direct unencrypted HTTP, LAN traffic is not confidential. Recommend HTTPS behind an existing trusted proxy/VPN for untrusted networks.
- Web process runs unprivileged; use narrow system permissions for optional local shutdown.
- No port forwarding/internet exposure by default.

## Installer and operations
- Bash installer detects distro, CPU architecture, init system, apcupsd availability and existing configuration.
- Build from the repository source with a temporary, checksum-verified Go toolchain; install minimal OS packages as needed.
- Back up files before modifying apcupsd; never replace working config without clear approval.
- Create a dedicated user, secure config/state/log directories, install systemd service, enable/start it.
- Support re-runnable installations, updates, uninstall and config backups.
- Test installation on multiple Linux variants; explicitly report unsupported systems.

## v1 web pages
1. Dashboard — UPS connectivity, mains status, battery %, runtime, load and voltage if available, remote device status, last event.
2. Events and Reports — power outage timeline, outage duration, action history, filters/export.
3. Devices — add/edit/remove remote SSH/WoL devices and validate addresses.
4. Power Rules — thresholds, delays, safe recovery, local shutdown enable with warnings.
5. System — UPS polling, web port, event retention, password, service health, version.

## Out of scope for v1
- Multiple UPS units
- NUT/native network UPS protocols
- Full continuous historical charts
- Email/push/cloud notifications
- Multi-user account management
- Automatic public internet access
- Arbitrary remote command editor

## Acceptance tests (minimum)
- ONLINE -> ON_BATTERY -> ONLINE short interruption without shutdown.
- Low battery triggers exactly one eligible shutdown per device.
- Mains returns before/after shutdown; only approved WoL actions run once.
- Restart while on battery, during shutdown or recovery.
- apcaccess fails, returns incomplete output, or UPS goes missing.
- Logging/retention under low disk space and corrupt/truncated event lines.
- Login/session/CSRF and unauthorized settings mutations.
- Service starts on boot; installer repeat run and upgrade preserve configs.
