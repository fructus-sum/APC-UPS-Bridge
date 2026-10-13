# APC UPS Bridge

**Give your older USB-connected APC UPS a second life with a simple, lightweight, self-hosted web dashboard and optional power protection for your Linux homelab.**

APC UPS Bridge runs on a systemd-based Linux computer, reads a compatible USB APC UPS through `apcupsd`, and shows its status in a password-protected web interface. It can also shut down other computers on your network when the power fails and wake them again afterwards, and it can email you about outages. No cloud account or database is needed: everything stays on your own hardware.

### Why I built this

I built this project after picking up an old APC UPS for cheap. The original batteries were dead, so I replaced them with LiFePO4 batteries.

**⚠️ DO NOT DO THIS — YOU COULD BURN YOUR HOUSE DOWN!** UPS charging systems are not necessarily designed for LiFePO4 batteries. This was my own experiment, not a recommended modification.

I then rigged it up to power my network and homelab.

I wanted a simple, lightweight way to monitor the UPS and automatically shut down higher-power devices when needed. Some of those devices run 24/7, while others are only switched on occasionally.

The UPS doesn't have any network connectivity, so I connected it via USB to a Raspberry Pi 4, which runs APC UPS Bridge and provides the web interface and power management.

> **Safety:** Power automation is switched off until you turn it on. Test your UPS, remote shutdown and Wake-on-LAN on equipment you can afford to restart before relying on them. Do not make this your only safeguard.

## What it does

- **Dashboard:** whether you are on mains or battery power, a battery charge bar, remaining runtime, load, voltages, UPS details and communication health, when your UPS reports them. A **Battery health & maintenance** panel shows battery details your UPS reports and steps for a manual battery test (see [battery health](docs/BATTERY.md)). A **Protected devices** panel shows whether each computer you protect is online. A notice appears when a firmware update is listed for your UPS.
- **History graphs:** input and output voltage and UPS load for the last 24 hours, 7, 14 (default) or 30 days, and a battery charge, runtime and mains-power graph for 30 (default), 90, 180 or 365 days. Power events are marked on the graphs. See [history graphs](docs/HISTORY.md).
- **Events & reports:** a timeline of power changes and communication problems, how long each outage lasted, filters, CSV export and a choice of how long events are kept.
- **Power rules & devices:** list up to 16 computers to shut down over SSH, in the order you choose, when the battery falls to a level you set, with optional Wake-on-LAN to start them again after power returns. Each device has an **Online check** (Ping, SSH port or Off) that is only shown on the dashboard and never shuts anything down, and a **Test SSH** button that checks the login without shutting anything down. Pending shutdowns and wake-ups survive restarts and power cuts. See [power protection](docs/POWER-PROTECTION.md).
- **Email notifications:** connect an email account to get a message when an outage starts and ends, plus a final report once power is back, the UPS is fully charged and your devices are reachable. See [email notifications](docs/EMAIL-NOTIFICATIONS.md).
- **Login security:** the web login blocks repeated wrong passwords, and the **Login security** page lists recent bans and unbans made by Fail2ban (set up automatically on Debian and Ubuntu). See [login security](docs/LOGIN-SECURITY.md).
- **Settings:** how often the UPS is read (5 to 60 seconds), the low-battery warning level, how long events are kept, when the local Linux host shuts down (below), the firmware panel and your administrator password.
- **Firmware check:** shows the firmware your UPS reports and warns when a newer one is listed for your model. It only compares against a built-in list and never installs firmware. See [firmware guidance](docs/FIRMWARE.md).

### Local Linux host shutdown settings

The **Settings** page can change when the Linux host running APC UPS Bridge shuts down during an outage: the battery percentage, the remaining runtime and an on-battery timer. This is separate from the shutdown rules for other computers. Changes are accepted only while mains power is on, and the previous configuration is backed up first. Uninstalling the bridge leaves these settings in `apcupsd.conf`.

### How often pages refresh

The dashboard updates every 10 seconds. Firmware information and recent events update every 60 seconds. Updating pauses while the browser tab is hidden and catches up as soon as you return to it.

## Linux distribution support

APC UPS Bridge is not limited to one device or Linux distribution. It is intended for **systemd-based Linux hosts** with a compatible USB-connected APC UPS. The statuses below describe support indicated by the installer and source code, **not completed hardware tests on every distribution**.

| Linux distribution | Main application | Automatic Fail2ban |
| --- | --- | --- |
| Ubuntu 22.04 / 24.04 / 26.04 LTS | ✅ | ✅ |
| Debian 12 / 13 | ✅ | ✅ |
| Raspberry Pi OS | ✅ | ✅ |
| Linux Mint | ✅ | ✅ |
| Fedora Server / Workstation | ✅ Expected, **not tested** | ❌ Manual — **MUST use `--skip-fail2ban` in the installer** |
| openSUSE Tumbleweed | ✅ Expected, **not tested** | ❌ Manual — **MUST use `--skip-fail2ban` in the installer** |

For **Fedora and openSUSE**, both new installs and updates **must** pass `--skip-fail2ban`. This skips only automatic Fail2ban setup: the main application and its built-in login rate limit remain available. Users who need Fail2ban on these distributions must configure it separately. See the [installation guide](docs/INSTALL.md#linux-distribution-support) for the exact commands.

## Install on Linux

**Requirements:** a Linux computer with systemd, a supported APC USB UPS, and internet access during installation. The installer can install `apcupsd` with apt, dnf or zypper; on other systems install it yourself first. The installer builds the program on your computer, so you do not need to download a release.

```bash
curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/install.sh | sudo bash
```

The installer asks for a web port (default **8080**) and an administrator password, then prints the address to open in your browser. It installs `apcupsd` if it is missing and sets it up for a USB UPS only in that case. An existing `apcupsd` configuration is left alone unless you ask otherwise. The bridge runs as its own limited user and starts automatically at boot.

**Use it on your home network only.** The web page uses plain HTTP, so the password is not encrypted on the wire. Never forward its port to the internet. For access from elsewhere use a VPN or an HTTPS reverse proxy, or install with `--listen 127.0.0.1:8080` and put a proxy in front. By default the installed service is reachable from other devices on your network.

Read the [installation guide](docs/INSTALL.md) for setup options, upgrades, service commands and removal. To set up a remote computer for shutdown, see the [SSH key guide](docs/SSH-KEYS.md) and the [ready-made setup scripts](tools/README.md).

## Update, troubleshoot or uninstall

To update, get the latest files and run the installer again:

```bash
git pull
sudo bash scripts/install.sh
```

(If you installed with the one-line command, run it again instead.) Your login, settings, email settings, device rules, event logs and listening address are kept. Automatic Fail2ban setup is available on Debian/Ubuntu-based systems; **Fedora and openSUSE users must use `sudo bash scripts/install.sh --skip-fail2ban` for updates as well as installs**. Check the running service and the UPS with:

```bash
systemctl status apc-ups-bridge.service
journalctl -u apc-ups-bridge.service -f
apcaccess status
```

To remove the application while **keeping its data**:

```bash
sudo apc-ups-bridge-uninstall
```

Add `--purge` to also delete the configuration, credentials, saved state, logs and the dedicated account. Uninstalling does **not** remove or reconfigure `apcupsd`.

## Developers

Build, test and release instructions are in the [architecture guide](docs/ARCHITECTURE.md#development).

APC is a trademark of its respective owner. This independent project is not affiliated with or endorsed by APC or Schneider Electric.
