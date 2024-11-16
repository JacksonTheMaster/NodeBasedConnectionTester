package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
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
}

// Node represents this instance of the tester
type Node struct {
	Config   *Config
	ID       string
	peers    map[string]*Peer
	results  []TestResult
	listener net.PacketConn
	mu       sync.RWMutex
}

func NewNode(cfg *Config) (*Node, error) {
	log.Printf("[DEBUG] Creating new node with ID: %s", cfg.NodeID)

	// Start iperf server
	if err := startIperfServer(cfg.IperfPort); err != nil {
		return nil, fmt.Errorf("failed to start iperf server: %v", err)
	}

	n := &Node{
		Config:  cfg,
		ID:      cfg.NodeID,
		peers:   make(map[string]*Peer),
		results: make([]TestResult, 0),
	}

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

	// Add a random delay at startup based on node ID to prevent test collisions
	// node1 starts immediately, node2 waits a bit
	if n.ID == "node2" {
		startupDelay := time.Duration(15) * time.Second
		log.Printf("[DEBUG] Node2 waiting %v before starting tests", startupDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(startupDelay):
		}
	}

	iperfTicker := time.NewTicker(time.Duration(n.Config.TestConfig.IperfInterval) * time.Second)
	internetTicker := time.NewTicker(30 * time.Second)
	defer iperfTicker.Stop()
	defer internetTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-iperfTicker.C:
			// Add mutex protection for the entire test cycle
			n.mu.RLock()
			peerCount := len(n.peers)
			activePeers := 0

			// Create a slice of active peers to test
			var activePeerList []*Peer
			for _, peer := range n.peers {
				if peer.IsActive {
					activePeers++
					activePeerList = append(activePeerList, peer)
				}
			}
			n.mu.RUnlock()

			log.Printf("[DEBUG] Running scheduled iperf tests")

			// Run tests sequentially with a small delay between each
			for _, peer := range activePeerList {
				log.Printf("[DEBUG] Running iperf test with peer %s at %s", peer.ID, peer.Address)
				result := runIperfTest(peer, n.Config.IperfPort)
				n.AddResult(result)

				// Small delay between tests
				time.Sleep(2 * time.Second)
			}

			log.Printf("[DEBUG] Completed iperf tests. Total peers: %d, Active: %d", peerCount, activePeers)

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
