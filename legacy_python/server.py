import argparse
from flask import Flask, render_template, jsonify, request
from flask_cors import CORS
from flask_socketio import SocketIO
import os
import re
import time
import json
import bleach
import psutil
import GPUtil
from datetime import datetime
import database as db

# Setup CLI Arguments
parser = argparse.ArgumentParser(description='Set webserver port and machine name')
parser.add_argument('-port', type=int, nargs='?', default=8000)
parser.add_argument('-machine_name', type=str, required=True)
args = parser.parse_args()

# Initialize Flask App & WebSockets
app = Flask(__name__, template_folder='templates', static_folder='static')
app.config['SECRET_KEY'] = 'salad_secret!'
CORS(app)
socketio = SocketIO(app, cors_allowed_origins="*")

# Initialize Local Database
db.init_db()

MACHINE_NAME = args.machine_name

# Session Tracking Variables
SESSION_START_TIME = time.time()
INITIAL_WALLET_BALANCE = None
log_cache = {'files': {}}
bandwidth_cache = {'files': {}}

# Log Parsing Regex Patterns
EARNINGS_REGEX = re.compile(r"(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] Predicted Earnings Report: ([\d.]+) from \(([^)]+)\)")
WALLET_REGEX = re.compile(r"(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] Wallet: Current\(([\d.]+)\), Predicted\(([-\d.]+)\)")
BANDWIDTH_REGEX = re.compile(r'(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] \{.*?"BidirThroughput":([\d.]+)')
ERROR_REGEX = re.compile(r'(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[(WRN|ERR)\] (.*)')
SYS_INFO_REGEX = re.compile(r"\[INF\] StaticData: (\{.*\})")

def parse_file(file_path, start_line=0, is_bandwidth=False):
    parsed = {'wallet': [], 'earnings': [], 'bandwidth': [], 'errors': []}
    try:
        with open(file_path, 'r', encoding='utf-8', errors='ignore') as f:
            content = f.readlines()[start_line:]
            joined_content = ''.join(content)

            for match in EARNINGS_REGEX.finditer(joined_content):
                parsed['earnings'].append({'timestamp': match.group(1), 'earnings': float(match.group(2)), 'containerId': match.group(3)})
            for match in WALLET_REGEX.finditer(joined_content):
                parsed['wallet'].append({'timestamp': match.group(1), 'currentBalance': float(match.group(2))})
            for match in ERROR_REGEX.finditer(joined_content):
                msg = match.group(3).strip()
                if "Node Compatibility" in msg or "Degraded" in msg or "Failed" in msg:
                    parsed['errors'].append({'timestamp': match.group(1), 'error_message': msg})
            
            if is_bandwidth:
                for match in BANDWIDTH_REGEX.finditer(joined_content):
                    parsed['bandwidth'].append({'timestamp': match.group(1), 'BidirThroughput': float(match.group(2)) / (280000*8)})
                    
        return parsed, len(content)
    except Exception:
        return parsed, 0

def background_log_monitor():
    """Runs continuously in the background. Checks logs and updates DB/WebSockets."""
    global INITIAL_WALLET_BALANCE
    log_dir = r'C:\ProgramData\Salad\logs'
    
    while True:
        has_new_data = False
        
        # 1. Process Main Logs
        main_files = [f.path for f in os.scandir(log_dir) if f.is_file() and f.name.endswith(('.txt', '.log')) and f.name.startswith('log-')]
        main_files.sort(key=os.path.getmtime, reverse=True)
        
        for file_path in main_files[:20]:
            last_mod = log_cache['files'].get(file_path, {}).get('last_modified', 0)
            if os.path.getmtime(file_path) > last_mod:
                start_line = log_cache['files'].get(file_path, {}).get('last_line', 0)
                data, lines_read = parse_file(file_path, start_line, False)
                
                if data['wallet'] or data['earnings'] or data['errors']:
                    db.insert_data('wallet', data['wallet'])
                    db.insert_data('earnings', data['earnings'])
                    
                    if data['errors']:
                        db.insert_data('errors', data['errors'])
                    
                    has_new_data = True
                    
                    if not INITIAL_WALLET_BALANCE and data['wallet']:
                        INITIAL_WALLET_BALANCE = data['wallet'][0]['currentBalance']
                        
                log_cache['files'][file_path] = {'last_line': start_line + lines_read, 'last_modified': os.path.getmtime(file_path)}

        # 2. Process Bandwidth Logs
        bw_files = [os.path.join(r, f) for r, _, fs in os.walk(log_dir) for f in fs if f.endswith(('.txt', '.log')) and "Bandwidth-SGS-" in r]
        bw_files.sort(key=os.path.getmtime, reverse=True)
        
        for file_path in bw_files[:20]:
            last_mod = bandwidth_cache['files'].get(file_path, {}).get('last_modified', 0)
            if os.path.getmtime(file_path) > last_mod:
                start_line = bandwidth_cache['files'].get(file_path, {}).get('last_line', 0)
                data, lines_read = parse_file(file_path, start_line, True)
                
                if data['bandwidth']:
                    db.insert_data('bandwidth', data['bandwidth'])
                    has_new_data = True
                bandwidth_cache['files'][file_path] = {'last_line': start_line + lines_read, 'last_modified': os.path.getmtime(file_path)}

        # Tell the frontend dashboard to refresh instantly
        if has_new_data:
            socketio.emit('data_updated', {'message': 'New data available'})
            
        socketio.sleep(15) 

# --- API ROUTES ---
@app.route('/')
def serve_index():
    return render_template('index.html')

@app.route('/api/data', methods=['GET'])
def api_data():
    hours = int(request.args.get('hours', 24))
    return jsonify(db.get_data(hours))

@app.route('/api/stats', methods=['GET'])
def api_stats():
    uptime_seconds = time.time() - SESSION_START_TIME
    uptime_str = f"{int(uptime_seconds // 3600)}h {int((uptime_seconds % 3600) // 60)}m"
    
    session_earnings = 0.0
    latest_wallet = db.get_data(1)['wallet']
    if latest_wallet and INITIAL_WALLET_BALANCE:
        session_earnings = latest_wallet[-1]['current'] - INITIAL_WALLET_BALANCE
        
    return jsonify({'uptime': uptime_str, 'session_earnings': max(0, session_earnings)})

@app.route('/api/errors', methods=['GET'])
def api_errors():
    return jsonify(db.get_active_errors())

@app.route('/api/dismiss-error', methods=['POST'])
def api_dismiss_error():
    ts = bleach.clean(request.json.get('timestamp', ''), strip=True)
    db.dismiss_error(ts)
    return jsonify({'status': 'success'})

@app.route('/api/dismiss-all-errors', methods=['POST'])
def api_dismiss_all_errors():
    db.dismiss_all_errors()
    return jsonify({'status': 'success'})

@app.route('/api/system-info', methods=['GET'])
def api_system_info():
    sys_dir = r"C:\ProgramData\Salad\logs\systeminformation"
    sys_info = {"CPU": "Unknown", "GPU": "Unknown", "RAM": "Unknown", "OS": "Unknown"}
    try:
        files = [os.path.join(sys_dir, f) for f in os.listdir(sys_dir) if f.endswith(".txt") or f.endswith(".log")]
        if files:
            files.sort(key=os.path.getmtime, reverse=True)
            with open(files[0], "r", encoding="utf-8", errors='ignore') as f:
                for line in f:
                    match = SYS_INFO_REGEX.search(line)
                    if match:
                        data = json.loads(match.group(1))
                        
                        # CPU 
                        cpu_brand = data.get('cpu', {}).get('brand', '').strip()
                        cpu_brand = cpu_brand.replace("Gen Intel", "Intel")
                        sys_info["CPU"] = cpu_brand
                        
                        # GPU 
                        controllers = data.get("graphics", {}).get("controllers", [])
                        if controllers:
                            real_gpu = controllers[0]
                            for gpu in controllers:
                                name_string = f"{gpu.get('vendor', '')} {gpu.get('model', '')}".lower()
                                if "nvidia" in name_string or "amd" in name_string or "radeon" in name_string:
                                    real_gpu = gpu
                                    break
                                    
                            gpu_vendor = real_gpu.get('vendor', '').strip()
                            gpu_model = real_gpu.get('model', '').strip()
                            
                            if gpu_vendor.lower() in gpu_model.lower() or "nvidia" in gpu_model.lower() or "amd" in gpu_model.lower():
                                sys_info["GPU"] = gpu_model
                            else:
                                sys_info["GPU"] = f"{gpu_vendor} {gpu_model}".strip()
                        
                        # RAM & OS
                        if "memLayout" in data:
                            sys_info["RAM"] = f"{sum(m.get('size', 0) for m in data['memLayout']) // (1024 ** 3)} GB"
                        sys_info["OS"] = data.get("os", {}).get("distro", "Unknown")
                        break
    except Exception: pass
    return jsonify(sys_info)

@app.route('/api/hardware-live', methods=['GET'])
def api_hardware_live():
    # FIX: Adding a 0.1 second interval forces Python to calculate an accurate average
    cpu_usage = psutil.cpu_percent(interval=0.1)
    ram_usage = psutil.virtual_memory().percent
    
    gpu_usage = 0
    gpu_temp = 0
    try:
        gpus = GPUtil.getGPUs()
        if gpus:
            # This reads the raw compute load from the NVIDIA driver
            gpu_usage = gpus[0].load * 100
            gpu_temp = gpus[0].temperature
    except Exception:
        pass
        
    return jsonify({
        "cpu_load": cpu_usage,
        "ram_load": ram_usage,
        "gpu_load": gpu_usage,
        "gpu_temp": gpu_temp
    })

if __name__ == '__main__':
    socketio.start_background_task(background_log_monitor)
    socketio.run(app, host='0.0.0.0', port=args.port, debug=False)