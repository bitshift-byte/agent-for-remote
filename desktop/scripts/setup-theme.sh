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

# 4. Clean SVG icon for Display & Font Settings shortcut
cat <<'EOF' > /usr/share/icons/hicolor/scalable/apps/sa-display.svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">
  <defs>
    <linearGradient id="g3" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#4f7cff"/>
      <stop offset="100%" stop-color="#3b5bdb"/>
    </linearGradient>
  </defs>
  <rect x="8" y="8" width="112" height="112" rx="28" fill="url(#g3)"/>
  <rect x="24" y="28" width="80" height="54" rx="10" fill="none" stroke="#ffffff" stroke-width="6"/>
  <line x1="48" y1="96" x2="80" y2="96" stroke="#ffffff" stroke-width="6" stroke-linecap="round"/>
  <line x1="64" y1="82" x2="64" y2="96" stroke="#ffffff" stroke-width="6" stroke-linecap="round"/>
  <text x="64" y="65" text-anchor="middle" fill="#ffffff" font-family="sans-serif" font-size="28" font-weight="700">Aa</text>
</svg>
EOF

# 5. Dedicated SVG icon for Codex Desktop shortcut
cat <<'EOF' > /usr/share/icons/hicolor/scalable/apps/sa-codex.svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">
  <defs>
    <linearGradient id="g4" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#18181b"/>
      <stop offset="100%" stop-color="#27272a"/>
    </linearGradient>
  </defs>
  <rect x="8" y="8" width="112" height="112" rx="28" fill="url(#g4)" stroke="#3f3f46" stroke-width="3"/>
  <path d="M46 44 L28 64 L46 84" fill="none" stroke="#22c55e" stroke-width="8" stroke-linecap="round" stroke-linejoin="round"/>
  <path d="M82 44 L100 64 L82 84" fill="none" stroke="#22c55e" stroke-width="8" stroke-linecap="round" stroke-linejoin="round"/>
  <line x1="70" y1="38" x2="58" y2="90" stroke="#fafafa" stroke-width="7" stroke-linecap="round"/>
</svg>
EOF

# 6. Dedicated SVG icon for Claude Code CLI shortcut
cat <<'EOF' > /usr/share/icons/hicolor/scalable/apps/sa-claude-code.svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">
  <defs>
    <linearGradient id="g5" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#2b1912"/>
      <stop offset="100%" stop-color="#432216"/>
    </linearGradient>
  </defs>
  <rect x="8" y="8" width="112" height="112" rx="28" fill="url(#g5)" stroke="#e07a4b" stroke-width="3"/>
  <path d="M34 46 L54 64 L34 82" fill="none" stroke="#f59e0b" stroke-width="8" stroke-linecap="round" stroke-linejoin="round"/>
  <line x1="62" y1="82" x2="94" y2="82" stroke="#fff7ed" stroke-width="8" stroke-linecap="round"/>
</svg>
EOF

# 7. Dedicated SVG icon for Workspace Terminal shortcut
cat <<'EOF' > /usr/share/icons/hicolor/scalable/apps/sa-terminal.svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">
  <rect x="8" y="8" width="112" height="112" rx="28" fill="#0f172a" stroke="#334155" stroke-width="3"/>
  <path d="M34 46 L54 64 L34 82" fill="none" stroke="#38bdf8" stroke-width="8" stroke-linecap="round" stroke-linejoin="round"/>
  <line x1="62" y1="82" x2="92" y2="82" stroke="#e2e8f0" stroke-width="8" stroke-linecap="round"/>
</svg>
EOF

# 8. Dedicated SVG icon for Workspace Folder shortcut
cat <<'EOF' > /usr/share/icons/hicolor/scalable/apps/sa-workspace.svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">
  <defs>
    <linearGradient id="g6" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#2563eb"/>
      <stop offset="100%" stop-color="#1d4ed8"/>
    </linearGradient>
  </defs>
  <rect x="8" y="8" width="112" height="112" rx="28" fill="url(#g6)"/>
  <path d="M26 44 C26 39 30 36 35 36 L54 36 L62 44 L93 44 C98 44 102 48 102 53 L102 86 C102 91 98 95 93 95 L35 95 C30 95 26 91 26 86 Z" fill="#eff6ff"/>
</svg>
EOF

gtk-update-icon-cache -f /usr/share/icons/hicolor 2>/dev/null || true
gtk-update-icon-cache -f /usr/share/icons/Papirus-Dark 2>/dev/null || true
