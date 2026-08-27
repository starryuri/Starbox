# Build STARBOX: native app -> stage installer payload -> build installer + uninstaller.
# Usage:  powershell -ExecutionPolicy Bypass -File build.ps1
$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$go = Join-Path $root ".tools\gosdk\go\bin\go.exe"
if (-not (Test-Path $go)) { $go = "go" }   # fallback to PATH go
$env:GOPROXY = "off"

Write-Host "== 1/3 build native app (star.exe, GUI no console) =="
& $go build -ldflags "-H=windowsgui" -o (Join-Path $root "star.exe") .\cmd\star
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== 2/3 stage installer payload + build uninstaller =="
Copy-Item (Join-Path $root "star.exe") (Join-Path $root "cmd\setup\payload\starbox.exe") -Force
& $go build -ldflags "-H=windowsgui" -o (Join-Path $root "cmd\setup\payload\unins.exe") .\cmd\unin
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== 3/3 build installer (setup.exe) =="
& $go build -ldflags "-H=windowsgui" -o (Join-Path $root "setup.exe") .\cmd\setup
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "OK -> star.exe, setup.exe"
