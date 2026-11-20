param(
    [string]$OutputDirectory = (Split-Path $PSScriptRoot),
    [string]$Go = 'go.exe',
    [string]$MakeNSIS = 'makensis.exe'
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$source = Join-Path $root 'src'
$versionFile = Get-Content -LiteralPath (Join-Path $source 'main.go') -Raw
if ($versionFile -notmatch '(?m)^const version = "([0-9]+\.[0-9]+\.[0-9]+)"') {
    throw 'Eine stabile Spherelay-Version fehlt in src/main.go.'
}
$version = $Matches[1]
$output = (New-Item -ItemType Directory -Path $OutputDirectory -Force).FullName
$stage = Join-Path ([IO.Path]::GetTempPath()) ('spherelay-package-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $stage
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'Installer.ps1'),(Join-Path $PSScriptRoot 'NSIS-COPYING.txt') -Destination $stage
Copy-Item -LiteralPath (Join-Path $source 'web\logo.ico') -Destination (Join-Path $stage 'Spherelay.ico')
$art = Join-Path $PSScriptRoot 'art'
$binary = Join-Path $stage 'Spherelay.exe'
$installer = Join-Path $output "Spherelay-$version-Windows-x64-Setup.exe"
$executable = Join-Path $output "Spherelay-$version-Windows-x64.exe"
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
Push-Location -LiteralPath $source
try {
    & $Go test -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go-Tests fehlgeschlagen.' }
    & $Go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go-Vet-Prüfung fehlgeschlagen.' }
    & $Go build -trimpath -ldflags '-H=windowsgui' -o $binary .
    if ($LASTEXITCODE -ne 0) { throw 'Windows-Build fehlgeschlagen.' }
    & $MakeNSIS /INPUTCHARSET UTF8 /V3 "/DVERSION=$version" "/DOUTPUT=$installer" "/DPAYLOAD=$stage" "/DART=$art" (Join-Path $PSScriptRoot 'setup.nsi')
    if ($LASTEXITCODE -ne 0) { throw 'Installer-Build fehlgeschlagen.' }
    Copy-Item -LiteralPath $binary -Destination $executable -Force
} finally {
    Pop-Location
}
Get-FileHash -LiteralPath $executable,$installer -Algorithm SHA256
