import sqlite3
from datetime import datetime, timedelta

DB_FILE = 'salad_metrics.db'

def init_db():
    conn = sqlite3.connect(DB_FILE)
    c = conn.cursor()
    # Create tables with UNIQUE timestamps to prevent duplicates
    c.execute('CREATE TABLE IF NOT EXISTS wallet (timestamp TEXT UNIQUE, current REAL)')
    c.execute('CREATE TABLE IF NOT EXISTS earnings (timestamp TEXT UNIQUE, container_id TEXT, rate REAL)')
    c.execute('CREATE TABLE IF NOT EXISTS bandwidth (timestamp TEXT UNIQUE, throughput REAL)')
    c.execute('CREATE TABLE IF NOT EXISTS errors (timestamp TEXT UNIQUE, message TEXT, dismissed INTEGER DEFAULT 0)')
    conn.commit()
    conn.close()

def insert_data(table, data_list):
    if not data_list: return
    conn = sqlite3.connect(DB_FILE)
    c = conn.cursor()
    
    if table == 'wallet':
        c.executemany('INSERT OR IGNORE INTO wallet (timestamp, current) VALUES (?, ?)', 
                      [(d['timestamp'], d['currentBalance']) for d in data_list])
    elif table == 'earnings':
        c.executemany('INSERT OR IGNORE INTO earnings (timestamp, container_id, rate) VALUES (?, ?, ?)', 
                      [(d['timestamp'], d['containerId'], d['earnings']) for d in data_list])
    elif table == 'bandwidth':
        c.executemany('INSERT OR IGNORE INTO bandwidth (timestamp, throughput) VALUES (?, ?)', 
                      [(d['timestamp'], d['BidirThroughput']) for d in data_list])
    elif table == 'errors':
        c.executemany('INSERT OR IGNORE INTO errors (timestamp, message) VALUES (?, ?)', 
                      [(d['timestamp'], d['error_message']) for d in data_list])
    
    conn.commit()
    conn.close()

def get_data(hours=24):
    conn = sqlite3.connect(DB_FILE)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    time_limit = (datetime.utcnow() - timedelta(hours=hours)).isoformat()
    
    wallet = [dict(row) for row in c.execute('SELECT * FROM wallet WHERE timestamp >= ? ORDER BY timestamp', (time_limit,)).fetchall()]
    earnings = [dict(row) for row in c.execute('SELECT * FROM earnings WHERE timestamp >= ? ORDER BY timestamp', (time_limit,)).fetchall()]
    bandwidth = [dict(row) for row in c.execute('SELECT * FROM bandwidth WHERE timestamp >= ? ORDER BY timestamp', (time_limit,)).fetchall()]
    
    conn.close()
    return {'wallet': wallet, 'earnings': earnings, 'bandwidth': bandwidth}

def get_active_errors():
    conn = sqlite3.connect(DB_FILE)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    errors = [dict(row) for row in c.execute('SELECT * FROM errors WHERE dismissed = 0 ORDER BY timestamp DESC').fetchall()]
    conn.close()
    return errors

def dismiss_error(timestamp):
    conn = sqlite3.connect(DB_FILE)
    c = conn.cursor()
    c.execute('UPDATE errors SET dismissed = 1 WHERE timestamp = ?', (timestamp,))
    conn.commit()
    conn.close()