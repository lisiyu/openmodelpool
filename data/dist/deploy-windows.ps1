# OpenModelPool 一键部署脚本 - Windows
# 使用方法: 右键 -> 使用 PowerShell 运行
# 或在 PowerShell 中: .\deploy-windows.ps1 [-InstallDir "C:\openmodelpool"] [-Port 8000]

param(
    [string]$InstallDir = "C:\openmodelpool",
    [int]$Port = 8000
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

Write-Host "=============================="
Write-Host " OpenModelPool 一键部署"
Write-Host " 平台: Windows x86_64"
Write-Host " 安装目录: $InstallDir"
Write-Host " 端口: $Port"
Write-Host "=============================="

# 检查管理员权限
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Write-Host "[错误] 请使用管理员权限运行 PowerShell" -ForegroundColor Red
    exit 1
}

# 创建目录
$dataDir = Join-Path $InstallDir "data"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null

# 复制文件
Write-Host "[1/3] 复制程序文件..."
$exeSrc = Join-Path $ScriptDir "openmodelpool-windows-amd64.exe"
if (-not (Test-Path $exeSrc)) {
    $exeSrc = Join-Path $ScriptDir "openmodelpool.exe"
}
if (-not (Test-Path $exeSrc)) {
    Write-Host "[错误] 找不到可执行文件" -ForegroundColor Red
    exit 1
}
Copy-Item $exeSrc -Destination (Join-Path $InstallDir "openmodelpool.exe") -Force

$htmlSrc = Join-Path $ScriptDir "admin.html"
if (Test-Path $htmlSrc) {
    Copy-Item $htmlSrc -Destination $InstallDir -Force
}

$docsSrc = Join-Path $ScriptDir "docs"
if (Test-Path $docsSrc) {
    Copy-Item $docsSrc -Destination $InstallDir -Force -Recurse
}

# 设置端口
Write-Host "[2/3] 配置端口 ($Port)..."
$startBat = Join-Path $InstallDir "start.bat"
@"
@echo off
cd /d "$InstallDir"
set PORT=$Port
openmodelpool.exe >> "$dataDir\app.log" 2>&1
"@ | Set-Content $startBat -Encoding ASCII

# 配置计划任务开机自启
Write-Host "[3/3] 配置服务..."
# 使用计划任务开机自启（通过 start.bat 启动以确保 PORT 环境变量正确传递）
$action = New-ScheduledTaskAction -Execute "cmd.exe" -Argument "/c `"$startBat`"" -WorkingDirectory $InstallDir
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
Register-ScheduledTask -TaskName "OpenModelPool" -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
Start-ScheduledTask -TaskName "OpenModelPool"
Write-Host "已注册为开机启动计划任务" -ForegroundColor Green

Start-Sleep -Seconds 2

$ip = (Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -notmatch "Loopback" -and $_.IPAddress -notmatch "^169\.254" } | Select-Object -First 1).IPAddress

Write-Host ""
Write-Host "✅ 部署成功！" -ForegroundColor Green
Write-Host "=============================="
Write-Host " 管理面板: http://${ip}:$Port/admin"
Write-Host " 安装目录: $InstallDir"
Write-Host " 日志文件: $dataDir\app.log"
Write-Host " 手动启动: 运行 $startBat"
Write-Host " 停止: Stop-ScheduledTask -TaskName OpenModelPool"
Write-Host " 启动: Start-ScheduledTask -TaskName OpenModelPool"
Write-Host "=============================="
Write-Host ""
Write-Host "⚠️  首次使用请访问管理面板设置管理员账号" -ForegroundColor Yellow
