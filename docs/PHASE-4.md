# Phase 4 — guarded remote power automation

**Status:** feature branch development preview. No UPS-hardware qualification; DO NOT deploy this branch as your only protection against a power outage.

## Scope

- Configurable devices in the authenticated `/power` web page; no database or additional network-facing port.
- Opt-in, globally disabled-by-default remote SSH shutdown and optional Wake-on-LAN recovery.
- Fixed remote commands, not browser-provided shell scripts or command strings.
- One small atomic `/var/lib/apc-ups-bridge/power-state.json` document records the exact outage cycle and attempts across restarts; configuration uses `/etc/apc-ups-bridge/power.json`.
- The normal polling loop invokes power monitoring synchronously, independently of browser sessions.
- Local Raspberry Pi / bridge-host shutdown is **not** managed by Phase 4. Do not enable competing local UPS shutdown daemons without coordinating `apcupsd` behavior.

## CLI preview

Use the README's build and account setup. Add the paths to the long-running launch command:

```sh
./apc-ups-bridge \
  --auth-file ./data/auth.json \
  --settings-file ./data/settings.json \
  --log-dir ./data/events \
  --power-config ./data/power.json \
  --power-state ./data/power-state.json \
  --listen 127.0.0.1:8080
```

Make these files accessible only to the bridge service account. **Do not use `/tmp` for the power-state file**: it must survive reboot. Keep service state and settings when upgrading. If persisted state is corrupted or cannot be read, startup fails rather than guessing what has already been executed. If the state directory becomes unwritable, automatic actions fail closed. The future installer will create and own the directories.

## Before enabling remote shutdown

1. On each remote host, configure a dedicated key-based SSH identity and add the UPS Bridge's public key to that account's `authorized_keys` with narrowly scoped permissions.
2. Verify host-key checking with `known_hosts` for the user running the bridge service. The SSH client enforces `StrictHostKeyChecking=yes` and `BatchMode=yes` and will not request a password or auto-trust a new host.
3. Confirm that the remote account can run the fixed `shutdown -h now` command, or that `sudo -n shutdown -h now` is explicitly permitted without prompting. Do not allow broad passwordless arbitrary commands for the UPS account.
4. Give each host a distinct stable device ID, correct hostname/IP, SSH user and port. Enabling remote shutdown is separate from enabling Wake-on-LAN.
5. If waking is desired, enter a six-byte MAC address and the appropriate **local IPv4 broadcast address** for the network where the device is located. Directed broadcasts may be blocked by routers. Default UDP wake port is 9.
6. Start with global automation **disabled**. After confirming that SSH and wake configurations are correct, explicitly enable it. No manual test-fire button is included yet; these operations can have disruptive consequences.

Never expose direct, unencrypted admin HTTP to the public internet. Prefer a private LAN, VPN, or trusted HTTPS proxy.

## Safety behavior

- The controller must observe healthy `ONLINE` while automation is enabled before it arms. Booting into an outage without previously persisted arming state never triggers spontaneous shutdowns.
- On the next `ONBATT` observation, it opens one durable power cycle and freezes the rule/device snapshot for that cycle.
- After at least 10 seconds on battery (30 by default), each approved host is eligible when UPS battery is at/below the shutdown percentage (default 20%), estimated runtime is at/below an optional configured threshold, or UPS has raised `LOWBATT`. Unknown or stale readings are **not** treated as low battery.
- Hosts shut down in ascending configured priority order (then ID). The bridge records intent to disk and logs the attempt **before** each command. Once the shutdown condition has been met in a cycle it stays met, even if the battery reading later rises above the threshold. A failed shutdown command is retried: up to 5 attempts in total per host, at least 30 seconds apart (retries happen on later UPS polls, at most once per poll). The attempt count is saved to disk before each command, so a restart cannot reset the budget. When the limit is reached, a critical `remote_shutdown_gave_up` event is logged and the host needs manual attention. If SSH reports that the remote host closed the connection (exit status 255), the host is treated as shutting down and counted as accepted. Hosts whose shutdown was never accepted never qualify for auto-wake.
- Recovery requires healthy `ONLINE` for a continuous confirmation period (120 seconds by default), battery at least 40% by default and no `LOWBATT` flag. Communication interruption or a renewed battery state resets that confirmation period. If the battery charge is unknown or stays below the threshold, the cycle still completes (waking eligible hosts) once mains has been continuously stable for 1 hour, and a `power_recovery_battery_timeout` warning is logged. An active `LOWBATT` flag always blocks recovery.
- Only hosts with a successful prior shutdown, wake permission, and unchanged approved identity can receive one WoL packet. Wake attempts also persist before network transmission; delivery is not guaranteed and there is no automatic repeat.
- The admin may disarm during an outage, but cannot edit live enabled rules until the cycle is over. Disarming forfeits automatic recovery for the current cycle; previously shut down hosts may need manual attention.
- Long SSH timeouts are bounded; failed or hanging connections cannot run arbitrary commands or prompt for passwords. Unexpected failures are added to the event log.

**Tradeoff:** Shutdown is idempotent, so it is retried a bounded number of times because stopping hosts before the UPS runs out matters more than avoiding a repeated command. Wake-on-LAN stays at-most-once to prevent accidental repeated actions. A crash between recording an attempt and executing it consumes one shutdown attempt (or skips the wake). A remote shutdown command returning success means it was accepted, not that shutdown has completed. A sent WoL packet does not guarantee the machine booted. A future release can provide carefully designed manual recovery and acknowledgments.

## Test coverage and remaining work

Automated tests cover booting into an outage, delayed battery thresholds, successful shutdown/recovery, power bouncing, communication loss, insufficient recovery charge, bounded shutdown retries and give-up, the recovery battery timeout, best-effort event logging, disabling mid-outage, persisted state across restart, corrupt state fail-closed, CSRF/authentication and invalid remote settings. Run:

```sh
go test ./...
go test -race ./...
go vet ./...
```

**Hardware tests are outstanding:** different APC USB models, LOWBATT behavior, abrupt service interruption, power failures during atomic state writes, systemd permissions, remote SSH command paths on target distros, WOL directed broadcast through local interfaces, and actual device boot/shutdown acknowledgments. There is no installer, automatic service installation, or signed release binary yet.
