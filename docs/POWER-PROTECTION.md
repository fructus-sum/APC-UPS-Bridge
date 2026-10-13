# Remote power protection

APC UPS Bridge can shut down other computers over SSH when the power fails, and can optionally wake them with Wake-on-LAN when power is back. It is **off by default**, and each device has its own shutdown and wake permission. The local Linux host itself is shut down by `apcupsd`, not by this feature.

For a first-time setup, see [Install](INSTALL.md). For the SSH key, see the [SSH key guide](SSH-KEYS.md). For battery self-tests, see [Battery health and maintenance](BATTERY.md).

## Add your devices

Open **Power rules & devices**. You can add up to 16 devices. Each needs a **Device ID**, **Friendly name**, **IP or hostname**, **SSH user** and **SSH port**. You can also set:

- **Shutdown priority** (0 to 100). Lower numbers shut down first.
- **Use sudo -n**, if the SSH user needs sudo to shut the machine down.
- **MAC address**, **Wake destination IP** and **Wake UDP port**, for Wake-on-LAN.
- **Allow automatic shutdown** and **Allow wake after shutdown**. Wake needs shutdown to be allowed too.

Save a device and select **Test SSH (no shutdown)**. The test checks the saved device with the bridge's key and takes up to about 8 seconds. It confirms that the key login works. It does not check that the account may shut the machine down, and it never sends a shutdown. Try the real shutdown once on equipment where an outage is acceptable.

The bridge only runs `shutdown -h now` (with `sudo -n` in front if ticked). It never uses SSH passwords or commands typed in the browser, and it needs an `ssh` program on the bridge host.

## Rules and their limits

These apply to the remote devices, not the Linux host running the bridge.

| Setting | Allowed | Default |
| --- | --- | --- |
| Remote machine shutdown at UPS battery (%) | 1 to 95 | 20 |
| Remote machine shutdown at estimated UPS runtime (minutes) | 0 to 180 (0 turns it off) | 0 |
| Minimum time confirmed on battery (seconds) | 10 to 3600 | 30 |
| Minimum battery for recovery (%) | above the shutdown battery, up to 100 | 40 |
| Stable mains before wake-up (seconds) | 30 to 3600 | 120 |

Turn on **Enable automatic remote protection (opt-in)** only when at least one device has automatic shutdown allowed.

## What happens in an outage

- The bridge must first see the UPS online while protection is on. If the bridge host starts while already on battery, it does not send shutdowns.
- Once the UPS has been on battery for the confirmation time, shutdowns start when the battery is at or below your limit, the runtime is at or below your limit, or the UPS reports low battery. A reading the UPS does not report is never treated as zero.
- Devices shut down in priority order. Devices with the same priority go at the same time, and the next group starts when the previous group's commands finish.
- A device gets up to 5 shutdown attempts, at least 30 seconds apart. Restarting the bridge does not reset the count.
- After a shutdown is accepted, the bridge waits 2 minutes, then checks the SSH port. Two failed checks at least 30 seconds apart mean the device is treated as off. This cannot tell a real shutdown from a network problem. If a device still answers, it can get another attempt. A device given up on is logged.
- Turning protection off stops further actions.

## Recovery and waking

When mains has been stable for the set time, the battery is above the recovery level and the UPS reports no low battery, devices that were shut down and are allowed to wake receive one Wake-on-LAN packet. If the battery level is not reported or stays too low, recovery goes ahead after an hour of steady mains. A low-battery signal always blocks it.

A wake packet is sent at most once per outage and is not a guarantee the device starts. Check your device's BIOS and network settings for Wake-on-LAN. If you turn protection off during an outage, you may need to start devices by hand.

Test shutdown order, SSH host-key checks, restarts and Wake-on-LAN on non-critical equipment before relying on them. The final outage report has its own rules; see [Email notifications](EMAIL-NOTIFICATIONS.md).

## Checking whether devices are online

The Power page and the dashboard (**Protected devices**) show whether each device is online. This is for information only. It never causes a shutdown or wake, and no SSH login is made. The page refreshes the status every 10 seconds while the browser tab is visible.

Each device has an **Online check** setting:

- **Ping (default):** one small ping about once a minute per device, one device at a time.
- **SSH port (connect only, no login):** opens a connection to the SSH port and closes it at once. Some SSH servers log this as a disconnect before login.
- **Off:** the device is not checked.

An online device shows as offline after two missed checks in a row. The label reads "Offline since HH:MM", followed by the reason when there is one. A device that is not saved yet shows **Save to start checking**. After you save, a quick extra check runs shortly afterwards.

Devices that block ping show as offline, so choose **SSH port** or **Off** for them. If ping is not allowed for the service on the bridge host, the device shows as unavailable; choose **SSH port** or see the administrator notes. Fail2ban normally ignores connect-only checks, so prefer **Ping**.

Saved devices are collapsed on the Power page, showing the name, status and **Remove**. Open one to edit it. New devices start open, and if a save is refused all devices open so you can fix them.

## Shutting down the local Linux host

The local Linux host is shut down by `apcupsd`, using the values in `/etc/apcupsd/apcupsd.conf`. The Power page shows them. On the **Settings** page, under **Local host shutdown thresholds (apcupsd)**, you can either keep your existing settings or manage them from the page:

| Setting | Allowed | Page default |
| --- | --- | --- |
| Host shutdown battery percentage (%) | 5 to 95 | 10 |
| Host remaining runtime (minutes) | 0 to 120 (0 turns it off) | 3 |
| Shut down after this many minutes on battery | 0 to 120 (0 turns it off) | 10 |

`apcupsd` acts on whichever limit is reached first, and also when the UPS reports low battery. Charge and runtime estimates become unreliable as batteries age, so a timer of about 10 minutes is a good start; test it with a real power cut. Applying changes backs up the old file and restarts `apcupsd`, and only works while the UPS is online. Switching back to existing settings does not undo values already applied.

These local host limits are separate from the remote device limits above (which default to 20% battery). The low battery warning in Settings only records an event and shuts nothing down. The installer's optional `--host-protection` preset is 10% battery or 3 minutes.

## For administrators

- Power rules are in `/etc/apc-ups-bridge/power.json`. Progress is in `/var/lib/apc-ups-bridge/power-state.json`. Keep both through upgrades. A damaged state file stops actions rather than guessing what was already done.
- SSH is run with `BatchMode=yes` and `StrictHostKeyChecking=yes` as the `apc-ups-bridge` user, so each destination must be in that user's `known_hosts`.
- Ping needs the service's group inside `net.ipv4.ping_group_range`. Check it with `sysctl net.ipv4.ping_group_range`. Current Debian and Raspberry Pi OS usually allow all groups. Status is kept in memory only and is lost on restart.
- Local host thresholds are applied by a small root-owned helper that listens on a systemd socket. The web service runs without login rights. The helper accepts requests from any process running as the service user, so a compromised web service could set a Pi threshold anywhere in the ranges above and cause an early local host shutdown. It cannot change other `apcupsd` settings or run other commands. Its restart of `apcupsd` is limited to 20 seconds. If the page reports no response, the result is unknown, so refresh and check the current values.
- Every action is saved before it runs. A wake packet is never repeated in the same outage.
