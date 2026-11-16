@echo off
rem ragflow-x launcher for Windows (double-clickable wrapper around start.ps1).
rem Keeps the console window open when started by double-click so a startup
rem failure is readable instead of a window that vanishes.
setlocal
set "SCRIPT_DIR=%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT_DIR%start.ps1" %*
set "RC=%ERRORLEVEL%"
if not "%~1"=="" goto :done
echo.
echo (exit code %RC%) press any key to close this window
pause >nul
:done
endlocal & exit /b %RC%
