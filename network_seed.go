package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// ============================================================
// Seed Node Discovery Service (:8001)
// ============================================================
//
// Every openmodelpool node runs a Seed endpoint on port 8001.
// This provides decentralized node discovery — no central server needed.
//
// Endpoints:
//   GET /api/peers   — Returns all known nodes from RouteTable
//   GET /health      — Seed health check
//   POST /api/register — Node self-registration (heartbeat)

// SeedPeerInfo is the response format for node discovery
type SeedPeerInfo struct {
	NodeID      string   `json:"node_id"`
	NodeName    string   `json:"node_name"`
	Addresses   []string `json:"addresses"`
	Status      string   `json:"status"`
	Models      []string `json:"models,omitempty"`
	LatencyMS   float64  `json:"latency_ms,omitempty"`
	LoadScore   float64  `json:"load_score,omitempty"`
	LastSeen    int64    `json:"last_seen,omitempty"` // Unix timestamp
	IsGateway   bool     `json:"is_gateway"`
	IsSeed      bool     `json:"is_seed"`
	Region      string   `json:"region,omitempty"`
	Version     string   `json:"version,omitempty"`
}

// SeedRegisterRequest is the request body for node self-registration
type SeedRegisterRequest struct {
	NodeID    string   `json:"node_id"`
	NodeName  string   `json:"node_name"`
	Addresses []string `json:"addresses"`
	Models    []string `json:"models,omitempty"`
	Region    string   `json:"region,omitempty"`
	IsGateway bool     `json:"is_gateway"`
	IsSeed    bool     `json:"is_seed"`
	Version   string   `json:"version,omitempty"`
	Secret    string   `json:"secret"` // shared secret for inter-node auth
}

// SeedPeersResponse is the response for GET /api/peers
type SeedPeersResponse struct {
	Peers    []SeedPeerInfo `json:"peers"`
	Self     *SeedPeerInfo  `json:"self,omitempty"`
	Total    int            `json:"total"`
	Online   int            `json:"online"`
	ServedAt int64          `json:"served_at"` // Unix timestamp
}

// handleSeedPeers returns all known nodes for peer discovery
func handleSeedPeers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if routeTable == nil {
		json.NewEncoder(w).Encode(SeedPeersResponse{
			Peers:    []SeedPeerInfo{},
			Total:    0,
			Online:   0,
			ServedAt: time.Now().Unix(),
		})
		return
	}

	entries := routeTable.GetAll()
	peers := make([]SeedPeerInfo, 0, len(entries))
	onlineCount := 0

	for _, e := range entries {
		// Filter out nodes that haven't been seen in 30 minutes
		if time.Since(e.LastSeen) > 30*time.Minute && !e.LastSeen.IsZero() {
			continue
		}

		peer := SeedPeerInfo{
			NodeID:    e.NodeID,
			NodeName:  e.NodeName,
			Addresses: e.Addresses,
			Status:    e.Status,
			Models:    e.Models,
			LatencyMS: e.LatencyMS,
			LoadScore: e.LoadScore,
			IsGateway: len(e.Models) > 0,
			IsSeed:    true, // every node is a potential seed
			Version:   AppVersion,
		}
		if !e.LastSeen.IsZero() {
			peer.LastSeen = e.LastSeen.Unix()
		}
		peers = append(peers, peer)

		if e.Status == "online" {
			onlineCount++
		}
	}

	// Include self info
	var self *SeedPeerInfo
	selfNodeID := ""
	if netMgr != nil {
		selfNodeID = netMgr.GetNodeID()
	}
	if selfNodeID != "" {
		self = &SeedPeerInfo{
			NodeID:    selfNodeID,
			NodeName:  cfg.Get("node_name", "OpenModelPool"),
			Addresses: getSelfAddresses(),
			Status:    "online",
			IsGateway: true,
			IsSeed:    true,
			Version:   AppVersion,
			LastSeen:  time.Now().Unix(),
		}
	}

	json.NewEncoder(w).Encode(SeedPeersResponse{
		Peers:    peers,
		Self:     self,
		Total:    len(peers),
		Online:   onlineCount,
		ServedAt: time.Now().Unix(),
	})
}

// handleSeedRegister handles POST /api/register — node self-registration
func handleSeedRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req SeedRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.NodeID == "" {
		http.Error(w, `{"error":"node_id required"}`, http.StatusBadRequest)
		return
	}

	// Validate shared secret (if configured)
	expectedSecret := cfg.Get("seed_secret", "")
	if expectedSecret != "" && req.Secret != expectedSecret {
		http.Error(w, `{"error":"invalid secret"}`, http.StatusUnauthorized)
		return
	}

	// Add to route table
	if routeTable != nil {
		routeTable.Put(req.NodeID, req.NodeName, req.Addresses)

		// Update models and gateway info
		entry := routeTable.Get(req.NodeID)
		if entry != nil {
			entry.Models = req.Models
			entry.LastSeen = time.Now()
			entry.Status = "online"
		}
	}

	slog.Info("node registered via seed endpoint",
		"node_id", req.NodeID,
		"node_name", req.NodeName,
		"addresses", req.Addresses,
		"models", req.Models,
		"is_gateway", req.IsGateway,
	)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"node_id": req.NodeID,
		"message": "registered successfully",
	})
}

// handleSeedHealth returns seed endpoint health status
func handleSeedHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	selfNodeID := ""
	if netMgr != nil {
		selfNodeID = netMgr.GetNodeID()
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"version": AppVersion,
		"node_id": selfNodeID,
		"role":    "seed",
	})
}

// getSelfAddresses returns the addresses other nodes can use to reach this node
func getSelfAddresses() []string {
	addrs := []string{}

	// Public URL from config
	publicURL := cfg.Get("public_url", "")
	if publicURL != "" {
		addrs = append(addrs, publicURL)
	}

	// LAN IP
	lanIP := cfg.Get("lan_ip", "")
	if lanIP != "" {
		port := cfg.Get("service_port", "8000")
		addrs = append(addrs, "http://"+lanIP+":"+port)
	}

	return addrs
}

// startSeedServer starts the Seed discovery HTTP server on port 8001
func startSeedServer() {
	seedMux := http.NewServeMux()

	// Node discovery endpoint
	seedMux.HandleFunc("GET /api/peers", handleSeedPeers)

	// Node registration endpoint
	seedMux.HandleFunc("POST /api/register", handleSeedRegister)

	// Health check
	seedMux.HandleFunc("GET /health", handleSeedHealth)
	seedMux.HandleFunc("GET /api/health", handleSeedHealth)

	seedPort := cfg.Get("seed_port", "8001")
	server := &http.Server{
		Addr:         ":" + seedPort,
		Handler:      seedMux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	slog.Info("Seed discovery service started", "port", seedPort)
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("seed server error", "error", err)
		}
	}()
}
