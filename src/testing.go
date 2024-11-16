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
	"time"
)

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

func startIperfServer(port int) error {
	// Check if iperf3 is already running on this port
	if _, err := net.DialTimeout("tcp", fmt.Sprintf(":%d", port), time.Second); err == nil {
		log.Printf("[DEBUG] iperf server already running on port %d", port)
		return nil
	}

	cmd := exec.Command("iperf3", "-s", "-p", strconv.Itoa(port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start iperf server: %v", err)
	}

	// Wait a moment for the server to start
	time.Sleep(time.Second)

	log.Printf("[DEBUG] Started iperf server on port %d", port)
	return nil
}

func runIperfTest(peer *Peer, port int) TestResult {
	result := TestResult{
		Timestamp:  time.Now(),
		TestType:   "iperf",
		TargetNode: peer.ID,
	}

	// Extract host from peer address
	host := peer.Address
	if strings.Contains(host, ":") {
		host, _, _ = net.SplitHostPort(host)
	}

	// Use the peer's iperf port based on their node ID
	// Node1 uses 5201, Node2 uses 5202
	targetPort := 5201
	if peer.ID == "node2" {
		targetPort = 5202
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	target := fmt.Sprintf("%s:%d", host, targetPort)
	log.Printf("[DEBUG] Running iperf test to %s", target)

	cmd := exec.CommandContext(ctx, "iperf3", "-c", host, "-p", strconv.Itoa(targetPort), "-J", "-t", "5")
	output, err := cmd.Output()

	if err != nil {
		errMsg := fmt.Sprintf("iperf3 error: %v", err)
		if exitErr, ok := err.(*exec.ExitError); ok {
			errMsg += fmt.Sprintf(" (stderr: %s)", string(exitErr.Stderr))
		}
		result.Error = errMsg
		log.Printf("[ERROR] %s", errMsg)
		return result
	}

	// Rest of the function remains the same...
	var iperfResult IperfResult
	if err := json.Unmarshal(output, &iperfResult); err != nil {
		result.Error = fmt.Sprintf("failed to parse iperf output: %v", err)
		log.Printf("[ERROR] %s", result.Error)
		return result
	}

	result.Bandwidth = iperfResult.End.SumSent.BitsPerSecond / 1_000_000

	if len(iperfResult.End.Streams) > 0 {
		stream := iperfResult.End.Streams[0]
		if stream.PacketsSent > 0 {
			result.PacketLoss = float64(stream.LostPackets) / float64(stream.PacketsSent) * 100
			result.Latency = stream.JitterMS
		}
	}

	log.Printf("[DEBUG] iperf test completed: %.2f Mbps", result.Bandwidth)
	return result
}

func runInternetCheck() TestResult {
	result := TestResult{
		Timestamp:  time.Now(),
		TestType:   "internet",
		TargetNode: "8.8.8.8", // Google DNS
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run 5 pings to Google DNS
	cmd := exec.CommandContext(ctx, "ping", "-c", "5", "8.8.8.8")
	output, err := cmd.Output()

	if err != nil {
		result.Error = fmt.Sprintf("internet check error: %v", err)
		return result
	}

	// Parse ping output
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "rtt min/avg/max") {
			// Extract average from "rtt min/avg/max/mdev = 0.123/0.456/0.789/0.012 ms"
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				stats := strings.Split(parts[1], "/")
				if len(stats) >= 2 {
					if avg, err := strconv.ParseFloat(strings.TrimSpace(stats[1]), 64); err == nil {
						result.Latency = avg
					}
				}
			}
		}
		if strings.Contains(line, "packet loss") {
			// Extract packet loss percentage
			parts := strings.Split(line, "%")
			if len(parts) > 0 {
				if pct, err := strconv.ParseFloat(strings.TrimSpace(strings.Fields(parts[0])[len(strings.Fields(parts[0]))-1]), 64); err == nil {
					result.PacketLoss = pct
				}
			}
		}
	}

	return result
}

// runPingTest performs a basic ping test to measure latency
func runPingTest(target string) TestResult {
	result := TestResult{
		Timestamp:  time.Now(),
		TestType:   "ping",
		TargetNode: target,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run 5 pings and average the result
	cmd := exec.CommandContext(ctx, "ping", "-c", "5", target)
	output, err := cmd.Output()

	if err != nil {
		result.Error = fmt.Sprintf("ping error: %v", err)
		return result
	}

	// Parse ping output
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "avg") {
			// Extract average from "rtt min/avg/max/mdev = 0.123/0.456/0.789/0.012 ms"
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				stats := strings.Split(parts[1], "/")
				if len(stats) >= 2 {
					if avg, err := strconv.ParseFloat(strings.TrimSpace(stats[1]), 64); err == nil {
						result.Latency = avg
					}
				}
			}
		}
		if strings.Contains(line, "packet loss") {
			// Extract packet loss percentage
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

// checkNetworkInterfaces returns information about all network interfaces
func checkNetworkInterfaces() []string {
	out, err := exec.Command("ip", "addr").Output()
	if err != nil {
		log.Printf("Failed to get network interfaces: %v", err)
		return nil
	}
	return strings.Split(string(out), "\n")
}

// checkDNSResolution tests DNS resolution
func checkDNSResolution(host string) error {
	_, err := exec.Command("dig", host, "+short").Output()
	return err
}

// Additional helper functions for network diagnostics

// checkTCPConnection tests TCP connection to a specific port
func checkTCPConnection(host string, port int) error {
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	return nil
}

// getMTU returns the MTU of a specific interface
func getMTU(interfaceName string) (int, error) {
	out, err := exec.Command("ip", "link", "show", interfaceName).Output()
	if err != nil {
		return 0, err
	}

	// Parse output to find MTU
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "mtu") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "mtu" && i+1 < len(fields) {
					return strconv.Atoi(fields[i+1])
				}
			}
		}
	}
	return 0, fmt.Errorf("MTU not found")
}
