package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// globalStopCh is closed during graceful shutdown to signal all background goroutines.
var globalStopCh = make(chan struct{})
var globalStopOnce sync.Once

// closeGlobalStopCh safely closes globalStopCh (idempotent via sync.Once).
func closeGlobalStopCh() {
	globalStopOnce.Do(func() { close(globalStopCh) })
}

// goSafe runs fn in a new goroutine, recovering from panics so a background
// task can never take down the whole process (B6-3: net/http only recovers
// handler goroutines, anything spawned with `go func()` is on its own).
func goSafe(name string, fn func()) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered in background goroutine",
					"task", name, "panic", rec, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

// initCore initializes core components: encryption, config, logging, providers, auth, multi-user.
func initCore() {
	if err := os.MkdirAll("data", 0700); err != nil {
		slog.Error("failed to create data directory", "error", err)
	}

	// Core infrastructure
	initEncryptor("data/.key")
	initConfig("data/config.json")
	// G5: config 文件里 secret_backend=keyring 时，用 keyring 重新解析主密钥
	//（init() 执行时 cfg 尚未加载，只能看到环境变量；首次触发 keyring 迁移）。
	refreshEncryptorForSecretBackend()
	initLogger("data")
	initProviderManager("data/providers.json")
	initTracker("data/usage.json")
	initSiderMonitor("data/sider_token_status.json")
	initAuth("data/admin.json")
	initVMessManager("data")
	initMultiUser("data")

	// SA-08: Fix data directory and file permissions
	os.Chmod("data", 0700) // #nosec G302 -- "data" is a directory; 0700 = owner-only rwx, correct per SA-08
	checkAndFixFilePermissions([]string{
		"data/.key",
		"data/config.json",
		"data/admin.json",
		"data/providers.json",
		"data/sider_token_status.json",
		"data/guest_keys.json",
		"data/invite_store.json",
		// G4: 账本六域 JSON（含 ledger.json 内 ed25519 私钥）。bbolt 为默认后端后
		// 这些文件只剩 .bak，但旧版本/手动放的遗留文件仍需收紧到 0600。
		"data/ledger.json",
		"data/governance.json",
		"data/algorithm_proposals.json",
		"data/contribution_quota.json",
		"data/quota_allocation.json",
		"data/global_pool.json",
	})

	// Guest key store (v2.0)
	initGuestKeyStore("data")

	// Algorithm chain & quota manager (Phase 3)
	initAlgorithmChain("data")
	initAlgorithmGovernance("data")
	initQuotaManager(algoChain)

	// B161: Audit logging for admin actions (zero-log privacy mode via audit_enabled=false)
	initAuditLog("data")

	// Global pool (Phase 4)
	initGlobalPool("data")

	// §10A: WAF four-layer protection
	initWAF("data")

	// §3.2.3: Public key four-layer quota
	initPublicKeyQuota()

	// G6: Cross-pool consumption priority manager (ACCESS_CONTROL.md §4.2)
	initQuotaPriority()

	// Free pool auto-sync (awesome-free-llm-apis)
	initFreePool()

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
	// v4.6.51: use absolute data dir path. The updater writes temp files to
	// dataDir, and relative paths break if the working directory changes
	// during the update process (causing "no such file" errors on read-back).
	dataDir := absDataDir("data")
	initNode(dataDir)
	LoadGenesisConfig(dataDir)
	initFederation(dataDir)
	initGossip()
	initReputation(dataDir)
	initAllocationManager(dataDir)
	initMessages(dataDir)
	initNodeWeightManager(dataDir)
	initInviteManager(dataDir)
	initUpdateManager(dataDir)
	initContributionLedger(dataDir)
	initNATManager()
}

// absDataDir converts a relative data directory to absolute based on the
// executable's location. Falls back to the given path if absolute already
// or if the conversion fails.
func absDataDir(dir string) string {
	if filepath.IsAbs(dir) {
		return dir
	}
	exe, err := os.Executable()
	if err != nil {
		return dir
	}
	base := filepath.Dir(exe)
	abs := filepath.Join(base, dir)
	return abs
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
	// REQ-2 / T6: reconcile federation (and gossip) with the network_enabled
	// single source of truth now that both managers are initialized.
	netMgr.syncFederationToNetwork()

	// 治理（P2-1）：启动时的软提醒（同步、best-effort）。绝不强制加入共享网络——
	// 仅在有闲置自有额度时温和建议。供应方若在 init 时尚未就绪也只是本次不提示，
	// admin 状态接口（/api/network/status）仍会持续展示软提醒。
	logSharedNetworkSoftReminder()
	if gossip == nil {
		initGossip()
	}

	// Phase 4: Region manager & Balance engine
	initRegionManager()
	if regionManager != nil {
		regionManager.AutoDetectSelfRegion()
	}
	initBalanceEngine()

	// Dynamic load balancer (Phase 4)
	initLoadBalancer(context.Background())

	// P1-1(ii): bridge the real UDP DHT transport into production for
	// decentralized node discovery. Runs only when joined to the shared network
	// (network_enabled) and a NodeID is derived. Nil-safe; a bind failure logs
	// and continues without crashing startup.
	startDHTNode()
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
		for {
			select {
			case <-ticker.C:
				if guestKeyStore != nil {
					guestKeyStore.save()
				}
			case <-globalStopCh:
				return
			}
		}
	}()

	// SA-10: Periodic cleanup of stale IP rate limiter entries
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cleanupIPRateLimiters(1 * time.Hour)
			case <-globalStopCh:
				return
			}
		}
	}()

	startConnTrackerCleanup()
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
