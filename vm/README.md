# Super-Agent KVM 虚拟机运行时部署指南（Ubuntu 24.04 + Incus）

当你的宿主机支持硬件虚拟化（`/dev/kvm` 存在）时，可以将 Super-Agent 的底层运行时从 `DESKTOP_RUNTIME=docker` 一键切换为 **`DESKTOP_RUNTIME=incus`**，为每个用户分配**完整独立 Linux 内核、独立虚拟硬件/MAC、原生 `virtiofs` 共享 `/workspace`** 的 KVM 虚拟机，同时 100% 复用现有 Go 网关、AES-256 双向同步、本机命令执行、托盘审批与端口预览。

## 1. 初始化 Ubuntu 24.04 宿主机

```bash
cd /path/to/super-agent
chmod +x vm/setup-incus-host.sh vm/build-base-vm.sh
./vm/setup-incus-host.sh
```

脚本会安装 `incus`、`qemu-system-x86`、`ovmf`、`virtiofsd`，初始化默认网桥 `incusbr0` 并打印网桥网关 IP（例如 `10.x.x.1`）。

## 2. 构建 Ubuntu 24.04 KVM 黄金镜像

```bash
./vm/build-base-vm.sh
```

构建完成后会在本机 Incus 中生成名为 `super-agent-vm-base` 的 KVM 黄金镜像，内置 XFCE、Chrome、Node 22、Claude Code CLI、Codex CLI 及全部 `sa-*` 工具脚本（并在开机时通过 `/dev/incus/sock` 自动同步网关地址、令牌、时区与独立出口代理）。

## 3. 配置 `.env` 并启动网关

在 `.env` 中切换运行时（网关建议直接在宿主机运行，或将 `/var/lib/incus/unix.socket` 挂入网关容器并开放宿主机网桥访问网关端口）：

```env
DESKTOP_RUNTIME=incus
INCUS_SOCKET=/var/lib/incus/unix.socket
INCUS_VM_IMAGE=super-agent-vm-base
INCUS_STORAGE_POOL=default
INCUS_NETWORK=incusbr0

# 虚拟机内部回连网关的地址（填步骤 1 打印的 incusbr0 网桥 IP）
VM_GATEWAY_URL=http://10.x.x.1:8080

# 宿主机共享工作区目录（通过 virtiofs 直接挂载到 VM 的 /workspace）
WORKSPACE_DATA_DIR=/data/workspaces
WORKSPACE_HOST_DIR=/data/workspaces

# 可选：按用户配置独立网络出口代理（逗号分隔 user=url，* 为默认）
USER_EGRESS_PROXIES=admin=http://egress-admin:8888

# 与出口地区对齐的时区
DESKTOP_TZ=America/Los_Angeles
DESKTOP_IDLE_PAUSE_MIN=30
```

启动网关后，在 Web 控制台点「启动并进入云桌面」，网关会自动从 `super-agent-vm-base` 克隆并启动该用户的独立 KVM 虚拟机（`sa-vm-<user>`），空闲超过 `DESKTOP_IDLE_PAUSE_MIN` 分钟后自动 `freeze` 冻结，再次访问秒级 `unfreeze` 唤醒。
