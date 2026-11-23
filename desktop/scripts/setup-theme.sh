#!/usr/bin/env bash
set -e

mkdir -p /usr/share/backgrounds /usr/share/icons/hicolor/scalable/apps

# 1. Generate a clean 1920x1080 deep-indigo ambient gradient wallpaper
python3 - <<'PY'
import struct, zlib, math

w, h = 1920, 1080
rows = []
for y in range(h):
    row = bytearray([0])
    ny = y / float(h)
    for x in range(w):
        nx = x / float(w)
        # Base deep slate-indigo gradient (#090e1a -> #131c36)
        r = 9.0 + 12.0 * ny + 6.0 * nx
        g = 14.0 + 16.0 * ny + 8.0 * nx
        b = 26.0 + 34.0 * ny + 18.0 * nx

        # Subtle top-left blue glow
        d1 = math.hypot((nx - 0.18) * 1.4, ny - 0.22)
        if d1 < 0.65:
            glow1 = ((0.65 - d1) / 0.65) ** 2.2
            r += 28.0 * glow1
            g += 62.0 * glow1
            b += 145.0 * glow1

        # Subtle bottom-right violet/cyan ambient glow
        d2 = math.hypot((nx - 0.82) * 1.4, ny - 0.78)
        if d2 < 0.70:
            glow2 = ((0.70 - d2) / 0.70) ** 2.4
            r += 42.0 * glow2
            g += 28.0 * glow2
            b += 110.0 * glow2

        row.extend((
            max(0, min(255, int(r))),
            max(0, min(255, int(g))),
            max(0, min(255, int(b))),
        ))
    rows.append(bytes(row))

raw = b"".join(rows)
def chunk(tag, data):
    return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xffffffff)

png = b"\x89PNG\r\n\x1a\n"
png += chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
png += chunk(b"IDAT", zlib.compress(raw, 6))
png += chunk(b"IEND", b"")

with open("/usr/share/backgrounds/super-agent-wallpaper.png", "wb") as f:
    f.write(png)
PY

# 2. Clean fallback SVG icon for ChatGPT desktop shortcut
cat <<'EOF' > /usr/share/icons/hicolor/scalable/apps/sa-chatgpt.svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">
  <defs>
    <linearGradient id="g1" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#10a37f"/>
      <stop offset="100%" stop-color="#0d7a5f"/>
    </linearGradient>
  </defs>
  <rect x="8" y="8" width="112" height="112" rx="28" fill="url(#g1)"/>
  <circle cx="64" cy="64" r="28" fill="none" stroke="#ffffff" stroke-width="8"/>
  <circle cx="64" cy="64" r="10" fill="#ffffff"/>
</svg>
EOF

# 3. Clean fallback SVG icon for Claude Desktop shortcut
cat <<'EOF' > /usr/share/icons/hicolor/scalable/apps/sa-claude.svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">
  <defs>
    <linearGradient id="g2" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#e07a4b"/>
      <stop offset="100%" stop-color="#c45c2c"/>
    </linearGradient>
  </defs>
  <rect x="8" y="8" width="112" height="112" rx="28" fill="url(#g2)"/>
  <g stroke="#fff8f2" stroke-width="8" stroke-linecap="round">
    <line x1="64" y1="30" x2="64" y2="98"/>
    <line x1="30" y1="64" x2="98" y2="64"/>
    <line x1="40" y1="40" x2="88" y2="88"/>
    <line x1="88" y1="40" x2="40" y2="88"/>
  </g>
</svg>
EOF

gtk-update-icon-cache -f /usr/share/icons/hicolor 2>/dev/null || true
