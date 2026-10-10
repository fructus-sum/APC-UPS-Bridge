# Installation, upgrades and recovery (Phase 5)

> **Pre-release warning:** No ready-to-install GitHub release exists until a version tag has been published and the release workflow succeeds. This is not yet qualified for live power protection. Test using real hardware before enabling remote actions or native host shutdown.

APC UPS Bridge runs as one unprivileged Go process on a **systemd-based Linux host** with an **APC USB UPS supported by apcupsd**. Runtime needs no Go toolchain, web framework, database, Docker container, or cloud account.

## Supported architectures and requirements

Pre-built release archives: linux/amd64 (PC), linux/arm64 (64-bit Pi), linux/armv7 (32-bit Pi 2/3/4 etc.), linux/armv6 (Pi 1/Zero). Kernel, OS and USB compatibility must still be verified on the chosen hardware. The installer checks `uname -m`, requires `systemd` and basic tools (`curl`, `sha256sum`, `tar`, `awk`, `runuser`, etc.) and uses the system package manager to install `apcupsd` **only if `apcaccess` is absent**. Automatic `apcupsd` package installation is supported for apt, dnf and zypper systems; otherwise install `apcupsd` manually before running it. The installer leaves other existing USB/UPS services alone.

## New installation

Download and inspect the installer before running it with administrative privileges:

```bash
curl -fL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/install.sh -o install.sh
less install.sh
sudo bash install.sh --version v0.1.0
```

Replace `v0.1.0` with an **actually published GitHub release**; omit `--version` for the latest published release. Enter and confirm a strong administrator password when prompted. The initial web interface listens on **127.0.0.1:8080**, so it is not reachable from other LAN computers by default.

On a clean Pi with an **APC USB UPS** and no configured apcupsd instance, the explicit setup option backs up and configures the UPS daemon for USB/NIS localhost access:

```bash
sudo bash install.sh --configure-apcupsd
```

Do not use `--configure-apcupsd` if the host is managing additional UPS units or has an apcupsd configuration that must remain untouched. It changes the apcupsd USB mode, NIS listener to `127.0.0.1:3551`, and on Debian-like systems enables `ISCONFIGURED=yes`. The existing `BATTERYLEVEL`, `MINUTES` and `TIMEOUT` settings are retained unless `--host-protection` is explicitly requested. Prior config copies use `.backup-YYYYMMDDTHHMMSSZ` suffixes.

To deliberately expose the interface on the private LAN:

```bash
sudo bash install.sh --listen 0.0.0.0:8080
```

**Security:** The current web server uses unencrypted HTTP. Do not port-forward it or expose it to untrusted networks. Use a VPN or a TLS reverse proxy to protect administrator credentials. For a remote HTTPS proxy, prefer localhost listening and proxy locally. The installer does not change firewall rules or open other network-facing ports.

For unattended first-time installation, pipe the initial password over standard input using a secret-handling mechanism (not a command argument or environment variable):

```bash
printf '%s\n' "$PASSWORD_FROM_YOUR_SECRET_MANAGER" | sudo bash install.sh --non-interactive --password-stdin
```

On subsequent upgrades, the password is not requested or reset, and `--password-stdin` is ignored if a password hash already exists.

## Linux host / Raspberry Pi self-protection

APC UPS Bridge **does not run a second local shutdown controller**. Use native `apcupsd` host shutdown so that UPS power-off timing and events are handled in one place. Existing apcupsd host shutdown configuration is preserved by default.

To opt into the installer setting the **local host** thresholds at **10% battery OR 3 minutes estimated runtime**, use:

```bash
sudo bash install.sh --configure-apcupsd --host-protection
```

This backs up the config and sets `BATTERYLEVEL 10`, `MINUTES 3`, and `TIMEOUT 0`, then restarts apcupsd. **These are active host shutdown thresholds** and could stop the Pi during an outage. Choose thresholds suitable for the UPS and allow enough time for configured remote SSH shutdowns to finish. Inspect `/etc/apcupsd/apcupsd.conf` before relying on it. A default or previously configured `apcupsd` instance may already have local shutdown rules active even if this flag is never used. Do not assume the host has no shutdown policy.

## Service management

```bash
systemctl status apc-ups-bridge.service
journalctl -u apc-ups-bridge.service -f
systemctl status apcupsd.service
apcaccess status
```

The systemd service runs as a dedicated `apc-ups-bridge` account and is enabled at boot. The binary is at `/usr/local/bin/apc-ups-bridge`; system configuration at `/etc/apc-ups-bridge`; password and power state at `/var/lib/apc-ups-bridge`; and event-only JSONL logs at `/var/log/apc-ups-bridge`. Regular readings remain in RAM. The unit uses systemd sandboxing and write access only to these three directories. If changing the bind address later, run `sudo bash install.sh --listen 127.0.0.1:9090` to update it and restart.

### SSH for approved remote shutdowns

Create a limited-purpose SSH identity **for the `apc-ups-bridge` service user** in `/var/lib/apc-ups-bridge/.ssh`, register the *public* key with each approved target and verify each target's host key into the service user's `known_hosts`. The service uses `StrictHostKeyChecking=yes`, batch mode and fixed shutdown commands; it cannot prompt for passwords. The dedicated service user may access the network but **is not root**, and systemd sandboxing does not provide it local shutdown privileges. The Power Rules switch is off until explicitly enabled in the web interface. Test shutdown on disposable equipment before enabling automatic protection.

## Updates and rollback

On an already-configured device, download the current installer again, inspect it, and run:

```bash
sudo bash install.sh --version v0.1.1
```

Release archives are matched against SHA-256 sums from the same GitHub release, and extracted as one named executable. The upgrade keeps service login, SSH identity, remote device rules, event logs and previous power state. Existing apcupsd configuration is *not touched* unless `--configure-apcupsd` is supplied. If the replacement service fails immediately, the installer attempts to restore the previous binary/service unit and restart it. As with any automated process, this is not a substitute for a full backup or UPS dry-run.

## Uninstall

```bash
sudo bash scripts/uninstall.sh
# Only when permanently discarding credentials, settings and all event history:
sudo bash scripts/uninstall.sh --purge
```

Uninstall does not remove `apcupsd` or change its UPS self-protection settings. With no `--purge`, sensitive state and logs remain available for reinstallation.

## Developer build

```bash
go test ./...
go test -race ./...
go vet ./...
bash scripts/test-install.sh
bash scripts/build-release.sh v0.1.0 ./dist
```

GitHub Actions runs tests on pull requests and publishes checksummed cross-compiled archives when a `vX.Y.Z` tag is pushed. Releasing must wait until hardware validation and a security review are complete.
