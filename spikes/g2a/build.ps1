# G2a prototype: builds the Go core as a C library for Windows and Android (plan §4.4, G2A-01).
# Usage (PowerShell, from the repository root):
#   spikes\g2a\build.ps1                 # windows + android (arm64 and x86_64)
#   spikes\g2a\build.ps1 -Targets windows
# Prerequisites (docs/medxfer-plan-prototype-g2a.md §3, versions in g2a-report.md):
#   Go 1.24+, llvm-mingw (x86_64-w64-mingw32-gcc) for Windows, Android NDK for Android.
param(
    [string[]]$Targets = @("windows", "android-arm64", "android-x86_64"),
    [string]$LlvmMingw = "$env:USERPROFILE\dev\llvm-mingw",
    [string]$Ndk = "",
    [int]$AndroidApi = 24
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$core = Join-Path $root "core"
$app = Join-Path $root "app"

if (-not $Ndk) {
    $ndkRoot = Join-Path $env:LOCALAPPDATA "Android\Sdk\ndk"
    if (Test-Path $ndkRoot) {
        $Ndk = (Get-ChildItem $ndkRoot -Directory | Sort-Object Name -Descending | Select-Object -First 1).FullName
    }
}

function Build-Core([string]$goos, [string]$goarch, [string]$cc, [string]$out) {
    New-Item -ItemType Directory -Force (Split-Path -Parent $out) | Out-Null
    $env:CGO_ENABLED = "1"; $env:GOOS = $goos; $env:GOARCH = $goarch; $env:CC = $cc
    $t = Get-Date
    # -s -w: no symbol table (size, G2A-11); -trimpath: no local paths in the binary.
    go build -trimpath -buildmode=c-shared -ldflags="-s -w" -o $out $core
    if ($LASTEXITCODE -ne 0) { throw "go build failed for $goos/$goarch" }
    Remove-Item ([IO.Path]::ChangeExtension($out, ".h")) -ErrorAction SilentlyContinue
    "{0,-16} {1,8:N1} Mo  {2,5:N1} s  {3}" -f "$goos/$goarch", ((Get-Item $out).Length / 1MB), ((Get-Date) - $t).TotalSeconds, $out
}

try {
    foreach ($target in $Targets) {
        switch ($target) {
            "windows" {
                $cc = Join-Path $LlvmMingw "bin\x86_64-w64-mingw32-gcc.exe"
                if (-not (Test-Path $cc)) { throw "llvm-mingw not found: $cc" }
                Build-Core "windows" "amd64" $cc (Join-Path $app "native\windows\medxfer.dll")
            }
            { $_ -like "android-*" } {
                if (-not $Ndk) { throw "Android NDK not found (pass -Ndk)" }
                $abi = @{ "android-arm64" = @("arm64", "aarch64", "arm64-v8a"); "android-x86_64" = @("amd64", "x86_64", "x86_64") }[$target]
                $cc = Join-Path $Ndk "toolchains\llvm\prebuilt\windows-x86_64\bin\$($abi[1])-linux-android$AndroidApi-clang.cmd"
                if (-not (Test-Path $cc)) { throw "NDK compiler not found: $cc" }
                Build-Core "android" $abi[0] $cc (Join-Path $app "android\app\src\main\jniLibs\$($abi[2])\libmedxfer.so")
            }
            default { throw "unknown target $target" }
        }
    }
} finally {
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH, Env:CC -ErrorAction SilentlyContinue
}
