#!/usr/bin/env bash
# Remove APC UPS Bridge without removing apcupsd, its configuration or its backups.
# Works from a checkout, from /usr/local/sbin/apc-ups-bridge-uninstall, or piped:
#   curl -fsSL .../scripts/uninstall.sh | sudo bash
#   curl -fsSL .../scripts/uninstall.sh | sudo bash -s -- --purge
# Safe to re-run: every step tolerates parts that are already gone.
# Everything lives in main, invoked on the last line, so a truncated download
# of a piped script can never run a partial uninstall.
set -Eeuo pipefail
usage() {
  printf 'Usage: sudo bash scripts/uninstall.sh [--purge]\nWithout --purge, administrator credentials, settings, safety state and logs are retained.\n'
}
main() {
  case "${1:-}" in
    --help|-h) usage; exit 0;;
  esac
  [[ $# -le 1 && ( $# -eq 0 || $1 == --purge ) ]] || { echo 'Unknown argument; see --help' >&2; exit 2; }
  [[ $EUID -eq 0 ]] || { echo 'Run as root/sudo' >&2; exit 1; }
  command -v systemctl >/dev/null 2>&1 || { echo 'systemctl is required' >&2; exit 1; }
  local purge=false
  if [[ ${1:-} == --purge ]]; then purge=true; fi

  # Service: stop and disable if it exists; ignore "not found" and friends.
  systemctl disable --now apc-ups-bridge.service </dev/null >/dev/null 2>&1 || true
  systemctl disable --now apc-ups-bridge-host-settings.socket </dev/null >/dev/null 2>&1 || true
  systemctl stop apc-ups-bridge-host-settings.service </dev/null >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/apc-ups-bridge-host-settings.socket /etc/systemd/system/apc-ups-bridge-host-settings.service
  rm -f /usr/local/libexec/apc-ups-bridge-host-helper
  rm -f /etc/systemd/system/apc-ups-bridge.service
  systemctl daemon-reload </dev/null || true
  systemctl reset-failed apc-ups-bridge.service </dev/null >/dev/null 2>&1 || true
  rm -f /usr/local/bin/apc-ups-bridge
  if [[ $purge == true ]]; then
    rm -rf -- /etc/apc-ups-bridge /var/lib/apc-ups-bridge /var/log/apc-ups-bridge
    if id -u apc-ups-bridge >/dev/null 2>&1; then
      userdel apc-ups-bridge </dev/null || true
    fi
    echo 'Removed APC UPS Bridge including credentials, settings, state and event logs.'
  else
    echo 'Removed application and service. Data was preserved in /etc/apc-ups-bridge, /var/lib/apc-ups-bridge and /var/log/apc-ups-bridge.'
  fi
  # Remove this uninstaller's installed copy last. The shell has already read this
  # script (or reads it from a pipe), so deleting it while running is safe.
  rm -f /usr/local/sbin/apc-ups-bridge-uninstall
  echo 'apcupsd, its configuration and its config backups were NOT modified.'
}
main "$@"
