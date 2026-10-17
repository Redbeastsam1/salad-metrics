@echo off
title Salad Metrics
echo ============================================
echo   Salad Metrics - Go Edition
echo ============================================

REM Default configuration
set MACHINE_NAME=Beast
set DISCORD_BOT_TOKEN=
set SILENT_FLAG=

REM Check for silent / tray-only flag
if "%1"=="--silent" set SILENT_FLAG=-silent
if "%1"=="-silent"  set SILENT_FLAG=-silent
if "%1"=="silent"   set SILENT_FLAG=-silent

REM Load .env if present
if exist .env (
    for /f "usebackq tokens=1,* delims==" %%a in (".env") do (
        set %%a=%%b
    )
)

echo [INFO] Building salad-metrics.exe...
go build -o salad-metrics.exe .

if not "%SERVER_URL%"=="" (
    echo [INFO] Running in Agent mode -> %SERVER_URL%
    salad-metrics.exe -mode agent -server "%SERVER_URL%" -machine-name "%MACHINE_NAME%" %SILENT_FLAG%
) else if "%DISCORD_BOT_TOKEN%"=="" (
    salad-metrics.exe -port 8000 -machine-name "%MACHINE_NAME%" %SILENT_FLAG%
) else (
    salad-metrics.exe -port 8000 -machine-name "%MACHINE_NAME%" -discord -discord-token "%DISCORD_BOT_TOKEN%" %SILENT_FLAG%
)

pause