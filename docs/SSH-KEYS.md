# SSH keys for remote shutdown

The bridge shuts down remote hosts over SSH, logging in with a public key. This guide shows how to find the bridge's key, authorize it on a remote host, and test the result. The same public key and a ready-made `authorized_keys` line are shown on the **Power rules & devices** page under "SSH key for remote hosts".

## Requirements

- The remote host runs an SSH server with public-key authentication enabled.
- The bridge's default shutdown command is `shutdown -h now`, or `sudo -n shutdown -h now` when the device has "Use sudo -n" set. A host that needs a different command should use `command=` in its authorized key line (see step 3).
- Run the bridge-side commands below on the bridge, as an administrator.

## 1) Create or check the bridge key (on the bridge)

The bridge runs as the system user `apc-ups-bridge`, whose home is `/var/lib/apc-ups-bridge`, so its SSH identity lives in `/var/lib/apc-ups-bridge/.ssh`.

```bash
sudo ls -la /var/lib/apc-ups-bridge/.ssh/
```

If there is no `id_ed25519` (or other key pair), create one:

```bash
sudo install -d -m 700 -o apc-ups-bridge -g apc-ups-bridge /var/lib/apc-ups-bridge/.ssh
sudo -u apc-ups-bridge ssh-keygen -t ed25519 -N '' -f /var/lib/apc-ups-bridge/.ssh/id_ed25519
```

Show the public key (it is also displayed on the Power rules page):

```bash
sudo cat /var/lib/apc-ups-bridge/.ssh/id_ed25519.pub
```

Only share the `.pub` file. Never copy the private key.

## 2) Choose the remote account

Pick the account the bridge logs in as on the remote host. Prefer a dedicated account that is allowed only to shut the host down; enable "Use sudo -n" on the device if that account needs sudo for it. Use an administrator account only if the host has nothing else.

## 3) Authorize the key on the host

Add one line to that account's authorized keys on the host. The recommended, restricted form is:

```
from="<bridge-ip>",command="<shutdown command for that host>",restrict ssh-ed25519 AAAA... bridge
```

Replace `<bridge-ip>` with the bridge's address as the host sees it, and paste the real public key after `restrict`.

- `from=` limits use of the key to the bridge's address.
- `command=` makes the key able to do nothing but run that host's own shutdown command, whatever the bridge sends.
- `restrict` disables terminals and forwarding. Very old SSH servers without `restrict` can use `no-pty,no-port-forwarding,no-agent-forwarding,no-X11-forwarding` instead.

Examples of shutdown commands (check them against the host's documentation):

- `shutdown -h now` on many Unix-like systems.
- `shutdown /s /t 0` on Windows with its OpenSSH server.

The bridge treats the connection dropping during shutdown as success.

**Warning:** once `command=` is set, ANY normal SSH login with that key runs the shutdown. Manual tests must use `-N` as shown in step 5.

Where the key goes depends on the host:

- Usually it is `.ssh/authorized_keys` in that account's home directory.
- Some systems use a different file, or manage keys in their own settings page.
- Some systems keep home directories in memory, so manual edits disappear after a reboot. Use the host's persistent method and confirm the key survives a restart.
- SSH servers usually require the key file and its folder to be writable only by that account.

## 4) Trust the host key (on the bridge)

The bridge uses `StrictHostKeyChecking=yes`, so the host must already be in the service user's `known_hosts`:

```bash
sudo -u apc-ups-bridge ssh -o StrictHostKeyChecking=ask -N -p <port> <user>@<host>
```

Compare the fingerprint shown with the one the host's administrator shows for its SSH host key. Answer `yes` only if they match, then press Ctrl+C.

## 5) Test (on the bridge)

```bash
sudo -u apc-ups-bridge ssh -v -N -T -o BatchMode=yes -o StrictHostKeyChecking=yes -o PreferredAuthentications=publickey -p <port> <user>@<host>
```

**Always use `-N`. Without it, a login with a `command=` key runs the shutdown command.**

Success shows "Authenticated to"; press Ctrl+C.

You can also use **Test SSH (no shutdown)** on the Power rules page. It checks login only, never runs the command, and does not prove shutdown permission. Test a real shutdown once, at a safe time, on equipment where an outage is acceptable.

## Troubleshooting

| Message | Likely cause |
| --- | --- |
| Host key verification failed | The host is not in the service user's `known_hosts`, or its key changed. Do step 4. |
| Permission denied (publickey...) | The key is missing for that account, the wrong account is configured, the `from=` address does not match the bridge, file permissions are too open, or the host does not allow that account to log in with keys (check its SSH server settings). |
| Connection refused / timed out | Check the host, the SSH port and any firewall between the bridge and the host. |
| identity file ... not accessible / no key | The service user has no key. Do step 1. |

If the host's SSH server logs rejected keys, compare the fingerprint shown on the Power rules page with the one in that log.

## Security notes

- The key gives whatever access the chosen account has, unless `command=` restricts it. Prefer the restricted form.
- To revoke access, remove the key's line from the host.
