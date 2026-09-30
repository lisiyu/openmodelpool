package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

// ============================================================
// G5 OS Keyring 后端测试（fake keyring，不依赖真实 D-Bus/Keychain）
// ============================================================

// fakeKeyring 是内存版 keyringStore：data 为空即模拟 ErrNotFound。
type fakeKeyring struct {
	data     map[string]string
	getErr   error
	setErr   error
	setCalls int
}

func (f *fakeKeyring) key(service, account string) string { return service + "\x00" + account }

func (f *fakeKeyring) Get(service, account string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.data[f.key(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Set(service, account, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.data == nil {
		f.data = map[string]string{}
	}
	f.data[f.key(service, account)] = value
	f.setCalls++
	return nil
}

func (f *fakeKeyring) storedKey(t *testing.T) []byte {
	t.Helper()
	s, ok := f.data[f.key(keyringService, keyringAccount)]
	if !ok {
		t.Fatal("fake keyring: no stored key")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != 32 {
		t.Fatalf("fake keyring: stored value corrupt: %v", err)
	}
	return raw
}

func rand32(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

// setupKeyringTest 把 encKeyFile 指向 TempDir、注入 fake keyring、
// 打开 secret_backend=keyring；清理时全部恢复。
func setupKeyringTest(t *testing.T, backend string) *fakeKeyring {
	t.Helper()
	oldKeyFile := encKeyFile
	encKeyFile = filepath.Join(t.TempDir(), ".enc_key")
	oldImpl := keyringImpl
	fake := &fakeKeyring{}
	keyringImpl = fake
	t.Setenv(secretBackendEnv, backend)
	t.Setenv("OPENMODELPOOL_ENC_KEY", "")
	t.Cleanup(func() {
		encKeyFile = oldKeyFile
		keyringImpl = oldImpl
	})
	return fake
}

func TestSecretBackend_EnvKeyWinsOverKeyring(t *testing.T) {
	fake := setupKeyringTest(t, "keyring")
	envKey := rand32(t)
	t.Setenv("OPENMODELPOOL_ENC_KEY", base64.StdEncoding.EncodeToString(envKey))
	krKey := rand32(t)
	fake.data = map[string]string{fake.key(keyringService, keyringAccount): base64.StdEncoding.EncodeToString(krKey)}

	e, err := NewEncryptor()
	if err != nil {
		t.Fatal(err)
	}
	if string(e.key) != string(envKey) {
		t.Error("OPENMODELPOOL_ENC_KEY must take precedence over keyring")
	}
}

func TestSecretBackend_KeyringHit(t *testing.T) {
	fake := setupKeyringTest(t, "keyring")
	krKey := rand32(t)
	fake.data = map[string]string{fake.key(keyringService, keyringAccount): base64.StdEncoding.EncodeToString(krKey)}

	e, err := NewEncryptor()
	if err != nil {
		t.Fatal(err)
	}
	if string(e.key) != string(krKey) {
		t.Error("expected key from keyring")
	}
	if _, err := os.Stat(encKeyFile); !os.IsNotExist(err) {
		t.Error("keyring hit must not create a key file")
	}
}

func TestSecretBackend_KeyringErrorFallsBackToFile(t *testing.T) {
	fake := setupKeyringTest(t, "keyring")
	fake.getErr = errors.New("D-Bus not available")
	fileKey := rand32(t)
	if err := os.WriteFile(encKeyFile, fileKey, 0o600); err != nil {
		t.Fatal(err)
	}

	e, err := NewEncryptor()
	if err != nil {
		t.Fatalf("keyring error must not fail closed: %v", err)
	}
	if string(e.key) != string(fileKey) {
		t.Error("expected fallback to file key")
	}
}

func TestSecretBackend_FreshGenerateDualWrite(t *testing.T) {
	fake := setupKeyringTest(t, "keyring")

	e, err := NewEncryptor()
	if err != nil {
		t.Fatal(err)
	}
	if len(e.key) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(e.key))
	}
	if fake.setCalls != 1 {
		t.Errorf("expected 1 keyring Set, got %d", fake.setCalls)
	}
	fileKey, err := os.ReadFile(encKeyFile)
	if err != nil {
		t.Fatalf("fallback seed file not written: %v", err)
	}
	if string(fileKey) != string(e.key) || string(fake.storedKey(t)) != string(e.key) {
		t.Error("keyring and file must hold the same generated key")
	}
}

func TestSecretBackend_MigrationRenamesBak(t *testing.T) {
	fake := setupKeyringTest(t, "keyring")
	fileKey := rand32(t)
	if err := os.WriteFile(encKeyFile, fileKey, 0o600); err != nil {
		t.Fatal(err)
	}

	e, err := NewEncryptor()
	if err != nil {
		t.Fatal(err)
	}
	if string(e.key) != string(fileKey) {
		t.Error("expected the file key to be used after migration")
	}
	if string(fake.storedKey(t)) != string(fileKey) {
		t.Error("expected the file key to be stored in keyring")
	}
	if _, err := os.Stat(encKeyFile); !os.IsNotExist(err) {
		t.Error("expected .enc_key to be renamed away")
	}
	bak, err := os.ReadFile(encKeyBakFile())
	if err != nil || string(bak) != string(fileKey) {
		t.Errorf("expected .bak to hold the migrated key: %v", err)
	}
}

func TestSecretBackend_BakMarkerRecovery(t *testing.T) {
	fake := setupKeyringTest(t, "file") // 后端显式设为 file
	krKey := rand32(t)
	fake.data = map[string]string{fake.key(keyringService, keyringAccount): base64.StdEncoding.EncodeToString(krKey)}
	// .enc_key 缺失、.bak 存在：模拟迁移后文件丢失
	if err := os.WriteFile(encKeyBakFile(), krKey, 0o600); err != nil {
		t.Fatal(err)
	}

	e, err := NewEncryptor()
	if err != nil {
		t.Fatalf("must recover key from keyring, got error: %v", err)
	}
	if string(e.key) != string(krKey) {
		t.Error("expected recovery of the keyring key")
	}
}

func TestSecretBackend_BakMarkerKeyringDownFailsClosed(t *testing.T) {
	fake := setupKeyringTest(t, "file")
	fake.getErr = errors.New("D-Bus not available")
	krKey := rand32(t)
	if err := os.WriteFile(encKeyBakFile(), krKey, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewEncryptor(); err == nil {
		t.Error("expected fail-closed error when key is unrecoverable (no file, keyring down, .bak exists)")
	}
}

func TestSecretBackend_StaleFileRemovedOnKeyringHit(t *testing.T) {
	fake := setupKeyringTest(t, "keyring")
	krKey := rand32(t)
	fake.data = map[string]string{fake.key(keyringService, keyringAccount): base64.StdEncoding.EncodeToString(krKey)}
	// .bak 存在 + .enc_key 被 init() 阶段误生成（含诱饵钥匙）
	if err := os.WriteFile(encKeyBakFile(), krKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(encKeyFile, rand32(t), 0o600); err != nil {
		t.Fatal(err)
	}

	e, err := NewEncryptor()
	if err != nil {
		t.Fatal(err)
	}
	if string(e.key) != string(krKey) {
		t.Error("expected the keyring key, not the stale file")
	}
	if _, err := os.Stat(encKeyFile); !os.IsNotExist(err) {
		t.Error("stale regenerated key file must be removed on keyring hit")
	}
}

func TestNormalizeSecretBackend(t *testing.T) {
	cases := map[string]string{
		"keyring": "keyring",
		"KEYRING": "keyring",
		"file":    "file",
		"":        "file",
		"bogus":   "file",
	}
	for in, want := range cases {
		if got := normalizeSecretBackend(in); got != want {
			t.Errorf("normalizeSecretBackend(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRefreshEncryptorForSecretBackend(t *testing.T) {
	fake := setupKeyringTest(t, "keyring")
	krKey := rand32(t)
	fake.data = map[string]string{fake.key(keyringService, keyringAccount): base64.StdEncoding.EncodeToString(krKey)}

	oldEnc := enc
	defer func() { enc = oldEnc }()

	refreshEncryptorForSecretBackend()
	if enc == nil || string(enc.key) != string(krKey) {
		t.Error("expected global enc to be refreshed from keyring")
	}
	if !enc.IsReady() {
		t.Error("expected refreshed enc to be ready")
	}
}

func TestRefreshEncryptorForSecretBackend_FileBackendNoop(t *testing.T) {
	setupKeyringTest(t, "file")
	sentinel := &Encryptor{key: rand32(t), ready: true}
	oldEnc := enc
	enc = sentinel
	defer func() { enc = oldEnc }()

	refreshEncryptorForSecretBackend()
	if enc != sentinel {
		t.Error("file backend must leave the global enc untouched")
	}
}
