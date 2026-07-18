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
Write-Host "    1) Cloudflare Tunnel — 完全免费，固定域名+HTTPS，需自有域名" -ForegroundColor $G
Write-Host "    2) FRP — 免费，固定IP+端口，需公网服务器" -ForegroundColor $G
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
    $configDir = "$env:USERPROFILE\.cloudflared"
    $certFile = "$configDir\cert.pem"
    
    if (Test-Path $certFile) {
        Write-Host "[2/5] 已检测到 Cloudflare 授权，跳过登录" -ForegroundColor $G
    } else {
        Write-Host "[2/5] 登录 Cloudflare..." -ForegroundColor $Y
        Write-Host "  即将打开浏览器授权，请在浏览器中选择你的域名并授权"
        Write-Host "  如果浏览器没有自动打开，请手动复制下方 URL 到浏览器访问：" -ForegroundColor $Y
        
        # Run login and capture the URL output
        $loginProcess = Start-Process -FilePath $cfExe -ArgumentList "tunnel", "login" -PassThru -NoNewWindow -RedirectStandardOutput "$env:TEMP\cf-login-stdout.txt" -RedirectStandardError "$env:TEMP\cf-login-stderr.txt"
        
        # Wait for login to complete (check for cert.pem)
        $timeout = 300  # 5 minutes
        $elapsed = 0
        while (-not (Test-Path $certFile) -and $elapsed -lt $timeout) {
            Start-Sleep -Seconds 2
            $elapsed += 2
            # Show the URL from stderr if available
            if ($elapsed -eq 4) {
                $errContent = Get-Content "$env:TEMP\cf-login-stderr.txt" -ErrorAction SilentlyContinue
                if ($errContent) {
                    $urlLine = $errContent | Where-Object { $_ -match "https://" } | Select-Object -First 1
                    if ($urlLine) {
                        # Extract URL
                        $url = [regex]::Match($urlLine, "https://\S+").Value
                        if ($url) {
                            Write-Host "  授权 URL: $url" -ForegroundColor $C
                            Write-Host "  请在浏览器中打开此 URL 完成授权..." -ForegroundColor $Y
                        }
                    }
                }
            }
        }
        
        if (Test-Path $certFile) {
            Write-Host "  授权成功！" -ForegroundColor $G
        } else {
            Write-Host "  登录超时，请稍后手动执行: cloudflared tunnel login" -ForegroundColor $R
            return
        }
    }

    # 3. Create tunnel (or reuse existing)
    Write-Host ""
    Write-Host "[3/5] 创建隧道..." -ForegroundColor $Y
    
    # Check if tunnel already exists
    $listOutput = & $cfExe tunnel list 2>&1 | Out-String
    $tunnelId = ""
    
    if ($listOutput -match "openmodelpool\s+([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})") {
        $tunnelId = $Matches[1]
        Write-Host "  已有 openmodelpool 隧道，复用: $tunnelId" -ForegroundColor $G
    } else {
        # Create new tunnel
        $createOutput = & $cfExe tunnel create openmodelpool 2>&1 | Out-String
        Write-Host "  创建输出: $createOutput" -ForegroundColor DarkGray
        
        if ($createOutput -match "([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})") {
            $tunnelId = $Matches[1]
        }
        
        if ($tunnelId) {
            Write-Host "  隧道已创建: $tunnelId" -ForegroundColor $G
        } else {
            Write-Host "  隧道创建失败: $createOutput" -ForegroundColor $R
            return
        }
    }

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
    if (-not (Test-Path $configDir)) { New-Item -ItemType Directory -Path $configDir -Force | Out-Null }
    
    $credFile = "$configDir\$tunnelId.json"
    
    @"
tunnel: $tunnelId
credentials-file: $credFile

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
    Write-Host "  FRP 需要一台有公网 IP 的服务器作为中转。" -ForegroundColor $Y
    Write-Host "  如果还没有，请按以下步骤搭建：" -ForegroundColor $Y
    Write-Host ""
    Write-Host "  ──────────────────────────────────────────" -ForegroundColor $C
    Write-Host "   如何搭建 FRP 服务器（在公网服务器上执行）" -ForegroundColor $C
    Write-Host "  ──────────────────────────────────────────" -ForegroundColor $C
    Write-Host ""
    Write-Host "  1. 购买一台云服务器（腾讯云/阿里云轻量级即可，约 30-50 元/月）"
    Write-Host "  2. 在公网服务器上执行以下命令：" -ForegroundColor $Y
    Write-Host ""
    Write-Host "     # 下载 FRP" -ForegroundColor $G
    Write-Host "     wget https://github.com/fatedier/frp/releases/download/v0.61.1/frp_0.61.1_linux_amd64.tar.gz"
    Write-Host "     tar xzf frp_0.61.1_linux_amd64.tar.gz && cd frp_0.61.1_linux_amd64"
    Write-Host ""
    Write-Host "     # 创建配置" -ForegroundColor $G
    Write-Host '     cat > frps.toml << EOF'
    Write-Host '     bindPort = 7000'
    Write-Host '     auth.token = "your-secret-token-here"'
    Write-Host '     EOF'
    Write-Host ""
    Write-Host "     # 启动" -ForegroundColor $G
    Write-Host "     ./frps -c frps.toml"
    Write-Host ""
    Write-Host "     # 开机自启 (systemd)" -ForegroundColor $G
    Write-Host '     sudo tee /etc/systemd/system/frps.service << EOF'
    Write-Host '     [Unit]'
    Write-Host '     Description=frps server'
    Write-Host '     After=network.target'
    Write-Host '     [Service]'
    Write-Host '     Type=simple'
    Write-Host '     ExecStart=/root/frp_0.61.1_linux_amd64/frps -c /root/frp_0.61.1_linux_amd64/frps.toml'
    Write-Host '     Restart=always'
    Write-Host '    RestartSec=5'
    Write-Host '     [Install]'
    Write-Host '     WantedBy=multi-user.target'
    Write-Host '     EOF'
    Write-Host '     sudo systemctl enable frps && sudo systemctl start frps'
    Write-Host ""
    Write-Host "     # 安全组放行端口" -ForegroundColor $G
    Write-Host "     在云服务器控制台安全组中放行: TCP 7000 + 映射端口(如 8001-8010)"
    Write-Host ""
    Write-Host "  ──────────────────────────────────────────" -ForegroundColor $C
    Write-Host ""
    Write-Host "  搭建完成后，请在下方填写你的 FRP 服务器信息：" -ForegroundColor $Y
    Write-Host ""
    
    $frpServer = Read-Host "  FRP 服务器公网 IP"
    if (-not $frpServer) {
        Write-Host "  服务器地址不能为空" -ForegroundColor $R
        return
    }

    $frpToken = Read-Host "  FRP 认证 Token"
    if (-not $frpToken) {
        Write-Host "  Token 不能为空" -ForegroundColor $R
        return
    }

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
    Write-Host "  外网地址: http://${frpServer}:$remotePort" -ForegroundColor $G
    Write-Host "  管理面板: http://${frpServer}:$remotePort/admin" -ForegroundColor $G
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
