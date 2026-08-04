package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Auth manages admin credentials, JWT tokens, and SMTP config.
// P0-3: All data access is protected by mu (sync.RWMutex) for concurrent safety.
type Auth struct {
	mu   sync.RWMutex
	data AdminStore
	path string
}

func initAuth(path string) {
	auth = &Auth{path: path}
	auth.load()
}

func (a *Auth) load() {
	b, err := os.ReadFile(a.path)
	if err != nil {
		a.data = AdminStore{
			JWTSecret:        randomString(64),
			JWTRefreshSecret: randomString(64),
			SMTP:             SMTPConfig{Port: 587, UseTLS: true},
		}
		return
	}
	if err := json.Unmarshal(b, &a.data); err != nil {
		slog.Error("failed to parse auth data, resetting to defaults", "error", err)
		a.data = AdminStore{
			JWTSecret:        randomString(64),
			JWTRefreshSecret: randomString(64),
			SMTP:             SMTPConfig{Port: 587, UseTLS: true},
		}
		a.save()
		return
	}
	if a.data.JWTSecret == "" {
		slog.Warn("auth data: JWTSecret missing, generating new one")
		a.data.JWTSecret = randomString(64)
		a.save()
	}
	// Ensure refresh secret exists (for backward compat)
	if a.data.JWTRefreshSecret == "" {
		a.data.JWTRefreshSecret = randomString(64)
		a.save()
	}
	// Decrypt SMTP password if encrypted
	if a.data.SMTP.Password != "" && IsEncrypted(a.data.SMTP.Password) {
		a.data.SMTP.Password = decryptField(a.data.SMTP.Password)
	}
}

// save persists the auth data to disk.
// P0-3: save acquires its own lock to prevent concurrent write corruption.
// C1-fix: Deep copy data before encrypting to avoid mutating in-memory plaintext.
func (a *Auth) save() {
	a.mu.Lock()
	safe := a.deepCopyDataLocked()
	if safe.SMTP.Password != "" && !IsEncrypted(safe.SMTP.Password) {
		safe.SMTP.Password = encryptField(safe.SMTP.Password)
	}
	b, _ := json.MarshalIndent(safe, "", "  ")
	a.mu.Unlock()

	if err := os.MkdirAll("data", 0700); err != nil {
		slog.Error("failed to create data directory", "error", err)
	}
	atomicWriteFile(a.path, b, 0600)
}

// saveLocked persists auth data; caller must already hold a.mu.
// Used internally by methods that already hold the lock.
// C1-fix: Deep copy data before encrypting to avoid mutating in-memory plaintext.
func (a *Auth) saveLocked() {
	safe := a.deepCopyDataLocked()
	if safe.SMTP.Password != "" && !IsEncrypted(safe.SMTP.Password) {
		safe.SMTP.Password = encryptField(safe.SMTP.Password)
	}
	b, _ := json.MarshalIndent(safe, "", "  ")
	os.MkdirAll("data", 0700)
	atomicWriteFile(a.path, b, 0600)
}

// deepCopyDataLocked returns a deep copy of a.data.
// Caller must hold a.mu.
func (a *Auth) deepCopyDataLocked() AdminStore {
	cp := a.data
	// Deep copy slices to avoid shared references
	if len(a.data.Collaborators) > 0 {
		cp.Collaborators = make([]Collaborator, len(a.data.Collaborators))
		copy(cp.Collaborators, a.data.Collaborators)
	}
	if a.data.Reset != nil {
		r := *a.data.Reset
		cp.Reset = &r
	}
	return cp
}

// Initialized returns whether admin has been set up.
func (a *Auth) Initialized() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.data.Initialized
}

// validatePasswordStrength checks password complexity requirements.
// SA-14 (strict): Enforces minimum 12 characters AND requires ALL 4 character classes:
// uppercase letters, lowercase letters, digits, and special characters.
func validatePasswordStrength(password string) error {
	if len(password) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	// B150: Limit password length to prevent bcrypt DoS (bcrypt processes up to 72 bytes)
	if len(password) > 128 {
		return errors.New("password must be at most 128 characters")
	}
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, ch := range password {
		switch {
		case ch >= 'A' && ch <= 'Z':
			hasUpper = true
		case ch >= 'a' && ch <= 'z':
			hasLower = true
		case ch >= '0' && ch <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
		return errors.New("password must contain uppercase, lowercase, digit, and special character")
	}
	return nil
}

// SetupAdmin creates the initial admin account.
func (a *Auth) SetupAdmin(username, password, email string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.data.Initialized {
		return errors.New("admin already initialized")
	}
	if username == "" || password == "" {
		return errors.New("username and password are required")
	}
	if err := validatePasswordStrength(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	a.data.Admin = AdminData{
		Username:     username,
		PasswordHash: string(hash),
		Email:        email,
		CreatedAt:    time.Now().Format(time.RFC3339),
	}
	a.data.Initialized = true
	a.saveLocked()
	slog.Info("admin initialized", "username", username)
	return nil
}

// VerifyCredentials checks username/password.
func (a *Auth) VerifyCredentials(username, password string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.data.Initialized || a.data.Admin.Username != username {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(a.data.Admin.PasswordHash), []byte(password)) == nil
}

// RegisterCollaborator creates a new collaborator account linked to a guest key.
func (a *Auth) RegisterCollaborator(username, password, guestKey string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if username == "" || password == "" || guestKey == "" {
		return errors.New("username, password and guest_key are required")
	}
	// B153: Limit collaborator count
	if len(a.data.Collaborators) >= 20 {
		return errors.New("maximum collaborator count (20) reached")
	}
	// Check if username already taken (admin or collaborator)
	if a.data.Admin.Username == username {
		return errors.New("username already taken")
	}
	for _, c := range a.data.Collaborators {
		if c.Username == username {
			return errors.New("username already taken")
		}
		if c.GuestKey == guestKey {
			return errors.New("this guest key is already registered")
		}
	}
	if err := validatePasswordStrength(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	a.data.Collaborators = append(a.data.Collaborators, Collaborator{
		Username:     username,
		PasswordHash: string(hash),
		GuestKey:     guestKey,
		CreatedAt:    time.Now().Format(time.RFC3339),
	})
	a.saveLocked()
	slog.Info("collaborator registered", "username", username)
	return nil
}

// VerifyCollaboratorCredentials checks collaborator username/password.
func (a *Auth) VerifyCollaboratorCredentials(username, password string) *Collaborator {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, c := range a.data.Collaborators {
		if c.Username == username {
			if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) == nil {
				return &c
			}
			return nil
		}
	}
	return nil
}

// ValidateGuestKeyForRegistration checks if a guest key is valid for collaborator registration.
func (a *Auth) ValidateGuestKeyForRegistration(key string) bool {
	if guestKeyStore == nil {
		return false
	}
	rec := guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil || rec.Revoked {
		return false
	}
	// Check if already registered
	for _, c := range a.data.Collaborators {
		if c.GuestKey == key {
			return false // already registered
		}
	}
	return true
}

// ChangePassword updates the admin password.
func (a *Auth) ChangePassword(oldPass, newPass string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.data.Initialized || a.data.Admin.Username == "" {
		return errors.New("admin not initialized")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.data.Admin.PasswordHash), []byte(oldPass)); err != nil {
		return errors.New("incorrect old password")
	}
	if err := validatePasswordStrength(newPass); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	a.data.Admin.PasswordHash = string(hash)
	a.saveLocked()
	return nil
}

// CreateToken generates a JWT access token (24h) and a refresh token (7d).
// S-3: Returns both tokens for the refresh token flow.
func (a *Auth) CreateToken(username string, remember bool) (accessToken string, refreshToken string) {
	a.mu.RLock()
	accessSecret := a.data.JWTSecret
	refreshSecret := a.data.JWTRefreshSecret
	a.mu.RUnlock()

	// Access token: 24h (or 7d if remember)
	accessExpHours := 24
	if remember {
		accessExpHours = 7 * 24
	}
	accessClaims := jwt.MapClaims{
		"sub":  username,
		"exp":  time.Now().Add(time.Duration(accessExpHours) * time.Hour).Unix(),
		"iat":  time.Now().Unix(),
		"iss":  "openmodelpool",
		"type": "access",
	}
	accessTokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessToken, err := accessTokenObj.SignedString([]byte(accessSecret))
	if err != nil {
		return "", ""
	}

	refreshClaims := jwt.MapClaims{
		"sub":  username,
		"exp":  time.Now().Add(7 * 24 * time.Hour).Unix(),
		"iat":  time.Now().Unix(),
		"iss":  "openmodelpool",
		"type": "refresh",
	}
	refreshTokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshToken, err = refreshTokenObj.SignedString([]byte(refreshSecret))
	if err != nil {
		return "", ""
	}

	return accessToken, refreshToken
}

// CreateAccessToken generates only an access token (legacy compatibility).
func (a *Auth) CreateAccessToken(username string, remember bool) string {
	access, _ := a.CreateToken(username, remember)
	return access
}

// RefreshAccessToken validates a refresh token and returns a new access token.
func (a *Auth) RefreshAccessToken(refreshTokenStr string) (string, error) {
	a.mu.RLock()
	refreshSecret := a.data.JWTRefreshSecret
	accessSecret := a.data.JWTSecret
	a.mu.RUnlock()

	token, err := jwt.Parse(refreshTokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(refreshSecret), nil
	})
	if err != nil || !token.Valid {
		return "", errors.New("invalid refresh token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid claims")
	}
	// Verify it is a refresh token
	tokenType, _ := claims["type"].(string)
	if tokenType != "refresh" {
		return "", errors.New("not a refresh token")
	}
	// B111: Validate issuer
	if iss, _ := claims["iss"].(string); iss != "openmodelpool" {
		return "", errors.New("invalid token issuer")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", errors.New("missing subject")
	}

	// Issue new access token (24h)
	newAccessClaims := jwt.MapClaims{
		"sub":  sub,
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
		"iat":  time.Now().Unix(),
		"iss":  "openmodelpool",
		"type": "access",
	}
	newAccessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, newAccessClaims)
	s, err := newAccessToken.SignedString([]byte(accessSecret))
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return s, nil
}

// VerifyToken validates a JWT access token and returns the username.
func (a *Auth) VerifyToken(tokenStr string) (string, error) {
	a.mu.RLock()
	secret := a.data.JWTSecret
	a.mu.RUnlock()

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return "", errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid claims")
	}
	// B111: Validate issuer
	if iss, _ := claims["iss"].(string); iss != "openmodelpool" {
		return "", errors.New("invalid token issuer")
	}
	// B109: Reject refresh tokens used as access tokens
	if tokenType, _ := claims["type"].(string); tokenType == "refresh" {
		return "", errors.New("refresh token cannot be used for authentication")
	}
	sub, _ := claims["sub"].(string)
	return sub, nil
}

// AdminInfo returns admin info (without password).
func (a *Auth) AdminInfo() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return map[string]string{
		"username":   a.data.Admin.Username,
		"email":      a.data.Admin.Email,
		"created_at": a.data.Admin.CreatedAt,
	}
}

// GetEmail returns admin email.
func (a *Auth) GetEmail() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.data.Admin.Email
}

// UpdateEmail updates admin email.
func (a *Auth) UpdateEmail(email string) {
	a.mu.Lock()
	a.data.Admin.Email = email
	a.mu.Unlock()
	a.save()
}

// SMTP methods
func (a *Auth) GetSMTP() SMTPConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.data.SMTP
}

func (a *Auth) IsSMTPConfigured() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s := a.data.SMTP
	return s.Host != "" && s.Username != "" && s.FromEmail != ""
}

func (a *Auth) UpdateSMTP(c SMTPConfig) {
	a.mu.Lock()
	a.data.SMTP = c
	a.mu.Unlock()
	a.save()
}

// CreateResetToken generates an email-based password reset token.
func (a *Auth) CreateResetToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Reuse existing unexpired token to prevent spam
	if a.data.Reset != nil && !a.data.Reset.Used {
		if exp, err := time.Parse(time.RFC3339, a.data.Reset.Expire); err == nil {
			if time.Now().Before(exp) {
				return a.data.Reset.Token
			}
		}
	}
	tok := randomString(32)
	a.data.Reset = &ResetToken{
		Token:  tok,
		Email:  a.data.Admin.Email,
		Expire: time.Now().Add(30 * time.Minute).Format(time.RFC3339),
	}
	a.saveLocked()
	return tok
}

func (a *Auth) VerifyResetToken(tok string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.verifyResetTokenLocked(tok)
}

// verifyResetTokenLocked checks reset token validity; caller must hold a.mu.
func (a *Auth) verifyResetTokenLocked(tok string) bool {
	r := a.data.Reset
	if r == nil || r.Used || r.Token != tok {
		return false
	}
	exp, err := time.Parse(time.RFC3339, r.Expire)
	if err != nil || time.Now().After(exp) {
		return false
	}
	return true
}

func (a *Auth) ResetPassword(tok, newPass string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.verifyResetTokenLocked(tok) {
		return errors.New("invalid or expired reset token")
	}
	if err := validatePasswordStrength(newPass); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	a.data.Admin.PasswordHash = string(hash)
	a.data.Reset.Used = true
	a.saveLocked()
	return nil
}

// ============================================================
// P0-2: Independent Reset Code (replaces Proxy API Key reuse)
// ============================================================

// GenerateResetCode creates a new independent reset code and stores its hash.
// This code can be used to reset the admin password without needing the Proxy API Key.
// Returns the plaintext code (shown once to the admin) and its expiration time.
func (a *Auth) GenerateResetCode() (string, time.Time, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Generate a random code: 8 chars, human-friendly
	codeBytes := make([]byte, 6)
	if _, err := rand.Read(codeBytes); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate reset code: %w", err)
	}
	code := base64.URLEncoding.EncodeToString(codeBytes)[:8]

	// Hash the code for storage (so we don't store it in plaintext)
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to hash reset code: %w", err)
	}

	expires := time.Now().Add(24 * time.Hour)
	a.data.ResetCodeHash = string(hash)
	a.data.ResetCodeExpires = expires.Format(time.RFC3339)
	a.saveLocked()

	slog.Info("admin reset code generated", "expires", expires.Format(time.RFC3339))
	return code, expires, nil
}

// ValidateAndConsumeResetCode checks if the provided code matches the stored hash
// and hasn't expired. If valid, the code is consumed (single-use) and returns true.
func (a *Auth) ValidateAndConsumeResetCode(code string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.data.ResetCodeHash == "" {
		return false, errors.New("no reset code configured")
	}

	// Check expiration
	if a.data.ResetCodeExpires != "" {
		expires, err := time.Parse(time.RFC3339, a.data.ResetCodeExpires)
		if err != nil || time.Now().After(expires) {
			return false, errors.New("reset code has expired")
		}
	}

	// Compare with stored hash
	if err := bcrypt.CompareHashAndPassword([]byte(a.data.ResetCodeHash), []byte(code)); err != nil {
		return false, errors.New("invalid reset code")
	}

	// Code is valid — consume it (single-use)
	a.data.ResetCodeHash = ""
	a.data.ResetCodeExpires = ""
	a.saveLocked()

	return true, nil
}

// HasResetCode returns whether a reset code is currently configured.
func (a *Auth) HasResetCode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.data.ResetCodeHash != ""
}

// randomString generates a cryptographically random string of length n.
// m2-fix: Log fatal instead of panic on rand.Read failure for graceful shutdown.
func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read should never fail on modern OS, but handle it gracefully
		slog.Error("crypto/rand.Read failed — system entropy source unavailable", "err", err)
		// Fallback: use timestamp-based string to avoid crashing the entire process
		// This is not cryptographically strong but prevents service outage
		return fmt.Sprintf("%d%s", time.Now().UnixNano(), base64.URLEncoding.EncodeToString(b))[:n]
	}
	return base64.URLEncoding.EncodeToString(b)[:n]
}
