#!/usr/bin/env bash
# APC UPS Bridge installer: builds from source. Requires systemd and an APC USB UPS supported by apcupsd.
# Keep `main` invoked ONLY on the last line: when piped (curl | sudo bash) bash must have
# read the whole script before anything runs, and nothing below may read the script's stdin.
set -Eeuo pipefail

REPO=fructus-sum/APC-UPS-Bridge
REPO_MODULE=github.com/fructus-sum/APC-UPS-Bridge
APP=apc-ups-bridge
PREFIX=/usr/local/bin
UNINSTALL_BIN=/usr/local/sbin/apc-ups-bridge-uninstall
CONFIG_DIR=/etc/apc-ups-bridge
STATE_DIR=/var/lib/apc-ups-bridge
LOG_DIR=/var/log/apc-ups-bridge
UNIT_DIR=/etc/systemd/system
SERVICE=apc-ups-bridge.service
SERVICE_USER=apc-ups-bridge
APC_CONF=/etc/apcupsd/apcupsd.conf
APC_DEFAULTS=/etc/default/apcupsd
BUILD_PARENT=/var/tmp
TTY_DEVICE=/dev/tty
MIN_PORT=1024
SERVICE_SETTLE_SECONDS=5
GO_MIN_MAJOR=1
GO_MIN_MINOR=22
DEFAULT_BRANCH=main
DEFAULT_LISTEN=0.0.0.0:8080
BRANCH=$DEFAULT_BRANCH
LISTEN=$DEFAULT_LISTEN
LISTEN_PROVIDED=false
SETUP_APC=false
SKIP_AUTO_APC=false
APCUPS_INSTALLED_BY_US=false
HOST_PROTECTION=false
NONINTERACTIVE=false
PASSWORD_STDIN=false
SKIP_START=false
# Runtime state (set by main).
WORKDIR=''
SOURCE_DIR=''
GO_BIN=''
INITIAL_PASSWORD=''
ROLLBACK_HAD_BINARY=false
ROLLBACK_HAD_UNIT=false
ROLLBACK_HAD_ENV=false
GO_ENV=()

usage() {
  cat <<'USAGE'
Install or upgrade APC UPS Bridge by building it from source.
Usage: sudo bash scripts/install.sh [options]
   or: curl -fsSL https://raw.githubusercontent.com/fructus-sum/APC-UPS-Bridge/main/scripts/install.sh | sudo bash
   or: ... | sudo bash -s -- [options]
  --branch NAME           When not run from a checkout, build this branch (default: main)
  --listen HOST:PORT      Web binding (default 0.0.0.0:8080, reachable on your LAN); port 1024-65535
                          Interactive first install asks for a port [8080].
  --configure-apcupsd     Back up and configure apcupsd for a local USB UPS
  --skip-apcupsd-setup    Do not auto-configure an apcupsd that this installer just installed
  --host-protection       With --configure-apcupsd, set apcupsd native shutdown at
                          10% battery or 3 minutes remaining (NOT app shutdown)
  --password-stdin        Read initial admin password from stdin (one line; needs a saved script, not a pipe)
  --non-interactive       Don't ask questions; initial setup requires --password-stdin
  --skip-start            Install and enable service but do not start it immediately
  --help                  Show this help
Prompts are read from /dev/tty, so piping this script into bash works.
The web interface is plain HTTP meant for a trusted home LAN: never port-forward it.
Updates preserve credentials, settings, remote devices, action state and logs.
A Go toolchain (1.22+) is used if present, otherwise a temporary one is downloaded and removed afterwards.
A freshly installed apcupsd is configured for USB automatically on a first install.
Existing apcupsd configuration is NEVER modified without --configure-apcupsd.
USAGE
}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
info() { printf 'APC UPS Bridge: %s\n' "$*"; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "Missing required tool: $1"; }

# Map `uname -m` (and userland word size) to the official Go tarball architecture.
# The official 32-bit ARM build (armv6l) runs on armv6, armv7 and armv8 32-bit userlands.
# A 64-bit kernel with a 32-bit userland reports aarch64, so check the word size too.
go_arch_name() {
  local machine="$1" bits="${2:-64}"
  case "$machine" in
    x86_64|amd64) echo amd64;;
    aarch64|arm64) if [[ "$bits" == 32 ]]; then echo armv6l; else echo arm64; fi;;
    armv7l|armv6l|armv8l|armv7*|armv6*) echo armv6l;;
    *) return 1;;
  esac
}
# True when a `go version`/GOVERSION string (go1.22.3, go1.23rc1, go1.22) is >= 1.22.
go_version_ok() {
  local major minor
  [[ "$1" =~ ^go([0-9]+)\.([0-9]+) ]] || return 1
  major=${BASH_REMATCH[1]}; minor=${BASH_REMATCH[2]}
  (( 10#$major > GO_MIN_MAJOR || (10#$major == GO_MIN_MAJOR && 10#$minor >= GO_MIN_MINOR) ))
}
# Stable release name as published at https://go.dev/VERSION?m=text, e.g. go1.24.1
valid_go_release_name() { [[ "$1" =~ ^go[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; }
validate_branch() {
  local b="$1"
  [[ "$b" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$ ]] || return 1
  [[ "$b" != *..* && "$b" != *//* && "$b" != */ && "$b" != *. ]]
}
validate_listen() {
  local value="$1" port host
  # IPv4/loopback binds only. Do not allow shell metacharacters in EnvironmentFile.
  [[ "$value" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}:[0-9]{1,5}$ ]] || return 1
  port=${value##*:}; host=${value%:*}
  [[ "$port" != 0* ]] || return 1
  (( 10#$port >= MIN_PORT && 10#$port <= 65535 )) || return 1
  local octet
  local -a octets
  IFS=. read -r -a octets <<< "$host"
  for octet in "${octets[@]}"; do
    [[ "$octet" == 0 || "$octet" != 0* ]] || return 1
    (( 10#$octet <= 255 )) || return 1
  done
  return 0
}
validate_password() {
  [[ ${#1} -ge 12 && ${#1} -le 256 && "$1" != *$'\n'* && "$1" != *$'\r'* ]]
}
parse_args() {
  while (($#)); do
    case "$1" in
      --branch) (($# >= 2)) || die 'Missing --branch argument'; BRANCH=$2; shift 2;;
      --version) die '--version was removed: the installer builds from source. Use --branch NAME to pick a branch.';;
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
  validate_branch "$BRANCH" || die 'Invalid --branch (letters, digits, . _ - / only; no "..")'
  validate_listen "$LISTEN" || die 'Invalid --listen (IPv4:port only; use a port from 1024 to 65535, the service runs unprivileged and cannot bind lower ports)'
  if [[ "$SETUP_APC" == true && "$SKIP_AUTO_APC" == true ]]; then
    die '--configure-apcupsd and --skip-apcupsd-setup cannot be combined'
  fi
  if [[ "$HOST_PROTECTION" == true && "$SETUP_APC" != true ]]; then
    die '--host-protection requires --configure-apcupsd'
  fi
}

# --- interactive input -------------------------------------------------------
# Prompts read from the controlling terminal so `curl ... | sudo bash` can still ask questions.
has_tty() { { : <"$TTY_DEVICE"; } 2>/dev/null; }
is_interactive() { [[ "$NONINTERACTIVE" != true ]] && has_tty; }
# read_tty_line PROMPT [secret]: prompt on stderr, one line from the terminal on stdout.
read_tty_line() {
  local prompt="$1" secret="${2:-false}" line=''
  printf '%s' "$prompt" >&2
  if [[ "$secret" == true ]]; then
    IFS= read -r -s line <"$TTY_DEVICE" || [[ -n "$line" ]] || return 1
    printf '\n' >&2
  else
    IFS= read -r line <"$TTY_DEVICE" || [[ -n "$line" ]] || return 1
  fi
  printf '%s' "$line"
}

# --- apcupsd ---------------------------------------------------------------
install_apcupsd_if_missing() {
  if command -v apcaccess >/dev/null 2>&1 || [[ -x /usr/sbin/apcaccess ]]; then
    info 'Existing apcaccess detected; preserving installed apcupsd.'
    return
  fi
  # </dev/null: package managers must never read the script from a piped stdin.
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -q </dev/null
    DEBIAN_FRONTEND=noninteractive apt-get install -y apcupsd </dev/null
  elif command -v dnf >/dev/null 2>&1; then
    dnf -y install apcupsd </dev/null
  elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install apcupsd </dev/null
  else
    die 'apcaccess not found. Install apcupsd with your distro package manager and retry.'
  fi
  command -v apcaccess >/dev/null 2>&1 || [[ -x /usr/sbin/apcaccess ]] || die 'apcaccess missing after package installation'
  APCUPS_INSTALLED_BY_US=true
}
# Auto-configure only an apcupsd that WE just installed, on a NEW Bridge host.
# Never touch a pre-existing UPS daemon or reconfigure during upgrades.
# An explicit --configure-apcupsd always applies.
should_configure_apcupsd() {
  [[ "$SETUP_APC" == true ]] && return 0
  [[ "$SKIP_AUTO_APC" == true || "$APCUPS_INSTALLED_BY_US" != true ]] && return 1
  [[ -e "$CONFIG_DIR/service.env" || -e "$PREFIX/$APP" ]] && return 1
  return 0
}
# Keep unrelated apcupsd settings intact. Runs on a newly installed apcupsd or with explicit opt-in.
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
# Warn (never fail) if apcupsd does not report a UPS status after configuration.
verify_apcupsd_communication() {
  local executable attempt output
  if command -v apcaccess >/dev/null 2>&1; then executable=apcaccess
  elif [[ -x /usr/sbin/apcaccess ]]; then executable=/usr/sbin/apcaccess
  else
    info 'WARNING: apcaccess unavailable; cannot verify UPS communication.'
    return 0
  fi
  # apcupsd may need a moment after the restart before its NIS listener answers.
  for attempt in 1 2 3; do
    if output=$("$executable" status 2>/dev/null </dev/null) \
       && grep -Eq '^STATUS[[:space:]]*:' <<< "$output" \
       && ! grep -Eq '^STATUS[[:space:]]*:[[:space:]]*COMMLOST' <<< "$output"; then
      info 'apcupsd reports UPS status; USB communication looks good.'
      return 0
    fi
    if (( attempt < 3 )); then sleep 1; fi
  done
  info 'WARNING: could not read UPS status from apcupsd (apcaccess status).'
  info 'Check that the UPS USB cable is connected, then run: apcaccess status / journalctl -u apcupsd'
  return 0
}

# --- users, unit, listen address --------------------------------------------
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
  (( 10#$port >= MIN_PORT && 10#$port <= 65535 ))
}
prompt_listen_port() {
  local port
  port=$(read_tty_line 'Web interface port [8080]: ') || die 'Could not read web interface port'
  port=${port:-8080}
  validate_web_port "$port" || die 'Invalid web port: use 1024-65535 (the service runs unprivileged and cannot bind ports below 1024)'
  LISTEN="0.0.0.0:$port"
  validate_listen "$LISTEN" || die 'Invalid selected web port'
}
choose_listen_port() {
  # Existing installations keep the saved binding unless --listen was given.
  # Unattended installs (or no terminal) use the default 0.0.0.0:8080 without prompting.
  if [[ "$LISTEN_PROVIDED" == true || -f "$CONFIG_DIR/service.env" ]] || ! is_interactive; then
    return 0
  fi
  prompt_listen_port
}
# Existing installs keep their saved binding unless --listen was given. Called during
# pre-flight so a bad saved value stops the run before any host change.
load_existing_listen() {
  local file="$CONFIG_DIR/service.env" current
  if [[ -f "$file" && "$LISTEN_PROVIDED" != true ]]; then
    current=$(sed -n 's/^APC_LISTEN=//p' "$file" | head -n 1)
    validate_listen "$current" || die "Invalid existing $file listening address: '$current' (need IPv4:port, port 1024-65535). Fix it or pass --listen."
    LISTEN="$current"
  fi
  return 0
}
set_listen_config() {
  local file="$CONFIG_DIR/service.env"
  if [[ -f "$file" && "$LISTEN_PROVIDED" != true ]]; then
    # Preserve user-edited service settings on upgrade.
    load_existing_listen
    return
  fi
  printf 'APC_LISTEN=%s\n' "$LISTEN" > "${file}.tmp"
  chown "$SERVICE_USER:$SERVICE_USER" "${file}.tmp"
  chmod 0600 "${file}.tmp"
  mv -f "${file}.tmp" "$file"
}
# URLs to print at the end. $1 = HOST:PORT, $2 = whitespace-separated host addresses.
access_urls() {
  local listen="$1" addresses="$2" host port ip found=false
  local -a ips
  host=${listen%:*}; port=${listen##*:}
  if [[ "$host" == 0.0.0.0 ]]; then
    read -r -a ips <<< "$addresses" || true
    for ip in "${ips[@]+"${ips[@]}"}"; do
      [[ "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || continue
      printf 'http://%s:%s/\n' "$ip" "$port"
      found=true
    done
    [[ "$found" == true ]] || printf 'http://<this-host-ip>:%s/\n' "$port"
  else
    printf 'http://%s:%s/\n' "$host" "$port"
  fi
}

# --- initial password ----------------------------------------------------------
# Collected BEFORE the long build so typos fail fast; applied after the binary is installed.
collect_initial_password() {
  local a='' b=''
  if [[ "$PASSWORD_STDIN" == true ]]; then
    IFS= read -r a || [[ -n "$a" ]] || die 'Failed to read password from stdin (a piped script cannot also supply the password; save the script to a file first)'
  elif is_interactive; then
    a=$(read_tty_line 'Choose an administrator password (at least 12 characters): ' true) || die 'Could not read password'
    b=$(read_tty_line 'Repeat administrator password: ' true) || die 'Could not read password'
    [[ "$a" == "$b" ]] || die 'Password confirmation did not match'
  else
    die 'No admin password exists. Re-run with --password-stdin or run where a terminal (/dev/tty) is available.'
  fi
  validate_password "$a" || die 'Password must be a single line of at least 12 characters'
  INITIAL_PASSWORD=$a
}
apply_initial_password() {
  local binary="$1"
  # The service user cannot traverse root's build dir, so this runs the installed binary.
  if ! printf '%s\n' "$INITIAL_PASSWORD" | (cd / && runuser -u "$SERVICE_USER" -- "$binary" --auth-file "$STATE_DIR/auth.json" --set-password-stdin); then
    INITIAL_PASSWORD=''
    rollback_install 'Could not set the administrator password.'
    die 'Could not set the administrator password (see the error above). Nothing was started; fix the problem and re-run the installer.'
  fi
  INITIAL_PASSWORD=''
}

# --- start check and rollback ----------------------------------------------------
service_restarts() { systemctl show -p NRestarts --value "$SERVICE" 2>/dev/null </dev/null || true; }
# Type=simple reports "active" the moment the process forks, so watch the unit for a few
# seconds: fail if it leaves "active" (crashed, e.g. port already in use) or restarts.
wait_for_service_stable() {
  local i baseline now
  baseline=$(service_restarts)
  for ((i = 0; i < SERVICE_SETTLE_SECONDS; i++)); do
    sleep 1
    systemctl is-active --quiet "$SERVICE" </dev/null || return 1
    now=$(service_restarts)
    [[ "$now" == "$baseline" ]] || return 1
  done
  systemctl is-active --quiet "$SERVICE" </dev/null
}
# restore_file BACKUP DEST: copy via temp file + rename, safe for a running executable.
restore_file() {
  local backup="$1" dest="$2" tmp
  tmp=$(mktemp "${dest}.XXXXXX")
  cp -p "$backup" "$tmp"
  mv -f "$tmp" "$dest"
}
# Undo the binary/unit/service.env changes. Flags and backups come from main (ROLLBACK_*).
rollback_install() {
  info "$1 Restoring previous binary/unit/settings where available."
  systemctl stop "$SERVICE" </dev/null >/dev/null 2>&1 || true
  if [[ "$ROLLBACK_HAD_BINARY" == true ]]; then restore_file "$WORKDIR/previous.binary" "$PREFIX/$APP"; else rm -f "$PREFIX/$APP"; fi
  if [[ "$ROLLBACK_HAD_UNIT" == true ]]; then
    restore_file "$WORKDIR/previous.service" "$UNIT_DIR/$SERVICE"
  else
    # Drop the enable symlink before removing the unit so nothing dangles.
    systemctl disable "$SERVICE" </dev/null >/dev/null 2>&1 || true
    rm -f "$UNIT_DIR/$SERVICE"
  fi
  if [[ "$ROLLBACK_HAD_ENV" == true ]]; then restore_file "$WORKDIR/previous.service.env" "$CONFIG_DIR/service.env"; else rm -f "$CONFIG_DIR/service.env"; fi
  systemctl daemon-reload </dev/null || true
  if [[ "$ROLLBACK_HAD_BINARY" == true ]]; then systemctl restart "$SERVICE" </dev/null || true; fi
}

# --- build from source -------------------------------------------------------------
cleanup() {
  if [[ -n "${WORKDIR:-}" && -d "$WORKDIR" ]]; then rm -rf -- "$WORKDIR"; fi
  return 0
}
is_source_tree() {
  [[ -f "$1/go.mod" && -d "$1/cmd/$APP" ]] \
    && grep -Eq "^module[[:space:]]+${REPO_MODULE//./\\.}[[:space:]]*\$" "$1/go.mod"
}
# Prints the checkout root when this script is run from inside a checkout of the project.
find_local_source() {
  local script="${BASH_SOURCE[0]:-}" root
  [[ -n "$script" && -f "$script" ]] || return 1
  root=$(cd -- "$(dirname -- "$script")/.." 2>/dev/null && pwd -P) || return 1
  is_source_tree "$root" || return 1
  printf '%s\n' "$root"
}
download_source() {
  local dest="$1" archive="$WORKDIR/source.tar.gz"
  local url="https://codeload.github.com/${REPO}/tar.gz/refs/heads/${BRANCH}"
  info "Downloading source for branch ${BRANCH} from GitHub"
  curl --fail --location --silent --show-error --retry 2 "$url" -o "$archive" \
    || die "Could not download $url (branch typo, no network, or the repository is private: clone it and run scripts/install.sh from the checkout)"
  mkdir -p "$dest"
  tar -xzf "$archive" -C "$dest" --strip-components=1 --no-same-owner </dev/null || die 'Source archive is malformed'
  rm -f "$archive"
  is_source_tree "$dest" || die 'Downloaded archive does not look like the APC UPS Bridge source tree'
}
prepare_source() {
  local local_src=''
  local_src=$(find_local_source) || local_src=''
  if [[ -n "$local_src" ]]; then
    SOURCE_DIR=$local_src
    info "Building from checkout at $SOURCE_DIR"
  else
    SOURCE_DIR="$WORKDIR/src"
    download_source "$SOURCE_DIR"
  fi
}
build_version() {
  local ver=''
  if [[ "$SOURCE_DIR" != "$WORKDIR"/* ]] && command -v git >/dev/null 2>&1; then
    ver=$(git -c safe.directory="$SOURCE_DIR" -C "$SOURCE_DIR" describe --always --dirty 2>/dev/null </dev/null) || ver=''
  fi
  [[ -n "$ver" ]] || ver="source-${BRANCH//\//-}-$(date -u +%Y%m%d)"
  # Keep it safe for the linker flag string.
  printf '%s' "$ver" | tr -c 'A-Za-z0-9._+-' '-'
}
init_go_env() {
  GO_ENV=(env GOTOOLCHAIN=local CGO_ENABLED=0 GOWORK=off GOFLAGS=-modcacherw
    "GOCACHE=$WORKDIR/gocache" "GOPATH=$WORKDIR/gopath" "GOMODCACHE=$WORKDIR/gopath/pkg/mod")
}
find_usable_go() {
  local candidate version
  for candidate in "$(command -v go 2>/dev/null || true)" /usr/local/go/bin/go; do
    [[ -n "$candidate" && -x "$candidate" ]] || continue
    version=$("${GO_ENV[@]}" "$candidate" env GOVERSION 2>/dev/null </dev/null) || continue
    if go_version_ok "$version"; then
      GO_BIN=$candidate
      info "Using existing Go toolchain ($version at $candidate)"
      return 0
    fi
  done
  return 1
}
download_go() {
  local goarch ver url archive="$WORKDIR/go.tar.gz" expected actual
  goarch=$(go_arch_name "$(uname -m)" "$(getconf LONG_BIT 2>/dev/null || echo 64)") || die "Unsupported CPU architecture: $(uname -m)"
  ver=$(curl --fail --location --silent --show-error --retry 2 'https://go.dev/VERSION?m=text') || die 'Could not look up the current Go version from go.dev'
  ver=${ver%%$'\n'*}; ver=${ver%$'\r'}
  valid_go_release_name "$ver" || die "Unexpected Go version string from go.dev: $ver"
  go_version_ok "$ver" || die "go.dev reported an unusable Go version: $ver"
  url="https://dl.google.com/go/${ver}.linux-${goarch}.tar.gz"
  info "No Go 1.22+ found; downloading temporary toolchain ${ver} (linux/${goarch}). It is removed after the build."
  curl --fail --location --silent --show-error --retry 2 "$url" -o "$archive" || die 'Could not download the Go toolchain'
  expected=$(curl --fail --location --silent --show-error --retry 2 "${url}.sha256") || die 'Could not download the Go toolchain checksum'
  expected=${expected%%[[:space:]]*}
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || die 'Invalid Go toolchain SHA-256 from dl.google.com'
  actual=$(sha256sum "$archive"); actual=${actual%% *}
  [[ "${actual,,}" == "${expected,,}" ]] || die 'Go toolchain checksum mismatch'
  mkdir -p "$WORKDIR/gotoolchain"
  tar -xzf "$archive" -C "$WORKDIR/gotoolchain" --no-same-owner </dev/null || die 'Go toolchain archive is malformed'
  rm -f "$archive"
  GO_BIN="$WORKDIR/gotoolchain/go/bin/go"
  [[ -x "$GO_BIN" ]] || die 'Go toolchain archive did not contain bin/go'
  "${GO_ENV[@]}" "$GO_BIN" version >/dev/null </dev/null || die 'Downloaded Go toolchain cannot run on this host'
}
build_binary() {
  local ver="$1" out="$WORKDIR/$APP"
  info 'Building from source. A small Raspberry Pi can take several minutes; please wait.'
  info "Build version: $ver"
  (cd "$SOURCE_DIR" && "${GO_ENV[@]}" "$GO_BIN" build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=$ver" -o "$out" ./cmd/apc-ups-bridge </dev/null) \
    || die 'Build failed'
  [[ -s "$out" ]] || die 'Build produced no binary'
  chmod 0755 "$out"
  "$out" --version >/dev/null </dev/null || die 'Built binary cannot execute on this host'
  (cd "$SOURCE_DIR" && "${GO_ENV[@]}" "$GO_BIN" build -trimpath -buildvcs=false -o "$WORKDIR/apc-ups-bridge-host-helper" ./cmd/apc-ups-bridge-host-helper </dev/null) || die 'Host settings helper build failed'
}

main() {
  parse_args "$@"
  [[ "$(uname -s)" == Linux ]] || die 'Only Linux is supported'
  [[ "$EUID" -eq 0 ]] || die 'Run with sudo/root privileges'
  require_cmd systemctl; require_cmd sha256sum; require_cmd tar; require_cmd gzip; require_cmd curl
  require_cmd awk; require_cmd mktemp; require_cmd install; require_cmd runuser; require_cmd grep
  [[ -d /run/systemd/system ]] || die 'This host is not booted with systemd'
  [[ -d "$BUILD_PARENT" ]] || die "$BUILD_PARENT does not exist"

  # Pre-flight: reject a bad saved listen address, then ask every question, all before the
  # long build/download or any change to the host.
  load_existing_listen
  choose_listen_port
  if [[ ! -f "$STATE_DIR/auth.json" ]]; then collect_initial_password; fi

  # Build dir lives on disk (/var/tmp), not tmpfs /tmp. Removed on exit, with any downloaded Go.
  WORKDIR=$(mktemp -d -p "$BUILD_PARENT" apc-ups-bridge-build.XXXXXX)
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  init_go_env
  prepare_source
  find_usable_go || download_go
  local version_label staged_bin
  version_label=$(build_version)
  build_binary "$version_label"

  # Back up what the rollback may need to restore, before touching anything.
  if [[ -f "$UNIT_DIR/$SERVICE" ]]; then ROLLBACK_HAD_UNIT=true; cp -p "$UNIT_DIR/$SERVICE" "$WORKDIR/previous.service"; fi
  if [[ -f "$PREFIX/$APP" ]]; then ROLLBACK_HAD_BINARY=true; cp -p "$PREFIX/$APP" "$WORKDIR/previous.binary"; fi
  if [[ -f "$CONFIG_DIR/service.env" ]]; then ROLLBACK_HAD_ENV=true; cp -p "$CONFIG_DIR/service.env" "$WORKDIR/previous.service.env"; fi

  # The host is only modified below this line.
  install_apcupsd_if_missing
  local configure_apc=false
  if should_configure_apcupsd; then configure_apc=true; fi
  if [[ "$configure_apc" == true ]]; then
    if [[ "$SETUP_APC" != true ]]; then info 'Fresh apcupsd install: configuring it for a USB UPS.'; fi
    configure_apcupsd
    verify_apcupsd_communication
  else
    info 'Existing apcupsd configuration left unchanged.'
  fi
  ensure_user
  set_listen_config
  staged_bin=$(mktemp "$PREFIX/.${APP}.XXXXXX")
  install -m 0755 "$WORKDIR/$APP" "$staged_bin"
  mv -f "$staged_bin" "$PREFIX/$APP"
  if [[ ! -f "$STATE_DIR/auth.json" ]]; then
    apply_initial_password "$PREFIX/$APP"
  else
    info 'Preserving existing administrator password hash.'
  fi
  write_service
  # Install root-owned, narrowly scoped socket helper without granting root
  # permissions or extra capabilities to the web process.
  install -d -m 0755 /usr/local/libexec
  install -o root -g root -m 0755 "$WORKDIR/apc-ups-bridge-host-helper" /usr/local/libexec/apc-ups-bridge-host-helper
  install -o root -g root -m 0644 "$SOURCE_DIR/packaging/systemd/apc-ups-bridge-host-settings.socket" "$UNIT_DIR/apc-ups-bridge-host-settings.socket"
  install -o root -g root -m 0644 "$SOURCE_DIR/packaging/systemd/apc-ups-bridge-host-settings.service" "$UNIT_DIR/apc-ups-bridge-host-settings.service"
  systemctl daemon-reload
  systemctl enable --now apc-ups-bridge-host-settings.socket
  systemctl enable "$SERVICE"
  if [[ "$SKIP_START" == false ]]; then
    info 'Starting the service and watching it for a few seconds...'
    if ! systemctl restart "$SERVICE" </dev/null || ! wait_for_service_stable; then
      rollback_install 'Service failed to start or stay running (is the port already used by another service?).'
      die 'Service failed; check journalctl -u apc-ups-bridge.service'
    fi
  fi
  if [[ -f "$SOURCE_DIR/scripts/uninstall.sh" ]]; then
    install -d -m 0755 "$(dirname "$UNINSTALL_BIN")"
    install -m 0755 "$SOURCE_DIR/scripts/uninstall.sh" "$UNINSTALL_BIN"
  else
    info "WARNING: scripts/uninstall.sh not found in the source tree; $UNINSTALL_BIN was not installed."
  fi

  local ips='' url
  ips=$(hostname -I 2>/dev/null || true)
  info 'Installation complete; service enabled at boot.'
  if [[ "$SKIP_START" == true ]]; then info 'Service was not started (--skip-start): systemctl start apc-ups-bridge.service'; fi
  while IFS= read -r url; do info "Open $url"; done < <(access_urls "$LISTEN" "$ips")
  if [[ "${LISTEN%:*}" == 127.0.0.1 ]]; then
    info 'Bound to this host only. For LAN access re-run the installer with --listen 0.0.0.0:<port>.'
  fi
  info 'The web interface is plain HTTP, intended for your home LAN. Never port-forward it to the internet.'
  info "To update, run the same install command again. To remove: sudo $(basename "$UNINSTALL_BIN") [--purge]"
  info 'Before depending on auto power actions, validate SSH, WoL and apcupsd thresholds on real hardware.'
}
if [[ "${UPS_BRIDGE_LIB_ONLY:-0}" != 1 ]]; then main "$@"; fi
