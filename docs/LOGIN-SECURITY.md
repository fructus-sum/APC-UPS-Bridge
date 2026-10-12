# Login security

APC UPS Bridge protects its sign-in page in two ways: a built-in limit on failed sign-ins, and a Fail2ban rule that temporarily blocks addresses that keep failing. Fail2ban is set up by the installer on Debian and Ubuntu.

## How blocking works

- **Ban rule:** five failed sign-ins from one address within 10 minutes get that address banned. The first ban lasts 5 minutes. Each repeat ban doubles: 10, 20, 40, 80 minutes and so on, with no maximum. Fail2ban forgets old bans after about a day, so an address that returns after more than a day starts at 5 minutes again. The installer does not change Fail2ban's other settings.
- **Built-in limit:** after six failed sign-ins from one address within 10 minutes, further attempts from it are refused for the rest of that 10 minutes, even with the right password. A successful sign-in clears the count, and so does restarting the service. This works with or without Fail2ban.
- **Warning email:** the fifth failure also sends one warning email if email is turned on. See [Email notifications](EMAIL-NOTIFICATIONS.md).

Only failed password attempts count. Passwords and usernames are never logged. Only the address is.

## The Login security page

**Login security** in the menu shows the most recent bans and unbans from this Fail2ban rule. It is read-only, and the addresses are shown only to signed-in users. There is no unban button. To unban an address, use the Fail2ban command line on the server (see below).

## Things to watch

- Behind a reverse proxy or a shared network address, many people can look like one address. The bridge ignores forwarding headers on purpose, so make sure your only trusted proxy or management address cannot be banned.
- Do not test bans from your only way into the server. Keep a console or second account in case you ban yourself.
- Banning never affects UPS monitoring, power protection, reports or service restarts.

## Setup

Installing or updating on Debian or Ubuntu sets up Fail2ban for you. Setup is safe to run again, and other Fail2ban rules are left alone. If setup fails, the installer prints a WARNING and undoes its own changes. The bridge keeps running with the built-in limit only.

- Pass `--skip-fail2ban` to the installer to skip this step. It does not remove a rule that is already set up.
- On systems without `apt-get`, setup is skipped with a warning and Fail2ban must be set up by hand. The built-in limit still applies. See [Install](INSTALL.md).

To run the setup on its own, from the project folder, using the port the bridge listens on (1024 to 65535):

```bash
sudo bash scripts/setup-fail2ban.sh --port 8080
sudo fail2ban-client status apc-ups-bridge
sudo fail2ban-client -t
```

## Checking and unbanning

```bash
sudo fail2ban-client status apc-ups-bridge
sudo fail2ban-client set apc-ups-bridge unbanip ADDRESS
sudo journalctl -u apc-ups-bridge.service --since '10 minutes ago'
sudo journalctl -u fail2ban.service --since '10 minutes ago'
sudo tail -n 30 /var/log/apc-ups-bridge-fail2ban/events.log
```

## For administrators

- Each failed password attempt is written to the service's systemd journal with only the connecting address. The Fail2ban rule reads that journal.
- The ban and unban history is written to `/var/log/apc-ups-bridge-fail2ban/events.log`, owned by root and readable by the service's group. Logs are rotated weekly with about 12 archives kept. The page shows the latest 100 actions.
- The web service has no sudo rights, no `fail2ban-client` access and no journal access.
- On a system without the automatic setup, install a Fail2ban with systemd journal support, copy the filter, action and rule from `packaging/fail2ban`, and create the root-owned log with read access for the `apc-ups-bridge` group.
