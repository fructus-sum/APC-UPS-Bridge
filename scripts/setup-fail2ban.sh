#!/usr/bin/env bash
# Idempotent Debian installer/updater for the dedicated APC UPS Bridge jail.
# All jail and ban actions remain privileged; the web process gets read-only
# access to this jail's exported ban/unban log only.
set -Eeuo pipefail
say() { printf '[apc-ups-bridge fail2ban] %s\n' "$*" >&2; }
die() { say "ERROR: $*"; exit 1; }
[[ ${EUID} -eq 0 ]] || die 'Run with sudo/root'
[[ $# -eq 2 && $1 == --port && $2 =~ ^[0-9]{1,5}$ ]] || die 'Usage: sudo bash scripts/setup-fail2ban.sh --port PORT'
port=$2
(( 10#$port >= 1024 && 10#$port <= 65535 )) || die 'Port must be 1024–65535'
command -v apt-get >/dev/null || die 'Automatic Fail2ban setup requires Debian/Ubuntu apt-get; configure your other distribution manually.'
id apc-ups-bridge >/dev/null 2>&1 || die 'Install APC UPS Bridge service account first'
base=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
filter_dir=/etc/fail2ban/filter.d
action_dir=/etc/fail2ban/action.d
jail_dir=/etc/fail2ban/jail.d
log_dir=/var/log/apc-ups-bridge-fail2ban
jail="$jail_dir/apc-ups-bridge.local"
if ! command -v fail2ban-client >/dev/null 2>&1; then
  say 'Installing fail2ban via apt-get.'
  DEBIAN_FRONTEND=noninteractive apt-get update -q </dev/null
  DEBIAN_FRONTEND=noninteractive apt-get install -y fail2ban python3-systemd </dev/null
fi
if ! dpkg-query -W -f='${Status}' python3-systemd 2>/dev/null | grep -q 'install ok installed'; then
  DEBIAN_FRONTEND=noninteractive apt-get install -y python3-systemd </dev/null
fi
install -d -o root -g root -m 0755 "$filter_dir" "$action_dir" "$jail_dir" /usr/local/libexec
install -d -o root -g apc-ups-bridge -m 0750 "$log_dir"
log="$log_dir/events.log"
if [[ ! -e "$log" ]]; then
  install -o root -g apc-ups-bridge -m 0640 /dev/null "$log"
fi
[[ -f "$log" && ! -L "$log" ]] || die 'Refusing an unexpected security log'
chown root:apc-ups-bridge "$log"
chmod 0640 "$log"
# Everything this script writes, so a failed run can be undone cleanly.
event_script=/usr/local/libexec/apc-ups-bridge-fail2ban-event
logrotate_conf=/etc/logrotate.d/apc-ups-bridge-fail2ban
managed_files=(
  "$filter_dir/apc-ups-bridge.conf"
  "$action_dir/apc-ups-bridge-event.conf"
  "$event_script"
  "$logrotate_conf"
  "$jail"
)
backup_dir=$(mktemp -d)
temp_files=()
setup_done=false

# Put every managed file back as it was (or remove it if it is new) and make
# Fail2ban load the old settings again, so a bad config cannot stop other jails.
undo_changes() {
  local f
  say 'Setup did not finish, so the changes just made are being undone.'
  for f in "${managed_files[@]}"; do
    if [[ -e "$backup_dir/${f##*/}" ]]; then
      cp -p -- "$backup_dir/${f##*/}" "$f" || say "WARNING: could not restore $f"
    else
      rm -f -- "$f"
    fi
  done
  if systemctl is-active --quiet fail2ban; then
    if fail2ban-client -t >/dev/null 2>&1; then
      fail2ban-client reload >/dev/null 2>&1 || say 'WARNING: Fail2ban could not reload its previous settings.'
    else
      say 'WARNING: Fail2ban still reports a configuration problem; check it with: fail2ban-client -t'
    fi
  fi
}
finish() {
  local status=$?
  trap - EXIT
  set +e
  if ((${#temp_files[@]})); then rm -f -- "${temp_files[@]}"; fi
  if [[ "$setup_done" != true ]]; then undo_changes; fi
  rm -rf -- "$backup_dir"
  exit "$status"
}
trap finish EXIT
trap 'exit 1' INT TERM
for f in "${managed_files[@]}"; do
  if [[ -f "$f" ]]; then cp -p -- "$f" "$backup_dir/${f##*/}"; fi
done

# Temp names end in "~", which logrotate ignores, and never end in .conf/.local,
# so Fail2ban ignores them too if a run is interrupted.
write_managed() {
  local src=$1 dst=$2 mode=$3
  if [[ -f "$dst" ]] && ! grep -q 'Managed by APC UPS Bridge' "$dst"; then
    die "Refusing to overwrite unmanaged configuration: $dst"
  fi
  local tmp
  tmp=$(mktemp --suffix='~' "${dst}.XXXXXX")
  temp_files+=("$tmp")
  install -o root -g root -m "$mode" "$src" "$tmp"
  mv -f "$tmp" "$dst"
}
write_managed "$base/packaging/fail2ban/filter.d/apc-ups-bridge.conf" "$filter_dir/apc-ups-bridge.conf" 0644
write_managed "$base/packaging/fail2ban/action.d/apc-ups-bridge-event.conf" "$action_dir/apc-ups-bridge-event.conf" 0644
# Script is not a config file; ownership and checksum are controlled by installer.
install -o root -g root -m 0755 "$base/packaging/fail2ban/apc-ups-bridge-event.sh" "$event_script"
write_managed "$base/packaging/fail2ban/logrotate.conf" "$logrotate_conf" 0644
# Fail2ban only reads *.conf and *.local here, so this dotfile temp is ignored.
tmp=$(mktemp "$jail_dir/.apc-ups-bridge.XXXXXX")
temp_files+=("$tmp")
cat >"$tmp" <<JAIL
# Managed by APC UPS Bridge; safe to regenerate on install or upgrade.
[apc-ups-bridge]
enabled = true
filter = apc-ups-bridge
backend = systemd
port = $port
protocol = tcp
maxretry = 5
findtime = 10m
bantime = 5m
bantime.increment = true
bantime.factor = 1
action = %(action_)s
         apc-ups-bridge-event
JAIL
chmod 0644 "$tmp"
if [[ -f "$jail" ]] && ! grep -q 'Managed by APC UPS Bridge' "$jail"; then
  die "Refusing to overwrite unmanaged jail: $jail"
fi
mv -f "$tmp" "$jail"
# Any failure from here on undoes the changes above (see finish), rather than
# leaving a broken ban integration that could stop other jails on next restart.
fail2ban-client -t >/dev/null || die 'Fail2ban configuration failed validation; inspect fail2ban-client -t'
if systemctl is-active --quiet fail2ban; then
  fail2ban-client reload || die 'Could not reload Fail2ban'
else
  systemctl enable --now fail2ban || die 'Could not enable/start Fail2ban'
fi
fail2ban-client status apc-ups-bridge || die 'APC UPS Bridge jail was not activated'
setup_done=true
say 'Five failed logins in ten minutes trigger an IP ban, starting at five minutes and doubling for repeat offences within a day.'
