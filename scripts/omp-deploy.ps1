# ============================================================
#  OpenModelPool 一键部署脚本 (Windows)
#  自动从 GitHub 下载对应架构的二进制文件
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
$RELEASE_TAG = "v3.2.0-release"
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

# [1/5] 下载
Write-Host "[1/5] 下载: $PKG" -ForegroundColor Cyan
Write-Host "      $DOWNLOAD_URL"
$tmpZip = Join-Path $env:TEMP "omp-deploy.zip"
try {
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

# 复制所有文件
Copy-Item (Join-Path $tmpDir "openmodelpool.exe") -Destination (Join-Path $InstallDir "openmodelpool.exe") -Force

# 复制所有 HTML 文件
foreach ($html in @("admin.html", "setup.html", "login.html")) {
    $src = Join-Path $tmpDir $html
    if (Test-Path $src) {
        Copy-Item $src -Destination $InstallDir -Force
    }
}

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
set OMP_PORT=$Port
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
    & nssm set openmodelpool AppEnvironmentExtra "OMP_PORT=$Port"
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
Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 1

if ($nssm) {
    & nssm start openmodelpool
} else {
    Start-Process -FilePath (Join-Path $InstallDir "openmodelpool.exe") -WorkingDirectory $InstallDir -WindowStyle Hidden
}
Start-Sleep -Seconds 3

$proc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($proc) {
    $ip = (Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -notmatch "Loopback" -and $_.IPAddress -notmatch "^169\.254" } | Select-Object -First 1).IPAddress
    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor Green
    Write-Host "           ✅ 部署成功！" -ForegroundColor Green
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
    Write-Host "  ⚠️  首次使用请访问管理面板设置管理员账号" -ForegroundColor Yellow
    Write-Host ""
} else {
    Write-Host "[错误] 服务启动失败" -ForegroundColor Red
    Write-Host "  查看日志: Get-Content $dataDir\app.log -Tail 50"
    exit 1
}

Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
