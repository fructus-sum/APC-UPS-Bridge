# APC UPS Bridge

**Web monitoring and optional power protection for a USB-connected APC UPS.**

APC UPS Bridge runs on a Raspberry Pi or other Linux host and reads a single APC UPS through `apcupsd`/`apcaccess`. It provides an authenticated web dashboard, event history, optional remote Linux shutdown/Wake-on-LAN, and SMTP outage notifications. The application is one Go executable with an embedded web interface: no database, external web framework, cloud account, or third-party Go modules.

> **Safety:** Power automation is disabled until explicitly enabled. The software has automated tests, but UPS models, remote shutdown, Wake-on-LAN, and installation still need validation on the equipment you will protect. Do not make this your sole safeguard without that testing.

## What it does

- **Dashboard:** live mains/battery state, charge, remaining runtime, load, voltages, UPS identification and communication health, when reported by the device.
- **Events and reports:** event-only monthly JSONL logs, filtered history, observed outage durations, CSV export and configurable retention. Routine poll samples are not stored.
- **Power rules and devices:** up to 16 approved remote Linux hosts; configurable SSH shutdown, order/priority, battery/runtime thresholds, and optional Wake-on-LAN recovery. Actions are persisted across bridge restarts and power cycles.
- **Email notifications:** configure a TLS/STARTTLS SMTP account; one-shot outage/restoration alerts and a durable final report after mains returns, the UPS reports 100% charge, and optionally monitored devices are SSH-reachable. Reports persist across bridge restarts.
- **Firmware advisory:** read-only warning on the dashboard for recognized model/UPS-ID combinations whose installed firmware is below a dated local Schneider reference. No firmware is flashed.
- **Administration:** single administrator account, session-based login, password change, configurable polling (5–60 seconds), battery warning and log retention.

See [power protection](docs/POWER-PROTECTION.md), [email delivery](docs/EMAIL-NOTIFICATIONS.md) and [firmware guidance](docs/FIRMWARE.md) for important behavior and limitations.

## Install on Linux / Raspberry Pi

**Requirements:** systemd-based Linux, supported APC USB UPS, network access to fetch the source and (if needed) Go, and standard Linux utilities. The installer supports apt, dnf and zypper for installing `apcupsd`; other systems may need that package installed manually. **The installer builds from source** and does not require a published release.

This repository is currently private. Clone it with your authorized GitHub credentials and install from the checkout:

```bash
git clone https://github.com/fructus-sum/APC-UPS-Bridge.git
cd APC-UPS-Bridge
sudo bash scripts/install.sh
```

After the repository is public, the installer also supports:

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/install.sh | sudo bash
```

The installer asks for the web port (default **8080**) and an administrator password on an interactive first installation. By default the **installed service listens on all LAN interfaces** (`0.0.0.0:8080`); standalone runs of the binary default to **localhost only** (`127.0.0.1:8080`). The installer prints a suggested browser URL after installing.

It uses Go 1.22+ if available, otherwise downloads and verifies a temporary Go toolchain, builds the executable, and removes temporary build files. It installs `apcupsd` if missing; **only a newly installed apcupsd on a new Bridge host** is auto-configured for USB. An existing apcupsd configuration is preserved unless you explicitly pass `--configure-apcupsd`. The bridge runs under a dedicated `apc-ups-bridge` no-login system account and starts through systemd.

**Security:** the web service uses **plain HTTP**, including administrator credentials. Use only on a trusted LAN; never port-forward it publicly. Prefer a trusted VPN or HTTPS reverse proxy for access beyond that network, or install with `--listen 127.0.0.1:8080` behind a local proxy.

Read the [installation and recovery guide](docs/INSTALL.md) for setup options, SSH prerequisites, upgrades, service commands and removal.

## Update, troubleshoot or uninstall

To upgrade a checkout installation, update your local checkout then rerun the installer:

```bash
git pull
sudo bash scripts/install.sh
```

The installer retains existing credentials, settings, email and power-action state, event logs, and listening address. Check the running service and UPS communications with:

```bash
systemctl status apc-ups-bridge.service
journalctl -u apc-ups-bridge.service -f
apcaccess status
```

To remove the application while **keeping its data**:

```bash
sudo apc-ups-bridge-uninstall
```

Add `--purge` to also delete the Bridge configuration, credentials, durable state, logs and dedicated account. Uninstalling the Bridge does **not** uninstall or reconfigure `apcupsd`.

## Developer build

Go 1.22+ and working `apcaccess status` are required for an actual hardware reading. Build and run locally:

```bash
go test ./...
go test -race ./...
go vet ./...
bash scripts/test-install.sh
go build -o apc-ups-bridge ./cmd/apc-ups-bridge

mkdir -p ./data
read -rs -p 'Administrator password: ' UPS_BRIDGE_PASS; echo
printf '%s\n' "$UPS_BRIDGE_PASS" | ./apc-ups-bridge --auth-file ./data/auth.json --set-password-stdin
unset UPS_BRIDGE_PASS

./apc-ups-bridge \
  --auth-file ./data/auth.json \
  --settings-file ./data/settings.json \
  --power-config ./data/power.json \
  --power-state ./data/power-state.json \
  --email-config ./data/email.json \
  --email-state ./data/email-state.json \
  --log-dir ./data/events \
  --listen 127.0.0.1:8080
```

Sign in at `http://127.0.0.1:8080/` as `admin`. To read the UPS once without starting a web service:

```bash
./apc-ups-bridge --once --settings-file ./data/settings.json --log-dir ./data/events
```

For the actual component layout and file locations, see [architecture](docs/ARCHITECTURE.md).

APC is a trademark of its respective owner. This independent project is not affiliated with or endorsed by APC or Schneider Electric.
