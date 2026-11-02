@echo off
rem Builds ble-probe.exe (WP-D.0 of the J6d plan) next to this file, with Visual Studio's C++
rem tools, the ones Flutter's Windows build uses. Out of the repository: build\ble-probe\.
setlocal
for /f "usebackq delims=" %%i in (`"%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe" -latest -products * -property installationPath`) do set VS=%%i
if not defined VS (echo Visual Studio's C++ tools not found & exit /b 1)
call "%VS%\VC\Auxiliary\Build\vcvars64.bat" >nul || exit /b 1
set OUT=%~dp0..\..\build\ble-probe
if not exist "%OUT%" mkdir "%OUT%"
cl /nologo /std:c++20 /EHsc /O2 /utf-8 "%~dp0ble-probe.cpp" /Fo"%OUT%\\" /link windowsapp.lib /out:"%OUT%\ble-probe.exe"
