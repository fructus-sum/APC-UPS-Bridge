#!/usr/bin/env bash
# Run on the REMOTE Debian server as root. The initial password is temporary:
# setup-bridge-ssh.sh enrolls a key and then disables password authentication.
set -Eeuo pipefail
umask 077

fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
[[ ${EUID} -eq 0 ]] || fail 'Run this script with sudo on the Debian server.'
for bin in useradd passwd sudo visudo ssh-keygen awk getent systemctl; do
  command -v "$bin" >/dev/null 2>&1 || fail "Required command missing: $bin"
done
SSHD=$(command -v sshd || true)
[[ -n $SSHD ]] || SSHD=/usr/sbin/sshd
[[ -x $SSHD ]] || fail 'OpenSSH server is not installed (install openssh-server).'
[[ -f /etc/ssh/sshd_config ]] || fail '/etc/ssh/sshd_config is missing.'
[[ -x /usr/bin/sudo ]] || fail '/usr/bin/sudo is missing.'
SHUTDOWN=$(command -v shutdown || true)
[[ -n $SHUTDOWN && -x $SHUTDOWN ]] || fail 'shutdown command not found.'
[[ $SHUTDOWN == /* && -x $SHUTDOWN ]] || fail 'Invalid shutdown executable.'

printf '\nAPC UPS Bridge - Debian shutdown-only SSH account\n'
read -r -p 'New restricted SSH login name (e.g. upsbridge): ' USERNAME
[[ $USERNAME =~ ^[a-z_][a-z0-9_-]{0,30}$ ]] || fail 'Use a lowercase Linux username (letters, numbers, _ or -).'
[[ $USERNAME != root ]] || fail 'Refusing root.'
if getent passwd "$USERNAME" >/dev/null; then
  fail "Account $USERNAME already exists; refusing to modify an existing login."
fi

# Keep a real shell for sshd ForceCommand. /usr/sbin/nologin would block it.
useradd --create-home --user-group --shell /bin/sh "$USERNAME"
# Prevent the account from modifying its home or installing alternative SSH keys.
chown root:root "/home/$USERNAME"
chmod 0755 "/home/$USERNAME"
install -d -o root -g root -m 0755 /etc/ssh/apc-ups-bridge
install -d -o root -g root -m 0755 /etc/ssh/apc-ups-bridge/authorized_keys

# Root-owned forced-command gate: no shell, file transfer or arbitrary command.
cat > /usr/local/sbin/apc-ups-bridge-shutdown-only <<'WRAPPER'
#!/bin/sh
# SSH_ORIGINAL_COMMAND is untrusted. Accept only literal protocol commands.
case "${SSH_ORIGINAL_COMMAND-}" in
  'enroll-key')
    exec /usr/bin/sudo -n /usr/local/sbin/apc-ups-bridge-enroll-key ;;
  'sudo -n shutdown -h now')
    exec /usr/bin/sudo -n shutdown -h now ;;
  '')
    # The web UI's connection-only SSH test must never cause a shutdown.
    exit 0 ;;
  *)
    echo 'Denied: this SSH account only supports UPS shutdown.' >&2
    exit 126 ;;
esac
WRAPPER
chown root:root /usr/local/sbin/apc-ups-bridge-shutdown-only
chmod 0755 /usr/local/sbin/apc-ups-bridge-shutdown-only

# Only the forced-command wrapper can invoke this helper through its sudoers
# entry. It accepts one validated ed25519 PUBLIC key via stdin, never a command.
cat > /usr/local/sbin/apc-ups-bridge-enroll-key <<'ENROLL'
#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
fail() { printf 'Key enrollment failed: %s\n' "$*" >&2; exit 1; }
[[ $EUID -eq 0 ]] || fail 'root required'
USERNAME=${SUDO_USER:-}
[[ $USERNAME =~ ^[a-z_][a-z0-9_-]{0,30}$ && $USERNAME != root ]] || fail 'invalid sudo caller'
KEYDIR=/etc/ssh/apc-ups-bridge/authorized_keys
KEYFILE="$KEYDIR/$USERNAME"
[[ ! -e $KEYFILE ]] || fail 'a key is already enrolled; use the Debian console to rotate it'
IFS= read -r LINE || fail 'no public key received'
if IFS= read -r EXTRA || [[ -n ${EXTRA:-} ]]; then
  fail 'expected exactly one public-key line'
fi
read -r TYPE KEY_B64 COMMENT <<< "$LINE"
[[ $TYPE == ssh-ed25519 && ${KEY_B64:-} =~ ^[A-Za-z0-9+/]+={0,2}$ ]] || fail 'only a plain ed25519 public key is accepted'
printf '%s %s\n' "$TYPE" "$KEY_B64" | ssh-keygen -E sha256 -lf - >/dev/null || fail 'invalid public key'
install -d -o root -g root -m 0755 "$KEYDIR"
TMPKEY=$(mktemp "$KEYDIR/.key.XXXXXXXX")
trap 'rm -f "$TMPKEY"' EXIT
printf 'restrict %s %s\n' "$TYPE" "$KEY_B64" > "$TMPKEY"
chown root:root "$TMPKEY"
chmod 0600 "$TMPKEY"
mv "$TMPKEY" "$KEYFILE"

# Switch the account from temporary password bootstrap to publickey-only.
CONF=/etc/ssh/sshd_config
START="# BEGIN APC UPS BRIDGE SHUTDOWN USER $USERNAME"
END="# END APC UPS BRIDGE SHUTDOWN USER $USERNAME"
TMPCONF=$(mktemp /etc/ssh/.sshd-config-ups.XXXXXXXX)
trap 'rm -f "$TMPKEY" "$TMPCONF"' EXIT
awk -v start="$START" -v end="$END" '
  $0 == start { inside=1 }
  inside && $1 == "PasswordAuthentication" { print "    PasswordAuthentication no"; changed=1; next }
  { print }
  $0 == end { inside=0 }
  END { if (!changed) exit 1 }
' "$CONF" > "$TMPCONF" || fail 'cannot find the managed SSH account block'
chmod --reference="$CONF" "$TMPCONF"
chown --reference="$CONF" "$TMPCONF"
/usr/sbin/sshd -t -f "$TMPCONF" || fail 'updated sshd configuration did not validate'
EFFECTIVE=$(/usr/sbin/sshd -T -f "$TMPCONF" -C "user=$USERNAME,host=localhost,addr=127.0.0.1") || fail 'cannot inspect effective SSH configuration'
printf '%s\n' "$EFFECTIVE" | grep -qx 'passwordauthentication no' || fail 'SSH password login was not disabled'
printf '%s\n' "$EFFECTIVE" | grep -qx 'forcecommand /usr/local/sbin/apc-ups-bridge-shutdown-only' || fail 'SSH forced command not effective'
printf '%s\n' "$EFFECTIVE" | grep -qx 'authorizedkeysfile /etc/ssh/apc-ups-bridge/authorized_keys/%u' || fail 'root-owned key location not effective'
# Install the validated configuration, lock the password, then reload sshd.
cp -p "$CONF" "$CONF.apc-ups-bridge-backup-$(date -u +%Y%m%dT%H%M%SZ)-$$"
mv -f "$TMPCONF" "$CONF"
passwd -l "$USERNAME" >/dev/null || fail 'could not lock bootstrap password; check server console'
if ! systemctl reload ssh; then
  fail 'sshd reload failed; check the Debian console (key installed and password locked)'
fi
printf '\nPublic key enrolled for %s. Password login is disabled.\n' "$USERNAME"
ENROLL
chown root:root /usr/local/sbin/apc-ups-bridge-enroll-key
chmod 0755 /usr/local/sbin/apc-ups-bridge-enroll-key

# Only one exact sudo command plus a no-arguments one-time key-enrollment helper.
SUDOERS="/etc/sudoers.d/apc-ups-bridge-$USERNAME"
printf '%s ALL=(root) NOPASSWD: %s -h now, /usr/local/sbin/apc-ups-bridge-enroll-key ""\n' \
  "$USERNAME" "$SHUTDOWN" > "$SUDOERS"
chown root:root "$SUDOERS"
chmod 0440 "$SUDOERS"
visudo -cf "$SUDOERS" >/dev/null || fail 'invalid restricted sudoers rule'

CONF=/etc/ssh/sshd_config
BACKUP="$CONF.apc-ups-bridge-backup-$(date -u +%Y%m%dT%H%M%SZ)-$$"
cp -p "$CONF" "$BACKUP"
TMPCONF=$(mktemp /etc/ssh/.sshd-config-ups.XXXXXXXX)
trap 'rm -f "$TMPCONF"' EXIT
cp -p "$CONF" "$TMPCONF"
cat >> "$TMPCONF" <<MATCH

# BEGIN APC UPS BRIDGE SHUTDOWN USER $USERNAME
Match User $USERNAME
    PasswordAuthentication yes
    KbdInteractiveAuthentication no
    PubkeyAuthentication yes
    AuthorizedKeysFile /etc/ssh/apc-ups-bridge/authorized_keys/%u
    ForceCommand /usr/local/sbin/apc-ups-bridge-shutdown-only
    DisableForwarding yes
    PermitTTY no
    PermitUserRC no
# END APC UPS BRIDGE SHUTDOWN USER $USERNAME
MATCH
if ! "$SSHD" -t -f "$TMPCONF"; then
  fail 'sshd configuration validation failed; original configuration unchanged.'
fi
EFFECTIVE=$("$SSHD" -T -f "$TMPCONF" -C "user=$USERNAME,host=localhost,addr=127.0.0.1") || {
  fail 'could not inspect effective SSH settings';
}
for EXPECTED in \
  'forcecommand /usr/local/sbin/apc-ups-bridge-shutdown-only' \
  'passwordauthentication yes' \
  'kbdinteractiveauthentication no' \
  'disableforwarding yes' \
  'permittty no' \
  'authorizedkeysfile /etc/ssh/apc-ups-bridge/authorized_keys/%u' \
  'authenticationmethods any'; do
  if ! printf '%s\n' "$EFFECTIVE" | grep -qx "$EXPECTED"; then
    fail "An SSH rule overrides '$EXPECTED'; original configuration unchanged."
  fi
done
mv -f "$TMPCONF" "$CONF"
if ! systemctl reload ssh; then
  cp -p "$BACKUP" "$CONF"
  systemctl reload ssh || true
  fail 'Could not reload sshd; restored previous configuration.'
fi

printf '\nSet a TEMPORARY password for %s (use a long unique password).\n' "$USERNAME"
printf 'This password will be disabled automatically after the Pi enrolls its SSH key.\n'
passwd "$USERNAME" || fail 'Password setup failed; account remains password-locked until corrected.'
printf '\nDebian side ready. Run tools/setup-bridge-ssh.sh on the APC UPS Bridge Pi next.\n'
printf 'Remote SSH username: %s\n' "$USERNAME"
printf 'The account has no interactive shell and can only enroll a key or shut down.\n'
