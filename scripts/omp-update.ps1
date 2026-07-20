# ============================================================
#  OpenModelPool 增量更新 (Windows)
#  仅替换二进制，保留配置和数据，一行命令搞定
#  
#  用法 (管理员 PowerShell):
#    irm https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-update.ps1 | iex
# ============================================================
param([string]$InstallDir = "C:\openmodelpool")

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$GITHUB_REPO = "lisiyu/openmodelpool"
$XRAY_VERSION = "v25.7.16"

# Dynamic release tag
$RELEASE_TAG = $env:OMP_RELEASE_TAG
if (-not $RELEASE_TAG) {
    try {
        $releaseInfo = Invoke-RestMethod -Uri "https://api.github.com/repos/$GITHUB_REPO/releases/latest" -UseBasicParsing
        $RELEASE_TAG = $releaseInfo.tag_name
    } catch {
        $RELEASE_TAG = "v4.0.5"
    }
}
Write-Host "  OpenModelPool 增量更新 (目标版本: $RELEASE_TAG)" -ForegroundColor Cyan

# 1. 停止进程
Write-Host "[1/3] 停止服务..." -ForegroundColor Yellow
Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2

# 2. 下载（动态匹配资产，兼容 .exe 和 .zip）
Write-Host "[2/3] 下载新版本..." -ForegroundColor Yellow
$tmpDir = Join-Path $env:TEMP "omp-update-$(Get-Random)"
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

$assetName = ""; $assetUrl = ""
try {
    $apiUrl = "https://api.github.com/repos/$GITHUB_REPO/releases/tags/$RELEASE_TAG"
    $release = Invoke-RestMethod -Uri $apiUrl -UseBasicParsing
    $bestBin = $null; $bestArc = $null
    foreach ($asset in $release.assets) {
        $n = $asset.name.ToLower()
        if ($n -match "sha256|checksum|\.txt") { continue }
        if ($n -match "windows" -and $n -match "amd64") {
            if ($n -match "\.zip$") { if (-not $bestArc) { $bestArc = $asset } }
            else { if (-not $bestBin) { $bestBin = $asset } }
        }
    }
    $selected = if ($bestBin) { $bestBin } else { $bestArc }
    if ($selected) { $assetName = $selected.name; $assetUrl = $selected.browser_download_url }
} catch {}
if (-not $assetUrl) {
    $assetName = "openmodelpool-windows-amd64.exe"
    $assetUrl = "https://github.com/$GITHUB_REPO/releases/download/$RELEASE_TAG/$assetName"
}

$tmpFile = Join-Path $tmpDir $assetName
Invoke-WebRequest -Uri $assetUrl -OutFile $tmpFile -UseBasicParsing

# 解压（如果是 .zip）
$ompExe = $tmpFile
if ($assetName -match "\.zip$") {
    $extractDir = Join-Path $tmpDir "extracted"
    Expand-Archive -Path $tmpFile -DestinationPath $extractDir -Force
    $exeFile = Get-ChildItem -Path $extractDir -Filter "openmodelpool*.exe" -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($exeFile) { $ompExe = $exeFile.FullName } else { Write-Host "  [错误] 解压后未找到 exe" -ForegroundColor Red; exit 1 }
}

# 3. 替换二进制
Write-Host "[3/3] 替换二进制..." -ForegroundColor Yellow
Copy-Item $ompExe -Destination (Join-Path $InstallDir "openmodelpool.exe") -Force
Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue

# 4. 安装/更新 Xray (VMess 代理支持)
Write-Host "  检查 Xray..." -ForegroundColor DarkGray
$xrayDir = Join-Path $InstallDir "xray"
$xrayExe = Join-Path $xrayDir "xray.exe"
$needXray = $true
if ((Test-Path $xrayExe) -and $args[0] -ne "--with-xray") { $needXray = $false }
if ($needXray) {
    New-Item -ItemType Directory -Path $xrayDir -Force | Out-Null
    $xrayUrl = "https://github.com/XTLS/Xray-core/releases/download/$XRAY_VERSION/Xray-windows-64.zip"
    Write-Host "  下载 Xray..."
    try {
        $xrayTmp = Join-Path $env:TEMP "xray-update.zip"
        Invoke-WebRequest -Uri $xrayUrl -OutFile $xrayTmp -UseBasicParsing
        $xrayExtract = Join-Path $env:TEMP "xray-update-extract"
        if (Test-Path $xrayExtract) { Remove-Item $xrayExtract -Recurse -Force }
        Expand-Archive -Path $xrayTmp -DestinationPath $xrayExtract -Force
        Copy-Item (Join-Path $xrayExtract "xray.exe") -Destination $xrayExe -Force
        Copy-Item (Join-Path $xrayExtract "geoip.dat") -Destination $xrayDir -Force -ErrorAction SilentlyContinue
        Copy-Item (Join-Path $xrayExtract "geosite.dat") -Destination $xrayDir -Force -ErrorAction SilentlyContinue
        Remove-Item $xrayTmp -Force -ErrorAction SilentlyContinue
        Write-Host "  Xray 已安装" -ForegroundColor Green
    } catch {
        Write-Host "  Xray 下载失败，VMess 代理不可用（不影响其他功能）" -ForegroundColor Yellow
    }
} else {
    Write-Host "  Xray 已存在，跳过" -ForegroundColor Green
}

# 5. 重启
Write-Host "  启动服务..." -ForegroundColor Yellow
$svc = Get-Service -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($svc) {
    & nssm start openmodelpool 2>$null
} else {
    $task = Get-ScheduledTask -TaskName "OpenModelPool" -ErrorAction SilentlyContinue
    if ($task) {
        Start-ScheduledTask -TaskName "OpenModelPool"
    } else {
        Start-Process -FilePath (Join-Path $InstallDir "openmodelpool.exe") -WorkingDirectory $InstallDir -WindowStyle Hidden
    }
}
Start-Sleep -Seconds 3

$proc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($proc) {
    Write-Host "  更新成功！数据已保留。" -ForegroundColor Green
} else {
    Write-Host "  更新失败，请检查日志: $InstallDir\data\app.log" -ForegroundColor Red
}

# Cleanup done
