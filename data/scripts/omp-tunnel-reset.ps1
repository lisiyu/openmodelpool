# ============================================================
#  OpenModelPool Cloudflare Tunnel 彻底重置脚本
#  清除所有旧配置，从零开始
# ============================================================
param(
    [int]$Port = 8000
)

$ErrorActionPreference = "Continue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$C = "Cyan"; $Y = "Yellow"; $G = "Green"; $R = "Red"
$cfDir = "$env:ProgramFiles\cloudflared"
$cfExe = "$cfDir\cloudflared.exe"
$configDir = "$env:USERPROFILE\.cloudflared"

Write-Host ""
Write-Host "  ============================================" -ForegroundColor $C
Write-Host "   Cloudflare Tunnel 彻底重置" -ForegroundColor $C
Write-Host "  ============================================" -ForegroundColor $C
Write-Host ""

# ============================================================
# Phase 1: 清理 Cloudflare 侧（删隧道、删DNS）
# ============================================================
Write-Host "[1/8] 清理 Cloudflare 隧道..." -ForegroundColor $Y

if (Test-Path $cfExe) {
    # 列出已有隧道
    $tunnels = & $cfExe tunnel list 2>&1 | Out-String
    Write-Host "  当前隧道列表:" -ForegroundColor DarkGray
    Write-Host $tunnels -ForegroundColor DarkGray
    
    # 删除 openmodelpool 隧道（会自动清理 DNS CNAME）
    $deleteOutput = & $cfExe tunnel delete openmodelpool 2>&1 | Out-String
    if ($deleteOutput -match "Deleted tunnel" -or $deleteOutput -match "deleted") {
        Write-Host "  隧道已删除" -ForegroundColor $G
    } elseif ($deleteOutput -match "not found" -or $deleteOutput -match "No tunnel") {
        Write-Host "  无已有隧道，跳过" -ForegroundColor $G
    } else {
        Write-Host "  隧道删除输出: $deleteOutput" -ForegroundColor DarkGray
        # 如果隧道有活跃连接无法删除，先清理
        Write-Host "  尝试强制清理..." -ForegroundColor $Y
    }
} else {
    Write-Host "  cloudflared 未安装，跳过" -ForegroundColor $G
}

# ============================================================
# Phase 2: 清理本地（服务、进程、计划任务、文件）
# ============================================================
Write-Host ""
Write-Host "[2/8] 停止并删除 Windows 服务..." -ForegroundColor $Y

# 强制停止服务
try { Stop-Service cloudflared -Force -ErrorAction SilentlyContinue } catch {}
Start-Sleep -Seconds 1

# 用 sc.exe 强制删除服务
$scOutput = sc.exe delete cloudflared 2>&1
Write-Host "  $scOutput" -ForegroundColor DarkGray

# 也删除可能的其他服务名
try { sc.exe delete Cloudflared 2>&1 | Out-Null } catch {}
Start-Sleep -Seconds 2

Write-Host "  服务清理完成" -ForegroundColor $G

Write-Host ""
Write-Host "[3/8] 杀掉所有 cloudflared 进程..." -ForegroundColor $Y
Stop-Process -Name cloudflared -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2
$remaining = Get-Process -Name cloudflared -ErrorAction SilentlyContinue
if ($remaining) {
    Write-Host "  进程仍在运行，等待..." -ForegroundColor $Y
    Start-Sleep -Seconds 3
    Stop-Process -Name cloudflared -Force -ErrorAction SilentlyContinue
}
Write-Host "  进程清理完成" -ForegroundColor $G

Write-Host ""
Write-Host "[4/8] 删除计划任务..." -ForegroundColor $Y
Unregister-ScheduledTask -TaskName "CloudflaredTunnel" -Confirm:$false -ErrorAction SilentlyContinue
Write-Host "  计划任务清理完成" -ForegroundColor $G

Write-Host ""
Write-Host "[5/8] 删除所有本地配置文件..." -ForegroundColor $Y
if (Test-Path $configDir) {
    Remove-Item -Path $configDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host "  已删除 $configDir" -ForegroundColor $G
}
if (Test-Path $cfDir) {
    Remove-Item -Path $cfDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host "  已删除 $cfDir" -ForegroundColor $G
}
Write-Host "  本地文件清理完成" -ForegroundColor $G

# ============================================================
# Phase 3: 全新安装
# ============================================================
Write-Host ""
Write-Host "[6/8] 下载安装 cloudflared..." -ForegroundColor $Y
New-Item -ItemType Directory -Path $cfDir -Force | Out-Null
$cfUrl = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"
Invoke-WebRequest -Uri $cfUrl -OutFile $cfExe -UseBasicParsing

# 添加到 PATH
$currentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($currentPath -notlike "*$cfDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$cfDir", "Machine")
    $env:Path += ";$cfDir"
}
Write-Host "  cloudflared 安装完成" -ForegroundColor $G

Write-Host ""
Write-Host "[7/8] 登录 Cloudflare..." -ForegroundColor $Y
Write-Host "  请在弹出的浏览器中选择你的域名并授权" -ForegroundColor $Y
Write-Host "  如果浏览器没有自动打开，请手动复制下方 URL 到浏览器：" -ForegroundColor $Y
Write-Host ""

$certFile = "$configDir\cert.pem"
$loginProc = Start-Process -FilePath $cfExe -ArgumentList "tunnel", "login" -PassThru -NoNewWindow -RedirectStandardOutput "$env:TEMP\cf-login-stdout.txt" -RedirectStandardError "$env:TEMP\cf-login-stderr.txt"

# 等待登录完成（检测 cert.pem）
$timeout = 300
$elapsed = 0
$urlShown = $false
while (-not (Test-Path $certFile) -and $elapsed -lt $timeout) {
    Start-Sleep -Seconds 2
    $elapsed += 2
    if (-not $urlShown -and $elapsed -ge 4) {
        $errContent = Get-Content "$env:TEMP\cf-login-stderr.txt" -ErrorAction SilentlyContinue
        if ($errContent) {
            $urlLine = $errContent | Where-Object { $_ -match "https://" } | Select-Object -First 1
            if ($urlLine) {
                $url = [regex]::Match($urlLine, "https://\S+").Value
                if ($url) {
                    Write-Host "  授权 URL: $url" -ForegroundColor $C
                    $urlShown = $true
                }
            }
        }
    }
}

if (Test-Path $certFile) {
    Write-Host "  授权成功！" -ForegroundColor $G
} else {
    Write-Host "  登录超时，请稍后手动执行: cloudflared tunnel login" -ForegroundColor $R
    exit 1
}

# 创建隧道
Write-Host ""
Write-Host "[8/8] 创建隧道并配置..." -ForegroundColor $Y
$createOutput = & $cfExe tunnel create openmodelpool 2>&1 | Out-String
Write-Host "  $createOutput" -ForegroundColor DarkGray

if ($createOutput -match "([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})") {
    $tunnelId = $Matches[1]
    Write-Host "  隧道已创建: $tunnelId" -ForegroundColor $G
} else {
    Write-Host "  隧道创建失败: $createOutput" -ForegroundColor $R
    exit 1
}

# 绑定域名
Write-Host ""
$subdomain = Read-Host "  请输入要绑定的子域名（如 omp.openmodelpool.com）"

$routeOutput = & $cfExe tunnel route dns openmodelpool $subdomain 2>&1 | Out-String
if ($routeOutput -match "Added CNAME" -or $routeOutput -match "already exists") {
    Write-Host "  域名已绑定: $subdomain" -ForegroundColor $G
} else {
    Write-Host "  域名绑定输出: $routeOutput" -ForegroundColor DarkGray
}

# 创建配置文件
$credFile = "$configDir\$tunnelId.json"
@"
tunnel: $tunnelId
credentials-file: $credFile

ingress:
  - hostname: $subdomain
    service: http://localhost:$Port
  - service: http_status:404
"@ | Set-Content "$configDir\config.yml" -Encoding UTF8
Write-Host "  配置文件已创建" -ForegroundColor $G

# 设置开机自启（用计划任务，避免 Windows 服务卡死问题）
$action = New-ScheduledTaskAction -Execute $cfExe -Argument "tunnel run openmodelpool"
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName "CloudflaredTunnel" -Action $action -Trigger $trigger -Settings $settings -RunLevel Highest -Force | Out-Null
Start-ScheduledTask -TaskName "CloudflaredTunnel"
Write-Host "  已设置计划任务并启动" -ForegroundColor $G

# 等待连接
Write-Host ""
Write-Host "  等待隧道连接..." -ForegroundColor $Y
Start-Sleep -Seconds 5

# 确认进程在运行
$proc = Get-Process -Name cloudflared -ErrorAction SilentlyContinue
if ($proc) {
    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor $G
    Write-Host "   Cloudflare Tunnel 配置完成！" -ForegroundColor $G
    Write-Host "  ============================================" -ForegroundColor $G
    Write-Host "  外网地址: https://$subdomain" -ForegroundColor $G
    Write-Host "  管理面板: https://$subdomain/admin" -ForegroundColor $G
    Write-Host "  隧道ID:   $tunnelId" -ForegroundColor $G
    Write-Host "  自启方式: 计划任务 (CloudflaredTunnel)" -ForegroundColor $G
    Write-Host ""
} else {
    Write-Host "  隧道进程未检测到，请手动运行测试:" -ForegroundColor $R
    Write-Host "  & `"$cfExe`" tunnel run openmodelpool" -ForegroundColor $Y
}
