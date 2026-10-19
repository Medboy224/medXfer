# Copies the core's libraries, built by scripts/build-lib.ps1, where the Flutter app takes them
# (WP-B.1 of the J6b plan): app/native/windows for the Windows runner (CMake), and
# app/native/android/<ABI> for Gradle (jniLibs). Linux: scripts/stage-app-native.sh.
# Usage (PowerShell, from the repository root):
#   scripts\build-lib.ps1; scripts\stage-app-native.ps1
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$lib = Join-Path $root "build\lib"
$native = Join-Path $root "app\native"

$copies = @(
    @{ From = "windows-amd64\medxfer.dll";   To = "windows\medxfer.dll" },
    @{ From = "android-arm64\libmedxfer.so"; To = "android\arm64-v8a\libmedxfer.so" },
    @{ From = "android-x86_64\libmedxfer.so"; To = "android\x86_64\libmedxfer.so" }
)
foreach ($c in $copies) {
    $src = Join-Path $lib $c.From
    if (-not (Test-Path $src)) {
        Write-Warning "missing $src (scripts\build-lib.ps1 builds it)"
        continue
    }
    $dst = Join-Path $native $c.To
    New-Item -ItemType Directory -Force (Split-Path -Parent $dst) | Out-Null
    Copy-Item -Force $src $dst
    "{0,-40} {1,8:N1} Mo" -f $c.To, ((Get-Item $dst).Length / 1MB)
}
