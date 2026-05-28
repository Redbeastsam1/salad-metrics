from flask import Flask, jsonify, render_template, request
from flask_socketio import SocketIO
from flask_cors import CORS
import os, re, json, time, argparse, bleach
from datetime import datetime
import database as db

parser = argparse.ArgumentParser(description='Set webserver port and machine name')
parser.add_argument('-port', type=int, nargs='?', default=8000)
parser.add_argument('-machine_name', type=str, required=True)
args = parser.parse_args()

app = Flask(__name__)
app.config['SECRET_KEY'] = 'salad_secret!'
CORS(app)
socketio = SocketIO(app, cors_allowed_origins="*")

# Init Database
db.init_db()

# Session tracking for "At a Glance" stats
SESSION_START_TIME = time.time()
INITIAL_WALLET_BALANCE = None

log_cache = {'files': {}}
bandwidth_cache = {'files': {}}

EARNINGS_REGEX = re.compile(r"(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] Predicted Earnings Report: ([\d.]+) from \(([^)]+)\)")
WALLET_REGEX = re.compile(r"(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] Wallet: Current\(([\d.]+)\), Predicted\(([-\d.]+)\)")
BANDWIDTH_REGEX = re.compile(r'(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[INF\] \{.*?"BidirThroughput":([\d.]+)')
ERROR_REGEX = re.compile(r'(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[WRN\] Node Compatibility Workload Failure (.*?) NodeCompatibilityMessage {(.*?)}', re.DOTALL)
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
                parsed['errors'].append({'timestamp': match.group(1), 'error_message': match.group(3)})
            
            if is_bandwidth:
                for match in BANDWIDTH_REGEX.finditer(joined_content):
                    parsed['bandwidth'].append({'timestamp': match.group(1), 'BidirThroughput': float(match.group(2)) / (280000*8)})
                    
        return parsed, len(content)
    except Exception:
        return parsed, 0

def background_log_monitor():
    """Runs continuously in the background to check for log updates."""
    global INITIAL_WALLET_BALANCE
    log_dir = r'C:\ProgramData\Salad\logs'
    
    while True:
        has_new_data = False
        
        # 1. Check Main Logs
        main_files = [os.path.join(r, f) for r, _, fs in os.walk(log_dir) for f in fs if f.endswith('.txt') and "Bandwidth-SGS-" not in r and "ndm" not in r]
        main_files.sort(key=os.path.getmtime, reverse=True)
        
        for file_path in main_files[:3]:
            last_mod = log_cache['files'].get(file_path, {}).get('last_modified', 0)
            if os.path.getmtime(file_path) > last_mod:
                start_line = log_cache['files'].get(file_path, {}).get('last_line', 0)
                data, lines_read = parse_file(file_path, start_line, False)
                
                if data['wallet'] or data['earnings'] or data['errors']:
                    db.insert_data('wallet', data['wallet'])
                    db.insert_data('earnings', data['earnings'])
                    db.insert_data('errors', data['errors'])
                    has_new_data = True
                    
                    # Set initial balance for session tracking if not set
                    if not INITIAL_WALLET_BALANCE and data['wallet']:
                        INITIAL_WALLET_BALANCE = data['wallet'][0]['currentBalance']
                        
                log_cache['files'][file_path] = {'last_line': start_line + lines_read, 'last_modified': os.path.getmtime(file_path)}

        # 2. Check Bandwidth Logs
        bw_files = [os.path.join(r, f) for r, _, fs in os.walk(log_dir) for f in fs if f.endswith('.txt') and "Bandwidth-SGS-" in r]
        bw_files.sort(key=os.path.getmtime, reverse=True)
        
        for file_path in bw_files[:2]:
            last_mod = bandwidth_cache['files'].get(file_path, {}).get('last_modified', 0)
            if os.path.getmtime(file_path) > last_mod:
                start_line = bandwidth_cache['files'].get(file_path, {}).get('last_line', 0)
                data, lines_read = parse_file(file_path, start_line, True)
                
                if data['bandwidth']:
                    db.insert_data('bandwidth', data['bandwidth'])
                    has_new_data = True
                bandwidth_cache['files'][file_path] = {'last_line': start_line + lines_read, 'last_modified': os.path.getmtime(file_path)}

        # If we found new data, tell the frontend to refresh instantly!
        if has_new_data:
            socketio.emit('data_updated', {'message': 'New data available'})
            
        socketio.sleep(30) # Check every 30 seconds

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
    
    # Calculate session earnings based on wallet difference
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
                        sys_info["CPU"] = f"{data.get('cpu', {}).get('manufacturer', '')} {data.get('cpu', {}).get('brand', '')}".strip()
                        if data.get("graphics", {}).get("controllers"):
                            sys_info["GPU"] = f"{data['graphics']['controllers'][0].get('vendor', '')} {data['graphics']['controllers'][0].get('model', '')}".strip()
                        if "memLayout" in data:
                            sys_info["RAM"] = f"{sum(m.get('size', 0) for m in data['memLayout']) // (1024 ** 3)} GB"
                        sys_info["OS"] = data.get("os", {}).get("distro", "Unknown")
                        break
    except Exception: pass
    return jsonify(sys_info)

if __name__ == '__main__':
    socketio.start_background_task(background_log_monitor)
    socketio.run(app, host='0.0.0.0', port=args.port, debug=False)