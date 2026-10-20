# Builds medXfer's Android package (WP-B.6 of the J6b plan): an APK for arm64 phones, signed
# with the development key of scripts\android-dev-key.ps1. An APK signed with the debug key is
# refused: Android would not accept the next one, signed otherwise, as its update.
# Usage (PowerShell, from the repository root): scripts\package-android.ps1 [-OutDir dir]
# Prerequisites: those of scripts\build-lib.ps1 (Go, the NDK), Flutter, the Android SDK.
param(
    [string]$OutDir = ""
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (-not $OutDir) { $OutDir = Join-Path $root "dist" }
$version = ((Select-String -Path (Join-Path $root "app\pubspec.yaml") -Pattern '^version:\s*([^+\s]+)').Matches[0].Groups[1].Value)

if (-not (Test-Path (Join-Path $root "app\android\key.properties"))) {
    throw "no app\android\key.properties: create the development key first (scripts\android-dev-key.ps1)"
}

& (Join-Path $root "scripts\build-lib.ps1") -Targets android-arm64
& (Join-Path $root "scripts\stage-app-native.ps1")

Push-Location (Join-Path $root "app")
try {
    flutter build apk --release --target-platform android-arm64
    if ($LASTEXITCODE -ne 0) { throw "flutter build apk failed" }
} finally { Pop-Location }
$apk = Join-Path $root "app\build\app\outputs\flutter-apk\app-release.apk"

# The signature: verified, and not the debug key's.
$sdk = Join-Path $env:LOCALAPPDATA "Android\Sdk"
$tools = Get-ChildItem (Join-Path $sdk "build-tools") -Directory | Sort-Object { [version]$_.Name } -Descending | Select-Object -First 1
$apksigner = Join-Path $tools.FullName "apksigner.bat"
$certs = & $apksigner verify --print-certs $apk
if ($LASTEXITCODE -ne 0) { throw "the APK's signature does not verify" }
if ($certs -match "CN=Android Debug") { throw "the APK is signed with the debug key" }

$out = Join-Path $OutDir "medxfer-$version-android-arm64.apk"
New-Item -ItemType Directory -Force $OutDir | Out-Null
Copy-Item -Force $apk $out
$hash = (Get-FileHash -Algorithm SHA256 $out).Hash.ToLower()
"{0}  {1:N1} Mo" -f $out, ((Get-Item $out).Length / 1MB)
"SHA-256 $hash"
$certs | Select-String "Signer #1 certificate (DN|SHA-256)"
