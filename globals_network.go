package main

// ============================================================================
// Federation & Network — global singleton pointers for P2P networking,
// federation, gossip, and related subsystems.
// Initialized in initAllFederation() and initAllNetwork() (see init.go).
// ============================================================================

var node *NodeIdentity

var fed *FederationManager

var gossip *GossipManager

var repMgr *ReputationManager

var allocMgr *AllocationManager

var msgMgr *MessageManager

var nwm *nodeWeightManager

var invMgr *inviteManager

// Global singleton, initialized in initAllFederation().
var updateManager *UpdateManager

var netMgr *NetworkManager

var routeTable *RouteTable

var guestKeyStore *GuestKeyStore

var guestKeyUsage *guestKeyUsageTracker

// lbInstance is the package-level singleton.
var lbInstance *LoadBalancer

var quotaMgr *OpenKeyQuotaManager

// regionManager is the process-wide RegionManager instance. It is wired up by
// initRegionManager() (see stubs.go) during startup. Every access site must
// nil-check it, because it stays nil in personal mode or if initialization
// failed — the region endpoints then degrade to safe empty responses.
var regionManager *RegionManager

var balanceEngine *BalanceEngine

var globalPool *GlobalPool

// publicQuota is the global instance for public key quota tracking.
var publicQuota *PublicKeyQuota

// algoChain is the package-level chain used by the quota/balance engines.
var algoChain *AlgorithmChain

// governor is the package-level governance store, wired in initCore().
var governor *AlgorithmGovernor

var quotaPriorityMgr *quotaPriorityManager

var tunnel *TunnelManager

// nodeRegistry is the package-level on-disk node registry. It is initialized in
// initNetworkManager (via initNodeRegistry) right after the in-memory route table
// is created. Every persistence helper below nil-checks it, so it is safe to call
// before initialization (e.g. from unit tests that build a bare RouteTable).
var nodeRegistry *NodeRegistry
