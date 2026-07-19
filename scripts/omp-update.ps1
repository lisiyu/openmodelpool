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

Write-Host ""
Write-Host "  ============================================" -ForegroundColor Cyan
Write-Host "   OpenModelPool 增量更新 (Windows)" -ForegroundColor Cyan
Write-Host "  ============================================" -ForegroundColor Cyan
Write-Host ""

# 1. Get latest release tag from GitHub API
Write-Host "[1/6] 获取最新版本..." -ForegroundColor Yellow
$apiUrl = "https://api.github.com/repos/lisiyu/openmodelpool/releases/latest"
try {
    $release = Invoke-RestMethod -Uri $apiUrl -UseBasicParsing
    $RELEASE_TAG = $release.tag_name
    Write-Host "  最新版本: $RELEASE_TAG" -ForegroundColor Green
} catch {
    # Fallback to env var or hardcoded
    $RELEASE_TAG = $env:OMP_RELEASE_TAG
    if (-not $RELEASE_TAG) { $RELEASE_TAG = "v4.0.2" }
    Write-Host "  使用版本: $RELEASE_TAG (API 获取失败，使用默认)" -ForegroundColor Yellow
}

# 2. Stop process
Write-Host "[2/6] 停止服务..." -ForegroundColor Yellow
Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2

# 3. Download binary + SHA256
Write-Host "[3/6] 下载新版本..." -ForegroundColor Yellow
$binName = "openmodelpool-windows-amd64.exe"
$baseUrl = "https://github.com/lisiyu/openmodelpool/releases/download/$RELEASE_TAG"
$binUrl = "$baseUrl/$binName"
$shaUrl = "$baseUrl/$binName.sha256"

$tmpExe = Join-Path $env:TEMP "omp-update.exe"
$tmpSha = Join-Path $env:TEMP "omp-update.exe.sha256"

Invoke-WebRequest -Uri $binUrl -OutFile $tmpExe -UseBasicParsing
try {
    Invoke-WebRequest -Uri $shaUrl -OutFile $tmpSha -UseBasicParsing
} catch {
    Write-Host "  SHA256 文件下载失败，跳过校验" -ForegroundColor Yellow
}

# 4. Verify SHA256
Write-Host "[4/6] 校验完整性..." -ForegroundColor Yellow
if (Test-Path $tmpSha) {
    $expectedSha = (Get-Content $tmpSha -Raw).Trim().Split(' ')[0]
    $actualSha = (Get-FileHash $tmpExe -Algorithm SHA256).Hash.ToLower()
    if ($expectedSha.ToLower() -ne $actualSha) {
        Write-Host "  SHA256 校验失败！" -ForegroundColor Red
        Write-Host "  期望: $expectedSha" -ForegroundColor Red
        Write-Host "  实际: $actualSha" -ForegroundColor Red
        exit 1
    }
    Write-Host "  SHA256 校验通过" -ForegroundColor Green
} else {
    Write-Host "  跳过校验（无 SHA256 文件）" -ForegroundColor Yellow
}

# 5. Replace binary
Write-Host "[5/6] 替换二进制..." -ForegroundColor Yellow
$targetExe = Join-Path $InstallDir "openmodelpool.exe"
Copy-Item $tmpExe -Destination $targetExe -Force
Write-Host "  二进制已替换" -ForegroundColor Green

# 6. Restart
Write-Host "[6/6] 启动服务..." -ForegroundColor Yellow
$svc = Get-Service -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($svc) {
    & nssm start openmodelpool 2>$null
} else {
    $task = Get-ScheduledTask -TaskName "OpenModelPool" -ErrorAction SilentlyContinue
    if ($task) {
        Start-ScheduledTask -TaskName "OpenModelPool"
    } else {
        Start-Process -FilePath $targetExe -WorkingDirectory $InstallDir -WindowStyle Hidden
    }
}
Start-Sleep -Seconds 3

$proc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($proc) {
    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor Green
    Write-Host "   更新成功！数据已保留。" -ForegroundColor Green
    Write-Host "  ============================================" -ForegroundColor Green
} else {
    Write-Host "  更新失败，请检查日志: $InstallDir\data\app.log" -ForegroundColor Red
}

# Cleanup
Remove-Item $tmpExe -Force -ErrorAction SilentlyContinue
Remove-Item $tmpSha -Force -ErrorAction SilentlyContinue
