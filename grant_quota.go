package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// GrantQuotaEntry — 受赠认证额度登记（阶段3"免费池增强：教育/公益组织认证额度"）。
//
// 公益口径与管理一致性：这不是货币，不是抽成，不进入任何兑换/优先级/治理
// 计算。它只是运营者给经认证的教育/公益主体**手动授予**的"每日免费额度"账
// 户——透明、可审计、可撤销，仍是免费的社区资源，只是额度更大且不以贡献为
// 前提（面向认证前的学习/公益演示）。1:1 记账、不增发、无手续费。
//
// 撤销保留记录（追加式 + revoked 标记），管理操作本身由审计日志记录。
type GrantQuotaEntry struct {
	ID          string     `json:"id"`
	Holder      string     `json:"holder"`       // 教育/公益组织名称（认证备注）
	KeyID       string     `json:"key_id"`       // 该主体连入时使用的 key 标识
	DailyTokens int64      `json:"daily_tokens"` // 每日受赠免费额度
	CreatedAt   time.Time  `json:"created_at"`
	Revoked     bool       `json:"revoked"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// GrantQuotaManager tracks certified education / public-welfare quota grants.
type GrantQuotaManager struct {
	mu           sync.RWMutex
	entries      []GrantQuotaEntry
	grantUsed    map[string]int64 // key_id -> tokens used today
	grantUsedDay string           // "2006-01-02" of grantUsed
	path         string
	seq          int64
}

var grantQuota *GrantQuotaManager

var errActiveGrantExists = errors.New("an active grant for this key already exists")

func initGrantQuota(dataDir string) {
	grantQuota = NewGrantQuotaManager(filepath.Join(dataDir, "grants.json"))
}

func NewGrantQuotaManager(path string) *GrantQuotaManager {
	g := &GrantQuotaManager{
		grantUsed:    make(map[string]int64),
		grantUsedDay: time.Now().Format("2006-01-02"),
		path:         path,
	}
	g.load()
	return g
}

func (g *GrantQuotaManager) load() {
	if g.path == "" {
		return
	}
	b, err := os.ReadFile(g.path)
	if err != nil {
		return
	}
	var entries []GrantQuotaEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return
	}
	g.entries = entries
	for _, e := range entries {
		if i := e.ID; len(i) > 1 && i[0] == 'g' {
			if n := e.ID[1:]; len(n) > 0 {
				if v, err := parseInt(n); err == nil && v > g.seq {
					g.seq = v
				}
			}
		}
	}
}

func (g *GrantQuotaManager) save() {
	if g.path == "" {
		return
	}
	tmp := g.path + ".tmp"
	b, err := json.MarshalIndent(g.entries, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.path), 0700); err != nil {
		return
	}
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, g.path)
}

// Grant registers a certified education/public-welfare quota account.
func (g *GrantQuotaManager) Grant(holder, keyID string, dailyTokens int64) (GrantQuotaEntry, error) {
	if holder == "" || keyID == "" {
		return GrantQuotaEntry{}, errors.New("holder and key_id are required")
	}
	if dailyTokens <= 0 {
		return GrantQuotaEntry{}, errors.New("daily_tokens must be positive")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.entries {
		if !e.Revoked && e.KeyID == keyID {
			return GrantQuotaEntry{}, errActiveGrantExists
		}
	}
	g.seq++
	id := "g" + itoa64(g.seq)
	now := time.Now()
	entry := GrantQuotaEntry{
		ID:          id,
		Holder:      holder,
		KeyID:       keyID,
		DailyTokens: dailyTokens,
		CreatedAt:   now,
	}
	g.entries = append(g.entries, entry)
	g.save()
	return entry, nil
}

// Revoke marks a grant revoked while keeping the record for audit.
func (g *GrantQuotaManager) Revoke(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range g.entries {
		if g.entries[i].ID == id {
			if g.entries[i].Revoked {
				return errors.New("grant already revoked")
			}
			now := time.Now()
			g.entries[i].Revoked = true
			g.entries[i].RevokedAt = &now
			g.save()
			return nil
		}
	}
	return errors.New("grant not found")
}

// ActiveGrantByKey returns the active grant for a key, if any.
func (g *GrantQuotaManager) ActiveGrantByKey(keyID string) (GrantQuotaEntry, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, e := range g.entries {
		if !e.Revoked && e.KeyID == keyID {
			return e, true
		}
	}
	return GrantQuotaEntry{}, false
}

// GetGrants returns a copy of the full audit-visible list.
func (g *GrantQuotaManager) GetGrants() []GrantQuotaEntry {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]GrantQuotaEntry, len(g.entries))
	copy(out, g.entries)
	return out
}

// TryGrantDraw consumes tokens from a key's certified daily budget. Returns
// true only if the key holds an active grant with enough remaining budget.
// Daily budget resets automatically at midnight.
func (g *GrantQuotaManager) TryGrantDraw(keyID string, tokens int64) bool {
	if keyID == "" || tokens <= 0 {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.active(keyID)
	if !ok {
		return false
	}
	g.rollDailyLocked()
	if g.grantUsed[keyID]+tokens <= entry.DailyTokens {
		g.grantUsed[keyID] += tokens
		return true
	}
	return false
}

func (g *GrantQuotaManager) active(keyID string) (GrantQuotaEntry, bool) {
	for _, e := range g.entries {
		if !e.Revoked && e.KeyID == keyID {
			return e, true
		}
	}
	return GrantQuotaEntry{}, false
}

func (g *GrantQuotaManager) rollDailyLocked() {
	today := time.Now().Format("2006-01-02")
	if g.grantUsedDay != today {
		g.grantUsed = make(map[string]int64)
		g.grantUsedDay = today
	}
}

func parseInt(s string) (int64, error) {
	var v int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errNotNumber
		}
		v = v*10 + int64(c-'0')
	}
	return v, nil
}

var errNotNumber = errors.New("not a number")

func itoa64(n int64) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
