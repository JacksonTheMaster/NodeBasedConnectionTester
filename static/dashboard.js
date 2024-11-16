// Dashboard state management
const state = {
    currentTab: null,
    nodes: new Map(),
    charts: new Map()
};

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
            tension: 0.4
        }]
    },
    options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: { legend: { display: false } },
        scales: {
            y: {
                beginAtZero: true,
                grid: { color: '#ffffff15' },
                ticks: { color: '#a0a0a0' }
            },
            x: {
                grid: { color: '#ffffff15' },
                ticks: { color: '#a0a0a0' }
            }
        }
    }
});

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
        createChartConfig('line', '#3498db')
    );
    const latencyChart = new Chart(
        content.querySelector('.latency-chart'),
        createChartConfig('line', '#2ecc71')
    );

    state.charts.set(nodeId, { bandwidthChart, latencyChart });
    return content;
};

// Switch active tab
const switchTab = (nodeId) => {
    if (state.currentTab === nodeId) return;
    
    // Update tab buttons
    document.querySelectorAll('.tab-button').forEach(btn => {
        btn.classList.toggle('active', btn.textContent === `Node ${nodeId}`);
    });

    // Update tab contents
    document.querySelectorAll('.tab-content').forEach(content => {
        content.classList.toggle('active', content.id === `tab-${nodeId}`);
    });

    state.currentTab = nodeId;
};

// Update node status display
const updateNodeStatus = (currentNode, peers) => {
    // Update current node
    document.getElementById('currentNodeId').textContent = `Node: ${currentNode}`;

    // Update peer displays
    const peerContainer = document.getElementById('connectedPeers');
    peerContainer.innerHTML = '';

    Object.entries(peers)
        .slice(0, 4)
        .forEach(([id, peer]) => {
            if (peer.isActive) {
                const peerElement = document.createElement('div');
                peerElement.className = 'node-status peer';
                peerElement.innerHTML = `
                    <div class="status-indicator peer"></div>
                    <span>Node: ${id}</span>
                `;
                peerContainer.appendChild(peerElement);
            }
        });
};

// Process and update metrics
const updateMetrics = (nodeId, results) => {
    const nodeResults = results.filter(r => r.sourceNode === nodeId);
    const content = document.getElementById(`tab-${nodeId}`);

    // Filter results by test type
    const iperfResults = nodeResults.filter(r => r.testType === 'iperf');
    const internetResults = nodeResults.filter(r => r.testType === 'internet');

    // Calculate averages only using the filtered results
    const metrics = {
        nodeBandwidth: iperfResults.reduce((acc, curr) => acc + curr.bandwidth, 0) / iperfResults.length || 0,
        nodeLatency: iperfResults.reduce((acc, curr) => acc + (curr.latency || 0), 0) / iperfResults.length || 0,
        internetLatency: internetResults.reduce((acc, curr) => acc + curr.latency, 0) / internetResults.length || 0 // Only using 'internet' testType
    };

    // Update metric cards
    const metricValues = content.querySelectorAll('.metric-value');
    metricValues[0].textContent = metrics.nodeBandwidth.toFixed(2);
    metricValues[2].textContent = metrics.nodeLatency.toFixed(2);
    metricValues[3].textContent = metrics.internetLatency.toFixed(2);

    // Update charts
    const charts = state.charts.get(nodeId);
    if (charts) {
        // Bandwidth chart (using iperf results)
        const bandwidthData = iperfResults.slice(-20).map(r => ({
            x: new Date(r.timestamp).toLocaleTimeString(),
            y: r.bandwidth
        }));
        charts.bandwidthChart.data.labels = bandwidthData.map(d => d.x);
        charts.bandwidthChart.data.datasets[0].data = bandwidthData.map(d => d.y);
        charts.bandwidthChart.update();

        // Latency chart (using internet results only for internet latency)
        const latencyData = internetResults.slice(-20).map(r => ({
            x: new Date(r.timestamp).toLocaleTimeString(),
            y: r.latency
        }));
        charts.latencyChart.data.labels = latencyData.map(d => d.x);
        charts.latencyChart.data.datasets[0].data = latencyData.map(d => d.y);
        charts.latencyChart.update();
    }

    // Update results table
    const tbody = content.querySelector('.results-table');
    tbody.innerHTML = nodeResults.slice(-10).reverse().map(result => `
        <tr>
            <td>${new Date(result.timestamp).toLocaleTimeString()}</td>
            <td><span class="badge badge-${result.testType}">${result.testType}</span></td>
            <td>${result.targetNode}</td>
            <td>${result.bandwidth ? result.bandwidth.toFixed(2) + ' Mbps' : '-'}</td>
            <td>${result.latency ? result.latency.toFixed(2) + ' ms' : '-'}</td>
            <td>${result.packetLoss ? result.packetLoss.toFixed(2) + '%' : '-'}</td>
        </tr>
    `).join('');
};

// Main update function
const updateDashboard = async () => {
    try {
        const [results, health, peers] = await Promise.all([
            fetch('/api/results').then(r => r.json()),
            fetch('/api/health').then(r => r.json()),
            fetch('/api/peers').then(r => r.json())
        ]);

        // Update node status display
        updateNodeStatus(health.nodeId, peers);

        // Initialize tabs for new nodes
        const uniqueNodes = [...new Set([
            health.nodeId,
            ...results.map(r => r.sourceNode),
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

        // Update metrics for each node
        uniqueNodes.forEach(nodeId => {
            updateMetrics(nodeId, results);
        });

    } catch (error) {
        console.error('Error updating dashboard:', error);
    }
};

// Initialize and start periodic updates
const initDashboard = () => {
    updateDashboard();
    setInterval(updateDashboard, 5000);
};

// Start dashboard when page loads
document.addEventListener('DOMContentLoaded', initDashboard);