# Installation, upgrades and recovery

> **Hardware safety:** The software has automated tests, but installation, USB UPS communication and remote power actions must be tested on your own hardware before relying on them.

APC UPS Bridge runs as one unprivileged Go process on a **systemd-based Linux host** with an **APC USB UPS supported by apcupsd**. The runtime needs no Go toolchain, web framework, database, Docker container, or cloud account. There are no GitHub releases: the installer builds the binary from source on the box.

## Install

One command (works once the repository is public):

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/install.sh | sudo bash
```

While the repository is private, clone it (you need a GitHub login) and run the installer from the checkout:

```bash
git clone https://github.com/fructus-sum/APC-UPS-Bridge.git && cd APC-UPS-Bridge && sudo bash scripts/install.sh
```

Prefer to read it first? Download `scripts/install.sh`, skim it, and run `sudo bash install.sh`. Run from a file outside a checkout it fetches the source itself, same as the piped version.

### What it does

1. Asks the questions up front, before anything slow or invasive: web port (default **8080**) and a new administrator password (twice, at least 12 characters). Questions are read from the terminal (`/dev/tty`), which is why the piped one-liner can still prompt.
2. Gets the source: the checkout it was run from, or a download of the `main` branch (`--branch NAME` for another).
3. Finds a Go 1.22+ toolchain. If there is none, it downloads the current official Go for your CPU from go.dev / dl.google.com, verifying its SHA-256. The toolchain is temporary.
4. Builds the binary (`CGO_ENABLED=0`, no third-party modules) in a scratch directory under `/var/tmp`. **Build time depends on CPU and storage speed**, and a download plus build may need a few hundred MB of free disk. Everything in the scratch directory, including any downloaded Go, is deleted when the installer exits. Go is never left installed.
5. Installs `apcupsd` with apt, dnf or zypper if `apcaccess` is missing. On that fresh install it also configures apcupsd for a USB UPS (details below) and checks that `apcaccess status` answers.
6. Creates the `apc-ups-bridge` service user, installs the binary to `/usr/local/bin`, writes the hardened systemd unit, enables it at boot and starts it.
7. Installs the uninstaller as `/usr/local/sbin/apc-ups-bridge-uninstall`, then prints the URL(s), e.g. `Open http://192.168.1.20:8080/`.

Supported CPUs are x86-64, 64-bit ARM, and 32-bit ARM (a 32-bit Raspberry Pi OS is detected even on a 64-bit kernel). Other architectures stop with an error before building. The Go toolchain download needs a working internet connection; with a recent Go already installed, only the source download is needed (none at all when run from a checkout).

### Network access

The web interface is served on **all interfaces** (`0.0.0.0:<port>`) so you can open it from any device on your LAN. It is **plain HTTP**: the administrator password and session cookie cross your network unencrypted. Keep it on a trusted home network, **never port-forward it to the internet**, and use a VPN or a local HTTPS reverse proxy for anything remote. The installer does not change firewall rules.

- Pick another port at the prompt (1024–65535; the service runs unprivileged and cannot bind ports below 1024), or pass `--listen 192.168.1.20:9090` to bind one address. `--listen 127.0.0.1:8080` keeps it host-only (then put a reverse proxy in front).
- Upgrades keep the existing `/etc/apc-ups-bridge/service.env` binding unless you give `--listen`. An install made by an earlier version that is bound to `127.0.0.1` stays that way until you re-run with `--listen 0.0.0.0:8080`.
- The URL hints come from `hostname -I`; if it is unavailable the installer prints `http://<this-host-ip>:8080/`.

### Options

```text
--branch NAME           branch to build when not run from a checkout (default main)
--listen HOST:PORT      web binding (default 0.0.0.0:8080; port 1024-65535)
--configure-apcupsd     back up and configure an existing apcupsd for a local USB UPS
--skip-apcupsd-setup    do not auto-configure an apcupsd the installer just installed
--host-protection       with --configure-apcupsd: native host shutdown at 10% battery or 3 minutes
--password-stdin        read the initial admin password from stdin (one line)
--non-interactive       never prompt; first install then requires --password-stdin
--skip-start            install and enable, but do not start now
```

Options after a pipe go through `bash -s --`, for example `curl -fsSL .../install.sh | sudo bash -s -- --branch develop`.

For unattended first-time installation, save the script (or use a checkout) and pipe the password in. Do not pass it as an argument or environment variable. `--password-stdin` cannot be combined with `curl | bash`, because stdin is the script:

```bash
printf '%s\n' "$PASSWORD_FROM_YOUR_SECRET_MANAGER" | sudo bash scripts/install.sh --non-interactive --password-stdin
```

The password is only requested on the first install. On later runs `--password-stdin` is ignored if a password hash already exists.

## apcupsd setup

- **Fresh install** (the installer had to install `apcupsd`, and this is a first Bridge install: no `/etc/apc-ups-bridge/service.env`, no installed binary): it backs up `apcupsd.conf` and sets `UPSCABLE usb`, `UPSTYPE usb`, an empty `DEVICE`, `NETSERVER on` and `NISIP 127.0.0.1`, sets `ISCONFIGURED=yes` in `/etc/default/apcupsd` where present, enables and restarts the service, then tries `apcaccess status` up to three times. If no `STATUS` answer appears (UPS unplugged, wrong model, `COMMLOST`) it prints a warning and carries on. It never fails the install.
- **Existing apcupsd, or an upgrade**: left completely alone.
- `--skip-apcupsd-setup` suppresses the automatic step. `--configure-apcupsd` forces it even for a pre-installed apcupsd (do not use it if the host manages other UPS units). The two flags cannot be combined.

Backups are written next to the originals as `.backup-YYYYMMDDTHHMMSSZ-N`. `BATTERYLEVEL`, `MINUTES` and `TIMEOUT` are retained unless `--host-protection` is given.

## Linux host / Raspberry Pi self-protection

APC UPS Bridge **does not run a second local shutdown controller**. Use native `apcupsd` host shutdown so that UPS power-off timing and events are handled in one place. Existing apcupsd host shutdown configuration is preserved by default.

To opt into the installer setting the **local host** thresholds at **10% battery OR 3 minutes estimated runtime**, use:

```bash
sudo bash scripts/install.sh --configure-apcupsd --host-protection
```

This backs up the config and sets `BATTERYLEVEL 10`, `MINUTES 3`, and `TIMEOUT 0`, then restarts apcupsd. **These are active host shutdown thresholds** and could stop the Pi during an outage. Choose thresholds suitable for the UPS and allow enough time for configured remote SSH shutdowns to finish. Inspect `/etc/apcupsd/apcupsd.conf` before relying on it. A default or previously configured `apcupsd` instance may already have local shutdown rules active even if this flag is never used. Do not assume the host has no shutdown policy.

## Service management

```bash
systemctl status apc-ups-bridge.service
journalctl -u apc-ups-bridge.service -f
systemctl status apcupsd.service
apcaccess status
```

The systemd service runs as a dedicated `apc-ups-bridge` account and is enabled at boot. The binary is at `/usr/local/bin/apc-ups-bridge`; system configuration at `/etc/apc-ups-bridge`; password and power state at `/var/lib/apc-ups-bridge`; and event-only JSONL logs at `/var/log/apc-ups-bridge`. Regular readings remain in RAM. The installer also builds a small, separately sandboxed host-settings helper and enables `apc-ups-bridge-host-settings.socket`, accessible only to the unprivileged Bridge service user. This allows the authenticated **Settings** page to change local `apcupsd` shutdown thresholds (battery percentage, remaining runtime and an on-battery timer) without granting the web server root privileges. Changes are refused unless the helper verifies the UPS as ONLINE; each change backs up `apcupsd.conf` before updating it and restarts `apcupsd`. Selecting existing-configuration mode stops web management but does not restore previous threshold values. A failed helper or communication check leaves settings unchanged (or attempts rollback after a restart failure).

The unit uses systemd sandboxing and write access only to these three directories. To change the bind address later, run the installer again with `--listen`.

### SSH for approved remote shutdowns

Create a limited-purpose SSH identity **for the `apc-ups-bridge` service user** in `/var/lib/apc-ups-bridge/.ssh`, register the *public* key with each approved target and verify each target's host key into the service user's `known_hosts`. The service uses `StrictHostKeyChecking=yes`, batch mode and fixed shutdown commands; it cannot prompt for passwords. The dedicated service user may access the network but **is not root**, and systemd sandboxing does not provide it local shutdown privileges. The Power Rules page shows the local Pi's configured apcupsd BATTERYLEVEL/MINUTES/TIMEOUT separately from remote power rules, and includes a **Test SSH (no shutdown)** action for each saved remote host. Testing SSH requests no remote session or command, does not establish sudo/shutdown permission, and cannot guarantee eventual shutdown success. The Power Rules switch is off until explicitly enabled in the web interface. Test shutdown on noncritical equipment before enabling automatic protection. See [Power protection](POWER-PROTECTION.md) for shutdown retries, confirmation, and recovery behavior.

## Updates and rollback

Run the install command again (the one-liner, or `git pull && sudo bash scripts/install.sh` in a checkout). It rebuilds from source and swaps the binary. The upgrade keeps the service login, SSH identity, remote device rules, event logs, power state and the existing listen address. Existing apcupsd configuration is *not touched* unless `--configure-apcupsd` is supplied. After restarting, the installer watches the service for about five seconds. If it fails to start or dies (for example because the port is already used by another service), the installer restores the previous binary, unit and `service.env` (or removes the new ones on a first install) and restarts the old service. As with any automated process, this is not a substitute for a full backup or UPS dry-run.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/uninstall.sh | sudo bash
# Only when permanently discarding credentials, settings and all event history:
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/uninstall.sh | sudo bash -s -- --purge
```

The installer also leaves a local copy: `sudo apc-ups-bridge-uninstall` or `sudo apc-ups-bridge-uninstall --purge`. From a checkout, `sudo bash scripts/uninstall.sh` works too. It is safe to run when parts are already gone, and it removes the service, unit, binary and the uninstaller itself.

Uninstall does not remove `apcupsd`, its configuration, its `.backup-*` copies, or its UPS self-protection settings. With no `--purge`, sensitive state and logs remain available for reinstallation. `--purge` also deletes `/etc/apc-ups-bridge`, `/var/lib/apc-ups-bridge`, `/var/log/apc-ups-bridge` and the service user.

## Developer build

```bash
go test ./...
go test -race ./...
go vet ./...
bash scripts/test-install.sh
go build -o apc-ups-bridge ./cmd/apc-ups-bridge
```

GitHub Actions runs Go, race, vet and installer tests on pull requests. The installer test suite is unprivileged and changes nothing on the host. A successful CI build is not a substitute for verifying a USB UPS, systemd permissions and actual shutdown/wake behavior.
