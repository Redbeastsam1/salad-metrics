@echo off
title Salad Metrics
echo ============================================
echo   Salad Metrics - Go Edition
echo ============================================

REM Default configuration
set MACHINE_NAME=Beast
set DISCORD_BOT_TOKEN=

REM Load .env if present
if exist .env (
    for /f "usebackq tokens=1,* delims==" %%a in (".env") do (
        set %%a=%%b
    )
)

if not exist salad-metrics.exe (
    echo [INFO] Compiling salad-metrics.exe...
    go build -o salad-metrics.exe .
)

if "%DISCORD_BOT_TOKEN%"=="" (
    salad-metrics.exe -port 8000 -machine-name "%MACHINE_NAME%"
) else (
    salad-metrics.exe -port 8000 -machine-name "%MACHINE_NAME%" -discord -discord-token "%DISCORD_BOT_TOKEN%"
)

pause