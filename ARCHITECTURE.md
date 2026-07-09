# OpenModelPool Architecture Design Document

> **Version**: 1.0  
> **Last Updated**: 2026-07-09  
> **Status**: Active Development

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Network Topology Architecture](#2-network-topology-architecture)
3. [Key Management System](#3-key-management-system)
4. [Capability Declaration & Service Broadcasting](#4-capability-declaration--service-broadcasting)
5. [Contribution Ledger System](#5-contribution-ledger-system)
6. [Trust & Reputation System](#6-trust--reputation-system)
7. [False Capability Defense](#7-false-capability-defense)
8. [Request Routing & Load Balancing](#8-request-routing--load-balancing)
9. [Security Architecture](#9-security-architecture)
10. [BitTorrent Protocol Analogy](#10-bittorrent-protocol-analogy)
11. [Performance Metrics & Targets](#11-performance-metrics--targets)
12. [Implementation Roadmap](#12-implementation-roadmap)
13. [Cost Analysis](#13-cost-analysis)

---

## 1. Project Overview

### 1.1 What is OpenModelPool?

OpenModelPool is a **P2P shared computing power pool** for AI model services. It enables individual developers and organizations to share their AI model API access (OpenAI, Anthropic, DeepSeek, etc.) in a decentralized network — similar to how BitTorrent enables file sharing, but OpenModelPool shares **AI model services** instead of files.

### 1.2 Core Philosophy

```
┌─────────────────────────────────────────────────────────────┐
│                    BitTorrent Analogy                        │
├─────────────────────────────────────────────────────────────┤
│  BitTorrent:    Peers share FILES    → download/upload       │
│  OpenModelPool: Peers share SERVICES → consume/contribute    │
│                                                              │
│  Every node is BOTH:                                         │
│  • Consumer (Leecher) — makes API requests                   │
│  • Provider (Seeder)  — shares API access to the pool        │
└─────────────────────────────────────────────────────────────┘
```

### 1.3 Key Principles

| Principle | Description |
|-----------|-------------|
| **Decentralization** | No central server; nodes discover and route via DHT |
| **Zero Infrastructure Cost** | Runs on IPFS (free) + IOTA (zero gas) |
| **Open Contribution** | Any node can contribute API keys to the shared pool |
| **Trust-Based Routing** | Reputation system ensures service quality |
| **Fail-Close Security** | Authorization chain fails safely on any error |

### 1.4 System Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Application Layer                             │
│  ┌──────────┐ ┌──────────────┐ ┌────────────┐ ┌─────────────────┐  │
│  │ API      │ │ Model Relay  │ │ Capability │ │ Load Balancer   │  │
│  │ Gateway  │ │ Handler      │ │ Manager    │ │ (5-dim scoring) │  │
│  └──────────┘ └──────────────┘ └────────────┘ └─────────────────┘  │
├─────────────────────────────────────────────────────────────────────┤
│                    Routing & Discovery Layer                         │
│  ┌──────────────────┐ ┌──────────────┐ ┌─────────────────────────┐ │
│  │ Kademlia DHT     │ │ GossipSub    │ │ Capability Claims       │ │
│  │ (256-bit ring)   │ │ (mesh net)   │ │ (signed, TTL-based)     │ │
│  │ k=20, α=10       │ │ fanout=10    │ │ Ed25519 signatures      │ │
│  └──────────────────┘ └──────────────┘ └─────────────────────────┘ │
├─────────────────────────────────────────────────────────────────────┤
│                    Contribution Ledger Layer                         │
│  ┌──────────────────┐ ┌──────────────┐ ┌─────────────────────────┐ │
│  │ Layer 1: Gossip  │ │ Layer 2:     │ │ Layer 3: Token          │ │
│  │ Ledger (LevelDB) │ │ IPFS + IOTA  │ │ Economy (reserved)      │ │
│  └──────────────────┘ └──────────────┘ └─────────────────────────┘ │
├─────────────────────────────────────────────────────────────────────┤
│                        Transport Layer                               │
│  ┌──────────────────┐ ┌──────────────┐ ┌─────────────────────────┐ │
│  │ HTTP/HTTPS       │ │ TLS          │ │ WebSocket               │ │
│  │ Connection Pool  │ │ (Noise)      │ │ (future: QUIC)          │ │
│  └──────────────────┘ └──────────────┘ └─────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 2. Network Topology Architecture

### 2.1 Kademlia DHT — 256-bit Hash Space

OpenModelPool implements a Kademlia-style Distributed Hash Table with the following parameters:

```
┌─────────────────────────────────────────────────────────────┐
│                  DHT Configuration                           │
├─────────────────────────────────────────────────────────────┤
│  Hash Space:     256-bit (SHA-256)                          │
│  Distance:       XOR metric                                  │
│  K-Bucket Size:  k = 20 (high redundancy vs churn)          │
│  Num Buckets:    256 (one per bit)                          │
│  Lookup α:       10 (P50 latency ~0.3s)                    │
│  Lookup β:       3 (termination condition)                  │
│  Refresh:        10 minutes                                  │
│  Query Timeout:  10 seconds                                  │
│  Record TTL:     48 hours                                    │
│  Protocol ID:    /openmodelpool/kad/1.0.0                   │
└─────────────────────────────────────────────────────────────┘
```

**Why 256-bit over BitTorrent's 160-bit?**
- BitTorrent uses SHA-1 (160-bit) — sufficient for ~8M nodes
- 256-bit provides 2^96× larger address space
- Matches IPFS/Kademlia standard, enabling future interoperability

### 2.2 Node Hashing & Distance

```go
// NodeHash — 256-bit position in DHT ring (from source: dht.go)
type NodeHash [HashSizeBytes]byte  // HashSizeBytes = 32

func ComputeNodeHash(nodeID string) NodeHash {
    return sha256.Sum256([]byte(nodeID))
}

func XORDistance(a, b NodeHash) *big.Int {
    aInt := a.BigInt()
    bInt := b.BigInt()
    return new(big.Int).Xor(aInt, bInt)
}
```

### 2.3 K-Bucket Structure

```
K-Bucket Organization (k=20):

Bucket 0:   nodes with common prefix length 0   (distance: 2^255 ~ 2^256)
Bucket 1:   nodes with common prefix length 1   (distance: 2^254 ~ 2^255)
...
Bucket i:   nodes with common prefix length i   (distance: 2^(255-i) ~ 2^(256-i))
...
Bucket 255: nodes with common prefix length 255 (distance: 2^0 ~ 2^1)

Each bucket holds up to 20 nodes, ordered by last-seen time.
```

### 2.4 GossipSub Message Propagation

```
┌──────────────────────────────────────────────────────────┐
│              GossipSub Mesh Network                       │
│                                                           │
│    ┌─────┐     ┌─────┐     ┌─────┐                      │
│    │ N1  │─────│ N2  │─────│ N3  │                      │
│    └──┬──┘     └──┬──┘     └──┬──┘                      │
│       │           │           │                           │
│       ▼           ▼           ▼                           │
│    ┌─────┐     ┌─────┐     ┌─────┐                      │
│    │ N4  │─────│ N5  │─────│ N6  │                      │
│    └─────┘     └─────┘     └─────┘                      │
│                                                           │
│  Mesh degree:    6 (acceptable: 4-12)                    │
│  Fanout:         10                                       │
│  Gossip interval: 3 seconds                               │
│  Propagation:    99% within 8s (10K nodes)               │
│  Bandwidth:      ~1.2 Mbps per node                       │
└──────────────────────────────────────────────────────────┘
```

**Message Types:**
- Model status updates (capability changes)
- Inference queue depth announcements
- Reputation score broadcasts
- Node join/leave notifications
- Contribution record synchronization

### 2.5 Node Discovery

```
Node Discovery Flow:

1. Bootstrap Phase
   ┌─────────────────────────────────────┐
   │ New Node contacts Bootstrap Nodes   │
   │ → Gets initial peer list            │
   │ → Joins DHT ring                    │
   └─────────────────────────────────────┘

2. DHT Walk
   ┌─────────────────────────────────────┐
   │ Iterative lookup (α=10 parallel)    │
   │ → Query closest known nodes         │
   │ → Discover new neighbors            │
   │ → Populate k-buckets                │
   └─────────────────────────────────────┘

3. Peer Exchange (PEX)
   ┌─────────────────────────────────────┐
   │ Gossip-based peer exchange          │
   │ → Share known peers via gossip      │
   │ → Maintain mesh connectivity        │
   └─────────────────────────────────────┘
```

### 2.6 Network Layering

```
┌─────────────────────────────────────────────────────────────┐
│                    Network Layer Hierarchy                    │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ Core Nodes (High Reputation, Score > 80)             │    │
│  │ • DHT Server mode                                   │    │
│  │ • Routing backbone                                  │    │
│  │ • High availability (>99% uptime)                   │    │
│  └─────────────────────────┬───────────────────────────┘    │
│                            │                                 │
│  ┌─────────────────────────┴───────────────────────────┐    │
│  │ Worker Nodes (Medium Reputation, Score 40-80)        │    │
│  │ • DHT Server or Client (AutoNAT)                    │    │
│  │ • Active contributors                               │    │
│  │ • Model serving + relay                             │    │
│  └─────────────────────────┬───────────────────────────┘    │
│                            │                                 │
│  ┌─────────────────────────┴───────────────────────────┐    │
│  │ Edge Nodes (New/Low Reputation, Score < 40)          │    │
│  │ • DHT Client mode                                   │    │
│  │ • Limited relay capabilities                        │    │
│  │ • Must build trust through contributions            │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**AutoNAT Role Determination:**
```go
func determineNodeMode(host host.Host, autonat autonat.AutoNAT) dht.ModeOpt {
    reachability := autonat.Status()
    if reachability == network.ReachabilityPublic {
        return dht.ModeServer  // Public: DHT Server + inference service
    }
    return dht.ModeClient      // NAT behind: DHT Client only
}
```

---

## 3. Key Management System

### 3.1 Three Key Types

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Key Type Hierarchy                               │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  1. Proxy API Key (Personal)                                         │
│     Format:  sk-{48 random chars}                                   │
│     Binding: Bound to specific Provider                             │
│     Scope:   Personal use, routes to owner's providers              │
│     ┌─────────────────────────────────────────┐                     │
│     │ sk-a1b2c3d4e5f6g7h8i9j0...              │                     │
│     │ → Routes to owner's OpenAI provider     │                     │
│     └─────────────────────────────────────────┘                     │
│                                                                      │
│  2. Guest Key (Shared)                                               │
│     Format:  sk-guest-{node_id}-{random}                            │
│     Binding: No provider binding (global shared pool)               │
│     Scope:   Can only access the issuing node                       │
│     ┌─────────────────────────────────────────┐                     │
│     │ sk-guest-mmx-abc123-xyz789              │                     │
│     │ → Routes to node mmx-abc123 only        │                     │
│     └─────────────────────────────────────────┘                     │
│                                                                      │
│  3. Public Key (Official)                                            │
│     Format:  sk-openmodelpool-com-github-lisiyu-openmodelpool-      │
│              public-key-v1                                            │
│     Binding: None (official public key)                             │
│     Scope:   Global shared pool, no node restriction                │
│     ┌─────────────────────────────────────────┐                     │
│     │ sk-openmodelpool-com-github-lisiyu-...  │                     │
│     │ → Routes to ANY node in shared pool     │                     │
│     └─────────────────────────────────────────┘                     │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.2 Authorization Chain (Fail-Close)

```
Request Authorization Flow (Fail-Close Model):

┌──────────────────────────────────────────────────────────────┐
│                                                              │
│  Incoming Request                                            │
│       │                                                      │
│       ▼                                                      │
│  ┌─────────────┐    No    ┌──────────┐                       │
│  │ Valid Key?  │─────────▶│ REJECT   │                       │
│  └──────┬──────┘          │ (401)    │                       │
│         │ Yes             └──────────┘                       │
│         ▼                                                    │
│  ┌─────────────┐                                             │
│  │ Classify    │                                             │
│  │ Key Type    │                                             │
│  └──────┬──────┘                                             │
│         │                                                    │
│    ┌────┴────┬─────────────┐                                 │
│    ▼         ▼             ▼                                 │
│ ┌──────┐ ┌───────┐ ┌──────────┐                              │
│ │Proxy │ │Guest  │ │Public    │                              │
│ │Key   │ │Key    │ │Key       │                              │
│ └──┬───┘ └──┬────┘ └────┬─────┘                              │
│    │        │            │                                    │
│    ▼        ▼            ▼                                    │
│ Route to  Route to    Route to                                │
│ owner's   issuing     shared                                  │
│ provider  node        pool                                    │
│    │        │            │                                    │
│    └────────┴────────────┘                                    │
│             │                                                 │
│             ▼                                                 │
│    ┌─────────────────┐   No    ┌──────────┐                  │
│    │ Provider exists │────────▶│ REJECT   │                  │
│    │ & has quota?    │         │ (503)    │                  │
│    └────────┬────────┘         └──────────┘                  │
│             │ Yes                                            │
│             ▼                                                │
│    ┌─────────────────┐                                       │
│    │ FORWARD REQUEST │                                       │
│    └─────────────────┘                                       │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

### 3.3 Key Routing Logic

```
Routing Priority: Personal > Shared > Public

┌─────────────────────────────────────────────────────────────┐
│                                                              │
│  Proxy Key (sk-xxx)                                          │
│  └─▶ Owner's bound provider(s)                              │
│      └─▶ If owner joined network: any node can relay        │
│                                                                      │
│  Guest Key (sk-guest-{node_id}-xxx)                         │
│  └─▶ Issuing node ONLY                                       │
│      └─▶ Cannot access other nodes                          │
│                                                                      │
│  Public Key (sk-openmodelpool-com-...)                       │
│  └─▶ Global shared pool                                      │
│      └─▶ All nodes with ShareToPool=true                    │
│      └─▶ Load balanced by reputation + availability         │
│                                                                      │
└─────────────────────────────────────────────────────────────┘
```

### 3.4 Key Classification (from source: auth.go)

```go
func ClassifyKey(key string) KeyType {
    switch {
    case key == PublicKeyValue:
        return KeyTypePublic
    case strings.HasPrefix(key, "sk-guest-"):
        return KeyTypeGuest
    case strings.HasPrefix(key, "sk-"):
        return KeyTypeProxy
    default:
        return KeyTypeUnknown
    }
}
```

---

## 4. Capability Declaration & Service Broadcasting

### 4.1 Concept

Each node broadcasts what AI model services it can provide — analogous to BitTorrent's `bitfield` message that declares which pieces a peer holds.

```
┌─────────────────────────────────────────────────────────────┐
│              BitTorrent ↔ OpenModelPool Mapping              │
├─────────────────────────────────────────────────────────────┤
│  BitTorrent:  "I have pieces [0,2,3,6,7]"                   │
│  OpenModelPool: "I can serve [gpt-4o, claude-3, deepseek]"  │
│                                                              │
│  BitTorrent:  Upload bandwidth limit                        │
│  OpenModelPool: API quota / rate limit                      │
└─────────────────────────────────────────────────────────────┘
```

### 4.2 CapabilityClaim Data Structure

```go
// CapabilityClaim — What a node can provide
type CapabilityClaim struct {
    NodeID      string           `json:"node_id"`
    Timestamp   time.Time        `json:"timestamp"`
    ExpiresAt   time.Time        `json:"expires_at"`
    
    // Model capabilities
    Models      []ModelCapability `json:"models"`
    
    // Node resources
    MaxConcurrent int            `json:"max_concurrent"`  // Max parallel requests
    RateLimit     int            `json:"rate_limit"`       // Requests per minute
    QuotaUsed     int64          `json:"quota_used"`       // Consumed quota
    QuotaLimit    int64          `json:"quota_limit"`      // Total quota
    
    // Signature
    Signature   []byte           `json:"signature"`        // Ed25519 signature
}

// ModelCapability — Per-model capability declaration
type ModelCapability struct {
    Provider    string    `json:"provider"`     // "openai", "anthropic"
    Model       string    `json:"model"`        // "gpt-4o", "claude-3-opus"
    Version     string    `json:"version"`      // Model version/date
    Available   bool      `json:"available"`    // Currently available?
    AvgLatency  int64     `json:"avg_latency"`  // Average latency in ms
    SuccessRate float64   `json:"success_rate"` // Historical success rate
}
```

### 4.3 Broadcasting Mechanism

```
Capability Update Flow:

┌─────────┐                    ┌─────────┐
│ Node A  │                    │ Node B  │
└────┬────┘                    └────┬────┘
     │                              │
     │  1. Capability Change        │
     │  (new model loaded)          │
     │                              │
     │  2. Sign Claim (Ed25519)     │
     │                              │
     │  3. Gossip Broadcast ───────▶│
     │     /openmodelpool/capability │
     │                              │
     │  4. Verify Signature ◀───────│
     │                              │
     │  5. Update local routing     │
     │     table with new claim     │
     │                              │
     └──────────────────────────────┘
```

### 4.4 PeerCapabilities Structure (from source: network.go)

```go
type PeerCapabilities struct {
    Providers   []string `json:"providers"`   // ["openai", "anthropic"]
    Bandwidth   string   `json:"bandwidth"`   // "100Mbps"
    CanRelay    bool     `json:"can_relay"`
    CanSeed     bool     `json:"can_seed"`
}

// v3.1: Unified peer model with share_to_pool toggle
// Controls whether node contributes providers to shared pool
ShareToPool bool `json:"share_to_pool"` // Default: false (opt-in)
```

---

## 5. Contribution Ledger System

### 5.1 Three-Layer Architecture (Zero Cost)

```
┌─────────────────────────────────────────────────────────────────┐
│                    Contribution Ledger System                     │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  ┌───────────────────────────────────────────────────────────┐   │
│  │ Layer 3: Token Economy (Reserved Interface)                │   │
│  │ • Token minting/burning                                   │   │
│  │ • Smart contract settlement                                │   │
│  │ • Exchange integration                                     │   │
│  │ • Options: IOTA native token / Custom credits             │   │
│  └───────────────────────────────┬───────────────────────────┘   │
│                                  │ (Future)                       │
│  ┌───────────────────────────────┴───────────────────────────┐   │
│  │ Layer 2: IPFS + IOTA (Optional Persistence)                │   │
│  │ • IPFS: Free storage via public gateways                   │   │
│  │ • IOTA: DAG architecture, ZERO gas fees                    │   │
│  │ • Only major events (>$100, node bans, disputes)          │   │
│  └───────────────────────────────┬───────────────────────────┘   │
│                                  │ (Sync)                         │
│  ┌───────────────────────────────┴───────────────────────────┐   │
│  │ Layer 1: Gossip Ledger (Current Implementation)            │   │
│  │ • Local LevelDB storage                                   │   │
│  │ • P2P synchronization via Gossip                          │   │
│  │ • Ed25519 signature verification                          │   │
│  │ • Cross-validation (min 3 confirmations)                  │   │
│  └───────────────────────────────────────────────────────────┘   │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 5.2 Layer 1: Gossip Ledger

#### Contribution Records

```go
// ContribRecord — Individual contribution events (from source: network.go)
type ContribRecord struct {
    Timestamp  string `json:"timestamp"`
    TokensUsed int64  `json:"tokens_used"`
    Requests   int64  `json:"requests"`
    FromNodeID string `json:"from_node_id"`
}

// Detailed contribution tracking
type ContributionRecord struct {
    ID             string    `json:"id"`
    PeerID         string    `json:"peer_id"`
    Timestamp      time.Time `json:"timestamp"`
    
    // Aggregate statistics
    RequestsServed int64     `json:"requests_served"`
    TokensProvided int64     `json:"tokens_provided"`
    CostUSD        float64   `json:"cost_usd"`
    
    // Per-model breakdown
    ModelBreakdown map[string]ModelContribution `json:"model_breakdown"`
    
    // Verification
    Signature      []byte    `json:"signature"`  // Ed25519
    Version        int       `json:"version"`
}

type ModelContribution struct {
    Provider    string  `json:"provider"`
    Model       string  `json:"model"`
    Requests    int64   `json:"requests"`
    Tokens      int64   `json:"tokens"`
    CostUSD     float64 `json:"cost_usd"`
    AvgLatency  int64   `json:"avg_latency_ms"`
    SuccessRate float64 `json:"success_rate"`
}
```

#### Ledger Operations

```
Record Flow:

User Request ──▶ Node Processes ──▶ Record Contribution
                                          │
                    ┌─────────────────────┼─────────────────────┐
                    ▼                     ▼                     ▼
              Local Storage        Ed25519 Sign         Gossip Broadcast
              (LevelDB)                                  (every 100 req
                                                         or 10 min)
                    │                     │                     │
                    └─────────────────────┼─────────────────────┘
                                          ▼
                                  Cross-Validation
                                  (min 3 confirmations
                                   from other nodes)
                                          │
                                          ▼
                                  Update Reputation
                                  & Routing Priority
```

#### Cross-Validation Mechanism

```
Cross-Validation Process:

┌──────────┐     ┌──────────┐     ┌──────────┐
│  Node A  │     │  Node B  │     │  Node C  │
│ (self)   │     │          │     │          │
└────┬─────┘     └────┬─────┘     └────┬─────┘
     │                │                │
     │ Report:        │ Report:        │ Report:
     │ served 50 req  │ served 48 req  │ served 52 req
     │ 10K tokens     │ 9.8K tokens    │ 10.2K tokens
     │                │                │
     └────────────────┼────────────────┘
                      ▼
              ┌─────────────────┐
              │ Deviation Check │
              │ Max: 20%        │
              │ If OK → accept  │
              │ If FAIL → flag  │
              └─────────────────┘
```

### 5.3 Layer 2: IPFS + IOTA

| Component | Purpose | Cost |
|-----------|---------|------|
| **IPFS** | Store contribution records, capability claims | $0 (public gateways) |
| **IOTA** | Immutable attestation of major events | $0 (zero gas DAG) |

**What gets stored on Layer 2:**
- Contributions > $100 USD
- Node ban decisions
- Dispute resolutions
- Reputation milestone changes

```go
// Layer 2 sync interface
type LedgerSynchronizer struct {
    gossip     *GossipLedger
    ipfsClient *ipfs.Client
    iotaClient *iota.Client
    
    MajorEventThreshold float64       // $100
    SyncInterval        time.Duration // 1 hour
}
```

### 5.4 Layer 3: Token Economy (Reserved)

```go
// TokenLedger — Reserved interface for future token integration
type TokenLedger interface {
    GetBalance(peerID string) (float64, error)
    Transfer(from, to string, amount float64) (txHash string, err error)
    RewardContribution(peerID string, amount float64) (txHash string, error)
    ChargeConsumption(peerID string, amount float64) (txHash string, error)
    Stake(peerID string, amount float64) (txHash string, error)
    Unstake(peerID string, amount float64) (txHash string, error)
}
```

---

## 6. Trust & Reputation System

### 6.1 Active Probing

```
Active Probing Flow:

┌─────────┐                         ┌─────────┐
│ Prober  │                         │ Target  │
│  Node   │                         │  Node   │
└────┬────┘                         └────┬────┘
     │                                   │
     │  1. Send 1-token test request    │
     │  (minimal cost probe)            │
     │──────────────────────────────────▶│
     │                                   │
     │  2. Response (success/fail)      │
     │◀──────────────────────────────────│
     │                                   │
     │  3. Record result                │
     │     - Latency                    │
     │     - Success/Fail               │
     │     - Response validity          │
     │                                   │
     │  4. Update reputation            │
     │     - EWMA scoring               │
     │     - Trust level adjustment     │
     │                                   │
```

**Probing Schedule:**
- New nodes: Every 5 minutes (first hour)
- Established nodes: Every 30 minutes
- High-reputation nodes: Every 2 hours
- Suspect nodes: Every 1 minute

### 6.2 Progressive Trust Levels

```
Trust Level Progression:

┌──────────┐    10 successful    ┌──────────┐
│   NEW    │ ──── requests ────▶ │   LOW    │
│ (trust=0)│                     │ (trust=25)│
└──────────┘                     └────┬─────┘
                                      │ 50 successful
                                      │ + 24h online
                                      ▼
                                ┌──────────┐
                                │  MEDIUM  │
                                │ (trust=50)│
                                └────┬─────┘
                                      │ 200 successful
                                      │ + 7d online
                                      │ + >80% success rate
                                      ▼
                                ┌──────────┐
                                │   HIGH   │
                                │ (trust=75)│
                                └────┬─────┘
                                      │ 1000 successful
                                      │ + 30d online
                                      │ + >95% success rate
                                      │ + cross-validated
                                      ▼
                                ┌──────────┐
                                │ VERIFIED │
                                │(trust=100)│
                                └──────────┘
```

### 6.3 Reputation Score (0-100)

```go
// Reputation scoring (from source: reputation.go)
type NodeReputation struct {
    NodeID         string  `json:"node_id"`
    Availability   float64 `json:"availability"`   // 0-100, EWMA
    Latency        float64 `json:"latency"`        // 0-100, EWMA
    Accuracy       float64 `json:"accuracy"`       // 0-100, EWMA
    OverallScore   float64 `json:"overall_score"`  // Weighted composite
    Grade          string  `json:"grade"`          // S/A/B/C/D
    TotalRequests  int64   `json:"total_requests"`
    FailedRequests int64   `json:"failed_requests"`
}

// EWMA update (alpha = 0.3)
const repEwmaAlpha = 0.3

// Overall score calculation (weights):
// Availability: 40%
// Latency:      30%
// Accuracy:     20%
// Peer scores:  10%
```

**Grading Scale:**
| Grade | Score Range | Description |
|-------|-------------|-------------|
| S | 95-100 | Elite — verified, high throughput |
| A | 80-94 | Excellent — reliable, low latency |
| B | 60-79 | Good — normal operation |
| C | 40-59 | Fair — occasional issues |
| D | 0-39 | Poor — isolate or ban |

### 6.4 Route Priority by Trust

```
Request Routing Priority:

┌──────────────────────────────────────────────────────┐
│                                                       │
│  VERIFIED (100)  ████████████████████████████  40%   │
│  HIGH (75)       ██████████████████████        30%   │
│  MEDIUM (50)     ████████████                  20%   │
│  LOW (25)        ████                          8%    │
│  NEW (0)         █                             2%    │
│                                                       │
└──────────────────────────────────────────────────────┘
```

---

## 7. False Capability Defense

### 7.1 The Problem

Nodes may claim they can serve a model but actually cannot — either due to misconfiguration, expired API keys, or malicious intent.

### 7.2 Multi-Layer Defense

```
False Capability Defense System:

┌─────────────────────────────────────────────────────────────┐
│                                                               │
│  Layer 1: Active Probing                                      │
│  ┌─────────────────────────────────────────────────────┐     │
│  │ Send 1-token test request periodically              │     │
│  │ • Cost: ~$0.00001 per probe                         │     │
│  │ • Detects: expired keys, rate limits, offline nodes │     │
│  └─────────────────────────────────────────────────────┘     │
│                                                               │
│  Layer 2: Cross-Validation                                    │
│  ┌─────────────────────────────────────────────────────┐     │
│  │ Multiple nodes independently verify the same claim  │     │
│  │ • Min 3 independent verifications required          │     │
│  │ • Discrepancies trigger investigation               │     │
│  └─────────────────────────────────────────────────────┘     │
│                                                               │
│  Layer 3: Punishment Mechanism                                │
│  ┌─────────────────────────────────────────────────────┐     │
│  │ Progressive enforcement:                            │     │
│  │ • Success rate < 70%: Warning issued                │     │
│  │ • Success rate < 50%: Reduced routing priority      │     │
│  │ • Success rate < 30%: Isolated (removed from pool)  │     │
│  │ • Success rate < 10%: Banned (globally broadcast)   │     │
│  └─────────────────────────────────────────────────────┘     │
│                                                               │
│  Layer 4: Global Broadcast                                    │
│  ┌─────────────────────────────────────────────────────┐     │
│  │ Ban information propagated to ALL nodes via GossipSub│     │
│  │ • Immediate routing table update                    │     │
│  │ • Banned node cannot serve any requests             │     │
│  │ • Requires multi-node consensus to lift ban         │     │
│  └─────────────────────────────────────────────────────┘     │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

### 7.3 Cost-Benefit Analysis

```
Why False Claims Are Irrational:

Cost of false claim:
  • 1-token probe cost: ~$0.00001
  • Detection time: < 5 minutes
  • Penalty: permanent ban + reputation loss
  • Recovery: requires 30+ days of honest service

Benefit of false claim:
  • Temporary routing priority (removed upon detection)
  • No actual revenue (can't serve real requests)

Conclusion: Cost >> Benefit → Self-enforcing honesty
```

---

## 8. Request Routing & Load Balancing

### 8.1 Five-Dimension Scoring Engine

```
┌─────────────────────────────────────────────────────────────┐
│              Dynamic Load Balancing Engine                    │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  Score = w₁×Trust + w₂×Reputation + w₃×Latency             │
│        + w₄×Availability + w₅×Contribution                  │
│                                                               │
│  ┌────────────────────────────────────────────────────┐      │
│  │ Dimension          │ Weight │ Source               │      │
│  ├────────────────────────────────────────────────────┤      │
│  │ Trust Level        │  25%   │ Progressive trust    │      │
│  │ Reputation Score   │  25%   │ EWMA (0-100)         │      │
│  │ Latency Score      │  20%   │ Recent avg latency   │      │
│  │ Availability       │  15%   │ Uptime EWMA          │      │
│  │ Contribution       │  15%   │ Share ratio          │      │
│  └────────────────────────────────────────────────────┘      │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

### 8.2 Routing Decision Flow

```
Request Routing Flow:

┌──────────┐
│ Request  │
│ Arrives  │
└────┬─────┘
     │
     ▼
┌──────────────────┐
│ Classify Key     │
│ (Proxy/Guest/    │
│  Public)         │
└────┬─────────────┘
     │
     ▼
┌──────────────────┐
│ Find Candidates  │
│ • Capability     │
│   matching       │
│ • Trust ≥ min    │
│ • Online         │
└────┬─────────────┘
     │
     ▼
┌──────────────────┐
│ Score Candidates │
│ (5-dimension)    │
└────┬─────────────┘
     │
     ▼
┌──────────────────┐
│ Select Best Node │
│ • Highest score  │
│ • Fallback chain │
└────┬─────────────┘
     │
     ▼
┌──────────────────┐     Fail     ┌─────────────────┐
│ Forward Request  │────────────▶│ Try Next Node   │
│                  │              │ (up to 3 hops)  │
└────────┬─────────┘              └─────────────────┘
         │ Success
         ▼
┌──────────────────┐
│ Record Result    │
│ • Update reput.  │
│ • Log contrib.   │
│ • Return response│
└──────────────────┘
```

### 8.3 Relay Mechanism (from source: network_relay.go)

```
Relay Path:

Client ──▶ Node A (relay) ──▶ Node B (target) ──▶ Provider API
                │                    │
                │ X-OpenModelPool-   │
                │ Agent-Hop: 1       │
                │                    │
                │ /network/mmx-xxx/  │
                │ v1/chat/completions│
                └────────────────────┘

Max hops: 3 (prevents infinite loops)
Path stripping: /network/{node_id} removed at target
```

### 8.4 Regional Awareness

```
Cross-Region Routing:

┌─────────────────────────────────────────────────────┐
│                                                      │
│  ┌─────────┐     ┌─────────┐     ┌─────────┐       │
│  │  US-W   │     │  EU-W   │     │  AP-SE  │       │
│  │  12 nodes│────│  8 nodes │────│  15 nodes│       │
│  └────┬────┘     └────┬────┘     └────┬────┘       │
│       │               │               │              │
│       └───────────────┼───────────────┘              │
│                       │                              │
│               Prefer same-region                     │
│               routing (lower latency)                │
│                                                      │
│  Cross-region only when:                            │
│  • Same-region node unavailable                     │
│  • Same-region node overloaded                      │
│  • Request requires specific model not in region    │
│                                                      │
└─────────────────────────────────────────────────────┘
```

---

## 9. Security Architecture

### 9.1 Authorization Chain (P0 Priority)

```
Security Layers:

┌─────────────────────────────────────────────────────────────┐
│                                                               │
│  1. Key Validation (Fail-Close)                              │
│     ┌───────────────────────────────────────────────────┐   │
│     │ Any error in key validation → REJECT (401)        │   │
│     │ • Malformed key → reject                          │   │
│     │ • Unknown key type → reject                       │   │
│     │ • Expired guest key → reject                      │   │
│     │ • Guest key wrong node → reject (403)             │   │
│     └───────────────────────────────────────────────────┘   │
│                                                               │
│  2. Signature Verification (Ed25519)                         │
│     ┌───────────────────────────────────────────────────┐   │
│     │ All capability claims signed with Ed25519         │   │
│     │ • Fast: 10x faster than RSA                       │   │
│     │ • Compact: 64-byte signatures                     │   │
│     │ • Secure: 256-bit security level                  │   │
│     └───────────────────────────────────────────────────┘   │
│                                                               │
│  3. Anti-Replay Protection                                   │
│     ┌───────────────────────────────────────────────────┐   │
│     │ Timestamps + nonce in all signed messages         │   │
│     │ • Claims expire after TTL (48h default)           │   │
│     │ • Duplicate detection via record ID               │   │
│     └───────────────────────────────────────────────────┘   │
│                                                               │
│  4. Data Integrity                                            │
│     ┌───────────────────────────────────────────────────┐   │
│     │ SHA-256 hashing for all record identifiers        │   │
│     │ • Node ID → SHA-256 → 256-bit position in DHT    │   │
│     │ • Record ID → verify no tampering                 │   │
│     └───────────────────────────────────────────────────┘   │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

### 9.2 Relay Security

```go
// Relay security measures (from source: network_relay.go)

const (
    headerRelayHop  = "X-OpenModelPool-Agent-Hop"   // Hop counter
    headerRelayFrom = "X-OpenModelPool-Agent-Relay-From"
    maxRelayHops    = 3                              // Max relay depth
)

// Loop prevention: hop count checked at each relay
if hopCount >= maxRelayHops {
    writeError(w, 508, "max relay hops exceeded")
    return
}

// Key-type-based routing restrictions
switch ClassifyKey(bearerKey) {
case KeyTypeGuest:
    guestNodeID, valid := ValidateGuestKey(bearerKey)
    if guestNodeID != "" && targetNodeID != guestNodeID {
        writeError(w, 403, "guest keys can only access the issuing node")
        return
    }
}
```

### 9.3 Node ID Format

```
Node ID Structure:

mmx-{random_suffix}

Example: mmx-abc123def456

• Prefix "mmx-" identifies OpenModelPool nodes
• Suffix provides uniqueness
• Used in DHT hash computation: SHA-256("mmx-abc123def456")
```

---

## 10. BitTorrent Protocol Analogy

### 10.1 Concept Mapping

```
┌─────────────────────────────────────────────────────────────────────┐
│               BitTorrent ↔ OpenModelPool Complete Mapping            │
├───────────────────────┬─────────────────────────────────────────────┤
│ BitTorrent            │ OpenModelPool                               │
├───────────────────────┼─────────────────────────────────────────────┤
│ Torrent client        │ Deployed proxy service node                 │
│ (uTorrent/qBittorrent)│                                             │
├───────────────────────┼─────────────────────────────────────────────┤
│ Torrent metadata      │ SharedModels + SharedProviders              │
│ (.torrent file)       │ + CapabilityClaim (version + capacity)      │
├───────────────────────┼─────────────────────────────────────────────┤
│ Tracker               │ Seed nodes + Bootstrap registry             │
│                       │ (no GitHub dependency in production)        │
├───────────────────────┼─────────────────────────────────────────────┤
│ DHT (Kademlia)        │ 256-bit DHT + k-buckets (k=20)             │
│                       │ α=10 for optimal latency                   │
├───────────────────────┼─────────────────────────────────────────────┤
│ Peer Exchange (PEX)   │ GossipSub protocol                          │
├───────────────────────┼─────────────────────────────────────────────┤
│ Seeder                │ Node sharing Provider Keys to pool          │
├───────────────────────┼─────────────────────────────────────────────┤
│ Leecher               │ Node consuming without contributing         │
│                       │ (discouraged by reputation system)          │
├───────────────────────┼─────────────────────────────────────────────┤
│ Pieces                │ Model request/response units                │
├───────────────────────┼─────────────────────────────────────────────┤
│ Bitfield              │ CapabilityClaim (model availability)        │
├───────────────────────┼─────────────────────────────────────────────┤
│ Have message          │ Capability update broadcast (GossipSub)     │
├───────────────────────┼─────────────────────────────────────────────┤
│ Choking algorithm     │ Dynamic capacity control + priority queue   │
│                       │ (based on contribution ratio)              │
├───────────────────────┼─────────────────────────────────────────────┤
│ Tit-for-Tat           │ Reputation system (contribution tracking)   │
├───────────────────────┼─────────────────────────────────────────────┤
│ Upload/Download ratio │ TokenBudget / TokenUsed                     │
│                       │ (per-model fine-grained tracking)           │
├───────────────────────┼─────────────────────────────────────────────┤
│ SHA1 piece hash       │ Ed25519 signature + SHA-256 hash            │
├───────────────────────┼─────────────────────────────────────────────┤
│ Optimistic unchoke    │ Reserve slots for new/untested nodes        │
├───────────────────────┼─────────────────────────────────────────────┤
│ Rarest-first          │ Route to least-loaded capable node          │
├───────────────────────┼─────────────────────────────────────────────┤
│ Snubbing              │ Slow node detection + replacement           │
└───────────────────────┴─────────────────────────────────────────────┘
```

### 10.2 Key Differences

| Aspect | BitTorrent | OpenModelPool |
|--------|------------|---------------|
| **Content** | Static files (immutable) | Dynamic services (stateful) |
| **Latency** | Tolerant (minutes) | Sensitive (milliseconds) |
| **Verification** | SHA1 hash (deterministic) | API response (non-deterministic) |
| **Incentive** | Soft (tit-for-tat) | Hard (reputation + future token) |
| **Churn** | High (peer join/leave) | Medium (nodes more stable) |
| **Topology** | Mesh (unstructured) | DHT (structured + gossip) |

### 10.3 The Fundamental Insight

```
BitTorrent:
  "I have file piece #42, I'll upload it to you"
  → Static, verifiable, one-time transfer

OpenModelPool:
  "I can serve GPT-4o, I'll relay your request"
  → Dynamic, probabilistic, ongoing service

Key challenge: Service quality is non-deterministic.
Solution: Reputation system + active probing + cross-validation.
```

---

## 11. Performance Metrics & Targets

### 11.1 Core Performance Targets

| Metric | Target | Reference |
|--------|--------|-----------|
| DHT Lookup | O(log N) | Kademlia guarantee |
| DHT P50 Latency | ~0.3s | ProbeLab (α=10) |
| Gossip Propagation | <1s (99%) | LibPa (10K nodes) |
| Node Discovery | <5s | Bootstrap + DHT |
| False Claim Detection | <5 min | Active probing |
| Route Decision | <10ms | Local scoring |
| Relay Overhead | <50ms | Direct connection +1 hop |
| Max Relay Hops | 3 | Loop prevention |

### 11.2 Scalability Targets

| Scale | Nodes | Performance |
|-------|-------|-------------|
| Small | <100 | Sub-second routing |
| Medium | 100-10K | <2s node discovery |
| Large | 10K-100K | <5s node discovery |
| Massive | >100K | Consider supernode architecture |

### 11.3 DHT Configuration for Scale

```
Recommended Configuration by Network Size:

┌──────────────────────────────────────────────────────────────┐
│ Small + Stable (<100 nodes)                                  │
│   k=5, α=3, Refresh=30min, TTL=96h                          │
├──────────────────────────────────────────────────────────────┤
│ Small + High Churn (<100 nodes)                              │
│   k=10, α=5, Refresh=15min, TTL=48h                         │
├──────────────────────────────────────────────────────────────┤
│ Large + Stable (10K+ nodes)                                  │
│   k=15, α=5, Refresh=15min, TTL=96h                         │
├──────────────────────────────────────────────────────────────┤
│ Large + High Churn (10K+ nodes) ← TARGET                     │
│   k=20, α=10, Refresh=10min, TTL=48h                        │
└──────────────────────────────────────────────────────────────┘
```

### 11.4 Bandwidth Efficiency

```
Optimization Strategies:

┌─────────────────────────────────────────────────────────────┐
│  Strategy                    │ Savings │ Implementation     │
├─────────────────────────────────────────────────────────────┤
│  IHAVE/IWANT metadata gossip │ ~90%    │ GossipSub 1.1     │
│  IDONTWANT control messages  │ ~15%    │ GossipSub 1.2     │
│  Compact capability encoding │ ~80%    │ Bitfield + delta  │
│  IP diversity filter         │ N/A     │ /16 subnet limit  │
│  Connection pooling          │ 61x     │ HTTP keep-alive   │
└─────────────────────────────────────────────────────────────┘
```

---

## 12. Implementation Roadmap

### 12.1 Phase Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                    Implementation Phases                         │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  Phase 1: Core P2P Network ✅ COMPLETED                          │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ ✅ 256-bit DHT with K-buckets (k=20)                   │    │
│  │ ✅ Three key types (Proxy, Guest, Public)               │    │
│  │ ✅ Relay mechanism with hop counting                    │    │
│  │ ✅ Gossip-based peer discovery                          │    │
│  │ ✅ Basic contribution tracking                          │    │
│  │ ✅ Peer capabilities + ShareToPool toggle               │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                   │
│  Phase 2: Contribution Ledger + Trust 🔄 IN PROGRESS             │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ ✅ Reputation manager (EWMA scoring)                    │    │
│  │ ✅ Grading system (S/A/B/C/D)                           │    │
│  │ 🔄 Gossip Ledger (LevelDB + cross-validation)          │    │
│  │ 🔄 Active probing implementation                        │    │
│  │ 🔄 IPFS storage integration                             │    │
│  │ 🔄 IOTA attestation integration                         │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                   │
│  Phase 3: Advanced Routing ⏳ PLANNED                            │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ ⏳ Cross-node intelligent routing                       │    │
│  │ ⏳ Global load balancing (5-dimension scoring)          │    │
│  │ ⏳ Regional awareness + cross-region failover           │    │
│  │ ⏳ Choking algorithm (contribution-based priority)      │    │
│  │ ⏳ Slow node detection (snubbing)                       │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                   │
│  Phase 4: Token Economy 🔮 FUTURE (Optional)                     │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ 🔮 TokenLedger interface implementation                 │    │
│  │ 🔮 IOTA native token or custom credits                  │    │
│  │ 🔮 Staking mechanism                                    │    │
│  │ 🔮 DAO governance (optional)                            │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 12.2 Detailed Phase 2 Tasks

```
Phase 2 Implementation Checklist:

Contribution Ledger:
  □ ContributionRecord data structure
  □ GossipLedger core methods (Record, Verify, Broadcast)
  □ Ed25519 signature integration
  □ Cross-validation (min 3 confirmations)
  □ LevelDB local storage
  □ LedgerSynchronizer for Layer 2

Trust System:
  □ Active probing scheduler
  □ 1-token test request implementation
  □ Progressive trust level tracking
  □ Punishment automation (warn → isolate → ban)
  □ Global ban broadcast

Storage Integration:
  □ IPFS client integration
  □ IOTA zero-gas attestation
  □ Major event filtering (>$100 threshold)
  □ Sync interval management (hourly)
```

---

## 13. Cost Analysis

### 13.1 Operational Costs

```
┌─────────────────────────────────────────────────────────────────┐
│                    Monthly Operating Costs                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  Component              │ Cost      │ Notes                      │
│  ───────────────────────┼───────────┼──────────────────────────  │
│  DHT Infrastructure     │ $0        │ Runs on peer nodes         │
│  GossipSub Network      │ $0        │ Peer-to-peer messaging     │
│  IPFS Storage           │ $0        │ Public gateways (free)     │
│  IOTA Attestation       │ $0        │ Zero gas fees (DAG)        │
│  Bootstrap Nodes        │ $0        │ Community-hosted           │
│  ───────────────────────┼───────────┼──────────────────────────  │
│  TOTAL OPERATING COST   │ $0/month  │ Fully decentralized        │
│                                                                   │
├─────────────────────────────────────────────────────────────────┤
│                    Node Deployment Costs                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  Component              │ Cost      │ Notes                      │
│  ───────────────────────┼───────────┼──────────────────────────  │
│  Software               │ $0        │ Open source                │
│  API Keys               │ Variable  │ User's own provider keys   │
│  Compute (node)         │ $0        │ Runs on user's machine     │
│  Network bandwidth      │ ~$0       │ Minimal (~1.2 Mbps)        │
│  ───────────────────────┼───────────┼──────────────────────────  │
│  TOTAL NODE COST        │ $0 + keys │ Self-hosted                │
│                                                                   │
├─────────────────────────────────────────────────────────────────┤
│                    Transaction Costs                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  Operation              │ Cost      │ Notes                      │
│  ───────────────────────┼───────────┼──────────────────────────  │
│  Contribution record    │ $0        │ Gossip ledger (local)      │
│  IPFS store             │ $0        │ Public gateway             │
│  IOTA attest            │ $0        │ Zero gas                   │
│  DHT lookup             │ $0        │ Peer routing               │
│  Capability broadcast   │ $0        │ GossipSub                  │
│  ───────────────────────┼───────────┼──────────────────────────  │
│  TOTAL TX COST          │ $0/tx     │ Zero-cost operations       │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 13.2 Cost Efficiency Summary

```
OpenModelPool vs Traditional Infrastructure:

┌─────────────────────────────────────────────────────────────┐
│                        │ Traditional │ OpenModelPool         │
│  ──────────────────────┼─────────────┼───────────────────── │
│  Infrastructure        │ $100-1000/mo│ $0                   │
│  Per-request cost      │ $0.001-0.01 │ $0                   │
│  Scaling cost          │ Linear      │ $0 (P2P)             │
│  Storage cost          │ $0.023/GB/mo│ $0 (IPFS)            │
│  Attestation cost      │ $0.01-0.10  │ $0 (IOTA zero gas)  │
│  ──────────────────────┼─────────────┼───────────────────── │
│  Total (10K req/day)   │ $300-3000/mo│ $0 + API keys        │
└─────────────────────────────────────────────────────────────┘

Note: Users still pay their own API provider costs.
The network layer itself is completely free.
```

---

## Appendix A: Key Source Files Reference

| File | Purpose |
|------|---------|
| `network.go` | Network mode, config, peer info, contribution records |
| `dht.go` | 256-bit Kademlia DHT implementation |
| `network_relay.go` | Decentralized relay handler, key routing |
| `network_discovery.go` | Peer discovery (registry + DHT + gossip) |
| `network_seed.go` | Seed node management |
| `network_loadbalancer.go` | Load balancing engine |
| `network_global_pool.go` | Global shared pool |
| `network_region.go` | Regional awareness |
| `network_quota.go` | Quota allocation |
| `reputation.go` | Reputation manager, EWMA scoring |
| `auth.go` | Authentication, key classification |
| `providers.go` | Preset provider definitions (34 platforms) |
| `discovery.go` | Bootstrap + peer exchange |

## Appendix B: Configuration Constants

```go
// DHT Parameters
const HashSizeBytes = 32        // 256-bit hash
const KBucketSize = 20          // k-bucket capacity
const NumBuckets = 256          // one per bit
const DHTLookupParallelism = 3  // α (can increase to 10)

// Network Parameters
const p2pNodeIDPrefix = "mmx-"
const maxRelayHops = 3
const routeTTL = 10 * time.Minute
const refreshInterval = 5 * time.Minute

// Reputation Parameters
const repEwmaAlpha = 0.3        // EWMA smoothing factor

// Ledger Parameters (planned)
const BroadcastInterval = 10 * time.Minute
const MinConfirmations = 3
const MaxDeviation = 0.20       // 20%
const MajorEventThreshold = 100.0 // $100 USD
```

---

*This document is maintained as part of the OpenModelPool project. For implementation details, refer to the source code in `/root/modelmux-deploy/`.*
