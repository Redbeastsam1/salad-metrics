let isDarkMode = JSON.parse(localStorage.getItem('isDarkMode'));
if (isDarkMode === null) isDarkMode = true;
let currentHours = 24;

// --- Socket.IO Integration ---
const socket = io();
socket.on('data_updated', () => {
    fetchDashboardData();
    fetchStats();
    fetchErrors();
});

// --- Initialization ---
document.addEventListener('DOMContentLoaded', () => {
    applyTheme();
    initTimeSelectors();
    fetchDashboardData();
    fetchSystemInfo();
    fetchStats();
    fetchErrors();
    
    setInterval(fetchStats, 60000); 
    setInterval(updateLiveHardware, 3000); // Live HW updates every 3s
    updateLiveHardware();
});

function initTimeSelectors() {
    const dropdownItems = document.querySelectorAll('.dropdown-item');
    const dropdownButton = document.getElementById('timeRangeDropdown');

    dropdownItems.forEach(item => {
        item.addEventListener('click', (e) => {
            e.preventDefault(); // Prevents the page from jumping to the top

            
            dropdownItems.forEach(b => b.classList.remove('active'));
            e.target.classList.add('active');

            
            dropdownButton.innerText = e.target.innerText;

            // Grab the hours data attribute and fetch the new graph data!
            currentHours = parseInt(e.target.getAttribute('data-hours'));
            fetchDashboardData();
        });
    });
}

// --- Theme Management ---
const themeToggle = document.getElementById('theme-toggle');
const themeIcon = document.getElementById('theme-icon');

function applyTheme() {
    document.documentElement.setAttribute('data-theme', isDarkMode ? 'dark' : 'light');
    if (isDarkMode) {
        themeIcon.innerHTML = '<circle cx="12" cy="12" r="5"></circle><line x1="12" y1="1" x2="12" y2="3"></line><line x1="12" y1="21" x2="12" y2="23"></line><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"></line><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"></line><line x1="1" y1="12" x2="3" y2="12"></line><line x1="21" y1="12" x2="23" y2="12"></line><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"></line><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"></line>';
    } else {
        themeIcon.innerHTML = '<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"></path>';
    }
    updateChartColors();
}

themeToggle.addEventListener('click', () => {
    isDarkMode = !isDarkMode;
    localStorage.setItem('isDarkMode', isDarkMode);
    applyTheme();
});

// --- Chart Configuration ---
const getChartTextColor = () => isDarkMode ? '#e0e0e0' : '#495057';
const getChartGridColor = () => isDarkMode ? '#333333' : '#e9ecef';

const baseChartOptions = {
    responsive: true, maintainAspectRatio: false,
    scales: {
        x: { type: 'time', time: { unit: 'minute' }, grid: { color: getChartGridColor() } },
        y: { beginAtZero: true, grid: { color: getChartGridColor() } }
    },
    plugins: { tooltip: { mode: 'nearest', intersect: false }, legend: { display: false } },
    elements: { point: { radius: 0, hitRadius: 10 }, line: { tension: 0.2, borderWidth: 2 } },
    interaction: { mode: 'index', intersect: false }
};

const walletChart = new Chart(document.getElementById('walletChart'), {
    type: 'line', data: { datasets: [] }, options: { ...baseChartOptions, plugins: { ...baseChartOptions.plugins, title: { display: true, text: 'Wallet Balance ($)' } } }
});
const containerChart = new Chart(document.getElementById('containerChart'), {
    type: 'line', data: { datasets: [] }, options: { ...baseChartOptions, plugins: { ...baseChartOptions.plugins, title: { display: true, text: 'Container Earnings ($/24h)' } } }
});
const bandwidthChart = new Chart(document.getElementById('bandwidthChart'), {
    type: 'line', data: { datasets: [] }, options: { ...baseChartOptions, plugins: { ...baseChartOptions.plugins, title: { display: true, text: 'Bandwidth (MB/s)' } } }
});

function updateChartColors() {
    const textColor = getChartTextColor();
    const gridColor = getChartGridColor();
    [walletChart, containerChart, bandwidthChart].forEach(chart => {
        if (chart) {
            chart.options.scales.x.ticks.color = textColor; chart.options.scales.x.grid.color = gridColor;
            chart.options.scales.y.ticks.color = textColor; chart.options.scales.y.grid.color = gridColor;
            chart.options.plugins.title.color = textColor;
            chart.update();
        }
    });
}

function hexToRgba(hex, alpha) {
    let c = hex.substring(1).split('');
    if(c.length== 3){ c= [c[0], c[0], c[1], c[1], c[2], c[2]]; }
    c= '0x'+c.join('');
    return 'rgba('+[(c>>16)&255, (c>>8)&255, c&255].join(',')+','+alpha+')';
}

// --- Data Fetching ---
async function fetchSystemInfo() {
    try {
        const sysRes = await fetch('/api/system-info');
        const sysInfo = await sysRes.json();
        document.getElementById('sys-cpu').innerText = sysInfo.CPU;
        document.getElementById('sys-gpu').innerText = sysInfo.GPU;
        document.getElementById('sys-ram').innerText = sysInfo.RAM;
        document.getElementById('sys-os').innerText = sysInfo.OS;
    } catch(e) {}
}

async function updateLiveHardware() {
    try {
        const res = await fetch('/api/hardware-live');
        const data = await res.json();
        
        document.getElementById('live-cpu-val').innerText = `${data.cpu_load.toFixed(1)}%`;
        document.getElementById('live-cpu-bar').style.width = `${data.cpu_load}%`;
        
        document.getElementById('live-ram-val').innerText = `${data.ram_load.toFixed(1)}%`;
        document.getElementById('live-ram-bar').style.width = `${data.ram_load}%`;
        
        document.getElementById('live-gpu-val').innerText = `${data.gpu_load.toFixed(1)}% (${data.gpu_temp}°C)`;
        document.getElementById('live-gpu-bar').style.width = `${data.gpu_load}%`;
    } catch (err) {}
}

async function fetchStats() {
    try {
        const res = await fetch('/api/stats');
        const stats = await res.json();
        document.getElementById('session-uptime').innerText = stats.uptime;
        document.getElementById('session-earnings').innerText = `+$${stats.session_earnings.toFixed(4)}`;
    } catch(e) {}
}

async function fetchDashboardData() {
    try {
        const res = await fetch(`/api/data?hours=${currentHours}`);
        const data = await res.json();
        
        // 1. Wallet Data
        if (data.wallet.length > 0) {
            document.getElementById('wallet-balance-text').innerText = `$${data.wallet[data.wallet.length - 1].current.toFixed(2)}`;
            walletChart.data.datasets = [{
                label: 'Wallet Balance ($)',
                data: data.wallet.map(d => ({ x: new Date(d.timestamp), y: d.current })),
                borderColor: '#28a745', backgroundColor: 'rgba(40, 167, 69, 0.1)', fill: true
            }];
            walletChart.update();
        }

        // 2. Bandwidth Data
        if (data.bandwidth.length > 0) {
            bandwidthChart.data.datasets = [{
                label: 'Bandwidth (MB/s)',
                data: data.bandwidth.map(d => ({ x: new Date(d.timestamp), y: d.throughput })),
                borderColor: '#17a2b8', backgroundColor: 'rgba(23, 162, 184, 0.1)', fill: true
            }];
            bandwidthChart.update();
        }

        // 3. Container Data
        const containersMap = {}; 
        data.earnings.forEach(d => {
            const id = d.container_id;
            const ts = new Date(d.timestamp);
            if (!containersMap[id]) containersMap[id] = { data: [], start: ts, predictedRate: 0 };
            containersMap[id].data.push({ x: ts, y: d.rate });
            containersMap[id].predictedRate = d.rate;
            if (ts < containersMap[id].start) containersMap[id].start = ts;
        });

        const containerDatasets = [];
        const historyRuns = [];
        const uniformColor = '#0d6efd';

        Object.keys(containersMap).forEach(id => {
            const cData = containersMap[id];
            containerDatasets.push({
                label: `Container ${id.substring(0,8)}`,
                data: cData.data, borderColor: uniformColor, backgroundColor: hexToRgba(uniformColor, 0.2), fill: true
            });
            historyRuns.push({ id, start: cData.start, predictedRate: cData.predictedRate, color: uniformColor });
        });

        containerChart.data.datasets = containerDatasets;
        containerChart.update();
        renderContainerHistory(historyRuns);

    } catch (error) { console.error("Error fetching data:", error); }
}

function renderContainerHistory(runs) {
    const historyContainer = document.getElementById('history-container');
    historyContainer.innerHTML = '';
    
    if (runs.length === 0) {
        historyContainer.innerHTML = '<p class="text-muted text-center mt-4">No containers in this time range.</p>';
        return;
    }

    runs.sort((a, b) => b.start - a.start);
    runs.forEach(run => {
        const startStr = run.start.toLocaleTimeString([], {hour: '2-digit', minute:'2-digit'});
        const dateStr = run.start.toLocaleDateString();

        const html = `
            <div class="card mb-3 shadow-sm border-secondary border-opacity-25" style="background-color: var(--bg-color); border-left: 4px solid ${run.color} !important;">
                <div class="card-body p-3">
                    <div class="d-flex justify-content-between align-items-center mb-1">
                        <span class="fw-bold text-truncate" style="font-size: 0.9rem;" title="${run.id}">
                            Container ${run.id.substring(0, 8)}...
                        </span>
                        <span class="fw-bold text-success" style="font-size: 0.85rem;">
                            $${run.predictedRate.toFixed(4)} / 24h
                        </span>
                    </div>
                    <div class="text-muted small"><strong>Started:</strong> ${dateStr} at ${startStr}</div>
                </div>
            </div>`;
        historyContainer.insertAdjacentHTML('beforeend', html);
    });
}

// --- Notifications & Errors ---
let notifiedErrors = new Set();
let isFirstErrorLoad = true; // Tracks if this is the initial page load

function showNotification(title, message, isError = false) {
    const container = document.getElementById('toast-container');
    const toastEl = document.createElement('div');
    const colorClass = isError ? 'text-danger' : 'text-primary';
    const borderClass = isError ? 'border-danger border-opacity-50' : 'border-primary border-opacity-25';
    
    toastEl.className = `toast align-items-center ${borderClass} mb-3`;
    toastEl.innerHTML = `
        <div class="toast-header border-bottom border-secondary border-opacity-25" style="background-color: var(--card-bg); color: var(--text-color);">
            <strong class="me-auto ${colorClass}">${isError ? '⚠ ' : '🔔 '}${title}</strong>
            <button type="button" class="btn-close" data-bs-dismiss="toast"></button>
        </div>
        <div class="toast-body small text-muted" style="background-color: var(--bg-color);">${message}</div>`;
    
    container.appendChild(toastEl);
    const toast = new bootstrap.Toast(toastEl, { autohide: false });
    toast.show();
    toastEl.addEventListener('hidden.bs.toast', () => toastEl.remove());
}

async function fetchErrors() {
    try {
        const res = await fetch('/api/errors');
        const errors = await res.json();
        const errorsContainer = document.getElementById('errors-container');
        const errorSection = document.getElementById('error-section');
        
        errorsContainer.innerHTML = '';

        if (errors.length > 0) {
            errorSection.style.display = 'block';
            errors.forEach(error => {
                if (!notifiedErrors.has(error.timestamp)) {
                    // NEW: Only show the popup toast if it is NOT the first page load
                    if (!isFirstErrorLoad) {
                        showNotification('Workload Failure', error.message, true);
                    }
                    notifiedErrors.add(error.timestamp);
                }

                const diffMin = Math.floor((new Date() - new Date(error.timestamp)) / 60000);
                const ago = diffMin > 60 ? `${Math.floor(diffMin/60)}h ago` : `${diffMin}m ago`;
                
                const content = document.createElement('div');
                content.innerHTML = `
                    <div class="card bg-danger bg-opacity-10 border-danger border-opacity-25 mb-3 shadow-sm">
                        <div class="card-body p-3">
                            <div class="d-flex justify-content-between align-items-start mb-2">
                                <h6 class="card-title text-danger fw-bold mb-0" style="font-size: 0.9rem;">Workload Failure</h6>
                                <span class="badge bg-danger bg-opacity-25 text-danger border border-danger border-opacity-50">${ago}</span>
                            </div>
                            <p class="card-text small mb-2 text-muted" style="line-height: 1.4; word-break: break-all;">${error.message}</p>
                            <button class="btn btn-sm btn-outline-danger w-100 dismiss-btn" data-ts="${error.timestamp}">Dismiss</button>
                        </div>
                    </div>`;
                errorsContainer.appendChild(content);
            });

            document.querySelectorAll('.dismiss-btn').forEach(btn => {
                btn.addEventListener('click', async (e) => {
                    const ts = e.target.getAttribute('data-ts');
                    await fetch('/api/dismiss-error', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ timestamp: ts }) });
                    fetchErrors();
                });
            });
        } else {
            errorSection.style.display = 'none';
        }
        
        // Mark the first load as complete so future errors trigger toasts
        isFirstErrorLoad = false; 

    } catch (err) {
        console.error("Failed to fetch errors:", err);
    }
}