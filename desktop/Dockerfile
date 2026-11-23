# Super-Agent Phase 1 MVP Cloud Desktop Image
# Base: LinuxServer Webtop (Ubuntu + XFCE + KasmVNC)
# Ref: https://docs.linuxserver.io/images/docker-webtop/
FROM lscr.io/linuxserver/webtop:ubuntu-xfce

LABEL org.opencontainers.image.title="super-agent-desktop" \
      org.opencontainers.image.description="Ubuntu XFCE KasmVNC desktop with Node.js, Python, Chrome, Chinese IME, Claude Code, and OpenAI Codex"

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=zh_CN.UTF-8 \
    LANGUAGE=zh_CN:zh:en_US:en \
    LC_ALL=zh_CN.UTF-8 \
    GTK_IM_MODULE=fcitx \
    QT_IM_MODULE=fcitx \
    XMODIFIERS=@im=fcitx \
    SDL_IM_MODULE=fcitx \
    NODE_MAJOR=22

# 1. Core system tools, Python 3, Chinese locale, fonts, and fcitx5 Pinyin IME
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    gnupg \
    git \
    tmux \
    vim \
    jq \
    ripgrep \
    unzip \
    zip \
    build-essential \
    python3 \
    python3-pip \
    python3-venv \
    locales \
    fonts-noto-cjk \
    fonts-wqy-zenhei \
    fcitx5 \
    fcitx5-chinese-addons \
    fcitx5-frontend-gtk3 \
    fcitx5-frontend-qt5 \
    fcitx5-config-qt \
    im-config \
    && sed -i '/zh_CN.UTF-8/s/^# //g' /etc/locale.gen \
    && sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen \
    && locale-gen \
    && rm -rf /var/lib/apt/lists/*

# 2. Node.js 22 LTS (reuse base image Node/NodeSource repo if already configured)
RUN if ! command -v node >/dev/null 2>&1; then \
      if [ ! -f /usr/share/keyrings/nodesource.gpg ] && [ ! -f /etc/apt/keyrings/nodesource.gpg ]; then \
        mkdir -p /usr/share/keyrings \
        && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
           | gpg --dearmor -o /usr/share/keyrings/nodesource.gpg \
        && echo "deb [signed-by=/usr/share/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" \
           > /etc/apt/sources.list.d/nodesource.list; \
      fi \
      && apt-get update \
      && apt-get install -y --no-install-recommends nodejs \
      && rm -rf /var/lib/apt/lists/*; \
    fi \
    && npm install -g pnpm

# 3. Google Chrome (amd64) or preinstalled/fallback browser (arm64 / Apple Silicon local dev)
RUN ARCH="$(dpkg --print-architecture)" \
    && if [ "$ARCH" = "amd64" ]; then \
         wget -qO /tmp/google-chrome.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb \
         && apt-get update \
         && apt-get install -y --no-install-recommends /tmp/google-chrome.deb \
         && rm -f /tmp/google-chrome.deb \
         && rm -rf /var/lib/apt/lists/*; \
       elif ! command -v chromium >/dev/null 2>&1 && ! command -v firefox >/dev/null 2>&1; then \
         apt-get update \
         && (apt-get install -y --no-install-recommends chromium || apt-get install -y --no-install-recommends firefox || true) \
         && rm -rf /var/lib/apt/lists/*; \
       fi

# 4. Install Claude Code CLI & OpenAI Codex CLI globally
RUN npm install -g @anthropic-ai/claude-code @openai/codex \
    && npm cache clean --force

# 5. Browser wrapper for non-privileged Docker sandbox (avoids --privileged)
RUN printf '#!/bin/sh\n\
if command -v google-chrome-stable >/dev/null 2>&1; then\n\
  exec google-chrome-stable --no-sandbox --disable-dev-shm-usage "$@"\n\
elif command -v chromium >/dev/null 2>&1; then\n\
  exec chromium --no-sandbox --disable-dev-shm-usage "$@"\n\
elif command -v chromium-browser >/dev/null 2>&1; then\n\
  exec chromium-browser --no-sandbox --disable-dev-shm-usage "$@"\n\
elif command -v firefox >/dev/null 2>&1; then\n\
  exec firefox "$@"\n\
else\n\
  exec xdg-open "$@"\n\
fi\n' > /usr/local/bin/super-agent-browser \
    && chmod +x /usr/local/bin/super-agent-browser

# 6. Modern Dark Theme (Arc-Dark + Papirus-Dark + JetBrains Mono + Color Emoji), Display Switcher, Keyring & optional official .deb pre-bake
ARG CLAUDE_DESKTOP_DEB_URL=""
ARG CHATGPT_DESKTOP_DEB_URL=""
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       gnome-keyring \
       libsecret-1-0 \
       arc-theme \
       papirus-icon-theme \
       fonts-jetbrains-mono \
       fonts-noto-color-emoji \
       xfce4-whiskermenu-plugin \
       zenity \
    && if [ -n "${CLAUDE_DESKTOP_DEB_URL}" ]; then \
         wget -qO /tmp/claude-desktop.deb "${CLAUDE_DESKTOP_DEB_URL}" \
         && (apt-get install -y --no-install-recommends /tmp/claude-desktop.deb || true) \
         && rm -f /tmp/claude-desktop.deb; \
       fi \
    && if [ -n "${CHATGPT_DESKTOP_DEB_URL}" ]; then \
         wget -qO /tmp/chatgpt-desktop.deb "${CHATGPT_DESKTOP_DEB_URL}" \
         && (apt-get install -y --no-install-recommends /tmp/chatgpt-desktop.deb || true) \
         && rm -f /tmp/chatgpt-desktop.deb; \
       fi \
    && rm -rf /var/lib/apt/lists/*

# 7. Generate dark gradient wallpaper & custom AI app icons
COPY scripts/setup-theme.sh /tmp/setup-theme.sh
RUN chmod +x /tmp/setup-theme.sh && /tmp/setup-theme.sh && rm -f /tmp/setup-theme.sh

# 8. Prepare shared /workspace mount point, launchers, display switcher, transparent local shell wrapper, and s6 init script
RUN mkdir -p /workspace /custom-cont-init.d
COPY scripts/sa-local-sh /usr/local/bin/sa-local-sh
COPY scripts/sa-display /usr/local/bin/sa-display
COPY scripts/super-agent-chatgpt /usr/local/bin/super-agent-chatgpt
COPY scripts/super-agent-claude-desktop /usr/local/bin/super-agent-claude-desktop
COPY scripts/init-workspace.sh /custom-cont-init.d/99-super-agent-init.sh
RUN chmod +x \
    /usr/local/bin/sa-local-sh \
    /usr/local/bin/sa-display \
    /usr/local/bin/super-agent-chatgpt \
    /usr/local/bin/super-agent-claude-desktop \
    /custom-cont-init.d/99-super-agent-init.sh

ENV BROWSER=/usr/local/bin/super-agent-browser

EXPOSE 3000 3001
