# ModelMux Windows 一键安装脚本
# 用法: irm https://raw.githubusercontent.com/lisiyu/modelmux/main/install.ps1 | iex
# 自定义: $Port=9090; irm ... | iex

#Requires -RunAsAdministrator

param(
    [string]$Version = "latest",
    [string]$InstallDir = "C:\Program Files\ModelMux",
    [string]$DataDir = "C:\ProgramData\ModelMux",
    [int]$Port = 8000
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Repo = "lisiyu/modelmux"
$BinaryName = "modelmux-windows-amd64.exe"
$ServiceName = "ModelMux"

function Write-Info  { param($Msg) Write-Host "▸ $Msg" -ForegroundColor Cyan }
function Write-Ok    { param($Msg) Write-Host "✓ $Msg" -ForegroundColor Green }
function Write-Warn  { param($Msg) Write-Host "▹ $Msg" -ForegroundColor Yellow }
function Write-Err   { param($Msg) Write-Host "✗ $Msg" -ForegroundColor Red }
function Write-Header{ param($Msg) Write-Host "`n── $Msg ──" -ForegroundColor White }

# ─── 系统检查 ───
Write-Header "检查系统环境"

if (-not ([Environment]::Is64BitOperatingSystem)) {
    Write-Err "仅支持 64 位 Windows"
    exit 1
}

$osVersion = [System.Environment]::OSVersion.Version
Write-Ok "Windows $($osVersion.Major).$($osVersion.Minor) x64"

# ─── 确定版本 ───
Write-Header "确定版本"

if ($Version -eq "latest") {
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -TimeoutSec 10
        $Version = $release.tag_name
        Write-Ok "最新版本: $Version"
    } catch {
        Write-Warn "无法获取最新版本信息，使用 main 分支"
        $Version = "main"
    }
} else {
    Write-Info "指定版本: $Version"
}

# ─── 下载 ───
Write-Header "下载 ModelMux"

$TempDir = Join-Path $env:TEMP "modelmux-install-$(Get-Random)"
New-Item -ItemType Directory -Path $TempDir -Force | Out-Null

$DownloadUrl = if ($Version -eq "main") {
    "https://raw.githubusercontent.com/$Repo/main/$BinaryName"
} else {
    "https://github.com/$Repo/releases/download/$Version/$BinaryName"
}

$ChecksumUrl = if ($Version -eq "main") {
    "https://raw.githubusercontent.com/$Repo/main/checksums.txt"
} else {
    "https://github.com/$Repo/releases/download/$Version/checksums.txt"
}

$DestFile = Join-Path $TempDir $BinaryName

Write-Info "下载地址: $DownloadUrl"

try {
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $DestFile -TimeoutSec 60
    Write-Ok "二进制文件下载完成"
} catch {
    Write-Err "下载失败: $_"
    Write-Err "请手动下载: $DownloadUrl"
    Remove-Item -Recurse -Force $TempDir
    exit 1
}

# ─── 校验 ───
Write-Header "验证完整性"

try {
    $ChecksumContent = (Invoke-WebRequest -Uri $ChecksumUrl -TimeoutSec 10).Content
    $ExpectedHash = ($ChecksumContent -split "`n" | Where-Object { $_ -match $BinaryName } | Select-Object -First 1) -replace '^\s*([a-f0-9]+).*', '$1'

    if ($ExpectedHash) {
        $ActualHash = (Get-FileHash -Path $DestFile -Algorithm SHA256).Hash.ToLower()
        if ($ExpectedHash -eq $ActualHash) {
            Write-Ok "SHA256 校验通过"
        } else {
            Write-Err "SHA256 校验失败!"
            Write-Err "期望: $ExpectedHash"
            Write-Err "实际: $ActualHash"
            Remove-Item -Recurse -Force $TempDir
            exit 1
        }
    } else {
        Write-Warn "未在校验文件中找到对应条目，跳过校验"
    }
} catch {
    Write-Warn "无法下载校验文件，跳过校验"
}

# ─── 安装 ───
Write-Header "安装文件"

# 创建目录
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}
if (-not (Test-Path $DataDir)) {
    New-Item -ItemType Directory -Path $DataDir -Force | Out-Null
}

# 复制二进制
Copy-Item -Path $DestFile -Destination (Join-Path $InstallDir "modelmux.exe") -Force
Write-Ok "已安装到 $InstallDir\modelmux.exe"

# 添加到 PATH（如果不在）
$currentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($currentPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$InstallDir", "Machine")
    Write-Ok "已添加到系统 PATH"
}

# ─── 注册服务 ───
Write-Header "注册 Windows 服务"

$ExePath = Join-Path $InstallDir "modelmux.exe"
$ServiceArgs = "-port $Port -data `"$DataDir`""

# 检查服务是否已存在
$existingService = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existingService) {
    Write-Info "停止现有服务..."
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    sc.exe delete $ServiceName | Out-Null
    Start-Sleep -Seconds 2
}

# 创建服务
sc.exe create $ServiceName `
    binPath= "`"$ExePath`" $ServiceArgs" `
    start= auto `
    DisplayName= "ModelMux API Gateway" | Out-Null

sc.exe description $ServiceName "ModelMux - AI Model Gateway & Load Balancer" | Out-Null

# 配置服务失败恢复
sc.exe failure $ServiceName reset= 86400 actions= restart/5000/restart/10000/restart/30000 | Out-Null

# 启动服务
Start-Service -Name $ServiceName
Start-Sleep -Seconds 3

$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -eq "Running") {
    Write-Ok "服务已启动"
} else {
    Write-Warn "服务未正常启动，请检查事件查看器"
}

# ─── 创建开始菜单快捷方式 ───
Write-Header "创建快捷方式"

$StartMenuDir = Join-Path $env:ProgramData "Microsoft\Windows\Start Menu\Programs\ModelMux"
New-Item -ItemType Directory -Path $StartMenuDir -Force | Out-Null

$WshShell = New-Object -ComObject WScript.Shell

# 管理面板快捷方式
$Shortcut = $WshShell.CreateShortcut((Join-Path $StartMenuDir "ModelMux 管理面板.url"))
$Shortcut.TargetPath = "http://localhost:$Port/admin"
$Shortcut.Save()

# 服务管理快捷方式
$Shortcut = $WshShell.CreateShortcut((Join-Path $StartMenuDir "ModelMux 服务管理.lnk"))
$Shortcut.TargetPath = "services.msc"
$Shortcut.Description = "管理 ModelMux 服务"
$Shortcut.Save()

Write-Ok "开始菜单快捷方式已创建"

# ─── 防火墙 ───
Write-Header "配置防火墙"

try {
    $existingRule = Get-NetFirewallRule -DisplayName "ModelMux" -ErrorAction SilentlyContinue
    if ($existingRule) {
        Remove-NetFirewallRule -DisplayName "ModelMux"
    }
    New-NetFirewallRule -DisplayName "ModelMux" `
        -Direction Inbound -Protocol TCP `
        -LocalPort $Port -Action Allow | Out-Null
    Write-Ok "防火墙规则已添加 (TCP/$Port)"
} catch {
    Write-Warn "无法添加防火墙规则，请手动添加"
}

# ─── 清理 ───
Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue

# ─── 完成 ───
Write-Host ""
Write-Host "╔══════════════════════════════════════════╗" -ForegroundColor Green
Write-Host "║     ModelMux 安装完成！                  ║" -ForegroundColor Green
Write-Host "╚══════════════════════════════════════════╝" -ForegroundColor Green
Write-Host ""
Write-Host "  访问地址:  http://localhost:$Port" -ForegroundColor White
Write-Host "  管理面板:  http://localhost:$Port/admin" -ForegroundColor White
Write-Host "  安装路径:  $InstallDir" -ForegroundColor White
Write-Host "  数据目录:  $DataDir" -ForegroundColor White
Write-Host ""
Write-Host "  服务管理:" -ForegroundColor Cyan
Write-Host "    查看状态:  Get-Service ModelMux"
Write-Host "    重启服务:  Restart-Service ModelMux"
Write-Host "    停止服务:  Stop-Service ModelMux"
Write-Host "    查看日志:  Get-EventLog -LogName Application -Source ModelMux"
Write-Host ""
Write-Host "  卸载:" -ForegroundColor Yellow
Write-Host "    Stop-Service ModelMux; sc.exe delete ModelMux"
Write-Host "    Remove-Item -Recurse '$InstallDir'"
Write-Host ""
