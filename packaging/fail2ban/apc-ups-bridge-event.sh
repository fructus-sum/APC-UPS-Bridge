#!/usr/bin/env bash
# Called as root by Fail2ban, never by the web service.
# A separate root-controlled directory prevents an unprivileged service from
# replacing this log or obtaining arbitrary root journal access.
set -Eeuo pipefail
[[ $# -eq 2 ]] || exit 2
action=$1 ip=$2
case "$action" in ban|unban) ;; *) exit 2;; esac
[[ "$ip" =~ ^[0-9a-fA-F:.]+$ && ${#ip} -le 45 ]] || exit 2
log=/var/log/apc-ups-bridge-fail2ban/events.log
[[ -f "$log" && ! -L "$log" ]] || exit 1
printf '%s %s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$action" "$ip" >> "$log"
