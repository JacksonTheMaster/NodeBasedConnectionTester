// Dashboard state management
const state = {
    currentTab: null,
    nodes: new Map(),
    charts: new Map(),
    lastUpdate: {
        health: 0,
        metrics: 0
    }
};

// Utility functions
const formatBandwidth = (bw) => bw ? `${bw.toFixed(2)}` : '-';
const formatLatency = (lat) => lat ? `${lat.toFixed(2)}` : '-';
const formatTime = (timestamp) => new Date(timestamp).toLocaleTimeString();

// Chart configuration factory
const createChartConfig = (type, color) => ({
    type: 'line',
    data: {
        labels: [],
        datasets: [{
            data: [],
            borderColor: color,
            backgroundColor: `${color}20`,
            fill: true,
            tension: 0.4,
            pointRadius: 2
        }]
    },
    options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: {
            duration: 750
        },
        plugins: { 
            legend: { display: false },
            tooltip: {
                callbacks: {
                    label: (context) => {
                        const value = context.raw.y;
                        return type === 'bandwidth' 
                            ? `${formatBandwidth(value)} Mbps`
                            : `${formatLatency(value)} ms`;
                    }
                }
            }
        },
        scales: {
            y: {
                beginAtZero: true,
                grid: { color: '#ffffff15' },
                ticks: { 
                    color: '#a0a0a0',
                    callback: (value) => type === 'bandwidth' 
                        ? `${value} Mbps`
                        : `${value} ms`
                }
            },
            x: {
                grid: { color: '#ffffff15' },
                ticks: { color: '#a0a0a0' }
            }
        }
    }
});

// Data fetching functions
const fetchWithTimeout = async (url, timeout = 5000) => {
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), timeout);
    
    try {
        const response = await fetch(url, { signal: controller.signal });
        clearTimeout(timeoutId);
        return await response.json();
    } catch (error) {
        clearTimeout(timeoutId);
        throw error;
    }
};

const fetchNodeResults = async (nodeIPPort) => {
    try {
        return await fetchWithTimeout(`http://${nodeIPPort}/api/results`);
    } catch (error) {
        console.error(`Failed to fetch results from ${nodeIPPort}:`, error);
        return [];
    }
};

// Initialize node tab and content
const initializeNodeTab = (nodeId) => {
    // Create tab button
    const tabButton = document.createElement('button');
    tabButton.className = 'tab-button';
    tabButton.textContent = `Node ${nodeId}`;
    tabButton.onclick = () => switchTab(nodeId);
    document.getElementById('nodeTabs').appendChild(tabButton);

    // Clone and initialize content
    const template = document.querySelector('.tab-content-template');
    const content = template.cloneNode(true);
    content.classList.replace('tab-content-template', 'tab-content');
    content.id = `tab-${nodeId}`;
    content.style.display = null;
    document.getElementById('tabContents').appendChild(content);

    // Initialize charts
    const bandwidthChart = new Chart(
        content.querySelector('.bandwidth-chart'),
        createChartConfig('bandwidth', '#3498db')
    );
    const latencyChart = new Chart(
        content.querySelector('.latency-chart'),
        createChartConfig('latency', '#2ecc71')
    );

    state.charts.set(nodeId, { bandwidthChart, latencyChart });
    return content;
};

// Switch active tab
const switchTab = (nodeId) => {
    if (state.currentTab === nodeId) return;

    // Update tab button styles
    document.querySelectorAll('.tab-button').forEach(btn => {
        btn.classList.toggle('active', btn.textContent === `Node ${nodeId}`);
    });

    // Update tab content visibility
    document.querySelectorAll('.tab-content').forEach(content => {
        content.classList.toggle('active', content.id === `tab-${nodeId}`);
    });

    // Update the currently active tab
    state.currentTab = nodeId;

    // Refresh data for the newly selected tab
    updateNodeMetrics(nodeId);
};

// Update node status display
const updateNodeStatus = (currentNode, peers) => {
    document.getElementById('currentNodeId').textContent = `Node: ${currentNode}`;

    const peerContainer = document.getElementById('connectedPeers');
    peerContainer.innerHTML = '<div class="peers-group"></div>';
    const peersGroup = peerContainer.querySelector('.peers-group');

    Object.entries(peers)
        .filter(([_, peer]) => peer.isActive)
        .slice(0, 4)
        .forEach(([id, peer]) => {
            const peerElement = document.createElement('div');
            peerElement.className = 'node-status peer';
            peerElement.innerHTML = `
                <div class="status-indicator peer"></div>
                <span>Node: ${id}</span>
            `;
            peersGroup.appendChild(peerElement);
        });
};

// Process and update metrics
const updateNodeMetrics = async (nodeId) => {
    try {
        // Fetch results for the specified node
        const results = nodeId === state.currentNode
            ? await fetchWithTimeout('/api/results') // Local API for the current node
            : await fetchNodeResults(state.peers[nodeId]?.nodeIPPort); // Remote node API

        const nodeResults = results.filter(r => r.sourceNode === nodeId);
        const content = document.getElementById(`tab-${nodeId}`);

        // Calculate metrics
        const iperfResults = nodeResults.filter(r => r.testType === 'iperf');
        const pingResults = nodeResults.filter(r => r.testType === 'ping');
        const internetResults = nodeResults.filter(r => r.testType === 'internet');
        const speedtestResults = nodeResults.filter(r => r.testType === 'speedtest');

        const metrics = {
            avgBandwidth: iperfResults.reduce((acc, curr) => acc + curr.bandwidth / 1, 0) / iperfResults.length || 0, // Convert bps to Gbps
            peakBandwidth: Math.max(...iperfResults.map(r => r.bandwidth / 10), 0), // Convert bps to Gbps
            avgNodeLatency: pingResults.reduce((acc, curr) => acc + curr.latency, 0) / pingResults.length || 0, // Latency in ms
            internetLatency: internetResults.reduce((acc, curr) => acc + curr.latency, 0) / internetResults.length || 0, // Latency in ms
            avgDownload: speedtestResults.reduce((acc, curr) => acc + curr.download_mbps / 1_000_00, 0) / speedtestResults.length || 0, // Convert bps to Mbps
            avgUpload: speedtestResults.reduce((acc, curr) => acc + curr.upload_mbps / 1_000_00, 0) / speedtestResults.length || 0, // Convert bps to Mbps
            speedtestLatency: speedtestResults.reduce((acc, curr) => acc + curr.latency, 0) / speedtestResults.length || 0 // Latency in ms
        };

        // Update metric cards
        const metricValues = content.querySelectorAll('.metric-value');
        metricValues[0].textContent = formatBandwidth(metrics.avgBandwidth, 'Gb/s'); // Average Bandwidth in Gbps
        metricValues[1].textContent = formatBandwidth(metrics.peakBandwidth, 'Gb/s'); // Peak Bandwidth in Gbps
        metricValues[2].textContent = formatLatency(metrics.avgNodeLatency); // Average Node Latency in ms
        metricValues[3].textContent = formatLatency(metrics.internetLatency); // Internet Latency in ms
        metricValues[4].textContent = formatBandwidth(metrics.avgDownload, 'Mbit/s'); // Speedtest Avg Download in Mbps
        metricValues[5].textContent = formatBandwidth(metrics.avgUpload, 'Mbit/s'); // Speedtest Avg Upload in Mbps

        // Update charts
        const charts = state.charts.get(nodeId);
        if (charts) {
            // Bandwidth chart (only iperf results)
            const bandwidthData = iperfResults.slice(-20).map(r => ({
                x: formatTime(r.timestamp),
                y: r.bandwidth / 1_000_000_000 // Convert bps to Gbps
            }));

            charts.bandwidthChart.data.labels = bandwidthData.map(d => d.x);
            charts.bandwidthChart.data.datasets[0].data = bandwidthData.map(d => d.y);
            charts.bandwidthChart.update('none'); // Use 'none' for smoother updates
            charts.bandwidthChart.options.scales.x.ticks.callback = function(value, index, values) {
                const label = this.getLabelForValue(value);
                // Truncate or format the label (e.g., display only the first 5 characters)
                return label.length > 5 ? `${label.substring(0, 5)}...` : label;
            };
            
            charts.bandwidthChart.options.scales.y.ticks.callback = function(value, index, values) {
                // Format y-axis values (e.g., limit decimal points to 2)
                return parseFloat(value).toFixed(2) + ' Mbps';
            };
            
            // Apply the updated configuration and update the chart
            charts.bandwidthChart.update();
            // Latency chart (combine internet and speedtest latency)
            const latencyData = [...speedtestResults]
                .sort((a, b) => new Date(a.timestamp) - new Date(b.timestamp))
                .slice(-20)
                .map(r => ({
                    x: formatTime(r.timestamp),
                    y: r.latency
                }));

            charts.latencyChart.data.labels = latencyData.map(d => d.x);
            charts.latencyChart.data.datasets[0].data = latencyData.map(d => d.y);
            charts.latencyChart.update('none');
        }

        // Update results table
        const tbody = content.querySelector('.results-table tbody');
        tbody.innerHTML = nodeResults
            .slice(-10)
            .reverse()
            .map(result => `
                <tr>
                    <td>${formatTime(result.timestamp)}</td>
                    <td><span class="badge badge-${result.testType}">${result.testType}</span></td>
                    <td>${result.targetNode || '-'}</td>
                    <td>${
                        result.testType === 'speedtest' 
                            ? `↓${formatBandwidth(result.download_mbps / 1_000_000, 'Mbps')} Mbps ↑${formatBandwidth(result.upload_mbps / 1_000_000, 'Mbps')} Mbps` 
                            : `${formatBandwidth(result.bandwidth / 1_000_000_000, 'Gbps')} Gbps` // Convert bps to Gbps
                    }</td>
                    <td>${formatLatency(result.latency)} ${result.latency ? 'ms' : ''}</td>
                    <td>${result.packetLoss ? result.packetLoss.toFixed(2) + '%' : '-'}</td>
                </tr>
            `)
            .join('');

    } catch (error) {
        console.error('Error updating metrics:', error);
    }
};



// Main dashboard update function
const updateDashboard = async () => {
    try {
        // Fast update cycle (node health status)
        const currentTime = Date.now();
        if (currentTime - state.lastUpdate.health > 2000) { // Update every 2 seconds
            const [health, peers] = await Promise.all([
                fetchWithTimeout('/api/health'),
                fetchWithTimeout('/api/peers')
            ]);

            state.currentNode = health.nodeId;
            state.peers = peers;
            updateNodeStatus(health.nodeId, peers);
            
            // Initialize tabs for new nodes
            const uniqueNodes = [...new Set([
                health.nodeId,
                ...Object.keys(peers)
            ])];

            uniqueNodes.forEach(nodeId => {
                if (!state.nodes.has(nodeId)) {
                    state.nodes.set(nodeId, initializeNodeTab(nodeId));
                    if (!state.currentTab) {
                        switchTab(nodeId);
                    }
                }
            });

            state.lastUpdate.health = currentTime;
        }

        // Slower update cycle (metrics)
        if (currentTime - state.lastUpdate.metrics > 5000) { // Update every 5 seconds
            if (state.currentTab) {
                await updateNodeMetrics(state.currentTab);
            }
            state.lastUpdate.metrics = currentTime;
        }

    } catch (error) {
        console.error('Error updating dashboard:', error);
    }
};

// Initialize and start periodic updates
const initDashboard = () => {
    updateDashboard();
    setInterval(updateDashboard, 1000); // Run every second but internal checks control actual update frequency
};

// Start dashboard when page loads
document.addEventListener('DOMContentLoaded', initDashboard);