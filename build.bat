@echo off
setlocal enabledelayedexpansion

echo ==================================================
echo             medXfer Multi-Platform Builder        
echo ==================================================

if not exist bin mkdir bin

set CGO_ENABLED=0

echo.
echo [*] [1/3] Building Windows (x64): bin\xfer.exe ...
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w" -o bin\xfer.exe .\cmd\xfer
if %ERRORLEVEL% equ 0 (
    echo [+] Windows build OK: bin\xfer.exe
) else (
    echo [-] Windows build FAILED!
    goto :error
)

echo.
echo [*] [2/3] Building Android / Termux (ARM64 PIE): bin\xfer-android-arm64 ...
set GOOS=android
set GOARCH=arm64
go build -ldflags="-s -w" -o bin\xfer-android-arm64 .\cmd\xfer
if %ERRORLEVEL% equ 0 (
    echo [+] Android/Termux ARM64 build OK: bin\xfer-android-arm64
) else (
    echo [-] Android/Termux ARM64 build FAILED!
    goto :error
)

echo.
echo [*] [3/3] Building Linux PC (x86_64): bin\xfer-linux-amd64 ...
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o bin\xfer-linux-amd64 .\cmd\xfer
if %ERRORLEVEL% equ 0 (
    echo [+] Linux x86_64 build OK: bin\xfer-linux-amd64
) else (
    echo [-] Linux x86_64 build FAILED!
    goto :error
)

rem Reset env
set GOOS=
set GOARCH=

echo.
echo ==================================================
echo [+] All builds completed successfully!
echo --------------------------------------------------
dir bin\xfer*
echo ==================================================
exit /b 0

:error
set GOOS=
set GOARCH=
echo.
echo [-] Build failed. Please check Go compiler output above.
exit /b 1
)