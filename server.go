package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// runServer sets up HTTP routes, starts the server, and handles graceful shutdown.
func runServer() {
	mux := setupRoutes()
	// SEC-P0-1: relay-to-self requests are dispatched in-process to this mux
	// (preserving the original RemoteAddr) instead of looping back over TCP.
	relayDispatchHandler = mux

	port := cfg.Get("service_port", "8000")
	addr := ":" + port

	// Initialize Cloudflare Tunnel if enabled
	portNum := 8000
	if p, err := strconv.Atoi(port); err == nil {
		portNum = p
	}
	initTunnel(portNum)

	// SEC-P0-2: the outermost middleware strips client-supplied internal
	// headers (X-OMP-KeyType) before any handler can trust them.
	// B6-3: recoverMiddleware wraps everything so a panicking handler returns
	// a JSON 500 instead of tearing down the connection (or process).
	handler := recoverMiddleware(stripInternalHeadersMiddleware(requestIDMiddleware(corsMiddleware(requestLogMiddleware(concurrencyMiddleware(adminTimeoutMiddleware(mux)))))))

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second, // SEC-P2-15: bound slow-header attacks
		WriteTimeout: 300 * time.Second,     // long for streaming
		IdleTimeout:  120 * time.Second,
	}

	// Start HTTPS server if public_url is https://
	setupHTTPS(server, handler)

	// Start Seed discovery service on port 8001
	startSeedServer()

	slog.Info("OpenModelPool Agent started", "port", port, "providers", len(pm.Enabled()))

	// Graceful shutdown
	go gracefulShutdown(server)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// setupRoutes registers all HTTP routes on a new ServeMux.
func setupHTTPS(server *http.Server, handler http.Handler) {
	publicURL := cfg.Get("public_url", "")
	if !strings.HasPrefix(publicURL, "https://") {
		return
	}

	u, err := url.Parse(publicURL)
	if err != nil {
		return
	}
	domain := u.Hostname()
	certDir := "data/certs"
	os.MkdirAll(certDir, 0700)

	certManager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(certDir),
		HostPolicy: autocert.HostWhitelist(domain),
	}

	// Wrap HTTP handler with ACME HTTP-01 challenge support
	server.Handler = certManager.HTTPHandler(handler)

	// HTTPS server on port 8443 (iptables forwards 443→8443)
	httpsServer := &http.Server{
		Addr:    ":8443",
		Handler: handler,
		TLSConfig: &tls.Config{
			GetCertificate: certManager.GetCertificate,
			MinVersion:     tls.VersionTLS12, // B14: reject TLS 1.0/1.1
		},
		ReadTimeout:  30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second, // SEC-P2-15
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("HTTPS server starting", "addr", ":8443", "domain", domain)
		if err := httpsServer.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			slog.Error("HTTPS server error", "error", err)
		}
	}()

	slog.Info("HTTPS enabled with Let's Encrypt auto-cert", "domain", domain)
}

// gracefulShutdown handles OS signals for hot reload and clean shutdown.
func gracefulShutdown(server *http.Server) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	for {
		sig := <-sigCh
		switch sig {
		case syscall.SIGHUP:
			// Hot reload configuration
			slog.Info("SIGHUP received, reloading configuration...")
			cfg.load()
			// Reinitialize rate limiter with new config
			initRateLimiter()
			// B7-5: WAF reads cfg once at init — without this a SIGHUP left
			// the old WAF rules active until restart, making the reload
			// half-effective.
			reloadWAF()
			// Reload federation: reconcile with the network_enabled single source
			// of truth instead of the legacy federation_enabled key (REQ-2).
			if netMgr != nil {
				netMgr.syncFederationToNetwork()
			}
			if fed != nil {
				fed.mu.Lock()
				fed.relayEnabled = cfg.Get("federation_relay_enabled", "false") == "true"
				fed.mu.Unlock()
			}
			// Broadcast config update via SSE
			BroadcastConfigUpdate("all")
			slog.Info("configuration reloaded successfully")

		case syscall.SIGINT, syscall.SIGTERM:
			slog.Info("shutting down...")
			closeGlobalStopCh() // signal all background goroutines
			stopDHTNode()       // P1-1(ii): close the DHT UDP socket promptly
			cfg.stop()
			cfg.saveSync()
			tracker.Stop()
			// Flush pending consumer usage (token counts, request counts).
			// RecordConsumerUsage only marks the manager dirty below its
			// batch threshold, so without this the last few seconds of usage
			// were silently dropped on every restart.
			if multiUser != nil {
				multiUser.StopBatchSave()
			}
			stopConnTracker() // B11: stop conn tracker goroutine
			StopBalanceLoop() // B6-8: stop balance engine cycle (was never wired into shutdown)
			if freePool != nil {
				freePool.stop() // B11: stop free pool sync goroutine
			}
			healthChecker.stop()
			CloseAccessLog()
			CloseAuditLog()
			if tunnel != nil {
				tunnel.stop()
			}
			if fed != nil {
				fed.stop()
			}
			if gossip != nil {
				gossip.stop()
			}
			saveContributionLedger()
			flushContributionLedger() // PERF-P1-4: final synchronous ledger flush before exit
			// PERF-P1-5: stop the ledger reconcile loop + replication workers.
			if ledgerReconcileStop != nil {
				select {
				case <-ledgerReconcileStop:
				default:
					close(ledgerReconcileStop)
				}
			}
			if ledgerReplicator != nil {
				ledgerReplicator.Stop()
			}
			if netMgr != nil {
				netMgr.stopRefreshLoop()			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			server.Shutdown(ctx)
			return
		}
	}
}
