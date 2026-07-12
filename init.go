package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"
)

// initCore initializes core components: encryption, config, logging, providers, auth, multi-user.
func initCore() {
	os.MkdirAll("data", 0755)

	// Core infrastructure
	initEncryptor("data/.key")
	initConfig("data/config.json")
	initLogger("data")
	initProviderManager("data/providers.json")
	initTracker("data/usage.json")
	initSiderMonitor("data/sider_token_status.json")
	initAuth("data/admin.json")
	initVMessManager("data")
	initMultiUser("data")

	// SA-08: Fix data directory and file permissions
	os.Chmod("data", 0700)
	checkAndFixFilePermissions([]string{
		"data/.key",
		"data/config.json",
		"data/admin.json",
		"data/providers.json",
		"data/sider_token_status.json",
		"data/guest_keys.json",
		"data/invite_store.json",
	})

	// Guest key store (v2.0)
	initGuestKeyStore("data")

	// Algorithm chain & quota manager (Phase 3)
	initAlgorithmChain("data")
	initQuotaManager(algoChain)

	// Global pool (Phase 4)
	initGlobalPool("data")

	// §10A: WAF four-layer protection
	initWAF("data")

	// §3.2.3: Public key four-layer quota
	initPublicKeyQuota()

	// Migrate: re-save to encrypt any plaintext sensitive data
	cfg.save()
	pm.save()
	auth.save()

	// Re-start VMess proxies on startup
	restartVMessProxies()

	// Start health checker (every 5 minutes)
	initHealthChecker(5 * time.Minute)
}

// initAllFederation initializes all federation-related components (v3.0).
func initAllFederation() {
	initNode("data")
	LoadGenesisConfig("data") // Load custom genesis or use compiled-in default
	initFederation("data")
	initGossip()
	initReputation("data")
	initAllocationManager("data")
	initMessages("data")
	initNodeWeightManager("data")
	initInviteManager("data")
}

// initAllNetwork initializes P2P networking, event bus, metrics, rate limiting, and load balancing.
//
// NOTE: DHT (Kademlia) is not yet implemented — the earlier initDHT() call
// referenced an undefined symbol and the DHT package was a non-functional
// placeholder. Network discovery currently relies on the seed/gossip layer.
func initAllNetwork() {
	// Event bus for real-time push
	initEventBus()

	// Metrics collector
	initMetrics()

	// Performance optimization layer (memory monitoring, worker pool, cleanup)
	initPerformance()

	// Rate limiter
	initRateLimiter()

	// P2P shared network manager (Phase 1)
	initNetworkManager("data")
	netMgr.Init()

	// Phase 4: Region manager & Balance engine
	initRegionManager()
	initBalanceEngine()

	// Dynamic load balancer (Phase 4)
	initLoadBalancer(context.Background())
}

// startBackgroundTasks launches long-running goroutines for periodic tasks.
func startBackgroundTasks() {
	// Heartbeat loop (Phase 2)
	startHeartbeatLoop()

	// Phase 4: Region sync & Balance loops
	startRegionSyncLoop()
	StartBalanceLoop(context.Background())

	// Register with bootstrap nodes (Phase 2) — delayed to let tunnel establish
	go func() {
		time.Sleep(3 * time.Second)
		registerWithBootstraps()
	}()

	// Periodic guest key store save (v2.0)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if guestKeyStore != nil {
				guestKeyStore.save()
			}
		}
	}()

	// SA-10: Periodic cleanup of stale IP rate limiter entries
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cleanupIPRateLimiters(1 * time.Hour)
		}
	}()
}

// restartVMessProxies re-starts all VMess proxies on startup.
func restartVMessProxies() {
	for _, p := range pm.GetAll() {
		raw, ok := pm.GetRaw(p.ID)
		if ok && strings.HasPrefix(raw.Proxy, "vmess://") {
			if _, err := ResolveProxy(raw.ID, raw.Proxy); err != nil {
				slog.Warn("failed to re-start VMess proxy on startup", "provider", raw.ID, "error", err)
			} else {
				slog.Info("re-started VMess proxy on startup", "provider", raw.ID)
			}
		}
	}
}
