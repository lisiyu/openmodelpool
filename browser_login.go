package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// isWindows returns true if the current OS is Windows
func isWindows() bool {
	return runtime.GOOS == "windows"
}

// ensureDisplayEnvironment sets up the display environment for browser automation.
// We always launch Chrome in headless mode, so a virtual display is normally not
// required. On Linux we still attempt to start Xvfb (best-effort, non-fatal) for
// environments where a display server is expected, but we deliberately do NOT set
// DISPLAY — headless Chrome ignores it and an incorrect DISPLAY can confuse Chrome.
func ensureDisplayEnvironment() {
	if isWindows() {
		return // Windows doesn't need Xvfb
	}
	// Linux: start Xvfb on display :99 if not already running.
	if err := exec.Command("xdpyinfo", "-display", ":99").Run(); err == nil {
		return // Xvfb already running
	}
	// Clean up stale lock files.
	os.Remove("/tmp/.X99-lock")
	os.Remove("/tmp/.X11-unix/X99")
	// Start Xvfb (best-effort; headless Chrome works without it).
	if cmd := exec.Command("Xvfb", ":99", "-screen", "0", "1280x800x24", "-ac"); cmd.Start() == nil {
		slog.Info("Xvfb started", "display", ":99", "pid", cmd.Process.Pid)
		time.Sleep(1 * time.Second)
	}
}

// findBrowserExecutable resolves the Chrome/Chromium executable path in a
// cross-platform, environment-overridable way.
//
// Resolution order:
//  1. Explicit override via env vars: OMP_CHROME_PATH, CHROME_PATH, CHROMIUM_PATH
//     (handy when Chrome is installed in a non-standard location, or the OS
//     package manager puts it somewhere unexpected).
//  2. OS-specific discovery (common install dirs + $PATH lookup).
//  3. Empty string — chromedp then falls back to its own findExecPath(), which
//     is fine on most systems. The caller logs a clear warning in that case.
func findBrowserExecutable() string {
	// 1. Environment override (highest priority).
	for _, env := range []string{"OMP_CHROME_PATH", "CHROME_PATH", "CHROMIUM_PATH"} {
		if p := strings.TrimSpace(os.Getenv(env)); p != "" {
			if _, err := os.Stat(p); err == nil {
				slog.Info("using Chrome from env override", "env", env, "path", p)
				return p
			}
			slog.Warn("Chrome path from env does not exist; ignoring", "env", env, "path", p)
		}
	}

	// 2. OS-specific discovery.
	var candidate string
	switch runtime.GOOS {
	case "windows":
		candidate = findChromeWindows()
	case "darwin":
		candidate = findChromeDarwin()
	default:
		candidate = findChromeLinux()
	}

	if candidate != "" {
		slog.Info("browser executable resolved", "path", candidate, "os", runtime.GOOS)
	} else {
		slog.Warn("could not locate a Chrome/Chromium executable; chromedp will use its default search", "os", runtime.GOOS)
	}
	return candidate
}

// findChromeWindows resolves Chrome/Edge on Windows using common install
// directories (which may contain spaces) and a $PATH lookup.
func findChromeWindows() string {
	programFiles := os.Getenv("ProgramFiles")
	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	localAppData := os.Getenv("LOCALAPPDATA")
	candidates := []string{}
	add := func(p string) {
		if p != "" {
			candidates = append(candidates, p)
		}
	}
	// %LOCALAPPDATA% is the most common Chrome location on Windows.
	add(localAppData + `\Google\Chrome\Application\chrome.exe`)
	add(programFilesX86 + `\Google\Chrome\Application\chrome.exe`)
	add(programFiles + `\Google\Chrome\Application\chrome.exe`)
	// Edge as fallback.
	add(localAppData + `\Microsoft\Edge\Application\msedge.exe`)
	add(programFilesX86 + `\Microsoft\Edge\Application\msedge.exe`)
	add(programFiles + `\Microsoft\Edge\Application\msedge.exe`)
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Finally, try $PATH lookup.
	if p, err := exec.LookPath("chrome.exe"); err == nil {
		return p
	}
	if p, err := exec.LookPath("msedge.exe"); err == nil {
		return p
	}
	return ""
}

// findChromeLinux resolves Chrome/Chromium on Linux via well-known package
// paths and a $PATH lookup.
func findChromeLinux() string {
	candidates := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/snap/bin/chromium",
		"/usr/lib/chromium/chromium",
		"/opt/google/chrome/chrome",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// findChromeDarwin resolves Chrome/Chromium on macOS via the standard
// /Applications location and a $PATH lookup.
func findChromeDarwin() string {
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// sanitizeProfileName removes characters that are unsafe for filesystem paths,
// replacing anything that is not [a-zA-Z0-9_-] with an underscore.
func sanitizeProfileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

// userDataDirFor returns a fresh, deterministic per-provider user-data dir for
// Chrome. Using a dedicated, cleaned directory (instead of relying solely on
// chromedp's random temp dir) avoids stale-lock and "chrome failed to start"
// issues on Windows when a previous session did not fully release its profile.
// The caller is responsible for removing the directory when the session ends.
func userDataDirFor(providerID string) (string, error) {
	base := filepath.Join(os.TempDir(), "omp-browser")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	// Unique suffix (pid + monotonic sequence) avoids lock contention between
	// launches of the same provider, even within a single process. A sequence
	// is used instead of a timestamp because timer resolution can be too coarse
	// (e.g. Windows) for two rapid calls to differ.
	seq := atomic.AddInt64(&browserProfileSeq, 1)
	suffix := fmt.Sprintf("%s-%d-%d", sanitizeProfileName(providerID), os.Getpid(), seq)
	dir := filepath.Join(base, suffix)
	// Start from a clean slate.
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// browserLaunchFlags returns the Chrome launch flags as a name→value map.
// A bool true renders as "--name"; a string renders as "--name=value". This map
// is the single source of truth for launch flags and is exercised directly by
// tests; buildBrowserAllocatorOptions converts it to chromedp options.
//
// It includes the flags required for modern headless Chrome:
//   - Chrome/Chromium 132+ removed the legacy headless mode, so we use the
//     explicit "--headless=new" form.
//   - Chromium 139+ no longer falls back to SwiftShader for software rendering
//     unless "--enable-unsafe-swiftshader" is passed; without it the GPU process
//     fails and the browser exits immediately with "chrome failed to start".
//     This was the root cause of the Windows launch failure.
//   - "--no-sandbox" and "--disable-dev-shm-usage" are required on Linux
//     (containers / running as root), otherwise Chrome refuses to start.
func browserLaunchFlags(userDataDir, proxyURL, chromePath string) map[string]any {
	flags := map[string]any{
		// New headless mode (Chrome 128+).
		"headless": "new",
		// Required for software rendering on headless Chrome 139+.
		"disable-gpu":                   true,
		"enable-unsafe-swiftshader":     true,
		"disable-extensions":            true,
		"disable-background-networking": true,
		"disable-default-apps":          true,
		"disable-sync":                  true,
		"no-first-run":                  true,
		"no-default-browser-check":      true,
		"user-data-dir":                 userDataDir,
	}

	if runtime.GOOS == "linux" {
		// Required in containers / when running as root.
		flags["no-sandbox"] = true
		flags["disable-dev-shm-usage"] = true
	}

	if proxyURL != "" {
		flags["proxy-server"] = proxyURL
		// Force DNS through proxy to prevent leaks; only when proxy is active.
		flags["host-resolver-rules"] = "MAP * ~NOTFOUND, EXCLUDE 127.0.0.1"
	}

	return flags
}

// buildBrowserAllocatorOptions builds chromedp allocator options that work
// reliably across Windows / Linux / macOS.
//
// Note: chromedp's NewExecAllocator does NOT merge DefaultExecAllocatorOptions,
// so every needed flag must be set explicitly here. chromedp still auto-adds
// "--remote-debugging-port=0" and a fallback "--no-sandbox" when running as
// root; we set no-sandbox ourselves on Linux for clarity.
func buildBrowserAllocatorOptions(userDataDir, proxyURL, chromePath string) []chromedp.ExecAllocatorOption {
	opts := []chromedp.ExecAllocatorOption{
		chromedp.WindowSize(1280, 800),
	}
	for name, value := range browserLaunchFlags(userDataDir, proxyURL, chromePath) {
		name, value := name, value
		switch v := value.(type) {
		case bool:
			if v {
				opts = append(opts, chromedp.Flag(name, true))
			}
		case string:
			opts = append(opts, chromedp.Flag(name, v))
		}
	}

	if chromePath != "" {
		opts = append(opts, chromedp.ExecPath(chromePath))
	}

	return opts
}

// BrowserLoginSession manages a Chrome session for provider login
type BrowserLoginSession struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	providerID  string
	status      string
	screenshot  string
	message     string
	currentURL  string
	proxyURL    string
	userDataDir string
	createdAt   time.Time
}

var (
	browserSessions   = make(map[string]*BrowserLoginSession)
	browserSessionsMu sync.RWMutex
	// browserProfileSeq generates a unique suffix per launched profile dir so
	// that rapid successive launches never collide on the same user-data dir.
	browserProfileSeq int64
)

func takeScreenshot(ctx context.Context) ([]byte, error) {
	var buf []byte
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		b, err := page.CaptureScreenshot().
			WithFormat(page.CaptureScreenshotFormatJpeg).
			WithQuality(70).
			Do(ctx)
		if err != nil {
			return err
		}
		buf = b
		return nil
	}))
	return buf, err
}

func clickByText(ctx context.Context, text string) bool {
	var clicked bool
	_ = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`
		(function() {
			var els = document.querySelectorAll('button, a, [role="button"], span, div');
			for (var i = 0; i < els.length; i++) {
				var t = els[i].textContent || els[i].innerText || '';
				if (t.indexOf(%q) !== -1 && els[i].offsetParent !== null) {
					els[i].click();
					return true;
				}
			}
			return false;
		})()
	`, text), &clicked))
	return clicked
}

func fillField(ctx context.Context, selectors []string, value string) bool {
	for _, sel := range selectors {
		err := chromedp.Run(ctx,
			chromedp.WaitVisible(sel, chromedp.ByQuery),
		)
		if err != nil {
			continue
		}
		err = chromedp.Run(ctx,
			chromedp.Clear(sel, chromedp.ByQuery),
			chromedp.SendKeys(sel, value, chromedp.ByQuery),
		)
		if err == nil {
			return true
		}
	}
	return false
}

func (s *BrowserLoginSession) update(status, message string, screenshot []byte) {
	s.mu.Lock()
	s.status = status
	s.message = message
	if screenshot != nil {
		s.screenshot = base64.StdEncoding.EncodeToString(screenshot)
	}
	s.mu.Unlock()
}

func getSession(providerID string) (*BrowserLoginSession, bool) {
	browserSessionsMu.RLock()
	sess, ok := browserSessions[providerID]
	browserSessionsMu.RUnlock()
	return sess, ok
}

func cleanupSession(providerID string) {
	browserSessionsMu.Lock()
	sess, ok := browserSessions[providerID]
	if ok {
		sess.cancel()
		delete(browserSessions, providerID)
	}
	browserSessionsMu.Unlock()
	// Best-effort cleanup of the Chrome user-data dir created for this session.
	if ok && sess.userDataDir != "" {
		_ = os.RemoveAll(sess.userDataDir)
	}
}

// isExecNotFound reports whether a chromedp launch error is caused by a missing
// Chrome/Chromium executable (as opposed to a runtime crash), so the start
// handler can surface an actionable message instead of a raw exec error.
func isExecNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, probe := range []string{
		"executable file not found",
		"no such file or directory",
		"exec:",
		"chrome: not found",
		"chromium: not found",
	} {
		if strings.Contains(msg, probe) {
			return true
		}
	}
	return false
}

// ============ HTTP Handlers ============

// recoverHandler recovers from panics in a browser HTTP handler and writes a
// valid JSON error response if the handler had not yet written anything.
//
// Without this guard, a panic would let Go's default handler return an empty
// (non-JSON) 500 body, which the frontend surfaces as
// "Failed to execute 'json' on 'Response': Unexpected end of JSON input".
func recoverHandler(w http.ResponseWriter, label string) {
	if rec := recover(); rec != nil {
		slog.Error("panic recovered in browser handler", "handler", label, "error", rec)
		// Only write if nothing has been written yet (Content-Type is set by
		// writeJSON/writeError before any body is emitted).
		if w.Header().Get("Content-Type") == "" {
			writeError(w, 500, "服务器内部错误（浏览器处理异常）")
		}
	}
}

func handleBrowserLoginStart(w http.ResponseWriter, r *http.Request) {
	defer recoverHandler(w, "start")
	id := r.PathValue("id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	// Check for existing session. A terminal (error/canceled) session must NOT
	// block a retry — otherwise a single failed launch (e.g. Chrome missing on
	// the host) sticks the admin on the stale error status for the full 10-min
	// auto-cleanup window and the "浏览器登录" feature becomes unusable. Only a
	// live/active session short-circuits; a terminal one is cleared so a fresh
	// launch can proceed immediately.
	if sess, ok := getSession(id); ok {
		if sess.status == "error" || sess.status == "canceled" {
			cleanupSession(id)
		} else {
			sess.mu.Lock()
			resp := map[string]any{
				"status":     sess.status,
				"message":    sess.message,
				"screenshot": sess.screenshot,
			}
			sess.mu.Unlock()
			writeJSON(w, 200, resp)
			return
		}
	}

	browserSessionsMu.RLock()
	count := len(browserSessions)
	browserSessionsMu.RUnlock()
	if count >= 2 {
		writeError(w, 429, "已有太多浏览器会话，请先取消其他会话")
		return
	}

	p, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, "provider not found")
		return
	}

	// Resolve proxy
	proxyURL := ""
	if p.Proxy != "" {
		if strings.HasPrefix(p.Proxy, "vmess://") {
			resolved, err := ResolveProxy(p.ID, p.Proxy)
			if err != nil {
				writeError(w, 500, "代理解析失败")
				return
			}
			proxyURL = resolved
		} else {
			proxyURL = p.Proxy
		}
	}

	// Set up display environment (Xvfb on Linux, no-op on Windows)
	ensureDisplayEnvironment()

	// Prepare a dedicated, cleaned user-data dir for Chrome (avoids stale
	// profile locks that cause "chrome failed to start" on Windows).
	userDataDir, err := userDataDirFor(id)
	if err != nil {
		writeError(w, 500, "浏览器启动失败（无法准备用户目录）")
		return
	}

	// Resolve the Chrome/Chromium executable (cross-platform + env override).
	chromePath := findBrowserExecutable()
	if chromePath == "" {
		slog.Warn("Chrome executable not found via discovery; relying on chromedp default search", "provider", id)
	}

	// Chrome options — platform-aware, headless, with the flags required for
	// modern Chrome (see buildBrowserAllocatorOptions for rationale).
	opts := buildBrowserAllocatorOptions(userDataDir, proxyURL, chromePath)
	if proxyURL != "" {
		slog.Info("browser login starting", "provider", id, "proxy", proxyURL)
	} else {
		slog.Info("browser login starting", "provider", id, "proxy", "none")
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)

	sess := &BrowserLoginSession{
		ctx:         ctx,
		cancel:      func() { ctxCancel(); allocCancel() },
		providerID:  id,
		status:      "navigating",
		message:     "正在启动浏览器...",
		proxyURL:    proxyURL,
		userDataDir: userDataDir,
		createdAt:   time.Now(),
	}

	browserSessionsMu.Lock()
	browserSessions[id] = sess
	browserSessionsMu.Unlock()

	// Auto-cleanup after 10 minutes
	go func() {
		time.Sleep(10 * time.Minute)
		cleanupSession(id)
	}()

	// Start browser in goroutine
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				sess.update("error", fmt.Sprintf("浏览器异常: %v", rec), nil)
			}
		}()

		// Use sess.ctx directly - derived timeout contexts break chromedp sessions
		// Navigate to about:blank
		sess.update("navigating", "正在启动浏览器...", nil)
		err := chromedp.Run(sess.ctx, chromedp.Navigate("about:blank"))
		if err != nil {
			msg := "浏览器启动失败: " + err.Error()
			if isExecNotFound(err) {
				msg = "浏览器启动失败：未找到 Chrome/Chromium 可执行文件。请在运行 OMP 的服务器安装 Chrome 或 Chromium，或通过环境变量 OMP_CHROME_PATH 指定其路径后重试。"
			}
			sess.update("error", msg, nil)
			return
		}

		// Inject stealth JS
		_ = chromedp.Run(sess.ctx, chromedp.Evaluate(`
			Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
			Object.defineProperty(navigator, 'plugins', {get: () => [1,2,3,4,5]});
			Object.defineProperty(navigator, 'languages', {get: () => ['en-US','en']});
			window.chrome = {runtime: {}};
		`, nil))

		// Take screenshot of blank page
		buf, err := takeScreenshot(sess.ctx)
		if err != nil {
			sess.update("error", "截图失败: "+err.Error(), nil)
			return
		}

		var currentURL string
		_ = chromedp.Run(sess.ctx, chromedp.Location(&currentURL))
		sess.mu.Lock()
		sess.currentURL = currentURL
		sess.mu.Unlock()

		sess.update("ready", "浏览器已就绪，请在下方输入要访问的网址", buf)
	}()

	writeJSON(w, 200, map[string]any{
		"status":  "navigating",
		"message": "浏览器正在启动，请稍候...",
	})
}

func handleBrowserLoginStatus(w http.ResponseWriter, r *http.Request) {
	defer recoverHandler(w, "status")
	id := r.PathValue("id")
	sess, ok := getSession(id)
	if !ok {
		writeError(w, 404, "没有活跃的浏览器登录会话")
		return
	}

	sess.mu.Lock()
	resp := map[string]any{
		"status":      sess.status,
		"message":     sess.message,
		"screenshot":  sess.screenshot,
		"current_url": sess.currentURL,
	}
	sess.mu.Unlock()

	writeJSON(w, 200, resp)
}

func handleBrowserLoginLogin(w http.ResponseWriter, r *http.Request) {
	defer recoverHandler(w, "login")
	id := r.PathValue("id")
	sess, ok := getSession(id)
	if !ok {
		writeError(w, 404, "没有活跃的浏览器登录会话")
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, 400, "无效的请求")
		return
	}

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				sess.update("error", fmt.Sprintf("登录异常: %v", rec), nil)
			}
		}()

		sess.update("logging_in", "正在查找登录表单...", nil)

		emailSelectors := []string{
			`input[type="email"]`,
			`input[name="email"]`,
			`input[id="email"]`,
			`input[placeholder*="mail"]`,
			`input[placeholder*="Mail"]`,
			`input[autocomplete*="email"]`,
		}

		emailFound := fillField(sess.ctx, emailSelectors, req.Email)
		if !emailFound {
			sess.update("logging_in", "未找到邮箱输入框，尝试点击登录按钮...", nil)
			clickByText(sess.ctx, "Sign in")
			clickByText(sess.ctx, "Sign In")
			clickByText(sess.ctx, "Log in")
			clickByText(sess.ctx, "登录")
			time.Sleep(2 * time.Second)
			emailFound = fillField(sess.ctx, emailSelectors, req.Email)
		}

		if !emailFound {
			buf, _ := takeScreenshot(sess.ctx)
			sess.update("waiting_input", "未找到邮箱输入框，请使用手动操作", buf)
			return
		}

		sess.update("logging_in", "已填写邮箱，正在查找密码框...", nil)

		pwdSelectors := []string{
			`input[type="password"]`,
			`input[name="password"]`,
			`input[id="password"]`,
		}

		pwdFound := fillField(sess.ctx, pwdSelectors, req.Password)
		if !pwdFound {
			sess.update("logging_in", "未找到密码框，尝试点击继续按钮...", nil)
			clickByText(sess.ctx, "Continue")
			clickByText(sess.ctx, "Next")
			clickByText(sess.ctx, "继续")
			time.Sleep(2 * time.Second)
			pwdFound = fillField(sess.ctx, pwdSelectors, req.Password)
		}

		if !pwdFound {
			buf, _ := takeScreenshot(sess.ctx)
			sess.update("waiting_input", "未找到密码输入框，请使用手动操作", buf)
			return
		}

		sess.update("logging_in", "已填写密码，正在提交登录...", nil)

		submitClicked := false
		submitSelectors := []string{
			`button[type="submit"]`,
			`button[class*="submit"]`,
			`input[type="submit"]`,
		}
		for _, sel := range submitSelectors {
			if chromedp.Run(sess.ctx, chromedp.Click(sel, chromedp.ByQuery)) == nil {
				submitClicked = true
				break
			}
		}
		if !submitClicked {
			for _, text := range []string{"Sign in", "Sign In", "Log in", "登录", "Continue", "继续"} {
				if clickByText(sess.ctx, text) {
					break
				}
			}
		}

		time.Sleep(5 * time.Second)

		buf, err := takeScreenshot(sess.ctx)
		if err != nil {
			sess.update("error", "截图失败: "+err.Error(), nil)
			return
		}

		var currentURL string
		_ = chromedp.Run(sess.ctx, chromedp.Location(&currentURL))
		sess.mu.Lock()
		sess.currentURL = currentURL
		sess.mu.Unlock()

		sess.update("waiting_input",
			"已提交登录表单。请查看截图：如需输入验证码请用手动操作，登录成功后点击「完成并保存」",
			buf)
	}()

	writeJSON(w, 200, map[string]any{
		"status":  "logging_in",
		"message": "正在自动填写登录表单...",
	})
}

func handleBrowserLoginAction(w http.ResponseWriter, r *http.Request) {
	defer recoverHandler(w, "action")
	id := r.PathValue("id")
	sess, ok := getSession(id)
	if !ok {
		writeError(w, 404, "没有活跃的浏览器登录会话")
		return
	}

	var req struct {
		Action   string `json:"action"`
		Selector string `json:"selector"`
		Value    string `json:"value"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, 400, "无效的请求")
		return
	}

	// Use sess.ctx directly - derived timeout contexts break chromedp sessions
	var errMsg string

	switch req.Action {
	case "click":
		if err := chromedp.Run(sess.ctx,
			chromedp.Click(req.Selector, chromedp.ByQuery),
		); err != nil {
			errMsg = "点击失败: " + err.Error()
		} else {
			time.Sleep(1 * time.Second)
		}

	case "clickText":
		if !clickByText(sess.ctx, req.Selector) {
			errMsg = "未找到包含该文字的元素"
		} else {
			time.Sleep(1 * time.Second)
		}

	case "input":
		if err := chromedp.Run(sess.ctx,
			chromedp.Clear(req.Selector, chromedp.ByQuery),
			chromedp.SendKeys(req.Selector, req.Value, chromedp.ByQuery),
		); err != nil {
			errMsg = "输入失败: " + err.Error()
		}

	case "wait":
		t := 3
		if req.Value != "" {
			fmt.Sscanf(req.Value, "%d", &t)
		}
		if t > 30 {
			t = 30
		}
		time.Sleep(time.Duration(t) * time.Second)

	case "navigate":
		if !strings.HasPrefix(req.Value, "http://") && !strings.HasPrefix(req.Value, "https://") {
			errMsg = "只允许 http/https 协议"
			break
		}
		sess.update("navigating", "正在导航到 "+req.Value+" ...", nil)
		navErr := chromedp.Run(sess.ctx, chromedp.Navigate(req.Value))
		if navErr != nil {
			errMsg = "导航失败: " + navErr.Error()
		}
		// Give the page a moment to settle before the next screenshot
		time.Sleep(2 * time.Second)

	case "screenshot":
		// Just refresh screenshot

	case "scroll":
		// Scroll page up or down
		direction := req.Value
		if direction == "" {
			direction = "down"
		}
		scrollScript := "window.scrollBy(0, 400)"
		if direction == "up" {
			scrollScript = "window.scrollBy(0, -400)"
		}
		if err := chromedp.Run(sess.ctx, chromedp.Evaluate(scrollScript, nil)); err != nil {
			errMsg = "滚动失败: " + err.Error()
		}
		time.Sleep(500 * time.Millisecond)

	case "mouse":
		// Real mouse event via CDP: type=move|click|dblclick|wheel, x,y, deltaX, deltaY
		// Value format: "type,x,y" or "type,x,y,deltaX,deltaY"
		parts := strings.Split(req.Value, ",")
		if len(parts) < 3 {
			writeError(w, 400, "鼠标事件格式错误")
			return
		}
		mouseType := parts[0]
		var x, y float64
		fmt.Sscanf(parts[1], "%f", &x)
		fmt.Sscanf(parts[2], "%f", &y)

		switch mouseType {
		case "move":
			_ = chromedp.Run(sess.ctx,
				input.DispatchMouseEvent(input.MouseMoved, x, y),
			)
		case "click":
			_ = chromedp.Run(sess.ctx,
				input.DispatchMouseEvent(input.MouseMoved, x, y),
				input.DispatchMouseEvent(input.MousePressed, x, y).
					WithButton(input.Left).WithClickCount(1),
				input.DispatchMouseEvent(input.MouseReleased, x, y).
					WithButton(input.Left).WithClickCount(1),
			)
			time.Sleep(300 * time.Millisecond)
		case "dblclick":
			_ = chromedp.Run(sess.ctx,
				input.DispatchMouseEvent(input.MouseMoved, x, y),
				input.DispatchMouseEvent(input.MousePressed, x, y).
					WithButton(input.Left).WithClickCount(2),
				input.DispatchMouseEvent(input.MouseReleased, x, y).
					WithButton(input.Left).WithClickCount(2),
			)
			time.Sleep(300 * time.Millisecond)
		case "wheel":
			deltaY := -100.0
			if len(parts) > 4 {
				fmt.Sscanf(parts[4], "%f", &deltaY)
			}
			_ = chromedp.Run(sess.ctx,
				input.DispatchMouseEvent(input.MouseWheel, x, y).
					WithDeltaX(0).WithDeltaY(deltaY),
			)
			time.Sleep(300 * time.Millisecond)
		}
		// No error message for mouse events - they should be silent

	case "keyboard":
		// Keyboard event via CDP: type=text for printable chars, type=special for special keys
		// Value format: "text,<char>" or "special,<keyname>"
		parts := strings.SplitN(req.Value, ",", 2)
		if len(parts) < 2 {
			writeError(w, 400, "键盘事件格式错误")
			return
		}
		kbType := parts[0]
		kbValue := parts[1]

		switch kbType {
		case "text":
			// Insert printable character using InsertText
			_ = chromedp.Run(sess.ctx,
				input.InsertText(kbValue),
			)
		case "special":
			// Handle special keys via DispatchKeyEvent
			var keyCode int64
			var code string
			switch kbValue {
			case "Enter":
				keyCode = 13
				code = "Enter"
			case "Backspace":
				keyCode = 8
				code = "Backspace"
			case "Tab":
				keyCode = 9
				code = "Tab"
			case "Escape":
				keyCode = 27
				code = "Escape"
			case "ArrowUp":
				keyCode = 38
				code = "ArrowUp"
			case "ArrowDown":
				keyCode = 40
				code = "ArrowDown"
			case "ArrowLeft":
				keyCode = 37
				code = "ArrowLeft"
			case "ArrowRight":
				keyCode = 39
				code = "ArrowRight"
			case "Space":
				keyCode = 32
				code = "Space"
				kbValue = " "
			default:
				keyCode = 0
				code = kbValue
			}
			if keyCode == 32 {
				// Space - use char type
				_ = chromedp.Run(sess.ctx,
					input.DispatchKeyEvent(input.KeyChar).WithText(" "),
				)
			} else {
				_ = chromedp.Run(sess.ctx,
					input.DispatchKeyEvent(input.KeyDown).
						WithKey(kbValue).WithCode(code).
						WithWindowsVirtualKeyCode(keyCode),
					input.DispatchKeyEvent(input.KeyUp).
						WithKey(kbValue).WithCode(code).
						WithWindowsVirtualKeyCode(keyCode),
				)
			}
		}
		time.Sleep(100 * time.Millisecond)

	default:
		writeError(w, 400, "未知操作: "+req.Action)
		return
	}

	// Take screenshot after action
	buf, err := takeScreenshot(sess.ctx)
	if err != nil {
		sess.update("error", "截图失败: "+err.Error(), nil)
		writeError(w, 500, "截图失败")
		return
	}

	var currentURL string
	_ = chromedp.Run(sess.ctx, chromedp.Location(&currentURL))
	sess.mu.Lock()
	sess.currentURL = currentURL
	sess.mu.Unlock()

	msg := "操作完成"
	if errMsg != "" {
		msg = errMsg + "（截图已刷新）"
	}
	sess.update("ready", msg, buf)

	sess.mu.Lock()
	resp := map[string]any{
		"status":      sess.status,
		"message":     sess.message,
		"screenshot":  sess.screenshot,
		"current_url": sess.currentURL,
	}
	sess.mu.Unlock()

	writeJSON(w, 200, resp)
}

func handleBrowserLoginFinish(w http.ResponseWriter, r *http.Request) {
	defer recoverHandler(w, "finish")
	id := r.PathValue("id")
	sess, ok := getSession(id)
	if !ok {
		writeError(w, 404, "没有活跃的浏览器登录会话")
		return
	}

	// Use sess.ctx directly
	// Get all cookies
	var cookies []*network.Cookie
	err := chromedp.Run(sess.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		c, err := network.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		cookies = c
		return nil
	}))

	if err != nil {
		writeError(w, 500, "获取Cookie失败")
		return
	}

	// Get User-Agent
	var userAgent string
	_ = chromedp.Run(sess.ctx, chromedp.Evaluate(`navigator.userAgent`, &userAgent))

	// Get current URL
	var currentURL string
	_ = chromedp.Run(sess.ctx, chromedp.Location(&currentURL))

	// Build cookie string and extract token
	var cookieParts []string
	var tokenValue string
	for _, c := range cookies {
		if c.Value == "" {
			continue
		}
		cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", c.Name, c.Value))
		if c.Name == "token" {
			tokenValue = c.Value
		}
	}

	// Process token value → extract raw API key
	apiKey := ""
	if tokenValue != "" {
		decoded, err := url.QueryUnescape(tokenValue)
		if err != nil {
			decoded = tokenValue
		}
		if strings.HasPrefix(decoded, "Bearer ") {
			apiKey = strings.TrimPrefix(decoded, "Bearer ")
		} else if strings.HasPrefix(decoded, "bearer ") {
			apiKey = strings.TrimPrefix(decoded, "bearer ")
		} else {
			apiKey = decoded
		}
	}

	// Build ExtraCookies (exclude token and refresh_token)
	var filteredParts []string
	for _, c := range cookies {
		if c.Name == "token" || c.Name == "refresh_token" || c.Value == "" {
			continue
		}
		filteredParts = append(filteredParts, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}
	extraCookies := strings.Join(filteredParts, "; ")

	// Save to provider
	p, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, "provider not found")
		return
	}

	if apiKey != "" {
		p.APIKey = apiKey
		if len(p.APIKeys) > 0 {
			p.APIKeys[0].Key = apiKey
			p.APIKeys[0].Enabled = true
		} else {
			p.APIKeys = []APIKeyConfig{
				{
					ID:            "key-" + id + "-1",
					Key:           apiKey,
					Alias:         "浏览器登录获取",
					AccessControl: "private",
					Priority:      1,
					Enabled:       true,
				},
			}
		}
	}

	if p.WebSession != nil {
		p.WebSession.ExtraCookies = extraCookies
		if userAgent != "" && p.WebSession.ExtraHeaders != nil {
			p.WebSession.ExtraHeaders["User-Agent"] = userAgent
		}
	}

	pm.Add(p)
	cleanupSession(id)

	go safeCheckProviderNow(id)

	slog.Info("browser login completed",
		"provider", id,
		"cookies", len(cookies),
		"has_token", apiKey != "",
		"url", currentURL)

	masked := ""
	if apiKey != "" {
		if len(apiKey) > 12 {
			masked = apiKey[:6] + "..." + apiKey[len(apiKey)-4:]
		} else {
			masked = "***"
		}
	}

	writeJSON(w, 200, map[string]any{
		"status":   "done",
		"message":  fmt.Sprintf("登录完成！已保存 %d 个 Cookie，Token: %s", len(cookies), masked),
		"cookies":  len(cookies),
		"hasToken": apiKey != "",
	})
}

func handleBrowserLoginCancel(w http.ResponseWriter, r *http.Request) {
	defer recoverHandler(w, "cancel")
	id := r.PathValue("id")
	cleanupSession(id)
	writeJSON(w, 200, map[string]any{
		"status":  "cancelled",
		"message": "浏览器登录会话已取消",
	})
}
