# ModelMux v3.0 Windows 一键安装脚本
# 功能：安装 modelmux 为 Windows 服务 + 配置 Cloudflare Tunnel
# 用法：以管理员身份运行 PowerShell，执行此脚本

param(
    [string]$Port = "8000",
    [string]$DataDir = "C:\ProgramData\ModelMux",
    [bool]$InstallTunnel = $true,
    [string]$TunnelName = ""
)

$ErrorActionPreference = "Stop"
$ServiceName = "ModelMux"
$InstallDir = "$env:ProgramFiles\ModelMux"
$NSSMUrl = "https://github.com/kirillkovalenko/nssm/releases/download/2.24/nssm-2.24.zip"
$CloudflaredUrl = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.msi"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  ModelMux v3.0 一键安装" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# ============================================================
# 1. 检查管理员权限
# ============================================================
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Write-Host "[ERROR] 请以管理员身份运行此脚本！" -ForegroundColor Red
    exit 1
}

# ============================================================
# 2. 创建目录
# ============================================================
Write-Host "[1/6] 创建安装目录..." -ForegroundColor Yellow
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Write-Host "  安装目录: $InstallDir"
Write-Host "  数据目录: $DataDir"

# ============================================================
# 3. 复制 modelmux.exe（假设在同一目录）
# ============================================================
Write-Host "[2/6] 安装 ModelMux 服务..." -ForegroundColor Yellow
$exePath = Join-Path $PSScriptRoot "modelmux.exe"
if (-not (Test-Path $exePath)) {
    Write-Host "[ERROR] 未找到 modelmux.exe，请确保它与此脚本在同一目录" -ForegroundColor Red
    exit 1
}
Copy-Item $exePath "$InstallDir\modelmux.exe" -Force
Write-Host "  已复制 modelmux.exe -> $InstallDir"

# ============================================================
# 4. 安装 NSSM（Windows 服务管理器）
# ============================================================
Write-Host "[3/6] 安装 NSSM 服务管理器..." -ForegroundColor Yellow
$nssmPath = "$InstallDir\nssm.exe"
if (-not (Test-Path $nssmPath)) {
    $nssmZip = "$env:TEMP\nssm.zip"
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Invoke-WebRequest -Uri $NSSMUrl -OutFile $nssmZip
    Expand-Archive -Path $nssmZip -DestinationPath "$env:TEMP\nssm" -Force
    # Find nssm.exe in the extracted directory (win64 subfolder)
    $nssmExe = Get-ChildItem -Path "$env:TEMP\nssm" -Recurse -Filter "nssm.exe" | Where-Object { $_.DirectoryName -like "*win64*" } | Select-Object -First 1
    if (-not $nssmExe) {
        $nssmExe = Get-ChildItem -Path "$env:TEMP\nssm" -Recurse -Filter "nssm.exe" | Select-Object -First 1
    }
    if ($nssmExe) {
        Copy-Item $nssmExe.FullName $nssmPath -Force
    } else {
        Write-Host "[WARN] NSSM 下载失败，使用 sc.exe 注册服务" -ForegroundColor Yellow
    }
    Remove-Item $nssmZip -ErrorAction SilentlyContinue
    Remove-Item "$env:TEMP\nssm" -Recurse -ErrorAction SilentlyContinue
}

# ============================================================
# 5. 注册 Windows 服务
# ============================================================
Write-Host "[4/6] 注册 Windows 服务..." -ForegroundColor Yellow

# Stop existing service if running
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc) {
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
    if (Test-Path $nssmPath) {
        & $nssmPath stop $ServiceName 2>$null
        & $nssmPath remove $ServiceName confirm 2>$null
    } else {
        sc.exe delete $ServiceName 2>$null
    }
    Start-Sleep -Seconds 1
}

if (Test-Path $nssmPath) {
    # Use NSSM for better service management
    & $nssmPath install $ServiceName "$InstallDir\modelmux.exe"
    & $nssmPath set $ServiceName AppDirectory $DataDir
    & $nssmPath set $ServiceName DisplayName "ModelMux v3.0 - AI Provider Gateway"
    & $nssmPath set $ServiceName Description "去中心化 AI 模型网关联邦网络节点"
    & $nssmPath set $ServiceName Start SERVICE_AUTO_START
    & $nssmPath set $ServiceName AppStdout "$DataDir\service.log"
    & $nssmPath set $ServiceName AppStderr "$DataDir\service-error.log"
    & $nssmPath set $ServiceName AppRotateFiles 1
    & $nssmPath set $ServiceName AppRotateBytes 1048576
    # Set environment variable for port
    & $nssmPath set $ServiceName AppEnvironmentExtra "service_port=$Port"
    Write-Host "  使用 NSSM 注册服务完成"
} else {
    # Fallback to sc.exe
    sc.exe create $ServiceName binPath= "`"$InstallDir\modelmux.exe`"" start= auto DisplayName= "ModelMux v3.0"
    sc.exe description $ServiceName "去中心化 AI 模型网关联邦网络节点"
    Write-Host "  使用 sc.exe 注册服务完成"
}

# Start the service
Start-Service -Name $ServiceName
Write-Host "  服务已启动，监听端口: $Port" -ForegroundColor Green

# ============================================================
# 6. 安装 Cloudflare Tunnel (可选)
# ============================================================
if ($InstallTunnel) {
    Write-Host "[5/6] 安装 Cloudflare Tunnel..." -ForegroundColor Yellow
    
    # Check if cloudflared is already installed
    $cfPath = Get-Command cloudflared -ErrorAction SilentlyContinue
    if (-not $cfPath) {
        # Download and install cloudflared MSI
        $msiPath = "$env:TEMP\cloudflared.msi"
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        Invoke-WebRequest -Uri $CloudflaredUrl -OutFile $msiPath
        Start-Process msiexec.exe -ArgumentList "/i `"$msiPath`" /quiet /norestart" -Wait
        Remove-Item $msiPath -ErrorAction SilentlyContinue
        
        # Refresh PATH
        $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User")
        $cfPath = Get-Command cloudflared -ErrorAction SilentlyContinue
    }
    
    if ($cfPath) {
        Write-Host "  Cloudflare Tunnel 已安装" -ForegroundColor Green
        
        if ($TunnelName) {
            # Named tunnel mode (requires Cloudflare account)
            Write-Host "  配置命名隧道: $TunnelName"
            Write-Host "  请先运行: cloudflared tunnel login"
            Write-Host "  然后运行: cloudflared tunnel create $TunnelName"
        } else {
            # Quick Tunnel mode (no account needed, random subdomain)
            Write-Host ""
            Write-Host "  正在启动 Quick Tunnel（无需账号，自动生成公网地址）..." -ForegroundColor Cyan
            
            # Create a batch script to run cloudflared as a background service
            $tunnelBat = "$InstallDir\cloudflared-tunnel.bat"
            @"
@echo off
title ModelMux Cloudflare Tunnel
:loop
echo [%date% %time%] Starting Cloudflare Tunnel...
cloudflared tunnel --url http://localhost:$Port --no-autoupdate 2>&1 >> "$DataDir\tunnel.log"
echo [%date% %time%] Tunnel disconnected, restarting in 5s...
timeout /t 5 /nobreak >nul
goto loop
"@ | Out-File -FilePath $tunnelBat -Encoding ASCII

            # Register tunnel as a separate service
            if (Test-Path $nssmPath) {
                & $nssmPath install "ModelMux-Tunnel" "cmd.exe" "/c `"$tunnelBat`""
                & $nssmPath set "ModelMux-Tunnel" AppDirectory $InstallDir
                & $nssmPath set "ModelMux-Tunnel" DisplayName "ModelMux Cloudflare Tunnel"
                & $nssmPath set "ModelMux-Tunnel" Start SERVICE_AUTO_START
                & $nssmPath start "ModelMux-Tunnel"
                Start-Sleep -Seconds 5
                
                # Try to get the tunnel URL from log
                $tunnelLog = "$DataDir\tunnel.log"
                if (Test-Path $tunnelLog) {
                    $urlLine = Select-String -Path $tunnelLog -Pattern "trycloudflare.com" | Select-Object -Last 1
                    if ($urlLine) {
                        $tunnelUrl = [regex]::Match($urlLine.Line, 'https://[a-zA-Z0-9-]+\.trycloudflare\.com').Value
                        if ($tunnelUrl) {
                            Write-Host ""
                            Write-Host "  ========================================" -ForegroundColor Green
                            Write-Host "  🌐 公网访问地址: $tunnelUrl" -ForegroundColor Green
                            Write-Host "  ========================================" -ForegroundColor Green
                        }
                    }
                }
            } else {
                # Start tunnel in background without NSSM
                Start-Process cmd.exe -ArgumentList "/c `"$tunnelBat`"" -WindowStyle Hidden
                Write-Host "  Quick Tunnel 已启动（后台运行）"
            }
        }
    } else {
        Write-Host "[WARN] Cloudflare Tunnel 安装失败，跳过内网穿透配置" -ForegroundColor Yellow
    }
} else {
    Write-Host "[5/6] 跳过 Cloudflare Tunnel 安装" -ForegroundColor Yellow
}

# ============================================================
# 7. 防火墙规则
# ============================================================
Write-Host "[6/6] 配置防火墙..." -ForegroundColor Yellow
$ruleName = "ModelMux HTTP ($Port)"
$existing = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
if (-not $existing) {
    New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Port $Port -Protocol TCP -Action Allow | Out-Null
    Write-Host "  已添加入站规则: 端口 $Port"
} else {
    Write-Host "  防火墙规则已存在"
}

# ============================================================
# 完成
# ============================================================
Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host "  ✅ 安装完成！" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green
Write-Host ""
Write-Host "  本地访问: http://localhost:$Port"
Write-Host "  数据目录: $DataDir"
Write-Host "  日志文件: $DataDir\service.log"
Write-Host ""
Write-Host "  管理命令:" -ForegroundColor Cyan
Write-Host "    启动服务: Start-Service ModelMux"
Write-Host "    停止服务: Stop-Service ModelMux"
Write-Host "    查看状态: Get-Service ModelMux"
Write-Host "    查看日志: Get-Content $DataDir\service.log -Tail 50"
Write-Host ""
Write-Host "  健康检查:" -ForegroundColor Cyan
Write-Host "    curl http://localhost:$Port/health"
Write-Host ""

# Verify service is running
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc.Status -eq "Running") {
    Write-Host "  ✅ 服务运行中" -ForegroundColor Green
} else {
    Write-Host "  ⚠️ 服务状态: $($svc.Status)" -ForegroundColor Yellow
}
