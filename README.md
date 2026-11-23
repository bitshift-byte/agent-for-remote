# Super-Agent — Phase 1 MVP (GUI 云桌面)

基于 V2.0 双模架构的 **Phase 1 MVP** 实现：在海外云服务器上部署定制 Ubuntu XFCE + KasmVNC 桌面镜像与 Go 控制面/反向代理网关，用户无需安装任何本地客户端，打开浏览器登录即可进入独立隔离的云端桌面，直接使用 **Claude Code**、**OpenAI Codex**、**Chrome** 与 **中文输入法（fcitx5）**。

## 目录结构

```text
super-agent/
├── .env.example                       # 环境变量模板（JWT 密钥、账号、容器配额、API Key）
├── desktop/
│   ├── Dockerfile                     # 定制 Ubuntu XFCE + KasmVNC + Node 22 + Python 3 + Chrome + fcitx5 + Claude Code + Codex
│   └── scripts/
│       └── init-workspace.sh          # s6 启动脚本：初始化 /workspace、fcitx5 自启与桌面快捷方式
└── gateway/
    ├── Dockerfile                     # Go 1.22 多阶段构建（零外部依赖，纯标准库）
    ├── go.mod
    ├── cmd/gateway/main.go            # 网关入口与优雅退出
    └── internal/
        ├── auth/                      # HS256 JWT 签发与校验（HttpOnly Cookie / Bearer）
        ├── config/                    # 环境变量配置加载
        ├── docker/                    # 原生 Docker Engine REST 客户端与单用户容器生命周期管理
        ├── proxy/                     # KasmVNC HTTP + WebSocket 反向代理
        └── server/
            ├── server.go              # API 路由、鉴权中间件与 /desktop/ 代理入口
            └── web/index.html         # 内嵌极简 Web 登录与云桌面控制台（含唤醒加载过渡）
```

## 核心设计要点

- **基础云桌面镜像**：基于 [LinuxServer.io Webtop (`lscr.io/linuxserver/webtop:ubuntu-xfce`)](https://docs.linuxserver.io/images/docker-webtop/)，默认内网端口 `3000`，通过 `SUBFOLDER=/desktop/` 原生挂载到网关子路径下，避免复杂的路径重写。
- **预装 AI CLI 工具**：全局预装 Claude Code（`@anthropic-ai/claude-code`，参见 [Claude Code 安装文档](https://claudecodeguide.dev/docs/foundations/installation)）与 OpenAI Codex CLI（`@openai/codex`，参见 [NexLev Quickstart](https://docs.nexlev.io/docs/mcp/quickstart)），桌面自带一键启动快捷方式。
- **数据与配置卷分离**：每个用户自动分配两个独立持久化卷，避免冷休眠/重启后登录态丢失，也为 Phase 2 的代码同步做好隔离：
  - `sa-workspace-<user>` → `/workspace`（项目代码目录）
  - `sa-config-<user>` → `/config`（浏览器 Cookie、~/.claude、~/.codex、XFCE 与输入法配置）
- **安全与隔离**：
  - 网关统一校验 `HttpOnly` JWT Cookie，底层 KasmVNC 端口（`3000`）仅暴露在内部 Docker 网络，不直接暴露公网。
  - 每个容器通过 cgroups 限制 CPU（默认 2 核）、内存（默认 4GB）与 `/dev/shm`（默认 2GB），不开启 `--privileged`。
  - `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` 等密钥通过容器运行时环境变量动态注入，不落盘到 `/workspace`。

## 快速部署（本地或海外 Linux 服务器）

### 1. 准备环境变量

```bash
cp .env.example .env
# 务必修改 JWT_SECRET 与 AUTH_USERS
openssl rand -hex 32
```

### 2. 构建定制桌面镜像与 Go 网关镜像

```bash
# 创建共享桥接网络
docker network create super-agent-net || true

# 1) 构建自定义 Ubuntu + KasmVNC + Claude Code / Codex 桌面镜像（首次构建约 3-6 分钟）
docker build -t super-agent-desktop:latest ./desktop

# 2) 构建 Go 网关镜像（纯标准库，无需外网拉取 Go 依赖）
docker build -t super-agent-gateway:latest ./gateway
```

### 3. 启动网关服务

```bash
docker run -d \
  --name super-agent-gateway \
  --restart unless-stopped \
  --network super-agent-net \
  -p 8080:8080 \
  --env-file .env \
  -v /var/run/docker.sock:/var/run/docker.sock \
  super-agent-gateway:latest
```

> 安全建议：生产环境可用 `docker-socket-proxy` 替代直接挂载 Docker socket，并将 `.env` 中的 `DOCKER_HOST` 指向代理地址（如 `tcp://docker-proxy:2375`）。

### 4. 访问使用

1. 浏览器打开 `http://<服务器IP>:8080`
2. 使用 `.env` 中 `AUTH_USERS` 配置的账号登录（默认 `admin` / `admin123`）
3. 点击 **「启动并进入云桌面」**，等待 10–25 秒容器就绪后自动进入 Ubuntu 桌面
4. 双击桌面图标即可启动 **Claude Code**、**OpenAI Codex**、**Chrome Browser** 或 **Workspace Terminal**
5. 中文输入法：按 `Ctrl + Space` 切换 fcitx5 拼音输入法

## 公网 HTTPS 部署建议（Caddy）

在海外服务器上线时，建议在网关前加一层 Caddy 自动签发 TLS 1.3 证书，并将 `.env` 中的 `COOKIE_SECURE` 设为 `true`：

```caddyfile
desktop.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

## 常用开发与验证命令

```bash
# 运行 Go 单元测试（需要本机安装 Go 1.22+）
cd gateway && go test ./...

# 查看网关日志
docker logs -f super-agent-gateway

# 查看已创建的用户桌面容器
docker ps -a --filter "label=super-agent.managed=true"
```
