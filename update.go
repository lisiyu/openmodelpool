package main

// update.go — Admin "one-click version update" feature (incremental).
//
// This file is the single home for the update domain logic:
//   - UpdateManager: in-memory state (local node + federation peers) with
//     integrity-protected persistence to data/update_status.json.
//   - Version detection against the public GitHub Releases API, with a 10-minute
//     in-memory cache (reusing GetSharedHTTPClient) to stay under the
//     60 req/min unauthenticated rate limit.
//   - Local self-update: download platform binary → atomic replace
//     (os.Rename, Windows backs up the running exe first) → write
//     data/update-pending.json → os.Exit(0) so the supervisor
//     (run-omp.sh / systemd / omp-manager.ps1 scheduled task) re-launches us.
//   - Cross-environment broadcast over the federation channel, reusing
//     withFederationAuth + node.SignJSON / VerifyJSONSig.
//
// No new third-party dependencies are introduced; only the Go standard
// library and existing internal helpers are used.

import (
	"context"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MinSupportedUpdateVersion is the lowest node version that understands the
// /api/federation/update-signal protocol (Q4 capability negotiation).
// A peer running an older version must reject the signal (and be marked
// "unsupported" rather than stalling the broadcast).
const MinSupportedUpdateVersion = "4.1.7"

// releaseSigningPubKey is the Ed25519 public key used to verify release binary
// signatures. The corresponding private key is stored securely and used by
// CI/CD to sign release artifacts. If a .sig file is present in a release,
// the signature is verified against this key (fail-closed). If no .sig file
// is present, verification falls back to SHA-256 checksum only (backward compat).
var releaseSigningPubKey = ed25519.PublicKey{
	202, 42, 33, 116, 183, 71, 73, 158, 75, 169, 126, 30,
	134, 159, 90, 55, 108, 17, 137, 106, 151, 31, 42, 19,
	132, 172, 139, 186, 201, 136, 170, 246,
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// UpdatePhase is the state-machine phase of one environment's update.
type UpdatePhase string

const (
	PhaseIdle               UpdatePhase = "idle"
	PhaseDownloading        UpdatePhase = "downloading"
	PhaseReplacing          UpdatePhase = "replacing"
	PhaseRestarting         UpdatePhase = "restarting"
	PhaseSuccess            UpdatePhase = "success"
	PhaseFailed             UpdatePhase = "failed"
	PhaseUnsupported        UpdatePhase = "unsupported"
	PhaseNeedsManualRestart UpdatePhase = "needs_manual_restart"
)

// isInFlightPhase reports whether the phase represents an update still in
// progress (so a process restart without completion should reset it).
func isInFlightPhase(p UpdatePhase) bool {
	switch p {
	case PhaseDownloading, PhaseReplacing, PhaseRestarting:
		return true
	}
	return false
}

// VersionInfo is the result of GET /api/admin/version/latest.
type VersionInfo struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	HasUpdate      bool   `json:"has_update"`
	CheckedAt      string `json:"checked_at"`
	Error          string `json:"error,omitempty"`
}

// UpdateStatus is the update state of one environment (local node or a peer).
type UpdateStatus struct {
	Env           string      `json:"env"` // "local" or node_id
	Name          string      `json:"name"`
	NodeID        string      `json:"node_id,omitempty"`
	IsLocal       bool        `json:"is_local"`
	Role          string      `json:"role"` // "origin" | "peer"
	TargetVersion string      `json:"target_version"`
	Phase         UpdatePhase `json:"phase"`
	Progress      int         `json:"progress"` // 0-100
	Log           string      `json:"log"`
	Error         string      `json:"error,omitempty"`
	UpdatedAt     string      `json:"updated_at"`
}

// UpdateSignal is the cross-environment "please self-update" message.
type UpdateSignal struct {
	BroadcastBy         string   `json:"broadcast_by"`
	OriginAddresses     []string `json:"origin_addresses"`
	TargetVersion       string   `json:"target_version"`
	MinSupportedVersion string   `json:"min_supported_version"`
	AssetHint           string   `json:"asset_hint,omitempty"`
	Timestamp           string   `json:"timestamp"`
	Signature           string   `json:"signature"`
}

// UpdateReport is a peer's self-update result reported back to the origin.
type UpdateReport struct {
	NodeID        string      `json:"node_id"`
	Name          string      `json:"name"`
	TargetVersion string      `json:"target_version"`
	Phase         UpdatePhase `json:"phase"`
	Progress      int         `json:"progress"`
	Log           string      `json:"log"`
	Error         string      `json:"error,omitempty"`
	UpdatedAt     string      `json:"updated_at"`
	Signature     string      `json:"signature"`
}

// ---------------------------------------------------------------------------
// UpdateManager
// ---------------------------------------------------------------------------

// updateStatusSnapshot is the on-disk persistence shape.
type updateStatusSnapshot struct {
	Local UpdateStatus            `json:"local"`
	Peers map[string]UpdateStatus `json:"peers"`
}

// versionCache holds the last GitHub lookup plus its expiry.
type versionCache struct {
	latest   VersionInfo
	expireAt time.Time
}

// UpdateManager owns all update state for this node.
type UpdateManager struct {
	mu sync.RWMutex

	local      UpdateStatus
	peers      map[string]UpdateStatus
	cache      *versionCache
	reportBack *UpdateSignal // signal we must report success to after our own self-update

	dataDir      string
	githubURL    string
	downloadHash string
}

// Global singleton, initialized in initAllFederation().
var updateManager *UpdateManager

const (
	updateStatusFile  = "update_status.json"
	updatePendingFile = "update_pending.json"
	versionCacheTTL   = 10 * time.Minute
	githubReleasesAPI = "https://api.github.com/repos/lisiyu/openmodelpool/releases/latest"
)

// initUpdateManager creates the global UpdateManager and restores any
// previously persisted state. It must run after node + fed are initialized
// (BroadCastUpdateSignal / reportToOrigin depend on them).
func initUpdateManager(dataDir string) {
	um := &UpdateManager{
		local: UpdateStatus{
			Env:           "local",
			Name:          localEnvName(),
			IsLocal:       true,
			Role:          "origin",
			Phase:         PhaseIdle,
			TargetVersion: AppVersion,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		},
		peers:     make(map[string]UpdateStatus),
		cache:     &versionCache{},
		dataDir:   dataDir,
		githubURL: githubReleasesAPI,
	}
	updateManager = um
	um.Load()
	um.reconcilePending()
}

// localEnvName returns a human-friendly display name for the local node.
func localEnvName() string {
	if node != nil && node.IsInitialized() {
		if info := node.GetInfo(); info.GitHubUser != "" {
			return info.GitHubUser
		}
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "local"
}

// ---------------------------------------------------------------------------
// Version detection (T-1)
// ---------------------------------------------------------------------------

// compareVersion compares two semantic versions (leading "v" optional).
// Returns >0 if a>b, <0 if a<b, 0 if equal. Short versions are
// padded with zeros (e.g. "4.1" < "4.1.7").
func compareVersion(a, b string) int {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		an, bn := 0, 0
		if i < len(as) {
			an, _ = strconv.Atoi(strings.TrimSpace(as[i]))
		}
		if i < len(bs) {
			bn, _ = strconv.Atoi(strings.TrimSpace(bs[i]))
		}
		if an != bn {
			if an > bn {
				return 1
			}
			return -1
		}
	}
	return 0
}

// GetLatestVersion returns the cached (or freshly fetched) GitHub release info.
// The result is cached for versionCacheTTL to avoid hammering the
// unauthenticated GitHub API.
func (um *UpdateManager) GetLatestVersion() VersionInfo {
	um.mu.Lock()
	defer um.mu.Unlock()
	if um.cache != nil && time.Now().Before(um.cache.expireAt) {
		return um.cache.latest
	}
	info := um.fetchLatestVersionLocked()
	um.cache = &versionCache{latest: info, expireAt: time.Now().Add(versionCacheTTL)}
	return info
}

// fetchLatestVersionLocked performs the actual GitHub lookup. Caller holds um.mu.
func (um *UpdateManager) fetchLatestVersionLocked() VersionInfo {
	info := VersionInfo{
		CurrentVersion: AppVersion,
		CheckedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, um.githubURL, nil)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "OpenModelPool/"+AppVersion)

	client := GetSharedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		info.Error = fmt.Sprintf("github returned HTTP %d", resp.StatusCode)
		return info
	}
	var gh struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gh); err != nil {
		info.Error = "invalid github response: " + err.Error()
		return info
	}
	info.LatestVersion = strings.TrimSpace(gh.TagName)
	info.HasUpdate = compareVersion(info.LatestVersion, info.CurrentVersion) > 0
	return info
}

// ---------------------------------------------------------------------------
// Persistence & status query (T-3)
// ---------------------------------------------------------------------------

// Load restores local + peer state from the integrity-protected snapshot.
func (um *UpdateManager) Load() {
	path := filepath.Join(um.dataDir, updateStatusFile)
	var snap updateStatusSnapshot
	if err := loadWithIntegrity(path, &snap); err != nil {
		// Not yet created (or tampered) — start fresh.
		return
	}
	um.mu.Lock()
	defer um.mu.Unlock()
	if snap.Peers != nil {
		um.peers = snap.Peers
	}
	if snap.Local.Env != "" {
		um.local = snap.Local
	}
}

// persistLocked writes the snapshot. Caller must hold um.mu.
func (um *UpdateManager) persistLocked() {
	snap := updateStatusSnapshot{Local: um.local, Peers: um.peers}
	path := filepath.Join(um.dataDir, updateStatusFile)
	if err := saveWithIntegrity(path, snap); err != nil {
		slog.Error("failed to persist update status", "error", err)
	}
}

// persist writes the snapshot under a read lock.
func (um *UpdateManager) persist() {
	um.mu.RLock()
	defer um.mu.RUnlock()
	um.persistLocked()
}

// reconcilePending is called on startup: if update-pending.json records a
// target version matching the now-running AppVersion, the previous self-update
// succeeded — promote local to success and clean the marker. Otherwise, any
// in-flight phase left over from a mid-update restart is reset to idle.
func (um *UpdateManager) reconcilePending() {
	path := filepath.Join(um.dataDir, updatePendingFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		um.mu.Lock()
		if isInFlightPhase(um.local.Phase) {
			um.local.Phase = PhaseIdle
			um.local.Progress = 0
			um.local.Log = ""
		}
		um.mu.Unlock()
		return
	}
	var pending struct {
		TargetVersion string `json:"target_version"`
		Timestamp     string `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &pending); err != nil {
		um.mu.Lock()
		if isInFlightPhase(um.local.Phase) {
			um.local.Phase = PhaseIdle
			um.local.Progress = 0
			um.local.Log = ""
		}
		um.mu.Unlock()
		return
	}
	um.mu.Lock()
	defer um.mu.Unlock()
	if compareVersion(pending.TargetVersion, AppVersion) == 0 {
		um.local.Phase = PhaseSuccess
		um.local.TargetVersion = AppVersion
		um.local.Progress = 100
		um.local.Error = ""
		um.local.Log = fmt.Sprintf("已更新至 v%s", AppVersion)
		um.local.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		um.persistLocked()
		_ = os.Remove(path)
		slog.Info("self-update completed on restart", "version", AppVersion)
	} else if isInFlightPhase(um.local.Phase) {
		um.local.Phase = PhaseIdle
		um.local.Progress = 0
		um.local.Log = ""
	}
}

// ListStatuses aggregates the local node and every tracked peer.
func (um *UpdateManager) ListStatuses() []UpdateStatus {
	um.mu.RLock()
	defer um.mu.RUnlock()
	out := make([]UpdateStatus, 0, len(um.peers)+1)
	out = append(out, um.local)
	for _, p := range um.peers {
		out = append(out, p)
	}
	return out
}

// updateLocalLocked mutates the local status under a write lock and persists.
func (um *UpdateManager) updateLocalLocked(fn func(s *UpdateStatus)) {
	um.mu.Lock()
	fn(&um.local)
	um.local.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	um.persistLocked()
	um.mu.Unlock()
}

// setLocalPhase transitions the local node to a new phase with progress/log.
func (um *UpdateManager) setLocalPhase(phase UpdatePhase, progress int, log, errMsg string) {
	um.updateLocalLocked(func(s *UpdateStatus) {
		s.Phase = phase
		s.Progress = progress
		s.Log = log
		if errMsg != "" {
			s.Error = errMsg
		}
	})
}

// setLocalFailed marks the local node's update as failed.
func (um *UpdateManager) setLocalFailed(errMsg string) {
	um.updateLocalLocked(func(s *UpdateStatus) {
		s.Phase = PhaseFailed
		s.Progress = 0
		s.Error = errMsg
		s.Log = "更新失败"
	})
}

// setLocalTarget records the target version on the local status.
func (um *UpdateManager) setLocalTarget(target string) {
	um.updateLocalLocked(func(s *UpdateStatus) {
		s.TargetVersion = target
	})
}

// upsertPeer creates-or-updates a peer's status under a write lock and persists.
func (um *UpdateManager) upsertPeer(nodeID string, fn func(s *UpdateStatus)) {
	um.mu.Lock()
	s, ok := um.peers[nodeID]
	if !ok {
		s = UpdateStatus{
			Env:     nodeID,
			NodeID:  nodeID,
			IsLocal: false,
			Role:    "peer",
		}
	}
	fn(&s)
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	um.peers[nodeID] = s
	um.persistLocked()
	um.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Local self-update (T-2)
// ---------------------------------------------------------------------------

// platformAssetName returns the release asset filename for this host platform,
// matching the published binaries: openmodelpool-<os>-<arch>[.exe].
func platformAssetName() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	switch goarch {
	case "amd64":
		goarch = "amd64"
	case "arm64":
		goarch = "arm64"
	}
	asset := fmt.Sprintf("openmodelpool-%s-%s", goos, goarch)
	if goos == "windows" {
		asset += ".exe"
	}
	return asset
}

// TriggerSelfUpdate downloads the target release asset, atomically replaces
// the running binary, records the pending target, and exits so the supervisor
// re-launches the new version. On any failure it marks local as failed
// and returns WITHOUT exiting (Q3: no automatic rollback).
func (um *UpdateManager) TriggerSelfUpdate(target string) {
	um.setLocalTarget(target)
	um.setLocalPhase(PhaseDownloading, 5, fmt.Sprintf("开始更新至 v%s", target), "")

	exePath, err := os.Executable()
	if err != nil {
		um.setLocalFailed("无法确定当前可执行文件路径: " + err.Error())
		return
	}
	exePath = filepath.Clean(exePath)

	asset := platformAssetName()
	downloadURL := fmt.Sprintf("https://github.com/lisiyu/openmodelpool/releases/download/%s/%s", target, asset)

	tmpPath := filepath.Join(um.dataDir, fmt.Sprintf(".omp-update-%s.tmp", target))
	_ = os.Remove(tmpPath) // best-effort cleanup of any stale temp

	if err := um.downloadFile(downloadURL, tmpPath); err != nil {
		um.setLocalFailed("下载失败: " + err.Error())
		return
	}

	// P0-1: Verify SHA-256 checksum from GitHub release assets.
	checksumURL := downloadURL + ".sha256"
	expectedHash, err := um.fetchChecksum(checksumURL)
	if err != nil {
		slog.Warn("checksum unavailable, skipping verification", "error", err)
	} else if um.downloadHash != expectedHash {
		_ = os.Remove(tmpPath)
		um.setLocalFailed(fmt.Sprintf("SHA-256 校验失败: 期望 %s, 实际 %s", expectedHash, um.downloadHash))
		return
	} else {
		slog.Info("SHA-256 checksum verified", "hash", expectedHash)
	}

	// P2: Verify Ed25519 signature if a .sig file is available in the release.
	// This provides cryptographic integrity verification beyond SHA-256,
	// protecting against scenarios where the GitHub repository is compromised.
	sigURL := downloadURL + ".sig"
	sigBytes, err := um.fetchSignature(sigURL)
	if err != nil {
		slog.Warn("Ed25519 signature unavailable, falling back to SHA-256 only", "error", err)
	} else {
		// Read the downloaded binary for signature verification
		binaryData, err := os.ReadFile(tmpPath)
		if err != nil {
			_ = os.Remove(tmpPath)
			um.setLocalFailed("无法读取已下载的二进制文件: " + err.Error())
			return
		}
		if !ed25519.Verify(releaseSigningPubKey, binaryData, sigBytes) {
			_ = os.Remove(tmpPath)
			um.setLocalFailed("Ed25519 签名验证失败: 二进制文件可能被篡改")
			return
		}
		slog.Info("Ed25519 signature verified", "version", target)
	}

	// Atomic replace.
	um.setLocalPhase(PhaseReplacing, 80, "正在替换二进制文件…", "")
	if err := atomicReplace(exePath, tmpPath); err != nil {
		um.setLocalFailed(err.Error())
		return
	}

	// Record pending target so the restarted process can self-verify.
	um.setLocalPhase(PhaseRestarting, 95, "已替换，正在重启（由 supervisor 重拉）…", "")
	um.writePending(target)

	// Report success back to the origin *before* exiting, in case this
	// node is a peer that received the signal (P1-2).
	um.mu.RLock()
	rb := um.reportBack
	um.mu.RUnlock()
	if rb != nil {
		um.reportToOrigin(*rb, PhaseSuccess, 100, "已更新至 "+target, "")
		um.clearReportBack()
	}

	// Give the HTTP layer a moment to flush any in-flight response
	// (e.g. the /api/admin/update/start 200) before we die.
	time.Sleep(300 * time.Millisecond)
	slog.Info("self-update binary replaced; exiting for supervisor restart",
		"target", target, "exe", exePath)
	os.Exit(0)
}

// downloadFile fetches url into dest, validating a non-empty result.
// It updates local progress along the way. Caller must NOT hold um.mu.
func (um *UpdateManager) downloadFile(url, dest string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "OpenModelPool/"+AppVersion)

	client := GetSharedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, h), resp.Body)
	if err != nil {
		return err
	}
	if written == 0 {
		return fmt.Errorf("下载文件为空")
	}
	um.downloadHash = hex.EncodeToString(h.Sum(nil))
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dest, 0755)
	}
	um.setLocalPhase(PhaseDownloading, 70, fmt.Sprintf("下载完成（%d 字节）", written), "")
	return nil
}

// fetchChecksum downloads a .sha256 checksum file and returns the hex hash.
func (um *UpdateManager) fetchChecksum(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "OpenModelPool/"+AppVersion)
	client := GetSharedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	// SHA-256 checksum files typically: "<hash>  <filename>" or just "<hash>"
	parts := strings.Fields(strings.TrimSpace(string(data)))
	if len(parts) == 0 || len(parts[0]) != 64 {
		return "", fmt.Errorf("invalid checksum format")
	}
	return parts[0], nil
}

// atomicReplace atomically replaces the running binary with the downloaded
// temp file. On Windows the running .exe cannot be renamed in place, so it
// is backed up to <exe>.bak first.
func atomicReplace(exePath, tmpPath string) error {
	if runtime.GOOS == "windows" {
		bakPath := exePath + ".bak"
		_ = os.Remove(bakPath)
		if err := os.Rename(exePath, bakPath); err != nil {
			return fmt.Errorf("备份当前二进制失败: %w", err)
		}
	}
	if err := os.Rename(tmpPath, exePath); err != nil {
		return fmt.Errorf("原子替换失败: %w", err)
	}
	return nil
}

// writePending records the target version we are about to restart into.
func (um *UpdateManager) writePending(target string) {
	path := filepath.Join(um.dataDir, updatePendingFile)
	pending := struct {
		TargetVersion string `json:"target_version"`
		Timestamp     string `json:"timestamp"`
	}{
		TargetVersion: target,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(pending, "", "  ")
	if err := atomicWriteFile(path, data, 0600); err != nil {
		slog.Error("failed to write pending update state", "error", err)
	}
}

// setReportBack / clearReportBack manage the optional "report success to
// origin" contract used when this node updates as a peer.
func (um *UpdateManager) setReportBack(sig UpdateSignal) {
	um.mu.Lock()
	rb := sig
	um.reportBack = &rb
	um.mu.Unlock()
}

func (um *UpdateManager) clearReportBack() {
	um.mu.Lock()
	um.reportBack = nil
	um.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Cross-environment broadcast (T-5)
// ---------------------------------------------------------------------------

// BroadcastUpdateSignal sends the update signal to every active federation
// peer (excluding self). Peers are contacted asynchronously; the local
// node's own self-update proceeds independently. An old peer that lacks
// the endpoint (HTTP 404) is marked "unsupported" rather than stalling.
func (um *UpdateManager) BroadcastUpdateSignal(target string) {
	if node == nil || fed == nil {
		slog.Warn("cannot broadcast update signal: node/fed not ready")
		return
	}

	sig := UpdateSignal{
		BroadcastBy:         node.NodeID(),
		TargetVersion:       target,
		MinSupportedVersion: MinSupportedUpdateVersion,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
	}
	// Origin addresses so peers know where to report back.
	info := node.GetInfo()
	if info.Endpoint != "" {
		sig.OriginAddresses = append(sig.OriginAddresses, info.Endpoint)
	}
	for _, a := range info.Addresses {
		sig.OriginAddresses = append(sig.OriginAddresses, a)
	}
	sig.OriginAddresses = dedupeStrings(sig.OriginAddresses)

	sig.Signature = node.SignJSON(sig)

	body, err := json.Marshal(sig)
	if err != nil {
		slog.Error("failed to marshal update signal", "error", err)
		return
	}

	peers := fed.GetActiveNodes()
	client := GetSharedHTTPClientWithTimeout(8 * time.Second)
	for _, peer := range peers {
		if peer.NodeID == node.NodeID() {
			continue
		}
		addrs := peerEndpoints(peer)
		if len(addrs) == 0 {
			continue
		}
		// Record the peer as "signal sent, awaiting report" up front.
		um.upsertPeer(peer.NodeID, func(s *UpdateStatus) {
			s.Env = peer.NodeID
			s.NodeID = peer.NodeID
			s.Name = peerDisplayName(peer)
			s.IsLocal = false
			s.Role = "peer"
			s.TargetVersion = target
			if s.Phase == "" || s.Phase == PhaseIdle || s.Phase == PhaseSuccess || s.Phase == PhaseFailed {
				s.Phase = PhaseDownloading
				s.Progress = 0
				s.Log = "已下发更新信号，等待对端回报…"
			}
		})
		go um.sendUpdateSignalToPeer(peer, body, client)
	}
}

// sendUpdateSignalToPeer delivers the signal to one peer, trying every
// known address. A 404 means an old peer without this endpoint → mark
// "unsupported".
func (um *UpdateManager) sendUpdateSignalToPeer(peer NodeInfo, body []byte, client *http.Client) {
	for _, addr := range peerEndpoints(peer) {
		url := fmt.Sprintf("%s/api/federation/update-signal", strings.TrimRight(addr, "/"))
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if node != nil {
			req.Header.Set("X-Node-ID", node.NodeID())
		}
		resp, err := client.Do(req)
		if err != nil {
			continue // try next address
		}
		code := resp.StatusCode
		resp.Body.Close()
		if code == http.StatusOK {
			return // accepted; peer will report back
		}
		if code == http.StatusNotFound {
			um.markPeerUnsupported(peer.NodeID, peer, "节点不支持更新信号（旧版本）")
			return
		}
		// other status → try next address
	}
	// All addresses failed; leave as "signal sent, awaiting report".
}

// markPeerUnsupported flags a peer that cannot process the signal.
func (um *UpdateManager) markPeerUnsupported(nodeID string, peer NodeInfo, log string) {
	um.upsertPeer(nodeID, func(s *UpdateStatus) {
		s.Env = nodeID
		s.NodeID = nodeID
		s.Name = peerDisplayName(peer)
		s.IsLocal = false
		s.Role = "peer"
		s.Phase = PhaseUnsupported
		s.Progress = 0
		s.Log = log
		s.Error = ""
	})
}

// ---------------------------------------------------------------------------
// Peer receive + report (T-6)
// ---------------------------------------------------------------------------

// OnSignalReceived handles an incoming update signal on a peer node: it
// remembers where to report success, then performs its own self-update.
// If the self-update fails (no exit), it reports the failure back.
func (um *UpdateManager) OnSignalReceived(sig UpdateSignal) {
	um.setReportBack(sig)
	um.TriggerSelfUpdate(sig.TargetVersion)
	// Reached only if TriggerSelfUpdate did NOT exit (failure / manual restart).
	um.mu.RLock()
	phase := um.local.Phase
	errMsg := um.local.Error
	um.mu.RUnlock()
	switch phase {
	case PhaseFailed:
		um.reportToOrigin(sig, PhaseFailed, 0, "自更新失败", errMsg)
		um.clearReportBack()
	case PhaseNeedsManualRestart:
		um.reportToOrigin(sig, PhaseNeedsManualRestart, 0, um.local.Log, "")
		um.clearReportBack()
	}
}

// OnReportReceived folds a peer's report-back into our peer status map.
func (um *UpdateManager) OnReportReceived(report UpdateReport) {
	name := report.Name
	if name == "" {
		name = shortNodeID(report.NodeID)
	}
	um.upsertPeer(report.NodeID, func(s *UpdateStatus) {
		s.Env = report.NodeID
		s.NodeID = report.NodeID
		s.Name = name
		s.IsLocal = false
		s.Role = "peer"
		s.TargetVersion = report.TargetVersion
		s.Phase = report.Phase
		s.Progress = report.Progress
		s.Log = report.Log
		s.Error = report.Error
	})
}

// reportToOrigin sends the local result back to the broadcasting node.
func (um *UpdateManager) reportToOrigin(sig UpdateSignal, phase UpdatePhase, progress int, log, errMsg string) {
	if node == nil {
		return
	}
	addrs := sig.OriginAddresses
	if len(addrs) == 0 {
		if origin, ok := fed.GetNode(sig.BroadcastBy); ok {
			addrs = peerEndpoints(*origin)
		}
	}
	if len(addrs) == 0 {
		slog.Warn("cannot report update result: no origin address", "origin", sig.BroadcastBy)
		return
	}
	report := UpdateReport{
		NodeID:        node.NodeID(),
		Name:          localEnvName(),
		TargetVersion: sig.TargetVersion,
		Phase:         phase,
		Progress:      progress,
		Log:           log,
		Error:         errMsg,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	report.Signature = node.SignJSON(report)
	body, err := json.Marshal(report)
	if err != nil {
		return
	}
	client := GetSharedHTTPClientWithTimeout(8 * time.Second)
	for _, addr := range addrs {
		url := fmt.Sprintf("%s/api/federation/update-report", strings.TrimRight(addr, "/"))
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Node-ID", node.NodeID())
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return
		}
	}
	slog.Warn("failed to report update result to origin", "origin", sig.BroadcastBy)
}

// ---------------------------------------------------------------------------
// HTTP handlers (wired in server.go / admin.go)
// ---------------------------------------------------------------------------

// handleAdminVersionLatest returns cached/live GitHub version info (T-1).
func handleAdminVersionLatest(w http.ResponseWriter, r *http.Request) {
	if updateManager == nil {
		writeError(w, 500, "update manager not initialized")
		return
	}
	writeJSON(w, 200, updateManager.GetLatestVersion())
}

// handleAdminUpdateStart triggers the local self-update and broadcasts the
// signal to peers. It accepts immediately (200) and performs the heavy
// work in a background goroutine that may eventually os.Exit.
func handleAdminUpdateStart(w http.ResponseWriter, r *http.Request) {
	if updateManager == nil {
		writeError(w, 500, "update manager not initialized")
		return
	}
	info := updateManager.GetLatestVersion()
	target := info.LatestVersion
	if target == "" || !info.HasUpdate {
		writeError(w, 400, "当前已是最新版本，无需更新")
		return
	}
	// Accept immediately; perform the update asynchronously.
	writeJSON(w, 200, map[string]any{"accepted": true, "target": target})
	go func() {
		updateManager.BroadcastUpdateSignal(target)
		updateManager.TriggerSelfUpdate(target)
	}()
}

// handleAdminUpdateStatus returns aggregated local + peer statuses (T-3).
func handleAdminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if updateManager == nil {
		writeError(w, 500, "update manager not initialized")
		return
	}
	statuses := updateManager.ListStatuses()
	writeJSON(w, 200, map[string]any{
		"current_version": AppVersion,
		"statuses":        statuses,
	})
}

// handleFederationUpdateSignal receives a broadcast update signal from the
// origin node, verifies the federation signature, negotiates capability,
// then triggers this node's own self-update (T-6).
func handleFederationUpdateSignal(w http.ResponseWriter, r *http.Request) {
	if updateManager == nil {
		writeError(w, 500, "update manager not initialized")
		return
	}
	var sig UpdateSignal
	if err := readJSON(w, r, &sig); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	senderID := sanitizeNodeID(r.Header.Get("X-Node-ID"))
	if senderID == "" {
		senderID = sig.BroadcastBy
	}
	sender, ok := fed.GetNode(senderID)
	if !ok {
		writeError(w, 403, "unknown federation node")
		return
	}
	// Replay protection: timestamp must be within ±5 minutes.
	if !validTimestamp(sig.Timestamp) {
		writeError(w, 400, "stale or invalid timestamp")
		return
	}
	if !VerifyJSONSig(sender.PubKey, sig, sig.Signature) {
		writeError(w, 403, "signature verification failed")
		return
	}
	// Capability negotiation (Q4): if we are older than required, refuse
	// and report "unsupported" back to the origin.
	if compareVersion(AppVersion, sig.MinSupportedVersion) < 0 {
		writeJSON(w, 200, map[string]any{"accepted": false, "unsupported": true, "reason": "node version too old"})
		go updateManager.reportToOrigin(sig, PhaseUnsupported, 0, "本节点版本过低，不支持自更新", "")
		return
	}
	// Accept and start our own self-update in the background.
	writeJSON(w, 200, map[string]any{"accepted": true})
	go updateManager.OnSignalReceived(sig)
}

// handleFederationUpdateReport receives a peer's self-update result and
// folds it into our peer status map (T-6).
func handleFederationUpdateReport(w http.ResponseWriter, r *http.Request) {
	if updateManager == nil {
		writeError(w, 500, "update manager not initialized")
		return
	}
	var report UpdateReport
	if err := readJSON(w, r, &report); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	senderID := sanitizeNodeID(r.Header.Get("X-Node-ID"))
	if senderID == "" {
		senderID = report.NodeID
	}
	sender, ok := fed.GetNode(senderID)
	if !ok {
		writeError(w, 403, "unknown federation node")
		return
	}
	if !VerifyJSONSig(sender.PubKey, report, report.Signature) {
		writeError(w, 403, "signature verification failed")
		return
	}
	updateManager.OnReportReceived(report)
	writeJSON(w, 200, map[string]any{"accepted": true})
}

// handleAdminUpdateJS serves the embedded admin-update.js file.
func handleAdminUpdateJS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedJS(w, r, "admin-update.js")
}

// ---------------------------------------------------------------------------
// Small shared helpers
// ---------------------------------------------------------------------------

// validTimestamp reports whether ts (RFC3339) is within ±5 minutes of now.
func validTimestamp(ts string) bool {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false
	}
	diff := time.Now().Sub(t)
	if diff < 0 {
		diff = -diff
	}
	return diff <= 5*time.Minute
}

// dedupeStrings returns the input with duplicate entries removed (order kept).
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// peerDisplayName derives a display name for a peer NodeInfo.
func peerDisplayName(peer NodeInfo) string {
	if peer.GitHubUser != "" {
		return peer.GitHubUser
	}
	if peer.Endpoint != "" {
		return peer.Endpoint
	}
	return shortNodeID(peer.NodeID)
}

// shortNodeID returns a compact, readable form of a node id.
func shortNodeID(id string) string {
	if len(id) > 12 {
		return id[:8] + "…" + id[len(id)-4:]
	}
	return id
}
