# Debian shutdown-only SSH setup tools

These tools provision a restricted account on a **remote Debian host** and connect it to APC UPS Bridge using a one-time password login to install an SSH public key. The password is then disabled, and the account accepts only the Bridge's shutdown command (`shutdown -h now` or `sudo -n shutdown -h now`) or a connection-only SSH test.

**Review both scripts before running them as root.** Keep an independent administrator SSH session open, and test first on a noncritical Debian machine. The account can still be used to shut down the host if its SSH key is stolen; firewall SSH access to the Pi's IP where possible.

## 1. Remote Debian server

```bash
curl -fsSLo /tmp/apc-ups-debian-user.sh https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/tools/setup-debian-shutdown-user.sh
sudo bash /tmp/apc-ups-debian-user.sh
```

The script asks for a new account name and a **temporary** password. Use a long random temporary password and run the Pi step promptly: password login for that account stays open until the key is enrolled, so restrict SSH to the Pi's address with a firewall where possible. The home directory is created root-owned and empty. The script refuses to run if existing `AllowUsers`/`AllowGroups`/`DenyUsers`/`DenyGroups` rules would block the account. It configures a root-owned forced-command gate, a narrow sudoers rule and an SSH `Match User` block. It requires `openssh-server`, `sudo`, and systemd.

## 2. APC UPS Bridge Pi

Run this after installing APC UPS Bridge, with the temporary Debian password ready:

```bash
curl -fsSLo /tmp/apc-ups-bridge-ssh.sh https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/tools/setup-bridge-ssh.sh
sudo bash /tmp/apc-ups-bridge-ssh.sh
```

The script prompts for the Debian hostname, account name and SSH port. It creates or reuses the service user's Ed25519 key, asks you to verify the Debian server's SSH host-key fingerprint against its console, then uses the temporary password through an interactive SSH prompt to enroll **only the public key**. The Debian host disables password authentication and sets the password field to `*` (no password login, but the account is not locked) as part of enrollment. Finally, the Pi checks key authentication **without issuing shutdown**.

Rerunning the Pi script is safe: if key login already works it skips enrollment.

In **Power rules & devices**, enter the SSH account and port you configured. The account accepts exactly two shutdown commands: `shutdown -h now` (leave **Use sudo -n** off) and `sudo -n shutdown -h now` (turn **Use sudo -n** on); either works. Use **Test SSH (no shutdown)** to confirm the login. Perform a real shutdown test only during a maintenance window.

**Online check:** the device's Online check only controls the status shown on the dashboard. **Ping** (the default) never touches SSH. **SSH port** connects and disconnects without logging in, which the host's SSH log may record as a connection closed before login. **Off** performs no check.

## Security and recovery

- The restricted account cannot obtain an interactive SSH shell, transfer files, or forward ports; arbitrary SSH commands are rejected.
- The SSH public key is stored in a root-owned authorized-keys file (mode 0644, so sshd can read it as the account) in a root-owned directory on Debian. The account cannot modify it.
- Enrollment is one-time. Re-enrollment/key rotation requires action by a Debian administrator.
- Keep the Pi's private key private. A compromised key could still shut down the Debian host.
- If the password-disable step completes but the Pi's test fails, recover using the Debian console or an independent administrator login. Review `journalctl -u ssh` and `/etc/ssh/sshd_config`; the Debian script makes timestamped backups.
- To revoke access, remove the user's root-owned key file under `/etc/ssh/apc-ups-bridge/authorized_keys/` on Debian, or remove the account entirely (below).

## Remove the account

On the Debian host, as root. Replace `<user>` with the account name.

```bash
# 1. Back up, then delete the block between the BEGIN/END markers:
#      # BEGIN APC UPS BRIDGE SHUTDOWN USER <user>
#      # END APC UPS BRIDGE SHUTDOWN USER <user>
cp -p /etc/ssh/sshd_config /etc/ssh/sshd_config.bak
nano /etc/ssh/sshd_config
sshd -t && systemctl try-reload-or-restart ssh

# 2. Remove the account and its files:
userdel -r <user>
rm -f /etc/sudoers.d/apc-ups-bridge-<user> /etc/ssh/apc-ups-bridge/authorized_keys/<user>
```

The shared helpers in `/usr/local/sbin/apc-ups-bridge-*` can stay; they are only usable through the sudoers rule above. The `/etc/ssh/sshd_config.apc-ups-bridge-backup-*` files can be deleted once you are satisfied.
