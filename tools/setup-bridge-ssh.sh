#!/usr/bin/env bash
# Run on the APC UPS Bridge Pi as root, AFTER tools/setup-debian-shutdown-user.sh
# has prepared the remote Debian host. Enrolls the bridge service user's ed25519
# public key on that host (using the account's temporary password once), then
# checks key login without causing a shutdown.
set -Eeuo pipefail
umask 077

SERVICE_USER=apc-ups-bridge

fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
[[ ${EUID} -eq 0 ]] || fail 'Run this script with sudo on the APC UPS Bridge Pi.'

PASSWD_ENTRY=$(getent passwd "$SERVICE_USER" || true)
[[ -n $PASSWD_ENTRY ]] || fail "User $SERVICE_USER does not exist; install APC UPS Bridge first."
SERVICE_HOME=$(printf '%s' "$PASSWD_ENTRY" | cut -d: -f6)
[[ $SERVICE_HOME == /* ]] || fail "Cannot determine the home directory of $SERVICE_USER."

for bin in ssh ssh-keygen ssh-keyscan timeout; do
  command -v "$bin" >/dev/null 2>&1 || fail "Required command missing: $bin"
done
# Prefer runuser (util-linux, always present on Debian); fall back to sudo -u.
if command -v runuser >/dev/null 2>&1; then
  AS_SERVICE=(runuser -u "$SERVICE_USER" --)
elif command -v sudo >/dev/null 2>&1; then
  AS_SERVICE=(sudo -u "$SERVICE_USER" --)
else
  fail 'Neither runuser nor sudo is available.'
fi

# Prompts read from /dev/tty so the script also works when piped (curl | bash).
{ : </dev/tty; } 2>/dev/null || fail 'No terminal available for prompts.'
ask() { # ask VARNAME 'prompt'
  local __v
  read -r -p "$2" __v </dev/tty || fail 'No input received.'
  printf -v "$1" '%s' "$__v"
}

printf '\nAPC UPS Bridge - enroll SSH key on a Debian host\n'
ask HOST 'Remote host (name or address): '
[[ $HOST =~ ^[A-Za-z0-9._:-]+$ && $HOST != -* ]] || fail 'Invalid host name or address.'
ask REMOTE_USER 'SSH account created by the Debian script: '
[[ $REMOTE_USER =~ ^[a-z_][a-z0-9_-]{0,30}$ ]] || fail 'Use a lowercase Linux username (letters, numbers, _ or -).'
[[ $REMOTE_USER != root ]] || fail 'Refusing root.'
ask PORT 'SSH port [22]: '
PORT=${PORT:-22}
[[ $PORT =~ ^[0-9]{1,5}$ ]] && (( 10#$PORT >= 1 && 10#$PORT <= 65535 )) || fail 'Port must be an integer from 1 to 65535.'
PORT=$((10#$PORT))

SSH_DIR="$SERVICE_HOME/.ssh"
KEY="$SSH_DIR/id_ed25519"
PUBKEY="$KEY.pub"
KNOWN_HOSTS="$SSH_DIR/known_hosts"

# Root must not follow service-user-controlled symlinks.
[[ ! -L $SSH_DIR && ! -L $KNOWN_HOSTS ]] || fail "$SSH_DIR and $KNOWN_HOSTS must not be symlinks."

# --- Bridge key -------------------------------------------------------------
if [[ ! -f $PUBKEY ]]; then
  # Never overwrite an existing private key.
  [[ ! -e $KEY ]] || fail "$KEY exists but $PUBKEY is missing; restore the public key first."
  "${AS_SERVICE[@]}" mkdir -p -m 700 "$SSH_DIR"
  "${AS_SERVICE[@]}" ssh-keygen -t ed25519 -N '' -f "$KEY"
fi
printf '\nBridge public key fingerprint:\n  '
ssh-keygen -lf "$PUBKEY"

# --- Host key trust ---------------------------------------------------------
# known_hosts names the host as "host" for port 22 and "[host]:port" otherwise.
if [[ $PORT -eq 22 ]]; then NAME=$HOST; else NAME="[$HOST]:$PORT"; fi

EXISTING=
if [[ -f $KNOWN_HOSTS ]]; then
  EXISTING=$(ssh-keygen -F "$NAME" -f "$KNOWN_HOSTS" 2>/dev/null | grep -v '^#' || true)
fi
if [[ -n $EXISTING ]]; then
  printf '\nHost key for %s is already trusted; leaving known_hosts unchanged:\n  ' "$NAME"
  # Fields 2 and 3 are the key type and key (the name column may be hashed).
  printf '%s\n' "$EXISTING" | head -n 1 | awk '{print $2, $3}' | ssh-keygen -lf - \
    || fail 'Existing known_hosts entry is unreadable.'
else
  # Prefer ed25519; fall back to ecdsa, then rsa. Keep exactly one key type.
  HOSTKEY=
  for T in ed25519 ecdsa rsa; do
    HOSTKEY=$(ssh-keyscan -T 5 -p "$PORT" -t "$T" -- "$HOST" 2>/dev/null | grep -v '^#' | head -n 1 || true)
    [[ -z $HOSTKEY ]] || break
  done
  [[ -n $HOSTKEY ]] || fail "Could not read an SSH host key from $HOST:$PORT."
  read -r _ KTYPE KB64 _ <<< "$HOSTKEY"
  [[ -n ${KTYPE:-} && ${KB64:-} =~ ^[A-Za-z0-9+/]+={0,2}$ ]] || fail 'Unexpected ssh-keyscan output.'
  FPR=$(printf '%s %s\n' "$KTYPE" "$KB64" | ssh-keygen -E sha256 -lf -) || fail 'Cannot compute host key fingerprint.'
  printf '\nHost key offered by %s:\n  %s\n' "$NAME" "$FPR"
  printf 'Compare it with the output of this command on the Debian server console:\n'
  case $KTYPE in ecdsa-*) HK=ecdsa ;; *) HK=${KTYPE#ssh-} ;; esac
  printf '  ssh-keygen -lf /etc/ssh/ssh_host_%s_key.pub\n' "$HK"
  ask CONFIRM 'Type yes if the fingerprints match: '
  [[ $CONFIRM == yes ]] || fail 'Host key not confirmed; nothing was changed.'
  "${AS_SERVICE[@]}" mkdir -p -m 700 "$SSH_DIR"
  # Append as the service user so root never writes inside its directories.
  "${AS_SERVICE[@]}" sh -c 'umask 077; printf "%s\n" "$1" >> "$2"' sh "$NAME $KTYPE $KB64" "$KNOWN_HOSTS"
fi

SSH_COMMON=(-o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$KNOWN_HOSTS" -p "$PORT")

# --- Verify key login without shutting anything down ------------------------
# -N means "no remote command": ssh authenticates and then just holds the
# connection open without opening a session, so no forced command can run.
# The server's gate also treats an empty command as a no-op, but -N keeps this
# check safe even against a host whose key is restricted to a shutdown command.
# timeout ends the idle connection (exit 124 is expected); we judge success from
# ssh's verbose "Authenticated to ... using "publickey"" line.
VERIFY_OUT=
verify_key_login() {
  local rc=0
  VERIFY_OUT=$(timeout 15 "${AS_SERVICE[@]}" ssh -N -T "${SSH_COMMON[@]}" \
    -o BatchMode=yes -o PreferredAuthentications=publickey -o LogLevel=VERBOSE \
    -o ConnectTimeout=10 -i "$KEY" -o IdentitiesOnly=yes \
    -- "$REMOTE_USER@$HOST" 2>&1 </dev/null) || rc=$?
  grep -q 'Authenticated to .* using "publickey"' <<< "$VERIFY_OUT"
}

# Reruns are safe: if key login already works (e.g. the password was already
# disabled by an earlier run), skip enrollment.
printf '\nChecking whether key login already works (no shutdown is issued)...\n'
if verify_key_login; then
  printf 'Key login already works; skipping enrollment.\n'
else
  # --- Enroll the key -------------------------------------------------------
  # ssh reads the password from the terminal; the public key goes over stdin.
  # Any message from the remote enrollment helper is shown directly below.
  printf '\nEnrolling the key. Enter the TEMPORARY password for %s@%s when asked.\n' "$REMOTE_USER" "$HOST"
  if ! "${AS_SERVICE[@]}" ssh -T "${SSH_COMMON[@]}" \
      -o PubkeyAuthentication=no -o PreferredAuthentications=password \
      -o NumberOfPasswordPrompts=3 -o ConnectTimeout=10 \
      -- "$REMOTE_USER@$HOST" enroll-key < "$PUBKEY"; then
    printf '\nEnrollment failed; see the message above.\n' >&2
    printf 'If a key is already enrolled, a Debian administrator must rotate it\n' >&2
    printf '(remove /etc/ssh/apc-ups-bridge/authorized_keys/%s on the server).\n' "$REMOTE_USER" >&2
    exit 1
  fi
  printf '\nVerifying key login (no shutdown is issued)...\n'
  if ! verify_key_login; then
    printf '%s\n' "$VERIFY_OUT" >&2
    fail 'Key login check failed. Use the Debian console to investigate: journalctl -u ssh'
  fi
  printf 'Key login works.\n'
fi

cat <<EOF

Done. On the bridge's "Power rules & devices" page enter:
  Host:      $HOST
  SSH port:  $PORT
  SSH user:  $REMOTE_USER

"Use sudo -n" works either on or off. Then click "Test SSH (no shutdown)".
Perform a real shutdown test only during a maintenance window.
EOF
