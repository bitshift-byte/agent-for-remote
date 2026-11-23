#!/usr/bin/with-contenv bash
set -e

USER_HOME="/config"
DESKTOP_DIR="${USER_HOME}/Desktop"
AUTOSTART_DIR="${USER_HOME}/.config/autostart"
XFCONF_DIR="${USER_HOME}/.config/xfce4/xfconf/xfce-perchannel-xml"
TERM_DIR="${USER_HOME}/.config/xfce4/terminal"

mkdir -p /workspace "${DESKTOP_DIR}" "${AUTOSTART_DIR}" "${XFCONF_DIR}" "${TERM_DIR}"

# Ensure abc user (LinuxServer Webtop default user, UID/GID mapped via PUID/PGID) owns workspace and desktop files
chown abc:abc /workspace "${DESKTOP_DIR}" "${AUTOSTART_DIR}" || true

# Apply modern dark XFCE theme, Papirus icons, clean desktop & wallpaper on first boot
if [ ! -f "${USER_HOME}/.sa-theme-v1" ]; then
  cat <<'EOF' > "${XFCONF_DIR}/xsettings.xml"
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xsettings" version="1.0">
  <property name="Net" type="empty">
    <property name="ThemeName" type="string" value="Arc-Dark"/>
    <property name="IconThemeName" type="string" value="Papirus-Dark"/>
    <property name="FallbackIconThemeName" type="string" value="hicolor"/>
  </property>
  <property name="Gtk" type="empty">
    <property name="FontName" type="string" value="Noto Sans CJK SC 10"/>
    <property name="MonospaceFontName" type="string" value="JetBrains Mono 10"/>
    <property name="CursorThemeSize" type="int" value="24"/>
  </property>
  <property name="Xft" type="empty">
    <property name="Antialias" type="int" value="1"/>
    <property name="Hinting" type="int" value="1"/>
    <property name="HintStyle" type="string" value="hintslight"/>
    <property name="RGBA" type="string" value="rgb"/>
  </property>
</channel>
EOF

  cat <<'EOF' > "${XFCONF_DIR}/xfwm4.xml"
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfwm4" version="1.0">
  <property name="general" type="empty">
    <property name="theme" type="string" value="Arc-Dark"/>
    <property name="title_font" type="string" value="Noto Sans CJK SC Medium 10"/>
    <property name="use_compositing" type="bool" value="true"/>
    <property name="show_frame_shadow" type="bool" value="true"/>
    <property name="show_popup_shadow" type="bool" value="true"/>
    <property name="button_layout" type="string" value="CHM|O"/>
  </property>
</channel>
EOF

  cat <<'EOF' > "${XFCONF_DIR}/xfce4-desktop.xml"
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-desktop" version="1.0">
  <property name="backdrop" type="empty">
    <property name="screen0" type="empty">
      <property name="monitorVNC-0" type="empty">
        <property name="workspace0" type="empty">
          <property name="color-style" type="int" value="0"/>
          <property name="image-style" type="int" value="5"/>
          <property name="last-image" type="string" value="/usr/share/backgrounds/super-agent-wallpaper.png"/>
        </property>
      </property>
      <property name="monitor0" type="empty">
        <property name="workspace0" type="empty">
          <property name="color-style" type="int" value="0"/>
          <property name="image-style" type="int" value="5"/>
          <property name="last-image" type="string" value="/usr/share/backgrounds/super-agent-wallpaper.png"/>
        </property>
      </property>
    </property>
  </property>
  <property name="desktop-icons" type="empty">
    <property name="icon-size" type="uint" value="52"/>
    <property name="use-custom-font-size" type="bool" value="true"/>
    <property name="font-size" type="double" value="10.5"/>
    <property name="file-icons" type="empty">
      <property name="show-home" type="bool" value="false"/>
      <property name="show-filesystem" type="bool" value="false"/>
      <property name="show-trash" type="bool" value="false"/>
      <property name="show-removable" type="bool" value="false"/>
    </property>
  </property>
</channel>
EOF

  cat <<'EOF' > "${TERM_DIR}/terminalrc"
[Configuration]
FontName=JetBrains Mono 11
ColorForeground=#e6ecf8
ColorBackground=#0b1020
ColorCursor=#4f7cff
ColorPalette=#1e293b;#f87171;#4ade80;#facc15;#60a5fa;#c084fc;#22d3ee;#e2e8f0;#475569;#fca5a5;#86efac;#fde047;#93c5fd;#d8b4fe;#67e8f9;#f8fafc
MiscMenubarDefault=FALSE
MiscBordersDefault=TRUE
ScrollingLines=10000
EOF

  touch "${USER_HOME}/.sa-theme-v1"
fi

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
Icon=sa-chatgpt
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
Icon=sa-claude
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
chown -R abc:abc "${DESKTOP_DIR}" "${AUTOSTART_DIR}" "${XFCONF_DIR}" "${TERM_DIR}" "${USER_HOME}/.sa-theme-v1" || true
