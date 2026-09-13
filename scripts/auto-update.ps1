# ============================================================
#  OpenModelPool 自动更新脚本 (Windows)
#  检查 GitHub 最新 Release，如有新版本则自动下载部署
#
#  用法:
#    手动执行: powershell -ExecutionPolicy Bypass -File auto-update.ps1
#    计划任务: 注册为 Windows 计划任务定期执行
#
#  安全说明:
#    - 通过公开 /api/version 端点获取当前版本，无需登录
#    - 下载后进行 SHA256 校验，校验失败不替换现有二进制
#    - 替换前自动备份旧版本
# ============================================================
param(
    [string]$InstallDir = "C:\openmodelpool",
    [int]$Port = 8000
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$GITHUB_REPO = "lisiyu/openmodelpool"
$exeName = "openmodelpool.exe"
$exePath = Join-Path $InstallDir $exeName
$logFile = Join-Path $InstallDir "data\auto-update.log"
$dataDir = Join-Path $InstallDir "data"

# 确保日志目录存在
if (-not (Test-Path $dataDir)) { New-Item -ItemType Directory -Force -Path $dataDir | Out-Null }

function Write-Log {
    param([string]$msg)
    $ts = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    $line = "[$ts] $msg"
    Add-Content -Path $logFile -Value $line -ErrorAction SilentlyContinue
    Write-Host $line
}

# 归一化版本号：去掉前缀 v 与后缀 -release / 预发布段
function Normalize-Version {
    param([string]$v)
    $v = $v -replace '^v', ''
    $v = $v -replace '-release$', ''
    $v = $v -replace '-.*$', ''
    $v = $v -replace '\+.*$', ''
    return $v
}

Write-Log "==== OpenModelPool 自动更新检查 ===="

# 获取当前版本
$CURRENT_VERSION = ""
try {
    $resp = Invoke-RestMethod -Uri "http://localhost:$Port/api/version" -UseBasicParsing -TimeoutSec 5
    $CURRENT_VERSION = $resp.version
} catch {
    Write-Log "⚠️ 无法获取当前版本（服务可能未运行），继续检查..."
}

# 获取 GitHub 最新 Release tag
$LATEST_TAG = $env:OMP_RELEASE_TAG
if (-not $LATEST_TAG) {
    try {
        $releaseInfo = Invoke-RestMethod -Uri "https://api.github.com/repos/$GITHUB_REPO/releases/latest" -UseBasicParsing
        $LATEST_TAG = $releaseInfo.tag_name
    } catch {
        Write-Log "❌ 无法获取最新 Release tag"
        exit 1
    }
}

if (-not $LATEST_TAG) {
    Write-Log "❌ 无法获取最新 Release tag"
    exit 1
}

Write-Log "当前版本: $CURRENT_VERSION | 最新 Release: $LATEST_TAG"

# 版本比较
$CUR_N = Normalize-Version -v $CURRENT_VERSION
$LAT_N = Normalize-Version -v $LATEST_TAG

if ($CUR_N -eq $LAT_N) {
    Write-Log "已是最新版本，跳过更新"
    exit 0
}

Write-Log "发现新版本，开始更新..."

# 动态匹配 Release 资产（兼容裸二进制 .exe 和压缩包 .zip）
$tmpDir = Join-Path $env:TEMP "omp-auto-update-$(Get-Random)"
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

$assetName = ""
$assetUrl = ""
try {
    $apiUrl = "https://api.github.com/repos/$GITHUB_REPO/releases/tags/$LATEST_TAG"
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
} catch {
    Write-Log "⚠️ API 查询失败，使用默认资产名"
}

if (-not $assetUrl) {
    $assetName = "openmodelpool-windows-amd64.exe"
    $assetUrl = "https://github.com/$GITHUB_REPO/releases/download/$LATEST_TAG/$assetName"
}

Write-Log "下载: $assetName"
$tmpFile = Join-Path $tmpDir $assetName
try {
    Invoke-WebRequest -Uri $assetUrl -OutFile $tmpFile -UseBasicParsing
} catch {
    Write-Log "❌ 下载失败: $_"
    Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    exit 1
}

# SHA256 校验
$tmpSha = Join-Path $tmpDir "$assetName.sha256"
try { Invoke-WebRequest -Uri "$assetUrl.sha256" -OutFile $tmpSha -UseBasicParsing } catch {}

if (Test-Path $tmpSha) {
    $expectedHash = (Get-Content $tmpSha -Raw).Trim().Split(' ')[0]
    $actualHash = (Get-FileHash $tmpFile -Algorithm SHA256).Hash.ToLower()
    if ($expectedHash.ToLower() -ne $actualHash) {
        Write-Log "❌ SHA256 校验失败，终止更新，现有二进制保持不变"
        Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
        exit 1
    }
    Write-Log "✅ SHA256 校验通过"
} else {
    Write-Log "⚠️ 未找到校验文件，跳过校验"
}

# 解压（如果是 .zip）
$ompExe = $tmpFile
if ($assetName -match "\.zip$") {
    $extractDir = Join-Path $tmpDir "extracted"
    Expand-Archive -Path $tmpFile -DestinationPath $extractDir -Force
    $exeFile = Get-ChildItem -Path $extractDir -Filter "openmodelpool*.exe" -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($exeFile) {
        $ompExe = $exeFile.FullName
        Write-Log "✅ 已从压缩包提取"
    } else {
        Write-Log "❌ 解压后未找到 openmodelpool.exe"
        Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
        exit 1
    }
}

# 备份当前二进制
$backupPath = Join-Path $InstallDir "$exeName.bak"
if (Test-Path $exePath) {
    Copy-Item $exePath -Destination $backupPath -Force
    Write-Log "已备份旧版本"
}

# 停止服务
Write-Log "停止服务..."
Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2

# 替换二进制
Copy-Item $ompExe -Destination $exePath -Force
Write-Log "二进制已替换"

# 启动服务
Write-Log "启动服务..."
$svc = Get-Service -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($svc) {
    & nssm start openmodelpool 2>$null
} else {
    $task = Get-ScheduledTask -TaskName "OpenModelPool" -ErrorAction SilentlyContinue
    if ($task) {
        Start-ScheduledTask -TaskName "OpenModelPool"
    } else {
        Start-Process -FilePath $exePath -WorkingDirectory $InstallDir -WindowStyle Hidden
    }
}
Start-Sleep -Seconds 3

# 验证
$proc = Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue
if ($proc) {
    Write-Log "✅ 更新成功！版本: $LATEST_TAG"
} else {
    Write-Log "❌ 更新后服务未正常启动，回滚..."
    Get-Process -Name "openmodelpool" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
    if (Test-Path $backupPath) {
        Copy-Item $backupPath -Destination $exePath -Force
        Start-Process -FilePath $exePath -WorkingDirectory $InstallDir -WindowStyle Hidden
        Write-Log "已回滚到上一版本"
    }
}

# 清理
Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
Write-Log "更新流程结束"
