# Super-Agent 完整项目文档与部署手册

Super-Agent 是一套 **「海外云端 Ubuntu 桌面 + 本地双向实时同步 + 透明本机执行 + 跨平台托盘伴侣」** 的完整 AI 工作区方案：

- **云端 Ubuntu 图形桌面（美化深色主题）**：基于 [LinuxServer.io Webtop (`ubuntu-xfce`)](https://docs.linuxserver.io/images/docker-webtop/)，预置 `Arc-Dark` 深色主题、`Papirus-Dark` 图标、`JetBrains Mono` 字体与深海渐变壁纸，内置 Chrome/Chromium、fcitx5 中文输入法、[Claude Code CLI](https://claudecodeguide.dev/docs/foundations/installation)、[OpenAI Codex CLI](https://docs.nexlev.io/docs/mcp/quickstart)，并支持直接预装 [ChatGPT 官方 Linux 桌面端](https://learn.chatgpt.com/docs/linux/linux-app) 与 [Claude 官方 Linux 桌面端](https://code.claude.com/docs/en/desktop-linux)（含 GNOME Keyring 登录态持久化）。
- **跨机器双向实时同步 + 单活设备锁**：本地与云端 `/workspace` 三向差分同步（支持普通文件与空文件夹），自动忽略 `node_modules`、`.venv`、`target`、`.git` 等大体积目录（支持 `.syncignore`），并将元数据存于系统用户配置目录，保持项目仓库彻底干净。
- **云端高速盘 + 命令前后强同步栅栏**：云端 AI 的 `Read`/`Edit`/`Grep` 走云端高速盘，而 Shell 命令通过 `sa-local-sh` 透明转发到本机仓库目录真实执行（Pre-Sync → 本机受限执行 + 高危护栏 → Post-Sync）。
- **6 位一次性配对码免密绑定 + 跨平台托盘应用**：基于 Tauri 2.0 的 macOS 顶部菜单栏 / Windows 系统托盘应用，网页生成 6 位配对码粘贴到托盘即可完成绑定，下发可随时在网页解绑吊销的独立设备凭证（`dev_...`）。

---

## 1. 项目目录结构

```text
super-agent/
├── .env.example                           # 网关与容器环境变量模板
├── .github/workflows/release-tray.yml     # GitHub Actions 自动打包 Mac (.dmg) & Windows (.exe/.msi)
├── docs/GUIDE.md                          # 本文档：完整架构、构建、部署与使用手册
├── desktop/
│   ├── Dockerfile                         # Ubuntu XFCE 美化桌面镜像（含 Keyring、主题、官方桌面端构建参数）
│   └── scripts/
│       ├── setup-theme.sh                 # 生成 1920x1080 深色渐变壁纸与 ChatGPT/Claude SVG 图标
│       ├── init-workspace.sh              # s6 启动脚本：应用深色 XFCE 主题、自启 fcitx5/Keyring 与桌面图标
│       ├── sa-local-sh                    # 透明本地 Shell 转发器（AI 命令转发至本机仓库执行）
│       ├── super-agent-chatgpt            # ChatGPT 桌面端启动包装（支持网页独立窗口自动回退）
│       └── super-agent-claude-desktop     # Claude Desktop 启动包装（支持网页独立窗口自动回退）
├── gateway/
│   ├── Dockerfile                         # Go 1.22 多阶段构建（纯标准库，零外部依赖）
│   ├── cmd/gateway/main.go                # 网关入口
│   └── internal/
│       ├── auth/                          # HS256 JWT 签发与校验
│       ├── config/                        # 环境变量配置加载
│       ├── docker/                        # 原生 Docker Engine REST 客户端与单用户容器生命周期管理
│       ├── execbridge/                    # 反向长轮询命令调度器
│       ├── proxy/                         # KasmVNC/Selkies HTTP & WebSocket 反向代理
│       ├── syncsvc/                       # 双向同步清单/文件/空目录服务、单活设备锁与 6 位配对码注册表
│       └── server/
│           ├── server.go                  # 路由与 JWT / DeviceToken 双重鉴权中间件
│           ├── sync_handlers.go           # /api/sync/* 同步接口
│           ├── exec_handlers.go           # /api/exec/* 远程命令接口
│           ├── pair_handlers.go           # /api/devices/* 配对码生成、兑换与解绑接口
│           └── web/index.html             # 内嵌 Web 登录门户、控制台、配对码与设备管理页
├── sync-agent/
│   ├── main.go                            # 跨平台同步与本地执行引擎（支持 -serve 托盘模式与 -config CLI 模式）
│   └── internal/syncer/
│       ├── engine.go                      # 三向差分双向同步引擎（文件 + 空目录）
│       ├── executor.go                    # 本机安全命令执行器（路径映射 + 高危护栏 + Pre/Post 同步栅栏）
│       ├── control.go                     # 托盘本地控制面 HTTP API（:17890）与父进程退出自动清理
│       ├── client*.go                     # 网关同步、命令长轮询与配对码兑换客户端
│       └── ignore.go                      # 默认忽略规则与 .syncignore 解析
└── tray-app/
    ├── package.json                       # Tauri 2.0 构建与开发脚本
    ├── ui/                                # 免密配对界面、控制台与实时日志面板
    └── src-tauri/                         # Rust 托盘/菜单栏宿主与内置 sync-agent sidecar 托管
```

---

## 2. 服务端构建与部署（本地 OrbStack 或海外 Linux 服务器）

### 2.1 准备环境变量与共享工作区目录

```bash
cd /Users/machaojin/AIProject/super-agent
cp -n .env.example .env

# 创建网关与桌面容器共享的工作区根目录（本地调试可用 /tmp/sa-workspaces，服务器建议 /data/workspaces）
export SA_WORKSPACES=/tmp/sa-workspaces
mkdir -p "${SA_WORKSPACES}"
```

编辑 `.env`，确认关键项（生产环境请用 `openssl rand -hex 32` 更新 `JWT_SECRET` 并修改 `AUTH_USERS`）：

```env
GATEWAY_PORT=8080
JWT_SECRET=替换为至少32位随机字符串
AUTH_USERS=admin:你的强密码
DESKTOP_MEMORY_MB=6144
DESKTOP_CPUS=2
DESKTOP_SHM_MB=2048
WORKSPACE_DATA_DIR=/tmp/sa-workspaces
WORKSPACE_HOST_DIR=/tmp/sa-workspaces
```

### 2.2 构建云端 Ubuntu 桌面镜像（内置美化主题 + ChatGPT / Claude 桌面端）

```bash
docker network create super-agent-net || true

# 方式 A：标准构建（内置深色美化主题、Chrome、fcitx5、Claude Code CLI、Codex CLI，
# ChatGPT / Claude 图标自动以独立无地址栏桌面窗口打开）
docker build -t super-agent-desktop:latest ./desktop

# 方式 B：预装官方 Linux .deb 原生桌面端（将官方页面下载的 amd64/arm64 .deb URL 传入构建参数）
# 官方文档：
# - ChatGPT Desktop for Linux: https://learn.chatgpt.com/docs/linux/linux-app
# - Claude Desktop for Linux:  https://code.claude.com/docs/en/desktop-linux
docker build \
  --build-arg CHATGPT_DESKTOP_DEB_URL="<ChatGPT官方deb链接>" \
  --build-arg CLAUDE_DESKTOP_DEB_URL="<Claude官方deb链接>" \
  -t super-agent-desktop:latest ./desktop
```

### 2.3 构建并启动 Go 网关容器

```bash
docker build -t super-agent-gateway:latest ./gateway
docker rm -f super-agent-gateway sa-desktop-admin 2>/dev/null || true

docker run -d \
  --name super-agent-gateway \
  --restart unless-stopped \
  --network super-agent-net \
  -p 8080:8080 \
  --env-file .env \
  -v "${DOCKER_SOCK:-/var/run/docker.sock}:/var/run/docker.sock" \
  -v "${SA_WORKSPACES}:${SA_WORKSPACES}" \
  super-agent-gateway:latest

docker logs -f super-agent-gateway
```

> **公网 HTTPS 建议**：海外服务器上线时，推荐在网关前加一层 Caddy 自动签发 TLS 1.3 证书，并将 `.env` 中的 `COOKIE_SECURE` 设为 `true`：
> ```caddyfile
> desktop.example.com {
>     reverse_proxy 127.0.0.1:8080
> }
> ```

---

## 3. 本机托盘伴侣应用（macOS / Windows）

### 3.1 编译三平台同步引擎与本地运行/打包

```bash
# 1. 交叉编译 Mac (Apple Silicon / Intel) 与 Windows 同步引擎（改动 Go 代码后执行）
cd /Users/machaojin/AIProject/super-agent/sync-agent
mkdir -p ../tray-app/src-tauri/binaries
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../tray-app/src-tauri/binaries/sync-agent-aarch64-apple-darwin .
GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../tray-app/src-tauri/binaries/sync-agent-x86_64-apple-darwin .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../tray-app/src-tauri/binaries/sync-agent-x86_64-pc-windows-msvc.exe .

# 2. 启动开发模式或打出本机安装包
cd /Users/machaojin/AIProject/super-agent/tray-app
npm install
npm run dev     # 开发模式：顶部菜单栏出现 SA 图标并打开控制窗口
npm run build   # 打包发布：产物位于 src-tauri/target/release/bundle/
```

### 3.2 GitHub Actions 自动打包发布双平台安装包

```bash
cd /Users/machaojin/AIProject/super-agent
git push -u origin main
git tag v0.1.0
git push origin v0.1.0
```

推送 `v*` 标签后，GitHub Actions 会自动编译并在 Releases 生成 macOS（`.dmg`）与 Windows（`.exe` / `.msi`）安装包供直接下载使用。

### 3.3 新用户 6 位配对码免密绑定流程

1. 浏览器打开云端控制台（本地调试为 `http://localhost:8080`）并登录。
2. 在 **「本机伴侣绑定」** 区域点击 **「生成 6 位配对码」**，再点 **「一键复制绑定口令（含网关地址）」**。
3. 打开本机 **Super-Agent** 托盘应用，粘贴绑定口令并点击 **「选择文件夹…」** 选定本地项目仓库目录。
4. 点击 **「立即绑定并启动同步」**，即可开始双向实时同步并激活本机透明命令执行通道。
5. 随时可在网页控制台的「已绑定设备」列表中点击 **「解绑」** 吊销任意设备凭证。

---

## 4. 验证、测试与排查

### 验证透明本地执行

在云端 Ubuntu 桌面打开终端运行：

```bash
sa-local-sh -c "uname -a && pwd && ls -la"
```

返回本机系统信息与本地仓库文件列表即表示 **Pre-Sync → 本机执行 → Post-Sync** 全链路正常。如需临时切回云端容器执行，在终端设置 `SA_LOCAL_EXEC=0` 即可。

### 运行全套单元测试

```bash
(cd /Users/machaojin/AIProject/super-agent/gateway && go test -v ./... && go vet ./...)
(cd /Users/machaojin/AIProject/super-agent/sync-agent && go test -v ./... && go vet ./...)
```

### 常见问题

- **更新桌面美化主题未生效**：旧用户的 `/config` 持久化卷已保存过旧 XFCE 配置，可在容器终端执行 `rm -f /config/.sa-theme-v1` 后重启桌面容器，或重新创建测试账号即可应用全新深色主题与壁纸。
- **端口 `:17890` 被占用**：运行 `pkill -f sync-agent || true` 清理残留进程后重启托盘应用。
- **文件未同步**：确认文件建在 `/workspace` 下（而非 `/config` 或桌面），且不在默认忽略列表（`node_modules`/`.venv`/`target`/`.git`）中。
