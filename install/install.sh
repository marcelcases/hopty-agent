#!/bin/sh
# Required release values are injected into the immutable installer artifact.
set -eu
umask 077

: "${HOPTY_VERSION:?HOPTY_VERSION is required}"
: "${HOPTY_SHA256_AMD64:?HOPTY_SHA256_AMD64 is required}"
: "${HOPTY_SHA256_ARM64:?HOPTY_SHA256_ARM64 is required}"
: "${HOPTY_SERVICE_URL:?HOPTY_SERVICE_URL is required}"

case "$(uname -s)" in Linux) ;; *) echo "Hopty supports Linux only" >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64; checksum=$HOPTY_SHA256_AMD64;; aarch64|arm64) arch=arm64; checksum=$HOPTY_SHA256_ARM64;; *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1;; esac

home=${HOME:?HOME is required}/.hopty
bin_dir=$home/bin
mkdir -p "$bin_dir" "$home/run"
chmod 700 "$home" "$bin_dir" "$home/run"
if [ ! -f "$home/config.toml" ]; then printf 'service_url = "%s"\n' "$HOPTY_SERVICE_URL" >"$home/config.toml"; fi
chmod 600 "$home/config.toml"

base_url=${HOPTY_RELEASE_BASE_URL:-https://github.com/marcelcases/hopty-agent/releases/download/$HOPTY_VERSION}
asset=hopty_linux_$arch
work=$(mktemp -d "${TMPDIR:-/tmp}/hopty.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

if command -v curl >/dev/null 2>&1; then curl --fail --location --proto '=https' --tlsv1.2 -o "$work/hopty" "$base_url/$asset"; elif command -v wget >/dev/null 2>&1; then wget -O "$work/hopty" "$base_url/$asset"; else echo "curl or wget is required" >&2; exit 1; fi
actual=$(sha256sum "$work/hopty" 2>/dev/null | awk '{print $1}' || shasum -a 256 "$work/hopty" | awk '{print $1}')
[ "$actual" = "$checksum" ] || { echo "Hopty checksum verification failed" >&2; exit 1; }
chmod 700 "$work/hopty"
mv -f "$work/hopty" "$bin_dir/hopty"

mkdir -p "$HOME/.config/systemd/user"
cat >"$HOME/.config/systemd/user/hopty.service" <<EOF
[Unit]
Description=Hopty agent
[Service]
ExecStart=$bin_dir/hopty agent
Restart=on-failure
RestartSec=2
[Install]
WantedBy=default.target
EOF

if systemctl --user daemon-reload >/dev/null 2>&1 && systemctl --user enable hopty.service >/dev/null 2>&1 && systemctl --user restart hopty.service >/dev/null 2>&1; then :; else nohup "$bin_dir/hopty" agent >"$home/agent.log" 2>&1 & fi
attempt=0
while :; do
  status=$("$bin_dir/hopty" status 2>/dev/null || true)
  case "$status" in *"connected=true"*) break;; esac
  attempt=$((attempt + 1))
  [ "$attempt" -lt 30 ] || { echo "Hopty agent did not connect within 30 seconds" >&2; exit 1; }
  sleep 1
done
case "$status" in *"paired=true"*) echo "Hopty agent is already paired.";; *) "$bin_dir/hopty" pair;; esac
printf '\nFor persistence after logout/reboot, run once:\n  sudo loginctl enable-linger %s\n' "$(id -un)"
