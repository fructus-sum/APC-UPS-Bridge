# APC UPS Bridge

**Give your older USB-connected APC UPS a new life on the network.**

APC UPS Bridge turns a Raspberry Pi or Linux host into a lightweight, self-hosted gateway for USB APC UPS units supported by `apcupsd`. It provides a password-protected dashboard, live readings, event-only logs and reports, editable monitoring preferences, and **opt-in** remote power protection. It uses **one Go executable, no database, no cloud service, and no third-party Go modules**.

> **Development preview (Phase 5), not a production release.** Remote SSH shutdown and Wake-on-LAN are implemented behind an explicit disabled-by-default safety switch, but have **not been tested on real UPS hardware**. An installer and systemd unit are implemented but have not been tested across target Linux devices. Local host shutdown is handled by apcupsd rather than a competing application shutdown loop. Do not rely on this preview to protect equipment.

## Features available through Phase 5

- Live mains/battery state, charge, runtime, load, voltage, UPS model and communication health when reported by the UPS.
- Read-only firmware advisory in the dashboard for recognized devices (manual vendor update only; never flashes USB firmware). See [firmware guidance](docs/FIRMWARE.md).
- Password-protected dashboard, events/reports and settings; administrator login and password rotation.
- Security controls: salted PBKDF2-SHA256 password hashing, ephemeral authenticated sessions, login attempt limits, and CSRF tokens on updates.
- Event-only monthly JSONL logs, adjustable retention including unlimited; CSV download for recent events.
- History graphs: input/output voltage and UPS load sampled every 15 minutes, kept for 30 days (see [docs/HISTORY.md](docs/HISTORY.md)).
- Editable polling interval (5–60 s), low battery **warning** level (0–100%), and event retention (0–36500 days). Changes are applied without restarting.
- All web routes share one configurable HTTP port. The installer serves it on your LAN (`0.0.0.0:8080`) over plain HTTP; the program on its own defaults to `127.0.0.1:8080`.
- Add multiple explicitly approved remote Linux hosts with SSH identity, optional `sudo -n`, shutdown order/priority, MAC and Wake-on-LAN destination.
- Remote power actions are globally **disabled by default**, and each host must separately allow shutdown and optional wake.
- Shutdown requires a previously healthy ONLINE observation, ON_BATTERY confirmation delay and a configured battery, UPS LOWBATT, or runtime trigger.
- Recovery requires stable, healthy ONLINE power and adequate battery charge; devices only wake if their shutdown SSH command reported success.
- Durable, tiny JSON power-action state prevents repeated remote actions after restarts. No periodic normal-status log writes.

## Install

One command on a Raspberry Pi / Debian-style Linux box with systemd (works once the repository is public):

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/install.sh | sudo bash
```

While the repository is private, clone it instead (needs a GitHub login) and run the installer from the checkout:

```bash
git clone https://github.com/fructus-sum/APC-UPS-Bridge.git && cd APC-UPS-Bridge && sudo bash scripts/install.sh
```

What it does: installs `apcupsd` if it is missing (and sets it up for a USB UPS on that fresh install), downloads a temporary Go toolchain if the box has no Go 1.22+, builds the binary from source (a small Pi can take several minutes), creates a locked-down `apc-ups-bridge` service user, enables the systemd service, and serves the web interface on your LAN at port 8080. It asks for the port (Enter keeps 8080) and an admin password up front, then prints the URL, for example `Open http://192.168.1.20:8080/`. The temporary Go toolchain and build files are deleted afterwards.

Update = run the same command again; your password, settings, devices and logs are kept. Uninstall:

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/uninstall.sh | sudo bash
# also discard credentials, settings and event history:
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/uninstall.sh | sudo bash -s -- --purge
```

or `sudo apc-ups-bridge-uninstall [--purge]`, which the installer puts on the box. Your `apcupsd` install and config are never touched. Full details, options and recovery notes: [docs/INSTALL.md](docs/INSTALL.md).

**Network note:** the dashboard is plain HTTP meant for your home LAN. Never port-forward it to the internet; use a VPN or an HTTPS reverse proxy for anything remote. Remote shutdown is opt-in and off by default, and it has **not** been proven on every UPS or target OS: test it on your own hardware (with equipment you can afford to power-cycle) before relying on it.

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

### Access from your private LAN (manual runs)

The installer already does this for you. When running the binary by hand, set `--listen 0.0.0.0:8080` and access `http://<host-ip>:8080` from another device. **Plain HTTP does not encrypt passwords or sessions.** Do not forward the port to the public internet. Use a trusted VPN or an HTTPS reverse proxy for remote administration, and limit network access at the firewall. Changing the listening port currently requires restarting the program.

`--set-password-stdin` resets an administrator password and overwrites its local password hash; restart a running web process after a CLI reset. Saving preferences from the authenticated settings page updates the settings file immediately. Password hashes, settings files, and event logs should be accessible only to the service account.

For remote power configuration and safety limitations see [Phase 4 safety and tests](docs/PHASE-4.md). For background see [Phase 3 usage and tests](docs/PHASE-3.md), [Phase 2 internals](docs/PHASE-2.md) and [Architecture](docs/ARCHITECTURE.md).

## Roadmap

1. ~~Read-only UPS polling, fault detection and event-only logging.~~
2. ~~Authenticated single-port dashboard, events, reports and basic editable settings.~~
3. ~~Implement opt-in remote SSH shutdown and guarded Wake-on-LAN recovery with durable safety state.~~
4. ~~Implement apcupsd-based optional local-host protection setup, an idempotent build-from-source installer and automatic startup.~~ Hardware/OS compatibility testing is still required.

APC is a trademark of its respective owner. This independent project is not affiliated with or endorsed by APC or Schneider Electric.


## Voltage and UPS load history

The password-protected **History graphs** tab records one input/output voltage and UPS-load percentage sample per 15 minutes for up to 30 days, with a default 14-day view. This is a separate bounded sample log, not continuous event-log recording. Power events annotate their actual timestamps; gaps while the host is off or the UPS unavailable remain blank rather than inferred. Existing event-only logs cannot reconstruct voltage or load samples recorded before this feature. See [graphing details](docs/HISTORY.md).
