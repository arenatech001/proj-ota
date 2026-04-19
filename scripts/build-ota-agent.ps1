# 编译 ota-agent 并打包：ota-agent 二进制 + agent.service + init-wifi.sh
# 用法：.\scripts\build-ota-agent.ps1 [-GoOs linux] [-GoArch amd64]
# 示例：.\scripts\build-ota-agent.ps1 -GoOs linux -GoArch arm64

param(
    [string]$GoOs = "linux",
    [string]$GoArch = "arm64",
    [string]$Version = "v2.0"
)

$ErrorActionPreference = "Stop"

$ScriptDir = $PSScriptRoot
if (-not $ScriptDir) { $ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
$RepoRoot = Split-Path -Parent $ScriptDir
$AgentDir = Join-Path $RepoRoot "ota-agent"
$OutDir = Join-Path $RepoRoot "dist"
$StageDir = Join-Path $OutDir "ota-agent-package"

$BinaryName = "ota-agent"
if ($GoOs -eq "windows") { $BinaryName = "ota-agent.exe" }

Write-Host "=========================================="
Write-Host "  OTA-Agent 编译与打包"
Write-Host "  GOOS=$GoOs GOARCH=$GoArch"
Write-Host "=========================================="

# 1. 编译
Write-Host "[1/4] 编译 ota-agent..."
$env:CGO_ENABLED = "0"
$env:GOOS = $GoOs
$env:GOARCH = $GoArch
Push-Location $AgentDir
try {
    go build -ldflags="-s -w" -o $BinaryName .
    Write-Host "      已生成: $AgentDir\$BinaryName"
} finally {
    Pop-Location
}

# 2. 准备打包目录
Write-Host "[2/4] 准备打包文件..."
if (Test-Path $StageDir) { Remove-Item $StageDir -Recurse -Force }
New-Item -ItemType Directory -Path $StageDir -Force | Out-Null

Copy-Item (Join-Path $AgentDir $BinaryName) -Destination $StageDir
Copy-Item (Join-Path $RepoRoot "scripts\$Version\ota-server.service") -Destination $StageDir
Copy-Item (Join-Path $RepoRoot "scripts\$Version\ota-client.service") -Destination $StageDir
Copy-Item (Join-Path $RepoRoot "scripts\init-wifi.sh") -Destination $StageDir
Copy-Item (Join-Path $RepoRoot "scripts\deploy-ota-client.sh") -Destination $StageDir
Copy-Item (Join-Path $RepoRoot "scripts\deploy-ota-server.sh") -Destination $StageDir

# 3. 打压缩包
$Timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$ArchiveName = "ota-agent-$GoOs-$GoArch-$Timestamp.tar.gz"
$ArchivePath = Join-Path $OutDir $ArchiveName
Write-Host "[3/4] 打包: $ArchiveName ..."
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }

Push-Location $OutDir
try {
    tar -czf $ArchiveName -C $StageDir $BinaryName  init-wifi.sh ota-server.service ota-client.service deploy-ota-server.sh deploy-ota-client.sh
} finally {
    Pop-Location
}
$ArchivePath = Join-Path $OutDir $ArchiveName

# 4. 清理临时目录
Write-Host "[4/4] 清理临时目录..."
Remove-Item $StageDir -Recurse -Force -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "完成。压缩包: $ArchivePath"
Write-Host '内含: ota-agent, ota-server.service, ota-client.service, init-wifi.sh, deploy-ota-server.sh, deploy-ota-client.sh'
