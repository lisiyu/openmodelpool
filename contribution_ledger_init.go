package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var contributionLedger *GossipLedger
var capabilityVerifier *CapabilityVerifier

// ledgerDataDir 是账本 JSON 路径的 dataDir（修掉原来硬编码 "data/ledger.json"）。
var ledgerDataDir = "data"

func initContributionLedger(dataDir string) {
	selfID := "unknown"
	if node != nil {
		selfID = node.NodeID()
	}
	ledgerDataDir = dataDir

	// G4: bbolt 后端。open/import 失败则降级走 JSON，保证启动不中断。
	if storageUseBbolt() {
		if h, err := openBbolt(dataDir); err == nil {
			if gl, err := importLedgerToBbolt(h, selfID, dataDir); err == nil {
				contributionLedger = gl
				slog.Info("contribution ledger loaded from bbolt",
					"peer_id", gl.PeerID(), "records", gl.Count())
			} else {
				slog.Warn("ledger bbolt import failed, falling back to JSON", "error", err)
				closeBbolt(dataDir)
				initContributionLedgerJSON(dataDir, selfID)
			}
		} else {
			slog.Warn("bbolt open failed, ledger using JSON backend", "error", err)
			initContributionLedgerJSON(dataDir, selfID)
		}
	} else {
		initContributionLedgerJSON(dataDir, selfID)
	}

	// P1-3(ii): wire the cross-node ledger replicator (nil-safe elsewhere).
	ledgerReplicator = NewLedgerReplicator(contributionLedger, selfID)
	// P1-3(iii): background loop that keeps federation ledgers consistent.
	startLedgerReconcileLoop()
	// P2-1: co-governance ledger — contributors govern; lightweight, no
	// penalties. Voters = nodes that have contributed compute to the commons.
	governanceLedger = NewGovernanceLedger(selfID, contributorsVoterSource, dataDir+"/governance.json")
	// P2-3(i): accrue each donor's public-welfare free-quota entitlement.
	contribQuotaTracker = initContributionQuotaTracker(dataDir)

	capabilityVerifier = NewCapabilityVerifier(realProbeFn, 3)
	go capabilityVerifier.ProbeSchedulerLoop()
	slog.Info("capability verifier initialized")

	// Phase 2 (PRD-phase1.md §Q5): trust-pool capability-claim probe
	// verification with false-claim defense. The loop self-gates on network
	// mode + config every tick, so it stays dormant in personal mode.
	startPeerCapabilityProber()

	initTicketStore()
	go notarizeLoop()
}

// initContributionLedgerJSON 是 JSON 后端（及 bbolt 降级）的账本初始化。
func initContributionLedgerJSON(dataDir, selfID string) {
	ledgerPath := dataDir + "/ledger.json"
	loaded := false
	if _, err := os.Stat(ledgerPath); err == nil {
		if gl, loadErr := LoadGossipLedger(ledgerPath); loadErr == nil {
			contributionLedger = gl
			loaded = true
			slog.Info("contribution ledger loaded from disk",
				"peer_id", gl.PeerID(),
				"records", gl.Count())
		} else {
			slog.Warn("failed to load contribution ledger, creating new", "error", loadErr)
		}
	}

	if !loaded {
		gl, err := NewGossipLedger(selfID)
		if err != nil {
			slog.Error("failed to create contribution ledger", "error", err)
			return
		}
		contributionLedger = gl
		if saveErr := gl.Save(ledgerPath); saveErr != nil {
			slog.Warn("failed to save initial contribution ledger", "error", saveErr)
		}
		slog.Info("contribution ledger initialized", "peer_id", selfID)
	}
}

// saveContributionLedgerDebounce is the coalescing window for ledger writes
// (PERF-P1-4): hot-path callers (relay, gossip, notify) mark the ledger dirty
// and at most one disk write happens per window instead of one per record.
const saveContributionLedgerDebounce = 2 * time.Second

var (
	ledgerSaveMu    sync.Mutex
	ledgerSaveTimer *time.Timer
)

// saveContributionLedger persists the contribution ledger, debounced
// (PERF-P1-4). Concurrent hot-path callers coalesce into a single write.
// gracefulShutdown must call flushContributionLedger() for a final sync flush.
func saveContributionLedger() {
	if contributionLedger == nil {
		return
	}
	ledgerSaveMu.Lock()
	if ledgerSaveTimer == nil {
		ledgerSaveTimer = time.AfterFunc(saveContributionLedgerDebounce, func() {
			ledgerSaveMu.Lock()
			ledgerSaveTimer = nil
			ledgerSaveMu.Unlock()
			persistContributionLedger()
		})
	}
	ledgerSaveMu.Unlock()
}

// persistContributionLedger 同步落盘一次：bbolt 模式走脏 key 批量 Put，
// JSON 模式走全量原子写（路径用 ledgerDataDir，不再硬编码）。
func persistContributionLedger() {
	if contributionLedger == nil {
		return
	}
	if h := contributionLedger.bbolt; h != nil {
		if err := contributionLedger.saveBbolt(h); err != nil {
			slog.Warn("failed to save contribution ledger to bbolt", "error", err)
		}
		return
	}
	if err := contributionLedger.Save(filepath.Join(ledgerDataDir, "ledger.json")); err != nil {
		slog.Warn("failed to save contribution ledger", "error", err)
	}
}

// flushContributionLedger synchronously persists any pending ledger changes
// (used at shutdown; idempotent).
func flushContributionLedger() {
	if contributionLedger == nil {
		return
	}
	ledgerSaveMu.Lock()
	if ledgerSaveTimer != nil {
		ledgerSaveTimer.Stop()
		ledgerSaveTimer = nil
	}
	ledgerSaveMu.Unlock()
	persistContributionLedger()
}

// realProbeFn sends a 1-token test request to a remote node to verify
// that the claimed model is actually available (§10.2).
func realProbeFn(peerID, modelID string) (bool, int64, error) {
	if routeTable == nil {
		return false, 0, fmt.Errorf("route table not initialized")
	}
	entry := routeTable.Get(peerID)
	if entry == nil {
		return false, 0, fmt.Errorf("peer %s not found in route table", peerID)
	}
	targetAddr := pickBestAddress(entry.Addresses)
	if targetAddr == "" {
		return false, 0, fmt.Errorf("no address for peer %s", peerID)
	}

	probePayload := map[string]any{
		"model": modelID,
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
		},
		"max_tokens": 1,
		"stream":     false,
	}
	body, _ := json.Marshal(probePayload)

	endpoint := targetAddr + "/v1/chat/completions"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	// B8-4 precedent: the bare X-OMP-NodeID header is not recognized by
	// withProxyAuth's federation path, so probes to real nodes always got
	// 401 and contribution claims could never verify. Use the canonical
	// relay-auth signing (X-Node-ID + X-Node-Auth + ed25519 signature +
	// timestamp), the same mechanism as the peer capability prober.
	// Signed fresh per call, so the relay replay cache is not tripped.
	signProbeRequestDefault(req, http.MethodPost, "/v1/chat/completions", body)

	start := time.Now()
	resp, err := GetSharedHTTPClient().Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return false, latency, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	ok := resp.StatusCode == 200
	return ok, latency, nil
}
