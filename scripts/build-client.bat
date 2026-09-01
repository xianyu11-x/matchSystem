@echo off
setlocal

rem Run the complete Windows client release pipeline from any working directory.
rem ExecutionPolicy Bypass applies only to this PowerShell process; no machine
rem or user execution policy is changed.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-client.ps1" %*
set "exitCode=%ERRORLEVEL%"
endlocal & exit /b %exitCode%
