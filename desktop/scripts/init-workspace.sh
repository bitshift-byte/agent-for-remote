#!/usr/bin/with-contenv bash
set -e

USER_HOME="/config"
DESKTOP_DIR="${USER_HOME}/Desktop"
AUTOSTART_DIR="${USER_HOME}/.config/autostart"

mkdir -p /workspace "${DESKTOP_DIR}" "${AUTOSTART_DIR}"

# Ensure abc user (LinuxServer Webtop default user, UID/GID mapped via PUID/PGID) owns workspace and desktop files
chown abc:abc /workspace "${DESKTOP_DIR}" "${AUTOSTART_DIR}" || true

# Autostart fcitx5 Chinese IME when XFCE starts
cat <<'EOF' > "${AUTOSTART_DIR}/fcitx5.desktop"
[Desktop Entry]
Type=Application
Name=Fcitx 5
Exec=fcitx5 -d
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
EOF

# Autostart GNOME Keyring daemon so ChatGPT & Claude Desktop sign-ins persist across container restarts
cat <<'EOF' > "${AUTOSTART_DIR}/gnome-keyring.desktop"
[Desktop Entry]
Type=Application
Name=GNOME Keyring
Exec=gnome-keyring-daemon --start --components=secrets
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
EOF

# Desktop shortcut: ChatGPT Official Desktop (with Codex)
cat <<'EOF' > "${DESKTOP_DIR}/ChatGPT.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=ChatGPT
Comment=ChatGPT Desktop App (with Codex)
Exec=/usr/local/bin/super-agent-chatgpt
Icon=chatgpt
Terminal=false
Categories=Network;Development;
EOF

# Desktop shortcut: Claude Official Desktop (Chat + Graphical Claude Code)
cat <<'EOF' > "${DESKTOP_DIR}/Claude-Desktop.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Claude Desktop
Comment=Claude Desktop App (Chat + Graphical Claude Code)
Exec=/usr/local/bin/super-agent-claude-desktop
Icon=claude-desktop
Terminal=false
Categories=Network;Development;
EOF

# Desktop shortcut: Chrome / Chromium
cat <<'EOF' > "${DESKTOP_DIR}/Browser.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Chrome Browser
Comment=Open browser in overseas cloud network
Exec=/usr/local/bin/super-agent-browser
Icon=web-browser
Terminal=false
Categories=Network;WebBrowser;
EOF

# Desktop shortcut: Claude Code CLI in /workspace (commands forwarded transparently to local Mac via sa-local-sh)
cat <<'EOF' > "${DESKTOP_DIR}/Claude-Code-CLI.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Claude Code (CLI)
Comment=Launch Claude Code CLI in /workspace (Local Exec Bridge enabled)
Exec=xfce4-terminal --working-directory=/workspace -e "bash -lc 'SHELL=/usr/local/bin/sa-local-sh claude; exec bash'"
Icon=utilities-terminal
Terminal=false
Categories=Development;
EOF

# Desktop shortcut: OpenAI Codex CLI in /workspace (commands forwarded transparently to local Mac via sa-local-sh)
cat <<'EOF' > "${DESKTOP_DIR}/OpenAI-Codex-CLI.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=OpenAI Codex (CLI)
Comment=Launch OpenAI Codex CLI in /workspace (Local Exec Bridge enabled)
Exec=xfce4-terminal --working-directory=/workspace -e "bash -lc 'SHELL=/usr/local/bin/sa-local-sh codex; exec bash'"
Icon=utilities-terminal
Terminal=false
Categories=Development;
EOF

# Desktop shortcut: Workspace Terminal
cat <<'EOF' > "${DESKTOP_DIR}/Workspace-Terminal.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Workspace Terminal
Comment=Open terminal in /workspace
Exec=xfce4-terminal --working-directory=/workspace
Icon=utilities-terminal
Terminal=false
Categories=System;TerminalEmulator;
EOF

# Remove legacy shortcuts if present from older builds
rm -f "${DESKTOP_DIR}/Cursor.desktop" "${DESKTOP_DIR}/VS-Code.desktop"

chmod +x "${DESKTOP_DIR}"/*.desktop
chown -R abc:abc "${DESKTOP_DIR}" "${AUTOSTART_DIR}" || true
