# Email notifications

APC UPS Bridge can email you when the power goes out, when it returns, and once everything has recovered. Email never delays a shutdown of your devices and is never needed for power recovery. No extra mail server or open port is installed.

## Set up email

1. Sign in and open **Email notifications**.
2. Enter your provider's **SMTP hostname** and **SMTP port**. Choose **STARTTLS (usually port 587)** or **Implicit TLS (usually port 465)** under **Transport encryption**. An encrypted connection is required; there is no unencrypted option.
3. Enter the **Login / account** and the **SMTP app password**. The password is not shown again after saving. Leave the field blank to keep the saved one. Use a dedicated mail account with an app password if your provider offers one. Providers that only allow OAuth sign-in are not supported.
4. Enter a **From email** and a **Send reports to** address. Each must be a single plain address.
5. Tick **Enable outgoing email** and select **Save email settings**.
6. While the power is on, select **Send test email (while on mains)** and check that it arrives and is not filtered as spam.

The page shows the current state (Enabled or Disabled) and the status of the recovery report.

## What you will receive

- **Power outage:** one email when the bridge first sees the UPS switch to battery. If the Pi starts up while already on battery, the email says the true start time is unknown.
- **Power restored:** one email when mains returns, for outages whose start email was attempted.
- **Final recovery report:** one email once everything has recovered (see below). It lists every interruption, the lowest battery level, service restarts, gaps in monitoring, the protected devices by name and recent power events. It leaves out addresses and ports.
- **Login security warning:** one email when a fifth failed sign-in within 10 minutes comes from the same address. At most one is sent per hour, and it contains no addresses. See [Login security](LOGIN-SECURITY.md).

Outage and restored emails are tried once and are not retried, so a lost internet connection cannot cause a pile-up. If the power flickers, a new outage email is held back until 10 minutes after the previous email. A short interruption that ends inside that time gets no email of its own but still appears in the final report.

## When the final report is sent

The bridge keeps one report open for the whole outage, including repeated power loss and Pi restarts. It sends the report only when all of these are true:

- the UPS is back on mains power;
- the UPS reports **100%** battery charge;
- if **Wait until configured protected devices accept SSH connections before sending the final report** is ticked, every protected device accepts a connection on its SSH port;
- your mail server can be reached.

Things to know:

- A UPS that never reports 100%, or does not report battery charge at all, keeps the report waiting.
- A device that never comes back keeps the report waiting. Untick the wait option, for example for a retired device.
- Accepting an SSH connection shows the device is reachable, not that its applications have fully started.
- If sending fails, it is tried again at least 10 minutes later. If the mail server rejects the login, sending stops until you update the settings.
- The report is checked at most every 2 minutes, and nothing is sent while the UPS is on battery.
- Mail systems cannot promise a message arrives exactly once, so after an unlucky restart the final report may arrive twice.

If the Pi was switched off for a long time, some outages are never seen. Reports separate what was observed from what is estimated, and never invent an outage length for time the Pi was off.

## For administrators

- The password is stored in `/var/lib/apc-ups-bridge/email.json` with owner-only permissions and is never returned by the web interface. Use HTTPS or a trusted VPN if you open the interface beyond a trusted network.
- The report state is kept in `/var/lib/apc-ups-bridge/email-state.json` and is written before each send, so restarts do not lose it.
- Events are kept in `/var/log/apc-ups-bridge/events-YYYY-MM.jsonl`. While a report is pending, automatic cleanup of these files pauses.
- Sending runs on its own worker with short timeouts, so DNS, mail or internet problems never block UPS polling or SSH shutdown. Each report has a fixed Message-ID, which helps spot a duplicate.
- A failure to save the report state is written to the service log and the report is kept.
