# ============================================================
#  OpenModelPool 外网穿透配置 (Windows)
#  支持 Cloudflare Tunnel 和 FRP 两种方案
#
#  用法 (管理员 PowerShell):
#    irm https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-tunnel.ps1 | iex
# ============================================================
param(
    [string]$InstallDir = "C:\openmodelpool",
    [int]$LocalPort = 8000
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$C = "Cyan"; $Y = "Yellow"; $G = "Green"; $R = "Red"

Write-Host ""
Write-Host "  ============================================" -ForegroundColor $C
Write-Host "   OpenModelPool 外网穿透配置向导 (Windows)" -ForegroundColor $C
Write-Host "  ============================================" -ForegroundColor $C
Write-Host ""
Write-Host "  请选择穿透方案："
Write-Host "    1) Cloudflare Tunnel  — 完全免费，固定域名+HTTPS，需自有域名" -ForegroundColor $G
Write-Host "    2) FRP              — 免费，固定IP+端口，需公网服务器" -ForegroundColor $G
Write-Host "    3) 跳过"
Write-Host ""
$choice = Read-Host "  请输入选项 [1/2/3]"

# ============================================================
# Cloudflare Tunnel
# ============================================================
function Setup-Cloudflare {
    Write-Host ""
    Write-Host "[Cloudflare Tunnel]" -ForegroundColor $Y
    Write-Host "  需要准备："
    Write-Host "    - 一个托管在 Cloudflare 的域名"
    Write-Host "    - Cloudflare 账号（免费注册）"
    Write-Host ""

    # 1. Install cloudflared
    Write-Host "[1/5] 安装 cloudflared..." -ForegroundColor $Y
    $cfDir = "$env:ProgramFiles\cloudflared"
    $cfExe = "$cfDir\cloudflared.exe"
    
    if (-not (Test-Path $cfExe)) {
        New-Item -ItemType Directory -Path $cfDir -Force | Out-Null
        $cfUrl = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"
        Invoke-WebRequest -Uri $cfUrl -OutFile $cfExe -UseBasicParsing
        
        # Add to PATH
        $currentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
        if ($currentPath -notlike "*$cfDir*") {
            [Environment]::SetEnvironmentVariable("Path", "$currentPath;$cfDir", "Machine")
            $env:Path += ";$cfDir"
        }
        Write-Host "  cloudflared 安装完成" -ForegroundColor $G
    } else {
        Write-Host "  cloudflared 已安装" -ForegroundColor $G
    }

    # 2. Login
    Write-Host ""
    Write-Host "[2/5] 登录 Cloudflare..." -ForegroundColor $Y
    Write-Host "  即将打开浏览器授权，请在浏览器中选择你的域名并授权"
    & $cfExe tunnel login
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  登录失败，请稍后手动执行: cloudflared tunnel login" -ForegroundColor $R
        return
    }

    # 3. Create tunnel
    Write-Host ""
    Write-Host "[3/5] 创建隧道..." -ForegroundColor $Y
    $createOutput = & $cfExe tunnel create openmodelpool 2>&1
    $tunnelId = ""
    if ($createOutput -match "([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})") {
        $tunnelId = $Matches[1]
    }
    if (-not $tunnelId) {
        Write-Host "  隧道创建失败: $createOutput" -ForegroundColor $R
        return
    }
    Write-Host "  隧道已创建: $tunnelId" -ForegroundColor $G

    # 4. Bind domain
    Write-Host ""
    Write-Host "[4/5] 绑定域名..." -ForegroundColor $Y
    Write-Host "  请输入要绑定的子域名（例如: omp.yourdomain.com）:"
    $subdomain = Read-Host "  > "
    
    & $cfExe tunnel route dns openmodelpool $subdomain 2>$null
    Write-Host "  域名已绑定: $subdomain" -ForegroundColor $G

    # 5. Config and service
    Write-Host ""
    Write-Host "[5/5] 配置并启动服务..." -ForegroundColor $Y
    $configDir = "$env:USERPROFILE\.cloudflared"
    if (-not (Test-Path $configDir)) { New-Item -ItemType Directory -Path $configDir -Force | Out-Null }
    
    $credFile = Get-ChildItem "$configDir\*.json" | Where-Object { $_.Name -like "$tunnelId*" } | Select-Object -First 1
    $credPath = if ($credFile) { $credFile.FullName } else { "$configDir\$tunnelId.json" }
    
    @"
tunnel: $tunnelId
credentials-file: $credPath

ingress:
  - hostname: $subdomain
    service: http://localhost:$LocalPort
  - service: http_status:404
"@ | Set-Content "$configDir\config.yml" -Encoding UTF8

    # Install as Windows service
    & $cfExe service install 2>$null
    if ($LASTEXITCODE -eq 0) {
        Write-Host "  已安装为 Windows 服务并启动" -ForegroundColor $G
    } else {
        Write-Host "  服务安装失败，使用计划任务替代..." -ForegroundColor $Y
        $action = New-ScheduledTaskAction -Execute $cfExe -Argument "tunnel run openmodelpool"
        $trigger = New-ScheduledTaskTrigger -AtStartup
        $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
        Register-ScheduledTask -TaskName "CloudflaredTunnel" -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
        Start-ScheduledTask -TaskName "CloudflaredTunnel"
        Write-Host "  已设置计划任务并启动" -ForegroundColor $G
    }

    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor $G
    Write-Host "   Cloudflare Tunnel 配置完成！" -ForegroundColor $G
    Write-Host "  ============================================" -ForegroundColor $G
    Write-Host "  外网地址: https://$subdomain" -ForegroundColor $G
    Write-Host "  管理面板: https://$subdomain/admin" -ForegroundColor $G
    Write-Host "  已设置开机自启" -ForegroundColor $G
    Write-Host ""
}

# ============================================================
# FRP
# ============================================================
function Setup-FRP {
    Write-Host ""
    Write-Host "[FRP 内网穿透]" -ForegroundColor $Y
    Write-Host ""

    $frpServer = Read-Host "  FRP 服务器地址 [默认: YOUR_FRP_SERVER_IP]"
    if (-not $frpServer) { $frpServer = "YOUR_FRP_SERVER_IP" }

    $frpToken = Read-Host "  FRP 认证 Token [默认: 使用内置]"
    if (-not $frpToken) { $frpToken = "YOUR_FRP_TOKEN" }

    $remotePortStr = Read-Host "  远程映射端口 [默认: 8001]"
    if (-not $remotePortStr) { $remotePort = 8001 } else { $remotePort = [int]$remotePortStr }

    $nodeName = ($env:COMPUTERNAME).ToLower() -replace '[^a-z0-9-]', ''

    # 1. Install frpc
    Write-Host ""
    Write-Host "[1/4] 安装 frpc..." -ForegroundColor $Y
    $frpDir = "$InstallDir\frp"
    $frpExe = "$frpDir\frpc.exe"
    
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
        Write-Host "  frpc 安装完成" -ForegroundColor $G
    } else {
        Write-Host "  frpc 已安装" -ForegroundColor $G
    }

    # 2. Config
    Write-Host ""
    Write-Host "[2/4] 创建配置..." -ForegroundColor $Y
    $configContent = @"
serverAddr = "$frpServer"
serverPort = 7000
auth.token = "$frpToken"

[[proxies]]
name = "omp-$nodeName"
type = "tcp"
localIP = "127.0.0.1"
localPort = $LocalPort
remotePort = $remotePort
"@
    $configContent | Set-Content "$frpDir\frpc.toml" -Encoding UTF8
    Write-Host "  配置已写入 $frpDir\frpc.toml" -ForegroundColor $G

    # 3. Test
    Write-Host ""
    Write-Host "[3/4] 测试连接..." -ForegroundColor $Y
    $proc = Start-Process -FilePath $frpExe -ArgumentList "-c", "$frpDir\frpc.toml" -PassThru -NoNewWindow
    Start-Sleep -Seconds 3
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Host "  连接测试完成" -ForegroundColor $G

    # 4. Service
    Write-Host ""
    Write-Host "[4/4] 设置开机自启..." -ForegroundColor $Y
    $action = New-ScheduledTaskAction -Execute $frpExe -Argument "-c `"$frpDir\frpc.toml`"" -WorkingDirectory $frpDir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName "OpenModelPoolFRP" -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
    Start-ScheduledTask -TaskName "OpenModelPoolFRP"
    Write-Host "  已设置计划任务并启动" -ForegroundColor $G

    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor $G
    Write-Host "   FRP 穿透配置完成！" -ForegroundColor $G
    Write-Host "  ============================================" -ForegroundColor $G
    Write-Host "  外网地址: http://$frpServer`:$remotePort" -ForegroundColor $G
    Write-Host "  管理面板: http://$frpServer`:$remotePort/admin" -ForegroundColor $G
    Write-Host "  已设置开机自启" -ForegroundColor $G
    Write-Host ""
}

# ============================================================
# Main
# ============================================================
switch ($choice) {
    "1" { Setup-Cloudflare }
    "2" { Setup-FRP }
    "3" { Write-Host "  跳过外网穿透配置。后续可随时运行此脚本配置。" -ForegroundColor $Y }
    default { Write-Host "  无效选项" -ForegroundColor $R; exit 1 }
}
