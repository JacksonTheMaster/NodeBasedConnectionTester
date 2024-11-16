package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

type Dashboard struct {
	node *Node
}

func NewDashboard(node *Node) *Dashboard {
	log.Printf("[DEBUG] Creating new dashboard instance")
	return &Dashboard{
		node: node,
	}
}

func (d *Dashboard) Start() {
	// Get port from environment variable or use default
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("[DEBUG] Starting dashboard on port %s", port)

	// Serve static files
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/", fs)
	log.Printf("[DEBUG] Configured static file server from 'static' directory")

	// Configure CORS middleware
	corsMiddleware := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next(w, r)
		}
	}

	// API endpoints with debugging
	http.HandleFunc("/api/results", corsMiddleware(d.handleResults))
	http.HandleFunc("/api/peers", corsMiddleware(d.handlePeers))
	http.HandleFunc("/api/health", corsMiddleware(d.handleHealth))
	log.Printf("[DEBUG] Registered API endpoints: /api/results, /api/peers, /api/health")

	// Start server with timeout configuration
	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	log.Printf("[INFO] Dashboard is now available at http://localhost:%s", port)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("[ERROR] Dashboard server error: %v", err)
	}
}

func (d *Dashboard) handleResults(w http.ResponseWriter, r *http.Request) {
	log.Printf("[DEBUG] Handling /api/results request from %s", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	results := d.node.GetResults()

	log.Printf("[DEBUG] Returning %d results", len(results))

	if err := json.NewEncoder(w).Encode(results); err != nil {
		log.Printf("[ERROR] Failed to encode results: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

func (d *Dashboard) handlePeers(w http.ResponseWriter, r *http.Request) {
	log.Printf("[DEBUG] Handling /api/peers request from %s", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	peers := d.node.GetPeers()

	log.Printf("[DEBUG] Returning %d peers", len(peers))

	if err := json.NewEncoder(w).Encode(peers); err != nil {
		log.Printf("[ERROR] Failed to encode peers: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

func (d *Dashboard) handleHealth(w http.ResponseWriter, r *http.Request) {
	log.Printf("[DEBUG] Handling /api/health request from %s", r.RemoteAddr)

	health := struct {
		Status    string    `json:"status"`
		Timestamp time.Time `json:"timestamp"`
		NodeID    string    `json:"nodeId"`
	}{
		Status:    "healthy",
		Timestamp: time.Now(),
		NodeID:    d.node.ID,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(health); err != nil {
		log.Printf("[ERROR] Failed to encode health status: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}
