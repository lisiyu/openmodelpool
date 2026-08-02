# Changelog

## [4.2.1] - 2025-08-03

### 🔒 Security Fixes (Critical)

- **C1**: Fixed TOCTOU race condition in `auth.go` `save()`/`saveLocked()` — previously, `safe := a.data` was a shallow copy, causing `encryptField()` to mutate the in-memory plaintext SMTP password to ciphertext. Now uses `deepCopyDataLocked()` to create a proper deep copy before encryption.
- **C2**: Fixed non-atomic file write in `provider.go` `writeFile()` — previously used `os.WriteFile()` which can corrupt data on crash. Now uses `atomicWriteFile()` (write-to-tmp + rename) consistent with `config.go` and `auth.go`.
- **C3**: Fixed anonymous admin access from public internet — previously, when `proxy_api_key` was unset and no consumers existed, any remote IP could access `/v1/chat/completions` as admin. Now anonymous admin access is restricted to localhost/private network IPs only.

### 🐛 Bug Fixes (Major)

- **M1**: Implemented wildcard subdomain matching in `isOriginAllowed()` — the comment promised `*.example.com` support but the code only did exact matching. Now properly matches subdomains against wildcard patterns.
- **M3**: `encryptField()` now refuses to encrypt data when using an ephemeral key — previously, data encrypted with an ephemeral key would be unrecoverable after restart. Now logs a warning and returns plaintext, preventing silent data loss.
- **M4**: Fixed `extractClientIP()` IPv6 address parsing — previously used `strings.LastIndex(":")` which incorrectly parsed `[::1]:8000` as `[::1]`. Now uses `net.SplitHostPort()` for correct handling.

### 🧹 Code Quality (Minor)

- **M5**: Removed debug logging residual in `provider.go` `load()` — the `slog.Info("after load", ...)` loop was printing key metadata for every provider on every startup, potentially leaking sensitive information (key length).
- **m1**: Replaced hand-rolled `toUpper()` in `config.go` with `strings.ToUpper()` — the manual implementation only handled ASCII a-z, while the standard library properly handles Unicode.
- **m4**: Fixed `Provider.Safe()` shallow copy — previously, `WebSession`, `ModelBotMap`, `ExtraHeaders`, and other map/slice fields were shared references. Callers modifying the returned value could corrupt the original. Now performs deep copies of all reference-type fields.
- **m6**: Fixed `readJSON()` `MaxBytesReader` using `nil` ResponseWriter — now passes `w` so HTTP 413 is automatically sent on body size overflow. Updated all 65+ call sites across the codebase.
- **A3**: Unified error response format in `withAuth()` middleware — previously used `map[string]string{"error": "..."}` inconsistent with the rest of the codebase. Now uses `ErrorResponse{Error: ErrorDetail{...}}` format.
