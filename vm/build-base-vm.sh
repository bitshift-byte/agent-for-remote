#!/usr/bin/env bash
# Build the Super-Agent Ubuntu 24.04 KVM Golden VM Image ("super-agent-vm-base")
# Runs on an Ubuntu 24.04 host with Incus installed (see ./vm/setup-incus-host.sh).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILDER_VM="sa-vm-builder"
IMAGE_ALIAS="${INCUS_VM_IMAGE:-super-agent-vm-base}"
HOST_CPUS="$(nproc 2>/dev/null || echo 2)"
DEFAULT_CPU="$(( HOST_CPUS > 2 ? 2 : HOST_CPUS ))"
BUILDER_CPU="${INCUS_BUILDER_CPU:-${DEFAULT_CPU}}"
BUILDER_MEM="${INCUS_BUILDER_MEM:-1536MiB}"

echo "[1/5] Launching temporary Ubuntu 24.04 KVM builder VM (${BUILDER_VM}, cpu=${BUILDER_CPU}, mem=${BUILDER_MEM})..."
incus delete -f "${BUILDER_VM}" 2>/dev/null || true
incus launch images:ubuntu/24.04/cloud "${BUILDER_VM}" --vm \
  -c "limits.cpu=${BUILDER_CPU}" \
  -c "limits.memory=${BUILDER_MEM}"

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
  xfce4 xfce4-terminal xfce4-whiskermenu-plugin dbus-x11 \
  tigervnc-standalone-server tigervnc-common tigervnc-tools novnc websockify \
  arc-theme papirus-icon-theme hicolor-icon-theme adwaita-icon-theme \
  librsvg2-common libgdk-pixbuf2.0-bin libgtk-3-bin \
  gvfs gvfs-backends xdg-utils exo-utils libnss3 libgbm1 \
  fonts-noto-cjk fonts-noto-color-emoji fonts-jetbrains-mono \
  locales fcitx5 fcitx5-pinyin fcitx5-chinese-addons fcitx5-frontend-gtk2 fcitx5-frontend-gtk3 fcitx5-frontend-qt5 fcitx5-config-qt im-config \
  gnome-keyring libsecret-1-0 libglib2.0-bin wmctrl x11-utils zenity \
  curl wget git jq ripgrep tmux vim python3 python3-pip build-essential ca-certificates gnupg

# Refresh GDK-Pixbuf SVG loader cache so XFCE renders all .svg desktop & theme icons
for loader in /usr/lib/*/gdk-pixbuf-2.0/gdk-pixbuf-query-loaders; do
  [ -x "$loader" ] && "$loader" --update-cache || true
done

# Generate UTF-8 Chinese & English locales and set default US timezone (America/Los_Angeles)
sed -i "/zh_CN.UTF-8/s/^# //g; /en_US.UTF-8/s/^# //g" /etc/locale.gen
locale-gen zh_CN.UTF-8 en_US.UTF-8
update-locale LANG=en_US.UTF-8
ln -snf /usr/share/zoneinfo/America/Los_Angeles /etc/localtime
echo "America/Los_Angeles" > /etc/timezone

# Node.js 22 + Claude Code & Codex CLI
curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
apt-get install -y nodejs
npm install -g pnpm @anthropic-ai/claude-code @openai/codex

# Google Chrome (amd64)
ARCH="$(dpkg --print-architecture)"
if [ "$ARCH" = "amd64" ]; then
  wget -qO /tmp/chrome.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
  apt-get install -y /tmp/chrome.deb
  rm -f /tmp/chrome.deb
fi

# Official OpenAI ChatGPT + Codex Desktop App for Linux (.deb, Ubuntu 24.04 LTS)
CHATGPT_DEB_URL="${CHATGPT_DESKTOP_DEB_URL:-}"
if [ -z "$CHATGPT_DEB_URL" ]; then
  if [ "$ARCH" = "amd64" ]; then
    CHATGPT_DEB_URL="https://persistent.oaistatic.com/codex-app-prod/linux/deb/latest/chatgpt_amd64.deb"
  elif [ "$ARCH" = "arm64" ]; then
    CHATGPT_DEB_URL="https://persistent.oaistatic.com/codex-app-prod/linux/deb/latest/chatgpt_arm64.deb"
  fi
fi
if [ -n "$CHATGPT_DEB_URL" ]; then
  echo "Installing official ChatGPT + Codex desktop app from ${CHATGPT_DEB_URL}..."
  if wget -qO /tmp/chatgpt-desktop.deb "${CHATGPT_DEB_URL}"; then
    apt-get install -y /tmp/chatgpt-desktop.deb || apt-get -f install -y || true
    rm -f /tmp/chatgpt-desktop.deb
  else
    echo "Warning: failed to download ChatGPT/Codex desktop .deb; will fall back to web app mode."
  fi
fi

# Create desktop user ubuntu if absent
id -u ubuntu >/dev/null 2>&1 || useradd -m -s /bin/bash -G sudo ubuntu
mkdir -p /workspace /etc/super-agent /home/ubuntu/.vnc /var/www/sa-desktop
ln -sfn /usr/share/novnc /var/www/sa-desktop/desktop
chown -R ubuntu:ubuntu /workspace /home/ubuntu/.vnc
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
'

# Push desktop streaming configs (:3000/desktop/ -> websockify -> loopback TigerVNC :1)
incus file push "${REPO_ROOT}/vm/files/super-agent-browser" "${BUILDER_VM}/usr/local/bin/super-agent-browser" --mode 0755
incus file push "${REPO_ROOT}/vm/files/init-vm-desktop.sh" "${BUILDER_VM}/usr/local/bin/sa-vm-init-desktop" --mode 0755
incus file push "${REPO_ROOT}/vm/files/sa-vm-tune.sh" "${BUILDER_VM}/usr/local/bin/sa-vm-tune" --mode 0755
incus file push "${REPO_ROOT}/desktop/scripts/setup-theme.sh" "${BUILDER_VM}/tmp/setup-theme.sh" --mode 0755
incus file push "${REPO_ROOT}/vm/files/xstartup" "${BUILDER_VM}/home/ubuntu/.vnc/xstartup" --mode 0755
incus file push "${REPO_ROOT}/vm/files/novnc-index.html" "${BUILDER_VM}/usr/share/novnc/index.html" --mode 0644
incus file push "${REPO_ROOT}/vm/files/super-agent-vnc.service" "${BUILDER_VM}/etc/systemd/system/super-agent-vnc.service" --mode 0644
incus file push "${REPO_ROOT}/vm/files/super-agent-desktop.service" "${BUILDER_VM}/etc/systemd/system/super-agent-desktop.service" --mode 0644

incus exec "${BUILDER_VM}" -- env VNC_PASSWORD="${INCUS_VNC_PASSWORD:-}" bash -lc '
set -euo pipefail
# Generate wallpaper + SVG app icons (ChatGPT, Claude, Display), populate Desktop shortcuts, and apply 1C2G tuning
/tmp/setup-theme.sh
rm -f /tmp/setup-theme.sh
/usr/local/bin/sa-vm-init-desktop
/usr/local/bin/sa-vm-tune

# Verify Claude Code & Codex CLIs are installed on PATH
command -v claude >/dev/null
command -v codex >/dev/null

if [ -z "${VNC_PASSWORD}" ]; then
  VNC_PASSWORD="$(openssl rand -hex 4)"
fi
printf "%s\n" "${VNC_PASSWORD}" > /etc/super-agent/vnc.pass
chmod 600 /etc/super-agent/vnc.pass
printf "%s\n" "${VNC_PASSWORD}" | vncpasswd -f > /home/ubuntu/.vnc/passwd
chown ubuntu:ubuntu /home/ubuntu/.vnc/xstartup /home/ubuntu/.vnc/passwd
chmod 600 /home/ubuntu/.vnc/passwd
systemctl daemon-reload
systemctl enable super-agent-vnc.service super-agent-desktop.service
apt-get clean
'

echo "[5/5] Stopping builder VM and publishing golden image alias '${IMAGE_ALIAS}'..."
incus stop "${BUILDER_VM}"
incus image delete "${IMAGE_ALIAS}" 2>/dev/null || true
incus publish "${BUILDER_VM}" --alias "${IMAGE_ALIAS}"
incus delete "${BUILDER_VM}"

echo "Golden KVM VM image '${IMAGE_ALIAS}' is ready!"
echo "Desktop streaming listens on :3000/desktop/ (VNC password saved at /etc/super-agent/vnc.pass)."
echo "Set DESKTOP_RUNTIME=incus in .env and start super-agent-gateway."
