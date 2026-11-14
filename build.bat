@echo off
setlocal
cd /d "%~dp0"

if not defined GOPROXY set "GOPROXY=https://goproxy.cn,direct"

echo [1/3] Resolving modules...
go mod tidy
if errorlevel 1 goto :fail

echo [2/3] Building GUI (AutoMBA.exe)...
go build -trimpath -ldflags "-s -w -H=windowsgui" -o AutoMBA.exe .
if errorlevel 1 goto :fail

echo [3/3] Building CLI (automba-cli.exe)...
go build -trimpath -ldflags "-s -w" -o automba-cli.exe .
if errorlevel 1 goto :fail

echo.
echo Build OK:
echo   %CD%\AutoMBA.exe
echo   %CD%\automba-cli.exe
exit /b 0

:fail
echo.
echo Build FAILED.
exit /b 1
