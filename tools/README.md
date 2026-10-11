# Debian shutdown-only SSH setup tools

These tools provision a restricted account on a **remote Debian host** and connect it to APC UPS Bridge using a one-time password login to install an SSH public key. The password is then locked, and the account accepts only the Bridge's shutdown command (`sudo -n shutdown -h now`) or a connection-only SSH test.

**Review both scripts before running them as root.** Keep an independent administrator SSH session open, and test first on a noncritical Debian machine. The account can still be used to shut down the host if its SSH key is stolen; firewall SSH access to the Pi's IP where possible.

## 1. Remote Debian server

```bash
curl -fsSLo /tmp/apc-ups-debian-user.sh https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/tools/setup-debian-shutdown-user.sh
sudo bash /tmp/apc-ups-debian-user.sh
```

The script asks for a new account name and a **temporary** password. It configures a root-owned forced-command gate, a narrow sudoers rule and an SSH `Match User` block. It requires `openssh-server`, `sudo`, and systemd.

## 2. APC UPS Bridge Pi

Run this after installing APC UPS Bridge, with the temporary Debian password ready:

```bash
curl -fsSLo /tmp/apc-ups-bridge-ssh.sh https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/tools/setup-bridge-ssh.sh
sudo bash /tmp/apc-ups-bridge-ssh.sh
```

The script prompts for the Debian hostname, account name and SSH port. It creates or reuses the service user's Ed25519 key, asks you to verify the Debian server's SSH host-key fingerprint against its console, then uses the temporary password through an interactive SSH prompt to enroll **only the public key**. The Debian host disables password authentication and locks the password as part of enrollment. Finally, the Pi checks key authentication **without issuing shutdown**.

In **Power rules & devices**, use the configured SSH account and port and enable **Use sudo -n**. Use **Test SSH (no shutdown)**. Perform a real shutdown test only during a maintenance window.

## Security and recovery

- The restricted account cannot obtain an interactive SSH shell, transfer files, or forward ports; arbitrary SSH commands are rejected.
- The SSH public key is stored in a root-owned authorized-keys file on Debian. The account cannot modify it.
- Enrollment is one-time. Re-enrollment/key rotation requires action by a Debian administrator.
- Keep the Pi's private key private. A compromised key could still shut down the Debian host.
- If the password-lock step completes but the Pi's test fails, recover using the Debian console or an independent administrator login. Review `journalctl -u ssh` and `/etc/ssh/sshd_config`; the Debian script makes timestamped backups.
- To revoke access, remove the user's root-owned key file under `/etc/ssh/apc-ups-bridge/authorized_keys/` on Debian.
- Anonymous raw GitHub URLs require a public repository. For a private repository, download through your authenticated GitHub session and run the scripts from the checkout.

The scripts are syntax checked but have not been end-to-end tested on real Debian/Pi machines.
