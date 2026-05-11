# 生成 ota-agent 可自动解压的压缩包：gzip + tar（.tar.gz / .tgz）
# 要求 Windows 10+ 自带 tar（或 Git Bash 的 tar）。
#
# 用法:
#   .\scripts\make-ota-archive.ps1 -SourceDir .\my-payload
#   .\scripts\make-ota-archive.ps1 -SourceDir .\my-payload -OutFile .\dist\bundle.tar.gz
#   $env:PRINT_SHA256='1'; .\scripts\make-ota-archive.ps1 -SourceDir .\my-payload

param(
    [Parameter(Mandatory = $true)]
    [string]$SourceDir,
    [string]$OutFile = ""
)

$ErrorActionPreference = "Stop"

$Resolved = Resolve-Path -LiteralPath $SourceDir -ErrorAction Stop
if (-not (Test-Path $Resolved -PathType Container)) {
    throw "不是目录: $SourceDir"
}

if (-not $OutFile) {
    $OutFile = Join-Path (Get-Location) ("$(Split-Path $Resolved -Leaf).tar.gz")
}
$OutDir = Split-Path -Parent $OutFile
if ($OutDir -and -not (Test-Path $OutDir)) {
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
}

$OutAbs = [System.IO.Path]::GetFullPath($OutFile)

# 整个目录内容打进包根（与 bash 版 tar -C SRC . 一致）
& tar -czf $OutAbs -C $Resolved.Path .

Write-Host "已生成: $OutAbs"

if ($env:PRINT_SHA256 -eq "1") {
    $hash = (Get-FileHash -LiteralPath $OutAbs -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Host "SHA256=$hash"
}
