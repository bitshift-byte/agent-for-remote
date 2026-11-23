# Super-Agent 变更日志与配置变动记录 (CHANGELOG)

本文档完整记录了 Super-Agent 从 Phase 1 MVP 初始化到应用层端到端加密（AES-256-GCM）的每一次提交变更，以及对应的环境变量、构建参数、本地配置文件和通信协议变动。

---

## 一、全量提交变更记录（按时间顺序）

### 1. Phase 1 MVP：云端 Ubuntu 桌面与 Go 网关
- **`e092f20`** `feat: initialize Phase 1 MVP (Ubuntu KasmVNC desktop + Go gateway)`
  - 新增 `desktop/`：基于 [LinuxServer.io Webtop](https://docs.linuxserver.io/images/docker-webtop/) (`ubuntu-xfce`) 的自定义云端桌面镜像，内置中文字体、Node.js、常用开发工具及 [Claude Code CLI](https://claudecodeguide.dev/docs/foundations/installation) / [OpenAI Codex CLI](https://docs.nexlev.io/docs/mcp/quickstart)。
  - 新增 `gateway/`：零外部依赖 Go 网关，实现账号密码登录、JWT 鉴权、Docker Engine REST 容器编排（按用户启停桌面容器）、HTTP/WebSocket 反向代理以及内嵌 Web 门户 (`gateway/internal/server/web/index.html`)。
  - 新增 `docker-compose.yml` 与 `.env.example`。
- **`a589164`** `feat(gateway): add optional MOCK_DESKTOP mode for local debugging without Docker`
  - 为 `gateway` 新增 `MOCK_DESKTOP` 开关，方便在没有 Docker 环境的本机调试登录、Web 门户和 API 流程。
- **`9142ccd`** `fix(desktop): avoid duplicate NodeSource GPG keyring and harden arm64 browser install`
  - 修复镜像构建时 NodeSource GPG 密钥环重复冲突问题，并加固 `arm64` 架构下浏览器的安装脚本。

### 2. Phase 2：跨机双向文件同步与单活跃设备锁
- **`940f36f`** `feat(phase2): add cross-machine bidirectional sync daemon and active device lock`
  - 网关新增 `/api/sync/*` 接口与 `syncsvc` 模块，支持工作区文件清单拉取、增量上传/下载/删除以及按用户维度的**单活跃设备锁（Active Device Lock）**，支持抢占接管与旧设备自动暂停。
  - 新增独立跨平台 Go 守护进程 `sync-agent/`，支持双向文件同步与 `.gitignore` 风格忽略规则。
- **`b09bbf4`** `chore(logging): add detailed container mount, device lock, and file sync logs`
  - 补充容器挂载路径、设备锁状态切换与文件同步明细日志，便于排查跨机同步问题。
- **`a6c481a`** `feat(sync): support bidirectional sync for empty directories`
  - 扩展同步协议与清单格式，支持空目录在云端与本机之间的双向创建与删除同步。

### 3. 云端 Shell 透明转发与同步屏障
- **`93006a2`** `feat(exec-bridge): add transparent local shell forwarding with pre/post sync barriers`
  - 网关新增 `/api/exec/*` 反向长轮询代理（`execbridge`），无需本机开放公网端口。
  - `sync-agent` 新增本地安全执行器：路径自动映射（`/workspace` → 本地项目目录）、高危命令护栏拦截，并在执行前后强制插入 **Pre-Sync（执行前同步）→ 本地执行 → Post-Sync（执行后回传）** 同步屏障。
  - 云端桌面内置 `sa-local-sh` 透明包装脚本，将工作区命令转发至本机执行。

### 4. 跨平台 Tauri 2.0 托盘伴侣应用
- **`7a5e204`** `feat(tray-app): add cross-platform Tauri 2.0 macOS/Windows tray companion and local control API`
  - 新增 `tray-app/`（macOS 菜单栏 + Windows 系统托盘），将 `sync-agent` 以 `-serve` 模式作为 sidecar 打包，暴露本地控制 API。
  - 新增 `.github/workflows/release-tray.yml` 双平台（macOS / Windows）自动构建发布工作流。
- **`eef133b`** `feat(pairing): add 6-char pairing code device binding, DeviceToken auth, and web/tray UI`
  - 实现 **6 位一次性配对码**绑定流程：Web 门户生成配对码及 `super-agent://pair?server=...&code=XXX-XXX` 绑定串，托盘端粘贴即可换取可随时吊销的 `dev_...` `DeviceToken`。
  - Web 门户新增已绑定设备列表与一键吊销能力。
- **`72097dd`** `fix(tray-app): prevent macOS window flash-hide on startup and add SA menu bar label`
  - 修复 macOS 启动时窗口闪现后隐藏的问题，并在状态栏添加 `SA` 标签。
- **`5742a00`** `fix(tray-app): make pick_folder async to avoid macOS main-thread dialog deadlock`
  - 将原生目录选择命令 `pick_folder` 改为异步调用，解决 macOS 主线程文件选择器死锁问题。
- **`3a257e7`** `fix(tray-app): align sidecar listen host to localhost:17890 and forward sidecar logs`
  - 统一 sidecar 监听地址与前端请求地址，并将 sidecar 标准输出/错误日志转发至主进程便于诊断。
- **`62680fd`** `fix(tray-app): bind dual-stack :17890, add PNA CORS header, and auto-exit orphaned sidecar`
  - sidecar 监听改为双栈 `:17890`，增加 `Access-Control-Allow-Private-Network: true` 响应头，并在父进程关闭（stdin EOF）时自动退出以防孤儿进程残留。

### 5. 云端桌面官方 AI 客户端、主题美化与文档
- **`0331b5f`** `feat(desktop): integrate ChatGPT Desktop and Claude Desktop launchers, GNOME Keyring, and official .deb build args`
  - 集成 [ChatGPT Linux 官方桌面端](https://learn.chatgpt.com/docs/linux/linux-app) 与 [Claude Linux 官方桌面端](https://code.claude.com/docs/en/desktop-linux) 启动器（移除 Cursor），支持通过 `--build-arg` 传入官方 `.deb` 下载地址；集成 `gnome-keyring` 持久化登录态，并提供无特权包装器与 Web 应用兜底。
- **`0e9e8a9`** `feat(desktop,docs): add Arc-Dark + Papirus theme, gradient wallpaper, custom AI icons, and full GUIDE.md`
  - 美化云端 XFCE 桌面：默认启用 `Arc-Dark` 主题、`Papirus-Dark` 图标、`JetBrains Mono` 等宽字体、1920×1080 渐变壁纸与自定义 AI 矢量图标。
  - 新增完整项目与部署指南 `docs/GUIDE.md`。
- **`5a895e8`** `docs: update README.md with full architecture, tray app, pairing code, and GUIDE.md link`
  - 全面更新 `README.md`，补充架构图、托盘伴侣、配对码绑定流程及 `docs/GUIDE.md` 索引。

### 6. 本次新增：应用层端到端载荷加密（AES-256-GCM）
- **`c216080`** `feat(crypto): add AES-256-GCM payload encryption bootstrapped via 6-char pairing code`
  - **密钥协商与分发**：配对码认领时，网关为该设备生成 256-bit 随机 `payloadKey`，使用 `SHA-256("super-agent-pair-wrap-v1:" + pairCode + ":" + deviceID + ":" + 32-byte salt)` 派生密钥包裹（AES-256-GCM Key Wrap）后随响应下发，托盘/守护进程本地用同一 6 位配对码解包并持久化。
  - **文件同步载荷加密**：文件上传与下载载荷全部使用该设备密钥进行 AES-256-GCM 加密，附加认证数据（AAD）绑定文件相对路径 `relPath`，防止密文被替换或篡改。
  - **远程执行载荷加密**：云端下发的执行任务与本机回传的 stdout/stderr/退出码均以 AES-256-GCM 加密传输（AAD 分别为 `exec-job` / `exec-result`）。
  - **测试覆盖**：新增 `TestAES256GCMRoundTripAndTamperDetection` 与 `TestPairCodeKeyWrapAndUnwrap` 单元测试，全量 `go test ./...` 与双平台 sidecar 编译通过。

### 7. 云端 Ubuntu 桌面样式升级与实时字号/缩放调节
- **`6892b21`** `feat(desktop,gateway): add live font/DPI/theme switcher, top bar + bottom dock, and GTK3 polish`
  - 新增 `desktop/scripts/sa-display`（桌面「显示与字号设置」快捷方式 + 命令行工具）：基于 `zenity` 提供图形化弹窗与 CLI 参数，支持 **100% / 125% / 150% / 175%** 一键预设、独立微调（DPI、系统字号、桌面图标字号/尺寸、终端字号）以及 **Arc-Dark / Arc 浅色主题** 切换，通过 `xfconf-query` 实时生效，无需重启容器。
  - 升级 `desktop/scripts/init-workspace.sh`（标记升级为 `.sa-theme-v2`）：将默认缩放提升至更舒适的 `115 DPI`（UI `11pt`、桌面图标 `11.5pt`、终端 `12pt`），替换为「顶部半透明细状态栏（Whisker 菜单 + 居中时钟 + 托盘）+ 底部居中自动隐藏 Dock（ChatGPT、Claude、Chrome、终端、显示设置）」布局，并新增 `~/.config/gtk-3.0/gtk.css`（桌面文字阴影、选中圆角高亮、终端内边距、菜单圆角）。
  - 升级 `desktop/Dockerfile` 与 `desktop/scripts/setup-theme.sh`：补充 `fonts-noto-color-emoji`、`xfce4-whiskermenu-plugin`、`zenity` 及 `sa-display.svg` 自定义图标。
  - 升级 `gateway` 与 `.env.example`：新增 `DESKTOP_DPI`、`DESKTOP_FONT_SIZE`、`DESKTOP_THEME` 可选配置并透传至桌面容器。

### 8. 同步防误删安全护栏 + 本地掉线云端降级续跑与 Agent 小批量约束
- **`9ece4f5`** `feat(sync,exec-bridge,desktop): add mass-deletion guardrail and cloud offline fallback with agent small-batch constraints`
  - 修复 `sync-agent` 云端空目录/批量误删本地文件 Bug，新增 `ErrMassDeleteBlocked` 护栏与服务端基线隔离；上线重连先同步再接管执行；网关返回 `503 worker_offline` / `504 job_timeout`；`sa-local-sh` 支持离线云端降级、实时读取 cgroup 资源配额、动态小批量约束提示与重型任务/低内存软拦截，并新增 `sa-claude-hook`。
- **`506b9de`** `fix(desktop,gateway): seed codex config on boot with dynamic gateway IP and runtime model overrides`
  - 将 Codex 配置迁移至 `init-workspace.sh` 启动时动态生成（自动探测 Docker 网桥网关 IP），支持通过 `.env` 覆盖 `ANTHROPIC_MODEL`、`ANTHROPIC_SMALL_FAST_MODEL`、`CODEX_MODEL`。

### 9. 压榨 Selkies 与云地协同核心能力（空闲冻结 + 高危审批 + 本地端口穿透 + 多项目切换）
- **`feat(gateway,sync-agent,tray,desktop)`** `add idle auto-pause, Selkies quality tuning, local approval workflow, encrypted localhost preview tunnel, and multi-project profiles`
  - **空闲自动冻结（`docker pause`）**：`gateway` 新增 `PauseContainer` 与 `StartIdleReaper` 后台巡检，默认空闲 `30` 分钟自动冻结桌面容器（`DESKTOP_IDLE_PAUSE_MIN=30`，CPU 归零且保留所有窗口会话），再次访问自动秒级 `Unpause` 唤醒。
  - **Selkies 2.0 画质与控制条增强**：默认透传 `SELKIES_USE_PAINT_OVER_QUALITY=true`（静止画面无损补帧）及通用 `SELKIES_EXTRA_ENV`（支持水印、帧率、锁定参数）；Web 桌面顶栏升级为实时 HUD，直接展示本机算力在线/云端降级状态、端口预览通道状态及一键预览本机端口入口。
  - **本机高危命令人工审批流**：`sync-agent` 新增 `ApprovalManager`（覆盖 `git push --force`、`git reset --hard`、`rm -rf`、`DROP/TRUNCATE`、`kubectl delete`、`terraform destroy`、`docker rm` 等二级敏感命令），触发时在托盘应用弹出审批卡片支持一键「允许执行 / 拒绝」，拒绝或超时返回 `exit 126`。
  - **加密本地端口反向穿透（Localhost Preview Tunnel）**：新增 `gateway/internal/tunnel`（`/api/tunnel/*`）与 `sync-agent/internal/syncer/tunnel.go`（复用 AES-256-GCM 端到端加密通道），并内置云端 `sa-preview <local-port> [cloud-port]` 助手，让云端 Chrome/Playwright 直接预览本机运行中的开发服务。
  - **多项目 Workspace Profile 切换**：`sync-agent` 与 `tray-app` 新增常用项目收藏与一键切换（`/local/profiles`）。

---

## 二、配置变更清单（Configuration Changes）

### 1. 网关与编排环境变量（`.env.example` / `docker-compose.yml`）

| 配置项 | 引入/变更提交 | 默认值 / 示例 | 说明与变动 |
| --- | --- | --- | --- |
| `GATEWAY_PORT` | `e092f20` | `8080` | 网关 HTTP/WS 监听端口 |
| `JWT_SECRET` / `JWT_TTL_HOURS` | `e092f20` | 自定义 / `24` | Web 会话 JWT 签名密钥与有效期（小时） |
| `COOKIE_SECURE` | `e092f20` | `false` | HTTPS 反向代理场景下设为 `true` |
| `AUTH_USERS` | `e092f20` | `admin:...,demo:...` | 初始登录账号列表（部署时务必修改） |
| `DOCKER_SOCKET` / `DOCKER_HOST` | `e092f20` | Docker API 入口 | 网关编排桌面容器使用的 Docker 通信地址 |
| `DOCKER_NETWORK` | `e092f20` | `super-agent-net` | 网关与桌面容器共用的 Docker 网络 |
| `DESKTOP_IMAGE` | `e092f20` | `super-agent-desktop:latest` | 云端桌面镜像名称 |
| `DESKTOP_MEMORY_MB` | `e092f20` → `0331b5f` | `4096` → **`6144`** | 集成官方桌面应用后，推荐内存上限提升至 6144 MB |
| `DESKTOP_CPUS` / `DESKTOP_SHM_MB` | `e092f20` | `2` / `2048` | 桌面容器 CPU 核数与 `/dev/shm` 大小 |
| `DESKTOP_TZ` / `DESKTOP_START_TIMEOUT_SEC` | `e092f20` | `Asia/Shanghai` / `60` | 容器时区与启动健康检查超时 |
| `DESKTOP_DPI` / `DESKTOP_FONT_SIZE` / `DESKTOP_THEME` | 桌面样式 v2 | 空（容器默认 `115` / `11` / `dark`） | 可选，预设新建桌面容器的初始缩放 DPI、UI 字号和深浅主题（容器内仍可随时用 `sa-display` 实时修改） |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY`（及 `*_BASE_URL`） | `e092f20` | 空（可选） | 透传至云端桌面容器的 CLI 模型密钥与自定义端点 |
| `MOCK_DESKTOP` | `a589164` | `false` | 无 Docker 本机调试开关 |
| `WORKSPACE_DATA_DIR` | `940f36f` | `/data/workspaces` | 网关侧持久化用户工作区及设备元数据目录 |
| `WORKSPACE_HOST_DIR` | `940f36f` | 空（可选） | 非空时以宿主机 `<WORKSPACE_HOST_DIR>/<user>` 绑定挂载至容器 `/workspace`，替代命名卷 `sa-workspace-<user>` |
| `SYNC_LOCK_TTL_SEC` | `940f36f` | `60` | 单活跃设备锁的租约超时秒数 |

### 2. 桌面容器自动注入与构建参数（`desktop/Dockerfile` & 容器运行时）

| 配置项 | 引入提交 | 作用范围 | 说明 |
| --- | --- | --- | --- |
| `SA_GATEWAY_URL` | `93006a2` | 容器运行时（自动注入） | 网关内部访问地址（`http://super-agent-gateway:<port>`），供 `sa-local-sh` 转发命令 |
| `SA_INTERNAL_TOKEN` | `93006a2` | 容器运行时（自动注入） | 7 天有效期的内部认证令牌，用于容器向网关发起远程执行请求 |
| `SA_LOCAL_EXEC` | `93006a2` | 容器运行时（可选覆盖） | 默认启用透明本地转发；设为 `0` 可临时关闭并在云端直接执行 |
| `SA_EXEC_TIMEOUT_SEC` | `93006a2` | 容器运行时（可选覆盖） | 单次本地命令转发超时时间，默认 `120` 秒 |
| `CLAUDE_DESKTOP_DEB_URL` | `0331b5f` | `docker build --build-arg` | 可选，构建时下载并安装 Claude Linux 官方 `.deb` 包 |
| `CHATGPT_DESKTOP_DEB_URL` | `0331b5f` | `docker build --build-arg` | 可选，构建时下载并安装 ChatGPT Linux 官方 `.deb` 包 |
| `DESKTOP_DPI` / `DESKTOP_FONT_SIZE` / `DESKTOP_THEME` | 桌面样式 v2 | 容器运行时（可选透传） | 控制首次初始化时的 DPI、系统字号与主题，并在 `~/.config/super-agent/display.env` 持久化 |

### 3. 本地 `sync-agent`、托盘应用与网关持久化配置变动

| 变动项 | 演变历程 | 说明 |
| --- | --- | --- |
| 本地状态文件位置 | 项目根目录 `.super-agent-*.json` (`940f36f`) → **`os.UserConfigDir()/super-agent/`** (`7a5e204`) | 迁移至系统用户配置目录（`device.json`、`config.json`、`state/<sha256[:8]>.json`），不再污染用户代码仓库 |
| 本地控制 API 监听地址 | `127.0.0.1:17890` (`7a5e204`) → `localhost:17890` (`3a257e7`) → **双栈 `:17890`** (`62680fd`) | 同时兼容 IPv4/IPv6，并新增 `Access-Control-Allow-Private-Network: true` 与 stdin-EOF 自动退出 |
| 认证凭据字段 | `config.json` 中的明文密码/短期 `token` → **`device_token` (`dev_...`)** (`eef133b`) | 改用配对码换取的可吊销设备令牌；网关侧持久化于 `<WORKSPACE_DATA_DIR>/.super-agent-devices.json` |
| 应用层加密密钥字段 | `c216080` 新增 **`payload_key_b64`** | 同时保存于本地 `config.json` 与网关 `.super-agent-devices.json`（32 字节 Base64 AES-256-GCM 密钥） |
| 加密协商 HTTP 头 | `c216080` 新增 **`X-SA-Enc: aes-256-gcm-v1`** / **`X-SA-Accept-Enc: aes-256-gcm-v1`** | 标识文件同步与远程执行接口的加密载荷格式，支持向前兼容 |

### 4. `.gitignore` 新增忽略项
- 新增忽略：`sync-agent/bin/`、`sync-agent/sync.json`、`tray-app/node_modules/`、`tray-app/src-tauri/target/`、`tray-app/src-tauri/binaries/`。
