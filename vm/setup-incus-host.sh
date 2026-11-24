#!/usr/bin/env bash
# Ubuntu 24.04 Host Setup for Super-Agent KVM Virtual Machine Runtime (Incus)
set -euo pipefail

if [ ! -e /dev/kvm ]; then
  echo "Error: /dev/kvm not found. KVM hardware virtualization is required." >&2
  exit 1
fi

echo "[1/3] Installing Incus and QEMU/KVM packages on Ubuntu 24.04..."
sudo apt-get update
sudo apt-get install -y incus qemu-system-x86 ovmf virtiofsd

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
