package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"
)

// ============================================================
// config.go — toUpper (pure function, no setup needed)
// ============================================================

func TestBatch1_toUpper_Lowercase(t *testing.T) {
	got := toUpper("hello")
	if got != "HELLO" {
		t.Errorf("toUpper(\"hello\") = %q, want %q", got, "HELLO")
	}
}

func TestBatch1_toUpper_MixedCase(t *testing.T) {
	got := toUpper("Hello_World123")
	if got != "HELLO_WORLD123" {
		t.Errorf("toUpper(\"Hello_World123\") = %q, want %q", got, "HELLO_WORLD123")
	}
}

func TestBatch1_toUpper_Empty(t *testing.T) {
	got := toUpper("")
	if got != "" {
		t.Errorf("toUpper(\"\") = %q, want %q", got, "")
	}
}

func TestBatch1_toUpper_AlreadyUpper(t *testing.T) {
	got := toUpper("ABC")
	if got != "ABC" {
		t.Errorf("toUpper(\"ABC\") = %q, want %q", got, "ABC")
	}
}

// ============================================================
// provider.go — AllModelsFiltered, FindCandidates (need pm)
// ============================================================

func TestBatch1_AllModelsFiltered_AdminSeesAll(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{
		ID: "p1", Name: "TestProvider", Type: "openai_compatible",
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test",
		Enabled: true, Models: makeModelDef("model-a", "model-b"),
		Priority: 1, AccessControl: ProviderAccessControl{ShareToPool: true},
	})

	models := pm.AllModelsFiltered("admin")
	if len(models) < 2 {
		t.Errorf("admin should see at least 2 models, got %d", len(models))
	}
	seen := map[string]bool{}
	for _, m := range models {
		seen[m.ID] = true
	}
	if !seen["model-a"] || !seen["model-b"] {
		t.Errorf("admin should see model-a and model-b, seen=%v", seen)
	}
}

func TestBatch1_AllModelsFiltered_GuestFiltered(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{
		ID: "p1", Name: "PrivateOnly", Type: "openai_compatible",
		BaseURL: "https://api.example.com/v1",
		Enabled: true, Models: makeModelDef("model-x"),
		Priority: 1, AccessControl: ProviderAccessControl{ShareToPool: false},
		APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-priv", AccessControl: "private", Enabled: true, Priority: 1}},
	})

	models := pm.AllModelsFiltered("guest")
	if len(models) != 0 {
		t.Errorf("guest should see 0 models from private-only provider, got %d", len(models))
	}
}

func TestBatch1_AllModelsFiltered_EmptyProviders(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	models := pm.AllModelsFiltered("admin")
	if len(models) != 0 {
		t.Errorf("with no providers, should get 0 models, got %d", len(models))
	}
}

func TestBatch1_FindCandidates_FindsModel(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{
		ID: "p1", Name: "TestProvider", Type: "openai_compatible",
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test",
		Enabled: true, Models: makeModelDef("deepseek-chat"),
		Priority: 1,
	})

	cands := pm.FindCandidates("deepseek-chat")
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
	if cands[0].Model != "deepseek-chat" {
		t.Errorf("candidate model = %q, want %q", cands[0].Model, "deepseek-chat")
	}
}

func TestBatch1_FindCandidates_DisabledProvider(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{
		ID: "p1", Name: "DisabledProvider", Type: "openai_compatible",
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test",
		Enabled: false, Models: makeModelDef("deepseek-chat"),
		Priority: 1,
	})

	cands := pm.FindCandidates("deepseek-chat")
	if len(cands) != 0 {
		t.Errorf("disabled provider should yield 0 candidates, got %d", len(cands))
	}
}

func TestBatch1_FindCandidates_DisabledModel(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{
		ID: "p1", Name: "TestProvider", Type: "openai_compatible",
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test",
		Enabled: true,
		Models: []ModelDef{{ID: "model-x", Name: "model-x", Enabled: false}},
		Priority: 1,
	})

	cands := pm.FindCandidates("model-x")
	if len(cands) != 0 {
		t.Errorf("disabled model should yield 0 candidates, got %d", len(cands))
	}
}

func TestBatch1_FindCandidates_NoMatchingModel(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{
		ID: "p1", Name: "TestProvider", Type: "openai_compatible",
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test",
		Enabled: true, Models: makeModelDef("model-a"),
		Priority: 1,
	})

	cands := pm.FindCandidates("nonexistent-model")
	if len(cands) != 0 {
		t.Errorf("nonexistent model should yield 0 candidates, got %d", len(cands))
	}
}

// ============================================================
// network_keys.go — GetGuestKeyAccessPublicPool, DeleteGuestKey, MarkAsCollaborator
// ============================================================

func TestBatch1_GetGuestKeyAccessPublicPool_ValidKey(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	origNetMgr := netMgr
	netMgr = nil
	t.Cleanup(func() { netMgr = origNetMgr; guestKeyStore = origGKS })

	key, err := GenerateGuestKey("test-node")
	if err != nil {
		t.Fatalf("GenerateGuestKey failed: %v", err)
	}

	nodeID, accessPool, valid := GetGuestKeyAccessPublicPool(key)
	if !valid {
		t.Error("expected valid=true for existing guest key")
	}
	if nodeID != "test-node" {
		t.Errorf("nodeID = %q, want %q", nodeID, "test-node")
	}
	if accessPool {
		t.Error("without shared mode, accessPublicPool should be false")
	}
}

func TestBatch1_GetGuestKeyAccessPublicPool_NonGuestKey(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	t.Cleanup(func() { guestKeyStore = origGKS })

	_, _, valid := GetGuestKeyAccessPublicPool("sk-someproxykey")
	if valid {
		t.Error("non-guest key should return valid=false")
	}
}

func TestBatch1_GetGuestKeyAccessPublicPool_RevokedKey(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	t.Cleanup(func() { guestKeyStore = origGKS })

	key, err := GenerateGuestKey("test-node")
	if err != nil {
		t.Fatalf("GenerateGuestKey failed: %v", err)
	}
	if err := guestKeyStore.RevokeGuestKey(key); err != nil {
		t.Fatalf("RevokeGuestKey failed: %v", err)
	}

	_, _, valid := GetGuestKeyAccessPublicPool(key)
	if valid {
		t.Error("revoked key should return valid=false")
	}
}

func TestBatch1_DeleteGuestKey_RevokedFirst(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	t.Cleanup(func() { guestKeyStore = origGKS })

	key, err := GenerateGuestKey("test-node")
	if err != nil {
		t.Fatalf("GenerateGuestKey failed: %v", err)
	}
	if err := guestKeyStore.RevokeGuestKey(key); err != nil {
		t.Fatalf("RevokeGuestKey failed: %v", err)
	}
	if err := guestKeyStore.DeleteGuestKey(key); err != nil {
		t.Errorf("DeleteGuestKey on revoked key should succeed, got: %v", err)
	}
}

func TestBatch1_DeleteGuestKey_NotRevoked(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	t.Cleanup(func() { guestKeyStore = origGKS })

	key, err := GenerateGuestKey("test-node")
	if err != nil {
		t.Fatalf("GenerateGuestKey failed: %v", err)
	}
	err = guestKeyStore.DeleteGuestKey(key)
	if err == nil {
		t.Error("DeleteGuestKey on non-revoked key should fail")
	}
}

func TestBatch1_DeleteGuestKey_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	t.Cleanup(func() { guestKeyStore = origGKS })

	err := guestKeyStore.DeleteGuestKey("sk-guest-nonexistent-abc")
	if err == nil {
		t.Error("DeleteGuestKey on nonexistent key should fail")
	}
}

func TestBatch1_MarkAsCollaborator_AddsPrefix(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	t.Cleanup(func() { guestKeyStore = origGKS })

	key, err := GenerateGuestKey("test-node", GuestKeyOptions{Note: "test note"})
	if err != nil {
		t.Fatalf("GenerateGuestKey failed: %v", err)
	}
	if err := guestKeyStore.MarkAsCollaborator(key); err != nil {
		t.Fatalf("MarkAsCollaborator failed: %v", err)
	}
	rec := guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil {
		t.Fatal("record not found")
	}
	if rec.Note != "[协作] test note" {
		t.Errorf("note = %q, want %q", rec.Note, "[协作] test note")
	}
}

func TestBatch1_MarkAsCollaborator_AlreadyMarked(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	t.Cleanup(func() { guestKeyStore = origGKS })

	key, err := GenerateGuestKey("test-node", GuestKeyOptions{Note: "[协作] existing"})
	if err != nil {
		t.Fatalf("GenerateGuestKey failed: %v", err)
	}
	if err := guestKeyStore.MarkAsCollaborator(key); err != nil {
		t.Fatalf("MarkAsCollaborator on already-marked key should not error, got: %v", err)
	}
	rec := guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil {
		t.Fatal("record not found")
	}
	if rec.Note != "[协作] existing" {
		t.Errorf("note should remain unchanged = %q, got %q", "[协作] existing", rec.Note)
	}
}

func TestBatch1_MarkAsCollaborator_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	origGKS := guestKeyStore
	initGuestKeyStore(env.dir)
	t.Cleanup(func() { guestKeyStore = origGKS })

	err := guestKeyStore.MarkAsCollaborator("sk-guest-nonexistent-abc")
	if err == nil {
		t.Error("MarkAsCollaborator on nonexistent key should fail")
	}
}

// ============================================================
// network_global_pool.go — AdjustQuota (PublicKeyQuota), CheckPublicKeyQuota, CheckQuota
// ============================================================

func newTestPublicKeyQuota() *PublicKeyQuota {
	return &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{"gpt-4o": 500},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        make(map[string]int64),
		lastDailyReset:    time.Now(),
		lastHourlyReset:   time.Now(),
	}
}

func TestBatch1_CheckQuota_AllowsWithinLimit(t *testing.T) {
	q := newTestPublicKeyQuota()
	allowed, reason, remaining := q.CheckQuota("1.2.3.4", "gpt-4o", 100)
	if !allowed {
		t.Errorf("expected allowed=true, got reason=%q", reason)
	}
	if remaining <= 0 {
		t.Errorf("expected positive remaining, got %d", remaining)
	}
}

func TestBatch1_CheckQuota_GlobalLimitExceeded(t *testing.T) {
	q := newTestPublicKeyQuota()
	q.globalUsedToday = q.GlobalDailyLimit
	allowed, reason, _ := q.CheckQuota("1.2.3.4", "gpt-4o", 100)
	if allowed {
		t.Error("should be rejected when global limit exceeded")
	}
	if reason == "" {
		t.Error("expected non-empty reject reason")
	}
}

func TestBatch1_CheckQuota_IPLimitExceeded(t *testing.T) {
	q := newTestPublicKeyQuota()
	q.ipUsage["1.2.3.4"] = &IPUsageTracker{DailyUsed: q.IPDailyLimit, LastReset: time.Now()}
	allowed, reason, _ := q.CheckQuota("1.2.3.4", "gpt-4o", 100)
	if allowed {
		t.Error("should be rejected when IP limit exceeded")
	}
	if reason == "" {
		t.Error("expected non-empty reject reason")
	}
}

func TestBatch1_CheckQuota_ModelLimitExceeded(t *testing.T) {
	q := newTestPublicKeyQuota()
	q.modelUsage["gpt-4o"] = 500
	allowed, reason, _ := q.CheckQuota("1.2.3.4", "gpt-4o", 100)
	if allowed {
		t.Error("should be rejected when model limit exceeded")
	}
	if reason == "" {
		t.Error("expected non-empty reject reason")
	}
}

func TestBatch1_AdjustQuota_Refund(t *testing.T) {
	q := newTestPublicKeyQuota()
	q.globalUsedToday = 500
	q.ipUsage["1.2.3.4"] = &IPUsageTracker{DailyUsed: 500, LastReset: time.Now()}
	q.modelUsage["gpt-4o"] = 200
	hourKey := time.Now().Format("2006-01-02-15")
	q.hourlyUsage[hourKey] = 200

	q.AdjustQuota("1.2.3.4", "gpt-4o", 500, 300)

	if q.globalUsedToday != 300 {
		t.Errorf("globalUsedToday = %d, want 300", q.globalUsedToday)
	}
	if q.ipUsage["1.2.3.4"].DailyUsed != 300 {
		t.Errorf("IP daily used = %d, want 300", q.ipUsage["1.2.3.4"].DailyUsed)
	}
	if q.modelUsage["gpt-4o"] != 0 {
		t.Errorf("model usage = %d, want 0", q.modelUsage["gpt-4o"])
	}
}

func TestBatch1_AdjustQuota_ChargeExtra(t *testing.T) {
	q := newTestPublicKeyQuota()
	q.globalUsedToday = 100
	q.ipUsage["1.2.3.4"] = &IPUsageTracker{DailyUsed: 100, LastReset: time.Now()}
	q.modelUsage["gpt-4o"] = 50
	hourKey := time.Now().Format("2006-01-02-15")
	q.hourlyUsage[hourKey] = 50

	q.AdjustQuota("1.2.3.4", "gpt-4o", 100, 200)

	if q.globalUsedToday != 200 {
		t.Errorf("globalUsedToday = %d, want 200", q.globalUsedToday)
	}
	if q.modelUsage["gpt-4o"] != 150 {
		t.Errorf("model usage = %d, want 150", q.modelUsage["gpt-4o"])
	}
}

func TestBatch1_AdjustQuota_NoDiff(t *testing.T) {
	q := newTestPublicKeyQuota()
	q.globalUsedToday = 100
	q.AdjustQuota("1.2.3.4", "gpt-4o", 100, 100)
	if q.globalUsedToday != 100 {
		t.Errorf("no diff should not change usage, got %d", q.globalUsedToday)
	}
}

func TestBatch1_CheckPublicKeyQuota_NilPublicQuota(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	origPQ := publicQuota
	publicQuota = nil
	t.Cleanup(func() { publicQuota = origPQ })

	gm := NewGlobalPoolManager()
	allowed, reason, _ := gm.CheckPublicKeyQuota("1.2.3.4", "gpt-4o", 100)
	if !allowed {
		t.Errorf("nil publicQuota should allow, got reason=%q", reason)
	}
}

func TestBatch1_CheckPublicKeyQuota_DelegatesToPublicQuota(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	origPQ := publicQuota
	publicQuota = newTestPublicKeyQuota()
	t.Cleanup(func() { publicQuota = origPQ })

	gm := NewGlobalPoolManager()
	allowed, _, remaining := gm.CheckPublicKeyQuota("1.2.3.4", "gpt-4o", 50)
	if !allowed {
		t.Error("should be allowed within limits")
	}
	if remaining <= 0 {
		t.Errorf("expected positive remaining, got %d", remaining)
	}
}

func TestBatch1_GlobalPoolManager_AdjustQuota(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	origPQ := publicQuota
	pq := newTestPublicKeyQuota()
	publicQuota = pq
	t.Cleanup(func() { publicQuota = origPQ })

	gm := NewGlobalPoolManager()
	pq.globalUsedToday = 500
	gm.AdjustQuota("1.2.3.4", "gpt-4o", 500, 300)
	if pq.globalUsedToday != 300 {
		t.Errorf("globalUsedToday = %d, want 300", pq.globalUsedToday)
	}
}

// ============================================================
// invite.go — CreateInvite, VerifyInvite, MarkUsed, GetInvites
// ============================================================

func setupNodeForInvite(t *testing.T, dir string) {
	t.Helper()
	origNode := node
	pub, priv, _ := ed25519.GenerateKey(nil)
	node = &NodeIdentity{
		nodeID:     "mmx-testnode",
		encPrivKey: encryptField(base64.StdEncoding.EncodeToString(priv)),
		pubKey:     pub,
		keyPath:    filepath.Join(dir, "node.key"),
	}
	t.Cleanup(func() { node = origNode })
}

func TestBatch1_CreateInvite_NodeNotInitialized(t *testing.T) {
	env := setupTestEnv(t)
	origInvMgr := invMgr
	origNode := node
	invMgr = &inviteManager{issued: make(map[string]*FederationInvite), used: make(map[string]bool), dataDir: env.dir}
	node = nil
	t.Cleanup(func() { invMgr = origInvMgr; node = origNode })

	_, err := invMgr.CreateInvite("*", "test", FederationInvitePublic, 24, "localhost:8080")
	if err == nil {
		t.Error("expected error when node not initialized")
	}
}

func TestBatch1_CreateInvite_Success(t *testing.T) {
	env := setupTestEnv(t)
	origInvMgr := invMgr
	invMgr = &inviteManager{issued: make(map[string]*FederationInvite), used: make(map[string]bool), dataDir: env.dir}
	t.Cleanup(func() { invMgr = origInvMgr })

	setupNodeForInvite(t, env.dir)

	invite, err := invMgr.CreateInvite("*", "testuser", FederationInvitePublic, 24, "localhost:8080")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	if invite.Signature == "" {
		t.Error("expected non-empty signature")
	}
	if invite.Inviter != "mmx-testnode" {
		t.Errorf("inviter = %q, want %q", invite.Inviter, "mmx-testnode")
	}
	if invite.Type != FederationInvitePublic {
		t.Errorf("type = %q, want %q", invite.Type, FederationInvitePublic)
	}
}

func TestBatch1_VerifyInvite_Valid(t *testing.T) {
	env := setupTestEnv(t)
	origInvMgr := invMgr
	invMgr = &inviteManager{issued: make(map[string]*FederationInvite), used: make(map[string]bool), dataDir: env.dir}
	t.Cleanup(func() { invMgr = origInvMgr })

	setupNodeForInvite(t, env.dir)

	invite, err := invMgr.CreateInvite("*", "testuser", FederationInvitePublic, 24, "localhost:8080")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	if err := invMgr.VerifyInvite(invite); err != nil {
		t.Errorf("VerifyInvite on valid invite should pass, got: %v", err)
	}
}

func TestBatch1_VerifyInvite_Expired(t *testing.T) {
	env := setupTestEnv(t)
	origInvMgr := invMgr
	invMgr = &inviteManager{issued: make(map[string]*FederationInvite), used: make(map[string]bool), dataDir: env.dir}
	t.Cleanup(func() { invMgr = origInvMgr })

	setupNodeForInvite(t, env.dir)

	invite, err := invMgr.CreateInvite("*", "testuser", FederationInvitePublic, 24, "localhost:8080")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	invite.ExpiresAt = time.Now().Add(-1 * time.Hour).Format(time.RFC3339)

	err = invMgr.VerifyInvite(invite)
	if err == nil {
		t.Error("expired invite should fail verification")
	}
}

func TestBatch1_VerifyInvite_InvalidSignature(t *testing.T) {
	env := setupTestEnv(t)
	origInvMgr := invMgr
	invMgr = &inviteManager{issued: make(map[string]*FederationInvite), used: make(map[string]bool), dataDir: env.dir}
	t.Cleanup(func() { invMgr = origInvMgr })

	setupNodeForInvite(t, env.dir)

	invite, err := invMgr.CreateInvite("*", "testuser", FederationInvitePublic, 24, "localhost:8080")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	invite.Signature = base64.StdEncoding.EncodeToString([]byte("invalid-signature"))

	err = invMgr.VerifyInvite(invite)
	if err == nil {
		t.Error("invalid signature should fail verification")
	}
}

func TestBatch1_MarkUsed_DirectedInvite(t *testing.T) {
	env := setupTestEnv(t)
	origInvMgr := invMgr
	invMgr = &inviteManager{issued: make(map[string]*FederationInvite), used: make(map[string]bool), dataDir: env.dir}
	t.Cleanup(func() { invMgr = origInvMgr })

	setupNodeForInvite(t, env.dir)

	invite, err := invMgr.CreateInvite("pubkey123", "testuser", FederationInviteDirected, 24, "localhost:8080")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	invMgr.MarkUsed(invite)

	inviteID := invMgr.inviteIDFromCode(invite)
	if !invMgr.used[inviteID] {
		t.Error("invite should be marked as used")
	}
}

func TestBatch1_GetInvites_ReturnsCreated(t *testing.T) {
	env := setupTestEnv(t)
	origInvMgr := invMgr
	invMgr = &inviteManager{issued: make(map[string]*FederationInvite), used: make(map[string]bool), dataDir: env.dir}
	t.Cleanup(func() { invMgr = origInvMgr })

	setupNodeForInvite(t, env.dir)

	_, err := invMgr.CreateInvite("*", "testuser", FederationInvitePublic, 24, "localhost:8080")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	invites := invMgr.GetInvites()
	if len(invites) != 1 {
		t.Errorf("expected 1 invite, got %d", len(invites))
	}
}

func TestBatch1_GetInvites_Empty(t *testing.T) {
	env := setupTestEnv(t)
	origInvMgr := invMgr
	invMgr = &inviteManager{issued: make(map[string]*FederationInvite), used: make(map[string]bool), dataDir: env.dir}
	t.Cleanup(func() { invMgr = origInvMgr })

	invites := invMgr.GetInvites()
	if len(invites) != 0 {
		t.Errorf("expected 0 invites, got %d", len(invites))
	}
}

// ============================================================
// multiuser.go — CreateInviteCode (need auth/multiUser)
// ============================================================

func TestBatch1_CreateInviteCode_DefaultRole(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(5, "")
	if code == "" {
		t.Error("expected non-empty invite code")
	}
	inv, ok := multiUser.invites[code]
	if !ok {
		t.Fatal("invite code not found in store")
	}
	if inv.Role != "consumer" {
		t.Errorf("role = %q, want %q", inv.Role, "consumer")
	}
	if inv.MaxUses != 5 {
		t.Errorf("maxUses = %d, want 5", inv.MaxUses)
	}
}

func TestBatch1_CreateInviteCode_CollaboratorRole(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(10, "collaborator")
	if code == "" {
		t.Error("expected non-empty invite code")
	}
	inv, ok := multiUser.invites[code]
	if !ok {
		t.Fatal("invite code not found in store")
	}
	if inv.Role != "collaborator" {
		t.Errorf("role = %q, want %q", inv.Role, "collaborator")
	}
}

func TestBatch1_CreateInviteCode_SingleUse(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "consumer")
	inv, ok := multiUser.invites[code]
	if !ok {
		t.Fatal("invite code not found in store")
	}
	if inv.MaxUses != 0 {
		t.Errorf("maxUses = %d, want 0 (single use)", inv.MaxUses)
	}
}
