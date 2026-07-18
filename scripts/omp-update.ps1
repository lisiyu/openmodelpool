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

$RELEASE_TAG = "v3.2.1-release"
$URL = "https://github.com/lisiyu/openmodelpool/releases/download/$RELEASE_TAG/openmodelpool-windows-amd64.zip"

Write-Host "  OpenModelPool 增量更新" -ForegroundColor Cyan

# 1. 停止进程
Write-Host "[1/4] 停止服务..." -ForegroundColor Yellow
Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2

# 2. 下载
Write-Host "[2/4] 下载新版本..." -ForegroundColor Yellow
$tmp = Join-Path $env:TEMP "omp-update.zip"
Invoke-WebRequest -Uri $URL -OutFile $tmp -UseBasicParsing
$tmpDir = Join-Path $env:TEMP "omp-update-extract"
if (Test-Path $tmpDir) { Remove-Item $tmpDir -Recurse -Force }
Expand-Archive -Path $tmp -DestinationPath $tmpDir -Force

# 3. 替换二进制
Write-Host "[3/4] 替换二进制..." -ForegroundColor Yellow
Copy-Item (Join-Path $tmpDir "openmodelpool.exe") -Destination (Join-Path $InstallDir "openmodelpool.exe") -Force

# 4. 重启
Write-Host "[4/4] 启动服务..." -ForegroundColor Yellow
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

Remove-Item $tmp -Force -ErrorAction SilentlyContinue
Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
