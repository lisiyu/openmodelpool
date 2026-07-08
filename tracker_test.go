package main

import (
	"testing"
	"time"
)

func TestTracker_Record(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "Provider 1", "gpt-4o", 100, 200, 150.0, true, "")

	if len(tracker.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(tracker.records))
	}

	r := tracker.records[0]
	if r.ProviderID != "p1" {
		t.Fatalf("provider_id mismatch: %s", r.ProviderID)
	}
	if r.Model != "gpt-4o" {
		t.Fatalf("model mismatch: %s", r.Model)
	}
	if r.PromptTokens != 100 || r.CompletionTokens != 200 {
		t.Fatalf("token mismatch: prompt=%d comp=%d", r.PromptTokens, r.CompletionTokens)
	}
	if r.TotalTokens != 300 {
		t.Fatalf("total tokens should be 300, got %d", r.TotalTokens)
	}
	if !r.Success {
		t.Fatal("record should be marked success")
	}
}

func TestTracker_RecordWithRetry(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.RecordWithRetry("p1", "P1", "gpt-4o", 50, 100, 200.0, true, "", true, 2)

	tracker.addRequestLog(RequestLogEntry{
		Timestamp:  "now",
		ProviderID: "test",
	})

	// Verify request log
	logs := tracker.GetRequestLog(10)
	found := false
	for _, l := range logs {
		if l.ProviderID == "p1" && l.RetryCount == 2 && l.Stream {
			found = true
		}
	}
	if !found {
		t.Fatal("request log should contain retry info")
	}
}

func TestTracker_MultipleRecords(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	for i := 0; i < 10; i++ {
		tracker.Record("p1", "P1", "gpt-4o", 10, 20, 100.0, true, "")
	}
	tracker.Record("p2", "P2", "gpt-4o", 10, 20, 200.0, false, "timeout")

	if len(tracker.records) != 11 {
		t.Fatalf("expected 11 records, got %d", len(tracker.records))
	}
}

func TestTracker_EWMA(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// First record sets initial EWMA
	tracker.Record("p1", "P1", "gpt-4o", 10, 20, 100.0, true, "")
	ewma := tracker.GetEWMA("p1")
	if ewma != 100.0 {
		t.Fatalf("first EWMA should be 100.0, got %v", ewma)
	}

	// Second record should blend
	tracker.Record("p1", "P1", "gpt-4o", 10, 20, 200.0, true, "")
	ewma2 := tracker.GetEWMA("p1")
	// EWMA = 0.3 * 200 + 0.7 * 100 = 60 + 70 = 130
	expected := round1(ewmaAlpha*200.0 + (1-ewmaAlpha)*100.0)
	if ewma2 != expected {
		t.Fatalf("EWMA should be %v, got %v", expected, ewma2)
	}
}

func TestTracker_EWMA_FailedRequestsIgnored(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "P1", "gpt-4o", 10, 20, 100.0, true, "")
	ewma1 := tracker.GetEWMA("p1")

	// Failed request should NOT update EWMA
	tracker.Record("p1", "P1", "gpt-4o", 10, 20, 9999.0, false, "error")
	ewma2 := tracker.GetEWMA("p1")

	if ewma1 != ewma2 {
		t.Fatalf("failed request should not change EWMA: was %v, now %v", ewma1, ewma2)
	}
}

func TestTracker_EWMA_UnknownProvider(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	ewma := tracker.GetEWMA("nonexistent")
	if ewma != 0 {
		t.Fatalf("unknown provider EWMA should be 0, got %v", ewma)
	}
}

func TestTracker_TotalTokensByProvider(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "P1", "gpt-4o", 100, 200, 50.0, true, "")
	tracker.Record("p1", "P1", "gpt-4o", 50, 100, 50.0, true, "")
	tracker.Record("p2", "P2", "gpt-3.5", 30, 60, 50.0, true, "")

	totals := tracker.TotalTokensByProvider()
	if totals["p1"] != 450 { // (100+200) + (50+100)
		t.Fatalf("p1 total tokens should be 450, got %d", totals["p1"])
	}
	if totals["p2"] != 90 { // 30+60
		t.Fatalf("p2 total tokens should be 90, got %d", totals["p2"])
	}
}

func TestTracker_ProviderStats(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "P1", "gpt-4o", 100, 200, 150.0, true, "")
	tracker.Record("p1", "P1", "gpt-4o", 100, 200, 250.0, true, "")
	tracker.Record("p1", "P1", "gpt-4o", 100, 200, 50.0, false, "err")

	stats := tracker.ProviderStats(7)
	s, ok := stats["p1"]
	if !ok {
		t.Fatal("p1 should have stats")
	}
	if s["request_count"].(int) != 3 {
		t.Fatalf("expected 3 requests, got %v", s["request_count"])
	}
	if s["success_count"].(int) != 2 {
		t.Fatalf("expected 2 successes, got %v", s["success_count"])
	}
}

func TestTracker_ProviderStats_DayFilter(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Add a record manually with old timestamp
	tracker.mu.Lock()
	tracker.records = append(tracker.records, UsageRecord{
		Timestamp:   time.Now().AddDate(0, 0, -10).Format(time.RFC3339),
		ProviderID:  "old-p",
		TotalTokens: 999,
		Success:     true,
		LatencyMS:   100,
	})
	tracker.mu.Unlock()

	// Query last 7 days - should NOT include the 10-day-old record
	stats := tracker.ProviderStats(7)
	if _, ok := stats["old-p"]; ok {
		t.Fatal("10-day-old record should not appear in 7-day stats")
	}

	// Query last 30 days - SHOULD include it
	stats30 := tracker.ProviderStats(30)
	if _, ok := stats30["old-p"]; !ok {
		t.Fatal("10-day-old record should appear in 30-day stats")
	}
}

func TestTracker_RequestLog(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	for i := 0; i < 5; i++ {
		tracker.Record("p1", "P1", "gpt-4o", 10, 20, 100.0, true, "")
	}

	logs := tracker.GetRequestLog(3)
	if len(logs) != 3 {
		t.Fatalf("expected 3 log entries, got %d", len(logs))
	}

	// Get more than available
	all := tracker.GetRequestLog(100)
	if len(all) != 5 {
		t.Fatalf("expected 5 log entries, got %d", len(all))
	}
}

func TestTracker_Reset(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "P1", "gpt-4o", 10, 20, 100.0, true, "")
	tracker.Reset()

	if len(tracker.records) != 0 {
		t.Fatalf("expected 0 records after reset, got %d", len(tracker.records))
	}
	if tracker.GetEWMA("p1") != 0 {
		t.Fatal("EWMA should be 0 after reset")
	}
}

func TestTracker_Flush(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "P1", "gpt-4o", 10, 20, 100.0, true, "")
	tracker.Flush()

	// After flush, dirtyCount should be 0
	tracker.mu.Lock()
	dc := tracker.dirtyCount
	tracker.mu.Unlock()
	if dc != 0 {
		t.Fatalf("dirtyCount should be 0 after flush, got %d", dc)
	}
}

func TestRound1(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{1.23, 1.2},
		{1.25, 1.3}, // round half up due to +0.5 trick
		{1.29, 1.3},
		{0.0, 0.0},
		{100.0, 100.0},
	}
	for _, tt := range tests {
		got := round1(tt.input)
		if got != tt.want {
			t.Errorf("round1(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestRound4(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{1.23456, 1.2346},
		{0.00001, 0.0},
		{1.0, 1.0},
	}
	for _, tt := range tests {
		got := round4(tt.input)
		if got != tt.want {
			t.Errorf("round4(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
