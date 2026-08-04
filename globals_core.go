package main

// ============================================================================
// Core Infrastructure — global singleton pointers for core subsystems.
// These are initialized in initCore() (see init.go) and remain nil until then.
// ============================================================================

var cfg *Config

// enc is the package-level encryptor used by config/auth/multiuser code.
var enc *Encryptor

var auth *Auth

var pm *ProviderManager

var tracker *Tracker

var siderMon *SiderMonitor

var appLogger *Logger

var multiUser *MultiUserManager

var auditLog *AuditLogger

var eventBus *EventBus

var metrics *Metrics

var rateLimiter *GlobalRateLimiter

var healthChecker *HealthChecker

// wafEngine is the package-global singleton, initialized by initWAF.
var wafEngine *WAFEngine

var freePool *FreePoolManager

var vmessManager *VMessProxy
