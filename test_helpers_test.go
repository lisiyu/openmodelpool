package main

import (
	"path/filepath"
	"testing"
)

// testEnv holds per-test isolated state for all global subsystems.
type testEnv struct {
	dir     string // temp data directory
	encInst *encryptor
	pmInst  *ProviderManager
	tkInst  *Tracker
	muInst  *MultiUserManager
	cfgInst *Config
	authInst *Auth
	siderInst *SiderMonitor
}

// setupTestEnv initializes all global singletons with isolated temp storage.
// Returns a testEnv and registers a cleanup that stops goroutines and restores globals.
func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()

	// Save originals
	origEnc := enc
	origCfg := cfg
	origPm := pm
	origTracker := tracker
	origMulti := multiUser
	origAuth := auth
	origSider := siderMon

	// Initialize encryptor
	initEncryptor(filepath.Join(dir, ".key"))
	encInst := enc

	// Initialize config
	initConfig(filepath.Join(dir, "config.json"))
	cfgInst := cfg

	// Initialize provider manager
	initProviderManager(filepath.Join(dir, "providers.json"))
	pmInst := pm

	// Initialize auth
	initAuth(filepath.Join(dir, "admin.json"))
	authInst := auth

	// Initialize sider monitor (with empty file so no crash)
	initSiderMonitor(filepath.Join(dir, "sider.json"))
	siderInst := siderMon

	// Initialize tracker
	initTracker(filepath.Join(dir, "usage.json"))
	tkInst := tracker

	// Initialize multi-user
	initMultiUser(dir)
	muInst := multiUser

	// Cleanup on test end
	t.Cleanup(func() {
		// Stop tracker goroutines
		if tkInst != nil {
			select {
			case <-tkInst.stopCh:
			default:
				close(tkInst.stopCh)
			}
		}
		// Restore globals
		enc = origEnc
		cfg = origCfg
		pm = origPm
		tracker = origTracker
		multiUser = origMulti
		auth = origAuth
		siderMon = origSider
	})

	return &testEnv{
		dir:       dir,
		encInst:   encInst,
		pmInst:    pmInst,
		tkInst:    tkInst,
		muInst:    muInst,
		cfgInst:   cfgInst,
		authInst:  authInst,
		siderInst: siderInst,
	}
}

// makeProvider creates a test provider with the given id, models, priority, and enabled state.
func makeProvider(id, name string, models []ModelDef, priority int, enabled bool) Provider {
	return Provider{
		ID:       id,
		Name:     name,
		Type:     "openai_compatible",
		BaseURL:  "https://example.com/v1",
		APIKey:   "test-key-" + id,
		Enabled:  enabled,
		Models:   models,
		Priority: priority,
	}
}

// makeModelDef is a shorthand for creating ModelDef slices.
func makeModelDef(ids ...string) []ModelDef {
	models := make([]ModelDef, len(ids))
	for i, id := range ids {
		models[i] = ModelDef{ID: id, Name: id, Enabled: true}
	}
	return models
}
