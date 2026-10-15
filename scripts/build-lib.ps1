# Builds medXfer as a C library for Windows and Android, from Windows (WP-6.4 of the J6a plan;
# bible §10.4). Linux, and Android under Termux: scripts/build-lib.sh.
# Usage (PowerShell, from the repository root):
#   scripts\build-lib.ps1                      # windows, android-arm64, android-x86_64
#   scripts\build-lib.ps1 -Targets windows
#   scripts\build-lib.ps1 -Targets windows -Smoke   # also run cmd\libmedxfer\testdata\smoke.c
# Prerequisites (versions in docs/g2a-report.md): Go, llvm-mingw for Windows
# (x86_64-w64-mingw32-gcc), the Android NDK for Android.
# Output: build\lib\<target>\ (medxfer.dll or libmedxfer.so, and the header).
param(
    [string[]]$Targets = @("windows", "android-arm64", "android-x86_64"),
    [string]$LlvmMingw = "$env:USERPROFILE\dev\llvm-mingw",
    [string]$Ndk = "",
    [int]$AndroidApi = 24,
    [switch]$Smoke
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$pkg = Join-Path $root "cmd\libmedxfer"

if (-not $Ndk) {
    $ndkRoot = Join-Path $env:LOCALAPPDATA "Android\Sdk\ndk"
    if (Test-Path $ndkRoot) {
        $Ndk = (Get-ChildItem $ndkRoot -Directory | Sort-Object Name -Descending | Select-Object -First 1).FullName
    }
}

function Build-Lib([string]$target, [string]$goos, [string]$goarch, [string]$cc, [string]$name) {
    $out = Join-Path $root "build\lib\$target\$name"
    New-Item -ItemType Directory -Force (Split-Path -Parent $out) | Out-Null
    $env:CGO_ENABLED = "1"; $env:GOOS = $goos; $env:GOARCH = $goarch; $env:CC = $cc
    $t = Get-Date
    # -s -w: no symbol table (size, G2A-11); -trimpath: no local paths in the library.
    go build -trimpath -tags libmedxfer -buildmode=c-shared -ldflags="-s -w" -o $out $pkg
    if ($LASTEXITCODE -ne 0) { throw "go build failed for $target" }
    $mb = (Get-Item $out).Length / 1MB
    "{0,-16} {1,8:N1} Mo  {2,5:N1} s  {3}" -f $target, $mb, ((Get-Date) - $t).TotalSeconds, $out
    if ($mb -gt 20) { Write-Warning "$target library over 20 MB (G2A-11)" }
}

try {
    foreach ($target in $Targets) {
        switch ($target) {
            "windows" {
                $cc = Join-Path $LlvmMingw "bin\x86_64-w64-mingw32-gcc.exe"
                if (-not (Test-Path $cc)) { throw "llvm-mingw not found: $cc" }
                Build-Lib "windows-amd64" "windows" "amd64" $cc "medxfer.dll"
                if ($Smoke) {
                    $dir = Join-Path $root "build\lib\windows-amd64"
                    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
                    & $cc -O1 -DMEDXFER_WIN -o (Join-Path $dir "smoke.exe") (Join-Path $root "cmd\libmedxfer\testdata\smoke.c") "-I$dir" "-L$dir" -lmedxfer -lpthread
                    if ($LASTEXITCODE -ne 0) { throw "smoke.c did not compile" }
                    $data = Join-Path ([IO.Path]::GetTempPath()) ("medxfer-smoke-" + [guid]::NewGuid())
                    New-Item -ItemType Directory $data | Out-Null
                    $env:PATH = (Join-Path $LlvmMingw "x86_64-w64-mingw32\bin") + ";" + $env:PATH # libwinpthread
                    try { & (Join-Path $dir "smoke.exe") ($data -replace '\\', '/') } finally { Remove-Item -Recurse -Force $data }
                    if ($LASTEXITCODE -ne 0) { throw "smoke test failed" }
                }
            }
            { $_ -like "android-*" } {
                if (-not $Ndk) { throw "Android NDK not found (pass -Ndk)" }
                $abi = @{ "android-arm64" = @("arm64", "aarch64"); "android-x86_64" = @("amd64", "x86_64") }[$target]
                if (-not $abi) { throw "unknown target $target" }
                $cc = Join-Path $Ndk "toolchains\llvm\prebuilt\windows-x86_64\bin\$($abi[1])-linux-android$AndroidApi-clang.cmd"
                if (-not (Test-Path $cc)) { throw "NDK compiler not found: $cc" }
                Build-Lib $target "android" $abi[0] $cc "libmedxfer.so"
            }
            default { throw "unknown target $target" }
        }
    }
} finally {
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH, Env:CC -ErrorAction SilentlyContinue
}
