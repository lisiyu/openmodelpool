# ============================================================
#  OpenModelPool 全功能管理脚本 (Windows)
#  集成：安装 / 升级 / 卸载 / 穿透配置(CF/FRP/ngrok) / 端口修改 / 状态查看 / 重启
#
#  用法 (管理员 PowerShell):
#    irm https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1 | iex
# ============================================================
param(
    [string]$InstallDir = "C:\openmodelpool",
    [int]$Port = 8000
)

$ErrorActionPreference = "Continue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$C = "Cyan"; $Y = "Yellow"; $G = "Green"; $R = "Red"; $W = "White"

# 常量 - OMP
$GITHUB_REPO = "lisiyu/openmodelpool"
$RELEASE_TAG = "v3.2.1-release"
$PKG = "openmodelpool-windows-amd64.zip"
$DOWNLOAD_URL = "https://github.com/$GITHUB_REPO/releases/download/$RELEASE_TAG/$PKG"
$exeName = "openmodelpool.exe"
$exePath = Join-Path $InstallDir $exeName
$dataDir = Join-Path $InstallDir "data"
$logFile = Join-Path $dataDir "app.log"
$ompTaskName = "OpenModelPool"

# 常量 - Cloudflare Tunnel
$cfDir = "$env:ProgramFiles\cloudflared"
$cfExe = "$cfDir\cloudflared.exe"
$cfConfigDir = "$env:USERPROFILE\.cloudflared"
$cfCertFile = "$cfConfigDir\cert.pem"
$cfTaskName = "CloudflaredTunnel"

# 常量 - FRP
$frpDir = Join-Path $InstallDir "frp"
$frpExe = "$frpDir\frpc.exe"
$frpConfig = "$frpDir\frpc.toml"
$frpTaskName = "OpenModelPoolFRP"

# 常量 - ngrok
$ngrokDir = "$env:ProgramFiles\ngrok"
$ngrokExe = "$ngrokDir\ngrok.exe"
$ngrokTaskName = "OpenModelPoolNgrok"

# ============================================================
# 工具函数
# ============================================================
function Write-Title($text) {
    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor $C
    Write-Host "   $text" -ForegroundColor $C
    Write-Host "  ============================================" -ForegroundColor $C
}

function Write-Step($num, $total, $text) {
    Write-Host "[$num/$total] $text" -ForegroundColor $Y
}

function Write-OK($text) { Write-Host "  OK  $text" -ForegroundColor $G }
function Write-Err($text) { Write-Host "  X   $text" -ForegroundColor $R }
function Write-Info($text) { Write-Host "  $text" -ForegroundColor DarkGray }

function Test-Admin {
    return ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# --- OMP 控制 ---
function Stop-OMP {
    Stop-ScheduledTask -TaskName $ompTaskName -ErrorAction SilentlyContinue
    Stop-Process -Name "openmodelpool" -Force -ErrorAction SilentlyContinue
    $conns = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue
    if ($conns) {
        $conns | ForEach-Object { Stop-Process -Id $_.OwningProcess -Force -ErrorAction SilentlyContinue }
        Start-Sleep -Seconds 2
    }
}

function Start-OMP {
    $startBat = Join-Path $InstallDir "start.bat"
    if (Test-Path $startBat) {
        Start-Process -FilePath "cmd.exe" -ArgumentList "/c", $startBat -WindowStyle Hidden
    } else {
        Start-Process -FilePath $exePath -WorkingDirectory $InstallDir -WindowStyle Hidden
    }
    Start-Sleep -Seconds 3
}

# --- Cloudflare Tunnel 控制 ---
function Stop-Cloudflared {
    Stop-ScheduledTask -TaskName $cfTaskName -ErrorAction SilentlyContinue
    Stop-Process -Name "cloudflared" -Force -ErrorAction SilentlyContinue
    try { Stop-Service cloudflared -Force -ErrorAction SilentlyContinue } catch {}
    Start-Sleep -Seconds 1
}

function Remove-CloudflaredService {
    try { Stop-Service cloudflared -Force -ErrorAction SilentlyContinue } catch {}
    Start-Sleep -Seconds 1
    sc.exe delete cloudflared 2>&1 | Out-Null
    Start-Sleep -Seconds 1
    try { sc.exe delete Cloudflared 2>&1 | Out-Null } catch {}
}

# --- FRP 控制 ---
function Stop-FRP {
    Stop-ScheduledTask -TaskName $frpTaskName -ErrorAction SilentlyContinue
    Stop-Process -Name "frpc" -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
}

# --- ngrok 控制 ---
function Stop-Ngrok {
    Stop-ScheduledTask -TaskName $ngrokTaskName -ErrorAction SilentlyContinue
    Stop-Process -Name "ngrok" -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
}

# --- 停止所有隧道 ---
function Stop-All-Tunnels {
    Stop-Cloudflared
    Stop-FRP
    Stop-Ngrok
}

# ============================================================
# 1. 安装
# ============================================================
function Install-OMP {
    Write-Title "OpenModelPool 全新安装"

    if (Test-Path $exePath) {
        Write-Host "  检测到已有安装: $InstallDir" -ForegroundColor $Y
        $confirm = Read-Host "  是否覆盖安装？(y/n)"
        if ($confirm -ne "y") { Write-Host "  已取消" -ForegroundColor $Y; return }
    }

    Write-Step 0 5 "清理旧版本..."
    Stop-OMP
    Stop-All-Tunnels
    Write-OK "清理完成"

    Write-Step 1 5 "下载 $PKG ..."
    $tmpZip = Join-Path $env:TEMP "omp-install.zip"
    try {
        Invoke-WebRequest -Uri $DOWNLOAD_URL -OutFile $tmpZip -UseBasicParsing
        Write-OK "下载完成"
    } catch {
        Write-Err "下载失败: $_"
        return
    }

    Write-Step 2 5 "解压..."
    $tmpDir = Join-Path $env:TEMP "omp-extract"
    if (Test-Path $tmpDir) { Remove-Item $tmpDir -Recurse -Force }
    Expand-Archive -Path $tmpZip -DestinationPath $tmpDir -Force
    Write-OK "解压完成"

    Write-Step 3 5 "安装到 $InstallDir ..."
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
    Copy-Item (Join-Path $tmpDir $exeName) -Destination $exePath -Force
    if (Test-Path (Join-Path $tmpDir "docs")) {
        Copy-Item (Join-Path $tmpDir "docs") -Destination $InstallDir -Force -Recurse
    }
    Write-OK "文件安装完成"

    # 安装 Xray (VMess 代理支持)
    $xrayDir = Join-Path $InstallDir "xray"
    New-Item -ItemType Directory -Force -Path $xrayDir | Out-Null
    $xrayUrl = "https://github.com/XTLS/Xray-core/releases/download/$XRAY_VERSION/Xray-windows-64.zip"
    Write-Host "  下载 Xray (VMess 代理)..." -ForegroundColor $C
    try {
        $xrayTmp = Join-Path $env:TEMP "xray-install.zip"
        Invoke-WebRequest -Uri $xrayUrl -OutFile $xrayTmp -UseBasicParsing
        $xrayExtract = Join-Path $env:TEMP "xray-install-extract"
        if (Test-Path $xrayExtract) { Remove-Item $xrayExtract -Recurse -Force }
        Expand-Archive -Path $xrayTmp -DestinationPath $xrayExtract -Force
        Copy-Item (Join-Path $xrayExtract "xray.exe") -Destination (Join-Path $xrayDir "xray.exe") -Force
        Copy-Item (Join-Path $xrayExtract "geoip.dat") -Destination $xrayDir -Force -ErrorAction SilentlyContinue
        Copy-Item (Join-Path $xrayExtract "geosite.dat") -Destination $xrayDir -Force -ErrorAction SilentlyContinue
        Remove-Item $xrayTmp -Force -ErrorAction SilentlyContinue
        Write-OK "Xray 安装完成"
    } catch {
        Write-Host "  Xray 下载失败，VMess 代理不可用（不影响其他功能）" -ForegroundColor $Y
    }

    $startBat = Join-Path $InstallDir "start.bat"
    @"
@echo off
cd /d "$InstallDir"
set PORT=$Port
$exeName >> "$logFile" 2>&1
"@ | Set-Content $startBat -Encoding ASCII

    $stopBat = Join-Path $InstallDir "stop.bat"
    @"
@echo off
taskkill /f /im $exeName 2>nul
echo stopped
"@ | Set-Content $stopBat -Encoding ASCII

    Write-Step 4 5 "配置服务 (端口 $Port)..."
    $action = New-ScheduledTaskAction -Execute $exePath -WorkingDirectory $InstallDir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
    Register-ScheduledTask -TaskName $ompTaskName -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
    Write-OK "计划任务已创建"

    Write-Step 5 5 "启动..."
    Start-OMP

    $proc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
    if ($proc) {
        $ip = (Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -notmatch "Loopback" -and $_.IPAddress -notmatch "^169\.254" } | Select-Object -First 1).IPAddress
        Write-Host ""
        Write-Host "  ============================================" -ForegroundColor $G
        Write-Host "           安装成功！" -ForegroundColor $G
        Write-Host "  ============================================" -ForegroundColor $G
        Write-Host "  管理面板:  http://${ip}:$Port/admin" -ForegroundColor $C
        Write-Host "  安装目录:  $InstallDir"
        Write-Host "  日志:      $logFile"
        Write-Host ""
    } else {
        Write-Err "启动失败，查看日志: Get-Content '$logFile' -Tail 50"
    }

    Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
    Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue

    # 询问穿透
    Write-Host ""
    Write-Host "  是否配置外网穿透？" -ForegroundColor $C
    Setup-Tunnel-Menu
}

# ============================================================
# 2. 升级
# ============================================================
function Upgrade-OMP {
    Write-Title "OpenModelPool 增量升级"

    if (-not (Test-Path $exePath)) {
        Write-Err "未检测到安装: $exePath"
        return
    }

    $oldVersion = (Get-Item $exePath).LastWriteTime.ToString("yyyy-MM-dd HH:mm")
    Write-Info "当前版本时间: $oldVersion"

    Write-Step 1 4 "下载最新版本..."
    $tmpZip = Join-Path $env:TEMP "omp-upgrade.zip"
    try {
        Invoke-WebRequest -Uri $DOWNLOAD_URL -OutFile $tmpZip -UseBasicParsing
        Write-OK "下载完成"
    } catch {
        Write-Err "下载失败: $_"
        return
    }

    Write-Step 2 4 "停止服务..."
    Stop-OMP
    Write-OK "已停止"

    Write-Step 3 4 "替换二进制文件..."
    $tmpDir = Join-Path $env:TEMP "omp-upgrade-extract"
    if (Test-Path $tmpDir) { Remove-Item $tmpDir -Recurse -Force }
    Expand-Archive -Path $tmpZip -DestinationPath $tmpDir -Force
    Copy-Item (Join-Path $tmpDir $exeName) -Destination $exePath -Force
    Write-OK "替换完成"

    Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
    Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue

    Write-Step 4 4 "重启服务..."
    Start-OMP
    $proc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
    if ($proc) {
        Write-OK "升级完成 (PID: $($proc.Id))"
        Write-Host "  管理面板: http://localhost:$Port/admin" -ForegroundColor $C
    } else {
        Write-Err "启动失败"
    }
    Write-Host ""
}

# ============================================================
# 3. 卸载
# ============================================================
function Uninstall-OMP {
    Write-Title "彻底卸载 OpenModelPool"

    $confirm = Read-Host "  确认卸载？将删除所有组件和配置 (输入 yes 确认)"
    if ($confirm -ne "yes") { Write-Host "  已取消" -ForegroundColor $Y; return }

    Write-Step 1 6 "停止所有服务..."
    Stop-OMP
    Stop-All-Tunnels
    Write-OK "已停止"

    Write-Step 2 6 "删除 OMP 计划任务..."
    Unregister-ScheduledTask -TaskName $ompTaskName -Confirm:$false -ErrorAction SilentlyContinue
    Write-OK "已删除"

    Write-Step 3 6 "删除隧道计划任务..."
    Unregister-ScheduledTask -TaskName $cfTaskName -Confirm:$false -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $frpTaskName -Confirm:$false -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $ngrokTaskName -Confirm:$false -ErrorAction SilentlyContinue
    Remove-CloudflaredService
    Write-OK "已删除"

    Write-Step 4 6 "删除 Cloudflare Tunnel 配置..."
    if (Test-Path $cfExe) {
        & $cfExe tunnel delete openmodelpool 2>&1 | Out-Null
    }
    if (Test-Path $cfConfigDir) { Remove-Item $cfConfigDir -Recurse -Force -ErrorAction SilentlyContinue }
    if (Test-Path $cfDir) { Remove-Item $cfDir -Recurse -Force -ErrorAction SilentlyContinue }
    Write-OK "已删除"

    Write-Step 5 6 "删除 FRP / ngrok 配置..."
    if (Test-Path $frpDir) { Remove-Item $frpDir -Recurse -Force -ErrorAction SilentlyContinue }
    if (Test-Path $ngrokDir) { Remove-Item $ngrokDir -Recurse -Force -ErrorAction SilentlyContinue }
    Write-OK "已删除"

    Write-Step 6 6 "删除 OMP 安装目录..."
    if (Test-Path $InstallDir) { Remove-Item $InstallDir -Recurse -Force -ErrorAction SilentlyContinue }
    Write-OK "已删除"

    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor $G
    Write-Host "   卸载完成！" -ForegroundColor $G
    Write-Host "  ============================================" -ForegroundColor $G
    Write-Host ""
}

# ============================================================
# 4. 配置穿透 (子菜单)
# ============================================================
function Setup-Tunnel-Menu {
    Write-Host ""
    Write-Host "  请选择穿透方案：" -ForegroundColor $C
    Write-Host "    1) Cloudflare Tunnel  完全免费，固定域名+HTTPS，需自有域名" -ForegroundColor $G
    Write-Host "    2) FRP               免费，固定IP+端口，需公网服务器" -ForegroundColor $G
    Write-Host "    3) ngrok             免费/付费，自动HTTPS，注册即用" -ForegroundColor $G
    Write-Host "    4) 跳过"
    $tunnelChoice = Read-Host "  请选择 [1/2/3/4]"
    switch ($tunnelChoice) {
        "1" { Setup-Cloudflare }
        "2" { Setup-FRP }
        "3" { Setup-Ngrok }
        "4" { Write-Host "  跳过穿透配置" -ForegroundColor $Y }
        default { Write-Host "  无效选项" -ForegroundColor $R }
    }
}

# ============================================================
# 4a. Cloudflare Tunnel
# ============================================================
function Setup-Cloudflare {
    Write-Title "配置 Cloudflare Tunnel"
    Write-Host ""
    Write-Host "  Cloudflare Tunnel 原理：在本机和 Cloudflare 边缘网络之间建立" -ForegroundColor $C
    Write-Host "  加密隧道，外网通过 Cloudflare 域名访问，自动 HTTPS，无需公网 IP。" -ForegroundColor $C
    Write-Host ""
    Write-Host "  优点：完全免费 | 固定域名 | 自动 HTTPS | 无流量限制" -ForegroundColor $G
    Write-Host "  缺点：需要一个托管在 Cloudflare 的域名" -ForegroundColor $Y
    Write-Host ""
    Write-Host "  前置条件：" -ForegroundColor $Y
    Write-Host "    1. 注册 Cloudflare 账号 (免费): https://dash.cloudflare.com/sign-up" -ForegroundColor $C
    Write-Host "    2. 拥有一个域名 (可在任意注册商购买)" -ForegroundColor $W
    Write-Host "    3. 将域名的 DNS 服务器改为 Cloudflare 分配的 NS" -ForegroundColor $W
    Write-Host "       (在 Cloudflare 控制台添加域名后，按提示修改 NS)" -ForegroundColor DarkGray
    Write-Host "    4. 等待 NS 生效 (通常几分钟到数小时)" -ForegroundColor $W
    Write-Host ""
    Write-Host "  注意：子域名不能与已有 DNS 记录冲突" -ForegroundColor $Y
    Write-Host "    正确: omp.yourdomain.com, api.yourdomain.com" -ForegroundColor $G
    Write-Host "    错误: yourdomain.com (根域名通常已有记录)" -ForegroundColor $R
    Write-Host ""

    $ready = Read-Host "  域名已托管到 Cloudflare？(y/n)"
    if ($ready -ne "y") {
        Write-Host "  请先完成域名托管，再来配置隧道。" -ForegroundColor $Y
        Write-Host "  参考: https://developers.cloudflare.com/fundamentals/get-started/setup/add-site/" -ForegroundColor $C
        return
    }

    Write-Step 1 5 "安装 cloudflared..."
    if (-not (Test-Path $cfExe)) {
        New-Item -ItemType Directory -Path $cfDir -Force | Out-Null
        $cfUrl = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"
        Invoke-WebRequest -Uri $cfUrl -OutFile $cfExe -UseBasicParsing
        $currentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
        if ($currentPath -notlike "*$cfDir*") {
            [Environment]::SetEnvironmentVariable("Path", "$currentPath;$cfDir", "Machine")
            $env:Path += ";$cfDir"
        }
        Write-OK "安装完成"
    } else {
        Write-OK "已安装"
    }

    Write-Step 2 5 "登录 Cloudflare..."
    if (Test-Path $cfCertFile) {
        Write-OK "已检测到授权，跳过"
    } else {
        Write-Host "  即将打开浏览器授权，请选择你的域名并授权" -ForegroundColor $Y
        $loginProc = Start-Process -FilePath $cfExe -ArgumentList "tunnel", "login" -PassThru -NoNewWindow -RedirectStandardOutput "$env:TEMP\cf-login-stdout.txt" -RedirectStandardError "$env:TEMP\cf-login-stderr.txt"
        $timeout = 300; $elapsed = 0; $urlShown = $false
        while (-not (Test-Path $cfCertFile) -and $elapsed -lt $timeout) {
            Start-Sleep -Seconds 2; $elapsed += 2
            if (-not $urlShown -and $elapsed -ge 4) {
                $errContent = Get-Content "$env:TEMP\cf-login-stderr.txt" -ErrorAction SilentlyContinue
                if ($errContent) {
                    $urlLine = $errContent | Where-Object { $_ -match "https://" } | Select-Object -First 1
                    if ($urlLine) {
                        $url = [regex]::Match($urlLine, "https://\S+").Value
                        if ($url) { Write-Host "  授权 URL: $url" -ForegroundColor $C; $urlShown = $true }
                    }
                }
            }
        }
        if (Test-Path $cfCertFile) { Write-OK "授权成功" }
        else { Write-Err "登录超时"; return }
    }

    Write-Step 3 5 "创建隧道..."
    $listOutput = & $cfExe tunnel list 2>&1 | Out-String
    $tunnelId = ""
    if ($listOutput -match "([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})\s+openmodelpool") {
        $tunnelId = $Matches[1]
        Write-OK "已有隧道，复用: $tunnelId"
    } else {
        $createOutput = & $cfExe tunnel create openmodelpool 2>&1 | Out-String
        if ($createOutput -match "([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})") {
            $tunnelId = $Matches[1]
            Write-OK "隧道已创建: $tunnelId"
        } else {
            Write-Err "创建失败: $createOutput"; return
        }
    }

    Write-Step 4 5 "绑定域名..."
    $subdomain = Read-Host "  请输入子域名 (如 omp.yourdomain.com)"
    $routeOutput = & $cfExe tunnel route dns openmodelpool $subdomain 2>&1 | Out-String
    if ($routeOutput -match "Added CNAME" -or $routeOutput -match "already exists") {
        Write-OK "域名已绑定: $subdomain"
    } else {
        Write-Info $routeOutput
    }

    Write-Step 5 5 "配置并启动..."
    if (-not (Test-Path $cfConfigDir)) { New-Item -ItemType Directory -Path $cfConfigDir -Force | Out-Null }
    $credFile = "$cfConfigDir\$tunnelId.json"
    @"
tunnel: $tunnelId
credentials-file: $credFile

ingress:
  - hostname: $subdomain
    service: http://localhost:$Port
  - service: http_status:404
"@ | Set-Content "$cfConfigDir\config.yml" -Encoding UTF8

    Stop-Cloudflared
    $action = New-ScheduledTaskAction -Execute $cfExe -Argument "tunnel run openmodelpool"
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $cfTaskName -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
    Start-ScheduledTask -TaskName $cfTaskName
    Start-Sleep -Seconds 5

    $proc = Get-Process -Name "cloudflared" -ErrorAction SilentlyContinue
    if ($proc) {
        Write-Host ""
        Write-Host "  ============================================" -ForegroundColor $G
        Write-Host "   Cloudflare Tunnel 配置完成！" -ForegroundColor $G
        Write-Host "  ============================================" -ForegroundColor $G
        Write-Host "  外网地址: https://$subdomain" -ForegroundColor $G
        Write-Host "  管理面板: https://$subdomain/admin" -ForegroundColor $G
        Write-Host ""
    } else {
        Write-Err "隧道进程未启动"
        Write-Host "  手动测试: & '$cfExe' tunnel run openmodelpool" -ForegroundColor $Y
    }
}

# ============================================================
# 4b. FRP
# ============================================================
function Setup-FRP {
    Write-Title "配置 FRP 内网穿透"
    Write-Host ""
    Write-Host "  FRP 原理：通过一台有公网 IP 的服务器中转流量。" -ForegroundColor $C
    Write-Host "  本机(frpc) → 公网服务器(frps) → 外网用户访问" -ForegroundColor $C
    Write-Host ""
    Write-Host "  优点：固定 IP+端口 | 可自定义端口 | 无域名要求" -ForegroundColor $G
    Write-Host "  缺点：需要一台公网服务器 (云服务器约 30-50 元/月)" -ForegroundColor $Y
    Write-Host ""
    Write-Host "  ── FRP 服务器搭建指南 (在公网服务器上执行) ──" -ForegroundColor $C
    Write-Host ""
    Write-Host "  1. 下载 FRP:" -ForegroundColor $Y
    Write-Host "     wget https://github.com/fatedier/frp/releases/download/v0.61.1/frp_0.61.1_linux_amd64.tar.gz" -ForegroundColor DarkGray
    Write-Host "     tar xzf frp_0.61.1_linux_amd64.tar.gz" -ForegroundColor DarkGray
    Write-Host "     cd frp_0.61.1_linux_amd64" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  2. 创建服务器配置 frps.toml:" -ForegroundColor $Y
    Write-Host '     bindPort = 7000              # FRP 通信端口' -ForegroundColor DarkGray
    Write-Host '     auth.token = "你的强密码"     # 认证密码，必须设置！' -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  3. 启动并设为开机自启:" -ForegroundColor $Y
    Write-Host "     ./frps -c frps.toml          # 前台运行测试" -ForegroundColor DarkGray
    Write-Host '     # systemd 开机自启:' -ForegroundColor DarkGray
    Write-Host '     sudo cp frps /usr/local/bin/' -ForegroundColor DarkGray
    Write-Host '     sudo mkdir -p /etc/frp && sudo cp frps.toml /etc/frp/' -ForegroundColor DarkGray
    Write-Host '     sudo tee /etc/systemd/system/frps.service << EOF' -ForegroundColor DarkGray
    Write-Host '     [Unit]' -ForegroundColor DarkGray
    Write-Host '     Description=FRP Server' -ForegroundColor DarkGray
    Write-Host '     After=network.target' -ForegroundColor DarkGray
    Write-Host '     [Service]' -ForegroundColor DarkGray
    Write-Host '     Type=simple' -ForegroundColor DarkGray
    Write-Host '     ExecStart=/usr/local/bin/frps -c /etc/frp/frps.toml' -ForegroundColor DarkGray
    Write-Host '     Restart=always' -ForegroundColor DarkGray
    Write-Host '     [Install]' -ForegroundColor DarkGray
    Write-Host '     WantedBy=multi-user.target' -ForegroundColor DarkGray
    Write-Host '     EOF' -ForegroundColor DarkGray
    Write-Host '     sudo systemctl enable --now frps' -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  4. 安全组/防火墙放行端口:" -ForegroundColor $Y
    Write-Host "     TCP 7000       (FRP 通信)" -ForegroundColor DarkGray
    Write-Host "     TCP 8001-8010  (映射端口，按需放行)" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  安全提示: auth.token 请使用随机强密码，避免被他人蹭用！" -ForegroundColor $R
    Write-Host ""

    $hasServer = Read-Host "  FRP 服务器已搭建好？(y/n)"
    if ($hasServer -ne "y") {
        Write-Host "  请先搭建 FRP 服务器，再来配置客户端。" -ForegroundColor $Y
        return
    }

    $frpServer = Read-Host "  FRP 服务器公网 IP"
    if (-not $frpServer) { Write-Err "不能为空"; return }

    $frpToken = Read-Host "  FRP 认证 Token"
    if (-not $frpToken) { Write-Err "不能为空"; return }

    Write-Host ""
    Write-Host "  远程映射端口：外网用户通过此端口访问你的 OMP" -ForegroundColor $C
    Write-Host "  例如填 8001，则外网访问地址为 http://服务器IP:8001" -ForegroundColor DarkGray
    Write-Host "  确保该端口已在服务器安全组中放行！" -ForegroundColor $Y
    $remotePortStr = Read-Host "  远程映射端口 [默认: 8001]"
    if (-not $remotePortStr) { $remotePort = 8001 } else { $remotePort = [int]$remotePortStr }

    $nodeName = ($env:COMPUTERNAME).ToLower() -replace '[^a-z0-9-]', ''

    Write-Step 1 4 "安装 frpc..."
    if (-not (Test-Path $frpExe)) {
        New-Item -ItemType Directory -Path $frpDir -Force | Out-Null
        $frpVer = "0.61.1"
        $frpUrl = "https://github.com/fatedier/frp/releases/download/v$frpVer/frp_${frpVer}_windows_amd64.zip"
        $tmpZip = Join-Path $env:TEMP "frp-download.zip"
        Invoke-WebRequest -Uri $frpUrl -OutFile $tmpZip -UseBasicParsing
        $tmpDir = Join-Path $env:TEMP "frp-extract"
        if (Test-Path $tmpDir) { Remove-Item $tmpDir -Recurse -Force }
        Expand-Archive -Path $tmpZip -DestinationPath $tmpDir -Force
        Copy-Item (Join-Path $tmpDir "frp_${frpVer}_windows_amd64\frpc.exe") -Destination $frpExe -Force
        Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
        Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
        Write-OK "安装完成"
    } else {
        Write-OK "已安装"
    }

    Write-Step 2 4 "创建配置..."
    @"
serverAddr = "$frpServer"
serverPort = 7000
auth.token = "$frpToken"

[[proxies]]
name = "omp-$nodeName"
type = "tcp"
localIP = "127.0.0.1"
localPort = $Port
remotePort = $remotePort
"@ | Set-Content $frpConfig -Encoding UTF8
    Write-OK "配置已写入 $frpConfig"

    Write-Step 3 4 "测试连接..."
    $testProc = Start-Process -FilePath $frpExe -ArgumentList "-c", $frpConfig -PassThru -NoNewWindow
    Start-Sleep -Seconds 3
    Stop-Process -Id $testProc.Id -Force -ErrorAction SilentlyContinue
    Write-OK "测试完成"

    Write-Step 4 4 "设置开机自启..."
    Stop-FRP
    $action = New-ScheduledTaskAction -Execute $frpExe -Argument "-c `"$frpConfig`"" -WorkingDirectory $frpDir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $frpTaskName -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
    Start-ScheduledTask -TaskName $frpTaskName
    Start-Sleep -Seconds 2

    $proc = Get-Process -Name "frpc" -ErrorAction SilentlyContinue
    if ($proc) {
        Write-Host ""
        Write-Host "  ============================================" -ForegroundColor $G
        Write-Host "   FRP 穿透配置完成！" -ForegroundColor $G
        Write-Host "  ============================================" -ForegroundColor $G
        Write-Host "  外网地址: http://${frpServer}:$remotePort" -ForegroundColor $G
        Write-Host "  管理面板: http://${frpServer}:$remotePort/admin" -ForegroundColor $G
        Write-Host ""
    } else {
        Write-Err "frpc 未启动，请检查服务器地址和端口"
    }
}

# ============================================================
# 4c. ngrok
# ============================================================
function Setup-Ngrok {
    Write-Title "配置 ngrok 内网穿透"
    Write-Host ""
    Write-Host "  ngrok 原理：通过 ngrok 云端服务中转，自动分配 HTTPS 域名。" -ForegroundColor $C
    Write-Host "  本机 → ngrok 云端 → 外网用户通过 xxx.ngrok.app 访问" -ForegroundColor $C
    Write-Host ""
    Write-Host "  优点：注册即用 | 自动 HTTPS | 无需服务器/域名 | 30秒搞定" -ForegroundColor $G
    Write-Host "  缺点：免费版域名随机且每次重启变化 | 有流量和连接数限制" -ForegroundColor $Y
    Write-Host ""
    Write-Host "  ── 获取 Authtoken 步骤 ──" -ForegroundColor $C
    Write-Host ""
    Write-Host "  1. 注册 ngrok 账号 (免费):" -ForegroundColor $Y
    Write-Host "     https://dashboard.ngrok.com/signup" -ForegroundColor $C
    Write-Host ""
    Write-Host "  2. 完成邮箱验证 (必须)" -ForegroundColor $Y
    Write-Host ""
    Write-Host "  3. 获取 Authtoken:" -ForegroundColor $Y
    Write-Host "     登录后访问 https://dashboard.ngrok.com/get-started/your-authtoken" -ForegroundColor $C
    Write-Host "     复制页面显示的 Authtoken (格式如: 2abc123...)" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  ── 域名说明 ──" -ForegroundColor $C
    Write-Host ""
    Write-Host "  免费版: 留空即可，ngrok 每次启动分配随机域名 (如 a1b2c3.ngrok.app)" -ForegroundColor $W
    Write-Host "    注意: 免费版每次重启域名会变！不适合长期固定访问" -ForegroundColor $Y
    Write-Host ""
    Write-Host "  付费版: 可绑定固定域名" -ForegroundColor $W
    Write-Host "    1. 在 ngrok 控制台 → Domains → Claim 一个域名" -ForegroundColor DarkGray
    Write-Host "    2. 将 claim 到的域名填入下方 (如 myapp.ngrok.app)" -ForegroundColor DarkGray
    Write-Host ""

    $ngrokToken = Read-Host "  请输入 ngrok Authtoken"
    if (-not $ngrokToken) { Write-Err "不能为空"; return }

    Write-Host ""
    $ngrokDomain = Read-Host "  固定域名 (免费版留空)"

    Write-Step 1 4 "安装 ngrok..."
    if (-not (Test-Path $ngrokExe)) {
        New-Item -ItemType Directory -Path $ngrokDir -Force | Out-Null
        $ngrokUrl = "https://bin.equinox.io/c/bNyj1mQVY4c/ngrok-v3-stable-windows-amd64.zip"
        $tmpZip = Join-Path $env:TEMP "ngrok-download.zip"
        try {
            Invoke-WebRequest -Uri $ngrokUrl -OutFile $tmpZip -UseBasicParsing
            $tmpDir = Join-Path $env:TEMP "ngrok-extract"
            if (Test-Path $tmpDir) { Remove-Item $tmpDir -Recurse -Force }
            Expand-Archive -Path $tmpZip -DestinationPath $tmpDir -Force
            Copy-Item (Join-Path $tmpDir "ngrok.exe") -Destination $ngrokExe -Force
            Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
            Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
            $currentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
            if ($currentPath -notlike "*$ngrokDir*") {
                [Environment]::SetEnvironmentVariable("Path", "$currentPath;$ngrokDir", "Machine")
                $env:Path += ";$ngrokDir"
            }
            Write-OK "安装完成"
        } catch {
            Write-Err "下载失败: $_"
            return
        }
    } else {
        Write-OK "已安装"
    }

    Write-Step 2 4 "配置 Authtoken..."
    & $ngrokExe config add-authtoken $ngrokToken 2>&1 | Out-Null
    Write-OK "Authtoken 已配置"

    Write-Step 3 4 "测试连接..."
    $testArgs = @("http", "$Port")
    if ($ngrokDomain) { $testArgs = @("http", "--domain=$ngrokDomain", "$Port") }
    $testProc = Start-Process -FilePath $ngrokExe -ArgumentList $testArgs -PassThru -NoNewWindow
    Start-Sleep -Seconds 5

    # 尝试获取 ngrok 分配的公网 URL
    $ngrokUrl = ""
    try {
        $resp = Invoke-WebRequest -Uri "http://localhost:4040/api/tunnels" -UseBasicParsing -ErrorAction Stop
        $tunnels = $resp.Content | ConvertFrom-Json
        if ($tunnels.tunnels.Count -gt 0) {
            $ngrokUrl = $tunnels.tunnels[0].public_url
        }
    } catch {}

    Stop-Process -Id $testProc.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
    Write-OK "测试完成"

    Write-Step 4 4 "设置开机自启..."
    Stop-Ngrok

    # 创建启动脚本
    $ngrokStartBat = Join-Path $ngrokDir "start-ngrok.bat"
    $batContent = @"
@echo off
"$ngrokExe" http $Port$(if ($ngrokDomain) { " --domain=$ngrokDomain" })
"@
    $batContent | Set-Content $ngrokStartBat -Encoding ASCII

    $action = New-ScheduledTaskAction -Execute "cmd.exe" -Argument "/c `"$ngrokStartBat`""
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $ngrokTaskName -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
    Start-ScheduledTask -TaskName $ngrokTaskName
    Start-Sleep -Seconds 3

    $proc = Get-Process -Name "ngrok" -ErrorAction SilentlyContinue
    if ($proc) {
        # 再次获取 URL
        if (-not $ngrokUrl) {
            try {
                Start-Sleep -Seconds 2
                $resp = Invoke-WebRequest -Uri "http://localhost:4040/api/tunnels" -UseBasicParsing -ErrorAction Stop
                $tunnels = $resp.Content | ConvertFrom-Json
                if ($tunnels.tunnels.Count -gt 0) { $ngrokUrl = $tunnels.tunnels[0].public_url }
            } catch {}
        }

        Write-Host ""
        Write-Host "  ============================================" -ForegroundColor $G
        Write-Host "   ngrok 穿透配置完成！" -ForegroundColor $G
        Write-Host "  ============================================" -ForegroundColor $G
        if ($ngrokUrl) {
            Write-Host "  外网地址: $ngrokUrl" -ForegroundColor $G
            $adminUrl = $ngrokUrl -replace "/$", ""
            Write-Host "  管理面板: $adminUrl/admin" -ForegroundColor $G
        } else {
            Write-Host "  外网地址: 请访问 http://localhost:4040 查看" -ForegroundColor $Y
        }
        if ($ngrokDomain) { Write-Host "  固定域名: $ngrokDomain" -ForegroundColor $G }
        Write-Host ""
    } else {
        Write-Err "ngrok 未启动，请检查 Authtoken"
    }
}

# ============================================================
# 5. 重置穿透 (子菜单)
# ============================================================
function Reset-Tunnel-Menu {
    Write-Title "重置穿透配置"
    Write-Host "  请选择要重置的方案：" -ForegroundColor $C
    Write-Host "    1) 重置 Cloudflare Tunnel"
    Write-Host "    2) 重置 FRP"
    Write-Host "    3) 重置 ngrok"
    Write-Host "    4) 重置全部"
    $choice = Read-Host "  请选择 [1/2/3/4]"
    switch ($choice) {
        "1" { Reset-Cloudflare }
        "2" { Reset-FRP }
        "3" { Reset-Ngrok }
        "4" { Reset-Cloudflare; Reset-FRP; Reset-Ngrok }
        default { Write-Host "  无效选项" -ForegroundColor $R }
    }
}

function Reset-Cloudflare {
    Write-Title "重置 Cloudflare Tunnel"
    $confirm = Read-Host "  确认重置？(输入 yes 确认)"
    if ($confirm -ne "yes") { Write-Host "  已取消" -ForegroundColor $Y; return }

    Write-Step 1 5 "删除隧道..."
    if (Test-Path $cfExe) { & $cfExe tunnel delete openmodelpool 2>&1 | Out-Null }
    Write-OK "完成"

    Write-Step 2 5 "删除服务和进程..."
    Remove-CloudflaredService
    Stop-Cloudflared
    Start-Sleep -Seconds 2
    Write-OK "完成"

    Write-Step 3 5 "删除计划任务..."
    Unregister-ScheduledTask -TaskName $cfTaskName -Confirm:$false -ErrorAction SilentlyContinue
    Write-OK "完成"

    Write-Step 4 5 "删除配置文件..."
    if (Test-Path $cfConfigDir) { Remove-Item $cfConfigDir -Recurse -Force -ErrorAction SilentlyContinue }
    Write-OK "完成"

    Write-Step 5 5 "重新配置..."
    Setup-Cloudflare
}

function Reset-FRP {
    Write-Title "重置 FRP"
    $confirm = Read-Host "  确认重置？(输入 yes 确认)"
    if ($confirm -ne "yes") { Write-Host "  已取消" -ForegroundColor $Y; return }

    Write-Step 1 3 "停止服务和进程..."
    Stop-FRP
    Unregister-ScheduledTask -TaskName $frpTaskName -Confirm:$false -ErrorAction SilentlyContinue
    Write-OK "完成"

    Write-Step 2 3 "删除配置..."
    if (Test-Path $frpConfig) { Remove-Item $frpConfig -Force -ErrorAction SilentlyContinue }
    Write-OK "完成"

    Write-Step 3 3 "重新配置..."
    Setup-FRP
}

function Reset-Ngrok {
    Write-Title "重置 ngrok"
    $confirm = Read-Host "  确认重置？(输入 yes 确认)"
    if ($confirm -ne "yes") { Write-Host "  已取消" -ForegroundColor $Y; return }

    Write-Step 1 3 "停止服务和进程..."
    Stop-Ngrok
    Unregister-ScheduledTask -TaskName $ngrokTaskName -Confirm:$false -ErrorAction SilentlyContinue
    Write-OK "完成"

    Write-Step 2 3 "删除配置..."
    $ngrokConfig = "$env:USERPROFILE\AppData\Local\ngrok\ngrok.yml"
    if (Test-Path $ngrokConfig) { Remove-Item $ngrokConfig -Force -ErrorAction SilentlyContinue }
    Write-OK "完成"

    Write-Step 3 3 "重新配置..."
    Setup-Ngrok
}

# ============================================================
# 6. 修改端口
# ============================================================
function Change-Port {
    Write-Title "修改端口"

    $newPort = Read-Host "  请输入新端口 (当前: $Port)"
    if (-not $newPort -or $newPort -lt 1 -or $newPort -gt 65535) { Write-Err "无效端口"; return }

    Write-Step 1 4 "停止服务..."
    Stop-OMP
    Stop-All-Tunnels
    Write-OK "已停止"

    Write-Step 2 4 "更新 OMP 配置..."
    $startBat = Join-Path $InstallDir "start.bat"
    @"
@echo off
cd /d "$InstallDir"
set PORT=$newPort
$exeName >> "$logFile" 2>&1
"@ | Set-Content $startBat -Encoding ASCII

    $action = New-ScheduledTaskAction -Execute $exePath -WorkingDirectory $InstallDir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
    Register-ScheduledTask -TaskName $ompTaskName -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
    Write-OK "OMP 端口已更新为 $newPort"

    Write-Step 3 4 "更新隧道配置..."
    # Cloudflare
    $cfConfig = "$cfConfigDir\config.yml"
    if (Test-Path $cfConfig) {
        $cfContent = Get-Content $cfConfig -Raw
        $cfContent = $cfContent -replace "http://localhost:\d+", "http://localhost:$newPort"
        $cfContent | Set-Content $cfConfig -Encoding UTF8
        Write-OK "Cloudflare Tunnel 已更新"
    }
    # FRP
    if (Test-Path $frpConfig) {
        $frpContent = Get-Content $frpConfig -Raw
        $frpContent = $frpContent -replace "localPort = \d+", "localPort = $newPort"
        $frpContent | Set-Content $frpConfig -Encoding UTF8
        Write-OK "FRP 已更新"
    }
    # ngrok
    $ngrokStartBat = Join-Path $ngrokDir "start-ngrok.bat"
    if (Test-Path $ngrokStartBat) {
        $ngrokDomain = ""
        $batContent = Get-Content $ngrokStartBat -Raw
        if ($batContent -match "--domain=(\S+)") { $ngrokDomain = $Matches[1] }
        @"
@echo off
"$ngrokExe" http $newPort$(if ($ngrokDomain) { " --domain=$ngrokDomain" })
"@ | Set-Content $ngrokStartBat -Encoding ASCII
        Write-OK "ngrok 已更新"
    }

    Write-Step 4 4 "重启服务..."
    $script:Port = $newPort
    Start-OMP
    Start-Sleep -Seconds 2

    # 重启已配置的隧道
    if (Get-ScheduledTask -TaskName $cfTaskName -ErrorAction SilentlyContinue) { Start-ScheduledTask -TaskName $cfTaskName }
    if (Get-ScheduledTask -TaskName $frpTaskName -ErrorAction SilentlyContinue) { Start-ScheduledTask -TaskName $frpTaskName }
    if (Get-ScheduledTask -TaskName $ngrokTaskName -ErrorAction SilentlyContinue) { Start-ScheduledTask -TaskName $ngrokTaskName }

    $proc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
    if ($proc) {
        Write-Host ""
        Write-Host "  管理面板: http://localhost:$newPort/admin" -ForegroundColor $G
        Write-Host ""
    } else {
        Write-Err "启动失败"
    }
}

# ============================================================
# 7. 查看状态
# ============================================================
function Show-Status {
    Write-Title "系统状态"

    # OMP
    Write-Host "  [OpenModelPool]" -ForegroundColor $C
    $ompProc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
    if ($ompProc) { Write-OK "运行中 (PID: $($ompProc.Id))" }
    else { Write-Err "未运行" }

    if (Test-Path $exePath) {
        Write-Info "安装路径: $exePath"
        Write-Info "构建时间: $((Get-Item $exePath).LastWriteTime.ToString('yyyy-MM-dd HH:mm'))"
    } else { Write-Info "未安装" }

    $portConn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    if ($portConn) { Write-OK "端口 $Port 监听中" }
    else { Write-Info "端口 $Port 未监听" }

    $ompTask = Get-ScheduledTask -TaskName $ompTaskName -ErrorAction SilentlyContinue
    if ($ompTask) { Write-Info "计划任务: $ompTaskName ($($ompTask.State))" }

    # Cloudflare Tunnel
    Write-Host ""
    Write-Host "  [Cloudflare Tunnel]" -ForegroundColor $C
    $cfProc = Get-Process -Name "cloudflared" -ErrorAction SilentlyContinue
    if ($cfProc) { Write-OK "运行中 (PID: $($cfProc.Id))" }
    else { Write-Info "未运行" }

    if (Test-Path $cfExe) { Write-Info "cloudflared: 已安装" }
    else { Write-Info "cloudflared: 未安装" }

    if (Test-Path $cfCertFile) { Write-Info "授权状态: 已登录" }
    else { Write-Info "授权状态: 未登录" }

    $cfConfig = "$cfConfigDir\config.yml"
    if (Test-Path $cfConfig) {
        $configContent = Get-Content $cfConfig -Raw
        if ($configContent -match "hostname:\s*(.+)") { Write-Info "域名: $($Matches[1].Trim())" }
    } else { Write-Info "配置文件: 未创建" }

    $cfTask = Get-ScheduledTask -TaskName $cfTaskName -ErrorAction SilentlyContinue
    if ($cfTask) { Write-Info "计划任务: $cfTaskName ($($cfTask.State))" }

    $cfSvc = Get-Service -Name cloudflared -ErrorAction SilentlyContinue
    if ($cfSvc) {
        Write-Host "  WARNING  检测到残留 Windows 服务: cloudflared ($($cfSvc.Status))" -ForegroundColor $Y
    }

    # FRP
    Write-Host ""
    Write-Host "  [FRP]" -ForegroundColor $C
    $frpProc = Get-Process -Name "frpc" -ErrorAction SilentlyContinue
    if ($frpProc) { Write-OK "运行中 (PID: $($frpProc.Id))" }
    else { Write-Info "未运行" }

    if (Test-Path $frpExe) { Write-Info "frpc: 已安装" }
    else { Write-Info "frpc: 未安装" }

    if (Test-Path $frpConfig) {
        $frpConfigContent = Get-Content $frpConfig -Raw
        if ($frpConfigContent -match 'serverAddr = "([^"]+)"') { Write-Info "服务器: $($Matches[1])" }
        if ($frpConfigContent -match "remotePort = (\d+)") { Write-Info "远程端口: $($Matches[1])" }
    } else { Write-Info "配置文件: 未创建" }

    $frpTask = Get-ScheduledTask -TaskName $frpTaskName -ErrorAction SilentlyContinue
    if ($frpTask) { Write-Info "计划任务: $frpTaskName ($($frpTask.State))" }

    # ngrok
    Write-Host ""
    Write-Host "  [ngrok]" -ForegroundColor $C
    $ngrokProc = Get-Process -Name "ngrok" -ErrorAction SilentlyContinue
    if ($ngrokProc) { Write-OK "运行中 (PID: $($ngrokProc.Id))" }
    else { Write-Info "未运行" }

    if (Test-Path $ngrokExe) { Write-Info "ngrok: 已安装" }
    else { Write-Info "ngrok: 未安装" }

    $ngrokTask = Get-ScheduledTask -TaskName $ngrokTaskName -ErrorAction SilentlyContinue
    if ($ngrokTask) { Write-Info "计划任务: $ngrokTaskName ($($ngrokTask.State))" }

    # 尝试获取 ngrok URL
    if ($ngrokProc) {
        try {
            $resp = Invoke-WebRequest -Uri "http://localhost:4040/api/tunnels" -UseBasicParsing -ErrorAction Stop
            $tunnels = $resp.Content | ConvertFrom-Json
            if ($tunnels.tunnels.Count -gt 0) {
                Write-Info "公网地址: $($tunnels.tunnels[0].public_url)"
            }
        } catch { Write-Info "公网地址: 获取失败" }
    }

    # 网络
    Write-Host ""
    Write-Host "  [网络信息]" -ForegroundColor $C
    $ips = Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -notmatch "Loopback" -and $_.IPAddress -notmatch "^169\.254" }
    foreach ($ip in $ips) { Write-Info "$($ip.InterfaceAlias): $($ip.IPAddress)" }
    Write-Host ""
}

# ============================================================
# 8. 重启服务
# ============================================================
function Restart-All {
    Write-Title "重启服务"

    Write-Step 1 4 "重启 OpenModelPool..."
    Stop-OMP
    Start-Sleep -Seconds 2
    Start-OMP
    $ompProc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
    if ($ompProc) { Write-OK "OMP 已启动 (PID: $($ompProc.Id))" }
    else { Write-Err "OMP 启动失败" }

    Write-Step 2 4 "重启 Cloudflare Tunnel..."
    if (Get-ScheduledTask -TaskName $cfTaskName -ErrorAction SilentlyContinue) {
        Stop-Cloudflared
        Start-Sleep -Seconds 2
        Start-ScheduledTask -TaskName $cfTaskName
        Start-Sleep -Seconds 3
        $cfProc = Get-Process -Name "cloudflared" -ErrorAction SilentlyContinue
        if ($cfProc) { Write-OK "Tunnel 已启动 (PID: $($cfProc.Id))" }
        else { Write-Err "Tunnel 启动失败" }
    } else { Write-Info "Cloudflare Tunnel 未配置，跳过" }

    Write-Step 3 4 "重启 FRP..."
    if (Get-ScheduledTask -TaskName $frpTaskName -ErrorAction SilentlyContinue) {
        Stop-FRP
        Start-Sleep -Seconds 1
        Start-ScheduledTask -TaskName $frpTaskName
        Start-Sleep -Seconds 2
        $frpProc = Get-Process -Name "frpc" -ErrorAction SilentlyContinue
        if ($frpProc) { Write-OK "FRP 已启动 (PID: $($frpProc.Id))" }
        else { Write-Err "FRP 启动失败" }
    } else { Write-Info "FRP 未配置，跳过" }

    Write-Step 4 4 "重启 ngrok..."
    if (Get-ScheduledTask -TaskName $ngrokTaskName -ErrorAction SilentlyContinue) {
        Stop-Ngrok
        Start-Sleep -Seconds 1
        Start-ScheduledTask -TaskName $ngrokTaskName
        Start-Sleep -Seconds 3
        $ngrokProc = Get-Process -Name "ngrok" -ErrorAction SilentlyContinue
        if ($ngrokProc) { Write-OK "ngrok 已启动 (PID: $($ngrokProc.Id))" }
        else { Write-Err "ngrok 启动失败" }
    } else { Write-Info "ngrok 未配置，跳过" }

    Write-Host ""
}

# ============================================================
# 主菜单
# ============================================================
if (-not (Test-Admin)) {
    Write-Host "[ERROR] 请使用管理员权限运行 PowerShell" -ForegroundColor $R
    exit 1
}

while ($true) {
    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor $C
    Write-Host "       OpenModelPool 全功能管理工具" -ForegroundColor $C
    Write-Host "  ============================================" -ForegroundColor $C
    Write-Host "    1. 安装          全新安装 OMP" -ForegroundColor $W
    Write-Host "    2. 升级          增量更新 (保留配置)" -ForegroundColor $W
    Write-Host "    3. 卸载          彻底删除所有组件" -ForegroundColor $W
    Write-Host "    4. 配置穿透      Cloudflare / FRP / ngrok" -ForegroundColor $W
    Write-Host "    5. 重置穿透      选择重置任一/全部隧道" -ForegroundColor $W
    Write-Host "    6. 修改端口      更换 OMP 服务端口" -ForegroundColor $W
    Write-Host "    7. 查看状态      检查所有组件运行情况" -ForegroundColor $W
    Write-Host "    8. 重启服务      重启 OMP + 所有隧道" -ForegroundColor $W
    Write-Host "    0. 退出" -ForegroundColor $W
    Write-Host "  ============================================" -ForegroundColor $C
    Write-Host "  安装目录: $InstallDir  端口: $Port" -ForegroundColor DarkGray
    $choice = Read-Host "  请选择 [0-8]"

    switch ($choice) {
        "1" { Install-OMP }
        "2" { Upgrade-OMP }
        "3" { Uninstall-OMP }
        "4" { Setup-Tunnel-Menu }
        "5" { Reset-Tunnel-Menu }
        "6" { Change-Port }
        "7" { Show-Status }
        "8" { Restart-All }
        "0" { Write-Host "  Bye!" -ForegroundColor $C; exit 0 }
        default { Write-Host "  无效选项" -ForegroundColor $R }
    }
}
