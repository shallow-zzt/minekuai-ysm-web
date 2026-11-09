@echo off
setlocal
cd /d "%~dp0"
title YSM Skin Bridge
echo Starting YSM Skin Bridge...
echo Keep this window open while the website is running.
echo.
if not exist "dist\ysm-bridge.exe" (
    echo ERROR: dist\ysm-bridge.exe is missing.
    pause
    exit /b 1
)
if not exist "config.json" (
    echo ERROR: config.json is missing.
    echo Copy config.example.json to config.json and fill in your SFTP connection settings.
    pause
    exit /b 1
)
"%~dp0dist\ysm-bridge.exe" -config "%~dp0config.json"
set "bridgeExitCode=%ERRORLEVEL%"
echo.
echo YSM Bridge stopped. Exit code: %bridgeExitCode%
echo Log file: %~dp0data\logs\startup.log
echo Press any key to close this window.
pause >nul
exit /b %bridgeExitCode%
