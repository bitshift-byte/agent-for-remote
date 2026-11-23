#!/usr/bin/with-contenv bash
set -e

USER_HOME="/config"
DESKTOP_DIR="${USER_HOME}/Desktop"
AUTOSTART_DIR="${USER_HOME}/.config/autostart"
XFCONF_DIR="${USER_HOME}/.config/xfce4/xfconf/xfce-perchannel-xml"
PANEL_DIR="${USER_HOME}/.config/xfce4/panel"
TERM_DIR="${USER_HOME}/.config/xfce4/terminal"
GTK3_DIR="${USER_HOME}/.config/gtk-3.0"
SA_CONF_DIR="${USER_HOME}/.config/super-agent"

mkdir -p /workspace "${DESKTOP_DIR}" "${AUTOSTART_DIR}" "${XFCONF_DIR}" "${PANEL_DIR}" "${TERM_DIR}" "${GTK3_DIR}" "${SA_CONF_DIR}"

# Ensure abc user (LinuxServer Webtop default user, UID/GID mapped via PUID/PGID) owns workspace and desktop files
chown abc:abc /workspace "${DESKTOP_DIR}" "${AUTOSTART_DIR}" || true

# Desktop shortcuts (always refreshed so upgrades add new icons cleanly)
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

cat <<'EOF' > "${DESKTOP_DIR}/Claude-Desktop.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Claude
Comment=Claude Desktop App (Chat + Graphical Claude Code)
Exec=/usr/local/bin/super-agent-claude-desktop
Icon=sa-claude
Terminal=false
Categories=Network;Development;
EOF

cat <<'EOF' > "${DESKTOP_DIR}/Browser.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Chrome
Comment=Open browser in overseas cloud network
Exec=/usr/local/bin/super-agent-browser
Icon=web-browser
Terminal=false
Categories=Network;WebBrowser;
EOF

cat <<'EOF' > "${DESKTOP_DIR}/Claude-Code-CLI.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Claude Code
Comment=Launch Claude Code CLI in /workspace (Local Exec Bridge enabled)
Exec=xfce4-terminal --maximize --working-directory=/workspace -e "bash -lc 'SHELL=/usr/local/bin/sa-local-sh claude; exec bash'"
Icon=utilities-terminal
Terminal=false
Categories=Development;
EOF

cat <<'EOF' > "${DESKTOP_DIR}/OpenAI-Codex-CLI.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Codex
Comment=Launch OpenAI Codex CLI in /workspace (Local Exec Bridge enabled)
Exec=xfce4-terminal --maximize --working-directory=/workspace -e "bash -lc 'SHELL=/usr/local/bin/sa-local-sh codex; exec bash'"
Icon=utilities-terminal
Terminal=false
Categories=Development;
EOF

cat <<'EOF' > "${DESKTOP_DIR}/Workspace-Terminal.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=终端
Comment=Open terminal in /workspace
Exec=xfce4-terminal --maximize --working-directory=/workspace
Icon=utilities-terminal
Terminal=false
Categories=System;TerminalEmulator;
EOF

cat <<'EOF' > "${DESKTOP_DIR}/Display-Settings.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=显示设置
Comment=Adjust DPI scaling, font sizes, and dark/light theme
Exec=/usr/local/bin/sa-display
Icon=sa-display
Terminal=false
Categories=Settings;DesktopSettings;
EOF

# Remove legacy shortcuts if present from older builds
rm -f "${DESKTOP_DIR}/Cursor.desktop" "${DESKTOP_DIR}/VS-Code.desktop"

# Mark .desktop launchers as trusted (avoids XFCE "untrusted application launcher" prompt)
chmod +x "${DESKTOP_DIR}"/*.desktop 2>/dev/null || true

# Clear stale Chrome singleton locks from unclean container shutdowns
rm -f "${USER_HOME}/.config/google-chrome"/Singleton* 2>/dev/null || true

# Apply modern v3 XFCE theme, 1:1 viewport-safe DPI default (96), smart window placement, top bar + dock, and GTK3 polish
if [ ! -f "${USER_HOME}/.sa-theme-v3" ]; then
  INIT_DPI="${DESKTOP_DPI:-96}"
  INIT_UI_FONT="${DESKTOP_FONT_SIZE:-11}"
  INIT_ICON_FONT="${DESKTOP_ICON_FONT_SIZE:-11}"
  INIT_TERM_FONT="${DESKTOP_TERM_FONT_SIZE:-12}"
  INIT_ICON_SIZE="${DESKTOP_ICON_SIZE:-52}"
  INIT_THEME="${DESKTOP_THEME:-dark}"

  GTK_THEME="Arc-Dark"
  ICON_THEME="Papirus-Dark"
  TERM_FG="#e6ecf8"
  TERM_BG="#0b1020"
  TERM_CURSOR="#4f7cff"
  if [ "${INIT_THEME}" = "light" ]; then
    GTK_THEME="Arc"
    ICON_THEME="Papirus-Light"
    TERM_FG="#1e293b"
    TERM_BG="#f8fafc"
    TERM_CURSOR="#2563eb"
  fi

  cat <<EOF > "${SA_CONF_DIR}/display.env"
DPI=${INIT_DPI}
UI_FONT=${INIT_UI_FONT}
ICON_FONT=${INIT_ICON_FONT}
TERM_FONT=${INIT_TERM_FONT}
ICON_SIZE=${INIT_ICON_SIZE}
THEME=${INIT_THEME}
EOF

  cat <<EOF > "${XFCONF_DIR}/xsettings.xml"
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xsettings" version="1.0">
  <property name="Net" type="empty">
    <property name="ThemeName" type="string" value="${GTK_THEME}"/>
    <property name="IconThemeName" type="string" value="${ICON_THEME}"/>
    <property name="FallbackIconThemeName" type="string" value="hicolor"/>
  </property>
  <property name="Gtk" type="empty">
    <property name="FontName" type="string" value="Noto Sans CJK SC ${INIT_UI_FONT}"/>
    <property name="MonospaceFontName" type="string" value="JetBrains Mono ${INIT_UI_FONT}"/>
    <property name="CursorThemeSize" type="int" value="24"/>
  </property>
  <property name="Xft" type="empty">
    <property name="DPI" type="int" value="${INIT_DPI}"/>
    <property name="Antialias" type="int" value="1"/>
    <property name="Hinting" type="int" value="1"/>
    <property name="HintStyle" type="string" value="hintslight"/>
    <property name="RGBA" type="string" value="rgb"/>
  </property>
</channel>
EOF

  cat <<EOF > "${XFCONF_DIR}/xfwm4.xml"
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfwm4" version="1.0">
  <property name="general" type="empty">
    <property name="theme" type="string" value="${GTK_THEME}"/>
    <property name="title_font" type="string" value="Noto Sans CJK SC Medium ${INIT_UI_FONT}"/>
    <property name="use_compositing" type="bool" value="false"/>
    <property name="show_frame_shadow" type="bool" value="false"/>
    <property name="show_popup_shadow" type="bool" value="false"/>
    <property name="button_layout" type="string" value="CHM|O"/>
    <property name="placement_mode" type="string" value="center"/>
    <property name="placement_ratio" type="int" value="20"/>
    <property name="snap_to_border" type="bool" value="true"/>
    <property name="snap_to_windows" type="bool" value="true"/>
    <property name="tile_on_move" type="bool" value="true"/>
    <property name="wrap_workspaces" type="bool" value="false"/>
  </property>
</channel>
EOF

  cat <<EOF > "${XFCONF_DIR}/xfce4-desktop.xml"
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
    <property name="icon-size" type="uint" value="${INIT_ICON_SIZE}"/>
    <property name="use-custom-font-size" type="bool" value="true"/>
    <property name="font-size" type="double" value="${INIT_ICON_FONT}"/>
    <property name="file-icons" type="empty">
      <property name="show-home" type="bool" value="false"/>
      <property name="show-filesystem" type="bool" value="false"/>
      <property name="show-trash" type="bool" value="false"/>
      <property name="show-removable" type="bool" value="false"/>
    </property>
  </property>
</channel>
EOF

  # Populate bottom Dock launcher directories
  for i in 11 12 13 14 15; do
    mkdir -p "${PANEL_DIR}/launcher-${i}"
  done
  cp -f "${DESKTOP_DIR}/ChatGPT.desktop" "${PANEL_DIR}/launcher-11/ChatGPT.desktop"
  cp -f "${DESKTOP_DIR}/Claude-Desktop.desktop" "${PANEL_DIR}/launcher-12/Claude-Desktop.desktop"
  cp -f "${DESKTOP_DIR}/Browser.desktop" "${PANEL_DIR}/launcher-13/Browser.desktop"
  cp -f "${DESKTOP_DIR}/Workspace-Terminal.desktop" "${PANEL_DIR}/launcher-14/Workspace-Terminal.desktop"
  cp -f "${DESKTOP_DIR}/Display-Settings.desktop" "${PANEL_DIR}/launcher-15/Display-Settings.desktop"

  # Modern XFCE panel: translucent top status bar + centered auto-hide bottom dock
  cat <<'EOF' > "${XFCONF_DIR}/xfce4-panel.xml"
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-panel" version="1.0">
  <property name="configver" type="int" value="2"/>
  <property name="panels" type="array">
    <value type="int" value="1"/>
    <value type="int" value="2"/>
    <property name="dark-mode" type="bool" value="true"/>
    <property name="panel-1" type="empty">
      <property name="position" type="string" value="p=6;x=0;y=0"/>
      <property name="length" type="double" value="100"/>
      <property name="position-locked" type="bool" value="true"/>
      <property name="icon-size" type="uint" value="18"/>
      <property name="size" type="uint" value="30"/>
      <property name="background-style" type="uint" value="1"/>
      <property name="background-rgba" type="array">
        <value type="double" value="0.058824"/>
        <value type="double" value="0.090196"/>
        <value type="double" value="0.164706"/>
        <value type="double" value="0.860000"/>
      </property>
      <property name="plugin-ids" type="array">
        <value type="int" value="1"/>
        <value type="int" value="2"/>
        <value type="int" value="3"/>
        <value type="int" value="4"/>
        <value type="int" value="5"/>
        <value type="int" value="6"/>
      </property>
    </property>
    <property name="panel-2" type="empty">
      <property name="autohide-behavior" type="uint" value="1"/>
      <property name="position" type="string" value="p=10;x=0;y=0"/>
      <property name="length" type="double" value="1"/>
      <property name="length-adjust" type="bool" value="true"/>
      <property name="position-locked" type="bool" value="true"/>
      <property name="icon-size" type="uint" value="36"/>
      <property name="size" type="uint" value="52"/>
      <property name="background-style" type="uint" value="1"/>
      <property name="background-rgba" type="array">
        <value type="double" value="0.058824"/>
        <value type="double" value="0.090196"/>
        <value type="double" value="0.164706"/>
        <value type="double" value="0.820000"/>
      </property>
      <property name="plugin-ids" type="array">
        <value type="int" value="11"/>
        <value type="int" value="12"/>
        <value type="int" value="13"/>
        <value type="int" value="14"/>
        <value type="int" value="15"/>
      </property>
    </property>
  </property>
  <property name="plugins" type="empty">
    <property name="plugin-1" type="string" value="whiskermenu">
      <property name="button-icon" type="string" value="view-app-grid-symbolic"/>
      <property name="show-button-title" type="bool" value="false"/>
    </property>
    <property name="plugin-2" type="string" value="separator">
      <property name="expand" type="bool" value="true"/>
      <property name="style" type="uint" value="0"/>
    </property>
    <property name="plugin-3" type="string" value="clock">
      <property name="digital-format" type="string" value="%m月%d日 %a  %H:%M"/>
    </property>
    <property name="plugin-4" type="string" value="separator">
      <property name="expand" type="bool" value="true"/>
      <property name="style" type="uint" value="0"/>
    </property>
    <property name="plugin-5" type="string" value="systray">
      <property name="square-icons" type="bool" value="true"/>
      <property name="icon-size" type="int" value="18"/>
    </property>
    <property name="plugin-6" type="string" value="actions">
      <property name="appearance" type="uint" value="0"/>
      <property name="items" type="array">
        <value type="string" value="-lock-screen"/>
        <value type="string" value="-switch-user"/>
        <value type="string" value="-separator"/>
        <value type="string" value="-suspend"/>
        <value type="string" value="-hibernate"/>
        <value type="string" value="-hybrid-sleep"/>
        <value type="string" value="-separator"/>
        <value type="string" value="-shutdown"/>
        <value type="string" value="-restart"/>
        <value type="string" value="-separator"/>
        <value type="string" value="+logout"/>
      </property>
    </property>
    <property name="plugin-11" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="ChatGPT.desktop"/>
      </property>
    </property>
    <property name="plugin-12" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="Claude-Desktop.desktop"/>
      </property>
    </property>
    <property name="plugin-13" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="Browser.desktop"/>
      </property>
    </property>
    <property name="plugin-14" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="Workspace-Terminal.desktop"/>
      </property>
    </property>
    <property name="plugin-15" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="Display-Settings.desktop"/>
      </property>
    </property>
  </property>
</channel>
EOF

  # GTK3 visual polish: crisp desktop label shadows, rounded hover states, and comfortable terminal padding
  cat <<'EOF' > "${GTK3_DIR}/gtk.css"
/* Crisp desktop icon text over dark ambient wallpaper */
XfdesktopIconView.view {
  background: transparent;
  color: #f8fafc;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.85);
}

XfdesktopIconView.view:active,
XfdesktopIconView.view:selected {
  background-color: rgba(79, 124, 255, 0.42);
  border-radius: 8px;
  color: #ffffff;
}

/* Top bar & floating dock button polish */
.xfce4-panel {
  border-bottom: 1px solid rgba(148, 163, 184, 0.14);
}

.xfce4-panel button {
  border-radius: 8px;
  padding: 2px 6px;
  transition: background-color 120ms ease;
}

.xfce4-panel button:hover {
  background-color: rgba(255, 255, 255, 0.12);
}

/* Comfortable breathing room inside xfce4-terminal */
VteTerminal,
vte-terminal {
  padding: 10px 14px;
}

/* Subtle rounding for context menus & popovers */
menu,
.menu,
popover {
  border-radius: 8px;
}
EOF

  cat <<EOF > "${TERM_DIR}/terminalrc"
[Configuration]
FontName=JetBrains Mono ${INIT_TERM_FONT}
ColorForeground=${TERM_FG}
ColorBackground=${TERM_BG}
ColorCursor=${TERM_CURSOR}
ColorPalette=#1e293b;#f87171;#4ade80;#facc15;#60a5fa;#c084fc;#22d3ee;#e2e8f0;#475569;#fca5a5;#86efac;#fde047;#93c5fd;#d8b4fe;#67e8f9;#f8fafc
MiscMenubarDefault=FALSE
MiscBordersDefault=FALSE
ScrollingLines=10000
EOF

  touch "${USER_HOME}/.sa-theme-v3"
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

# Seed Claude Code PreToolUse hook (only if settings.json does not exist yet) so Claude Code
# automatically receives cloud container resource limits & small-batch reminders when local machine is offline.
CLAUDE_DIR="${USER_HOME}/.claude"
mkdir -p "${CLAUDE_DIR}"
if [ ! -f "${CLAUDE_DIR}/settings.json" ]; then
  cat <<'EOF' > "${CLAUDE_DIR}/settings.json"
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/sa-claude-hook"
          }
        ]
      }
    ]
  }
}
EOF
fi

# Seed Codex CLI config into persistent /config/.codex/config.toml on boot so existing /config volumes
# also receive it. Prefers OPENAI_BASE_URL if set; otherwise auto-detects the Docker bridge gateway IP.
CODEX_DIR="${USER_HOME}/.codex"
mkdir -p "${CODEX_DIR}"
if [ ! -f "${CODEX_DIR}/config.toml" ] || [ "${CODEX_OVERWRITE_CONFIG:-0}" = "1" ]; then
  CODEX_MODEL_VAL="${CODEX_MODEL:-GLM}"
  CODEX_BASE_URL_VAL="${OPENAI_BASE_URL:-}"
  if [ -z "${CODEX_BASE_URL_VAL}" ]; then
    GW_IP="$(ip route show default 2>/dev/null | awk '/default/ {print $3; exit}' || true)"
    if [ -z "${GW_IP}" ]; then
      GW_IP="172.18.0.1"
    fi
    CODEX_BASE_URL_VAL="http://${GW_IP}:18317/v1"
  fi
  cat <<EOF > "${CODEX_DIR}/config.toml"
model = "${CODEX_MODEL_VAL}"
model_provider = "glm"

[model_providers.glm]
name = "GLM via tunnel CPA"
base_url = "${CODEX_BASE_URL_VAL}"
wire_api = "responses"
env_key = "OPENAI_API_KEY"
EOF
fi

chmod +x "${DESKTOP_DIR}"/*.desktop
chown -R abc:abc "${DESKTOP_DIR}" "${AUTOSTART_DIR}" "${XFCONF_DIR}" "${PANEL_DIR}" "${TERM_DIR}" "${GTK3_DIR}" "${SA_CONF_DIR}" "${CLAUDE_DIR}" "${CODEX_DIR}" "${USER_HOME}/.sa-theme-v3" || true
