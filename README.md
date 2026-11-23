# Super-Agent

**海外云端 Ubuntu 桌面 + 本地双向实时同步 + 透明本机执行 + 跨平台托盘伴侣** 的一站式 AI 工作区。

> 完整部署步骤、架构细节与常见问题排查请参见 [docs/GUIDE.md](./docs/GUIDE.md)。

## 核心特性

- **美化云端 Ubuntu 桌面**：基于 [LinuxServer.io Webtop (`ubuntu-xfce`)](https://docs.linuxserver.io/images/docker-webtop/)，预置 `Arc-Dark` 深色主题、`Papirus-Dark` 图标、`JetBrains Mono` 字体、深海渐变壁纸与 fcitx5 中文输入法，内置 Chrome/Chromium、[Claude Code CLI](https://claudecodeguide.dev/docs/foundations/installation)、[OpenAI Codex CLI](https://docs.nexlev.io/docs/mcp/quickstart)，并支持预装 [ChatGPT 官方 Linux 桌面端](https://learn.chatgpt.com/docs/linux/linux-app) 与 [Claude 官方 Linux 桌面端](https://code.claude.com/docs/en/desktop-linux)（含 GNOME Keyring 登录态持久化）。
- **跨机器双向实时同步 + 单活设备锁**：本机代码仓库与云端 `/workspace` 毫秒级三向差分双向同步（支持普通文件与空文件夹），内置忽略 `node_modules`、`.venv`、`target`、`.git` 等目录并支持 `.syncignore`；同步元数据保存在系统用户配置目录，保持项目仓库彻底干净。
- **云端高速盘 + 命令前后强同步栅栏（透明本机执行）**：云端 AI 的文件检索与编辑直接走云端本地盘，而通过 `sa-local-sh` 转发的 Shell 命令会在你本机当前仓库目录真实执行（Pre-Sync 栅栏 → 本机安全护栏执行 → Post-Sync 栅栏）。
- **6 位一次性配对码免密绑定 + 跨平台托盘伴侣**：基于 Tauri 2.0 的 macOS 顶部菜单栏 / Windows 系统托盘客户端，在网页控制台生成 6 位配对码粘贴到托盘即可完成绑定，下发可随时在网页一键解绑吊销的独立设备凭证（`dev_...`），本机不保存主密码。

## 目录结构

```text
super-agent/
├── .env.example                           # 网关与容器配置模板
├── .github/workflows/release-tray.yml     # GitHub Actions 自动打包 Mac (.dmg) & Windows (.exe/.msi)
├── docs/GUIDE.md                          # 完整架构、构建、部署与排查手册
├── desktop/                               # 云端 Ubuntu 桌面镜像、主题美化脚本与 AI 启动器
├── gateway/                               # Go 1.22 纯标准库网关（鉴权、容器编排、同步服务、命令调度、配对码与 Web 门户）
├── sync-agent/                            # 跨平台本地同步与命令执行守护引擎（macOS / Windows / Linux）
└── tray-app/                              # Tauri 2.0 跨平台菜单栏 / 系统托盘应用
```

## 快速上手

### 1. 构建并启动服务端（网关 + 云桌面镜像）

```bash
cp -n .env.example .env
docker network create super-agent-net || true

# 构建美化后的云桌面镜像（如需预装官方 ChatGPT / Claude .deb，详见 docs/GUIDE.md）
docker build -t super-agent-desktop:latest ./desktop

# 构建 Go 网关镜像（纯标准库，零外部依赖）
docker build -t super-agent-gateway:latest ./gateway
```

在 `.env` 中配置 `JWT_SECRET`、`AUTH_USERS` 以及共享工作区目录（`WORKSPACE_DATA_DIR` / `WORKSPACE_HOST_DIR`），然后按 [docs/GUIDE.md](./docs/GUIDE.md) 第 2.3 节启动 `super-agent-gateway` 容器并打开 `http://localhost:8080`。

### 2. 启动本机托盘伴侣并配对绑定

```bash
cd tray-app
npm install
npm run dev     # 开发模式启动（顶部菜单栏出现 SA 图标）
npm run build   # 打包本机安装程序（macOS .dmg / Windows .exe）
```

1. 浏览器登录云端控制台 → 点击 **「生成 6 位配对码」** → **「一键复制绑定口令」**。
2. 打开本机 **Super-Agent** 托盘应用 → 粘贴绑定口令 → 点击 **「选择文件夹…」** 选中本地项目目录 → 点击 **「立即绑定并启动同步」**。
3. 进入云桌面打开终端运行 `sa-local-sh -c "uname -a && pwd"`，确认命令已透明转发至本机仓库执行。

## 测试与发版

```bash
# 运行网关与同步引擎全部单元测试
(cd gateway && go test -v ./... && go vet ./...)
(cd sync-agent && go test -v ./... && go vet ./...)

# 推送并打标签以触发 GitHub Actions 自动发布 macOS & Windows 托盘安装包
git push -u origin main
git tag v0.1.0 && git push origin v0.1.0
```
