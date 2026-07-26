// admin-common.js - Shared utilities for all admin pages

// ============================================================
// XSS escaping helpers (security baseline)
// ============================================================
// escapeHtml escapes a value for safe insertion as HTML text or inside an
// element. All API-returned / user-controlled strings rendered via innerHTML
// MUST go through this function.
function escapeHtml(s) {
  if (s === null || s === undefined) return '';
  return String(s).replace(/[&<>"']/g, function(c) {
    return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];
  });
}
// escapeAttr escapes a value for use inside a double-quoted HTML attribute
// (e.g. href="..." or src="...").
function escapeAttr(s) { return escapeHtml(s); }
// escapeJS escapes a value for safe embedding inside a single-quoted
// JavaScript string literal (e.g. onclick="someFunc('...')").
function escapeJS(s) {
  if (s === null || s === undefined) return '';
  return String(s).replace(/[\\'"`\n\r\t]/g, function(c){ return '\\' + c; });
}

// safeUrl validates a URL before it is placed into an HTML href/src attribute.
// Only http:, https:, mailto:, and same-origin relative links (starting with
// '/', '#', or './') are allowed. Any dangerous scheme (e.g. javascript:,
// data:, vbscript:) is rejected and replaced with '#' to prevent XSS.
function safeUrl(url) {
  if (url === null || url === undefined) return '#';
  const s = String(url).trim();
  if (s === '') return '#';
  if (s.startsWith('/') || s.startsWith('#') || s.startsWith('./')) {
    return escapeAttr(s);
  }
  const lower = s.toLowerCase();
  if (lower.startsWith('http://') || lower.startsWith('https://') || lower.startsWith('mailto:')) {
    return escapeAttr(s);
  }
  return '#';
}

// validatePeerAddress validates a peer / seed address entered by the operator.
// Only http:// and https:// schemes are accepted (matches the backend
// resolvePeerNodeID scheme check), preventing SSRF-prone or malformed input.
function validatePeerAddress(url) {
  if (typeof url !== 'string') return false;
  const s = url.trim();
  if (s === '') return false;
  const lower = s.toLowerCase();
  return lower.startsWith('http://') || lower.startsWith('https://');
}

// copyToClipboard copies text to the clipboard and shows a toast on success.
// Falls back gracefully if the Clipboard API is unavailable (e.g. non-secure
// context) by selecting a temporary textarea.
function copyToClipboard(text) {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function() {
        toast('已复制到剪贴板', 'success');
      }, function() {
        toast('复制失败，请手动复制', 'error');
      });
      return true;
    }
  } catch (e) { /* fall through to legacy path */ }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    toast(ok ? '已复制到剪贴板' : '复制失败，请手动复制', ok ? 'success' : 'error');
    return ok;
  } catch (e) {
    toast('复制失败，请手动复制', 'error');
    return false;
  }
}

let authToken = localStorage.getItem('admin_token') || '';

async function authFetch(url, opts = {}) {
  opts.headers = { ...opts.headers, 'Authorization': 'Bearer ' + authToken, 'Content-Type': 'application/json' };
  const r = await fetch(url, opts);
  if (r.status === 401) {
    localStorage.removeItem('admin_token');
    toast('login expired', 'error');
    setTimeout(function() { location.href = '/login'; }, 1500);
    throw new Error('not logged in');
  }
  return r;
}

function logout() { localStorage.removeItem('admin_token'); location.href = '/login'; }

function toast(msg, type) {
  type = type || 'success';
  var t = document.createElement('div');
  t.className = 'toast ' + type;
  t.textContent = msg;
  document.body.appendChild(t);
  setTimeout(function() { t.remove(); }, 3000);
}

function extractError(d) {
  if (!d) return 'unknown error';
  if (typeof d.error === 'string') return d.error;
  if (d.error && typeof d.error === 'object') return d.error.message || d.error.type || JSON.stringify(d.error);
  if (d.detail) return typeof d.detail === 'string' ? d.detail : JSON.stringify(d.detail);
  return 'unknown error';
}

function formatTokens(n) {
  if (n >= 1e9) return (n / 1e9).toFixed(2) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return String(n);
}

function formatTime(isoStr) {
  if (!isoStr) return '-';
  var d = new Date(isoStr);
  return d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

function getQueryParam(name) {
  var params = new URLSearchParams(window.location.search);
  return params.get(name);
}

function getIcon(id) {
  var icons = {
    openai: '\u{1F916}', deepseek: '\u{1F40B}', qwen: '\u{1F984}', sider: '\u{1F9E0}', nvidia: '\u{1F3AE}',
    cloudflare: '\u2601\uFE0F', huggingface: '\u{1F917}', chutes: '\u{1F3AF}', vllm: '\u26A1',
    gemini: '\u{1F48E}', claude: '\u{1F3AD}', mistral: '\u{1F32C}\uFE0F', groq: '\u{1F680}', together: '\u{1F91D}',
    openrouter: '\u{1F500}', fireworks: '\u{1F386}', perplexity: '\u{1F50E}', cohere: '\u{1F517}',
    yi: '\u{1F985}', moonshot: '\u{1F319}', zhipu: '\u2728', baichuan: '\u{1F3D4}\uFE0F', minimax: '\u{1F4CF}',
    spark: '\u2728', elyza: '\u{1F31F}', sakura: '\u{1F338}', kimi: '\u{1F319}', doubao: '\u{1FAC8}',
  };
  return icons[id] || '\u{1F50C}';
}
