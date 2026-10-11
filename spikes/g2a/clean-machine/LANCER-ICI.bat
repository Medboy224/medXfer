@echo off
rem medXfer G2a : essai sur machine propre. Rien n'est installe ni modifie sur ce PC.
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0check-g2a.ps1"
echo.
pause
