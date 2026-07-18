# ============================================================
#  OpenModelPool 一键部署脚本 (Windows)
#  自动从 GitHub 下载对应架构的二进制文件
#  HTML 文件已嵌入二进制，无需额外文件
#  
#  使用方法 (管理员 PowerShell):
#    irm https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-deploy.ps1 | iex
#    或:
#    .\omp-deploy.ps1 [-InstallDir "C:\openmodelpool"] [-Port 8000]
# ============================================================
param(
    [string]$InstallDir = "C:\openmodelpool",
    [int]$Port = 8000
)

$ErrorActionPreference = "Stop"
$GITHUB_REPO = "lisiyu/openmodelpool"
$RELEASE_TAG = "v3.4.1-release"
$PKG = "openmodelpool-windows-amd64.zip"
$DOWNLOAD_URL = "https://github.com/$GITHUB_REPO/releases/download/$RELEASE_TAG/$PKG"

Write-Host ""
Write-Host "  ============================================" -ForegroundColor Cyan
Write-Host "   OpenModelPool 一键部署 (Windows 自动下载)" -ForegroundColor Cyan
Write-Host "  ============================================" -ForegroundColor Cyan
Write-Host ""

$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Write-Host "[错误] 请使用管理员权限运行 PowerShell" -ForegroundColor Red
    exit 1
}

# [0/5] 停止已有服务/进程
Write-Host "[0/5] 清理旧版本..." -ForegroundColor Cyan

# 停止 NSSM 服务（如果存在）
$existingService = Get-Service -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($existingService) {
    Write-Host "      停止已有 NSSM 服务..." -ForegroundColor Yellow
    try { & nssm stop openmodelpool 2>$null } catch {}
    Start-Sleep -Seconds 2
    try { & nssm remove openmodelpool confirm 2>$null } catch {}
    Start-Sleep -Seconds 1
}

# 停止计划任务（如果存在）
$existingTask = Get-ScheduledTask -TaskName "OpenModelPool" -ErrorAction SilentlyContinue
if ($existingTask) {
    Write-Host "      停止已有计划任务..." -ForegroundColor Yellow
    Stop-ScheduledTask -TaskName "OpenModelPool" -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName "OpenModelPool" -Confirm:$false -ErrorAction SilentlyContinue
}

# 杀掉残留进程
Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2

# 确保端口已释放
$portConn = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue
if ($portConn) {
    $portConn | ForEach-Object { 
        Stop-Process -Id $_.OwningProcess -Force -ErrorAction SilentlyContinue 
    }
    Start-Sleep -Seconds 2
}
Write-Host "      清理完成" -ForegroundColor Green

# [1/5] 下载
Write-Host "[1/5] 下载: $PKG" -ForegroundColor Cyan
Write-Host "      $DOWNLOAD_URL"
$tmpZip = Join-Path $env:TEMP "omp-deploy.zip"
try {
    # 使用 TLS 1.2 解决 GitHub 下载问题
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Invoke-WebRequest -Uri $DOWNLOAD_URL -OutFile $tmpZip -UseBasicParsing
} catch {
    Write-Host "[错误] 下载失败: $_" -ForegroundColor Red
    exit 1
}
$size = [math]::Round((Get-Item $tmpZip).Length / 1MB, 1)
Write-Host "      下载完成 (${size} MB)" -ForegroundColor Green

# [2/5] 解压
Write-Host "[2/5] 解压..." -ForegroundColor Cyan
$tmpDir = Join-Path $env:TEMP "omp-deploy-extract"
if (Test-Path $tmpDir) { Remove-Item $tmpDir -Recurse -Force }
Expand-Archive -Path $tmpZip -DestinationPath $tmpDir -Force
Write-Host "      解压完成" -ForegroundColor Green

# [3/5] 安装
Write-Host "[3/5] 安装到 $InstallDir ..." -ForegroundColor Cyan
$dataDir = Join-Path $InstallDir "data"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null

# 复制二进制文件（HTML 已嵌入，无需复制 HTML 文件）
Copy-Item (Join-Path $tmpDir "openmodelpool.exe") -Destination (Join-Path $InstallDir "openmodelpool.exe") -Force

if (Test-Path (Join-Path $tmpDir "docs")) {
    Copy-Item (Join-Path $tmpDir "docs") -Destination $InstallDir -Force -Recurse
}
Write-Host "      安装完成" -ForegroundColor Green

# [4/5] 配置服务
Write-Host "[4/5] 配置服务 (端口 $Port)..." -ForegroundColor Cyan

$startBat = Join-Path $InstallDir "start.bat"
@"
@echo off
cd /d "$InstallDir"
set PORT=$Port
openmodelpool.exe >> "$dataDir\app.log" 2>&1
"@ | Set-Content $startBat -Encoding ASCII

$stopBat = Join-Path $InstallDir "stop.bat"
@"
@echo off
taskkill /f /im openmodelpool.exe 2>nul
echo stopped
"@ | Set-Content $stopBat -Encoding ASCII

$nssm = Get-Command nssm -ErrorAction SilentlyContinue
if ($nssm) {
    & nssm install openmodelpool (Join-Path $InstallDir "openmodelpool.exe")
    & nssm set openmodelpool AppDirectory "$InstallDir"
    & nssm set openmodelpool AppEnvironmentExtra "PORT=$Port"
    & nssm set openmodelpool AppStdout "$dataDir\app.log"
    & nssm set openmodelpool AppStderr "$dataDir\app.log"
    & nssm set openmodelpool AppRestartDelay 5000
    Write-Host "      服务方式: NSSM 系统服务" -ForegroundColor Green
} else {
    $action = New-ScheduledTaskAction -Execute (Join-Path $InstallDir "openmodelpool.exe") -WorkingDirectory $InstallDir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Register-ScheduledTask -TaskName "OpenModelPool" -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
    Write-Host "      服务方式: 计划任务 (开机自启)" -ForegroundColor Green
}

# [5/5] 启动
Write-Host "[5/5] 启动服务..." -ForegroundColor Cyan

if ($nssm) {
    & nssm start openmodelpool
} else {
    # 通过 start.bat 启动，确保工作目录正确
    Start-Process -FilePath "cmd.exe" -ArgumentList "/c", $startBat -WindowStyle Hidden
}
Start-Sleep -Seconds 3

$proc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($proc) {
    $ip = (Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -notmatch "Loopback" -and $_.IPAddress -notmatch "^169\.254" } | Select-Object -First 1).IPAddress
    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor Green
    Write-Host "           部署成功！" -ForegroundColor Green
    Write-Host "  ============================================" -ForegroundColor Green
    Write-Host ""
    Write-Host "  管理面板:  http://${ip}:$Port/admin" -ForegroundColor Cyan
    Write-Host "  安装目录:  $InstallDir"
    Write-Host "  日志文件:  $dataDir\app.log"
    Write-Host ""
    Write-Host "  常用命令:" -ForegroundColor Yellow
    Write-Host "    启动:  $startBat"
    Write-Host "    停止:  $stopBat"
    if ($nssm) {
        Write-Host "    服务:  nssm start/stop/restart openmodelpool"
    } else {
        Write-Host "    任务:  Start/Stop-ScheduledTask -TaskName OpenModelPool"
    }
    Write-Host "    日志:  Get-Content $dataDir\app.log -Tail 50 -Wait"
    Write-Host ""
    Write-Host "  首次使用请访问管理面板设置管理员账号" -ForegroundColor Yellow
    Write-Host ""
} else {
    Write-Host "[错误] 服务启动失败" -ForegroundColor Red
    Write-Host "  查看日志: Get-Content $dataDir\app.log -Tail 50"
    exit 1
}


# ============================================================
# 外网穿透配置（可选）
# ============================================================
Write-Host "  是否配置外网穿透？" -ForegroundColor Cyan
Write-Host "    1) Cloudflare Tunnel — 免费，固定域名+HTTPS" -ForegroundColor Green
Write-Host "    2) FRP — 免费，固定IP+端口" -ForegroundColor Green
Write-Host "    3) 跳过（稍后可单独配置）"
$tunnelChoice = Read-Host "  请选择 [1/2/3]"

if ($tunnelChoice -eq "1" -or $tunnelChoice -eq "2") {
    Write-Host ""
    Write-Host "  正在下载穿透配置脚本..." -ForegroundColor Yellow
    $tunnelUrl = "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-tunnel.ps1"
    $tunnelScript = (Invoke-WebRequest -Uri $tunnelUrl -UseBasicParsing).Content
    $tunnelScript | Out-File -FilePath (Join-Path $env:TEMP "omp-tunnel.ps1") -Encoding UTF8
    & (Join-Path $env:TEMP "omp-tunnel.ps1") -InstallDir $InstallDir -LocalPort $Port
} else {
    Write-Host "  跳过外网穿透配置。后续可运行:" -ForegroundColor Yellow
    Write-Host "    irm https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-tunnel.ps1 | iex" -ForegroundColor Cyan
}
Write-Host ""

Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
