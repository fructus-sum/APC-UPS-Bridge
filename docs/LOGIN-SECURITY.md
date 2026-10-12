# Login security and Fail2ban

APC UPS Bridge has two complementary defenses: application-level rate limiting, and a dedicated Fail2ban jail on supported Debian/Ubuntu installations.

## Policy

- Every **failed password attempt** is logged to the service's systemd journal with only the connecting peer's IP address. Passwords and submitted usernames are not logged. Do not trust `X-Forwarded-For` for bans.
- Five failures from one IP within **ten minutes** trigger the Fail2ban `apc-ups-bridge` jail.
- The first ban is **ten minutes**. Fail2ban's incremental-bantime policy doubles repeat bans, capped at **one day**, subject to the local Fail2ban database/history retention.
- The application's separate in-memory throttle blocks new logins after six failures within ten minutes. It resets on a successful login or expiry; rebooting the service also clears this in-memory counter. Fail2ban history is separately managed by the OS.
- When the application sees a fifth failed attempt within its ten-minute window, it attempts a **single warning email**, globally rate-limited to one per hour. The email contains no remote IP addresses, usernames or credentials. It uses the existing SMTP configuration, if enabled. Email is best-effort and cannot guarantee delivery while offline.
- No global web lockout is enabled. Blocking an IP must not interfere with UPS polling, power protection, reporting or service restarts.
- **Login security** in the web navigation shows only ban/unban history from this application's Fail2ban jail, not the general server journal. This history is **read-only**. IP addresses appear in this authenticated local view, but not emails.

## Install / upgrade

Running `sudo bash scripts/install.sh` on a supported Debian host automatically installs/configures Fail2ban or fixes missing APC jail configuration on **every install and upgrade**. The new setup is idempotent; unrelated Fail2ban jails are left intact. The application continues to run if the security setup fails, but the installer clearly prints a WARNING (it does not claim a secured setup).

The installer delegates to `scripts/setup-fail2ban.sh --port PORT`. To run it separately from an up-to-date checkout:

```bash
sudo bash scripts/setup-fail2ban.sh --port 8080
sudo fail2ban-client status apc-ups-bridge
sudo fail2ban-client -t
```

The port must match the application's listening port. On non-apt Linux systems the setup is manual: ensure a systemd-journal-capable Fail2ban installation, copy the dedicated filter/action/jail and provision the root-owned security log with group-read-only access for `apc-ups-bridge`.

Only the root-managed audit log `/var/log/apc-ups-bridge-fail2ban/events.log` is group readable by the web service. The web service remains unprivileged and has **no** sudo, `fail2ban-client` write permissions or general journal access. Log rotation retains approximately 12 weekly archives; the UI reads the current live log and displays the most recent 100 actions.

## Operational checks

```bash
sudo fail2ban-client status apc-ups-bridge
sudo journalctl -u apc-ups-bridge.service --since '10 minutes ago'
sudo journalctl -u fail2ban.service --since '10 minutes ago'
sudo tail -n 30 /var/log/apc-ups-bridge-fail2ban/events.log
```

Avoid testing on your administrator's only access path. Use an independent console or management account in case firewall rules unexpectedly ban your IP.

For emergency troubleshooting, use the normal privileged Fail2ban CLI from the server console. The web application intentionally has no unban action.

## Email reporting

The final outage report retains its interruption timeline, UPS metrics, relevant numerical event details and friendly names of protected machines. Raw addresses, SSH ports and arbitrary event detail fields are excluded from email. Security emails likewise exclude source IPs. The logs on the trusted server may retain network addresses for diagnosis.
