package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func main() {
	// Command line flags
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize node
	node, err := NewNode(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize node: %v", err)
	}

	// Start dashboard
	dashboard := NewDashboard(node)
	go dashboard.Start()

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// WaitGroup for graceful shutdown
	var wg sync.WaitGroup

	// Start node operations
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := node.Start(ctx); err != nil {
			log.Printf("Node error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down...")
	cancel()

	// Wait for all operations to complete
	wg.Wait()

	// Save data before exit
	if err := node.SaveData(); err != nil {
		log.Printf("Error saving data: %v", err)
	}
}
