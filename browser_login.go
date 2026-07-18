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
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// startXvfb ensures Xvfb is running on display :99
func startXvfb() {
	// Check if display :99 is accessible
	if err := exec.Command("xdpyinfo", "-display", ":99").Run(); err == nil {
		return // Xvfb already running
	}
	// Clean up stale lock files
	os.Remove("/tmp/.X99-lock")
	os.Remove("/tmp/.X11-unix/X99")
	// Start Xvfb
	cmd := exec.Command("Xvfb", ":99", "-screen", "0", "1280x800x24", "-ac")
	cmd.Start()
	time.Sleep(2 * time.Second)
	slog.Info("Xvfb started", "display", ":99", "pid", cmd.Process.Pid)
}

// BrowserLoginSession manages a Chrome session for provider login
type BrowserLoginSession struct {
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	providerID string
	status     string
	screenshot string
	message    string
	currentURL string
	proxyURL   string
	createdAt  time.Time
}

var (
	browserSessions   = make(map[string]*BrowserLoginSession)
	browserSessionsMu sync.RWMutex
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
}

// ============ HTTP Handlers ============

func handleBrowserLoginStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	// Check for existing session
	if sess, ok := getSession(id); ok {
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
				writeError(w, 500, "代理解析失败: "+err.Error())
				return
			}
			proxyURL = resolved
		} else {
			proxyURL = p.Proxy
		}
	}

	// Start Xvfb for non-headless mode
	startXvfb()
	os.Setenv("DISPLAY", ":99")

	// Chrome options — non-headless with Xvfb to bypass Cloudflare detection
	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "AutomationControlled"),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("enable-automation", false),
		chromedp.WindowSize(1280, 800),
	}
	if proxyURL != "" {
		opts = append(opts, chromedp.ProxyServer(proxyURL))
		// Force DNS through proxy to prevent leaks; only when proxy is active
		opts = append(opts, chromedp.Flag("host-resolver-rules", "MAP * ~NOTFOUND, EXCLUDE 127.0.0.1"))
		slog.Info("browser login starting", "provider", id, "proxy", proxyURL)
	} else {
		slog.Info("browser login starting", "provider", id, "proxy", "none")
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)

	sess := &BrowserLoginSession{
		ctx:        ctx,
		cancel:     func() { ctxCancel(); allocCancel() },
		providerID: id,
		status:     "navigating",
		message:    "正在启动浏览器...",
		proxyURL:   proxyURL,
		createdAt:  time.Now(),
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
			sess.update("error", "浏览器启动失败: "+err.Error(), nil)
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
	if err := readJSON(r, &req); err != nil {
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
					submitClicked = true
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
	if err := readJSON(r, &req); err != nil {
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
		// Use setTimeout for async navigation (doesn't block CDP connection)
		sess.update("navigating", "正在导航到 "+req.Value+" ...", nil)
		navErr := chromedp.Run(sess.ctx, chromedp.Evaluate(`setTimeout(function(){window.location.href="`+req.Value+`"},0)`, nil))
		if navErr != nil {
			errMsg = "导航失败: " + navErr.Error()
		}
		// Wait for page to load regardless
		time.Sleep(10 * time.Second)

	case "screenshot":
		// Just refresh screenshot

	default:
		writeError(w, 400, "未知操作: "+req.Action)
		return
	}

	// Take screenshot after action
	buf, err := takeScreenshot(sess.ctx)
	if err != nil {
		sess.update("error", "截图失败: "+err.Error(), nil)
		writeError(w, 500, "截图失败: "+err.Error())
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
		writeError(w, 500, "获取Cookie失败: "+err.Error())
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

	go healthChecker.CheckProviderNow(id)

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
	id := r.PathValue("id")
	cleanupSession(id)
	writeJSON(w, 200, map[string]any{
		"status":  "cancelled",
		"message": "浏览器登录会话已取消",
	})
}
