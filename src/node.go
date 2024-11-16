package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TestResult represents a single test result
type TestResult struct {
	Timestamp  time.Time `json:"timestamp"`
	TestType   string    `json:"testType"` // "iperf", "mlab", "ping"
	SourceNode string    `json:"sourceNode"`
	TargetNode string    `json:"targetNode"`
	Bandwidth  float64   `json:"bandwidth"`  // Mbps
	Latency    float64   `json:"latency"`    // ms
	PacketLoss float64   `json:"packetLoss"` // percentage
	Error      string    `json:"error,omitempty"`
}

// Peer represents another node in the network
type Peer struct {
	ID       string    `json:"id"`
	Address  string    `json:"address"`
	LastSeen time.Time `json:"lastSeen"`
	IsActive bool      `json:"isActive"`
	NodeIP   string    `json:"nodeIPPort"`
}

// Node represents this instance of the tester
type Node struct {
	Config     *Config
	ID         string
	NodeIP     string // Add this field
	peers      map[string]*Peer
	results    []TestResult
	listener   net.PacketConn
	testRunner *TestRunner
	mu         sync.RWMutex
}

func NewNode(cfg *Config) (*Node, error) {
	testRunner := NewTestRunner(cfg.IperfPort)
	if err := testRunner.StartIperfServer(); err != nil {
		return nil, fmt.Errorf("failed to start iperf server: %v", err)
	}

	n := &Node{
		Config:     cfg,
		ID:         cfg.NodeID,
		NodeIP:     cfg.NodeIP, // Populate NodeIP from the Config
		peers:      make(map[string]*Peer),
		results:    make([]TestResult, 0),
		testRunner: testRunner,
	}
	log.Printf("[DEBUG] Node initialized: ID=%s, IP=%s", n.ID, n.NodeIP)
	// Try to load previous results
	if err := n.LoadData(); err != nil {
		log.Printf("[DEBUG] No previous data found or error loading: %v", err)
	} else {
		log.Printf("[DEBUG] Loaded %d previous results", len(n.results))
	}

	return n, nil
}

// Start begins node operations
func (n *Node) Start(ctx context.Context) error {
	log.Printf("[DEBUG] Starting node operations on %s", n.Config.ListenAddr)

	// Start UDP listener for peer discovery
	listener, err := net.ListenPacket("udp", n.Config.ListenAddr)
	if err != nil {
		log.Printf("[ERROR] Failed to start UDP listener: %v", err)
		return err
	}
	n.listener = listener
	defer n.listener.Close()

	// Start discovery and testing goroutines
	var wg sync.WaitGroup
	wg.Add(3)

	// Peer discovery
	go func() {
		defer wg.Done()
		log.Printf("[DEBUG] Starting peer discovery routine")
		n.runDiscovery(ctx)
	}()

	// Test scheduler
	go func() {
		defer wg.Done()
		log.Printf("[DEBUG] Starting test scheduler routine")
		n.runTests(ctx)
	}()

	// Peer heartbeat
	go func() {
		defer wg.Done()
		log.Printf("[DEBUG] Starting peer heartbeat routine")
		n.runHeartbeat(ctx)
	}()

	// Wait for context cancellation
	<-ctx.Done()
	log.Printf("[DEBUG] Context cancelled, shutting down node operations")
	wg.Wait()
	return nil
}

// runTests schedules and executes tests
func (n *Node) runTests(ctx context.Context) {
	log.Printf("[DEBUG] Test scheduler starting with iperf interval: %d seconds", n.Config.TestConfig.IperfInterval)

	// Add initial delay for node2 to prevent test collisions
	if n.ID == "node2" {
		startupDelay := time.Duration(15) * time.Second
		log.Printf("[DEBUG] Node2 waiting %v before starting tests", startupDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(startupDelay):
		}
	}

	// Create tickers for different test types
	iperfTicker := time.NewTicker(time.Duration(n.Config.TestConfig.IperfInterval) * time.Second)
	internetTicker := time.NewTicker(30 * time.Second)
	defer iperfTicker.Stop()
	defer internetTicker.Stop()

	// Start result collector in a single goroutine
	resultDone := make(chan struct{})
	go func() {
		defer close(resultDone)
		for {
			select {
			case <-ctx.Done():
				return
			case result, ok := <-n.testRunner.GetResultChannel():
				if !ok {
					return
				}
				n.AddResult(result)
				log.Printf("[DEBUG] Recorded test result: type=%s, target=%s, bandwidth=%.2f, latency=%.2f",
					result.TestType, result.TargetNode, result.Bandwidth, result.Latency)
			}
		}
	}()

	// Test execution loop
	for {
		select {
		case <-ctx.Done():
			// Wait for result collector to finish
			<-resultDone
			return

		case <-iperfTicker.C:
			// Get active peers under mutex protection
			n.mu.RLock()
			var activePeerList []*Peer
			for _, peer := range n.peers {
				if peer.IsActive {
					activePeerList = append(activePeerList, peer)
				}
			}
			peerCount := len(n.peers)
			activePeers := len(activePeerList)
			n.mu.RUnlock()

			if len(activePeerList) == 0 {
				log.Printf("[DEBUG] No active peers to test")
				continue
			}

			log.Printf("[DEBUG] Starting network tests with %d active peers", len(activePeerList))

			// Create a context with timeout for this test cycle
			testCtx, cancel := context.WithTimeout(ctx, time.Minute)

			// Run tests for each peer with proper spacing
			for i, peer := range activePeerList {
				select {
				case <-testCtx.Done():
					log.Printf("[WARN] Test cycle cancelled before completion")
					cancel()
					continue
				default:
					log.Printf("[DEBUG] Running network tests with peer %s at %s (%d/%d)",
						peer.ID, peer.Address, i+1, len(activePeerList))

					// Run the tests
					n.testRunner.RunNetworkTests(testCtx, peer)

					// Add delay between peer tests to prevent network congestion
					if i < len(activePeerList)-1 { // Don't delay after last peer
						select {
						case <-testCtx.Done():
							continue
						case <-time.After(2 * time.Second):
						}
					}
				}
			}

			cancel() // Clean up the test cycle context
			log.Printf("[DEBUG] Completed network tests. Total peers: %d, Active: %d", peerCount, activePeers)

		case <-internetTicker.C:
			log.Printf("[DEBUG] Running internet connectivity check")
			result := runInternetCheck()
			n.AddResult(result)
			if result.Error != "" {
				log.Printf("[ERROR] Internet check failed: %s", result.Error)
			} else {
				log.Printf("[DEBUG] Internet check successful: %.2f ms latency", result.Latency)
			}
		}
	}
}

// runInternetCheck performs internet connectivity test
func runInternetCheck() TestResult {
	result := TestResult{
		Timestamp:  time.Now(),
		TestType:   "internet",
		TargetNode: "8.8.8.8", // Google DNS
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run 5 pings to Google DNS
	cmd := exec.CommandContext(ctx, "ping", "-c", "5", "-i", "0.2", "8.8.8.8")
	output, err := cmd.Output()

	if err != nil {
		result.Error = fmt.Sprintf("internet check error: %v", err)
		return result
	}

	// Parse ping output with more detailed metrics
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "rtt min/avg/max") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				stats := strings.Split(strings.TrimSpace(parts[1]), "/")
				if len(stats) >= 4 {
					if avg, err := strconv.ParseFloat(strings.TrimSpace(stats[1]), 64); err == nil {
						result.Latency = avg
					}
				}
			}
		}
		if strings.Contains(line, "packet loss") {
			parts := strings.Split(line, ",")
			for _, part := range parts {
				if strings.Contains(part, "packet loss") {
					if pct, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(part), "% packet loss"), 64); err == nil {
						result.PacketLoss = pct
					}
				}
			}
		}
	}

	return result
}

// AddResult adds a test result to the history
func (n *Node) AddResult(result TestResult) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Set source node if not set
	if result.SourceNode == "" {
		result.SourceNode = n.ID
	}

	n.results = append(n.results, result)
	log.Printf("[DEBUG] Added new test result: type=%s, target=%s, bandwidth=%.2f, latency=%.2f",
		result.TestType, result.TargetNode, result.Bandwidth, result.Latency)
}

// SaveData saves current results to disk
func (n *Node) SaveData() error {
	n.mu.RLock()
	defer n.mu.RUnlock()

	log.Printf("[DEBUG] Saving %d results to %s", len(n.results), n.Config.DataFile)
	data, err := json.MarshalIndent(n.results, "", "  ")
	if err != nil {
		log.Printf("[ERROR] Failed to marshal results: %v", err)
		return err
	}

	if err := os.WriteFile(n.Config.DataFile, data, 0644); err != nil {
		log.Printf("[ERROR] Failed to write results to file: %v", err)
		return err
	}

	log.Printf("[DEBUG] Successfully saved results to disk")
	return nil
}

// LoadData loads previous results from disk
func (n *Node) LoadData() error {
	log.Printf("[DEBUG] Attempting to load data from %s", n.Config.DataFile)

	data, err := os.ReadFile(n.Config.DataFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[DEBUG] No existing data file found")
		} else {
			log.Printf("[ERROR] Failed to read data file: %v", err)
		}
		return err
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	if err := json.Unmarshal(data, &n.results); err != nil {
		log.Printf("[ERROR] Failed to unmarshal results: %v", err)
		return err
	}

	log.Printf("[DEBUG] Successfully loaded %d results from disk", len(n.results))
	return nil
}

// GetResults returns a copy of current results
func (n *Node) GetResults() []TestResult {
	n.mu.RLock()
	defer n.mu.RUnlock()

	results := make([]TestResult, len(n.results))
	copy(results, n.results)
	log.Printf("[DEBUG] Returning %d results", len(results))
	return results
}

// GetPeers returns a copy of current peers
func (n *Node) GetPeers() map[string]*Peer {
	n.mu.RLock()
	defer n.mu.RUnlock()

	peers := make(map[string]*Peer)
	for k, v := range n.peers {
		peers[k] = v
	}
	log.Printf("[DEBUG] Returning %d peers", len(peers))
	return peers
}

func (n *Node) runDiscovery(ctx context.Context) {
	buffer := make([]byte, 1024)
	log.Printf("[DEBUG] Starting discovery service on %s", n.Config.ListenAddr)

	// Broadcast presence to known peers immediately
	n.broadcastPresence()

	discoveryTicker := time.NewTicker(30 * time.Second)
	defer discoveryTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-discoveryTicker.C:
			n.broadcastPresence()
		default:
			// Set read deadline to prevent blocking forever
			n.listener.SetReadDeadline(time.Now().Add(time.Second))

			bytes, addr, err := n.listener.ReadFrom(buffer)
			if err != nil {
				if !os.IsTimeout(err) {
					log.Printf("[ERROR] Discovery read error: %v", err)
				}
				continue
			}

			// Process discovery message
			msg := make([]byte, bytes)
			copy(msg, buffer[:bytes])

			if err := n.handleDiscoveryMessage(msg, addr); err != nil {
				log.Printf("[ERROR] Failed to handle discovery message: %v", err)
			}
		}
	}
}

// broadcastPresence sends discovery message to known peers
func (n *Node) broadcastPresence() {
	msg := &DiscoveryMessage{
		NodeID:    n.ID,
		Timestamp: time.Now(),
		Type:      "presence",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ERROR] Failed to marshal discovery message: %v", err)
		return
	}

	log.Printf("[DEBUG] Broadcasting presence to known peers")

	for _, peer := range n.Config.KnownPeers {
		addr, err := net.ResolveUDPAddr("udp", peer)
		if err != nil {
			log.Printf("[ERROR] Failed to resolve peer address %s: %v", peer, err)
			continue
		}

		_, err = n.listener.WriteTo(data, addr)
		if err != nil {
			log.Printf("[ERROR] Failed to send presence to %s: %v", peer, err)
		} else {
			log.Printf("[DEBUG] Sent presence to %s", peer)
		}
	}
}

// DiscoveryMessage represents a peer discovery message
type DiscoveryMessage struct {
	NodeID    string    `json:"nodeId"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
}

// handleDiscoveryMessage processes incoming discovery messages
func (n *Node) handleDiscoveryMessage(data []byte, addr net.Addr) error {
	var msg DiscoveryMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal discovery message: %v", err)
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// Update peer information
	peer, exists := n.peers[msg.NodeID]
	if !exists {
		peer = &Peer{
			ID:       msg.NodeID,
			Address:  addr.String(),
			IsActive: true,
			NodeIP:   n.NodeIP, // Populate the NodeIP
		}
		n.peers[msg.NodeID] = peer
		log.Printf("[DEBUG] Discovered new peer: %s at %s", msg.NodeID, addr.String())
	}

	peer.LastSeen = msg.Timestamp
	peer.IsActive = true

	return nil
}

// runHeartbeat sends periodic heartbeats to peers
func (n *Node) runHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	log.Printf("[DEBUG] Starting heartbeat service")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.mu.RLock()
			activePeers := 0
			for id, peer := range n.peers {
				// Mark peers as inactive if not seen in the last 30 seconds
				if time.Since(peer.LastSeen) > 30*time.Second {
					peer.IsActive = false
					log.Printf("[DEBUG] Peer %s marked as inactive", id)
				} else {
					activePeers++
				}
			}
			n.mu.RUnlock()
			log.Printf("[DEBUG] Heartbeat check complete. Active peers: %d", activePeers)
		}
	}
}
