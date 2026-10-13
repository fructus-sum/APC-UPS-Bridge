# Architecture

APC UPS Bridge is a single Go application for monitoring one USB APC UPS through `apcupsd` and coordinating optional network power protection. The web assets are embedded in the executable. It uses only the Go standard library.

## Components

| Source | Role |
|---|---|
| `cmd/apc-ups-bridge/` | CLI flags, process lifecycle, polling, server and worker orchestration |
| `cmd/apc-ups-bridge-host-helper/` | Small root-run helper, installed as `/usr/local/libexec/apc-ups-bridge-host-helper` and started by a systemd socket; applies the Settings page's apcupsd threshold changes for the unprivileged service |
| `internal/apc/` | Executes and parses `apcaccess status` |
| `internal/monitor/` | Periodic UPS readings, health classification, cached status and transition events |
| `internal/events/` | `events.go` event-only JSONL files and retention; `types.go` event type constants; `read.go` filtered and CSV/report reads; `power_timeline.go` outage and power timeline built from events |
| `internal/history/` | Bounded 15-minute voltage and load samples, 30-day retention and graph data |
| `internal/auth/` | Administrator password hash, sessions, login limits and CSRF |
| `internal/config/` | Monitoring preferences and atomic file updates |
| `internal/fileutil/` | Atomic file writes; the directory sync step is skipped on Windows |
| `internal/power/` | `config.go` protected-host rules; `controller.go` automation controller and durable outage state; `actions.go` SSH shutdown and Wake-on-LAN actions; `broadcast_linux.go` / `broadcast_other.go` Wake-on-LAN broadcast addresses; `status.go` display-only device online monitor; `ping.go`, `ping_linux.go`, `ping_other.go` ping check; `ssh_probe.go` connect-only SSH port check and Test SSH; `ssh_identity.go` reads the service user's public key |
| `internal/notify/` | SMTP configuration (`config.go`), durable outage incidents and final reports (`engine.go`), email delivery (`smtp.go`) |
| `internal/hostsettings/` | Validates and applies the local apcupsd shutdown thresholds with a backup and atomic file replace; used by the host helper |
| `internal/firmware/` | Offline, conservative UPS firmware reference comparison |
| `internal/web/` | Authenticated HTTP server: `web.go` routes, sessions and base handlers; `status.go` device online status API (reads the monitor, never starts a check); `history.go` graph data; `email.go` email settings and status; `security.go` read-only Fail2ban event feed; `power_diagnostics.go` power rule and SSH diagnostics |
| `internal/web/assets/` | Embedded front end: `app.html` (all main pages), `login.html`, `email.html`, `app.css`, `common.js` (shared fetch, CSRF post and visible-tab polling helpers), `app.js`, `history.js`, `email.js` |
| `packaging/systemd/` | `apc-ups-bridge.service`, `apc-ups-bridge-host-settings.socket` and `apc-ups-bridge-host-settings.service` |
| `packaging/fail2ban/` | Filter, ban/unban action, event-export script and logrotate rule for the Fail2ban jail |
| `scripts/` | `install.sh`, `uninstall.sh`, `setup-fail2ban.sh`, `test-install.sh` (installer checks), `build-release.sh` |
| `tools/` | `setup-debian-shutdown-user.sh` and `setup-bridge-ssh.sh`; see [tools/README.md](../tools/README.md) |

## Runtime flow

1. `apcupsd` communicates with the USB UPS. The bridge runs `apcaccess status` on a configurable 5-60 second interval (10 seconds by default).
2. The monitor caches the current reading in memory, with an explicit unhealthy/unknown state for absent or contradictory data. Web requests read the cache, not the USB device.
3. Significant transitions append event records; routine samples are not written to event logs. One voltage and load sample per 15 minutes goes to the history store for the graphs.
4. If remote power automation is explicitly enabled, its controller evaluates each observation, persists action intent and state, and coordinates approved remote-host SSH shutdowns and Wake-on-LAN recovery.
5. A device online monitor checks each protected device (ping, SSH port connect, or off, per device) for the dashboard's Protected devices panel. It is display only and never triggers an action.
6. A separate email worker handles outage and restoration alerts and pending final reports without holding up power actions or dashboard requests.
7. One HTTP server serves the administrator login and these pages: Dashboard, History graphs, Events & reports, Power rules & devices, Email notifications, Login security and Settings. The Login security page reads the exported Fail2ban ban/unban log; Fail2ban itself runs separately and privileged.
8. The Settings page's local host thresholds go through the host-settings socket to the root helper, which edits `apcupsd.conf`.

The binary defaults to `127.0.0.1:8080`. The installed service is set to `0.0.0.0:8080` by the installer; see [Installation](INSTALL.md#network-access). The `apcupsd` NIS listener can stay bound to `127.0.0.1`.

## Files on disk

| Path on an installed system | Contents |
|---|---|
| `/usr/local/bin/apc-ups-bridge` | Main binary |
| `/usr/local/libexec/apc-ups-bridge-host-helper` | Host-settings helper binary |
| `/usr/local/sbin/apc-ups-bridge-uninstall` | Local copy of the uninstaller |
| `/etc/systemd/system/apc-ups-bridge.service`, `apc-ups-bridge-host-settings.socket`, `apc-ups-bridge-host-settings.service` | Unit files |
| `/etc/apc-ups-bridge/settings.json` | Polling interval, low-charge warning, retention |
| `/etc/apc-ups-bridge/power.json` | Approved remote hosts and power rules |
| `/etc/apc-ups-bridge/service.env` | Web bind address and port |
| `/var/lib/apc-ups-bridge/auth.json` | Administrator password verifier |
| `/var/lib/apc-ups-bridge/power-state.json` | Persisted power cycle, shutdown and wake attempts |
| `/var/lib/apc-ups-bridge/email.json` | SMTP configuration and credential |
| `/var/lib/apc-ups-bridge/email-state.json` | Email incident, interruptions and report-delivery state |
| `/var/lib/apc-ups-bridge/history/samples-YYYY-MM-DD.jsonl` | 15-minute voltage and load samples, kept 30 days |
| `/var/lib/apc-ups-bridge/.ssh/` | SSH identity and `known_hosts` of the service user |
| `/var/log/apc-ups-bridge/events-YYYY-MM.jsonl` | Significant event history |
| `/var/log/apc-ups-bridge-fail2ban/events.log` | Fail2ban ban/unban feed read by the Login security page |
| `/etc/fail2ban/jail.d/apc-ups-bridge.local` | Fail2ban jail (Debian/Ubuntu) |
| `/etc/apcupsd/apcupsd.conf` | Edited by the helper, with timestamped `.backup-*` copies |
| `/run/apc-ups-bridge-host-settings.sock` | Helper socket, mode 0600, owned by the service user |

Configuration and state updates use restricted files and atomic writes. A pending email incident pauses event-log retention cleanup. The service account has the `/usr/sbin/nologin` shell, and `apcupsd` remains a separate service responsible for the USB device and native local-host shutdown.

## Feature documentation

- Remote shutdown, Wake-on-LAN, retries and recovery: [Power protection](POWER-PROTECTION.md); SSH identity setup: [SSH keys](SSH-KEYS.md).
- Alerts, final reports and cooldowns: [Email notifications](EMAIL-NOTIFICATIONS.md).
- Graphs, sampling and retention: [History](HISTORY.md).
- Battery panel and manual battery test: [Battery](BATTERY.md).
- Login limits and Fail2ban: [Login security](LOGIN-SECURITY.md).
- Firmware comparison: [Firmware](FIRMWARE.md).

## Limits

- One locally attached `apcupsd` UPS; not a multi-UPS manager or a NUT server.
- No per-poll history, no outage timings while the bridge is off, and no delivery acknowledgement from a remote operating system.
- Remote shutdown success and SSH-port disappearance are limited signals, not proof that every application shut down cleanly.
- Direct HTTP offers no encryption of passwords or sessions.

## Development

### Requirements

Go 1.22 or later (the `go` line in `go.mod`). A working `apcaccess status` is needed only to read a real UPS. Bash and Node.js are needed for the installer checks and the JavaScript syntax check.

### Build

```bash
go build -o apc-ups-bridge ./cmd/apc-ups-bridge
go build -o apc-ups-bridge-host-helper ./cmd/apc-ups-bridge-host-helper
```

### Run locally

Point every path flag at a scratch directory so no system path is touched:

```bash
mkdir -p ./data
printf '%s\n' 'a-password-of-12-or-more-characters' | ./apc-ups-bridge --auth-file ./data/auth.json --set-password-stdin

./apc-ups-bridge \
  --auth-file ./data/auth.json \
  --settings-file ./data/settings.json \
  --power-config ./data/power.json \
  --power-state ./data/power-state.json \
  --email-config ./data/email.json \
  --email-state ./data/email-state.json \
  --log-dir ./data/events \
  --history-dir ./data/history \
  --listen 127.0.0.1:8080
```

Sign in at `http://127.0.0.1:8080/` as `admin`. Other flags: `--apcaccess` (executable path, useful for pointing at a script that prints a fixture from `tests/fixtures/`), `--interval`, `--timeout`, `--retention-days`, `--low-battery` (initial values, overridden by saved settings) and `--version`. To read the UPS once and print JSON without starting the web server:

```bash
./apc-ups-bridge --once --settings-file ./data/settings.json --log-dir ./data/events
```

### Tests

```bash
go test ./...
go test -race ./...
go vet ./...
node --check internal/web/assets/app.js
node --check internal/web/assets/history.js
bash -n scripts/*.sh tools/*.sh
bash scripts/test-install.sh
```

Some tests exercise Linux-only features and pass only on Linux. `scripts/test-install.sh` is unprivileged and changes nothing on the host. Automated tests do not replace checking a real USB UPS, systemd permissions, and shutdown and wake behavior on your own equipment.

### Continuous integration

`.github/workflows/ci.yml` runs on pushes to `main` and on pull requests. It runs `go test ./...`, the race tests, `go vet ./...`, `node --check` on `app.js` and `history.js`, `bash -n` on the shell scripts, and `scripts/test-install.sh`.

### Releases

Pushing a tag that starts with `v` runs `.github/workflows/release.yml`. It runs the tests, builds Linux archives for amd64, arm64, armv7 and armv6 with `scripts/build-release.sh vX.Y.Z output-dir` (each containing the `apc-ups-bridge` binary, plus a `SHA256SUMS` file) and publishes them as a GitHub release. The installer does not use these archives; it builds from source.

### Updating the firmware reference table

The table lives in `internal/firmware/firmware.go`. Each entry in the `latest` map is keyed by `family/UPS ID` (for example `SMT/1015`), because the same UPS ID can mean different firmware in different product families, and its value is the newest firmware version. To update it:

1. Review Schneider's firmware table and release notes.
2. Change `latest` entries only for confirmed family/UPS ID pairs.
3. Set `ReferenceDate` to the date of the review.
4. Keep model exceptions and exclusion checks current.
5. Add a test for every new family or special case.

The web warning is advisory and links to the manufacturer; it makes no network requests. See [Firmware](FIRMWARE.md) for what users see.
