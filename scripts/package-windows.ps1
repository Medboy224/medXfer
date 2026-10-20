# Builds medXfer's Windows package (WP-B.6 of the J6b plan): a portable folder, zipped, with
# the core (medxfer.dll) and the Visual C++ runtime next to the program. Nothing to install,
# no administrator rights; the firewall rules are in docs/guide-utilisation-app.md until the
# installer of J8.
# Usage (PowerShell, from the repository root): scripts\package-windows.ps1 [-OutDir dir]
# Prerequisites: those of scripts\build-lib.ps1 (Go, llvm-mingw), Flutter, Visual Studio's C++
# tools.
param(
    [string]$OutDir = ""
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (-not $OutDir) { $OutDir = Join-Path $root "dist" }
$version = ((Select-String -Path (Join-Path $root "app\pubspec.yaml") -Pattern '^version:\s*([^+\s]+)').Matches[0].Groups[1].Value)

& (Join-Path $root "scripts\build-lib.ps1") -Targets windows
& (Join-Path $root "scripts\stage-app-native.ps1")

Push-Location (Join-Path $root "app")
try {
    flutter build windows --release
    if ($LASTEXITCODE -ne 0) { throw "flutter build windows failed" }
} finally { Pop-Location }

$release = Join-Path $root "app\build\windows\x64\runner\Release"
# What a Windows without development tools lacks (G2A-08): the core and the C++ runtime.
foreach ($f in "medxfer.exe", "medxfer.dll", "flutter_windows.dll", "msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll") {
    if (-not (Test-Path (Join-Path $release $f))) { throw "missing in the package: $f" }
}

$name = "medxfer-$version-windows-x64"
$stage = Join-Path $OutDir $name
$zip = "$stage.zip"
New-Item -ItemType Directory -Force $OutDir | Out-Null
Remove-Item -Recurse -Force $stage, $zip -ErrorAction SilentlyContinue
Copy-Item -Recurse $release $stage
Compress-Archive -Path $stage -DestinationPath $zip
Remove-Item -Recurse -Force $stage

$hash = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
"{0}  {1:N1} Mo" -f $zip, ((Get-Item $zip).Length / 1MB)
"SHA-256 $hash"
