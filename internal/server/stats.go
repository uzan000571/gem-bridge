package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PersistedAccountStats records request hit counters for an account.
type PersistedAccountStats struct {
	Date          string `json:"date"`
	TodayRequests int    `json:"todayRequests"`
	TotalRequests int    `json:"totalRequests"`
}

func (s *Server) accountStatsDiskPath() string {
	if s.Cfg.HomeDir == "" {
		return ""
	}
	st, err := os.Stat(s.Cfg.HomeDir)
	if err != nil || !st.IsDir() {
		return ""
	}
	return filepath.Join(s.Cfg.HomeDir, "account-stats.json")
}

func (s *Server) loadAccountStatsFromDisk() map[string]PersistedAccountStats {
	p := s.accountStatsDiskPath()
	if p == "" {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var out map[string]PersistedAccountStats
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func (s *Server) applyPersistedAccountStatsLocked() {
	stats := s.loadAccountStatsFromDisk()
	if stats == nil {
		return
	}
	now := s.now()
	todayStr := now.Format("2006-01-02")
	changed := false

	for _, a := range s.accounts {
		if a == nil || a.ID == "" {
			continue
		}
		entry, ok := stats[a.ID]
		if !ok {
			continue
		}
		a.mu.Lock()
		a.totalRequests = entry.TotalRequests
		if entry.Date == todayStr {
			a.todayRequests = entry.TodayRequests
			a.statsDate = entry.Date
		} else {
			a.todayRequests = 0
			a.statsDate = todayStr
			changed = true
		}
		a.mu.Unlock()

		st, _ := AccountStatus(a, now)
		if st == StatusExpired {
			a.mu.Lock()
			if a.totalRequests > 0 || a.todayRequests > 0 {
				a.totalRequests = 0
				a.todayRequests = 0
				changed = true
			}
			a.mu.Unlock()
		}
	}
	if changed {
		s.saveAccountStatsToDisk()
	}
}

func (s *Server) saveAccountStatsToDisk() {
	p := s.accountStatsDiskPath()
	if p == "" {
		return
	}
	if !s.statsFlushPending.CompareAndSwap(false, true) {
		return
	}
	s.statsFlushWG.Add(1)
	go func() {
		defer s.statsFlushWG.Done()
		s.flushAccountStatsLocked()
	}()
}

// FlushAccountStatsNow writes account hit statistics to disk synchronously.
func (s *Server) FlushAccountStatsNow() {
	s.statsFlushWG.Wait()
	s.flushAccountStatsLocked()
}

func (s *Server) flushAccountStatsLocked() {
	p := s.accountStatsDiskPath()
	if p == "" {
		return
	}
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsFlushPending.Store(false)

	now := s.now()
	todayStr := now.Format("2006-01-02")

	accts := s.Accounts()
	out := make(map[string]PersistedAccountStats, len(accts))

	for _, a := range accts {
		if a == nil || a.ID == "" {
			continue
		}
		a.mu.RLock()
		today := a.todayRequests
		if a.statsDate != todayStr {
			today = 0
		}
		total := a.totalRequests
		a.mu.RUnlock()

		st, _ := AccountStatus(a, now)
		if st == StatusExpired {
			today = 0
			total = 0
		}

		out[a.ID] = PersistedAccountStats{
			Date:          todayStr,
			TodayRequests: today,
			TotalRequests: total,
		}
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	tmp := fmt.Sprintf("%s.tmp-%d", p, time.Now().UnixNano())
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		if err := os.Rename(tmp, p); err != nil {
			_ = os.Remove(tmp)
		}
	}
}

func (s *Server) resetAccountStatsIfExpired(a *AccountState) {
	if a == nil {
		return
	}
	st, _ := AccountStatus(a, s.now())
	if st != StatusExpired {
		return
	}
	a.mu.Lock()
	changed := a.totalRequests != 0 || a.todayRequests != 0
	a.totalRequests = 0
	a.todayRequests = 0
	a.mu.Unlock()
	if changed {
		s.saveAccountStatsToDisk()
	}
}
