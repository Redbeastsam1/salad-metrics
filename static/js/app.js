/* ═══════════════════════════════════════════════════════
   SALAD METRICS — Frontend Application (Go Edition)
   WebSocket protocol: native ws://, event JSON { event, data }
   ═══════════════════════════════════════════════════════ */

'use strict';

// ─── State ─────────────────────────────────────────────
let isDarkMode    = JSON.parse(localStorage.getItem('isDarkMode') ?? 'true');
let currentHours  = 24;
let walletChart, containerChart, bandwidthChart, powerChart;
let notifiedErrors    = new Set();
let isFirstErrorLoad  = true;
let errorSidebarOpen  = false;

let lastWalletUSD          = 0;
let lastSessionEarningsUSD = 0;
let lastCurrentRateUSD     = 0;
let lastTotalPowerWatts    = 0;
let lastMachineStats       = null;
let lastEarningsData       = null;

const CURRENCIES = {
    USD: { symbol: '$', rate: 1.0 },
    INR: { symbol: '₹', rate: 86.5 },
    EUR: { symbol: '€', rate: 0.92 },
    GBP: { symbol: '£', rate: 0.79 },
    CAD: { symbol: 'CA$', rate: 1.38 },
    AUD: { symbol: 'AU$', rate: 1.55 }
};
let activeCurrency = localStorage.getItem('selectedCurrency') || 'USD';

function formatCurrency(usdVal, decimals = 4, withPrefix = '') {
    const num = parseFloat(usdVal) || 0;
    const curr = CURRENCIES[activeCurrency] || CURRENCIES.USD;
    const converted = num * curr.rate;
    return `${withPrefix}${curr.symbol}${converted.toFixed(decimals)}`;
}

async function fetchExchangeRates() {
    try {
        const res = await fetch('https://open.er-api.com/v6/latest/USD');
        const data = await res.json();
        if (data && data.rates) {
            for (const code of Object.keys(CURRENCIES)) {
                if (data.rates[code]) CURRENCIES[code].rate = data.rates[code];
            }
            refreshCurrencyDisplays();
        }
    } catch (e) {
        console.log('[currency] Using standard rates');
    }
}

function refreshCurrencyDisplays() {
    animateValue('wallet-balance-text', formatCurrency(lastWalletUSD));
    animateValue('session-earnings', formatCurrency(lastSessionEarningsUSD, 4, '+'));
    if (lastCurrentRateUSD > 0) {
        animateValue('current-rate', formatCurrency(lastCurrentRateUSD));
    } else {
        animateValue('current-rate', '--');
    }
    updateEfficiency(lastCurrentRateUSD, lastTotalPowerWatts);
    fetchDashboardData();
}

function updateEfficiency(rateUSD, totalWatts) {
    const effEl = document.getElementById('live-efficiency-val');
    if (!effEl) return;
    if (!rateUSD || rateUSD <= 0 || !totalWatts || totalWatts <= 0) {
        effEl.textContent = '--';
        return;
    }
    const dailyKWh = (totalWatts * 24.0) / 1000.0;
    if (dailyKWh <= 0) {
        effEl.textContent = '--';
        return;
    }
    const effPerKWh = rateUSD / dailyKWh;
    effEl.textContent = formatCurrency(effPerKWh, 3);
}

// ─── WebSocket Connection (native, replaces Socket.IO) ─
let ws;
function connectWS() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(`${proto}://${location.host}/ws`);

    ws.onopen = () => {
        setConnectionStatus(true);
        console.log('[ws] Connected');
        fetchDashboardData();
        fetchStats();
    };

    ws.onmessage = (evt) => {
        try {
            const msg = JSON.parse(evt.data);
            if (msg.event === 'data_updated') {
                fetchDashboardData();
                fetchStats();
                fetchErrors();
            }
        } catch (e) {}
    };

    ws.onclose = () => {
        setConnectionStatus(false);
        console.warn('[ws] Disconnected — reconnecting in 3s');
        setTimeout(connectWS, 3000); // Auto-reconnect
    };

    ws.onerror = () => ws.close();
}

function setConnectionStatus(connected) {
    const pill  = document.getElementById('connection-pill');
    const label = document.getElementById('conn-label');
    pill.className = 'connection-pill ' + (connected ? 'connected' : 'disconnected');
    label.textContent = connected ? 'Live' : 'Offline';
}

// ─── Theme ─────────────────────────────────────────────
function applyTheme() {
    document.documentElement.setAttribute('data-theme', isDarkMode ? 'dark' : 'light');
    const icon = document.getElementById('theme-icon');
    if (isDarkMode) {
        icon.innerHTML = `<circle cx="12" cy="12" r="5"></circle>
            <line x1="12" y1="1" x2="12" y2="3"></line><line x1="12" y1="21" x2="12" y2="23"></line>
            <line x1="4.22" y1="4.22" x2="5.64" y2="5.64"></line><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"></line>
            <line x1="1" y1="12" x2="3" y2="12"></line><line x1="21" y1="12" x2="23" y2="12"></line>
            <line x1="4.22" y1="19.78" x2="5.64" y2="18.36"></line><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"></line>`;
    } else {
        icon.innerHTML = `<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"></path>`;
    }
    updateChartColors();
}

document.getElementById('theme-toggle').addEventListener('click', () => {
    isDarkMode = !isDarkMode;
    localStorage.setItem('isDarkMode', isDarkMode);
    applyTheme();
});

// ─── Time Selector ─────────────────────────────────────
document.querySelectorAll('.time-btn').forEach(btn => {
    btn.addEventListener('click', (e) => {
        document.querySelectorAll('.time-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        currentHours = parseInt(btn.dataset.hours);
        fetchDashboardData();
    });
});

// ─── Charts Setup ──────────────────────────────────────
const getTextColor  = () => isDarkMode ? '#94a3b8' : '#475569';
const getGridColor  = () => isDarkMode ? 'rgba(255,255,255,0.05)' : 'rgba(0,0,0,0.07)';

const baseOptions = () => ({
    responsive: true,
    maintainAspectRatio: false,
    animation: { duration: 400 },
    scales: {
        x: {
            type: 'time',
            time: {
                displayFormats: {
                    minute: 'HH:mm',
                    hour: 'HH:mm',
                    day: 'MMM dd',
                    month: 'MMM yyyy'
                }
            },
            grid: { color: getGridColor() },
            ticks: { color: getTextColor(), maxTicksLimit: 7, font: { family: "'JetBrains Mono', monospace", size: 11 } }
        },
        y: {
            beginAtZero: false,
            grid: { color: getGridColor() },
            ticks: { color: getTextColor(), font: { family: "'JetBrains Mono', monospace", size: 11 } }
        }
    },
    plugins: {
        legend: { display: false },
        tooltip: {
            mode: 'nearest', intersect: false,
            backgroundColor: 'rgba(13,21,38,0.95)',
            borderColor: 'rgba(255,255,255,0.1)', borderWidth: 1,
            titleColor: '#e2e8f0', bodyColor: '#94a3b8',
            titleFont: { family: "'JetBrains Mono', monospace", size: 11 },
            bodyFont:  { family: "'JetBrains Mono', monospace", size: 11 },
        }
    },
    elements: { point: { radius: 0, hitRadius: 10 }, line: { tension: 0.3, borderWidth: 2 } },
    interaction: { mode: 'index', intersect: false }
});

function makeGradient(ctx, color) {
    const g = ctx.createLinearGradient(0, 0, 0, 200);
    g.addColorStop(0, color.replace('1)', '0.25)'));
    g.addColorStop(1, color.replace('1)', '0)'));
    return g;
}

function initCharts() {
    const wCtx = document.getElementById('walletChart').getContext('2d');
    const cCtx = document.getElementById('containerChart').getContext('2d');
    const bCtx = document.getElementById('bandwidthChart').getContext('2d');
    const pCtx = document.getElementById('powerChart').getContext('2d');

    walletChart = new Chart(wCtx, {
        type: 'line', data: { datasets: [] }, options: baseOptions()
    });
    containerChart = new Chart(cCtx, {
        type: 'line', data: { datasets: [] }, options: baseOptions()
    });
    bandwidthChart = new Chart(bCtx, {
        type: 'line', data: { datasets: [] }, options: baseOptions()
    });
    powerChart = new Chart(pCtx, {
        type: 'line', data: { datasets: [] }, options: baseOptions()
    });
}

function updateChartColors() {
    if (!walletChart) return;
    [walletChart, containerChart, bandwidthChart, powerChart].forEach(chart => {
        if (!chart) return;
        chart.options.scales.x.grid.color = getGridColor();
        chart.options.scales.x.ticks.color = getTextColor();
        chart.options.scales.y.grid.color = getGridColor();
        chart.options.scales.y.ticks.color = getTextColor();
        chart.update('none');
    });
}

// ─── Data Fetching ─────────────────────────────────────
async function fetchDashboardData() {
    try {
        const res  = await fetch(`/api/data?hours=${currentHours}`);
        const data = await res.json();
        // Keep chart x-axes free (auto-scaling to collected data range)
        [walletChart, containerChart, bandwidthChart, powerChart].forEach(chart => {
            if (!chart) return;
            delete chart.options.scales.x.min;
            delete chart.options.scales.x.max;
        });

        const curr = CURRENCIES[activeCurrency] || CURRENCIES.USD;

        // ── Wallet ──
        if (data.wallet?.length > 0) {
            const last = data.wallet[data.wallet.length - 1];
            lastWalletUSD = parseFloat(last.current) || 0;
            animateValue('wallet-balance-text', formatCurrency(lastWalletUSD));

            const wCtx = walletChart.canvas.getContext('2d');
            walletChart.data.datasets = [{
                label: `Wallet (${curr.symbol})`,
                data: data.wallet.map(d => ({ x: new Date(d.timestamp), y: d.current * curr.rate })),
                borderColor: '#00ff88',
                backgroundColor: makeGradient(wCtx, 'rgba(0,255,136,1)'),
                fill: true
            }];
            walletChart.update();
        }

        // ── Bandwidth ──
        if (data.bandwidth?.length > 0) {
            const bCtx = bandwidthChart.canvas.getContext('2d');
            bandwidthChart.data.datasets = [{
                label: 'Throughput (MB/s)',
                data: data.bandwidth.map(d => ({ x: new Date(d.timestamp), y: d.throughput })),
                borderColor: '#22d3ee',
                backgroundColor: makeGradient(bCtx, 'rgba(34,211,238,1)'),
                fill: true
            }];
            bandwidthChart.update();
        }

        // ── Power Telemetry ──
        if (data.power?.length > 0 && powerChart) {
            const pCtx = powerChart.canvas.getContext('2d');
            powerChart.data.datasets = [
                {
                    label: 'Total Power (W)',
                    data: data.power.map(d => ({ x: new Date(d.timestamp), y: d.total_power })),
                    borderColor: '#ffd700',
                    backgroundColor: makeGradient(pCtx, 'rgba(255,215,0,1)'),
                    fill: false
                },
                {
                    label: 'GPU Power (W)',
                    data: data.power.map(d => ({ x: new Date(d.timestamp), y: d.gpu_power })),
                    borderColor: '#a855f7',
                    backgroundColor: makeGradient(pCtx, 'rgba(168,85,247,1)'),
                    fill: false
                },
                {
                    label: 'CPU Power (W)',
                    data: data.power.map(d => ({ x: new Date(d.timestamp), y: d.cpu_power })),
                    borderColor: '#4d9fff',
                    backgroundColor: makeGradient(pCtx, 'rgba(77,159,255,1)'),
                    fill: false
                }
            ];
            powerChart.update();
        }

        // ── Earnings / Containers ──
        const containersMap = {};
        (data.earnings ?? []).forEach(d => {
            const id = d.container_id;
            const ts = new Date(d.timestamp);
            if (!containersMap[id]) containersMap[id] = { data: [], start: ts, rate: 0 };
            containersMap[id].data.push({ x: ts, y: d.rate * curr.rate });
            containersMap[id].rate = d.rate;
            if (ts < containersMap[id].start) containersMap[id].start = ts;
        });

        const cCtx = containerChart.canvas.getContext('2d');
        containerChart.data.datasets = Object.entries(containersMap).map(([id, c]) => ({
            label: id,
            data: c.data,
            borderColor: '#4d9fff',
            backgroundColor: makeGradient(cCtx, 'rgba(77,159,255,1)'),
            fill: true
        }));
        containerChart.update();
        renderContainerHistory(containersMap);

        // Update status card
        updateStatusCard(data.earnings);

    } catch (e) { console.error('[data]', e); }
}

async function fetchStats() {
    try {
        const res   = await fetch('/api/stats');
        const stats = await res.json();
        document.getElementById('session-uptime').textContent = `Uptime: ${stats.uptime}`;
        lastSessionEarningsUSD = parseFloat(stats.session_earnings) || 0;
        animateValue('session-earnings', formatCurrency(lastSessionEarningsUSD, 4, '+'));
        if (stats.wallet_balance !== undefined && stats.wallet_balance !== null) {
            lastWalletUSD = parseFloat(stats.wallet_balance) || 0;
            animateValue('wallet-balance-text', formatCurrency(lastWalletUSD));
        }
        if (stats.machine_name) {
            document.getElementById('machine-name-label').textContent = stats.machine_name;
        }
        lastMachineStats = stats;
        updateStatusCard(lastEarningsData);
    } catch (e) {}
}

async function fetchSystemInfo() {
    try {
        const res  = await fetch('/api/system-info');
        const info = await res.json();
        document.getElementById('sys-cpu').textContent = info.CPU || '--';
        document.getElementById('sys-gpu').textContent = info.GPU || '--';
        document.getElementById('sys-ram').textContent = info.RAM || '--';
        document.getElementById('sys-os').textContent  = info.OS || '--';
    } catch (e) {}
}

async function updateLiveHardware() {
    try {
        const res  = await fetch('/api/hardware-live');
        const data = await res.json();

        const cpuPowerStr = data.cpu_power ? ` · ~${data.cpu_power.toFixed(0)}W` : '';
        const gpuPowerStr = data.gpu_power ? ` · ${data.gpu_power.toFixed(0)}W` : '';

        setBar('live-cpu-val', 'live-cpu-bar', data.cpu_load, `${data.cpu_load.toFixed(1)}%${cpuPowerStr}`);
        setBar('live-ram-val', 'live-ram-bar', data.ram_load, `${data.ram_load.toFixed(1)}%`);
        setBar('live-gpu-val', 'live-gpu-bar', data.gpu_load, `${data.gpu_load.toFixed(1)}% (${data.gpu_temp.toFixed(0)}°C)${gpuPowerStr}`);

        // Update dedicated live power metrics
        const totalPowerEl = document.getElementById('live-total-power');
        if (totalPowerEl) totalPowerEl.textContent = `~${(data.total_power ?? 0).toFixed(0)} W`;

        const gpuPowerEl = document.getElementById('live-gpu-power');
        if (gpuPowerEl) gpuPowerEl.textContent = `${(data.gpu_power ?? 0).toFixed(1)} W`;

        const gpuLimitEl = document.getElementById('live-gpu-limit');
        if (gpuLimitEl) {
            gpuLimitEl.textContent = data.gpu_limit > 0 ? `/ ${data.gpu_limit.toFixed(0)}W` : '';
        }

        const cpuPowerEl = document.getElementById('live-cpu-power');
        if (cpuPowerEl) cpuPowerEl.textContent = `~${(data.cpu_power ?? 0).toFixed(1)} W`;

        lastTotalPowerWatts = data.total_power ?? 0;
        updateEfficiency(lastCurrentRateUSD, lastTotalPowerWatts);

    } catch (e) {}
}

function setBar(valId, barId, pct, label) {
    document.getElementById(valId).textContent = label;
    document.getElementById(barId).style.width  = `${Math.min(100, pct)}%`;
}

// ─── Status Card ───────────────────────────────────────
function updateStatusCard(earnings) {
    if (earnings) {
        lastEarningsData = earnings;
    } else {
        earnings = lastEarningsData;
    }

    const badge = document.getElementById('node-status');
    const statusText = document.getElementById('status-text');
    const containerLabel = document.getElementById('active-container-id');
    const rateEl = document.getElementById('current-rate');

    // 1. Authoritative status from backend /api/stats
    if (lastMachineStats && lastMachineStats.status) {
        if (lastMachineStats.status === 'running') {
            badge.className = 'status-badge running';
            statusText.textContent = 'Running';
            const id = lastMachineStats.active_container_id || '';
            containerLabel.textContent = id.length > 8 ? `${id.substring(0, 8)}...` : (id || 'Active Workload');
            lastCurrentRateUSD = parseFloat(lastMachineStats.current_rate) || 0;
            animateValue('current-rate', formatCurrency(lastCurrentRateUSD));
            updateEfficiency(lastCurrentRateUSD, lastTotalPowerWatts);
            return;
        } else {
            badge.className = 'status-badge idle';
            statusText.textContent = 'Idle';
            containerLabel.textContent = 'No active container';
            lastCurrentRateUSD = 0;
            animateValue('current-rate', '--');
            updateEfficiency(0, lastTotalPowerWatts);
            return;
        }
    }

    // 2. Fallback to earnings log array
    if (!earnings || earnings.length === 0) {
        badge.className = 'status-badge idle';
        statusText.textContent = 'Idle';
        containerLabel.textContent = 'No active container';
        lastCurrentRateUSD = 0;
        rateEl.textContent = '--';
        updateEfficiency(0, lastTotalPowerWatts);
        return;
    }

    const latest = earnings[earnings.length - 1];
    const rate   = parseFloat(latest.rate ?? 0);
    const ts     = new Date(latest.timestamp);
    const isStale = (Date.now() - ts.getTime()) > 30 * 60 * 1000;

    if (rate > 0 && !isStale) {
        badge.className = 'status-badge running';
        statusText.textContent = 'Running';
        const id = latest.container_id ?? '';
        containerLabel.textContent = id.length > 8 ? `${id.substring(0, 8)}...` : id;
        lastCurrentRateUSD = rate;
        animateValue('current-rate', formatCurrency(lastCurrentRateUSD));
        updateEfficiency(lastCurrentRateUSD, lastTotalPowerWatts);
    } else {
        badge.className = 'status-badge idle';
        statusText.textContent = 'Idle';
        containerLabel.textContent = 'No active container';
        lastCurrentRateUSD = 0;
        animateValue('current-rate', '--');
        updateEfficiency(0, lastTotalPowerWatts);
    }
}

// ─── Container History ─────────────────────────────────
function renderContainerHistory(containersMap) {
    const container = document.getElementById('history-container');
    const entries   = Object.entries(containersMap).sort((a, b) => b[1].start - a[1].start);

    if (entries.length === 0) {
        container.innerHTML = '<p class="muted-center">No containers in this time range.</p>';
        return;
    }

    container.innerHTML = entries.map(([id, c]) => {
        const startStr = c.start.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        const dateStr  = c.start.toLocaleDateString();
        return `
        <div class="history-card">
            <div class="history-card-header">
                <span class="history-id">${id.substring(0, 8)}...</span>
                <span class="history-rate">${formatCurrency(c.rate)}/24h</span>
            </div>
            <div class="history-time">Started ${dateStr} at ${startStr}</div>
        </div>`;
    }).join('');
}

// ─── Errors ────────────────────────────────────────────
async function fetchErrors() {
    try {
        const res    = await fetch('/api/errors');
        const errors = await res.json();
        const list   = document.getElementById('errors-container');
        const badge  = document.getElementById('error-count');
        const hBadge = document.getElementById('header-error-count');
        const toggleBtn = document.getElementById('error-toggle-btn');
        list.innerHTML = '';
        
        const count = errors.length;
        badge.textContent = count;
        if (hBadge) hBadge.textContent = count;

        if (toggleBtn) {
            if (count > 0) {
                toggleBtn.classList.add('has-errors');
            } else {
                toggleBtn.classList.remove('has-errors');
            }
        }

        if (count === 0) {
            list.innerHTML = '<p class="muted-center">No degraded errors or failures reported.</p>';
            return;
        }

        errors.forEach(err => {
            const diffMin = Math.floor((Date.now() - new Date(err.timestamp)) / 60000);
            const ago = diffMin > 60 ? `${Math.floor(diffMin / 60)}h ago` : `${diffMin}m ago`;

            // Classify title based on content
            let title = 'Workload Failure';
            if (err.message.includes('Node Compatibility')) {
                title = 'Node Incompatible';
            } else if (err.message.includes('Degraded')) {
                title = 'Node Degraded';
            }

            const card = document.createElement('div');
            card.className = 'error-card';
            card.innerHTML = `
                <div class="error-card-header">
                    <span class="error-card-title">${title}</span>
                    <span class="error-age">${ago}</span>
                </div>
                <p class="error-msg">${err.message}</p>
                <button class="dismiss-btn" data-ts="${err.timestamp}">Dismiss</button>`;
            list.appendChild(card);
        });

        list.querySelectorAll('.dismiss-btn').forEach(btn => {
            btn.addEventListener('click', async () => {
                await fetch('/api/dismiss-error', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ timestamp: btn.dataset.ts })
                });
                fetchErrors();
            });
        });

        isFirstErrorLoad = false;
    } catch (e) {}
}

// ─── Error Sidebar ─────────────────────────────────────
function openErrorSidebar() {
    errorSidebarOpen = true;
    document.getElementById('error-sidebar').classList.add('open');
    document.getElementById('app-grid').classList.add('sidebar-open');
}
function closeErrorSidebar() {
    errorSidebarOpen = false;
    document.getElementById('error-sidebar').classList.remove('open');
    document.getElementById('app-grid').classList.remove('sidebar-open');
}

const errorToggleBtn = document.getElementById('error-toggle-btn');
if (errorToggleBtn) {
    errorToggleBtn.addEventListener('click', () => {
        if (errorSidebarOpen) {
            closeErrorSidebar();
        } else {
            openErrorSidebar();
        }
    });
}

const closeErrorBtn = document.getElementById('close-error-sidebar-btn');
if (closeErrorBtn) {
    closeErrorBtn.addEventListener('click', closeErrorSidebar);
}

document.getElementById('clear-all-errors-btn').addEventListener('click', async () => {
    await fetch('/api/dismiss-all-errors', { method: 'POST' });
    fetchErrors();
});

// ─── History Drawer ────────────────────────────────────
document.getElementById('open-drawer').addEventListener('click', () => {
    document.getElementById('history-drawer').classList.add('open');
    document.getElementById('drawer-overlay').classList.add('open');
});
function closeDrawer() {
    document.getElementById('history-drawer').classList.remove('open');
    document.getElementById('drawer-overlay').classList.remove('open');
}
document.getElementById('close-drawer').addEventListener('click', closeDrawer);
document.getElementById('drawer-overlay').addEventListener('click', closeDrawer);

// ─── Toast Notifications ───────────────────────────────
function showToast(title, message, isError = false) {
    const container = document.getElementById('toast-container');
    const el = document.createElement('div');
    el.className = 'toast' + (isError ? ' error' : '');
    el.innerHTML = `
        <span class="toast-icon">${isError ? '⚠️' : '🔔'}</span>
        <div class="toast-body">
            <div class="toast-title">${title}</div>
            <div class="toast-msg">${message.substring(0, 120)}${message.length > 120 ? '…' : ''}</div>
        </div>
        <button class="toast-close" onclick="this.closest('.toast').remove()">✕</button>`;
    container.prepend(el);
    setTimeout(() => el.remove(), 8000);
}

// ─── Animate value changes ─────────────────────────────
function animateValue(id, newValue) {
    const el = document.getElementById(id);
    if (!el) return;
    if (el.textContent !== newValue) {
        el.style.opacity = '0.4';
        el.style.transform = 'translateY(-4px)';
        setTimeout(() => {
            el.textContent = newValue;
            el.style.opacity = '1';
            el.style.transform = 'translateY(0)';
            el.style.transition = 'opacity 0.25s ease, transform 0.25s ease';
        }, 150);
    }
}

// ─── Boot ──────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
    applyTheme();
    initCharts();
    connectWS();

    // Currency selector initialization
    const currencySelect = document.getElementById('currency-select');
    if (currencySelect) {
        currencySelect.value = activeCurrency;
        currencySelect.addEventListener('change', (e) => {
            activeCurrency = e.target.value;
            localStorage.setItem('selectedCurrency', activeCurrency);
            refreshCurrencyDisplays();
        });
    }

    // Export dropdown toggle
    const exportBtn = document.getElementById('export-menu-btn');
    const exportMenu = document.getElementById('export-dropdown');
    if (exportBtn && exportMenu) {
        exportBtn.addEventListener('click', (e) => {
            e.stopPropagation();
            exportMenu.classList.toggle('show');
        });
        document.addEventListener('click', () => {
            exportMenu.classList.remove('show');
        });
    }

    // Initial data load
    fetchDashboardData();
    fetchSystemInfo();
    fetchStats();
    fetchErrors();
    fetchExchangeRates();

    // Periodic refresh fallback (even if WS is healthy)
    setInterval(fetchStats, 10_000);
    setInterval(fetchDashboardData, 10_000);
    setInterval(updateLiveHardware, 2000);
    updateLiveHardware();
});