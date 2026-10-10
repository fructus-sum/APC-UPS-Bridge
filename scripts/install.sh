#!/usr/bin/env bash
# APC UPS Bridge release installer. Requires systemd and an APC USB UPS supported by apcupsd.
set -Eeuo pipefail

REPO=fructus-sum/APC-UPS-Bridge
APP=apc-ups-bridge
PREFIX=/usr/local/bin
CONFIG_DIR=/etc/apc-ups-bridge
STATE_DIR=/var/lib/apc-ups-bridge
LOG_DIR=/var/log/apc-ups-bridge
UNIT_DIR=/etc/systemd/system
SERVICE=apc-ups-bridge.service
SERVICE_USER=apc-ups-bridge
APC_CONF=/etc/apcupsd/apcupsd.conf
APC_DEFAULTS=/etc/default/apcupsd
VERSION=latest
LISTEN=127.0.0.1:8080
LISTEN_PROVIDED=false
SETUP_APC=false
SKIP_AUTO_APC=false
APCUPS_INSTALLED_BY_US=false
HOST_PROTECTION=false
NONINTERACTIVE=false
PASSWORD_STDIN=false
SKIP_START=false

usage() {
  cat <<'USAGE'
Install or upgrade APC UPS Bridge from a checksum-verified GitHub release.
Usage: sudo bash scripts/install.sh [options]
  --version v1.0.0        Install this release (default: latest)
  --listen HOST:PORT      Web binding; overrides the installer prompt (LAN requires explicit opt-in)
                          Interactive first install asks for a port [8080], bound to 127.0.0.1
  --configure-apcupsd     Explicitly configure apcupsd for USB, even if preinstalled
  --skip-apcupsd-setup    Do not auto-configure apcupsd on a fresh installation
  --host-protection       With --configure-apcupsd, set apcupsd native shutdown at
                          10% battery or 3 minutes remaining (NOT app shutdown)
  --password-stdin        Read initial admin password from stdin (one line)
  --non-interactive       Don't ask questions; initial setup requires --password-stdin
  --skip-start            Install and enable service but do not start it immediately
  --help                  Show this help
Updates preserve credentials, settings, remote devices, action state and logs.
A newly installed apcupsd is configured for USB automatically on first install.
Existing apcupsd configurations are preserved unless --configure-apcupsd is used.
USAGE
}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
info() { printf 'APC UPS Bridge: %s\n' "$*"; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "Missing required tool: $1"; }
arch_name() {
  case "$1" in
    x86_64|amd64) echo amd64;;
    aarch64|arm64) echo arm64;;
    armv7l|armv7*) echo armv7;;
    armv6l|armv6*) echo armv6;;
    *) return 1;;
  esac
}
validate_listen() {
  local value="$1" port host
  # IPv4/loopback binds only. Do not allow shell metacharacters in EnvironmentFile.
  [[ "$value" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}:[0-9]{1,5}$ ]] || return 1
  port=${value##*:}; host=${value%:*}
  [[ "$port" != 0* ]] || return 1
  (( 10#$port >= 1 && 10#$port <= 65535 )) || return 1
  local octet
  local -a octets
  IFS=. read -r -a octets <<< "$host"
  for octet in "${octets[@]}"; do
    [[ "$octet" == 0 || "$octet" != 0* ]] || return 1
    (( 10#$octet <= 255 )) || return 1
  done
  return 0
}
validate_version() { [[ "$1" == latest || "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.-]+)?$ ]]; }
verify_release() {
  local checksum_file="$1" archive_file="$2" filename="$3" expected
  expected=$(awk -v filename="$filename" '$2 == filename && $1 ~ /^[0-9a-fA-F]+$/ { print $1 }' "$checksum_file")
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || die "Missing/invalid $filename SHA-256 in checksum manifest"
  [[ "$(sha256sum "$archive_file" | cut -d' ' -f1)" == "${expected,,}" ]] || die "Release checksum mismatch: $filename"
}
parse_args() {
  while (($#)); do
    case "$1" in
      --version) (($# >= 2)) || die 'Missing --version argument'; VERSION=$2; shift 2;;
      --listen) (($# >= 2)) || die 'Missing --listen argument'; LISTEN=$2; LISTEN_PROVIDED=true; shift 2;;
      --configure-apcupsd) SETUP_APC=true; shift;;
      --skip-apcupsd-setup) SKIP_AUTO_APC=true; shift;;
      --host-protection) HOST_PROTECTION=true; shift;;
      --password-stdin) PASSWORD_STDIN=true; shift;;
      --non-interactive) NONINTERACTIVE=true; shift;;
      --skip-start) SKIP_START=true; shift;;
      --help|-h) usage; exit 0;;
      *) die "Unknown option $1";;
    esac
  done
  validate_version "$VERSION" || die 'Invalid release version'
  validate_listen "$LISTEN" || die 'Invalid --listen (IPv4:port only; port 1–65535)'
  if [[ "$SETUP_APC" == true && "$SKIP_AUTO_APC" == true ]]; then
    die '--configure-apcupsd and --skip-apcupsd-setup cannot be combined'
  fi
  if [[ "$HOST_PROTECTION" == true && "$SETUP_APC" != true ]]; then
    die '--host-protection requires --configure-apcupsd'
  fi
}
install_apcupsd_if_missing() {
  if command -v apcaccess >/dev/null 2>&1 || [[ -x /usr/sbin/apcaccess ]]; then
    info 'Existing apcaccess detected; preserving installed apcupsd.'
    return
  fi
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -q
    DEBIAN_FRONTEND=noninteractive apt-get install -y apcupsd
  elif command -v dnf >/dev/null 2>&1; then
    dnf -y install apcupsd
  elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install apcupsd
  else
    die 'apcaccess not found. Install apcupsd with your distro package manager and retry.'
  fi
  command -v apcaccess >/dev/null 2>&1 || [[ -x /usr/sbin/apcaccess ]] || die 'apcaccess missing after package installation'
  APCUPS_INSTALLED_BY_US=true
}

# Auto-configure only a NEW apcupsd installation on a NEW Bridge host.
# Never overwrite a pre-existing UPS daemon or change the UPS configuration
# during upgrades. An explicit --configure-apcupsd remains available.
should_configure_apcupsd() {
  [[ "$SETUP_APC" == true ]] && return 0
  [[ "$SKIP_AUTO_APC" == true || "$APCUPS_INSTALLED_BY_US" != true ]] && return 1
  [[ -e "$CONFIG_DIR/service.env" || -e "$PREFIX/$APP" ]] && return 1
  return 0
}

verify_apcupsd_communication() {
  local executable=apcaccess output
  if ! command -v "$executable" >/dev/null 2>&1; then
    if [[ -x /usr/sbin/apcaccess ]]; then executable=/usr/sbin/apcaccess
    else info 'WARNING: apcaccess unavailable; cannot verify UPS communication.'; return; fi
  fi
  # The service may need a moment to expose its localhost NIS status.
  local attempt
  for attempt in 1 2 3; do
    if output=$("$executable" status 2>/dev/null) && [[ "$output" =~ (^|
# Keep unrelated apcupsd settings intact. Runs on newly installed apcupsd or with explicit user opt-in.
set_apc_key() {
  local filename="$1" key="$2" value="$3" tmp
  tmp=$(mktemp "${filename}.XXXXXX")
  awk -v k="$key" -v v="$value" '
    BEGIN { found=0 }
    $1 == k { if (!found) { print k " " v; found=1 }; next }
    { print }
    END { if (!found) print k " " v }
  ' "$filename" > "$tmp"
  chmod --reference="$filename" "$tmp"
  chown --reference="$filename" "$tmp"
  mv -f "$tmp" "$filename"
}
set_apc_default() {
  local file="$1" tmp
  tmp=$(mktemp "${file}.XXXXXX")
  awk 'BEGIN { found=0 } /^ISCONFIGURED=/ { if (!found) { print "ISCONFIGURED=yes"; found=1 }; next } { print } END { if (!found) print "ISCONFIGURED=yes" }' "$file" > "$tmp"
  chmod --reference="$file" "$tmp"
  chown --reference="$file" "$tmp"
  mv -f "$tmp" "$file"
}
configure_apcupsd() {
  [[ -f "$APC_CONF" ]] || die "No apcupsd configuration at $APC_CONF"
  local suffix
  suffix=$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM
  cp -p "$APC_CONF" "${APC_CONF}.backup-${suffix}"
  info "Backed up apcupsd config to ${APC_CONF}.backup-${suffix}"
  set_apc_key "$APC_CONF" UPSCABLE usb
  set_apc_key "$APC_CONF" UPSTYPE usb
  set_apc_key "$APC_CONF" DEVICE ''
  set_apc_key "$APC_CONF" NETSERVER on
  set_apc_key "$APC_CONF" NISIP 127.0.0.1
  if [[ "$HOST_PROTECTION" == true ]]; then
    set_apc_key "$APC_CONF" BATTERYLEVEL 10
    set_apc_key "$APC_CONF" MINUTES 3
    # TIMEOUT is an independent on-battery timer: 0 means disabled.
    set_apc_key "$APC_CONF" TIMEOUT 0
    info 'Enabled apcupsd native LOCAL host shutdown thresholds (10% / 3 minutes).'
  else
    info 'Preserved existing apcupsd local-host shutdown thresholds.'
  fi
  if [[ -f "$APC_DEFAULTS" ]]; then
    cp -p "$APC_DEFAULTS" "${APC_DEFAULTS}.backup-${suffix}"
    set_apc_default "$APC_DEFAULTS"
  fi
  systemctl enable apcupsd.service || die 'Failed to enable apcupsd'
  systemctl restart apcupsd.service || die 'Failed to restart apcupsd; restore config backup and inspect journalctl'
}
ensure_user() {
  if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
    useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
  fi
  install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0700 "$CONFIG_DIR" "$STATE_DIR" "$LOG_DIR"
}
write_service() {
  local tmp
  tmp=$(mktemp "${UNIT_DIR}/${SERVICE}.XXXXXX")
  cat > "$tmp" <<'UNIT'
[Unit]
Description=APC UPS Bridge (USB APC UPS network monitoring)
Documentation=https://github.com/fructus-sum/APC-UPS-Bridge
Wants=network-online.target
After=network-online.target apcupsd.service

[Service]
Type=simple
User=apc-ups-bridge
Group=apc-ups-bridge
WorkingDirectory=/var/lib/apc-ups-bridge
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
EnvironmentFile=/etc/apc-ups-bridge/service.env
ExecStart=/usr/local/bin/apc-ups-bridge --listen ${APC_LISTEN}
Restart=on-failure
RestartSec=5s
TimeoutStopSec=20s
UMask=0077
NoNewPrivileges=true
CapabilityBoundingSet=
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectControlGroups=true
ProtectKernelModules=true
ProtectKernelTunables=true
RestrictSUIDSGID=true
LockPersonality=true
SystemCallArchitectures=native
ReadWritePaths=/etc/apc-ups-bridge /var/lib/apc-ups-bridge /var/log/apc-ups-bridge

[Install]
WantedBy=multi-user.target
UNIT
  chmod 0644 "$tmp"
  mv -f "$tmp" "${UNIT_DIR}/${SERVICE}"
  systemctl daemon-reload
}
validate_web_port() {
  local port="$1"
  [[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] || return 1
  (( 10#$port >= 1 && 10#$port <= 65535 ))
}
prompt_listen_port() {
  local port
  IFS= read -r -p 'Web interface port [8080]: ' port || die 'Could not read web interface port'
  port=${port:-8080}
  validate_web_port "$port" || die 'Invalid web port: enter a number from 1 to 65535'
  LISTEN="127.0.0.1:$port"
  validate_listen "$LISTEN" || die 'Invalid selected web port'
}
choose_listen_port() {
  # Existing installations keep the saved binding unless --listen was given.
  # Unattended installs retain 127.0.0.1:8080 without prompting.
  if [[ "$LISTEN_PROVIDED" == true || "$NONINTERACTIVE" == true || ! -t 0 || -f "$CONFIG_DIR/service.env" ]]; then
    return
  fi
  prompt_listen_port
}
set_listen_config() {
  local file="$CONFIG_DIR/service.env" current
  if [[ -f "$file" && "$LISTEN_PROVIDED" != true ]]; then
    # Preserve user-edited service settings on upgrade.
    current=$(sed -n 's/^APC_LISTEN=//p' "$file" | head -n 1)
    validate_listen "$current" || die "Invalid existing service.env listening address: $current"
    LISTEN="$current"
    return
  fi
  printf 'APC_LISTEN=%s\n' "$LISTEN" > "${file}.tmp"
  chown "$SERVICE_USER:$SERVICE_USER" "${file}.tmp"
  chmod 0600 "${file}.tmp"
  mv -f "${file}.tmp" "$file"
}
read_initial_password() {
  local binary="$1" a b
  if [[ "$PASSWORD_STDIN" == true ]]; then
    IFS= read -r a || die 'Failed to read password from stdin'
  elif [[ "$NONINTERACTIVE" == false && -t 0 ]]; then
    read -r -s -p 'Choose an administrator password (at least 12 characters): ' a; printf '\n'
    read -r -s -p 'Repeat administrator password: ' b; printf '\n'
    [[ "$a" == "$b" ]] || die 'Password confirmation did not match'
  else
    die 'No admin password exists. Re-run with --password-stdin or run in an interactive terminal.'
  fi
  [[ ${#a} -ge 12 && ${#a} -le 256 && "$a" != *$'\n'* && "$a" != *$'\r'* ]] || die 'Password must be a single line of at least 12 characters'
  printf '%s\n' "$a" | runuser -u "$SERVICE_USER" -- "$binary" --auth-file "$STATE_DIR/auth.json" --set-password-stdin
  unset a b
}
download_release() {
  local stage="$1" arch archive url base
  arch=$(arch_name "$(uname -m)") || die "Unsupported CPU architecture: $(uname -m)"
  archive="${APP}_linux_${arch}.tar.gz"
  if [[ "$VERSION" == latest ]]; then
    base="https://github.com/${REPO}/releases/latest/download"
  else
    base="https://github.com/${REPO}/releases/download/${VERSION}"
  fi
  url="${base}/${archive}"
  info "Downloading ${VERSION} for linux/${arch} from GitHub"
  curl --fail --location --silent --show-error --retry 2 "$url" -o "${stage}/${archive}" || die 'Could not download release. An official GitHub release must exist first.'
  curl --fail --location --silent --show-error --retry 2 "${base}/SHA256SUMS" -o "${stage}/SHA256SUMS" || die 'Could not download release checksums'
  verify_release "${stage}/SHA256SUMS" "${stage}/${archive}" "$archive"
  # Extract one named file to prevent path traversal or archive overwrite attacks.
  tar -xOzf "${stage}/${archive}" "$APP" > "${stage}/${APP}" || die 'Release archive is malformed'
  [[ -s "${stage}/${APP}" ]] || die 'Release binary is empty'
  chmod 0755 "${stage}/${APP}"
  "${stage}/${APP}" --version >/dev/null || die 'Downloaded release binary cannot execute on this host'
}
main() {
  parse_args "$@"
  [[ "$(uname -s)" == Linux ]] || die 'Only Linux is supported'
  [[ "$EUID" -eq 0 ]] || die 'Run with sudo/root privileges'
  require_cmd systemctl; require_cmd sha256sum; require_cmd tar; require_cmd curl
  require_cmd awk; require_cmd mktemp; require_cmd install; require_cmd runuser
  [[ -d /run/systemd/system ]] || die 'This host is not booted with systemd'
  if [[ ! -f "$STATE_DIR/auth.json" && "$NONINTERACTIVE" == true && "$PASSWORD_STDIN" != true ]]; then
    die 'Non-interactive initial installation requires --password-stdin'
  fi
  if [[ ! -f "$STATE_DIR/auth.json" && "$NONINTERACTIVE" == false && "$PASSWORD_STDIN" != true && ! -t 0 ]]; then
    die 'Initial installation requires an interactive terminal or --password-stdin'
  fi
  # Ask before downloading a release or modifying host files.
  choose_listen_port
  local stage previous_binary='' old_unit=false had_binary=false
  stage=$(mktemp -d)
  trap 'rm -rf "$stage"' EXIT
  download_release "$stage"
  install_apcupsd_if_missing
  if should_configure_apcupsd; then
    if [[ "$SETUP_APC" != true ]]; then
      info 'Fresh installation: configuring newly installed apcupsd for USB UPS monitoring.'
    fi
    configure_apcupsd
    verify_apcupsd_communication
  else
    info 'Preserving existing apcupsd configuration. Use --configure-apcupsd to change it explicitly.'
  fi
  ensure_user
  set_listen_config
  if [[ ! -f "$STATE_DIR/auth.json" ]]; then
    read_initial_password "${stage}/${APP}"
  else
    info 'Preserving existing administrator password hash.'
  fi
  if [[ -f "$UNIT_DIR/$SERVICE" ]]; then old_unit=true; cp -p "$UNIT_DIR/$SERVICE" "$stage/previous.service"; fi
  if [[ -f "$PREFIX/$APP" ]]; then had_binary=true; cp -p "$PREFIX/$APP" "$stage/previous.binary"; fi
  local staged_bin
  staged_bin=$(mktemp "$PREFIX/.${APP}.XXXXXX")
  install -m 0755 "${stage}/${APP}" "$staged_bin"
  mv -f "$staged_bin" "$PREFIX/$APP"
  write_service
  systemctl enable "$SERVICE"
  if [[ "$SKIP_START" == false ]]; then
    if ! systemctl restart "$SERVICE" || ! systemctl is-active --quiet "$SERVICE"; then
      info 'Service failed to start. Restoring previous binary/unit where available.'
      if [[ "$had_binary" == true ]]; then cp -p "$stage/previous.binary" "$PREFIX/$APP"; else rm -f "$PREFIX/$APP"; fi
      if [[ "$old_unit" == true ]]; then cp -p "$stage/previous.service" "$UNIT_DIR/$SERVICE"; else rm -f "$UNIT_DIR/$SERVICE"; fi
      systemctl daemon-reload
      if [[ "$had_binary" == true ]]; then systemctl restart "$SERVICE" || true; fi
      die 'Service failed; check journalctl -u apc-ups-bridge.service'
    fi
  fi
  info "Installation complete. Listening at ${LISTEN}; service enabled at boot."
  info 'If you enabled LAN access, protect plain HTTP with a trusted LAN/VPN or HTTPS reverse proxy; never forward directly to the internet.'
  info 'Before depending on auto power actions, validate SSH, WoL and apcupsd thresholds on real hardware.'
}
if [[ "${UPS_BRIDGE_LIB_ONLY:-0}" != 1 ]]; then main "$@"; fi
\n')STATUS[[:space:]]*: ]]; then
      info 'USB UPS communication verified through apcaccess.'
      return
    fi
    [[ "$attempt" == 3 ]] || sleep 1
  done
  info 'WARNING: apcupsd was configured, but no UPS STATUS was reported. Check USB connection and run: apcaccess status'
}

# Keep unrelated apcupsd settings intact. Runs only when user explicitly requests it.
set_apc_key() {
  local filename="$1" key="$2" value="$3" tmp
  tmp=$(mktemp "${filename}.XXXXXX")
  awk -v k="$key" -v v="$value" '
    BEGIN { found=0 }
    $1 == k { if (!found) { print k " " v; found=1 }; next }
    { print }
    END { if (!found) print k " " v }
  ' "$filename" > "$tmp"
  chmod --reference="$filename" "$tmp"
  chown --reference="$filename" "$tmp"
  mv -f "$tmp" "$filename"
}
set_apc_default() {
  local file="$1" tmp
  tmp=$(mktemp "${file}.XXXXXX")
  awk 'BEGIN { found=0 } /^ISCONFIGURED=/ { if (!found) { print "ISCONFIGURED=yes"; found=1 }; next } { print } END { if (!found) print "ISCONFIGURED=yes" }' "$file" > "$tmp"
  chmod --reference="$file" "$tmp"
  chown --reference="$file" "$tmp"
  mv -f "$tmp" "$file"
}
configure_apcupsd() {
  [[ -f "$APC_CONF" ]] || die "No apcupsd configuration at $APC_CONF"
  local suffix
  suffix=$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM
  cp -p "$APC_CONF" "${APC_CONF}.backup-${suffix}"
  info "Backed up apcupsd config to ${APC_CONF}.backup-${suffix}"
  set_apc_key "$APC_CONF" UPSCABLE usb
  set_apc_key "$APC_CONF" UPSTYPE usb
  set_apc_key "$APC_CONF" DEVICE ''
  set_apc_key "$APC_CONF" NETSERVER on
  set_apc_key "$APC_CONF" NISIP 127.0.0.1
  if [[ "$HOST_PROTECTION" == true ]]; then
    set_apc_key "$APC_CONF" BATTERYLEVEL 10
    set_apc_key "$APC_CONF" MINUTES 3
    # TIMEOUT is an independent on-battery timer: 0 means disabled.
    set_apc_key "$APC_CONF" TIMEOUT 0
    info 'Enabled apcupsd native LOCAL host shutdown thresholds (10% / 3 minutes).'
  else
    info 'Preserved existing apcupsd local-host shutdown thresholds.'
  fi
  if [[ -f "$APC_DEFAULTS" ]]; then
    cp -p "$APC_DEFAULTS" "${APC_DEFAULTS}.backup-${suffix}"
    set_apc_default "$APC_DEFAULTS"
  fi
  systemctl enable apcupsd.service || die 'Failed to enable apcupsd'
  systemctl restart apcupsd.service || die 'Failed to restart apcupsd; restore config backup and inspect journalctl'
}
ensure_user() {
  if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
    useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
  fi
  install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0700 "$CONFIG_DIR" "$STATE_DIR" "$LOG_DIR"
}
write_service() {
  local tmp
  tmp=$(mktemp "${UNIT_DIR}/${SERVICE}.XXXXXX")
  cat > "$tmp" <<'UNIT'
[Unit]
Description=APC UPS Bridge (USB APC UPS network monitoring)
Documentation=https://github.com/fructus-sum/APC-UPS-Bridge
Wants=network-online.target
After=network-online.target apcupsd.service

[Service]
Type=simple
User=apc-ups-bridge
Group=apc-ups-bridge
WorkingDirectory=/var/lib/apc-ups-bridge
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
EnvironmentFile=/etc/apc-ups-bridge/service.env
ExecStart=/usr/local/bin/apc-ups-bridge --listen ${APC_LISTEN}
Restart=on-failure
RestartSec=5s
TimeoutStopSec=20s
UMask=0077
NoNewPrivileges=true
CapabilityBoundingSet=
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectControlGroups=true
ProtectKernelModules=true
ProtectKernelTunables=true
RestrictSUIDSGID=true
LockPersonality=true
SystemCallArchitectures=native
ReadWritePaths=/etc/apc-ups-bridge /var/lib/apc-ups-bridge /var/log/apc-ups-bridge

[Install]
WantedBy=multi-user.target
UNIT
  chmod 0644 "$tmp"
  mv -f "$tmp" "${UNIT_DIR}/${SERVICE}"
  systemctl daemon-reload
}
validate_web_port() {
  local port="$1"
  [[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] || return 1
  (( 10#$port >= 1 && 10#$port <= 65535 ))
}
prompt_listen_port() {
  local port
  IFS= read -r -p 'Web interface port [8080]: ' port || die 'Could not read web interface port'
  port=${port:-8080}
  validate_web_port "$port" || die 'Invalid web port: enter a number from 1 to 65535'
  LISTEN="127.0.0.1:$port"
  validate_listen "$LISTEN" || die 'Invalid selected web port'
}
choose_listen_port() {
  # Existing installations keep the saved binding unless --listen was given.
  # Unattended installs retain 127.0.0.1:8080 without prompting.
  if [[ "$LISTEN_PROVIDED" == true || "$NONINTERACTIVE" == true || ! -t 0 || -f "$CONFIG_DIR/service.env" ]]; then
    return
  fi
  prompt_listen_port
}
set_listen_config() {
  local file="$CONFIG_DIR/service.env" current
  if [[ -f "$file" && "$LISTEN_PROVIDED" != true ]]; then
    # Preserve user-edited service settings on upgrade.
    current=$(sed -n 's/^APC_LISTEN=//p' "$file" | head -n 1)
    validate_listen "$current" || die "Invalid existing service.env listening address: $current"
    LISTEN="$current"
    return
  fi
  printf 'APC_LISTEN=%s\n' "$LISTEN" > "${file}.tmp"
  chown "$SERVICE_USER:$SERVICE_USER" "${file}.tmp"
  chmod 0600 "${file}.tmp"
  mv -f "${file}.tmp" "$file"
}
read_initial_password() {
  local binary="$1" a b
  if [[ "$PASSWORD_STDIN" == true ]]; then
    IFS= read -r a || die 'Failed to read password from stdin'
  elif [[ "$NONINTERACTIVE" == false && -t 0 ]]; then
    read -r -s -p 'Choose an administrator password (at least 12 characters): ' a; printf '\n'
    read -r -s -p 'Repeat administrator password: ' b; printf '\n'
    [[ "$a" == "$b" ]] || die 'Password confirmation did not match'
  else
    die 'No admin password exists. Re-run with --password-stdin or run in an interactive terminal.'
  fi
  [[ ${#a} -ge 12 && ${#a} -le 256 && "$a" != *$'\n'* && "$a" != *$'\r'* ]] || die 'Password must be a single line of at least 12 characters'
  printf '%s\n' "$a" | runuser -u "$SERVICE_USER" -- "$binary" --auth-file "$STATE_DIR/auth.json" --set-password-stdin
  unset a b
}
download_release() {
  local stage="$1" arch archive url base
  arch=$(arch_name "$(uname -m)") || die "Unsupported CPU architecture: $(uname -m)"
  archive="${APP}_linux_${arch}.tar.gz"
  if [[ "$VERSION" == latest ]]; then
    base="https://github.com/${REPO}/releases/latest/download"
  else
    base="https://github.com/${REPO}/releases/download/${VERSION}"
  fi
  url="${base}/${archive}"
  info "Downloading ${VERSION} for linux/${arch} from GitHub"
  curl --fail --location --silent --show-error --retry 2 "$url" -o "${stage}/${archive}" || die 'Could not download release. An official GitHub release must exist first.'
  curl --fail --location --silent --show-error --retry 2 "${base}/SHA256SUMS" -o "${stage}/SHA256SUMS" || die 'Could not download release checksums'
  verify_release "${stage}/SHA256SUMS" "${stage}/${archive}" "$archive"
  # Extract one named file to prevent path traversal or archive overwrite attacks.
  tar -xOzf "${stage}/${archive}" "$APP" > "${stage}/${APP}" || die 'Release archive is malformed'
  [[ -s "${stage}/${APP}" ]] || die 'Release binary is empty'
  chmod 0755 "${stage}/${APP}"
  "${stage}/${APP}" --version >/dev/null || die 'Downloaded release binary cannot execute on this host'
}
main() {
  parse_args "$@"
  [[ "$(uname -s)" == Linux ]] || die 'Only Linux is supported'
  [[ "$EUID" -eq 0 ]] || die 'Run with sudo/root privileges'
  require_cmd systemctl; require_cmd sha256sum; require_cmd tar; require_cmd curl
  require_cmd awk; require_cmd mktemp; require_cmd install; require_cmd runuser
  [[ -d /run/systemd/system ]] || die 'This host is not booted with systemd'
  if [[ ! -f "$STATE_DIR/auth.json" && "$NONINTERACTIVE" == true && "$PASSWORD_STDIN" != true ]]; then
    die 'Non-interactive initial installation requires --password-stdin'
  fi
  if [[ ! -f "$STATE_DIR/auth.json" && "$NONINTERACTIVE" == false && "$PASSWORD_STDIN" != true && ! -t 0 ]]; then
    die 'Initial installation requires an interactive terminal or --password-stdin'
  fi
  # Ask before downloading a release or modifying host files.
  choose_listen_port
  local stage previous_binary='' old_unit=false had_binary=false
  stage=$(mktemp -d)
  trap 'rm -rf "$stage"' EXIT
  download_release "$stage"
  install_apcupsd_if_missing
  # Existing UPS daemon configuration remains untouched unless explicitly requested.
  if [[ "$SETUP_APC" == true ]]; then configure_apcupsd; fi
  ensure_user
  set_listen_config
  if [[ ! -f "$STATE_DIR/auth.json" ]]; then
    read_initial_password "${stage}/${APP}"
  else
    info 'Preserving existing administrator password hash.'
  fi
  if [[ -f "$UNIT_DIR/$SERVICE" ]]; then old_unit=true; cp -p "$UNIT_DIR/$SERVICE" "$stage/previous.service"; fi
  if [[ -f "$PREFIX/$APP" ]]; then had_binary=true; cp -p "$PREFIX/$APP" "$stage/previous.binary"; fi
  local staged_bin
  staged_bin=$(mktemp "$PREFIX/.${APP}.XXXXXX")
  install -m 0755 "${stage}/${APP}" "$staged_bin"
  mv -f "$staged_bin" "$PREFIX/$APP"
  write_service
  systemctl enable "$SERVICE"
  if [[ "$SKIP_START" == false ]]; then
    if ! systemctl restart "$SERVICE" || ! systemctl is-active --quiet "$SERVICE"; then
      info 'Service failed to start. Restoring previous binary/unit where available.'
      if [[ "$had_binary" == true ]]; then cp -p "$stage/previous.binary" "$PREFIX/$APP"; else rm -f "$PREFIX/$APP"; fi
      if [[ "$old_unit" == true ]]; then cp -p "$stage/previous.service" "$UNIT_DIR/$SERVICE"; else rm -f "$UNIT_DIR/$SERVICE"; fi
      systemctl daemon-reload
      if [[ "$had_binary" == true ]]; then systemctl restart "$SERVICE" || true; fi
      die 'Service failed; check journalctl -u apc-ups-bridge.service'
    fi
  fi
  info "Installation complete. Listening at ${LISTEN}; service enabled at boot."
  info 'If you enabled LAN access, protect plain HTTP with a trusted LAN/VPN or HTTPS reverse proxy; never forward directly to the internet.'
  info 'Before depending on auto power actions, validate SSH, WoL and apcupsd thresholds on real hardware.'
}
if [[ "${UPS_BRIDGE_LIB_ONLY:-0}" != 1 ]]; then main "$@"; fi
