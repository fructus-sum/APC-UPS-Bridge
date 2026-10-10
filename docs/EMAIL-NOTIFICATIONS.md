# Email alerts and recovery reports

Status: development feature, not yet verified on a physical Raspberry Pi/UPS or with real email providers.

## Purpose

The bridge may lose internet access during an outage. Sending email must never delay remote shutdown or be required for power recovery. A single low-overhead email worker processes UPS observations independently of the monitoring and protection loop. No database, SMTP server or extra listening port is installed.

## Configure SMTP

1. Sign in as administrator and open **Email notifications**.
2. Enter your provider's SMTP hostname and port, choose **STARTTLS** (usually 587) or **implicit TLS** (usually 465), and enter the SMTP login and app-specific password. SMTP AUTH PLAIN over authenticated TLS is required; providers requiring OAuth-only authentication are not yet supported.
3. Set a plain sender and recipient email address, then enable email and save.
4. While mains power is verified, use **Send test email**. Verify sender authorization and spam filtering.
5. Configure the remote devices under **Power rules & devices**. When **Wait for devices** is checked, each shutdown-enabled device must accept a TCP connection on its configured SSH port. This is a *reachability test*, **not** proof that applications or services have fully recovered.

The SMTP password is held in `/var/lib/apc-ups-bridge/email.json` with `0600` permissions, never sent in subsequent GET API responses. Submitting an empty password keeps the existing saved secret. The email account should be dedicated to this application, ideally using an app password. Enable HTTPS or a trusted VPN before using the web UI outside a trusted host.

## Notification sequence

- **On battery:** on first detected loss of mains, try one outage email. If the Pi boots to ONBATT without prior observation, the alert explicitly says the true start time is unknown. Each detected interruption has its own email attempt, recorded on disk *before* contacting SMTP. A failed alert is not retried.
- **Power restored:** when a healthy ONLINE reading first follows ONBATT, try one restoration email. If internet is down, no alert retry is scheduled.
- **Recovery pending:** keep one incident (possibly containing multiple interruptions) and a running low-battery observation on disk, including through repeated Raspberry Pi reboots. The system does **not** count an incident complete just because power returned.
- **Complete recovery:** only when mains is ONLINE, the reported battery percentage is **100%**, and every protected device is SSH-reachable (when configured) can the final summary be attempted. The SMTP endpoint must first accept a TCP connection. A failed reachability probe **does not send an email** and is checked at most once every 2 minutes. Temporary summary-send failures keep the report and space further attempts by at least 10 minutes. Invalid SMTP credentials block further delivery until the configuration is updated.
- **Successful summary:** persist its delivery outcome, then end the incident. The summary includes every recorded interruption, lowest observed battery level, service restarts, observation gaps, protected hosts, and recent power-related events.

SMTP has no generally supported exactly-once delivery guarantee: a Pi can restart after the mail relay accepts a message but before the success result is persisted, so the final summary might be delivered twice. The deterministic incident Message-ID helps identify duplicates. Individual start/restoration alerts favor at-most-once attempts to avoid alert spam during a loss of internet service.

## Files and retention

- `/var/lib/apc-ups-bridge/email.json` — protected SMTP settings and password.
- `/var/lib/apc-ups-bridge/email-state.json` — compact durable incident and delivery state (atomic write + sync).
- `/var/log/apc-ups-bridge/events-YYYY-MM.jsonl` — event-only log files. During a pending report, automatic retention cleanup pauses so events survive until the summary can be delivered.

After restart, the first observed UPS reading is reconciled with saved incident state. No outage duration is fabricated for a period when the Pi was offline; the report labels start/end timestamps as observed boundaries and flags monitoring interruptions. Power protection does not depend on SMTP and remains disabled by default.

## Limitations and fault handling

- A UPS that never reports 100% charge will retain the report until that condition is met; this is by design.
- If a protected SSH host never becomes reachable, the report waits unless **Wait for devices** is switched off (for example for a retired device). No indefinite ping flood is used.
- DNS, SMTP and internet failures must not block UPS polling or SSH shutdown: email operates on a separate buffered worker with short timeouts.
- Normal UPS samples are not logged. Unexpected failures of state-file persistence are reported to service logs and the pending report is preserved rather than silently discarded.
- If the host is powered off for long periods, some outages may never be observed. Reports explicitly distinguish observed events from estimated durations.
- This feature does not install or update UPS firmware and retains the existing firmware advisory unchanged.
