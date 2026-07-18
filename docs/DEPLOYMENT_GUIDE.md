# OpenModelPool 部署指南

> **版本**: v3.2.1 | **更新日期**: 2026-07-18

---

## 目录

1. [安装](#1-安装)
   - [Linux](#11-linux)
   - [群晖 NAS](#12-群晖-nas)
   - [Windows](#13-windows)
2. [卸载](#2-卸载)
   - [Linux](#21-linux)
   - [群晖 NAS](#22-群晖-nas)
   - [Windows](#23-windows)
3. [更新/升级](#3-更新升级)
4. [常见问题](#4-常见问题)

---

## 1. 安装

### 1.1 Linux

**一键安装（推荐）：**

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-deploy.sh | sudo bash
```

自定义端口和安装目录：

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-deploy.sh | sudo bash -s -- /opt/openmodelpool 9090
```

安装脚本会自动：
- 检测系统架构（x86_64 / ARM64 / ARMv7）
- 从 GitHub Release 下载对应二进制
- 创建安装目录和数据目录
- 配置 systemd 开机自启服务
- 启动服务

**手动安装：**

```bash
# 下载二进制
wget https://github.com/lisiyu/openmodelpool/releases/download/v3.2.1-release/openmodelpool-linux-amd64.tar.gz
tar xzf openmodelpool-linux-amd64.tar.gz

# 安装
sudo mkdir -p /opt/openmodelpool
sudo cp openmodelpool /opt/openmodelpool/
cd /opt/openmodelpool
sudo mkdir -p data

# 启动
sudo ./openmodelpool &
```

### 1.2 群晖 NAS

群晖 NAS 使用与 Linux 相同的部署脚本，脚本会自动检测群晖 DSM 系统并使用 rc.d 方式配置开机自启。

**一键安装：**

通过 SSH 登录群晖后执行：

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-deploy.sh | sudo bash
```

> **注意**：群晖需在 DSM 控制面板 → 终端机和 SNMP 中启用 SSH 服务。

### 1.3 Windows

**一键安装（推荐）：**

以**管理员身份**打开 PowerShell，执行：

```powershell
irm https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-deploy.ps1 | iex
```

自定义安装目录和端口：

```powershell
& .\omp-deploy.ps1 -InstallDir "D:\openmodelpool" -Port 9090
```

安装脚本会自动：
- 从 GitHub Release 下载 Windows 二进制
- 停止并清理旧版本（NSSM 服务 / 计划任务 / 残留进程）
- 创建安装目录和数据目录
- 配置开机自启（优先使用 NSSM 系统服务，未安装则使用计划任务）
- 启动服务

**手动安装：**

1. 下载 `openmodelpool-windows-amd64.zip`
2. 解压到目标目录（如 `C:\openmodelpool`）
3. 打开 CMD/PowerShell，进入安装目录
4. 运行 `openmodelpool.exe`

---

## 2. 卸载

### 2.1 Linux

```bash
# 1. 停止服务
sudo systemctl stop openmodelpool 2>/dev/null

# 2. 禁用开机自启
sudo systemctl disable openmodelpool 2>/dev/null

# 3. 删除服务文件
sudo rm -f /etc/systemd/system/openmodelpool.service
sudo systemctl daemon-reload

# 4. 杀掉残留进程
sudo pkill -f openmodelpool 2>/dev/null

# 5. 删除安装目录（含数据）
sudo rm -rf /opt/openmodelpool

echo "卸载完成"
```

> **提示**：默认安装目录为 `/opt/openmodelpool`，如自定义了目录请相应修改。

### 2.2 群晖 NAS

```bash
# 1. 停止服务
sudo /usr/local/etc/rc.d/openmodelpool.sh stop 2>/dev/null

# 2. 删除开机自启脚本
sudo rm -f /usr/local/etc/rc.d/openmodelpool.sh

# 3. 杀掉残留进程
sudo pkill -f openmodelpool 2>/dev/null

# 4. 删除安装目录（含数据）
sudo rm -rf /opt/openmodelpool

echo "卸载完成"
```

### 2.3 Windows

以**管理员身份**打开 PowerShell，执行以下命令：

```powershell
# 1. 停止并删除 NSSM 服务（如果有）
$svc = Get-Service -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($svc) {
    nssm stop openmodelpool 2>$null
    nssm remove openmodelpool confirm 2>$null
    Write-Host "NSSM 服务已删除" -ForegroundColor Green
}

# 2. 停止并删除计划任务（如果有）
$task = Get-ScheduledTask -TaskName "OpenModelPool" -ErrorAction SilentlyContinue
if ($task) {
    Stop-ScheduledTask -TaskName "OpenModelPool" 2>$null
    Unregister-ScheduledTask -TaskName "OpenModelPool" -Confirm:$false
    Write-Host "计划任务已删除" -ForegroundColor Green
}

# 3. 杀掉残留进程
Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force
Write-Host "进程已终止" -ForegroundColor Green

# 4. 删除安装目录（含数据）
$installDir = "C:\openmodelpool"
if (Test-Path $installDir) {
    Remove-Item $installDir -Recurse -Force
    Write-Host "安装目录已删除: $installDir" -ForegroundColor Green
}

Write-Host "`n卸载完成！" -ForegroundColor Cyan
```

> **提示**：默认安装目录为 `C:\openmodelpool`，如自定义了目录请相应修改。

---

## 3. 更新/升级

更新流程：**卸载旧版本 → 安装新版本**。

数据目录（`data/`）在卸载时会被删除。如需保留配置和数据，在卸载前备份 `data` 目录：

**Linux / 群晖：**
```bash
cp -r /opt/openmodelpool/data /tmp/omp-data-backup
# 卸载后重新安装，再恢复
cp -r /tmp/omp-data-backup /opt/openmodelpool/data
```

**Windows：**
```powershell
Copy-Item -Recurse "C:\openmodelpool\data" "C:\omp-data-backup"
# 卸载后重新安装，再恢复
Copy-Item -Recurse "C:\omp-data-backup" "C:\openmodelpool\data"
```

> **注意**：从 v3.2.1 开始，HTML 文件已嵌入二进制，不再需要单独的 HTML 文件。升级时只需替换二进制即可。

---

## 4. 常见问题

### Q: 安装后访问页面显示 404？

**A:** v3.2.1 已修复此问题。HTML 文件已嵌入二进制内部，不再依赖外部文件。请确保使用 v3.2.1 或更高版本。如仍有问题，请检查：
- 服务是否正常启动（查看日志）
- 端口是否被占用
- 是否有防火墙拦截

### Q: 端口被占用怎么办？

**Linux:**
```bash
# 查看端口占用
sudo lsof -i :8000
# 杀掉占用进程
sudo kill -9 <PID>
```

**Windows:**
```powershell
# 查看端口占用
Get-NetTCPConnection -LocalPort 8000
# 杀掉占用进程
Stop-Process -Id <PID> -Force
```

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

**方法一**：重新安装时指定端口

**方法二**：修改配置文件 `data/config.json` 中的 `service_port` 字段，然后重启服务。

### Q: 群晖 NAS 安装后无法访问？

**A:** 请检查：
1. DSM 防火墙是否放行了对应端口（控制面板 → 安全性 → 防火墙）
2. DSM 控制面板 → 登录门户 → 高级 → 反向代理，可配置反向代理
3. SSH 登录后执行 `ps aux | grep openmodelpool` 确认进程正在运行

---

## 外网穿透配置

安装完成后，部署脚本会自动询问是否配置外网穿透。也可以随时单独运行配置：

### Windows
```powershell
irm https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-tunnel.ps1 | iex
```

### Linux / 群晖
```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-tunnel.sh | sudo bash
```

### 方案一：Cloudflare Tunnel（推荐）

- **完全免费**，固定域名 + HTTPS
- 需要：一个托管在 Cloudflare 的域名（域名注册约 ¥50/年，Cloudflare DNS 免费使用）
- 注册：https://dash.cloudflare.com/sign-up

配置流程：
1. 选择方案 1（Cloudflare Tunnel）
2. 脚本自动安装 cloudflared
3. 浏览器授权（选择你的域名）
4. 输入子域名（如 `omp.yourdomain.com`）
5. 脚本自动创建隧道、绑定域名、设置开机自启

配置完成后访问：`https://omp.yourdomain.com/admin`

### 方案二：FRP

- **完全免费**，固定 IP + 端口
- 需要：一台有公网 IP 的服务器运行 frps
- 默认使用内置 FRP 服务器（`YOUR_FRP_SERVER_IP`），也可自建

配置流程：
1. 选择方案 2（FRP）
2. 确认 FRP 服务器地址（回车使用默认）
3. 确认认证 Token（回车使用默认）
4. 输入远程映射端口（每个节点用不同端口，如 8001、8002、8003...）
5. 脚本自动安装 frpc、创建配置、设置开机自启

配置完成后访问：`http://YOUR_FRP_SERVER_IP:8001/admin`

### 自建 FRP 服务器

如果你有自己的公网服务器，可以自建 FRP：

```bash
# 在公网服务器上下载 frps
wget https://github.com/fatedier/frp/releases/download/v0.61.1/frp_0.61.1_linux_amd64.tar.gz
tar xzf frp_0.61.1_linux_amd64.tar.gz
cd frp_0.61.1_linux_amd64

# 创建 frps.toml
cat > frps.toml << 'EOF'
bindPort = 7000
auth.token = "your-secret-token"
EOF

# 启动
./frps -c frps.toml
```

然后在节点配置时输入你自己的服务器地址和 Token。
