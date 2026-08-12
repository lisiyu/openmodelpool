package main

import (
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"
)

// ============================================================
// Region table reconciliation (P5-4)
//
// Background: startRegionSyncLoop used to be a ticker whose body was a
// comment — the goroutine ran forever and did nothing, while its doc comment
// claimed it "synchronizes region assignments across the federation". That is
// exactly the doc/code divergence this project treats as a defect.
//
// What region state actually exists, and where it comes from:
//   - peers report their region on join (RegisterNodeSelfReport) and on every
//     heartbeat (ProcessHeartbeatRegion). That IS the cross-node exchange
//     channel; there is nothing to invent on top of it.
//   - what the exchange channel does NOT cover is (a) peers we know about but
//     that never heartbeat us, (b) our own region when auto-detection failed
//     at boot because the public address was not up yet, and (c) entries for
//     nodes that are long gone, which nothing ever removes.
//
// So the loop now does a real, local reconciliation pass over those three
// gaps. It deliberately performs no network I/O and no DNS lookups: it only
// reconciles state this process already holds, so a slow or hostile peer can
// never stall the loop.
// ============================================================

const regionSyncInterval = 30 * time.Second

// regionEntryTTL is how long an entry may go unreferenced before the
// reconciler drops it. Entries are re-stamped on every pass while the node is
// still known, so only genuinely departed nodes age out. Variable (not const)
// so tests can shrink it.
var regionEntryTTL = 24 * time.Hour

// regionSeenAt tracks when the reconciler last observed each node. It is kept
// out of NodeRegion on purpose: every register path (join / heartbeat / IP
// detect) replaces the whole NodeRegion value, which would wipe an embedded
// timestamp. Keeping it beside the table means a heartbeating node is simply
// re-stamped on the next pass, while a silent one keeps ageing.
type regionSeenAt map[string]time.Time

// startRegionSyncLoop runs reconcileRegionsOnce on a fixed cadence until the
// process stops.
func startRegionSyncLoop() {
	seen := regionSeenAt{}
	go func() {
		ticker := time.NewTicker(regionSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if regionManager == nil {
					continue
				}
				filled, pruned := reconcileRegionsOnce(regionManager, collectKnownNodes(), seen, time.Now())
				if filled > 0 || pruned > 0 {
					slog.Debug("region table reconciled", "filled", filled, "pruned", pruned)
				}
			case <-globalStopCh:
				return
			}
		}
	}()
}

// reconcileRegionsOnce performs a single reconciliation pass and returns how
// many entries it filled in and how many it pruned.
//
//	known — nodeID -> what this node knows about that peer, for every peer it
//	        currently knows about. An EMPTY map disables pruning: a transient
//	        empty view (managers not initialized yet, network momentarily down)
//	        must never wipe the region table.
//	seen  — reconciler-owned last-observed stamps, mutated in place.
//	now   — injected clock, so tests do not have to sleep.
func reconcileRegionsOnce(rm *RegionManager, known map[string]knownNode, seen regionSeenAt, now time.Time) (filled, pruned int) {
	if rm == nil {
		return 0, 0
	}
	if seen == nil {
		seen = regionSeenAt{}
	}

	// 1. Fill gaps: peers we know about whose region is missing or unknown.
	//    Preference order is (a) the region the peer reported when it joined
	//    the pool, (b) classification of a literal IP endpoint. Hostnames are
	//    skipped rather than resolved, to keep the loop I/O-free.
	for nodeID, kn := range known {
		if nodeID == "" {
			continue
		}
		rm.mu.RLock()
		cur := rm.nodes[nodeID]
		rm.mu.RUnlock()
		if cur != nil && cur.Region != RegionUnknown && cur.Region != RegionEmpty {
			continue
		}

		region := RegionUnknown
		source := ""
		if r := regionCanonical(kn.Region); r != RegionUnknown && r != RegionEmpty {
			region, source = r, "reconcile_pool_report"
		} else if host := hostFromEndpoint(kn.Endpoint); host != "" && net.ParseIP(host) != nil {
			if r := rm.DetectRegion(nodeID, host); r != RegionUnknown {
				region, source = r, "reconcile_addr"
			}
		}
		if region == RegionUnknown {
			continue
		}
		rm.mu.Lock()
		rm.nodes[nodeID] = &NodeRegion{Region: region, Source: source}
		rm.mu.Unlock()
		filled++
	}

	// 2. Re-stamp everything currently known (including this node), so only
	//    nodes that have dropped out of every peer view start ageing.
	selfID := ""
	if node != nil {
		selfID = node.NodeID()
	}
	for nodeID := range known {
		seen[nodeID] = now
	}
	if selfID != "" {
		seen[selfID] = now
	}

	// 3. Prune: entries that are absent from the known set AND have not been
	//    observed within the TTL. Never prune on an empty view, and never
	//    prune this node's own entry.
	if len(known) == 0 {
		return filled, 0
	}
	rm.mu.Lock()
	for nodeID := range rm.nodes {
		if nodeID == selfID {
			continue
		}
		if _, stillKnown := known[nodeID]; stillKnown {
			continue
		}
		last, ok := seen[nodeID]
		if !ok {
			// First time the reconciler sees this entry (e.g. registered by a
			// heartbeat between passes) — stamp it and let it age normally
			// instead of dropping it immediately.
			seen[nodeID] = now
			continue
		}
		if now.Sub(last) < regionEntryTTL {
			continue
		}
		delete(rm.nodes, nodeID)
		delete(seen, nodeID)
		pruned++
	}
	rm.mu.Unlock()

	return filled, pruned
}

// knownNode is what this process knows about a peer for region purposes: the
// region it reported when joining the pool, and an endpoint that may or may
// not be a literal IP.
type knownNode struct {
	Region   string
	Endpoint string
}

// collectKnownNodes returns nodeID -> knownNode for every peer this process
// currently knows about, merging the global pool with the network manager's
// peer list. Returning an empty map is meaningful: it tells the reconciler its
// view is untrustworthy, which suppresses pruning.
func collectKnownNodes() map[string]knownNode {
	out := make(map[string]knownNode)
	if globalPool != nil {
		for _, n := range globalPool.GetNodes() {
			if n.NodeID == "" {
				continue
			}
			out[n.NodeID] = knownNode{Region: n.Region}
		}
	}
	if netMgr != nil {
		for _, p := range netMgr.GetPeers() {
			if p.NodeID == "" {
				continue
			}
			addr := ""
			if len(p.Addresses) > 0 {
				addr = p.Addresses[0]
			}
			cur := out[p.NodeID]
			if cur.Region == "" {
				cur.Region = p.Region
			}
			if cur.Endpoint == "" {
				cur.Endpoint = addr
			}
			out[p.NodeID] = cur
		}
	}
	return out
}

// hostFromEndpoint extracts the host portion of an advertised endpoint. It
// accepts both "https://host:port/path" and bare "host:port" / "host" forms.
func hostFromEndpoint(endpoint string) string {
	s := strings.TrimSpace(endpoint)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}
