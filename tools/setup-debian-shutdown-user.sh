#!/usr/bin/env bash
# Run on the REMOTE Debian server as root. The initial password is temporary:
# setup-bridge-ssh.sh enrolls a key and then disables password authentication.
set -Eeuo pipefail
umask 077

fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
[[ ${EUID} -eq 0 ]] || fail 'Run this script with sudo on the Debian server.'
for bin in useradd userdel usermod passwd sudo visudo ssh-keygen awk getent systemctl mktemp sed flock; do
  command -v "$bin" >/dev/null 2>&1 || fail "Required command missing: $bin"
done
SSHD=$(command -v sshd || true)
[[ -n $SSHD ]] || SSHD=/usr/sbin/sshd
[[ -x $SSHD ]] || fail 'OpenSSH server is not installed (install openssh-server).'
[[ -f /etc/ssh/sshd_config ]] || fail '/etc/ssh/sshd_config is missing.'
[[ -x /usr/bin/sudo ]] || fail '/usr/bin/sudo is missing.'
SHUTDOWN=$(command -v shutdown || true)
[[ -n $SHUTDOWN && -x $SHUTDOWN ]] || fail 'shutdown command not found.'
# This path is embedded in the wrapper and the sudoers rule, so keep it to a
# conservative character set (no spaces, quotes, commas, sed or sudoers specials).
[[ $SHUTDOWN =~ ^/[A-Za-z0-9._/+-]+$ ]] || fail 'Invalid shutdown executable path.'

printf '\nAPC UPS Bridge - Debian shutdown-only SSH account\n'
read -r -p 'New restricted SSH login name (e.g. upsbridge): ' USERNAME
[[ $USERNAME =~ ^[a-z_][a-z0-9_-]{0,30}$ ]] || fail 'Use a lowercase Linux username (letters, numbers, _ or -).'
[[ $USERNAME != root ]] || fail 'Refusing root.'
if getent passwd "$USERNAME" >/dev/null; then
  fail "Account $USERNAME already exists; refusing to modify an existing login."
fi
if getent group "$USERNAME" >/dev/null; then
  fail "A group named $USERNAME already exists; refusing to reuse it."
fi

SUDOERS="/etc/sudoers.d/apc-ups-bridge-$USERNAME"
CONF=/etc/ssh/sshd_config
START="# BEGIN APC UPS BRIDGE SHUTDOWN USER $USERNAME"
# Leftovers from an earlier run for this name must be removed by an administrator.
[[ ! -e $SUDOERS ]] || fail "$SUDOERS already exists; remove it first."
[[ ! -e /etc/ssh/apc-ups-bridge/authorized_keys/$USERNAME ]] || fail "A key file for $USERNAME already exists; remove it first."
if grep -qxF "$START" "$CONF"; then
  fail "$CONF already has a managed block for $USERNAME; remove it first."
fi

# Decide the home path up front. The home is created by this run (root-owned and
# empty, no skeleton files), so rolling back with `userdel -r` is safe.
HOME_BASE=$(useradd -D | sed -n 's/^HOME=//p')
HOME_BASE=${HOME_BASE:-/home}
HOME_BASE=${HOME_BASE%/}
[[ $HOME_BASE =~ ^/[A-Za-z0-9._/+-]+$ ]] || fail 'Unsupported default home directory base from useradd -D.'
HOME_DIR="$HOME_BASE/$USERNAME"
[[ ! -e $HOME_DIR && ! -L $HOME_DIR ]] || fail "$HOME_DIR already exists; remove it first."
[[ ! -e /var/mail/$USERNAME ]] || fail "/var/mail/$USERNAME already exists; remove it first."

# Existing global rules (AllowUsers, AllowGroups, DenyUsers, DenyGroups) can lock
# the new account out regardless of its Match block. Reads `sshd -T` output on
# stdin; prints "BLOCK: ..." / "WARN: ..." lines. Only exact names are compared;
# patterns cannot be evaluated here, so they produce a warning instead.
# The account's primary group has the same name (useradd --user-group).
access_report() {
  awk -v u="$USERNAME" -v g="$USERNAME" '
    function scan(who, n,   i) {
      for (i = 2; i <= NF; i++) {
        if ($i == who) hit[n] = 1
        else if ($i ~ /[*?!@]/) pat[n] = 1
      }
      seen[n] = 1
    }
    $1 == "allowusers"  { scan(u, "au") }
    $1 == "allowgroups" { scan(g, "ag") }
    $1 == "denyusers"   { scan(u, "du") }
    $1 == "denygroups"  { scan(g, "dg") }
    END {
      if (seen["au"] && !hit["au"]) {
        if (pat["au"]) print "WARN: AllowUsers contains patterns that could not be evaluated; check that " u " is allowed."
        else print "BLOCK: AllowUsers is set and does not list " u ". Add " u " to AllowUsers (or adjust the rule), then rerun."
      }
      if (seen["ag"] && !hit["ag"]) {
        if (pat["ag"]) print "WARN: AllowGroups contains patterns that could not be evaluated; check that group " g " is allowed."
        else print "BLOCK: AllowGroups is set and does not list group " g ". Add it to AllowGroups (or adjust the rule), then rerun."
      }
      if (hit["du"]) print "BLOCK: DenyUsers lists " u ". Adjust the rule, then rerun."
      else if (pat["du"]) print "WARN: DenyUsers contains patterns that could not be evaluated; check that " u " is not denied."
      if (hit["dg"]) print "BLOCK: DenyGroups lists group " g ". Adjust the rule, then rerun."
      else if (pat["dg"]) print "WARN: DenyGroups contains patterns that could not be evaluated; check that group " g " is not denied."
    }'
}
# Print warnings; fail on any BLOCK line. Argument: sshd -T output.
check_access() {
  local report line blocked=0
  report=$(printf '%s\n' "$1" | access_report)
  while IFS= read -r line; do
    [[ -n $line ]] || continue
    case $line in
      BLOCK:*) blocked=1; printf 'Error: %s\n' "${line#BLOCK: }" >&2 ;;
      *) printf '%s\n' "$line" >&2 ;;
    esac
  done <<< "$report"
  [[ $blocked -eq 0 ]] || return 1
}

# Do this before creating anything, so a blocked setup leaves nothing behind.
# Some sshd versions cannot evaluate a user that does not exist yet; then this
# early check is skipped and the same check after account creation (inside the
# rollback window) still decides.
if PRE_EFFECTIVE=$("$SSHD" -T -f "$CONF" -C "user=$USERNAME,host=localhost,addr=127.0.0.1" 2>/dev/null); then
  check_access "$PRE_EFFECTIVE" || fail "Existing SSH access rules would block $USERNAME; nothing was changed."
else
  printf 'Note: SSH access rules will be checked after the account is created.\n'
fi

# Rollback: remember what this run created so an early failure leaves nothing
# behind. Once the new sshd config is installed and reloaded (CONF_DONE=1) the
# setup is considered complete and nothing is rolled back. The shared wrapper and
# enroll helper in /usr/local/sbin are harmless and may serve other accounts.
USER_ATTEMPTED=0
SUDOERS_TMP=
SUDOERS_INSTALLED=0
TMPCONF=
CONF_DONE=0
cleanup() {
  local rc=$?
  trap - EXIT
  [[ -z $TMPCONF ]] || rm -f "$TMPCONF"
  if [[ $CONF_DONE -eq 0 ]]; then
    [[ -z $SUDOERS_TMP ]] || rm -f "$SUDOERS_TMP"
    [[ $SUDOERS_INSTALLED -eq 0 ]] || rm -f "$SUDOERS"
    # useradd may have created the account and still returned nonzero.
    if [[ $USER_ATTEMPTED -eq 1 ]] && getent passwd "$USERNAME" >/dev/null 2>&1; then
      printf 'Setup failed; removing the account %s created by this run.\n' "$USERNAME" >&2
      userdel -r "$USERNAME" >/dev/null 2>&1 || true
    fi
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

# Keep a real shell for sshd ForceCommand. /usr/sbin/nologin would block it.
# No home is created by useradd (no skeleton files); a root-owned empty one is
# installed instead, so the account cannot modify its home or add SSH keys.
USER_ATTEMPTED=1
useradd --no-create-home -d "$HOME_DIR" --user-group --shell /bin/sh "$USERNAME"
[[ $(getent passwd "$USERNAME" | cut -d: -f6) == "$HOME_DIR" ]] || fail "Unexpected home directory for $USERNAME."
install -d -o root -g root -m 0755 "$HOME_DIR"
install -d -o root -g root -m 0755 /etc/ssh/apc-ups-bridge
install -d -o root -g root -m 0755 /etc/ssh/apc-ups-bridge/authorized_keys

# Root-owned forced-command gate: no shell, file transfer or arbitrary command.
# The shutdown path below is a placeholder replaced with the path validated above
# (a safe character set, so it cannot break the sed expression or the quoting).
GATE=/usr/local/sbin/apc-ups-bridge-shutdown-only
cat > "$GATE" <<'WRAPPER'
#!/bin/sh
# SSH_ORIGINAL_COMMAND is untrusted. Accept only literal protocol commands.
case "${SSH_ORIGINAL_COMMAND-}" in
  'enroll-key')
    exec /usr/bin/sudo -n /usr/local/sbin/apc-ups-bridge-enroll-key ;;
  'shutdown -h now'|'sudo -n shutdown -h now')
    # Same command whether or not the device has "Use sudo -n" ticked. The
    # absolute path matches the sudoers rule exactly.
    exec /usr/bin/sudo -n '@SHUTDOWN@' -h now ;;
  '')
    # The web UI's connection-only SSH test must never cause a shutdown.
    exit 0 ;;
  *)
    echo 'Denied: this SSH account only supports UPS shutdown.' >&2
    exit 126 ;;
esac
WRAPPER
sed -i "s|@SHUTDOWN@|$SHUTDOWN|" "$GATE"
grep -qF "exec /usr/bin/sudo -n '$SHUTDOWN' -h now" "$GATE" || fail 'could not embed the shutdown path in the wrapper'
chown root:root "$GATE"
chmod 0755 "$GATE"

# Only the forced-command wrapper can invoke this helper through its sudoers
# entry. It accepts one validated ed25519 PUBLIC key via stdin, never a command.
# A failed enrollment is rolled back (key removed, config and password restored)
# so it can be retried with the same temporary password.
cat > /usr/local/sbin/apc-ups-bridge-enroll-key <<'ENROLL'
#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
fail() { printf 'Key enrollment failed: %s\n' "$*" >&2; exit 1; }
[[ $EUID -eq 0 ]] || fail 'root required'
USERNAME=${SUDO_USER:-}
[[ $USERNAME =~ ^[a-z_][a-z0-9_-]{0,30}$ && $USERNAME != root ]] || fail 'invalid sudo caller'
# One enrollment at a time.
exec 9>/run/apc-ups-bridge-enroll.lock
flock -n 9 || fail 'another enrollment is in progress'
SSHD=$(command -v sshd || true)
[[ -n $SSHD ]] || SSHD=/usr/sbin/sshd
[[ -x $SSHD ]] || fail 'sshd not found'
CONF=/etc/ssh/sshd_config
KEYDIR=/etc/ssh/apc-ups-bridge/authorized_keys
KEYFILE="$KEYDIR/$USERNAME"
[[ ! -e $KEYFILE ]] || fail 'a key is already enrolled; use the Debian console to rotate it'

# Rollback state. Nothing is undone once DONE=1 (after a successful reload).
DONE=0
TMPKEY=
TMPCONF=
KEY_MOVED=0
BACKUP=
CONF_MOVED=0
OLD_HASH=
PW_CHANGED=0
cleanup() {
  local rc=$?
  trap - EXIT
  [[ -z $TMPKEY ]] || rm -f "$TMPKEY"
  [[ -z $TMPCONF ]] || rm -f "$TMPCONF"
  if [[ $DONE -eq 0 ]]; then
    if [[ $CONF_MOVED -eq 1 ]]; then cp -p "$BACKUP" "$CONF" || true; fi
    if [[ $PW_CHANGED -eq 1 ]]; then usermod -p "$OLD_HASH" "$USERNAME" || true; fi
    if [[ $KEY_MOVED -eq 1 ]]; then rm -f "$KEYFILE"; fi
    if [[ $CONF_MOVED -eq 1 ]]; then systemctl try-reload-or-restart ssh || true; fi
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

IFS= read -r -t 30 LINE || fail 'no public key received'
# A client that closed stdin normally gives EOF here; a stalled one times out,
# which is treated as "no extra line".
EXTRA=
RC=0
IFS= read -r -t 5 EXTRA || RC=$?
if [[ $RC -eq 0 ]] || { [[ $RC -le 128 ]] && [[ -n $EXTRA ]]; }; then
  fail 'expected exactly one public-key line'
fi
read -r TYPE KEY_B64 COMMENT <<< "$LINE"
[[ $TYPE == ssh-ed25519 && ${KEY_B64:-} =~ ^[A-Za-z0-9+/]+={0,2}$ ]] || fail 'only a plain ed25519 public key is accepted'
printf '%s %s\n' "$TYPE" "$KEY_B64" | ssh-keygen -E sha256 -lf - >/dev/null || fail 'invalid public key'

# 1. Stage the key.
install -d -o root -g root -m 0755 "$KEYDIR"
TMPKEY=$(mktemp "$KEYDIR/.key.XXXXXXXX")
printf 'restrict %s %s\n' "$TYPE" "$KEY_B64" > "$TMPKEY"
# sshd reads AuthorizedKeysFile with the TARGET user's privileges, so the file
# must be readable by that user (0644). It stays root-owned and not group- or
# world-writable (so StrictModes accepts it) inside a root-owned 0755 directory,
# which means the account itself still cannot change its own key.
chown root:root "$TMPKEY"
chmod 0644 "$TMPKEY"

# 2. Build and validate the publickey-only configuration before changing anything.
START="# BEGIN APC UPS BRIDGE SHUTDOWN USER $USERNAME"
END="# END APC UPS BRIDGE SHUTDOWN USER $USERNAME"
TMPCONF=$(mktemp /etc/ssh/.sshd-config-ups.XXXXXXXX)
awk -v start="$START" -v end="$END" '
  $0 == start { inside=1 }
  inside && $1 == "PasswordAuthentication" { print "    PasswordAuthentication no"; changed=1; next }
  { print }
  $0 == end { inside=0 }
  END { if (!changed) exit 1 }
' "$CONF" > "$TMPCONF" || fail 'cannot find the managed SSH account block'
chmod --reference="$CONF" "$TMPCONF"
chown --reference="$CONF" "$TMPCONF"
"$SSHD" -t -f "$TMPCONF" || fail 'updated sshd configuration did not validate'
EFFECTIVE=$("$SSHD" -T -f "$TMPCONF" -C "user=$USERNAME,host=localhost,addr=127.0.0.1") || fail 'cannot inspect effective SSH configuration'
printf '%s\n' "$EFFECTIVE" | grep -qx 'passwordauthentication no' || fail 'SSH password login was not disabled'
printf '%s\n' "$EFFECTIVE" | grep -qx 'forcecommand /usr/local/sbin/apc-ups-bridge-shutdown-only' || fail 'SSH forced command not effective'
printf '%s\n' "$EFFECTIVE" | grep -qx 'authorizedkeysfile /etc/ssh/apc-ups-bridge/authorized_keys/%u' || fail 'root-owned key location not effective'

# 3. Install the key, then the configuration (after taking a backup).
mv "$TMPKEY" "$KEYFILE"
KEY_MOVED=1
TMPKEY=
BACKUP="$CONF.apc-ups-bridge-backup-$(date -u +%Y%m%dT%H%M%SZ)-$$"
cp -p "$CONF" "$BACKUP"
mv -f "$TMPCONF" "$CONF"
TMPCONF=
CONF_MOVED=1

# 4. Disable the bootstrap password. Use '*' rather than 'passwd -l': a
# '!'-prefixed hash makes sshd refuse even key logins when UsePAM is no, while
# '*' only disables password login. The old hash is kept for rollback.
OLD_HASH=$(getent shadow "$USERNAME" | cut -d: -f2)
PW_CHANGED=1
usermod -p '*' "$USERNAME" || fail 'could not disable bootstrap password'
[[ $(getent shadow "$USERNAME" | cut -d: -f2) == '*' ]] || fail 'bootstrap password was not disabled'

# 5. try-reload-or-restart reloads a running sshd and is a harmless no-op when it
# is socket-activated or inactive.
systemctl try-reload-or-restart ssh || fail 'sshd reload failed; enrollment was rolled back'
DONE=1
printf '\nPublic key enrolled for %s. Password login is disabled.\n' "$USERNAME"
ENROLL
chown root:root /usr/local/sbin/apc-ups-bridge-enroll-key
chmod 0755 /usr/local/sbin/apc-ups-bridge-enroll-key

# Only one exact sudo command plus a no-arguments one-time key-enrollment helper.
# sudo ignores files in /etc/sudoers.d whose name contains a dot, so the rule is
# written to a dotted temp name, validated, and only then renamed into place: an
# invalid file is never visible to sudo.
SUDOERS_TMP=$(mktemp /etc/sudoers.d/.apc-ups-bridge.XXXXXX)
printf '%s ALL=(root) NOPASSWD: %s -h now, /usr/local/sbin/apc-ups-bridge-enroll-key ""\n' \
  "$USERNAME" "$SHUTDOWN" > "$SUDOERS_TMP"
chown root:root "$SUDOERS_TMP"
chmod 0440 "$SUDOERS_TMP"
visudo -cf "$SUDOERS_TMP" >/dev/null || fail 'invalid restricted sudoers rule'
mv "$SUDOERS_TMP" "$SUDOERS"
SUDOERS_TMP=
SUDOERS_INSTALLED=1

BACKUP="$CONF.apc-ups-bridge-backup-$(date -u +%Y%m%dT%H%M%SZ)-$$"
cp -p "$CONF" "$BACKUP"
TMPCONF=$(mktemp /etc/ssh/.sshd-config-ups.XXXXXXXX)
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
  'pubkeyauthentication yes' \
  'kbdinteractiveauthentication no' \
  'disableforwarding yes' \
  'permittty no' \
  'permituserrc no' \
  'authorizedkeysfile /etc/ssh/apc-ups-bridge/authorized_keys/%u' \
  'authenticationmethods any'; do
  if ! printf '%s\n' "$EFFECTIVE" | grep -qx "$EXPECTED"; then
    fail "An SSH rule overrides '$EXPECTED'; original configuration unchanged."
  fi
done
# Also re-check the global access rules on the final configuration.
check_access "$EFFECTIVE" || fail "Existing SSH access rules would block $USERNAME; original configuration unchanged."
mv -f "$TMPCONF" "$CONF"
TMPCONF=
if ! systemctl try-reload-or-restart ssh; then
  cp -p "$BACKUP" "$CONF"
  systemctl try-reload-or-restart ssh || true
  fail 'Could not reload sshd; restored previous configuration.'
fi
# The new configuration is live: from here on, no rollback.
CONF_DONE=1

printf '\nSet a TEMPORARY password for %s (use a long unique password).\n' "$USERNAME"
printf 'This password will be disabled automatically after the Pi enrolls its SSH key.\n'
passwd "$USERNAME" || fail 'Password setup failed; account remains password-locked until corrected.'
printf '\nDebian side ready. Run tools/setup-bridge-ssh.sh on the APC UPS Bridge Pi next.\n'
printf 'Remote SSH username: %s\n' "$USERNAME"
printf 'The account cannot open a terminal or shell: a root-owned gate allows only key enrollment or shutdown.\n'
