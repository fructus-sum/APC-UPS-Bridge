#!/usr/bin/env bash
# Remove APC UPS Bridge without removing apcupsd or changing UPS shutdown settings.
set -Eeuo pipefail
if [[ ${1:-} == --help ]]; then
  printf 'Usage: sudo bash scripts/uninstall.sh [--purge]\nWithout --purge, administrator credentials, settings, safety state and logs are retained.\n'
  exit 0
fi
[[ $# -le 1 && ( $# -eq 0 || $1 == --purge ) ]] || { echo 'Unknown argument; see --help' >&2; exit 2; }
[[ $EUID -eq 0 ]] || { echo 'Run as root/sudo' >&2; exit 1; }
command -v systemctl >/dev/null || { echo 'systemctl is required' >&2; exit 1; }
systemctl disable --now apc-ups-bridge.service 2>/dev/null || true
rm -f /etc/systemd/system/apc-ups-bridge.service
systemctl daemon-reload
rm -f /usr/local/bin/apc-ups-bridge
if [[ ${1:-} == --purge ]]; then
  rm -rf -- /etc/apc-ups-bridge /var/lib/apc-ups-bridge /var/log/apc-ups-bridge
  if id -u apc-ups-bridge >/dev/null 2>&1; then
    userdel apc-ups-bridge || true
  fi
  echo 'Removed APC UPS Bridge including credentials, settings, state and event logs.'
else
  echo 'Removed application and service. Data was preserved in /etc, /var/lib and /var/log/apc-ups-bridge.'
fi
echo 'apcupsd and its host shutdown configuration were NOT modified.'
