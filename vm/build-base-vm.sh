#!/usr/bin/env bash
# Build the Super-Agent Ubuntu 24.04 KVM Golden VM Image ("super-agent-vm-base")
# Runs on an Ubuntu 24.04 host with Incus installed (see ./vm/setup-incus-host.sh).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILDER_VM="sa-vm-builder"
IMAGE_ALIAS="${INCUS_VM_IMAGE:-super-agent-vm-base}"

echo "[1/5] Launching temporary Ubuntu 24.04 KVM builder VM (${BUILDER_VM})..."
incus delete -f "${BUILDER_VM}" 2>/dev/null || true
incus launch images:ubuntu/24.04/cloud "${BUILDER_VM}" --vm \
  -c limits.cpu=4 \
  -c limits.memory=4GiB

echo "[2/5] Waiting for Incus VM agent to become ready..."
for _ in $(seq 1 60); do
  if incus exec "${BUILDER_VM}" -- true 2>/dev/null; then
    break
  fi
  sleep 2
done

echo "[3/5] Installing XFCE desktop, Chrome, Node 22, Claude Code, Codex CLI, and Super-Agent tools inside VM..."
incus exec "${BUILDER_VM}" -- bash -lc '
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
  xfce4 xfce4-terminal xfce4-whiskermenu-plugin \
  arc-theme papirus-icon-theme fonts-noto-cjk fonts-noto-color-emoji fonts-jetbrains-mono \
  fcitx5 fcitx5-chinese-addons fcitx5-frontend-gtk3 \
  gnome-keyring libsecret-1-0 libglib2.0-bin wmctrl x11-utils zenity \
  curl wget git jq ripgrep tmux vim python3 python3-pip build-essential ca-certificates gnupg

# Node.js 22 + Claude Code & Codex CLI
curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
apt-get install -y nodejs
npm install -g pnpm @anthropic-ai/claude-code @openai/codex

# Google Chrome (amd64)
if [ "$(dpkg --print-architecture)" = "amd64" ]; then
  wget -qO /tmp/chrome.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
  apt-get install -y /tmp/chrome.deb
  rm -f /tmp/chrome.deb
fi

# Create desktop user ubuntu if absent
id -u ubuntu >/dev/null 2>&1 || useradd -m -s /bin/bash -G sudo ubuntu
mkdir -p /workspace /etc/super-agent
chown ubuntu:ubuntu /workspace
'

echo "[4/5] Pushing Super-Agent helper scripts into VM..."
for script in sa-local-sh sa-claude-hook sa-preview sa-fit-windows sa-window-guard sa-trust-desktop sa-display super-agent-chatgpt super-agent-claude-desktop; do
  incus file push "${REPO_ROOT}/desktop/scripts/${script}" "${BUILDER_VM}/usr/local/bin/${script}" --mode 0755
done

# Boot hook inside VM reads Incus metadata (/dev/incus/sock) to configure SA_GATEWAY_URL, SA_INTERNAL_TOKEN, TZ, and optional egress
incus exec "${BUILDER_VM}" -- bash -lc 'cat <<'\''EOF'\'' > /usr/local/bin/sa-vm-boot
#!/usr/bin/env bash
set -u
get_meta() {
  curl -sf --unix-socket /dev/incus/sock "http://localhost/1.0/config/$1" 2>/dev/null || true
}
GW="$(get_meta user.super-agent.gateway)"
TOK="$(get_meta user.super-agent.token)"
TZ_VAL="$(get_meta user.super-agent.tz)"
EGRESS="$(get_meta user.super-agent.egress_url)"

[ -n "$TZ_VAL" ] && timedatectl set-timezone "$TZ_VAL" 2>/dev/null || true

{
  [ -n "$GW" ] && printf "export SA_GATEWAY_URL=%q\n" "$GW"
  [ -n "$TOK" ] && printf "export SA_INTERNAL_TOKEN=%q\n" "$TOK"
  if [ -n "$EGRESS" ]; then
    printf "export HTTP_PROXY=%q\n" "$EGRESS"
    printf "export HTTPS_PROXY=%q\n" "$EGRESS"
    printf "export ALL_PROXY=%q\n" "$EGRESS"
    printf "export NO_PROXY=\"localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16\"\n"
  fi
} > /etc/profile.d/super-agent.sh
chmod 0644 /etc/profile.d/super-agent.sh
EOF
chmod +x /usr/local/bin/sa-vm-boot

cat <<'\''EOF'\'' > /etc/systemd/system/sa-vm-boot.service
[Unit]
Description=Super-Agent VM Metadata Bootstrap
After=network-online.target incus-agent.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/sa-vm-boot
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl enable sa-vm-boot.service
apt-get clean
'

echo "[5/5] Stopping builder VM and publishing golden image alias '${IMAGE_ALIAS}'..."
incus stop "${BUILDER_VM}"
incus image delete "${IMAGE_ALIAS}" 2>/dev/null || true
incus publish "${BUILDER_VM}" --alias "${IMAGE_ALIAS}"
incus delete "${BUILDER_VM}"

echo "Golden KVM VM image '${IMAGE_ALIAS}' is ready!"
echo "Set DESKTOP_RUNTIME=incus in .env and start super-agent-gateway."
