#!/usr/bin/env bash
# Unprivileged installer tests: no user, package, service, or /etc modifications.
set -Eeuo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
export UPS_BRIDGE_LIB_ONLY=1
# shellcheck disable=SC1091
source "$ROOT/scripts/install.sh"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
for pair in 'x86_64 amd64' 'aarch64 arm64' 'armv7l armv7' 'armv6l armv6'; do
  read -r a b <<< "$pair"
  [[ $(arch_name "$a") == "$b" ]] || exit 1
done
if arch_name unknown >/dev/null; then exit 1; fi
for bind in '127.0.0.1:8080' '0.0.0.0:8080' '192.168.1.10:65535'; do
  validate_listen "$bind"
done
for bind in 'foo:8080' '192.168.1.999:8080' '0.0.0.0:0' '0.0.0.0:65536' '0.0.0.0:88;whoami' '127.0.0.1:08'; do
  if validate_listen "$bind"; then echo "Bad bind accepted: $bind" >&2; exit 1; fi
done
# Prompt validation and non-interactive/upgrade preservation.
for port in 1 8080 9090 65535; do validate_web_port "$port"; done
for port in 0 65536 08 -1 abc '80;id' ' 8080' ''; do
  if validate_web_port "$port"; then echo "Bad port accepted: $port" >&2; exit 1; fi
done
LISTEN=127.0.0.1:8080
LISTEN_PROVIDED=false
NONINTERACTIVE=true
CONFIG_DIR="$stage/config"
mkdir -p "$CONFIG_DIR"
choose_listen_port
[[ "$LISTEN" == "127.0.0.1:8080" ]]
LISTEN=127.0.0.1:9090
LISTEN_PROVIDED=true
choose_listen_port
[[ "$LISTEN" == "127.0.0.1:9090" ]]
LISTEN_PROVIDED=false
printf 'APC_LISTEN=127.0.0.1:8888\n' > "$CONFIG_DIR/service.env"
choose_listen_port
set_listen_config
[[ "$LISTEN" == "127.0.0.1:8888" ]]
grep -q '^APC_LISTEN=127.0.0.1:8888$' "$CONFIG_DIR/service.env"
rm "$CONFIG_DIR/service.env"
LISTEN=127.0.0.1:8080
printf '\n' | prompt_listen_port
[[ "$LISTEN" == "127.0.0.1:8080" ]]
printf '9090\n' | prompt_listen_port
[[ "$LISTEN" == "127.0.0.1:9090" ]]
if (printf '65536\n' | prompt_listen_port) >/dev/null 2>&1; then
  echo 'Invalid interactive port accepted' >&2; exit 1
fi
printf 'binary\n' > "$stage/apc-ups-bridge"
tar -C "$stage" -czf "$stage/test.tar.gz" apc-ups-bridge
(cd "$stage" && sha256sum test.tar.gz > SHA256SUMS)
verify_release "$stage/SHA256SUMS" "$stage/test.tar.gz" test.tar.gz
printf '\ncorruption\n' >> "$stage/test.tar.gz"
if (verify_release "$stage/SHA256SUMS" "$stage/test.tar.gz" test.tar.gz >/dev/null 2>&1); then
  echo 'Failed to reject checksum mismatch' >&2; exit 1
fi
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
[[ $(grep -c '^UPSCABLE usb$' "$stage/apcupsd.conf") == 1 ]]
grep -q '^BATTERYLEVEL 5$' "$stage/apcupsd.conf"
grep -q '^# preserved comment$' "$stage/apcupsd.conf"
printf 'ISCONFIGURED=no\n# retained\n' > "$stage/apcupsd.default"
set_apc_default "$stage/apcupsd.default"
grep -q '^ISCONFIGURED=yes$' "$stage/apcupsd.default"
grep -q '^# retained$' "$stage/apcupsd.default"
echo 'Installer unit tests passed (no system changes)'
