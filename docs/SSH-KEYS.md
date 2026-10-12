# SSH keys for remote shutdown

The bridge shuts down other computers over SSH, signing in with a key instead of a password. This guide shows how to find the bridge's key, allow it on a remote computer, and test it. The **Power rules & devices** page shows the same public key and a ready-made line under "SSH key for remote hosts".

On Debian hosts, the scripts in `tools/` can create a shutdown-only account and add the key for you. See [tools/README.md](../tools/README.md).

## What you need

- The remote computer runs an SSH server that allows key sign-in.
- The bridge runs `shutdown -h now`, or `sudo -n shutdown -h now` when the device has **Use sudo -n** ticked. A host that needs a different command can set it with `command=` (step 3).
- Run the commands marked "on the bridge" on the Pi, as an administrator.

## Words used below

- **Authorized keys** is the list of keys a remote account accepts.
- **known_hosts** is the list of computers the bridge has agreed to trust. The bridge refuses to connect to a computer that is not in its list, which protects against impostors.
- **from=**, **command=** and **restrict** are options you put in front of a key in the authorized keys list. `from=` limits the key to the bridge's address. `command=` makes the key able to run only that one command, whatever the bridge sends. `restrict` turns off terminals and forwarding. Very old SSH servers without `restrict` can use `no-pty,no-port-forwarding,no-agent-forwarding,no-X11-forwarding`.

## 1) Create or check the bridge key (on the bridge)

The bridge runs as the user `apc-ups-bridge`, whose key lives in `/var/lib/apc-ups-bridge/.ssh`.

```bash
sudo ls -la /var/lib/apc-ups-bridge/.ssh/
```

If there is no `id_ed25519`, create one:

```bash
sudo install -d -m 700 -o apc-ups-bridge -g apc-ups-bridge /var/lib/apc-ups-bridge/.ssh
sudo -u apc-ups-bridge ssh-keygen -t ed25519 -N '' -f /var/lib/apc-ups-bridge/.ssh/id_ed25519
```

Show the public key (the Power page shows it too):

```bash
sudo cat /var/lib/apc-ups-bridge/.ssh/id_ed25519.pub
```

Share only the `.pub` file. Never copy the private key.

## 2) Choose the remote account

Pick the account the bridge signs in as. Prefer a dedicated account that may only shut the computer down, and tick **Use sudo -n** on the device if it needs sudo for that. Use an administrator account only if the computer has nothing else.

## 3) Allow the key on the remote computer

Add one line to that account's authorized keys:

```
from="<bridge-ip>",command="<shutdown command for that host>",restrict ssh-ed25519 AAAA... bridge
```

Replace `<bridge-ip>` with the bridge's address as the computer sees it, and paste the real public key after `restrict`. Example shutdown commands (check your computer's documentation):

- `shutdown -h now` on many Unix-like systems.
- `shutdown /s /t 0` on Windows with its OpenSSH server.

The bridge counts the connection dropping during shutdown as success.

**Warning:** once `command=` is set, ANY normal sign-in with that key runs the shutdown. Manual tests must use `-N` as in step 5.

Where the line goes depends on the computer:

- Usually `.ssh/authorized_keys` in the account's home folder.
- Some systems use another file or manage keys in their own settings page. For example, some SSH servers read administrator keys from a separate system-wide file.
- Some systems keep home folders in memory, so manual edits vanish on reboot. Use the computer's lasting method and check the key survives a restart.
- SSH servers usually require the key file and its folder to be writable only by that account.

## 4) Trust the computer (on the bridge)

```bash
sudo -u apc-ups-bridge ssh -o StrictHostKeyChecking=ask -N -p <port> <user>@<host>
```

Compare the fingerprint shown with the one the computer's administrator gives you. Answer `yes` only if they match, then press Ctrl+C.

## 5) Test (on the bridge)

```bash
sudo -u apc-ups-bridge ssh -v -N -T -o BatchMode=yes -o StrictHostKeyChecking=yes -o PreferredAuthentications=publickey -p <port> <user>@<host>
```

**Always use `-N`. Without it, a sign-in with a `command=` key runs the shutdown.** Success shows "Authenticated to". Press Ctrl+C.

You can also select **Test SSH (no shutdown)** on the device in the Power page. It tests the saved device, can take up to about 8 seconds, only checks the sign-in, never runs the command, and does not prove shutdown permission. Test a real shutdown once, at a safe time, on equipment where an outage is acceptable.

## Troubleshooting

| Message | Likely cause |
| --- | --- |
| Host key verification failed | The computer is not in the service user's `known_hosts`, or its key changed. Do step 4. |
| Permission denied (publickey...) | The key is missing for that account, the wrong account is set, the `from=` address does not match the bridge, file permissions are too open, or the computer does not allow key sign-in for that account. |
| Connection refused / timed out | Check the computer, the SSH port and any firewall between them. |
| identity file ... not accessible / no key | The service user has no key. Do step 1. |

If the computer's SSH server logs rejected keys, compare the fingerprint on the Power page with the one in that log.

## Security notes

- The key gives whatever access the chosen account has, unless `command=` restricts it. Prefer the restricted form.
- To remove access, delete the key's line from the computer.
