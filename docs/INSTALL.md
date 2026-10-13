# Installation, updates and removal

APC UPS Bridge runs as one unprivileged process on a **systemd-based Linux host** with an **APC USB UPS supported by apcupsd**. It needs no database, Docker container or cloud account. The installer builds the program from source on the host.

Test the installation, USB communication and any remote power actions on your own hardware before relying on them.

## Linux distribution support

The application runs on **systemd-based Linux**, not just single-board computers. The table reflects compatibility expected from the current source and installer; **it does not mean every combination has been installed and tested on real hardware**.

| Linux distribution | Main application | Automatic Fail2ban |
| --- | --- | --- |
| Ubuntu 22.04 / 24.04 / 26.04 LTS | ✅ | ✅ |
| Debian 12 / 13 | ✅ | ✅ |
| Raspberry Pi OS | ✅ | ✅ |
| Linux Mint | ✅ | ✅ |
| Fedora Server / Workstation | ✅ Expected, **not tested** | ❌ Manual — **MUST skip in installer** |
| openSUSE Tumbleweed | ✅ Expected, **not tested** | ❌ Manual — **MUST skip in installer** |

**Fedora and openSUSE users MUST pass `--skip-fail2ban` on every installation or update.** The installer supports `dnf` and `zypper` for `apcupsd` but the automatic Fail2ban configuration is Debian/Ubuntu-specific. The built-in login limit works regardless of Fail2ban.

From a checkout:

```bash
sudo bash scripts/install.sh --skip-fail2ban
```

Or when installing or updating using the downloaded script:

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/install.sh | sudo bash -s -- --skip-fail2ban
```

These expected-compatible distributions still need real installer, USB, systemd and shutdown-permission testing before being described as verified. Automatic Fail2ban setup is unchanged for Debian, Ubuntu and their listed derivatives.

## Install

One command:

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/install.sh | sudo bash
```

Or clone the repository and run the installer from the checkout:

```bash
git clone https://github.com/fructus-sum/APC-UPS-Bridge.git && cd APC-UPS-Bridge && sudo bash scripts/install.sh
```

To read the script first, download `scripts/install.sh`, review it and run `sudo bash install.sh`. Run from a file outside a checkout, it fetches the source itself, as the piped version does.

### What it does

1. Asks its questions up front: web port (default **8080**) and a new administrator password (twice, at least 12 characters). Questions are read from the terminal (`/dev/tty`), so the piped command can still prompt.
2. Gets the source: the checkout it was run from, or a download of the `main` branch (`--branch NAME` for another).
3. Finds a Go 1.22+ toolchain. If there is none, it downloads the current official Go for your CPU from go.dev / dl.google.com and verifies its SHA-256. The toolchain is temporary.
4. Builds the bridge and the host helper (see step 7) with `CGO_ENABLED=0` in a scratch directory under `/var/tmp`. Build time depends on CPU and storage speed, and a download plus build may need a few hundred MB of free disk. The scratch directory, including any downloaded Go, is deleted when the installer exits.
5. Installs `apcupsd` with apt, dnf or zypper if `apcaccess` is missing. On that fresh install it also configures apcupsd for a USB UPS (see [apcupsd setup](#apcupsd-setup)) and checks that `apcaccess status` answers.
6. Creates the `apc-ups-bridge` service user, installs the binary to `/usr/local/bin/apc-ups-bridge`, writes the hardened systemd unit, enables it at boot and starts it.
7. Installs the host helper `/usr/local/libexec/apc-ups-bridge-host-helper` and enables its socket unit `apc-ups-bridge-host-settings.socket`. The helper lets the Settings page change the local Linux host's shutdown thresholds (see [Local host self-protection](#local-linux-host-self-protection)). If the helper cannot be installed, the bridge still runs and the installer warns; rerun the installer to retry.
8. Debian/Ubuntu: sets up a Fail2ban jail for repeated failed web logins (skip with `--skip-fail2ban`).
9. Installs the uninstaller as `/usr/local/sbin/apc-ups-bridge-uninstall`, then prints the URL(s), for example `Open http://192.168.1.20:8080/`.

Supported CPUs are x86-64, 64-bit ARM and 32-bit ARM (including 32-bit userspace on a 64-bit ARM kernel). Other architectures stop with an error before building. The Go download needs a working internet connection; with a recent Go already installed, only the source download is needed (none at all when run from a checkout).

### Fail2ban

On Debian/Ubuntu-based distributions, installing or updating configures a Fail2ban jail specific to APC UPS Bridge. Other Fail2ban jails are preserved. If the setup fails, the installer warns and undoes its own changes so other jails keep working; check and retry as described in [Login security](LOGIN-SECURITY.md), which also covers the ban policy and the **Login security** page. Skipping with `--skip-fail2ban` does not remove a jail that is already set up. On Fedora and openSUSE, **use `--skip-fail2ban` for installations and updates**, and set up Fail2ban manually if wanted. On other distributions without automatic Fail2ban support, the built-in login limit still applies.

### Network access

The web interface is served on **all interfaces** (`0.0.0.0:<port>`) so you can open it from any device on your network. It is **plain HTTP**: the administrator password and session cookie cross your network unencrypted. Keep it on a trusted home network, **never port-forward it to the internet**, and use a VPN or a local HTTPS reverse proxy for anything remote. The installer does not change firewall rules.

- Pick another port at the prompt (1024-65535; the service is unprivileged and cannot bind lower ports), or pass `--listen 192.168.1.20:9090` to bind one address. `--listen 127.0.0.1:8080` keeps it host-only (then put a reverse proxy in front).
- Updates keep the existing `/etc/apc-ups-bridge/service.env` binding unless you give `--listen`. An install bound to `127.0.0.1` stays that way until you rerun with `--listen 0.0.0.0:8080`.
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
--skip-fail2ban         do not set up Fail2ban protection against repeated failed web logins
```

Options after a pipe go through `bash -s --`, for example `curl -fsSL .../install.sh | sudo bash -s -- --branch develop`.

For unattended first-time installation, save the script (or use a checkout) and pipe the password in. Do not pass it as an argument or environment variable. `--password-stdin` cannot be combined with `curl | bash`, because stdin is the script:

```bash
printf '%s\n' "$PASSWORD_FROM_YOUR_SECRET_MANAGER" | sudo bash scripts/install.sh --non-interactive --password-stdin
```

The password is only requested on the first install. On later runs `--password-stdin` is ignored if a password hash already exists.

## apcupsd setup

- **Fresh install** (the installer had to install `apcupsd`, and this is a first Bridge install: no `/etc/apc-ups-bridge/service.env`, no installed binary): it backs up `apcupsd.conf` and sets `UPSCABLE usb`, `UPSTYPE usb`, an empty `DEVICE`, `NETSERVER on` and `NISIP 127.0.0.1`, sets `ISCONFIGURED=yes` in `/etc/default/apcupsd` where present, enables and restarts the service, then tries `apcaccess status` up to three times. If no `STATUS` answer appears (UPS unplugged, wrong model, `COMMLOST`) it prints a warning and carries on. It never fails the install.
- **Existing apcupsd, or an update**: left completely alone.
- `--skip-apcupsd-setup` suppresses the automatic step. `--configure-apcupsd` forces it even for a pre-installed apcupsd (do not use it if the host manages other UPS units). The two flags cannot be combined.

Backups are written next to the originals as `.backup-YYYYMMDDTHHMMSSZ-N`. `BATTERYLEVEL`, `MINUTES` and `TIMEOUT` are retained unless `--host-protection` is given.

## Local Linux host self-protection

APC UPS Bridge does not run a second local shutdown controller. The local Linux host's shutdown is handled by native `apcupsd`, so UPS power-off timing and events are handled in one place. An existing apcupsd shutdown configuration is preserved by default, and a default or earlier `apcupsd` setup may already have local shutdown rules active.

To have the installer set the **local host** thresholds at **10% battery OR 3 minutes estimated runtime**, use:

```bash
sudo bash scripts/install.sh --configure-apcupsd --host-protection
```

This backs up the config, sets `BATTERYLEVEL 10`, `MINUTES 3` and `TIMEOUT 0`, then restarts apcupsd. These are active shutdown thresholds and could stop the local host during an outage. Choose values suitable for your UPS and leave enough time for configured remote SSH shutdowns to finish. Inspect `/etc/apcupsd/apcupsd.conf` before relying on it.

To change the thresholds afterwards, use the **Settings** page (battery percentage, remaining runtime and on-battery timer). Changes go through the host helper, are refused unless the UPS reports ONLINE mains, and back up `apcupsd.conf` before restarting `apcupsd`. A failed change leaves the settings unchanged (or is rolled back after a restart failure). Choosing the existing-configuration mode stops web management but does not restore earlier values.

## Service management

```bash
systemctl status apc-ups-bridge.service
journalctl -u apc-ups-bridge.service -f
systemctl status apcupsd.service
apcaccess status
```

The main service runs as the `apc-ups-bridge` account and is enabled at boot. It uses systemd sandboxing, and its write access is limited to `/etc/apc-ups-bridge`, `/var/lib/apc-ups-bridge` and `/var/log/apc-ups-bridge`. The host helper is a separate root service, started on demand through its socket; the socket is reachable only by the `apc-ups-bridge` account, and the helper can write only under `/etc/apcupsd`. To change the bind address later, rerun the installer with `--listen`.

Locations on disk are listed in [Architecture](ARCHITECTURE.md#files-on-disk).

### SSH for approved remote shutdowns

Create a limited-purpose SSH identity for the `apc-ups-bridge` service user in `/var/lib/apc-ups-bridge/.ssh`, register the *public* key with each approved target and verify each target's host key into the service user's `known_hosts`. Step-by-step instructions are in the [SSH key setup guide](SSH-KEYS.md); [ready-made scripts](../tools/README.md) can set up a Debian target. The service uses `StrictHostKeyChecking=yes`, batch mode and fixed shutdown commands, so it cannot prompt for passwords.

### Power rules

The **Power rules & devices** page holds the approved remote hosts. The switch is off until you enable it. Each device has a **Use sudo -n** option for accounts that need sudo to shut down, a **Test SSH (no shutdown)** button, and an **Online check** setting (Ping, SSH port or Off) that only affects the status shown on the dashboard. Test SSH requests no remote session or command and does not prove shutdown permission. Test a real shutdown on noncritical equipment before enabling automatic protection. Behavior, retries and recovery are described in [Power protection](POWER-PROTECTION.md).

## Updates and rollback

Run the install command again (the one-liner, or `git pull && sudo bash scripts/install.sh` in a checkout). It rebuilds from source and swaps the binary. An update keeps:

- the administrator login and settings
- email settings
- the SSH identity
- device rules and power state
- event logs and history samples
- the listen address
- the Fail2ban jail

Existing apcupsd configuration is not touched unless `--configure-apcupsd` is given. After restarting, the installer watches the service for about five seconds. If it fails to start or dies (for example because the port is already used), the installer restores the previous binary, unit and `service.env` (or removes the new ones on a first install) and restarts the old service. This is not a substitute for a full backup.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/uninstall.sh | sudo bash
# Only when permanently discarding credentials, settings and all event history:
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/uninstall.sh | sudo bash -s -- --purge
```

The installer also leaves a local copy: `sudo apc-ups-bridge-uninstall` or `sudo apc-ups-bridge-uninstall --purge`. From a checkout, `sudo bash scripts/uninstall.sh` works too. It is safe to run when parts are already gone. It removes the service, host helper and its socket, unit files, binary and the uninstaller itself.

What remains:

- `apcupsd`, its configuration, its `.backup-*` copies and any local host shutdown thresholds set through the installer or the Settings page.
- The Fail2ban jail and the `/var/log/apc-ups-bridge-fail2ban` log.
- Without `--purge`, the credentials, settings, state and logs. `--purge` deletes `/etc/apc-ups-bridge`, `/var/lib/apc-ups-bridge`, `/var/log/apc-ups-bridge` and the service user.

## Building from source

Building and testing the program by hand is covered in [Architecture, Development](ARCHITECTURE.md#development).
