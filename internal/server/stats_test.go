package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azrinaldi/gemini-stealth-bridge/internal/config"
)

func TestAccountStatsPersistenceAndReset(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{
		HomeDir: tempDir,
	}

	s := New(cfg)
	acct := &AccountState{
		ID:    "test-acct",
		Email: "test@example.com",
	}
	s.SetAccounts([]*AccountState{acct})

	// 1. Record requests
	for i := 0; i < 5; i++ {
		if err := s.checkAccountRateLimit(acct); err != nil {
			t.Fatalf("checkAccountRateLimit failed: %v", err)
		}
	}

	if acct.totalRequests != 5 || acct.todayRequests != 5 {
		t.Fatalf("expected 5 requests, got total=%d, today=%d", acct.totalRequests, acct.todayRequests)
	}

	s.FlushAccountStatsNow()

	// Verify file on disk
	statsPath := filepath.Join(tempDir, "account-stats.json")
	raw, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatalf("failed to read account-stats.json: %v", err)
	}

	var stats map[string]PersistedAccountStats
	if err := json.Unmarshal(raw, &stats); err != nil {
		t.Fatalf("failed to parse account-stats.json: %v", err)
	}

	st, ok := stats["test-acct"]
	if !ok {
		t.Fatalf("test-acct not found in stats: %v", stats)
	}
	if st.TotalRequests != 5 || st.TodayRequests != 5 {
		t.Fatalf("persisted stats mismatch: %+v", st)
	}

	// 2. Reload into a new server instance
	s2 := New(cfg)
	acct2 := &AccountState{
		ID:    "test-acct",
		Email: "test@example.com",
	}
	s2.SetAccounts([]*AccountState{acct2})
	s2.mu.Lock()
	s2.applyPersistedAccountStatsLocked()
	s2.mu.Unlock()

	if acct2.totalRequests != 5 || acct2.todayRequests != 5 {
		t.Fatalf("reloaded stats mismatch: total=%d today=%d", acct2.totalRequests, acct2.todayRequests)
	}

	// 3. Test day rollover: set date to past date
	stats["test-acct"] = PersistedAccountStats{
		Date:          "2020-01-01",
		TodayRequests: 10,
		TotalRequests: 25,
	}
	data, _ := json.Marshal(stats)
	_ = os.WriteFile(statsPath, data, 0o600)

	s3 := New(cfg)
	acct3 := &AccountState{
		ID:    "test-acct",
		Email: "test@example.com",
	}
	s3.SetAccounts([]*AccountState{acct3})
	s3.mu.Lock()
	s3.applyPersistedAccountStatsLocked()
	s3.mu.Unlock()

	if acct3.totalRequests != 25 {
		t.Fatalf("expected totalRequests 25 on date rollover, got %d", acct3.totalRequests)
	}
	if acct3.todayRequests != 0 {
		t.Fatalf("expected todayRequests 0 on date rollover, got %d", acct3.todayRequests)
	}

	// 4. Test session expired reset
	s3.markAccountFailure(acct3, "upstream_auth_error: session is unauthenticated or the saved cookies have expired (code 1016)")
	s3.FlushAccountStatsNow()

	if acct3.totalRequests != 0 || acct3.todayRequests != 0 {
		t.Fatalf("expected 0 after session expired, got total=%d today=%d", acct3.totalRequests, acct3.todayRequests)
	}

	// Verify disk file is also reset
	rawReset, _ := os.ReadFile(statsPath)
	var resetStats map[string]PersistedAccountStats
	_ = json.Unmarshal(rawReset, &resetStats)
	if resetStats["test-acct"].TotalRequests != 0 || resetStats["test-acct"].TodayRequests != 0 {
		t.Fatalf("expected disk stats to be reset to 0, got %+v", resetStats["test-acct"])
	}
}
