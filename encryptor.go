package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// encPrefix marks ciphertext produced by this Encryptor so callers can tell
// encrypted values apart from plaintext and avoid double-encryption.
const encPrefix = "omp:e:"

// Encryptor provides AES-256-GCM authenticated encryption at rest for
// sensitive fields (API keys, SMTP passwords, proxy API keys).
//
// This replaces the previously misnamed file (which actually contained the
// EventBus/SSE implementation, now moved to eventbus.go). The earlier code
// referenced an undefined `enc` Encryptor and the README claimed AES-256-GCM
// with no implementation — that gap is now closed by real crypto/aes usage.
type Encryptor struct {
	mu        sync.RWMutex
	key       []byte
	ready     bool
	ephemeral bool
}

// IsEphemeral reports whether the encryptor is using a temporary key
// that will be lost on restart (all previously encrypted data becomes unrecoverable).
func (e *Encryptor) IsEphemeral() bool {
	return e.ephemeral
}

// IsReady returns whether the encryptor has a valid key.
func (e *Encryptor) IsReady() bool {
	return e.ready
}

// encKeyFile 是主密钥文件的路径。包级变量（而非常量）以便测试指向 TempDir。
// 其 ".bak" 后缀版本是 G5 keyring 迁移后保留的回滚副本。
var encKeyFile = "data/.enc_key"

// encKeyBakFile 返回主密钥文件的 .bak 回滚副本路径。
func encKeyBakFile() string { return encKeyFile + ".bak" }

// NewEncryptor resolves the 32-byte AES key with the following precedence:
//  1. OPENMODELPOOL_ENC_KEY env var (raw 32 bytes or base64-encoded)
//  2. OS keyring (G5; only when secret_backend=keyring):
//     hit → use; miss → migrate from the key file (rename to .enc_key.bak)
//     or dual-write a freshly generated key; keyring unavailable → warn log
//     + silent fallback to the file chain (never fail-closed)
//  3. data/.enc_key on disk (auto-generated and persisted on first run)
//  4. a freshly generated in-memory key (no persistence; survives one process)
//
// G5 安全不变式：keyring 只换"钥匙放哪"，不换"锁"——上层 omp:e:/GCM 格式、
// node.key、provider token 等所有密文格式完全不变。
func NewEncryptor() (*Encryptor, error) {
	if k := os.Getenv("OPENMODELPOOL_ENC_KEY"); k != "" {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(raw) != 32 {
			raw = []byte(k)
			if len(raw) != 32 {
				return nil, errors.New("OPENMODELPOOL_ENC_KEY must decode to exactly 32 bytes (raw or base64)")
			}
		}
		return &Encryptor{key: raw}, nil
	}

	// G5: keyring 后端（仅当 secret_backend=keyring）。
	if secretBackend() == secretBackendKeyring {
		if key, ok := loadKeyringKey(); ok {
			removeStaleKeyFile()
			return &Encryptor{key: key}, nil
		}
		// keyring 未命中/不可用：走文件链；文件命中则迁移进 keyring。
		if b, err := os.ReadFile(encKeyFile); err == nil && len(b) == 32 {
			if storeKeyringKey(b) {
				if err := os.Rename(encKeyFile, encKeyBakFile()); err != nil {
					slog.Warn("keyring migration: could not rename key file to .bak; leaving it in place",
						"err", err, "path", encKeyFile)
				} else {
					slog.Info("G5: master key migrated to OS keyring",
						"service", keyringService, "account", keyringAccount,
						"rollback", "mv "+encKeyBakFile()+" "+encKeyFile+" 并把 secret_backend 设回 file")
				}
			}
			return &Encryptor{key: b}, nil
		}
		// 全新安装：生成新钥匙并双写——文件作为 keyring 不可用时的 fallback 种子。
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		storeKeyringKey(key) // 失败已在内部记 warn
		if err := os.MkdirAll("data", 0o700); err == nil {
			if werr := atomicWriteFile(encKeyFile, key, 0o600); werr != nil {
				slog.Warn("could not persist encryption key; using in-memory key", "err", werr)
			}
		}
		return &Encryptor{key: key}, nil
	}

	if b, err := os.ReadFile(encKeyFile); err == nil && len(b) == 32 {
		return &Encryptor{key: b}, nil
	}

	// G5: .bak 存在但 .enc_key 缺失 —— 说明曾经迁移到 keyring，钥匙只在
	// keyring（/.bak）里。此时生成新钥匙是灾难（旧数据全部无法解密），
	// 所以直接从 keyring 找回来；找不回就 fail-closed，操作员按下面的
	// 指引恢复 .bak 或修好 keyring 后再启动。
	// 回滚风险（文档）：若用户删了 .bak 又关闭 keyring 且 env 未设，
	// 这里会触发 init() 的 os.Exit(1)，服务起不来——这是故意的，
	// 静默用错钥匙比起不来更危险。
	if _, err := os.Stat(encKeyBakFile()); err == nil {
		if key, ok := loadKeyringKey(); ok {
			slog.Warn("secret_backend=file but master key was previously migrated to keyring; recovered from OS keyring",
				"hint", "mv "+encKeyBakFile()+" "+encKeyFile+" to silence this warning")
			return &Encryptor{key: key}, nil
		}
		return nil, errors.New("master key file " + encKeyFile + " is missing but " + encKeyBakFile() +
			" exists: restore it (mv " + encKeyBakFile() + " " + encKeyFile +
			") or make the OS keyring reachable, then restart")
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll("data", 0o700); err == nil {
		if werr := atomicWriteFile(encKeyFile, key, 0o600); werr != nil {
			slog.Warn("could not persist encryption key; using ephemeral key", "err", werr)
		}
	}
	return &Encryptor{key: key}, nil
}

// removeStaleKeyFile 删除迁移后 init() 阶段误生成的陈旧 .enc_key。
// 调用场景：无 env 时 init() 先以 file 后端生成了一个新钥匙文件，随后
// refreshEncryptorForSecretBackend() 从 keyring 拿到真钥匙——此时的 .enc_key
// 是诱饵（内容与真钥匙无关），而 .bak 的存在证明迁移已完成，直接删除。
// 有日志，无静默操作。
func removeStaleKeyFile() {
	if _, err := os.Stat(encKeyBakFile()); err != nil {
		return
	}
	if _, err := os.Stat(encKeyFile); err != nil {
		return
	}
	if err := os.Remove(encKeyFile); err != nil {
		slog.Warn("could not remove stale regenerated key file", "path", encKeyFile, "err", err)
		return
	}
	slog.Info("removed stale regenerated key file after keyring recovery", "path", encKeyFile)
}

// refreshEncryptorForSecretBackend 在 initConfig 之后重新解析主密钥。
// init() 执行时 cfg 尚未加载，只能看到环境变量；config 文件里的
// secret_backend=keyring 在这里生效（首次触发 keyring 迁移）。
// 幂等：keyring 命中路径除清理陈旧 .enc_key 外无副作用。
// 调用时机是单线程启动期（initCore），早于所有后台 goroutine。
func refreshEncryptorForSecretBackend() {
	if secretBackend() != secretBackendKeyring {
		return
	}
	e, err := NewEncryptor()
	if err != nil {
		slog.Error("keyring encryptor refresh failed; keeping startup key", "err", err)
		return
	}
	enc = e
	enc.ready = true
	slog.Info("encryptor refreshed from secret_backend=keyring")
}

// Encrypt encrypts plaintext and returns "omp:e:" + base64(nonce||ciphertext).
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Non-prefixed input is returned unchanged so that
// legacy/empty values never cause a hard failure.
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, encPrefix) {
		return ciphertext, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encPrefix))
	if err != nil {
		return ciphertext, err
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return ciphertext, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ciphertext, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return ciphertext, errors.New("ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return ciphertext, err
	}
	return string(pt), nil
}
func init() {
	var err error
	enc, err = NewEncryptor()
	if err != nil {
		// P0-fix: abort the process on ephemeral-key fallback. Continuing with an
		// in-memory key silently breaks encryption for all previously-stored
		// secrets (SMTP passwords, API keys) — a denial-of-service that also masks
		// the real incident (key file missing/corrupted). The operator must fix the
		// key source before any sensitive data can be stored or recovered.
		slog.Error("CRITICAL: encryptor init failed — aborting to prevent silent data loss", "err", err)
		os.Exit(1)
	}
	enc.ready = true
}

// IsEncrypted reports whether s looks like a value produced by this Encryptor.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, encPrefix)
}

// encryptField best-effort encrypts a field, returning the input unchanged on error.
// M3-fix: Refuses to encrypt new data when using an ephemeral key to prevent
// data loss on restart (previously encrypted data would become unrecoverable).
func encryptField(s string) string {
	if enc == nil || s == "" {
		return s
	}
	if enc.IsEphemeral() {
		slog.Warn("refusing to encrypt data with ephemeral key — data would be unrecoverable after restart", "hint", "resolve the encryption key file issue before storing sensitive data")
		return s
	}
	e, err := enc.Encrypt(s)
	if err != nil {
		slog.Warn("encrypt failed", "err", err)
		return s
	}
	return e
}

// decryptField best-effort decrypts a field for internal use (i.e. in-memory data
// that may later be persisted). On failure it returns the ORIGINAL ciphertext
// unchanged — never a derived/marked string — so callers that re-encrypt on save
// (provider/config/node persistence) cannot overwrite the stored ciphertext with a
// garbage value. Use decryptFieldDisplay instead when the result is shown to humans.
func decryptField(s string) string {
	return decryptFieldWith(enc, s)
}

// decryptFieldWith is decryptField with an explicit encryptor, for callers
// that captured theirs at startup. Background loops must use this form:
// re-reading the enc global races test fixtures that reassign it.
func decryptFieldWith(e *Encryptor, s string) string {
	if e == nil || s == "" {
		return s
	}
	if !IsEncrypted(s) {
		return s // not encrypted, return as-is (e.g. legacy plaintext, or DECRYPT_FAILED markers never reach here)
	}
	d, err := e.Decrypt(s)
	if err != nil {
		slog.Error("decrypt failed — field may be corrupted or key mismatch", "err", err, "hint", "original ciphertext kept to avoid clobbering on save")
		return s // keep ciphertext; save paths skip it because IsEncrypted(s) is true
	}
	return d
}

// decryptFieldDisplay decrypts a field for DISPLAY ONLY (admin UI, logs, masking).
// Unlike decryptField it never feeds a persist path back, so on failure it marks the
// result with a "DECRYPT_FAILED:" prefix so operators can spot a bad value instead of
// mistaking undecryptable ciphertext for plaintext.
func decryptFieldDisplay(s string) string {
	if enc == nil || s == "" {
		return s
	}
	if !IsEncrypted(s) {
		return s
	}
	d, err := enc.Decrypt(s)
	if err != nil {
		slog.Error("decrypt display failed — field may be corrupted or key mismatch", "err", err, "prefix_hint", encPrefix)
		return "DECRYPT_FAILED:" + s
	}
	return d
}

// decryptAPIKey decrypts an API key for testing/display. Returns an error if
// decryption fails (so callers can surface a 500 instead of leaking plaintext).
func decryptAPIKey(s string) (string, error) {
	if enc == nil {
		return s, nil
	}
	return enc.Decrypt(s)
}

// ensure path/filepath import is used even if key-file logic changes.
var _ = filepath.Join
