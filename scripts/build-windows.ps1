# Build YukiTunnel for Windows from a Windows host.
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -Arch 386
#
# Produces dist\windows\*.exe for the requested architectures. The WebUI is
# embedded from internal\server\webdist, which scripts\build.sh (or the release
# workflow) fills in; when it is missing the server still builds and serves a
# JSON hint instead of the UI.
param(
    [ValidateSet('amd64', '386', 'arm64', 'all')]
    [string]$Arch = 'all',
    [string]$Version = '0.1.0',
    # Absolute path to go.exe when Go is not on PATH (common on fresh Windows
    # machines where the installer's PATH change has not been picked up yet).
    [string]$GoExe = 'go'
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

$ldflags = "-s -w -X github.com/yukitunnel/yukitunnel/internal/version.Version=$Version"
$targets = if ($Arch -eq 'all') { @('amd64', '386', 'arm64') } else { @($Arch) }

New-Item -ItemType Directory -Force -Path 'dist\windows' | Out-Null

if (-not (Get-Command $GoExe -ErrorAction SilentlyContinue)) {
    throw "找不到 Go 编译器：$GoExe（安装 Go 或使用 -GoExe C:\path\to\go.exe）"
}
Write-Host "Using $(& $GoExe version)"

foreach ($a in $targets) {
    $env:GOOS = 'windows'
    $env:GOARCH = $a
    $env:CGO_ENABLED = '0'
    $suffix = if ($a -eq 'amd64') { 'amd64' } else { $a }

    Write-Host "==> windows/$a (server)"
    & $GoExe build -trimpath -ldflags $ldflags -o "dist\windows\yuki-server-windows-$suffix.exe" ./cmd/yuki-server
    if ($LASTEXITCODE -ne 0) { throw "server build failed for $a" }

    Write-Host "==> windows/$a (client)"
    & $GoExe build -trimpath -ldflags $ldflags -o "dist\windows\yuki-windows-$suffix.exe" ./cmd/yuki
    if ($LASTEXITCODE -ne 0) { throw "client build failed for $a" }
}

Write-Host ''
Write-Host 'Done:'
Get-ChildItem 'dist\windows\*.exe' | Select-Object Name, @{N='MB';E={[math]::Round($_.Length/1MB,1)}}
