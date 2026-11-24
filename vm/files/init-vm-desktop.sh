#!/usr/bin/env bash
# Initialize XFCE desktop shortcuts, theme, autostart, Chrome policies, and Claude hook for ubuntu user in KVM VM.
set -euo pipefail

USER_NAME="${SA_VM_USER:-ubuntu}"
USER_HOME="/home/${USER_NAME}"
DESKTOP_DIR="${USER_HOME}/Desktop"
AUTOSTART_DIR="${USER_HOME}/.config/autostart"
XFCONF_DIR="${USER_HOME}/.config/xfce4/xfconf/xfce-perchannel-xml"
TERM_DIR="${USER_HOME}/.config/xfce4/terminal"
GTK3_DIR="${USER_HOME}/.config/gtk-3.0"
KEYRING_DIR="${USER_HOME}/.local/share/keyrings"
CLAUDE_DIR="${USER_HOME}/.claude"
FCITX_DIR="${USER_HOME}/.config/fcitx5"

mkdir -p /workspace "${DESKTOP_DIR}" "${AUTOSTART_DIR}" "${XFCONF_DIR}" "${TERM_DIR}" "${GTK3_DIR}" "${KEYRING_DIR}" "${CLAUDE_DIR}" "${FCITX_DIR}" /etc/opt/chrome/policies/managed

# Chrome managed policies (skip Google signin, metrics, default browser, password manager prompts)
cat <<'EOF' > /etc/opt/chrome/policies/managed/super-agent.json
{
  "BrowserSignin": 0,
  "MetricsEnabled": false,
  "DefaultBrowserSettingEnabled": false,
  "PasswordManagerEnabled": false,
  "SafeBrowsingProtectionLevel": 1
}
EOF

if [ -f /usr/share/applications/google-chrome.desktop ]; then
  sed -i 's|^Exec=/usr/bin/google-chrome-stable|Exec=/usr/local/bin/super-agent-browser|g' /usr/share/applications/google-chrome.desktop
fi

# Desktop shortcuts (matching Docker desktop)
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
Comment=Open browser in KVM VM network
Exec=/usr/local/bin/super-agent-browser
Icon=google-chrome
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
Icon=sa-claude-code
Terminal=false
Categories=Development;
EOF

cat <<'EOF' > "${DESKTOP_DIR}/OpenAI-Codex-CLI.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=Codex
Comment=OpenAI Codex Desktop App (ChatGPT Linux App with Codex)
Exec=/usr/local/bin/super-agent-chatgpt
Icon=sa-codex
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
Icon=sa-terminal
Terminal=false
Categories=System;TerminalEmulator;
EOF

cat <<'EOF' > "${DESKTOP_DIR}/Workspace-Folder.desktop"
[Desktop Entry]
Version=1.0
Type=Application
Name=工作区
Comment=Open /workspace synced repositories
Exec=exo-open --launch FileManager /workspace
Icon=sa-workspace
Terminal=false
Categories=System;FileManager;
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

chmod +x "${DESKTOP_DIR}"/*.desktop

# XFCE dark theme, terminal titlebar, and untruncated icon labels
cat <<'EOF' > "${XFCONF_DIR}/xsettings.xml"
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xsettings" version="1.0">
  <property name="Net" type="empty">
    <property name="ThemeName" type="string" value="Arc-Dark"/>
    <property name="IconThemeName" type="string" value="Papirus-Dark"/>
    <property name="FallbackIconThemeName" type="string" value="hicolor"/>
  </property>
  <property name="Gtk" type="empty">
    <property name="FontName" type="string" value="Noto Sans CJK SC 11"/>
    <property name="MonospaceFontName" type="string" value="JetBrains Mono 11"/>
    <property name="CursorThemeSize" type="int" value="24"/>
  </property>
  <property name="Xft" type="empty">
    <property name="DPI" type="int" value="96"/>
    <property name="Antialias" type="int" value="1"/>
    <property name="Hinting" type="int" value="1"/>
    <property name="HintStyle" type="string" value="hintslight"/>
    <property name="RGBA" type="string" value="rgb"/>
  </property>
</channel>
EOF

cat <<'EOF' > "${TERM_DIR}/terminalrc"
[Configuration]
FontName=JetBrains Mono 12
ColorForeground=#e6ecf8
ColorBackground=#0b1020
ColorCursor=#4f7cff
MiscMenubarDefault=FALSE
MiscBordersDefault=TRUE
ScrollingLines=10000
EOF

cat <<'EOF' > "${GTK3_DIR}/gtk.css"
XfdesktopIconView.view {
  background: transparent;
  color: #f8fafc;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.85);
  -XfdesktopIconView-cell-text-width-proportion: 2.5;
  -XfdesktopIconView-ellipsize-icon-labels: false;
}
EOF

# Add /workspace to Thunar / GTK file-chooser sidebar bookmarks
if ! grep -qx 'file:///workspace 工作区' "${GTK3_DIR}/bookmarks" 2>/dev/null; then
  printf 'file:///workspace 工作区\n' >> "${GTK3_DIR}/bookmarks"
fi

# Passwordless unlocked keyring so Chrome/apps don't prompt
if [ ! -f "${KEYRING_DIR}/default" ]; then
  printf 'Default_keyring\n' > "${KEYRING_DIR}/default"
  cat <<'EOF' > "${KEYRING_DIR}/Default_keyring.keyring"
[keyring]
display-name=Default keyring
ctime=0
mtime=0
lock-on-idle=false
lock-after=false
EOF
  chmod 600 "${KEYRING_DIR}/Default_keyring.keyring"
fi

# Autostart helpers (IME, keyring, trusted launchers, window bounds guard)
cat <<'EOF' > "${AUTOSTART_DIR}/fcitx5.desktop"
[Desktop Entry]
Type=Application
Name=Fcitx 5
Exec=fcitx5 -d
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
EOF

cat <<'EOF' > "${AUTOSTART_DIR}/gnome-keyring.desktop"
[Desktop Entry]
Type=Application
Name=GNOME Keyring
Exec=sh -c 'printf "\n" | gnome-keyring-daemon --unlock --start --components=secrets'
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
EOF

cat <<'EOF' > "${AUTOSTART_DIR}/sa-trust-desktop.desktop"
[Desktop Entry]
Type=Application
Name=Super-Agent Trust Desktop Launchers
Exec=/usr/local/bin/sa-trust-desktop
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
EOF

cat <<'EOF' > "${AUTOSTART_DIR}/sa-window-guard.desktop"
[Desktop Entry]
Type=Application
Name=Super-Agent Window Bounds Guard
Exec=/usr/local/bin/sa-window-guard
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
EOF

# Claude Code PreToolUse hook
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

# Pre-configure fcitx5 with English keyboard + Chinese Pinyin so Ctrl+Space switches immediately
cat <<'EOF' > /etc/profile.d/fcitx5.sh
export LANG=zh_CN.UTF-8
export LANGUAGE=zh_CN:zh:en_US:en
export LC_ALL=zh_CN.UTF-8
export GTK_IM_MODULE=fcitx
export QT_IM_MODULE=fcitx
export XMODIFIERS=@im=fcitx
export SDL_IM_MODULE=fcitx
export INPUT_METHOD=fcitx
EOF
chmod 0644 /etc/profile.d/fcitx5.sh

if [ ! -f "${FCITX_DIR}/profile" ]; then
  cat <<'EOF' > "${FCITX_DIR}/profile"
[Groups/0]
Name=Default
Default Layout=us
DefaultIM=pinyin

[Groups/0/Items/0]
Name=keyboard-us
Layout=

[Groups/0/Items/1]
Name=pinyin
Layout=

[GroupOrder]
0=Default
EOF
fi

if [ ! -f "${FCITX_DIR}/config" ]; then
  cat <<'EOF' > "${FCITX_DIR}/config"
[Hotkey/TriggerKeys]
0=Control+space
1=Zenkaku_Hankaku
2=Hangul

[Behavior]
ActiveByDefault=False
ShareInputState=No
ShowInputMethodInformation=True
EOF
fi

chown -R "${USER_NAME}:${USER_NAME}" /workspace "${USER_HOME}/.config" "${USER_HOME}/.local" "${USER_HOME}/.claude" "${DESKTOP_DIR}" "${AUTOSTART_DIR}" "${XFCONF_DIR}" "${TERM_DIR}" "${GTK3_DIR}" "${KEYRING_DIR}" "${FCITX_DIR}"
