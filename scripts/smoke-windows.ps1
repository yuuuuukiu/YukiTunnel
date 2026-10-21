# End-to-end smoke test for the Windows binaries (run inside Windows).
#
#   powershell -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1
#
# Requires: dist\windows\*.exe plus Go for the local echo
# service (pass -GoExe C:\path\to\go.exe when Go is not on PATH). Uses
# curl.exe (bundled with Windows 10+) so self-signed certificates work without
# touching the machine's TLS policy.
param(
    [string]$BinDir = 'dist\windows',
    [string]$Arch = 'amd64',
    [string]$GoExe = 'go'
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

$server = Join-Path $BinDir "yuki-server-windows-$Arch.exe"
$client = Join-Path $BinDir "yuki-windows-$Arch.exe"
if (-not (Test-Path $server) -or -not (Test-Path $client)) {
    throw "找不到 $BinDir 下的 $Arch 二进制，请先运行 scripts\build-windows.ps1"
}

$tmp = Join-Path $env:TEMP ("yukitunnel-smoke-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
$env:YUKI_CLIENT_HOME = Join-Path $tmp 'client'
$apiPort = 18443
$ctlPort = 19443
$pubPort = 28001
$localPort = 18080
$api = "https://127.0.0.1:$apiPort"
$failures = @()

function Say($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Check($name, $cond, $detail = '') {
    if ($cond) { Write-Host "    OK   $name" -ForegroundColor Green }
    else { Write-Host "    FAIL $name $detail" -ForegroundColor Red; $script:failures += $name }
}
function Health { (curl.exe -sk --max-time 5 "$api/api/v1/health") 2>$null }

try {
    Say "init"
    & $server init --data-dir (Join-Path $tmp 'data') --api-addr "127.0.0.1:$apiPort" `
        --control-addr "127.0.0.1:$ctlPort" --admin-password admin12345 `
        --port-min $pubPort --port-max $pubPort --public-host 127.0.0.1 *> $null
    Check 'init' ($LASTEXITCODE -eq 0)

    Say "start"
    Start-Process -FilePath $server -ArgumentList @('serve','--data-dir',(Join-Path $tmp 'data')) `
        -RedirectStandardOutput (Join-Path $tmp 'server.log') `
        -RedirectStandardError (Join-Path $tmp 'server.err') -WindowStyle Hidden | Out-Null
    $healthy = $false
    for ($i = 0; $i -lt 40; $i++) { Start-Sleep -Milliseconds 500; if (Health) { $healthy = $true; break } }
    Check 'health' $healthy

    Say "create user + login"
    & $server user create alice --password alice12345 --data-dir (Join-Path $tmp 'data') *> $null
    Check 'user create' ($LASTEXITCODE -eq 0)
    & $client login --server $api --username alice --password alice12345 --insecure *> $null
    Check 'login' ($LASTEXITCODE -eq 0)
    $servers = & $client server list --json 2>$null | Out-String | ConvertFrom-Json
    Check 'server list' ($servers.servers.Count -ge 1)

    Say "local service"
    # A tiny Go echo server avoids here-string interpolation pitfalls and needs
    # nothing beyond the Go toolchain used to build the binaries.
    $goSrc = Join-Path $tmp 'echo.go'
    @'
package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "hello-from-windows")
	})
	_ = http.ListenAndServe("127.0.0.1:18080", nil)
}
'@ | Set-Content -Path $goSrc -Encoding UTF8
    Push-Location $tmp
    & $GoExe mod init echosrv *> $null
    & $GoExe build -o (Join-Path $tmp 'echo.exe') $goSrc
    $built = ($LASTEXITCODE -eq 0)
    Pop-Location
    Check 'echo service built' $built
    Start-Process -FilePath (Join-Path $tmp 'echo.exe') -WindowStyle Hidden | Out-Null
    Start-Sleep -Seconds 2
    Check 'local service' ((curl.exe -s --max-time 5 "http://127.0.0.1:$localPort/") -like 'hello*')

    Say "tunnel"
    $created = & $client tunnel create --proto tcp --local-ip 127.0.0.1 --local-port $localPort --no-domain --json 2>$null | Out-String | ConvertFrom-Json
    Check 'tunnel create' ($null -ne $created.tunnel.id)
    Start-Process -FilePath $client -ArgumentList @('run') `
        -RedirectStandardOutput (Join-Path $tmp 'agent.out') `
        -RedirectStandardError (Join-Path $tmp 'agent.err') -WindowStyle Hidden | Out-Null
    Start-Sleep -Seconds 5
    & $client tunnel start $created.tunnel.id *> $null
    Start-Sleep -Seconds 2
    $body = curl.exe -s --max-time 8 "http://127.0.0.1:$pubPort/"
    Check 'traffic through tunnel' ($body -like 'hello*') "got: $body"

    Say "stop"
    & $client tunnel stop $created.tunnel.id *> $null
    Start-Sleep -Seconds 2
    $code = curl.exe -s --max-time 3 -o NUL -w '%{http_code}' "http://127.0.0.1:$pubPort/" 2>$null
    Check 'port closed after stop' ($code -eq '000') "got: $code"

    Say "delete"
    & $client tunnel delete $created.tunnel.id -y *> $null
    $list = & $client tunnel list --json 2>$null | Out-String | ConvertFrom-Json
    Check 'tunnel deleted' (-not ($list.tunnels.id -contains $created.tunnel.id))

    Say "server CLI"
    & $server domain binding on --suffix smoke.local --data-dir (Join-Path $tmp 'data') *> $null
    Check 'domain on' ($LASTEXITCODE -eq 0)
    & $server doctor --data-dir (Join-Path $tmp 'data') *> $null
    Check 'doctor' ($LASTEXITCODE -eq 0)
}
finally {
    Say "cleanup"
    Get-Process | Where-Object { $_.ProcessName -like 'yuki-*' } | Stop-Process -Force -ErrorAction SilentlyContinue
    Get-Process | Where-Object { $_.ProcessName -like 'powershell*' -and $_.Id -ne $PID } |
        Where-Object { $_.StartTime -gt (Get-Date).AddMinutes(-5) } |
        Stop-Process -Force -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host ''
if ($failures.Count -eq 0) {
    Write-Host 'All Windows smoke tests passed.' -ForegroundColor Green
    exit 0
}
Write-Host ("Failures: " + ($failures -join ', ')) -ForegroundColor Red
exit 1
