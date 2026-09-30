package main

// G4: bbolt 持久化基础层。
//
// data/*.json（账本 / 治理 / 配额六域）迁移到单一嵌入式 KV 文件：
//   data/openmodelpool.bbolt （0600，mmap + 单写者事务）
//
// 设计要点：
//   - 按 dataDir 共享句柄（生产环境只有一个 "data"；测试用各自 TempDir）。
//   - storage_backend 开关：cfg.Get("storage_backend", "bbolt")，
//     "bbolt" | "json"；cfg 未初始化（单测）时默认走 JSON，保持旧测试行为。
//   - 迁移幂等：meta bucket 里按域打 migrated 标记；成功后原 JSON rename 为 .bak
//     （保留做手动回滚，不自动删除）。
//   - 直接切换（用户决策 3）：无双写影子期；切回 "json" 即走旧代码路径。

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	bboltFileName = "openmodelpool.bbolt"

	bboltBucketMeta       = "meta"
	bboltBucketLedger     = "ledger"
	bboltBucketGovernance = "governance"
	bboltBucketAlgoGov    = "algo_gov"
	bboltBucketQuota      = "quota"

	// storageBackendKey 是 config.json / 环境变量里的开关名。
	storageBackendKey   = "storage_backend"
	storageBackendBbolt = "bbolt"
	storageBackendJSON  = "json"
)

// bboltHandle 是按 dataDir 共享的 DB 句柄。
type bboltHandle struct {
	db   *bolt.DB
	path string
}

var (
	bboltMu      sync.Mutex
	bboltHandles = make(map[string]*bboltHandle)
)

// storageUseBbolt 报告 bbolt 后端是否启用。cfg 为 nil（单测直调 init* 函数时）
// 返回 false，保持旧测试走 JSON 路径；生产环境 initConfig 先行，默认 bbolt。
func storageUseBbolt() bool {
	if cfg == nil {
		return false
	}
	return cfg.Get(storageBackendKey, storageBackendBbolt) == storageBackendBbolt
}

// openBbolt 打开（或复用）dataDir 下的共享 DB 句柄，建 bucket，强制 0600。
// 幂等；调用方负责在不需要时 closeBbolt。
func openBbolt(dataDir string) (*bboltHandle, error) {
	bboltMu.Lock()
	defer bboltMu.Unlock()

	if h, ok := bboltHandles[dataDir]; ok {
		return h, nil
	}
	path := filepath.Join(dataDir, bboltFileName)
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("bbolt open %s: %w", path, err)
	}
	// 已存在文件可能是旧版本/外部以宽松权限创建，强制收紧
	//（DB 内含账本 ed25519 私钥）。
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("bbolt chmod %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{
			bboltBucketMeta, bboltBucketLedger, bboltBucketGovernance,
			bboltBucketAlgoGov, bboltBucketQuota,
		} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	h := &bboltHandle{db: db, path: path}
	bboltHandles[dataDir] = h
	return h, nil
}

// closeBbolt 关闭 dataDir 的共享句柄（测试清理 / 关机）。
func closeBbolt(dataDir string) {
	bboltMu.Lock()
	defer bboltMu.Unlock()
	if h, ok := bboltHandles[dataDir]; ok {
		if err := h.db.Close(); err != nil {
			slog.Warn("bbolt close failed", "path", h.path, "error", err)
		}
		delete(bboltHandles, dataDir)
	}
}

// closeAllBbolt 关闭所有已打开的句柄，关机时调用。
func closeAllBbolt() {
	bboltMu.Lock()
	paths := make([]string, 0, len(bboltHandles))
	for p := range bboltHandles {
		paths = append(paths, p)
	}
	bboltMu.Unlock()
	for _, p := range paths {
		closeBbolt(p)
	}
}

// isMigrated 报告某域是否已完成 JSON→bbolt 迁移。
func (h *bboltHandle) isMigrated(domain string) bool {
	var done bool
	_ = h.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bboltBucketMeta)).Get([]byte("migrated/" + domain))
		done = string(v) == "1"
		return nil
	})
	return done
}

// markMigrated 打某域的迁移完成标记。
func (h *bboltHandle) markMigrated(domain string) error {
	return h.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bboltBucketMeta)).Put([]byte("migrated/"+domain), []byte("1"))
	})
}

// bakJSON 将迁移成功的原 JSON 文件 rename 为 .bak（保留做手动回滚）。
// 幂等：.bak 已存在或 JSON 不存在时直接返回 nil。
func bakJSON(jsonPath string) error {
	bakPath := jsonPath + ".bak"
	if _, err := os.Stat(bakPath); err == nil {
		return nil // 已备份过
	}
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		return nil // 全新节点，无需备份
	}
	if err := os.Rename(jsonPath, bakPath); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", jsonPath, bakPath, err)
	}
	// rename 保留原权限；显式收紧，防遗留宽松文件。
	if err := os.Chmod(bakPath, 0600); err != nil {
		slog.Warn("chmod bak failed", "path", bakPath, "error", err)
	}
	slog.Info("json migrated to bbolt, original backed up", "bak", bakPath)
	return nil
}

// bboltGet 是 tx 内单 key 读取的小 helper。
func bboltGet(b *bolt.Bucket, key string) []byte {
	if b == nil {
		return nil
	}
	return b.Get([]byte(key))
}
