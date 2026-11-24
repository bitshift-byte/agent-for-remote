#!/usr/bin/env bash
# Ubuntu 22.04 / 24.04 Host Setup for Super-Agent KVM Virtual Machine Runtime (Incus)
set -euo pipefail

if [ ! -e /dev/kvm ]; then
  echo "Error: /dev/kvm not found. KVM hardware virtualization is required." >&2
  exit 1
fi

echo "[1/3] Installing Incus and QEMU/KVM packages..."
sudo apt-get update
if ! apt-cache show incus >/dev/null 2>&1; then
  echo "Official Ubuntu repo lacks incus (e.g. Ubuntu 22.04); adding Zabbly stable Incus repo..."
  sudo apt-get install -y curl gnupg
  sudo mkdir -p /etc/apt/keyrings
  curl -fsSL https://pkgs.zabbly.com/key.asc | sudo tee /etc/apt/keyrings/zabbly.asc >/dev/null
  CODENAME="$(. /etc/os-release && echo "${VERSION_CODENAME}")"
  cat <<EOF | sudo tee /etc/apt/sources.list.d/zabbly-incus-stable.sources >/dev/null
Enabled: yes
Types: deb
URIs: https://pkgs.zabbly.com/incus/stable
Suites: ${CODENAME}
Components: main
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/zabbly.asc
EOF
  sudo apt-get update
fi

sudo apt-get install -y incus qemu-system-x86 ovmf
if ! command -v virtiofsd >/dev/null 2>&1; then
  sudo apt-get install -y virtiofsd || true
fi
if [ ! -e /usr/bin/virtiofsd ] && [ -x /usr/libexec/virtiofsd ]; then
  sudo ln -sf /usr/libexec/virtiofsd /usr/bin/virtiofsd
fi

echo "[2/3] Initializing Incus with default bridge (incusbr0) and dir storage pool..."
if ! incus storage list --format csv | grep -q '^default,'; then
  sudo incus admin init --auto
fi

echo "[3/3] Checking Incus bridge gateway IP..."
BRIDGE_IP="$(incus network get incusbr0 ipv4.address | cut -d/ -f1)"
echo "Done! Incus KVM host is ready."
echo "  Socket:         /var/lib/incus/unix.socket"
echo "  Bridge Network: incusbr0 (Gateway IP: ${BRIDGE_IP})"
echo ""
echo "Next step: run ./vm/build-base-vm.sh to build the Ubuntu 24.04 desktop golden VM image."
