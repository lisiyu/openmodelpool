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

# 端口范围校验
if ($Port -lt 1 -or $Port -gt 65535) {
    Write-Host "[错误] 无效端口号 (1-65535): $Port" -ForegroundColor Red
    exit 1
}

# 规范化安装目录
if (Test-Path $InstallDir) {
    $InstallDir = (Resolve-Path $InstallDir).Path
}

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$GITHUB_REPO = "lisiyu/openmodelpool"
# 动态获取最新 Release tag（可通过环境变量 OMP_RELEASE_TAG 覆盖）
$RELEASE_TAG = $env:OMP_RELEASE_TAG
if (-not $RELEASE_TAG) {
    try {
        $releaseInfo = Invoke-RestMethod -Uri "https://api.github.com/repos/$GITHUB_REPO/releases/latest" -UseBasicParsing
        $RELEASE_TAG = $releaseInfo.tag_name
    } catch {
        Write-Host "[警告] 无法获取最新 Release，使用默认版本" -ForegroundColor Yellow
        $RELEASE_TAG = "v4.5.0"
    }
}
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

# [1.5/5] SHA256 完整性校验（fail-closed，仅从 GitHub 官方直连获取校验和）
Write-Host "[1.5/5] SHA256 完整性校验..." -ForegroundColor Cyan
$sha256Url = "https://github.com/$GITHUB_REPO/releases/download/$RELEASE_TAG/$PKG.sha256"
$sha256File = Join-Path $env:TEMP "omp-deploy.sha256"
try {
    Invoke-WebRequest -Uri $sha256Url -OutFile $sha256File -UseBasicParsing -TimeoutSec 30
} catch {
    Write-Host "[错误] 无法获取 SHA256 校验和（fail-closed），已中止" -ForegroundColor Red
    Write-Host "       请检查网络或设置环境变量 OMP_ALLOW_UNSIGNED=1 跳过（不推荐）"
    Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
    exit 1
}

if (-not (Test-Path $sha256File) -or (Get-Item $sha256File).Length -eq 0) {
    Write-Host "[错误] SHA256 校验文件为空，已中止" -ForegroundColor Red
    Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
    exit 1
}

# 解析期望 hash
$expectedHash = (Get-Content $sha256File -Raw).Trim().Split()[0].ToLower()
$actualHash = (Get-FileHash -Path $tmpZip -Algorithm SHA256).Hash.ToLower()

if ($expectedHash -ne $actualHash) {
    Write-Host "[错误] SHA256 校验失败，二进制可能被篡改，已中止" -ForegroundColor Red
    Write-Host "       期望: $expectedHash"
    Write-Host "       实际: $actualHash"
    Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
    Remove-Item $sha256File -Force -ErrorAction SilentlyContinue
    exit 1
}
Write-Host "      SHA256 校验通过" -ForegroundColor Green
Remove-Item $sha256File -Force -ErrorAction SilentlyContinue

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

# 统一使用计划任务管理（与 omp-manager.ps1 主脚本一致），通过 start.bat 启动确保 PORT 环境变量正确
$action = New-ScheduledTaskAction -Execute "cmd.exe" -Argument "/c `"$startBat`"" -WorkingDirectory $InstallDir
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
Register-ScheduledTask -TaskName "OpenModelPool" -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
Write-Host "      服务方式: 计划任务 (开机自启)" -ForegroundColor Green

# [5/5] 启动
Write-Host "[5/5] 启动服务..." -ForegroundColor Cyan

# 启动计划任务
Start-ScheduledTask -TaskName "OpenModelPool"
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
    Write-Host "    任务:  Start/Stop-ScheduledTask -TaskName OpenModelPool"
    Write-Host "    日志:  Get-Content $dataDir\app.log -Tail 50 -Wait"
    Write-Host ""
    Write-Host "  首次使用请访问管理面板设置管理员账号" -ForegroundColor Yellow
    Write-Host ""
} else {
    Write-Host "[错误] 服务启动失败" -ForegroundColor Red
    Write-Host "  查看日志: Get-Content $dataDir\app.log -Tail 50"
    exit 1
}

Remove-Item $tmpZip -Force -ErrorAction SilentlyContinue
Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
