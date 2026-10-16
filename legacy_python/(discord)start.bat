@echo off
echo ========================================
echo      Starting Salad Metrics Suite
echo ========================================

:: Activate the virtual environment
call venv\Scripts\activate.bat

:: Start the Flask Web Server in a new window
echo [1/2] Launching Web Server...
start "Salad Web Server" cmd /k "python server.py -port 1000 -machine_name Beast"

:: Start the Discord Bot in a new window
echo [2/2] Launching Discord Bot...
start "Salad Discord Bot" cmd /k "python discord_bot.py"

echo.
echo Both services have been launched in separate windows! 
echo Close those new windows whenever you want to stop the servers.
echo ========================================
pause