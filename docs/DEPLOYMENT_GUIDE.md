# OpenModelPool 部署指南

> **版本**: v4.0.5 | **更新日期**: 2026-07-20

---

## 目录

1. [快速开始](#1-快速开始)
2. [安装](#2-安装)
   - [Linux](#21-linux)
   - [群晖 NAS](#22-群晖-nas)
   - [Windows](#23-windows)
   - [macOS](#24-macos)
3. [升级](#3-升级)
4. [自动更新](#4-自动更新)
5. [卸载](#5-卸载)
6. [外网穿透配置](#6-外网穿透配置)
7. [常见问题](#7-常见问题)

---

## 1. 快速开始

OpenModelPool 提供全功能管理脚本，一条命令即可完成安装、升级、卸载、穿透配置等所有操作：

| 平台 | 命令 |
|------|------|
| **Linux / 群晖** | `curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +%s)" \| sudo bash` |
| **Windows** | `irm "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1?t=$(Get-Date -Format 'yyyyMMddHHmmss')" \| iex` |

运行后进入交互菜单：

```
  ╔══════════════════════════════════════════╗
  ║       OpenModelPool 全功能管理工具        ║
  ╚══════════════════════════════════════════╝
    1. 安装          全新安装 OMP
    2. 升级          增量更新 (保留配置)
    3. 卸载          彻底删除所有组件
    4. 配置穿透      Cloudflare / FRP / ngrok
    5. 重置穿透      选择重置任一/全部隧道
    6. 修改端口      更换 OMP 服务端口
    7. 查看状态      检查所有组件运行情况
    8. 重启服务      重启 OMP + 所有隧道
    0. 退出
```

---

## 2. 安装

### 2.1 Linux

**一键安装（推荐）：**

```bash
curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +%s)" | sudo bash
```

选择菜单 **1. 安装**，脚本会自动：
- 检测系统架构（x86_64 / ARM64 / ARMv7）
- 从 GitHub Release 动态获取最新版本并下载对应二进制
- SHA256 校验确保文件完整性
- 创建安装目录（默认 `/opt/openmodelpool`）和数据目录
- 配置 systemd 开机自启服务
- 启动服务

> **区域感知下载（v4.3.28）**：安装脚本与内置自更新（Go 侧 `update.go`）都会先探测 VPS 公网 IP 归属——中国大陆环境**镜像优先、直连兜底**，海外环境**直连优先、镜像兜底**。镜像列表含 `ghfast.top` / `gh-proxy.com` / `ghproxy.net` / `mirror.ghproxy.com` 等，每个源带重试退避与文件大小校验，SHA256 校验同样从多源获取。GitHub 访问不便的地区也能顺利安装/升级。

**自定义端口和安装目录：**

```bash
curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +%s)" | sudo bash -s -- --install-dir /opt/openmodelpool --port 9090
```

**手动安装：**

```bash
# 获取最新 Release 版本号
TAG=$(curl -s https://api.github.com/repos/lisiyu/openmodelpool/releases/latest | python3 -c 'import sys,json;print(json.load(sys.stdin)["tag_name"])')

# 下载二进制 + SHA256 校验
wget "https://github.com/lisiyu/openmodelpool/releases/download/${TAG}/openmodelpool-linux-amd64"
wget "https://github.com/lisiyu/openmodelpool/releases/download/${TAG}/openmodelpool-linux-amd64.sha256"
sha256sum -c openmodelpool-linux-amd64.sha256

# 安装
sudo mkdir -p /opt/openmodelpool/data
sudo cp openmodelpool-linux-amd64 /opt/openmodelpool/openmodelpool
sudo chmod +x /opt/openmodelpool/openmodelpool
cd /opt/openmodelpool
sudo ./openmodelpool &
```

支持的平台二进制：

| 平台 | 文件名 |
|------|--------|
| Linux x86_64 | `openmodelpool-linux-amd64` |
| Linux ARM64 | `openmodelpool-linux-arm64` |
| Linux ARMv7 | `openmodelpool-linux-armv7` |
| Windows x86_64 | `openmodelpool-windows-amd64.exe` |
| macOS Intel | `openmodelpool-darwin-amd64` |
| macOS Apple Silicon | `openmodelpool-darwin-arm64` |

### 2.2 群晖 NAS

管理脚本自动检测群晖 DSM 系统，使用 rc.d 方式配置开机自启，默认安装路径 `/volume1/@appstore/openmodelpool`。

通过 SSH 登录群晖后执行：

```bash
curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +%s)" | sudo bash
```

> **注意**：群晖需在 DSM 控制面板 → 终端机和 SNMP 中启用 SSH 服务。

### 2.3 Windows

**一键安装（推荐）：**

以**管理员身份**打开 PowerShell，执行：

```powershell
irm "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1?t=$(Get-Date -Format 'yyyyMMddHHmmss')" | iex
```

选择菜单 **1. 安装**，脚本会自动：
- 从 GitHub Release 动态获取最新版本并下载 Windows 二进制
- SHA256 校验确保文件完整性
- 停止并清理旧版本（计划任务 / 残留进程）
- 创建安装目录（默认 `C:\openmodelpool`）和数据目录
- 配置计划任务实现开机自启
- 启动服务

**自定义安装目录和端口：**

```powershell
# 先下载脚本
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1" -OutFile omp-manager.ps1
# 指定参数运行
.\omp-manager.ps1 -InstallDir "D:\openmodelpool" -Port 9090
```

**手动安装：**

1. 从 [Releases](https://github.com/lisiyu/openmodelpool/releases/latest) 下载 `openmodelpool-windows-amd64.exe`
2. 放置到目标目录（如 `C:\openmodelpool`），重命名为 `openmodelpool.exe`
3. 创建 `data` 子目录
4. 打开 CMD/PowerShell，进入安装目录，运行 `openmodelpool.exe`

### 2.4 macOS

```bash
# 获取最新 Release 版本号
TAG=$(curl -s https://api.github.com/repos/lisiyu/openmodelpool/releases/latest | python3 -c 'import sys,json;print(json.load(sys.stdin)["tag_name"])')

# Apple Silicon (M1/M2/M3/M4)
curl -fsSL "https://github.com/lisiyu/openmodelpool/releases/download/${TAG}/openmodelpool-darwin-arm64" -o openmodelpool
# Intel Mac
# curl -fsSL "https://github.com/lisiyu/openmodelpool/releases/download/${TAG}/openmodelpool-darwin-amd64" -o openmodelpool

chmod +x openmodelpool
mkdir -p data
./openmodelpool &
```

---

## 3. 升级

使用管理脚本的 **增量更新** 功能，保留所有配置和数据，仅替换二进制文件。

**Linux / 群晖：**

```bash
curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +%s)" | sudo bash
# 选择菜单 2. 升级
```

**Windows：**

```powershell
irm "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1?t=$(Get-Date -Format 'yyyyMMddHHmmss')" | iex
# 选择菜单 2. 升级
```

升级流程：
1. 动态获取 GitHub 最新 Release 版本
2. 下载对应平台二进制 + SHA256 校验
3. 备份当前二进制（`.bak`）
4. 停止服务 → 替换二进制 → 启动服务
5. 启动失败自动回滚

> **注意**：HTML 文件已嵌入二进制，升级只需替换二进制文件，无需处理其他文件。

---

## 4. 自动更新

通过 cron（Linux）或计划任务（Windows）实现无人值守自动更新。

### Linux

```bash
# 编辑 crontab
sudo crontab -e

# 添加每天凌晨 4 点自动检查更新
0 4 * * * curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +\%s)" | bash -s -- --auto-update >> /tmp/omp-auto-update.log 2>&1
```

### Windows

```powershell
# 创建计划任务（管理员 PowerShell）
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-ExecutionPolicy Bypass -Command `"irm 'https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1' | iex -- -AutoUpdate`""
$trigger = New-ScheduledTaskTrigger -Daily -At "04:00"
Register-ScheduledTask -TaskName "OpenModelPool-AutoUpdate" -Action $action -Trigger $trigger -RunLevel Highest
```

自动更新日志位置：
- Linux: `/tmp/omp-auto-update.log`
- Windows: `C:\openmodelpool\data\auto-update.log`

---

## 5. 卸载

使用管理脚本的 **卸载** 功能，彻底删除所有组件：

**Linux / 群晖：**

```bash
curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +%s)" | sudo bash
# 选择菜单 3. 卸载
```

**Windows：**

```powershell
irm "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1?t=$(Get-Date -Format 'yyyyMMddHHmmss')" | iex
# 选择菜单 3. 卸载
```

卸载会自动清理：
- OMP 二进制和安装目录
- systemd 服务 / 计划任务 / rc.d 脚本
- cloudflared / frpc / ngrok 相关配置和进程
- 所有隧道服务

**手动卸载（Linux）：**

```bash
sudo systemctl stop openmodelpool 2>/dev/null
sudo systemctl disable openmodelpool 2>/dev/null
sudo rm -f /etc/systemd/system/openmodelpool.service
sudo systemctl daemon-reload
sudo pkill -f openmodelpool 2>/dev/null
sudo rm -rf /opt/openmodelpool
```

**手动卸载（Windows）：**

```powershell
Stop-ScheduledTask -TaskName "OpenModelPool" -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName "OpenModelPool" -Confirm:$false -ErrorAction SilentlyContinue
Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force
Remove-Item "C:\openmodelpool" -Recurse -Force
```

---

## 6. 外网穿透配置

安装完成后，使用管理脚本的 **配置穿透** 功能：

**Linux / 群晖：**

```bash
curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +%s)" | sudo bash
# 选择菜单 4. 配置穿透
```

**Windows：**

```powershell
irm "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1?t=$(Get-Date -Format 'yyyyMMddHHmmss')" | iex
# 选择菜单 4. 配置穿透
```

支持三种穿透方案，可同时配置多个：

### 方案一：Cloudflare Tunnel（推荐）

- **完全免费**，固定域名 + HTTPS
- 需要：一个托管在 Cloudflare 的域名
- 注册：https://dash.cloudflare.com/sign-up

配置流程：
1. 选择 Cloudflare Tunnel
2. 脚本自动安装 cloudflared
3. 浏览器授权（选择你的域名）
4. 输入子域名（如 `omp.yourdomain.com`）
5. 脚本自动创建隧道、绑定域名、设置开机自启

配置完成后访问：`https://omp.yourdomain.com/admin`

> **提示**：配置完成后域名信息会自动同步到 OMP 的 `config.json`，管理面板不再提示绑定域名。

### 方案二：FRP

- **完全免费**，固定 IP + 端口
- 需要：一台有公网 IP 的服务器运行 frps

#### 第一步：在公网服务器上搭建 FRP 服务端

```bash
# 下载 FRP
wget https://github.com/fatedier/frp/releases/download/v0.61.1/frp_0.61.1_linux_amd64.tar.gz
tar xzf frp_0.61.1_linux_amd64.tar.gz
cd frp_0.61.1_linux_amd64

# 创建服务端配置
cat > frps.toml << 'EOF'
bindPort = 7000
auth.token = "your-secret-token-here"
EOF

# 启动
./frps -c frps.toml

# 设置开机自启 (systemd)
sudo tee /etc/systemd/system/frps.service << EOF
[Unit]
Description=frps server
After=network.target
[Service]
Type=simple
ExecStart=$(pwd)/frps -c $(pwd)/frps.toml
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl enable frps && sudo systemctl start frps
```

> **重要：** 在云服务器控制台的安全组中放行端口：TCP 7000 + 你要映射的端口范围（如 8001-8010）

#### 第二步：在本地节点上配置 FRP 客户端

运行管理脚本，选择 4. 配置穿透 → FRP，按提示输入：
1. FRP 服务器公网 IP
2. FRP 认证 Token
3. 远程映射端口（每个节点用不同端口，如 8001、8002、8003...）

配置完成后访问：`http://你的公网IP:8001/admin`

### 方案三：ngrok

- 免费版可用，固定域名需付费
- 注册：https://dashboard.ngrok.com/signup

配置流程：
1. 选择 ngrok
2. 输入 ngrok authtoken
3. 可选：输入固定域名（付费功能）
4. 脚本自动安装配置并设置开机自启

配置完成后访问脚本输出的 ngrok 地址。

### 重置穿透

如需重新配置或清除穿透，选择菜单 **5. 重置穿透**，可以：
- 重置 Cloudflare Tunnel
- 重置 FRP
- 重置 ngrok
- 重置全部

---

## 7. 常见问题

### Q: 安装后访问页面显示 404？

**A:** 从 v3.2.1 开始，HTML 文件已嵌入二进制，不再依赖外部文件。请检查：
- 服务是否正常启动（管理脚本菜单 7. 查看状态）
- 端口是否被占用
- 是否有防火墙拦截

### Q: 更新后前端页面没有变化？

**A:** 这是浏览器缓存问题。虽然服务端已设置 `Cache-Control: no-cache`，但部分浏览器仍可能使用缓存的 JS 文件。请**强制刷新**：`Ctrl+Shift+R`（Windows）或 `Cmd+Shift+R`（macOS）。

### Q: 局域网 IP 显示 169.254.x.x？

**A:** 早期版本（v4.0.3 仅修复了部分路径）在 Windows 上仍可能显示 `169.254.x.x`（链路本地 / APIPA 地址）。该问题已在 **v4.1.2** 彻底修复：`handlers.go` 的 `getLocalIP()` 现在优先返回 RFC1918 私网地址（10/8、172.16/12、192.168/16），并过滤 loopback / APIPA(169.254/16) / IPv6 链路本地 / 组播等无效地址；`tunnel.go` 同步复用同一逻辑。请升级到 **v4.1.2 或更高版本**；若仍显示错误地址，请确认至少有一张网卡已获取到有效私网 IP。

### Q: 端口被占用怎么办？

**Linux:**
```bash
sudo lsof -i :8000
sudo kill -9 <PID>
```

**Windows:**
```powershell
Get-NetTCPConnection -LocalPort 8000
Stop-Process -Id <PID> -Force
```

或者直接使用管理脚本菜单 **6. 修改端口** 更换端口。

### Q: 如何查看日志？

**Linux:**
```bash
# systemd 日志
journalctl -u openmodelpool -f
# 或应用日志
tail -f /opt/openmodelpool/data/app.log
```

**Windows:**
```powershell
Get-Content C:\openmodelpool\data\app.log -Tail 50 -Wait
```

### Q: 如何修改端口？

使用管理脚本菜单 **6. 修改端口**，会自动更新：
- OMP 启动配置
- Cloudflare Tunnel 配置
- FRP 配置
- ngrok 配置
- 重启所有服务

### Q: 群晖 NAS 安装后无法访问？

**A:** 请检查：
1. DSM 防火墙是否放行了对应端口（控制面板 → 安全性 → 防火墙）
2. SSH 登录后执行 `ps aux | grep openmodelpool` 确认进程正在运行
3. 使用管理脚本菜单 **7. 查看状态** 检查各组件运行情况

### Q: 使用内置浏览器登录网页平台失败（chrome failed to start / Unexpected end of JSON input）？

**A:** 内置浏览器依赖本机已安装的 Chrome / Chromium 来抓取登录态，需满足：
1. 已在目标机上安装 Chrome 或 Chromium（桌面环境通常自带；Linux 服务器/容器需 `apt install chromium` 或设 `OMP_CHROME_PATH` 环境变量指向可执行文件）。
2. v4.1.5 起已修复 Chromium 139+ 的 SwiftShader 渲染问题并补充 `--no-sandbox`（Linux）、统一临时用户目录、`--enable-unsafe-swiftshader`，以及后端 panic 兜底返回合法 JSON。
3. 找不到 Chrome 时前端会显示明确错误提示，而非空白/JSON 解析失败。

### Q: CI/CD 自动编译和发布？

**A:** 项目使用 GitHub Actions 自动化发布：
- 推送代码到 `main` 分支 → 触发 CI（go vet + 测试 + 交叉编译 + 安全扫描）
- 创建 `v*` 标签 → 触发 Release workflow（6 平台编译 + SHA256 + 自动创建 GitHub Release）
- 详细文档：[CI/CD 流程](openmodelpool_ci_cd_workflow.md)
