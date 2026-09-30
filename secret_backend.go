package main

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

// G5: OS Keyring 集成 —— 主密钥的可选存放后端。
//
// 设计要点（用户已拍板）：
//   - 默认关闭 opt-in：secret_backend 开关（"keyring"|"file"，默认 "file"），
//     config 文件 + OPENMODELPOOL_SECRET_BACKEND 环境变量覆盖（环境变量优先）。
//   - keyring 只存 32 字节主密钥，不逐条存 provider token；上层
//     encryptField/decryptField 的 omp:e: + AES-256-GCM 格式完全不变。
//   - keyring 不可用（Linux 容器/无头服务器无 D-Bus 等）时记 warn 日志并静默
//     回退文件链，绝不 fail-closed。
//   - 库用 zalando/go-keyring：零 CGO（darwin 走 exec /usr/bin/security），
//     6 平台交叉编译安全。99designs/keyring 因 darwin 需 CGO 会导致交叉编译
//     静默失效，明确排除。
//   - 不碰：bbolt ledger 明文账本私钥、data/node.key 格式、omp:e: 密文格式。
//
// 安全注记：在服务器场景（独立 UID 运行）keyring 相对 0600 文件的增益有限；
// 真正价值在桌面端会话隔离与防 data/ 目录被整体拷贝时钥匙跟着密文一起走。

const (
	// secretBackendKey 是 config.json / 环境变量里的开关名。
	secretBackendKey     = "secret_backend"
	secretBackendFile    = "file"
	secretBackendKeyring = "keyring"

	// secretBackendEnv 是覆盖 secret_backend 的环境变量
	//（沿用 OPENMODELPOOL_* 前缀惯例；优先级高于 config 文件）。
	secretBackendEnv = "OPENMODELPOOL_SECRET_BACKEND"

	// keyringService/keyringAccount 是 OS keyring 条目的命名：
	// 各平台 keyring 里显示为 服务=openmodelpool / 账号=master-key。
	keyringService = "openmodelpool"
	keyringAccount = "master-key"
)

// keyringStore 是 OS keyring 的最小操作接口。包级变量 keyringImpl 方便测试
// 注入 fake；生产实现走 zalando/go-keyring。
// 测试直接重赋值（与 enc 全局的重赋值惯例一致）；不要 t.Parallel。
type keyringStore interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
}

// osKeyring 是 zalando/go-keyring 的生产适配器。
type osKeyring struct{}

func (osKeyring) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (osKeyring) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}

// keyringImpl 是当前使用的 keyring 后端（生产=osKeyring，测试=fake）。
var keyringImpl keyringStore = osKeyring{}

// secretBackend 解析 secret_backend 开关：环境变量 > config 文件 > 默认 file。
// 注意 init() 执行时 cfg 尚未加载（nil），此时只能看到环境变量；config 文件
// 的值在 initCore 的 refreshEncryptorForSecretBackend() 里生效。
func secretBackend() string {
	if v := strings.TrimSpace(os.Getenv(secretBackendEnv)); v != "" {
		return normalizeSecretBackend(v)
	}
	if cfg != nil {
		return normalizeSecretBackend(cfg.Get(secretBackendKey, secretBackendFile))
	}
	return secretBackendFile
}

func normalizeSecretBackend(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case secretBackendKeyring:
		return secretBackendKeyring
	case secretBackendFile, "":
		return secretBackendFile
	default:
		slog.Warn("unknown secret_backend value; falling back to file", "value", v)
		return secretBackendFile
	}
}

// loadKeyringKey 从 OS keyring 读取主密钥（base64 包装的 32 字节）。
// ok=false 仅表示"未找到"；keyring 本身不可用或条目损坏时记 warn 日志并
// 返回 ok=false，调用方静默回退文件链——绝不 fail-closed。
func loadKeyringKey() (key []byte, ok bool) {
	s, err := keyringImpl.Get(keyringService, keyringAccount)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, false
		}
		slog.Warn("OS keyring unavailable; falling back to file key chain",
			"service", keyringService, "err", err,
			"hint", "Linux 需要 D-Bus session bus + secret service；容器/无头服务器上这是预期行为")
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != 32 {
		slog.Warn("OS keyring entry is corrupt (not 32 bytes); falling back to file key chain",
			"service", keyringService, "account", keyringAccount)
		return nil, false
	}
	return raw, true
}

// storeKeyringKey 把主密钥写入 OS keyring。失败只记 warn（调用方继续走文件链）。
func storeKeyringKey(key []byte) bool {
	if err := keyringImpl.Set(keyringService, keyringAccount, base64.StdEncoding.EncodeToString(key)); err != nil {
		slog.Warn("failed to store master key in OS keyring; continuing with file chain", "err", err)
		return false
	}
	return true
}
