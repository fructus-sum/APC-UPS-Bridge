# APC UPS Bridge

**Give your older USB-connected APC UPS a new life on the network.**

APC UPS Bridge turns a Raspberry Pi or Linux host into a lightweight, self-hosted gateway for USB APC UPS units supported by `apcupsd`. It provides a password-protected dashboard, live readings, event-only logs and reports, editable monitoring preferences, and **opt-in** remote power protection. It uses **one Go executable, no database, no cloud service, and no third-party Go modules**.

> **Development preview (Phase 4), not a production release.** Remote SSH shutdown and Wake-on-LAN are implemented behind an explicit disabled-by-default safety switch, but have **not been tested on real UPS hardware**. Automatic local host shutdown and an installer/systemd service are not yet included. Do not rely on this preview to protect equipment.

## Features available through Phase 4

- Live mains/battery state, charge, runtime, load, voltage, UPS model and communication health when reported by the UPS.
- Password-protected dashboard, events/reports and settings; administrator login and password rotation.
- Conservative firmware version advisory on the web dashboard for recognized, reference-matched UPS devices (manual update guidance only). See [firmware checker](docs/FIRMWARE.md).
- Security controls: salted PBKDF2-SHA256 password hashing, ephemeral authenticated sessions, login attempt limits, and CSRF tokens on updates.
- Event-only monthly JSONL logs, adjustable retention including unlimited; CSV download for recent events.
- Editable polling interval (5–3600 s), low battery **warning** level (0–100%), and event retention (0–36500 days). Changes are applied without restarting.
- All web routes share one configurable HTTP port. Default bind address is `127.0.0.1:8080` to avoid exposing the administrator password over the network unintentionally.
- Add multiple explicitly approved remote Linux hosts with SSH identity, optional `sudo -n`, shutdown order/priority, MAC and Wake-on-LAN destination.
- Remote power actions are globally **disabled by default**, and each host must separately allow shutdown and optional wake.
- Shutdown requires a previously healthy ONLINE observation, ON_BATTERY confirmation delay and a configured battery, UPS LOWBATT, or runtime trigger.
- Recovery requires stable, healthy ONLINE power and adequate battery charge; devices only wake if their shutdown SSH command reported success.
- Durable, tiny JSON power-action state prevents repeated remote actions after restarts. No periodic normal-status log writes.

## Quick start (development/testing)

Prerequisites: Go 1.22+ for building, an APC USB UPS configured with `apcupsd`, and `apcaccess` available in PATH. The compiled executable does not require Go. On a minimal Linux/Raspberry Pi, `apcupsd` handles USB communication and may need its separate NIS listener restricted to localhost.

```bash
go test ./...
go build -o apc-ups-bridge ./cmd/apc-ups-bridge

mkdir -p ./data
read -rs -p 'Choose a long administrator password: ' UPS_BRIDGE_PASS; echo
printf '%s\n' "$UPS_BRIDGE_PASS" | ./apc-ups-bridge --auth-file ./data/auth.json --set-password-stdin
unset UPS_BRIDGE_PASS

./apc-ups-bridge \
  --auth-file ./data/auth.json \
  --settings-file ./data/settings.json \
  --power-config ./data/power.json \
  --power-state ./data/power-state.json \
  --log-dir ./data/events \
  --listen 127.0.0.1:8080
```

Open `http://127.0.0.1:8080/` on the host and sign in as `admin`. To check a UPS without starting the web listener:

```bash
./apc-ups-bridge --once --settings-file ./data/settings.json --log-dir ./data/events
```

### Access from your private LAN

Explicitly set `--listen 0.0.0.0:8080` and access `http://<host-ip>:8080` from another device. **Plain HTTP does not encrypt passwords or sessions.** Do not forward the port to the public internet. Use a trusted VPN or an HTTPS reverse proxy for remote administration, and limit network access at the firewall. Changing the listening port currently requires restarting the program.

`--set-password-stdin` resets an administrator password and overwrites its local password hash; restart a running web process after a CLI reset. Saving preferences from the authenticated settings page updates the settings file immediately. Password hashes, settings files, and event logs should be accessible only to the service account.

For remote power configuration and safety limitations see [Phase 4 safety and tests](docs/PHASE-4.md). For background see [Phase 3 usage and tests](docs/PHASE-3.md), [Phase 2 internals](docs/PHASE-2.md) and [Architecture](docs/ARCHITECTURE.md).

## Roadmap

1. ~~Read-only UPS polling, fault detection and event-only logging.~~
2. ~~Authenticated single-port dashboard, events, reports and basic editable settings.~~
3. ~~Implement opt-in remote SSH shutdown and guarded Wake-on-LAN recovery with durable safety state.~~
4. Add local UPS-host protection coordination, versioned GitHub releases, checksums, an idempotent installer, automatic startup and real-hardware compatibility validation.

APC is a trademark of its respective owner. This independent project is not affiliated with or endorsed by APC or Schneider Electric.
