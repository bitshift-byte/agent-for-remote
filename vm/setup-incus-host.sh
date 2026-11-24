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

echo "[3/4] Applying host-level KVM memory optimizations (KSM + zram for low-RAM hosts)..."
# Enable Kernel Samepage Merging (KSM) to deduplicate memory pages across VMs
if [ -w /sys/kernel/mm/ksm/run ]; then
  echo 1 | sudo tee /sys/kernel/mm/ksm/run >/dev/null
  echo 200 | sudo tee /sys/kernel/mm/ksm/sleep_millisecs >/dev/null || true
fi

# On <= 4GB RAM hosts without swap, configure zstd zram so QEMU never gets OOM-killed
HOST_MEM_KB="$(awk '/MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null || echo 0)"
if [ "${HOST_MEM_KB}" -gt 0 ] && [ "${HOST_MEM_KB}" -le 4500000 ] && ! swapon --show=NAME 2>/dev/null | grep -q '/dev/zram'; then
  sudo modprobe zram 2>/dev/null || true
  if command -v zramctl >/dev/null 2>&1; then
    ZDEV="$(sudo zramctl --find --size "$((HOST_MEM_KB * 1024 / 2))" --algorithm zstd 2>/dev/null || sudo zramctl --find --size "$((HOST_MEM_KB * 1024 / 2))" 2>/dev/null || true)"
    if [ -n "${ZDEV}" ]; then
      sudo mkswap "${ZDEV}" >/dev/null 2>&1 && sudo swapon -p 100 "${ZDEV}" 2>/dev/null || true
      echo "  Enabled host zram swap on ${ZDEV}"
    fi
  fi
fi

echo "[4/4] Checking Incus bridge gateway IP..."
BRIDGE_IP="$(incus network get incusbr0 ipv4.address | cut -d/ -f1)"
echo "Done! Incus KVM host is ready."
echo "  Socket:         /var/lib/incus/unix.socket"
echo "  Bridge Network: incusbr0 (Gateway IP: ${BRIDGE_IP})"
echo ""
echo "Next step: run ./vm/build-base-vm.sh to build the Ubuntu 24.04 desktop golden VM image."
