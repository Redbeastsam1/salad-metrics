@echo off
echo Starting Salad Metrics Server...
call venv\Scripts\activate.bat
python server.py -port 1000 -machine_name "YourMachineName"
pause