#!/bin/sh
# Required release values are injected into the immutable installer artifact.
set -eu
umask 077

: "${HOPTY_VERSION:?HOPTY_VERSION is required}"
: "${HOPTY_SHA256_AMD64:?HOPTY_SHA256_AMD64 is required}"
: "${HOPTY_SHA256_ARM64:?HOPTY_SHA256_ARM64 is required}"
: "${HOPTY_SERVICE_URL:?HOPTY_SERVICE_URL is required}"

if [ -t 1 ]; then
  accent=$(printf '\033[1;38;2;232;138;69m'); dim=$(printf '\033[2m'); cyan=$(printf '\033[1;96m'); reset=$(printf '\033[0m')
else
  accent= dim= cyan= reset=
fi
heading() { printf '\n%s╭─ Hopty%s\n%s│  One hop to your shell.%s\n%s╰─%s\n' "$accent" "$reset" "$dim" "$reset" "$accent" "$reset"; }
step() { printf '%s›%s %s\n' "$accent" "$reset" "$1"; }
success() { printf '%s✓%s %s\n' "$accent" "$reset" "$1"; }

case "$(uname -s)" in Linux) ;; *) echo "Hopty supports Linux only" >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64; checksum=$HOPTY_SHA256_AMD64;; aarch64|arm64) arch=arm64; checksum=$HOPTY_SHA256_ARM64;; *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1;; esac

home=${HOME:?HOME is required}/.hopty
bin_dir=$home/bin
local_bin=$HOME/.local/bin
mkdir -p "$bin_dir" "$home/run" "$home/tmp" "$local_bin"
chmod 700 "$home" "$bin_dir" "$home/run" "$home/tmp" "$local_bin"
if [ ! -f "$home/config.toml" ]; then printf 'service_url = "%s"\n' "$HOPTY_SERVICE_URL" >"$home/config.toml"; fi
chmod 600 "$home/config.toml"

base_url=${HOPTY_RELEASE_BASE_URL:-https://github.com/marcelcases/hopty-agent/releases/download/$HOPTY_VERSION}
asset=hopty_linux_$arch
work=$(mktemp -d "$home/tmp/hopty.XXXXXX")
trap 'rm -rf "$work" "$home/tmp"' EXIT HUP INT TERM

heading
step "Downloading Hopty $HOPTY_VERSION for Linux/$arch"
if command -v curl >/dev/null 2>&1; then
  if [ -t 1 ]; then
    printf '%s' "$accent"
    curl --fail --location --proto '=https' --tlsv1.2 --progress-bar -o "$work/hopty" "$base_url/$asset"
    printf '%s\n' "$reset"
  else
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 -o "$work/hopty" "$base_url/$asset"
  fi
elif command -v wget >/dev/null 2>&1; then
  wget -O "$work/hopty" "$base_url/$asset"
else
  echo "curl or wget is required" >&2
  exit 1
fi
actual=$(sha256sum "$work/hopty" 2>/dev/null | awk '{print $1}' || shasum -a 256 "$work/hopty" | awk '{print $1}')
[ "$actual" = "$checksum" ] || { echo "Hopty checksum verification failed" >&2; exit 1; }
chmod 700 "$work/hopty"
mv -f "$work/hopty" "$bin_dir/hopty"
ln -sfn "$bin_dir/hopty" "$local_bin/hopty"
for profile in "$HOME/.profile" "$HOME/.bashrc"; do
  [ -f "$profile" ] || : >"$profile"
  grep -Fqx 'export PATH="$HOME/.local/bin:$PATH"' "$profile" || printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >>"$profile"
done
success "Verified agent installed"

step "Starting the local agent"
printf '%s  Connection timeout: 30 seconds%s\n' "$dim" "$reset"
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
  case "$status" in *"Connection"*"connected"*) break;; esac
  attempt=$((attempt + 1))
  [ "$attempt" -lt 30 ] || { echo "Hopty agent did not connect within 30 seconds" >&2; exit 1; }
  sleep 1
done
success "Agent connected securely"

case "$status" in
  *"Host        paired"*|*"Host        code verified"*) success "This host is already linked.";;
  *) "$bin_dir/hopty" pair --wait;;
esac

printf '\n%sHopty is ready.%s\n\n' "$accent" "$reset"
printf 'Go to %shttps://hopty.net%s and open a new shell.\n\n' "$cyan" "$reset"
printf '%sOptional:%s keep the agent running after logout with:\n  sudo loginctl enable-linger %s\n\n' "$dim" "$reset" "$(id -un)"
printf 'To uninstall, run: %shopty uninstall%s\n' "$cyan" "$reset"
printf 'To revoke, run: %shopty revoke%s\n\n' "$cyan" "$reset"
