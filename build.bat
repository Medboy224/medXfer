@echo off
echo [*] Building medXfer for Windows (x64)...

if not exist bin mkdir bin

set CGO_ENABLED=0
go build -ldflags="-s -w" -o bin\xfer.exe .\cmd\medXfer

if %ERRORLEVEL% equ 0 (
    echo [+] Build successful!
    echo [+] Executable created: bin\xfer.exe
) else (
    echo [-] Build failed.
)