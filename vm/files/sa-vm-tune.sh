#!/usr/bin/env bash
# Extreme low-memory / 1-vCPU optimization for Super-Agent Ubuntu 24.04 KVM VM
# Safe to run both during golden image build and on an existing running VM.
set -euo pipefail

echo "[tune] Applying 1C2G low-memory & low-CPU VM optimizations..."

# 1. Disable/purge background bloat daemons (saves ~250-350MB RAM & prevents 100% CPU spikes)
DISABLE_UNITS=(
  snapd.service
  snapd.socket
  snapd.seeded.service
  unattended-upgrades.service
  apt-daily.timer
  apt-daily-upgrade.timer
  apt-daily.service
  apt-daily-upgrade.service
  multipathd.service
  multipathd.socket
  ModemManager.service
  cups.service
  cups-browsed.service
  avahi-daemon.service
  avahi-daemon.socket
  fwupd.service
  udisks2.service
  packagekit.service
  thermald.service
  accounts-daemon.service
  man-db.timer
  fstrim.timer
)
for u in "${DISABLE_UNITS[@]}"; do
  systemctl disable --now "$u" 2>/dev/null || true
done

if dpkg -l snapd >/dev/null 2>&1; then
  DEBIAN_FRONTEND=noninteractive apt-get purge -y snapd >/dev/null 2>&1 || true
  rm -rf /snap /var/snap /var/lib/snapd /var/cache/snapd
fi

# 2. Cap systemd-journald memory & disk usage
mkdir -p /etc/systemd/journald.conf.d
cat <<'EOF' > /etc/systemd/journald.conf.d/lowmem.conf
[Journal]
Storage=volatile
RuntimeMaxUse=16M
SystemMaxUse=32M
MaxRetentionSec=1day
EOF

# 3. Kernel memory tuning for zram-backed low-RAM desktop VMs
cat <<'EOF' > /etc/sysctl.d/99-super-agent-lowmem.conf
vm.swappiness = 100
vm.page-cluster = 0
vm.vfs_cache_pressure = 150
vm.watermark_boost_factor = 0
vm.dirty_ratio = 10
vm.dirty_background_ratio = 5
EOF

# 4. Boot-time zram compressed swap (zstd, 100% of RAM -> turns 1.4-2GB RAM into ~2.8-3.5GB effective)
cat <<'EOF' > /usr/local/bin/sa-zram-setup
#!/usr/bin/env bash
set -u
modprobe zram 2>/dev/null || exit 0
if swapon --show=NAME 2>/dev/null | grep -q '/dev/zram0'; then
  exit 0
fi
MEM_KB="$(awk '/MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null || echo 1500000)"
ZRAM_BYTES="$((MEM_KB * 1024))"
if command -v zramctl >/dev/null 2>&1; then
  DEV="$(zramctl --find --size "${ZRAM_BYTES}" --algorithm zstd 2>/dev/null || zramctl --find --size "${ZRAM_BYTES}" 2>/dev/null || true)"
  if [ -n "${DEV}" ]; then
    mkswap "${DEV}" >/dev/null 2>&1 && swapon -p 100 "${DEV}" 2>/dev/null || true
  fi
fi
EOF
chmod 0755 /usr/local/bin/sa-zram-setup

cat <<'EOF' > /etc/systemd/system/sa-zram.service
[Unit]
Description=Super-Agent ZRAM Compressed Swap Setup
After=local-fs.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/sa-zram-setup
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable sa-zram.service
/usr/local/bin/sa-zram-setup || true

# 5. Cap Node.js heap so CLI agents (Claude Code / Codex) never OOM a 2GB VM
cat <<'EOF' > /etc/profile.d/super-agent-lowmem.sh
export NODE_OPTIONS="${NODE_OPTIONS:---max-old-space-size=512}"
EOF
chmod 0644 /etc/profile.d/super-agent-lowmem.sh

echo "[tune] Low-memory VM optimizations applied."
