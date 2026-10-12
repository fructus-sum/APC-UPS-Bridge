# Remote power protection

APC UPS Bridge can issue approved SSH shutdown commands to remote Linux machines and optionally send Wake-on-LAN packets after power recovers. The feature is **globally disabled by default**, and each host has separate shutdown/wake permissions. The Raspberry Pi's own shutdown is handled by `apcupsd`, not a second local shutdown loop in the bridge.

## Configure protected hosts

Use the authenticated **Power rules & devices** page. Up to 16 remote devices may be configured. Each needs a unique ID, name, host/address, SSH username and port. Set shutdown priority, whether `sudo -n` is needed, and optional MAC, IPv4 wake destination and UDP port for WoL.

Prepare the SSH identity as the **`apc-ups-bridge` service user**, including a trustworthy `known_hosts` entry for each destination (see the [SSH key setup guide](SSH-KEYS.md)). The application uses `BatchMode=yes`, `StrictHostKeyChecking=yes` and fixed commands (`shutdown -h now`, optionally prefixed with `sudo -n`). It does not accept SSH passwords or arbitrary shell commands from the browser. A suitable `ssh` client must be installed on the bridge host.

On the **Power rules & devices** page, save a host and select **Test SSH (no shutdown)**. The test attempts a key-based SSH login with strict host-key checking as the Bridge service user without requesting a remote shell or remote command. It **does not test sudo/shutdown permission** and never sends a shutdown command; an SSH account may have a forced command, so a connection-only test is safer. Verify actual shutdown permission separately on disposable equipment. Test the real command manually on disposable equipment before relying on automation.

**Local Pi settings:** The Settings page can switch between leaving existing `apcupsd` settings alone and managing three shutdown triggers: battery percentage (5–95%), minutes of runtime remaining (0–120, 0 = off) and minutes on battery (`TIMEOUT`, 0–120, 0 = off). `apcupsd` shuts the Pi down when whichever is reached first, and also on the UPS's own low-battery (`LOWBATT`) signal. On older UPS units or original batteries, charge and runtime estimates become optimistic as the batteries age, so set the on-battery timer (about 10 minutes is a sensible start) and verify it with a real mains-off test. Applying managed settings backs up the original file, then restarts `apcupsd`. A restricted root-owned systemd socket helper applies validated values; the web process keeps its no-login unprivileged account. A change requires a healthy ONLINE UPS. Returning to existing mode stops web management **without reverting** thresholds already applied; manually restore a backup if desired. The local shutdown controller always remains `apcupsd`.

**Different thresholds:** the Pi/Bridge host itself is shut down by native `apcupsd` at the values configured in `/etc/apcupsd/apcupsd.conf` (battery %, minutes remaining, or an independent on-battery timer). The UI reads these settings separately. The installer’s optional `--host-protection` preset is 10% battery or 3 minutes, but existing apcupsd settings may differ. Remote hosts use the bridge's independent shutdown threshold, **20% UPS charge by default** or optional runtime/LOWBATT conditions. A low-battery *warning* in Settings is a third, event-only threshold, not a shutdown rule.

**Helper trust model:** the helper accepts requests from any process running as the `apc-ups-bridge` service user, so a compromised web service could set Pi thresholds anywhere in the validated range (5–95% battery, 0–120 minutes runtime) and cause an early Pi shutdown. It cannot write any other `apcupsd` setting or run other commands. The `apcupsd` restart is limited to 20 seconds; a "no response" result means the outcome is unknown, so refresh to check the current Pi thresholds.

Verify that the remote account can issue the shutdown command using narrowly scoped permissions before enabling power automation. If using WoL, confirm the target's firmware/network settings and broadcast reachability on your actual network. WoL delivery is not guaranteed.

## Shutdown behavior

- The controller must observe a healthy ONLINE state while enabled before it arms. Booting already on battery does not trigger unobserved shutdown commands.
- After ON_BATTERY confirmation (30 seconds by default, minimum 10), a shutdown trigger is low reported battery (20% by default), configured remaining runtime, or the UPS `LOWBATT` flag. Unknown battery and runtime values are not interpreted as zero.
- Hosts are processed in increasing shutdown-priority groups. Hosts within one priority group are handled in parallel, and the next priority group begins when the previous group's commands return.
- Each host may get up to **5 shutdown attempts**, separated by at least **30 seconds**, with attempts persisted before execution. Later polling cycles can retry an unconfirmed shutdown; restarting the bridge does not reset the limit.
- Once a shutdown command is accepted, the bridge later checks SSH-port reachability. Two failed checks (at least 30 seconds apart, following a verification delay) classify a host as off. This cannot distinguish genuine shutdown from a network failure. A host that still answers can receive another bounded shutdown attempt. Gave-up cases are logged.
- Turning automation off interrupts further approved actions. State and exceptions are logged as meaningful events; routine poll samples are not written.

## Recovery and waking hosts

Healthy ONLINE must remain stable for a configured period (120 seconds by default), with battery above the configured recovery threshold (40% by default) and no `LOWBATT` indication. If battery readings are absent or remain too low, recovery can proceed after an hour of continuously stable mains with a warning; an active `LOWBATT` always blocks recovery.

Only hosts with an accepted prior shutdown, explicit wake permission and unchanged approved identity qualify for a WoL packet. A wake attempt is persisted before sending and is **at most once** in a cycle, not a guarantee of startup. Disabling automation during an incident may require manual recovery.

Power protection state is stored at `/var/lib/apc-ups-bridge/power-state.json`, separate from configurable rules in `/etc/apc-ups-bridge/power.json`. Corrupt state fails closed instead of guessing which commands were already attempted. Always keep both files across upgrades or power cycles.

**Safety:** Test shutdown order, SSH host-key verification, service permissions, abrupt Pi restarts, battery thresholds and WoL on noncritical equipment before relying on automatic protection. Use [installation guidance](INSTALL.md) for service setup and [email documentation](EMAIL-NOTIFICATIONS.md) for the separate full-charge report completion rule.

## Dashboard device availability (read-only)

The dashboard and saved remote-device cards show friendly names and **Online** or **Offline (SSH unreachable)**. This is an independent, authenticated, read-only TCP probe of each configured SSH port, refreshed every 30 seconds by the browser. It performs **no SSH authentication**, does not test the shutdown command, and cannot wake or shut down a host. An SSH port that is closed, blocked or unreachable may show Offline even when the machine is running; Online means only that the SSH TCP port accepted a connection. It does not claim verified machine power state. The response contains only friendly names, device IDs, status and check timestamps, never SSH credentials.

Saved devices are collapsed on the Power rules page by default, with the friendly name, reachability status and Remove control always visible. Open a device to edit its fields or perform the existing connection-only Test SSH. New devices start expanded, and invalid fields reopen their editor when saving. The web server retains its original unprivileged service identity.

## Battery health and self-testing

The dashboard's collapsed **System information** details include a **Battery health & maintenance** group. Optional status values from apcaccess are battery voltage (BATTV), nominal battery voltage (NOMBATTV), installation/replacement date (BATTDATE), last self-test (LASTSTEST), self-test result (SELFTEST), and self-test interval (STESTI). Missing values are shown as **Not reported**, not zeros or a false health verdict. The page does not change UPS settings.

The **Battery self-test instructions** button has an explicit danger warning and requires confirmation to reveal the local command sequence. **It never runs a self-test in the web process.** On supported APC hardware, a host administrator can enter maintenance mode at the machine's console with:

```sh
sudo systemctl stop apcupsd
sudo apctest
# Select the supported self-test action from the interactive menu.
# Regardless of success/failure, finish by restoring service:
sudo systemctl start apcupsd
apcaccess status
```

WARNING: An APC self-test can run the protected load from battery, possibly triggering a UPS shutdown on weak or overloaded equipment. Stopping apcupsd temporarily disables native Pi UPS shutdown protection and causes monitoring/automation interruptions. Do not test during an outage or when a battery is weak. Perform under a planned, attended maintenance window and verify the service recovered. Hardware/driver support differs, so absence of a supported self-test action in apctest means it must not be attempted through arbitrary commands. No sudoers, service user or host-settings helper permissions are widened for this feature.
