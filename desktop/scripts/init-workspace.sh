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

# Desktop shortcut: Claude Code in /workspace
cat <<'EOF' > "${DESKTOP_DIR}/Claude-Code.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Claude Code
Comment=Launch Claude Code in /workspace
Exec=xfce4-terminal --working-directory=/workspace -e "bash -lc 'claude; exec bash'"
Icon=utilities-terminal
Terminal=false
Categories=Development;
EOF

# Desktop shortcut: OpenAI Codex in /workspace
cat <<'EOF' > "${DESKTOP_DIR}/OpenAI-Codex.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=OpenAI Codex
Comment=Launch OpenAI Codex CLI in /workspace
Exec=xfce4-terminal --working-directory=/workspace -e "bash -lc 'codex; exec bash'"
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

chmod +x "${DESKTOP_DIR}"/*.desktop
chown -R abc:abc "${DESKTOP_DIR}" "${AUTOSTART_DIR}" || true
