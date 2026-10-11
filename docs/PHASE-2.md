# Phase 2 — UPS monitoring core (read-only)

This phase implements the hardware poller, in-memory status, conservative state transitions, and event-only JSONL logs. **No web server, SSH shutdown, Wake-on-LAN, installer, or production service file is included yet.** Do not use this version for equipment protection.

## Requirements

- Go 1.22 or newer to build (not a runtime dependency after building).
- An APC USB UPS managed by an independently configured and running `apcupsd`.
- `apcaccess status` must work locally. `apcaccess` normally communicates through apcupsd's NIS port; restrict that listener to localhost.

## Build and inspect

```sh
go test ./...
go build -o apc-ups-bridge ./cmd/apc-ups-bridge
./apc-ups-bridge --once --log-dir ./test-events
```

The single-run JSON output describes the current reading and health. This is not a hosted API yet. Polling without `--once` runs until SIGTERM/SIGINT:

```sh
./apc-ups-bridge --interval 10s --timeout 4s --log-dir ./test-events
```

The executable needs write permission to its log directory only when an event occurs. For manual use, supply a writable directory explicitly; the future installer will provision `/var/log/apc-ups-bridge` with restricted permissions.

## Command flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--apcaccess` | `apcaccess` | Executable path or name in PATH |
| `--interval` | `10s` | Poll interval (whole seconds, 5-60 s) |
| `--timeout` | `4s` | Command timeout |
| `--log-dir` | `/var/log/apc-ups-bridge` | Directory for event logs |
| `--retention-days` | `365` | Whole-month log cleanup; `0` is unlimited |
| `--low-battery` | `20` | **Event threshold only**; does not shut anything down |
| `--once` | false | Make one reading and print JSON |

## Events

Only changes are logged: `power_lost`, `on_battery_detected`, `low_battery`, `power_restored`, `ups_communication_lost`, and `ups_communication_restored`. Existing event logs are kept as one JSON object per line, in monthly files `events-YYYY-MM.jsonl`. A normal polling day creates **no log file**. Repeated identical communication errors are consolidated into one loss event until healthy readings resume.

Battery percentages, runtime and voltages unsupported by a UPS are represented as *missing*, not zero. An ambiguous UPS status is treated as a communication/health fault, **never as confirmation of restored mains**.

Outage duration in `power_restored` reflects the monitoring process's observation window, not a guaranteed utility outage duration. Restart persistence will be added with the protection state machine; timestamps spanning a restart are not yet reconstructed.

Retention removes only monthly files *completely outside* the retention period, to avoid deleting newer events in the same file. Days without changes are not stored. Retention cleanup runs on startup and daily while the monitor is active; `0` disables cleanup.

## Testing approach

Automated tests use fixture output from representative legacy APC UPS responses, including missing metrics, composite statuses, N/A values and communication loss. They do not require hardware. **Hardware testing remains mandatory** before any later phase can activate shutdown/recovery actions.
