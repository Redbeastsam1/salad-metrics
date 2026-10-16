# Salad Metrics — Legacy Python Implementation

This directory contains the original Python (Flask + Flask-SocketIO + discord.py) implementation of Salad Metrics prior to the Go rewrite.

## Files
- `server.py`: Flask web application and background log watcher.
- `database.py`: SQLite wrapper functions.
- `discord_bot.py`: Standalone Discord bot client.
- `(discord)start.bat`: Original start script for running the Python server with Discord.

## Dependencies (Historical)
If you ever need to run the Python version:
```bash
pip install flask flask-cors flask-socketio eventlet discord.py psutil pynvml
python server.py
```

*Note: The primary, high-performance version of Salad Metrics is now written in Go located in the root repository directory.*
