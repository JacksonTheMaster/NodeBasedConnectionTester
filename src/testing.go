// testing.go
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

// TestResult remains the same as before

// IperfResult represents the parsed output from iperf3
type IperfResult struct {
	End struct {
		SumSent struct {
			BitsPerSecond float64 `json:"bits_per_second"`
			RetransBits   int     `json:"retransmits"`
		} `json:"sum_sent"`
		Streams []struct {
			JitterMS    float64 `json:"jitter_ms"`
			LostPackets int     `json:"lost_packets"`
			PacketsSent int     `json:"packets"`
		} `json:"streams"`
	} `json:"end"`
}

// TestRunner handles all network testing operations
type TestRunner struct {
	mu          sync.Mutex
	activeTests map[string]context.CancelFunc
	iperfServer *exec.Cmd
	serverPort  int
	maxRetries  int
	retryDelay  time.Duration
	testTimeout time.Duration
	resultChan  chan TestResult
}

func NewTestRunner(port int) *TestRunner {
	return &TestRunner{
		activeTests: make(map[string]context.CancelFunc),
		serverPort:  port,
		maxRetries:  3,
		retryDelay:  time.Second * 5,
		testTimeout: time.Second * 30,
		resultChan:  make(chan TestResult, 100),
	}
}

// StartIperfServer starts the iperf server with better error handling
func (tr *TestRunner) StartIperfServer() error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	// Check if server is already running
	if tr.iperfServer != nil {
		return nil
	}

	// Check if port is already in use
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf(":%d", tr.serverPort), time.Second); err == nil {
		conn.Close()
		return fmt.Errorf("port %d is already in use", tr.serverPort)
	}

	// Start iperf server with detailed logging
	cmd := exec.Command("iperf3", "-s", "-p", strconv.Itoa(tr.serverPort))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start iperf server: %v", err)
	}

	// Wait for server to be ready
	time.Sleep(time.Second)

	// Verify server is running
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf(":%d", tr.serverPort), time.Second); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("iperf server failed to start on port %d", tr.serverPort)
	} else {
		conn.Close()
	}

	tr.iperfServer = cmd
	log.Printf("[INFO] Started iperf server on port %d", tr.serverPort)
	return nil
}

// StopIperfServer gracefully stops the iperf server
func (tr *TestRunner) StopIperfServer() error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tr.iperfServer != nil && tr.iperfServer.Process != nil {
		if err := tr.iperfServer.Process.Kill(); err != nil {
			return fmt.Errorf("failed to stop iperf server: %v", err)
		}
		tr.iperfServer = nil
	}
	return nil
}

// RunNetworkTests coordinates all network tests for a peer
func (tr *TestRunner) RunNetworkTests(ctx context.Context, peer *Peer) {
	testID := fmt.Sprintf("%s-%d", peer.ID, time.Now().Unix())

	// Create cancelable context for this test
	testCtx, cancel := context.WithTimeout(ctx, tr.testTimeout)
	defer cancel()

	tr.mu.Lock()
	tr.activeTests[testID] = cancel
	tr.mu.Unlock()

	defer func() {
		tr.mu.Lock()
		delete(tr.activeTests, testID)
		tr.mu.Unlock()
	}()

	// Run ping and iperf tests concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	// Run continuous ping during iperf test
	go func() {
		defer wg.Done()
		pingResult := tr.runContinuousPing(testCtx, peer.ID, peer.Address) // Pass peer.ID
		tr.resultChan <- pingResult
	}()

	// Run iperf test
	go func() {
		defer wg.Done()
		iperfResult := tr.runIperfTest(testCtx, peer)
		tr.resultChan <- iperfResult
	}()

	wg.Wait()
}

// runContinuousPing runs ping throughout the duration of other tests
func (tr *TestRunner) runContinuousPing(ctx context.Context, peerID, target string) TestResult {
	result := TestResult{
		Timestamp:  time.Now(),
		TestType:   "ping",
		TargetNode: peerID, // Use peer ID instead of raw address
	}

	// Extract host from target if it contains port
	host := target
	if strings.Contains(host, ":") {
		host, _, _ = net.SplitHostPort(host)
	}

	// Run ping with timeout
	cmd := exec.CommandContext(ctx, "ping", "-c", "10", "-i", "0.2", host)
	output, err := cmd.Output()

	if err != nil {
		result.Error = fmt.Sprintf("ping error: %v", err)
		return result
	}

	// Parse ping output
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "rtt min/avg/max") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				stats := strings.Split(strings.TrimSpace(parts[1]), "/")
				if len(stats) >= 2 {
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

// runIperfTest runs an iperf test with retries and better error handling
func (tr *TestRunner) runIperfTest(ctx context.Context, peer *Peer) TestResult {
	result := TestResult{
		Timestamp:  time.Now(),
		TestType:   "iperf",
		TargetNode: peer.ID,
	}

	host := peer.Address
	if strings.Contains(host, ":") {
		host, _, _ = net.SplitHostPort(host)
	}

	// Determine target port based on peer ID
	targetPort := tr.serverPort
	if peer.ID == "node2" {
		targetPort = tr.serverPort + 1
	}

	var lastErr error
	for retry := 0; retry < tr.maxRetries; retry++ {
		if retry > 0 {
			log.Printf("[INFO] Retrying iperf test to %s (attempt %d/%d)", peer.ID, retry+1, tr.maxRetries)
			select {
			case <-ctx.Done():
				result.Error = "test cancelled"
				return result
			case <-time.After(tr.retryDelay):
			}
		}

		// Run iperf test
		cmd := exec.CommandContext(ctx, "iperf3",
			"-c", host,
			"-p", strconv.Itoa(targetPort),
			"-J",      // JSON output
			"-t", "5", // 5 second test
			"--connect-timeout", "5000", // 5 second connection timeout
		)

		output, err := cmd.Output()
		if err != nil {
			lastErr = fmt.Errorf("iperf3 error: %v", err)
			if exitErr, ok := err.(*exec.ExitError); ok {
				lastErr = fmt.Errorf("iperf3 error: %v (stderr: %s)", err, string(exitErr.Stderr))
			}
			continue
		}

		// Parse results
		var iperfResult IperfResult
		if err := json.Unmarshal(output, &iperfResult); err != nil {
			lastErr = fmt.Errorf("failed to parse iperf output: %v", err)
			continue
		}

		// Calculate metrics
		result.Bandwidth = iperfResult.End.SumSent.BitsPerSecond / 1_000_000 // Convert to Mbps

		if len(iperfResult.End.Streams) > 0 {
			stream := iperfResult.End.Streams[0]
			if stream.PacketsSent > 0 {
				result.PacketLoss = float64(stream.LostPackets) / float64(stream.PacketsSent) * 100
			}
		}

		log.Printf("[INFO] Successful iperf test to %s: %.2f Mbps", peer.ID, result.Bandwidth)
		return result
	}

	result.Error = lastErr.Error()
	log.Printf("[ERROR] Failed all iperf test attempts to %s: %v", peer.ID, lastErr)
	return result
}

// GetResultChannel returns the channel for receiving test results
func (tr *TestRunner) GetResultChannel() <-chan TestResult {
	return tr.resultChan
}

// CancelAllTests cancels all running tests
func (tr *TestRunner) CancelAllTests() {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	for _, cancel := range tr.activeTests {
		cancel()
	}
	tr.activeTests = make(map[string]context.CancelFunc)
}
