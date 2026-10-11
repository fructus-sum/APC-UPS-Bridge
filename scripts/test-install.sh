#!/usr/bin/env bash
# Unprivileged installer tests: no user, package, service, or /etc modifications.
set -Eeuo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
export UPS_BRIDGE_LIB_ONLY=1
# shellcheck disable=SC1091
source "$ROOT/scripts/install.sh"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
must_fail() { if ( "$@" ) >/dev/null 2>&1; then fail "unexpectedly succeeded: $*"; fi; }

# --- Go architecture mapping (official tarball names) ---
for pair in 'x86_64 amd64' 'amd64 amd64' 'aarch64 arm64' 'arm64 arm64' 'armv7l armv6l' 'armv6l armv6l' \
            'armv8l armv6l' 'armv7foo armv6l' 'armv6foo armv6l'; do
  read -r a b <<< "$pair"
  [[ $(go_arch_name "$a") == "$b" ]] || fail "go_arch_name $a"
done
# 64-bit kernel with a 32-bit userland must get the 32-bit ARM toolchain.
[[ $(go_arch_name aarch64 32) == armv6l ]] || fail 'aarch64 with 32-bit userland'
[[ $(go_arch_name aarch64 64) == arm64 ]] || fail 'aarch64 with 64-bit userland'
must_fail go_arch_name riscv64
must_fail go_arch_name i686
must_fail go_arch_name ''

# --- Go version comparison (minimum 1.22) ---
for v in go1.22 go1.22.0 go1.22.5 go1.23rc1 go1.24.1 go1.100.0 go2.0; do
  go_version_ok "$v" || fail "go_version_ok rejected $v"
done
for v in go1.21.9 go1.9 go1.2 go1 go0.99 '' devel go-1.23 'devel go1.23-abc' unknown; do
  if go_version_ok "$v"; then fail "go_version_ok accepted '$v'"; fi
done
for v in go1.24.1 go1.22 go1.22.10; do valid_go_release_name "$v" || fail "release name rejected $v"; done
for v in go1.24rc1 go1.24.1beta go1.24.1.2 'go1.24.1 x' '' 'devel'; do
  if valid_go_release_name "$v"; then fail "release name accepted '$v'"; fi
done

# --- branch name validation ---
for b in main develop fix/install-and-safety-improvements release-1.2 feature/a.b_c v1.0.0; do
  validate_branch "$b" || fail "branch rejected: $b"
done
for b in '' '-x' '.hidden' '/main' 'a/' 'a//b' 'a/../b' '..' 'main;id' 'a b' 'a$(id)' 'a`id`' 'a|b' 'a&b' 'a:b' "a'b" 'a"b' 'ma*in' 'branch.'; do
  if validate_branch "$b"; then fail "branch accepted: '$b'"; fi
done
long=$(printf 'a%.0s' {1..101}); if validate_branch "$long"; then fail 'overlong branch accepted'; fi
(parse_args --branch fix/x) || fail 'parse_args --branch'
must_fail bash -c 'UPS_BRIDGE_LIB_ONLY=1 source "$1"; parse_args --branch "a;b"' _ "$ROOT/scripts/install.sh"

# --- option parsing: release options are gone, conflicts are rejected ---
must_fail bash -c 'UPS_BRIDGE_LIB_ONLY=1 source "$1"; parse_args --version v1.0.0' _ "$ROOT/scripts/install.sh"
must_fail bash -c 'UPS_BRIDGE_LIB_ONLY=1 source "$1"; parse_args --configure-apcupsd --skip-apcupsd-setup' _ "$ROOT/scripts/install.sh"
must_fail bash -c 'UPS_BRIDGE_LIB_ONLY=1 source "$1"; parse_args --host-protection' _ "$ROOT/scripts/install.sh"
must_fail bash -c 'UPS_BRIDGE_LIB_ONLY=1 source "$1"; parse_args --listen 1.2.3.4:99999' _ "$ROOT/scripts/install.sh"
for gone in verify_release download_release arch_name validate_version; do
  if declare -F "$gone" >/dev/null; then fail "$gone should have been removed"; fi
done

# --- listen address validation ---
for bind in '127.0.0.1:8080' '0.0.0.0:8080' '192.168.1.10:65535' '0.0.0.0:1024'; do
  validate_listen "$bind" || fail "bind rejected: $bind"
done
for bind in 'foo:8080' '192.168.1.999:8080' '0.0.0.0:0' '0.0.0.0:1' '0.0.0.0:80' '0.0.0.0:443' '0.0.0.0:1023' '0.0.0.0:65536' '0.0.0.0:88;whoami' '127.0.0.1:08'; do
  if validate_listen "$bind"; then fail "Bad bind accepted: $bind"; fi
done

# --- web port validation ---
for port in 1024 8080 9090 65535; do validate_web_port "$port" || fail "port rejected $port"; done
for port in 0 1 80 443 1023 65536 08 -1 abc '80;id' ' 8080' ''; do
  if validate_web_port "$port"; then fail "Bad port accepted: $port"; fi
done

# --- tty / interactive decision (a regular file stands in for /dev/tty) ---
TTY_DEVICE="$stage/no-such-tty"
if has_tty; then fail 'has_tty true without a tty'; fi
NONINTERACTIVE=false
if is_interactive; then fail 'interactive without a tty'; fi
TTY_DEVICE="$stage/tty"
: > "$TTY_DEVICE"
has_tty || fail 'has_tty false with readable device'
is_interactive || fail 'not interactive with a tty'
NONINTERACTIVE=true
if is_interactive; then fail '--non-interactive ignored'; fi
NONINTERACTIVE=false
# Prompt input comes from the tty device, never from stdin (which is the script when piped).
printf '9090\n' > "$TTY_DEVICE"
[[ $(read_tty_line 'x: ' 2>/dev/null </dev/null) == 9090 ]] || fail 'read_tty_line must read the tty device'

# --- LAN default and port prompt ---
[[ "$DEFAULT_LISTEN" == 0.0.0.0:8080 && "$LISTEN" == 0.0.0.0:8080 ]] || fail 'default listen is not 0.0.0.0:8080'
CONFIG_DIR="$stage/config"
mkdir -p "$CONFIG_DIR"
LISTEN_PROVIDED=false
# No terminal: keep the LAN default, never prompt.
TTY_DEVICE="$stage/no-such-tty"
choose_listen_port </dev/null
[[ "$LISTEN" == 0.0.0.0:8080 ]] || fail 'no-tty listen changed'
# Terminal available: prompt, bind all interfaces on the chosen port.
TTY_DEVICE="$stage/tty"
printf '\n' > "$TTY_DEVICE"
choose_listen_port 2>/dev/null </dev/null
[[ "$LISTEN" == 0.0.0.0:8080 ]] || fail 'Enter should keep 8080'
printf '9090\n' > "$TTY_DEVICE"
choose_listen_port 2>/dev/null </dev/null
[[ "$LISTEN" == 0.0.0.0:9090 ]] || fail 'prompted port not applied'
printf '65536\n' > "$TTY_DEVICE"
must_fail prompt_listen_port
printf '80\n' > "$TTY_DEVICE"
must_fail prompt_listen_port
# --non-interactive never prompts even with a terminal.
LISTEN=0.0.0.0:8080; NONINTERACTIVE=true
printf '9999\n' > "$TTY_DEVICE"
choose_listen_port
[[ "$LISTEN" == 0.0.0.0:8080 ]] || fail 'prompted despite --non-interactive'
NONINTERACTIVE=false
# Explicit --listen wins.
LISTEN=127.0.0.1:9090; LISTEN_PROVIDED=true
choose_listen_port 2>/dev/null
[[ "$LISTEN" == 127.0.0.1:9090 ]] || fail '--listen overridden'
# Upgrades keep the saved binding.
LISTEN_PROVIDED=false; LISTEN=0.0.0.0:8080
printf 'APC_LISTEN=127.0.0.1:8888\n' > "$CONFIG_DIR/service.env"
printf '7777\n' > "$TTY_DEVICE"
choose_listen_port 2>/dev/null
[[ "$LISTEN" == 0.0.0.0:8080 ]] || fail 'prompted during upgrade'
set_listen_config
[[ "$LISTEN" == 127.0.0.1:8888 ]] || fail 'existing listen not preserved'
grep -q '^APC_LISTEN=127.0.0.1:8888$' "$CONFIG_DIR/service.env" || fail 'service.env rewritten'
rm "$CONFIG_DIR/service.env"

# --- URLs printed at the end ---
[[ $(access_urls 0.0.0.0:8080 '192.168.1.20 fe80::1 10.0.0.5') == $'http://192.168.1.20:8080/\nhttp://10.0.0.5:8080/' ]] || fail 'access_urls LAN'
[[ $(access_urls 0.0.0.0:9090 '') == 'http://<this-host-ip>:9090/' ]] || fail 'access_urls fallback'
[[ $(access_urls 0.0.0.0:9090 'fe80::1 ::1') == 'http://<this-host-ip>:9090/' ]] || fail 'access_urls ipv6 only'
[[ $(access_urls 127.0.0.1:8080 '192.168.1.20') == 'http://127.0.0.1:8080/' ]] || fail 'access_urls loopback'
[[ $(access_urls 192.168.1.5:8081 '10.0.0.5') == 'http://192.168.1.5:8081/' ]] || fail 'access_urls specific'

# --- initial password collection ---
validate_password 'twelve-chars!' || fail 'good password rejected'
for p in short '' 'elevenchars' $'line\nbreak-long-enough' $'cr\rlong-enough-pw'; do
  if validate_password "$p"; then fail "bad password accepted"; fi
done
STATE_DIR="$stage/state"
PASSWORD_STDIN=true; NONINTERACTIVE=true
INITIAL_PASSWORD=''
collect_initial_password <<< 'a-long-enough-password'
[[ "$INITIAL_PASSWORD" == a-long-enough-password ]] || fail '--password-stdin path'
must_fail collect_initial_password <<< 'tiny'
PASSWORD_STDIN=false
must_fail collect_initial_password </dev/null   # non-interactive without --password-stdin
NONINTERACTIVE=false
TTY_DEVICE="$stage/tty"
: > "$TTY_DEVICE"
# A regular file cannot hand out successive lines to successive opens, so stub the reader.
tty_queue="$stage/tty-queue"
read_tty_line_real=$(declare -f read_tty_line)
read_tty_line() { local l; l=$(head -n 1 "$tty_queue"); sed -i 1d "$tty_queue"; printf '%s' "$l"; }
printf 'a-long-enough-password\na-long-enough-password\n' > "$tty_queue"
INITIAL_PASSWORD=''
collect_initial_password 2>/dev/null </dev/null
[[ "$INITIAL_PASSWORD" == a-long-enough-password ]] || fail 'tty password path'
printf 'a-long-enough-password\nsomething-else-entirely\n' > "$tty_queue"
must_fail collect_initial_password
printf 'short\nshort\n' > "$tty_queue"
must_fail collect_initial_password
eval "$read_tty_line_real"
TTY_DEVICE="$stage/no-such-tty"
must_fail collect_initial_password </dev/null
INITIAL_PASSWORD=''

# --- source tree detection ---
is_source_tree "$ROOT" || fail 'repo root not recognised as source tree'
[[ $(find_local_source) == "$(cd "$ROOT" && pwd -P)" ]] || fail 'find_local_source from checkout'
mkdir -p "$stage/fake/cmd/apc-ups-bridge"
printf 'module example.com/other\n\ngo 1.22\n' > "$stage/fake/go.mod"
if is_source_tree "$stage/fake"; then fail 'foreign module accepted'; fi
printf 'module github.com/fructus-sum/APC-UPS-Bridge\r\n\r\ngo 1.22\r\n' > "$stage/fake/go.mod"
is_source_tree "$stage/fake" || fail 'CRLF go.mod rejected'
printf 'module github.com/fructus-sum/APC-UPS-Bridgeevil\n' > "$stage/fake/go.mod"
if is_source_tree "$stage/fake"; then fail 'module prefix match accepted'; fi
printf 'module github.com/fructusXsum/APC-UPS-Bridge\n' > "$stage/fake/go.mod"
if is_source_tree "$stage/fake"; then fail 'dot in module name treated as wildcard'; fi
# A piped script has no BASH_SOURCE file: must fall back to downloading.
piped=$({ cat "$ROOT/scripts/install.sh"; echo 'if find_local_source >/dev/null; then echo local; else echo download; fi'; } | UPS_BRIDGE_LIB_ONLY=1 bash)
[[ "$piped" == download ]] || fail "piped script should not find a local tree (got $piped)"

# --- apcupsd: key editing ---
cat > "$stage/apcupsd.conf" <<'CONFIG'
UPSCABLE smart
UPSTYPE apcsmart
BATTERYLEVEL 5
# preserved comment
NETSERVER off
CONFIG
set_apc_key "$stage/apcupsd.conf" UPSCABLE usb
set_apc_key "$stage/apcupsd.conf" UPSCABLE usb
set_apc_key "$stage/apcupsd.conf" NETSERVER on
[[ $(grep -c '^UPSCABLE usb$' "$stage/apcupsd.conf") == 1 ]] || fail 'UPSCABLE duplicated'
grep -q '^BATTERYLEVEL 5$' "$stage/apcupsd.conf" || fail 'BATTERYLEVEL changed'
grep -q '^# preserved comment$' "$stage/apcupsd.conf" || fail 'comment lost'
printf 'ISCONFIGURED=no\n# retained\n' > "$stage/apcupsd.default"
set_apc_default "$stage/apcupsd.default"
grep -q '^ISCONFIGURED=yes$' "$stage/apcupsd.default" || fail 'ISCONFIGURED not set'
grep -q '^# retained$' "$stage/apcupsd.default" || fail 'default file comment lost'

# --- apcupsd: should_configure_apcupsd decision matrix ---
CONFIG_DIR="$stage/new-install-config"
PREFIX="$stage/new-install-bin"
mkdir -p "$CONFIG_DIR" "$PREFIX"
decide() { # SETUP_APC SKIP_AUTO_APC INSTALLED_BY_US HAVE_SERVICE_ENV HAVE_BINARY -> prints yes/no
  SETUP_APC=$1; SKIP_AUTO_APC=$2; APCUPS_INSTALLED_BY_US=$3
  rm -f "$CONFIG_DIR/service.env" "$PREFIX/$APP"
  if [[ $4 == true ]]; then : > "$CONFIG_DIR/service.env"; fi
  if [[ $5 == true ]]; then : > "$PREFIX/$APP"; fi
  if should_configure_apcupsd; then echo yes; else echo no; fi
}
#       setup skip  byUs  env   bin   expected
while read -r s k u e b want; do
  [[ $(decide $s $k $u $e $b) == "$want" ]] || fail "should_configure_apcupsd($s $k $u $e $b) != $want"
done <<'MATRIX'
false false true  false false yes
false false false false false no
false false true  true  false no
false false true  false true  no
false false true  true  true  no
false true  true  false false no
false true  false false false no
true  false false false false yes
true  false false true  true  yes
true  false true  true  true  yes
true  false true  false false yes
MATRIX
rm -f "$CONFIG_DIR/service.env" "$PREFIX/$APP"

# --- apcupsd: communication check (fake apcaccess; warns, never fails) ---
mkdir -p "$stage/fakebin"
cat > "$stage/fakebin/apcaccess" <<'FAKE'
#!/usr/bin/env bash
case "${FAKE_APC:-ok}" in
  ok) printf 'APC      : 001,036,0865\nSTATUS   : ONLINE\nLINEV    : 230.0 Volts\n';;
  commlost) printf 'APC      : 001,036,0865\nSTATUS   : COMMLOST\n';;
  nostatus) printf 'APC      : 001,036,0865\nMODEL    : UPS\n';;
  fail) echo 'Error contacting apcupsd' >&2; exit 1;;
esac
FAKE
chmod +x "$stage/fakebin/apcaccess"
OLD_PATH=$PATH
PATH="$stage/fakebin:$PATH"
out=$(FAKE_APC=ok verify_apcupsd_communication)
[[ "$out" == *'looks good'* ]] || fail 'verify did not report success'
for mode in commlost nostatus fail; do
  out=$(FAKE_APC=$mode verify_apcupsd_communication) || fail "verify must not fail ($mode)"
  [[ "$out" == *WARNING* ]] || fail "verify did not warn ($mode)"
done
PATH=$OLD_PATH

# --- saved listen address is validated in pre-flight ---
CONFIG_DIR="$stage/preflight"
mkdir -p "$CONFIG_DIR"
LISTEN_PROVIDED=false; LISTEN=0.0.0.0:8080
load_existing_listen   # no service.env: no-op
[[ "$LISTEN" == 0.0.0.0:8080 ]] || fail 'load_existing_listen changed LISTEN without service.env'
printf 'APC_LISTEN=192.168.1.5:9000\n' > "$CONFIG_DIR/service.env"
load_existing_listen
[[ "$LISTEN" == 192.168.1.5:9000 ]] || fail 'saved listen not loaded'
for bad in 'APC_LISTEN=nonsense' 'APC_LISTEN=0.0.0.0:80' 'APC_LISTEN=' 'OTHER=1'; do
  printf '%s\n' "$bad" > "$CONFIG_DIR/service.env"
  LISTEN=0.0.0.0:8080
  must_fail load_existing_listen
done
LISTEN_PROVIDED=true; LISTEN=0.0.0.0:7000
load_existing_listen   # --listen given: a bad saved value is irrelevant
[[ "$LISTEN" == 0.0.0.0:7000 ]] || fail '--listen overridden by saved value'
LISTEN_PROVIDED=false; LISTEN=0.0.0.0:8080
rm -f "$CONFIG_DIR/service.env"

# --- start check and rollback (fake systemctl / runuser) ---
mkdir -p "$stage/scbin" "$stage/sc"
cat > "$stage/scbin/systemctl" <<'FAKE'
#!/usr/bin/env bash
d="$FAKE_SC_DIR"
echo "$*" >> "$d/calls"
case "$1" in
  is-active)
    [[ "${FAKE_SC_MODE:-stable}" != inactive ]] ;;
  show)
    n=$(cat "$d/restarts" 2>/dev/null || echo 0)
    if [[ "${FAKE_SC_MODE:-stable}" == restarting ]]; then n=$((n + 1)); echo "$n" > "$d/restarts"; fi
    echo "$n" ;;
  *) : ;;
esac
FAKE
printf '#!/usr/bin/env bash\nexit 1\n' > "$stage/scbin/runuser"
chmod +x "$stage/scbin/systemctl" "$stage/scbin/runuser"
OLD_PATH=$PATH
PATH="$stage/scbin:$PATH"
export FAKE_SC_DIR="$stage/sc"
SERVICE_SETTLE_SECONDS=2
: > "$FAKE_SC_DIR/calls"; echo 0 > "$FAKE_SC_DIR/restarts"
FAKE_SC_MODE=stable wait_for_service_stable || fail 'stable service rejected'
: > "$FAKE_SC_DIR/calls"
if FAKE_SC_MODE=inactive wait_for_service_stable; then fail 'crashed service accepted'; fi
echo 0 > "$FAKE_SC_DIR/restarts"
if FAKE_SC_MODE=restarting wait_for_service_stable; then fail 'restarting service accepted'; fi

WORKDIR="$stage/rb-work"; PREFIX="$stage/rb-bin"; UNIT_DIR="$stage/rb-unit"; CONFIG_DIR="$stage/rb-conf"
mkdir -p "$WORKDIR" "$PREFIX" "$UNIT_DIR" "$CONFIG_DIR"
# Fresh install: everything new is removed, the unit is disabled first, nothing is restarted.
printf new > "$PREFIX/$APP"; printf new > "$UNIT_DIR/$SERVICE"; printf new > "$CONFIG_DIR/service.env"
ROLLBACK_HAD_BINARY=false; ROLLBACK_HAD_UNIT=false; ROLLBACK_HAD_ENV=false
: > "$FAKE_SC_DIR/calls"
rollback_install 'test.' >/dev/null
[[ ! -e "$PREFIX/$APP" && ! -e "$UNIT_DIR/$SERVICE" && ! -e "$CONFIG_DIR/service.env" ]] || fail 'fresh rollback left files'
grep -q "^disable $SERVICE" "$FAKE_SC_DIR/calls" || fail 'fresh rollback did not disable the unit'
if grep -q '^restart' "$FAKE_SC_DIR/calls"; then fail 'fresh rollback restarted a service'; fi
[[ $(grep -n '^disable' "$FAKE_SC_DIR/calls" | cut -d: -f1) -lt $(grep -n '^daemon-reload' "$FAKE_SC_DIR/calls" | cut -d: -f1) ]] || fail 'disable must precede daemon-reload'
# Upgrade: previous binary, unit and service.env come back; previous service is restarted.
printf old > "$WORKDIR/previous.binary"; printf old > "$WORKDIR/previous.service"; printf old > "$WORKDIR/previous.service.env"
printf new > "$PREFIX/$APP"; printf new > "$UNIT_DIR/$SERVICE"; printf new > "$CONFIG_DIR/service.env"
ROLLBACK_HAD_BINARY=true; ROLLBACK_HAD_UNIT=true; ROLLBACK_HAD_ENV=true
: > "$FAKE_SC_DIR/calls"
rollback_install 'test.' >/dev/null
for p in "$PREFIX/$APP" "$UNIT_DIR/$SERVICE" "$CONFIG_DIR/service.env"; do [[ $(cat "$p") == old ]] || fail "upgrade rollback did not restore $p"; done
grep -q "^restart $SERVICE" "$FAKE_SC_DIR/calls" || fail 'upgrade rollback did not restart the old service'
[[ -z $(ls -A "$PREFIX" | grep -v "^$APP\$") ]] || fail 'rollback left temp files'
# Password failure: clear message, installed files rolled back.
printf new > "$PREFIX/$APP"; printf new > "$CONFIG_DIR/service.env"
ROLLBACK_HAD_BINARY=false; ROLLBACK_HAD_UNIT=false; ROLLBACK_HAD_ENV=false
INITIAL_PASSWORD='a-long-enough-password'
out=$( (apply_initial_password "$PREFIX/$APP") 2>&1 ) && fail 'password failure not fatal'
[[ "$out" == *'Could not set the administrator password'* ]] || fail "unclear password failure message: $out"
[[ ! -e "$PREFIX/$APP" && ! -e "$CONFIG_DIR/service.env" ]] || fail 'password failure did not roll back'
PATH=$OLD_PATH
unset FAKE_SC_DIR

# --- installed unit matches the packaged unit ---
sed -n "/<<'UNIT'\$/,/^UNIT\$/p" "$ROOT/scripts/install.sh" | sed '1d;$d' | tr -d '\r' > "$stage/embedded.service"
tr -d '\r' < "$ROOT/packaging/systemd/apc-ups-bridge.service" > "$stage/packaged.service"
cmp -s "$stage/embedded.service" "$stage/packaged.service" || fail 'packaging/systemd unit differs from the unit embedded in install.sh'

# --- privileged helper installation contracts (no host modifications) ---
grep -q 'cmd/apc-ups-bridge-host-helper' "$ROOT/scripts/install.sh" || fail 'host settings helper not built'
grep -q 'apc-ups-bridge-host-settings.socket' "$ROOT/scripts/install.sh" || fail 'host settings socket not installed'
grep -q 'systemctl enable --now apc-ups-bridge-host-settings.socket' "$ROOT/scripts/install.sh" || fail 'host settings socket not enabled'
grep -q 'SocketMode=0600' "$ROOT/packaging/systemd/apc-ups-bridge-host-settings.socket" || fail 'host settings socket is not private'
grep -q 'NoNewPrivileges=true' "$ROOT/packaging/systemd/apc-ups-bridge-host-settings.service" || fail 'host settings helper not hardened'
grep -q 'apc-ups-bridge-host-settings.socket' "$ROOT/scripts/uninstall.sh" || fail 'host settings socket not uninstalled'

# --- script hygiene ---
[[ "$(tail -n 1 "$ROOT/scripts/install.sh" | tr -d '\r')" == *'main "$@"'* ]] || fail 'main must be invoked on the last line of install.sh'
[[ $(grep -c 'main "\$@"' "$ROOT/scripts/install.sh") == 1 ]] || fail 'main must be invoked exactly once'
if grep -En 'releases/|SHA256SUMS|-t 0' "$ROOT/scripts/install.sh" >/dev/null; then fail 'install.sh still references releases or -t 0'; fi
bash -n "$ROOT/scripts/uninstall.sh" || fail 'uninstall.sh syntax'
[[ "$(tail -n 1 "$ROOT/scripts/uninstall.sh")" == 'main "$@"' ]] || fail 'uninstall.sh must invoke main on its last line'
[[ $(bash "$ROOT/scripts/uninstall.sh" --help | head -n 1) == Usage:* ]] || fail 'uninstall --help'
bash "$ROOT/scripts/uninstall.sh" --bogus >/dev/null 2>&1 && fail 'uninstall accepted a bogus argument'
echo 'Installer unit tests passed (no system changes)'
