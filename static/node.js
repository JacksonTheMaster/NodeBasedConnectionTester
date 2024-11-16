// static/node.js

// Format timestamp to readable string
function formatTime(timestamp) {
    return new Date(timestamp).toLocaleString();
}

// Format numbers to fixed decimal places
function formatNumber(num, decimals = 2) {
    return num ? num.toFixed(decimals) : 'N/A';
}

// Update node status
function updateNodeStatus() {
    fetch('/api/health')
        .then(response => response.json())
        .then(health => {
            const statusHtml = `
                <div class="status-item">
                    <strong>Node ID:</strong> ${health.nodeId}<br>
                    <strong>Status:</strong> ${health.status}<br>
                    <strong>Last Updated:</strong> ${formatTime(health.timestamp)}
                </div>
            `;
            document.getElementById('node-status').innerHTML = statusHtml;
        })
        .catch(error => console.error('Error fetching health:', error));
}

// Update peers list
function updatePeers() {
    fetch('/api/peers')
        .then(response => response.json())
        .then(peers => {
            const peersHtml = Object.entries(peers).map(([id, peer]) => `
                <div class="peer-item ${peer.isActive ? 'active' : 'inactive'}">
                    <strong>${peer.id}</strong> (${peer.address})<br>
                    Last seen: ${formatTime(peer.lastSeen)}
                </div>
            `).join('');
            document.getElementById('peers-list').innerHTML = peersHtml || '<div>No peers connected</div>';
        })
        .catch(error => console.error('Error fetching peers:', error));
}

// Update test results
function updateResults() {
    fetch('/api/results')
        .then(response => response.json())
        .then(results => {
            const recentResults = results.slice(-20).reverse(); // Get last 20 results

            // Process results for bandwidth and latency tables
            const bandwidthHtml = `
                <table>
                    <tr><th>Time</th><th>Target</th><th>Bandwidth (Mbps)</th></tr>
                    ${recentResults
                        .filter(r => r.bandwidth)
                        .slice(0, 5)
                        .map(r => `
                            <tr>
                                <td>${formatTime(r.timestamp)}</td>
                                <td>${r.targetNode}</td>
                                <td>${formatNumber(r.bandwidth)}</td>
                            </tr>
                        `).join('')}
                </table>
            `;
            document.getElementById('bandwidth-table').innerHTML = bandwidthHtml;

            const latencyHtml = `
                <table>
                    <tr><th>Time</th><th>Target</th><th>Latency (ms)</th></tr>
                    ${recentResults
                        .filter(r => r.latency)
                        .slice(0, 5)
                        .map(r => `
                            <tr>
                                <td>${formatTime(r.timestamp)}</td>
                                <td>${r.targetNode}</td>
                                <td>${formatNumber(r.latency)}</td>
                            </tr>
                        `).join('')}
                </table>
            `;
            document.getElementById('latency-table').innerHTML = latencyHtml;

            // Update full results list
            const resultsHtml = recentResults.map(result => `
                <div class="result-item">
                    <div class="result-time">${formatTime(result.timestamp)}</div>
                    <div class="result-type">${result.testType}</div>
                    <div class="result-target">${result.targetNode}</div>
                    <div class="result-metrics">
                        ${result.bandwidth ? `<span>Bandwidth: ${formatNumber(result.bandwidth)} Mbps</span>` : ''}
                        ${result.latency ? `<span>Latency: ${formatNumber(result.latency)} ms</span>` : ''}
                        ${result.packetLoss ? `<span>Packet Loss: ${formatNumber(result.packetLoss)}%</span>` : ''}
                    </div>
                    ${result.error ? `<div class="result-error">Error: ${result.error}</div>` : ''}
                </div>
            `).join('');
            document.getElementById('results-list').innerHTML = resultsHtml;
        })
        .catch(error => console.error('Error fetching results:', error));
}

// Initialize and start periodic updates
function initDashboard() {
    // Initial updates
    updateNodeStatus();
    updatePeers();
    updateResults();

    // Set up periodic updates
    setInterval(updateNodeStatus, 5000);  // Every 5 seconds
    setInterval(updatePeers, 5000);       // Every 5 seconds
    setInterval(updateResults, 5000);      // Every 5 seconds
}

// Start dashboard when page loads
document.addEventListener('DOMContentLoaded', initDashboard);