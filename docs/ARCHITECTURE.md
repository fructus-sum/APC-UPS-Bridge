# Architecture

APC UPS Bridge is a single Go application for monitoring one USB APC UPS through `apcupsd` and coordinating optional network power protection. The web assets are embedded in the executable. No database, external web service, or third-party Go modules are required.

## Components

| Source | Role |
|---|---|
| `cmd/apc-ups-bridge/` | CLI flags, process lifecycle, polling, server and worker orchestration |
| `cmd/apc-ups-bridge-host-helper/` | Small root-run helper started by a systemd socket; applies the Settings page's apcupsd threshold changes on behalf of the unprivileged service |
| `internal/apc/` | Executes and parses `apcaccess status` |
| `internal/monitor/` | Periodic UPS readings, health classification, cached status and transition events |
| `internal/events/` | Event-only JSONL files, retention, CSV/report reads |
| `internal/history/` | Bounded 15-minute voltage and load samples, 30-day retention and graph data |
| `internal/auth/` | Administrator password hashes, sessions, login limits and CSRF |
| `internal/config/` | Monitoring preferences and atomic file updates |
| `internal/power/` | Protected-host rules, durable outage state, SSH shutdown, UDP Wake-on-LAN and display-only device online status (ping / SSH-port connect) |
| `internal/notify/` | SMTP configuration, durable outage incidents, email delivery and final reports |
| `internal/hostsettings/` | Validates and applies the local apcupsd shutdown thresholds (battery %, runtime, timer) with a backup and atomic file replace; used by the host helper |
| `internal/firmware/` | Offline, conservative UPS firmware reference comparison |
| `internal/web/` | Authenticated HTTP handlers, templates and embedded HTML/CSS/JS |
| `scripts/` | Source-based installer, installer checks, uninstaller and release build helper |
| `packaging/systemd/` | Reference systemd unit |

## Runtime flow

1. `apcupsd` communicates with the USB UPS. The bridge runs `apcaccess status` on a configurable 5–60-second interval (10 seconds by default).
2. The monitor caches the current reading in memory, with an explicit unhealthy/unknown state for absent or contradictory data. Web requests read the cache, not the USB device.
3. Significant transitions append event records; routine samples are not written to event logs. One voltage and load sample per 15 minutes is kept separately for 30 days for the history graphs.
4. If remote power automation is explicitly enabled, its controller evaluates each observation, persists action intent/state, and coordinates approved remote-host SSH shutdowns and WoL recovery.
5. A separate email worker handles outage/restoration alerts and pending recovery summaries without holding up remote power actions or dashboard requests.
6. One standard-library HTTP server serves administrator login, dashboard, history, power rules, email settings and monitoring settings. Firmware warnings are read-only.

The normal binary defaults to `127.0.0.1:8080`. The interactive installer defaults the **installed service** to `0.0.0.0:8080` for LAN access; that is plain HTTP, so the installation must be kept on a trusted network or protected by a VPN/TLS proxy. The `apcupsd` NIS listener can stay bound to `127.0.0.1` and should not be exposed publicly.

## Disk state

| Path on an installed system | Contents |
|---|---|
| `/etc/apc-ups-bridge/settings.json` | Polling interval, low-charge warning, retention |
| `/etc/apc-ups-bridge/power.json` | Approved remote hosts and power rules |
| `/etc/apc-ups-bridge/service.env` | Web bind address/port |
| `/var/lib/apc-ups-bridge/auth.json` | Administrator password verifier |
| `/var/lib/apc-ups-bridge/power-state.json` | Persisted power cycle, shutdown/wake attempts |
| `/var/lib/apc-ups-bridge/email.json` | SMTP configuration and credential |
| `/var/lib/apc-ups-bridge/email-state.json` | Persistent email incident, interruptions, report-delivery state |
| `/var/lib/apc-ups-bridge/history/samples-YYYY-MM-DD.jsonl` | 15-minute voltage and load samples, kept 30 days |
| `/var/log/apc-ups-bridge/events-YYYY-MM.jsonl` | Significant event history |

Configuration and state updates use restricted files and atomic writes. A pending email incident pauses event-log retention cleanup. The installed service uses a dedicated `apc-ups-bridge` system account with `/usr/sbin/nologin`, systemd sandboxing and restricted writable paths. `apcupsd` remains a separate service responsible for the USB device and native local-host shutdown.

## Failure and recovery behavior

Remote shutdown is disabled by default, requires an observed healthy online state before arming, and is subject to explicit thresholds, verification and bounded retries. WoL is sent only for eligible hosts under recovery rules. Persisted state avoids resetting action budgets after a bridge restart. Details: [Power protection](POWER-PROTECTION.md).

Email alerts use one-shot sends with an anti-flapping cooldown; final reports persist through repeat outages and bridge restarts. A report is eligible only after ONLINE mains and 100% reported UPS charge, with optional SSH reachability checks. SMTP failures must not block power actions. Details: [Email notifications](EMAIL-NOTIFICATIONS.md).

Firmware information comes from `apcaccess` and a dated built-in table. It does not check the internet for the latest version or perform firmware flashing. Details: [Firmware](FIRMWARE.md).

## Operational limits

- One locally attached `apcupsd` UPS, not a multi-UPS manager or a NUT server.
- No continuous per-poll history, guaranteed outage timings while the bridge is powered off, or delivery acknowledgements from a remote operating system.
- Remote shutdown success and SSH-port disappearance are limited signals, not proof every application shut down cleanly.
- Direct HTTP offers no encryption of passwords or sessions.
- Automated tests cover many state transitions; physical power-loss, different USB UPS models and target-host behavior still require hardware qualification.

See [Installation](INSTALL.md) for the supported installer workflow and administration commands.
