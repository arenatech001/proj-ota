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

$BinaryName = "agent-arm64.bin"
if ($GoOs -eq "windows") { $BinaryName = "agent-windows.exe" }

Write-Host "=========================================="
Write-Host "  OTA-Agent 编译与打包"
Write-Host "  GOOS=$GoOs GOARCH=$GoArch"
Write-Host "=========================================="

# 1. 编译
Write-Host "编译 agent..."
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