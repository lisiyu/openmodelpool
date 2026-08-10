// admin-update.js - One-click version update UI for the admin page.
//
// Responsibilities (per PRD P0-1 / P0-3 and ARCH §3):
//   * Poll GET /api/admin/version/latest every 5 minutes and render a
//     four-state update button (更新进行中 / "更新到 vX.Y.Z" / "已是最新" /
//     "无需更新" when the local build is ahead of the newest published release).
//   * On click, POST /api/admin/update/start, then poll
//     GET /api/admin/update/status and aggregate local + peer progress.
//   * While any node is mid-update, poll status every ~2.5s; otherwise
//     fall back to a slow 15s poll.
//
// All dynamic strings are rendered through escapeHtml() (from admin-common.js)
// to preserve the XSS-safe baseline.

(function () {
  'use strict';

  // -------------------------------------------------------------------------
  // Module state
  // -------------------------------------------------------------------------
  var _versionInfo = null;     // last VersionInfo from /api/admin/version/latest
  var _statusList = [];        // UpdateStatus[] from /api/admin/update/status
  var _currentVersion = '';    // server-reported current version
  var _versionTimer = null;    // 5-minute version poll
  var _statusTimer = null;     // adaptive status poll (recursive setTimeout)

  var INFLOW_PHASES = ['downloading', 'replacing', 'restarting'];

  // -------------------------------------------------------------------------
  // Phase presentation
  // -------------------------------------------------------------------------
  function phaseLabel(p) {
    switch (p) {
      case 'idle': return '空闲';
      case 'downloading': return '下载中';
      case 'replacing': return '替换中';
      case 'restarting': return '重启中';
      case 'success': return '已完成';
      case 'failed': return '失败';
      case 'unsupported': return '不支持';
      case 'needs_manual_restart': return '需手动重启';
      default: return p || '未知';
    }
  }

  // Returns {bg, fg, bar} color hints for a phase.
  function phaseStyle(p) {
    switch (p) {
      case 'success':
        return { bg: 'rgba(63,185,80,.15)', fg: 'var(--success,#3fb950)', bar: 'var(--success,#3fb950)' };
      case 'failed':
        return { bg: 'rgba(229,72,77,.15)', fg: 'var(--danger,#e5484d)', bar: 'var(--danger,#e5484d)' };
      case 'unsupported':
        return { bg: 'rgba(139,148,158,.15)', fg: 'var(--text-muted,#8b949e)', bar: 'var(--text-muted,#8b949e)' };
      case 'needs_manual_restart':
        return { bg: 'rgba(245,166,35,.15)', fg: 'var(--warning,#f5a623)', bar: 'var(--warning,#f5a623)' };
      case 'downloading':
      case 'replacing':
      case 'restarting':
        return { bg: 'rgba(88,166,255,.15)', fg: 'var(--primary,#58a6ff)', bar: 'var(--primary,#58a6ff)' };
      default:
        return { bg: 'rgba(139,148,158,.12)', fg: 'var(--text-muted,#8b949e)', bar: 'var(--text-muted,#8b949e)' };
    }
  }

  function anyInFlight() {
    if (!_statusList) return false;
    for (var i = 0; i < _statusList.length; i++) {
      if (INFLOW_PHASES.indexOf(_statusList[i].phase) !== -1) return true;
    }
    return false;
  }

  // -------------------------------------------------------------------------
  // Version string helpers
  //
  // The backend only exposes a boolean `has_update`, which cannot distinguish
  // "local == remote" from "local is NEWER than the newest published release"
  // (the latter happens whenever a version bump is committed without a git
  // tag, so no GitHub Release exists yet). We therefore re-compare locally.
  //
  // NOTE: comparison MUST be numeric per segment. A naive string compare would
  // report "4.3.9" > "4.3.15", which is exactly the class of bug this fixes.
  // -------------------------------------------------------------------------

  // Splits a version string into an array of non-negative integers.
  // Tolerates an optional leading "v"/"V" and a -prerelease / +build suffix.
  // Returns null when the input is empty or not a numeric dotted version, so
  // that callers can fall back to a neutral, non-misleading presentation.
  function parseVersionParts(v) {
    var s = (v === null || v === undefined) ? '' : String(v).trim();
    if (!s) return null;
    if (s.charAt(0) === 'v' || s.charAt(0) === 'V') s = s.slice(1);

    // Drop SemVer pre-release / build metadata: "4.3.15-rc.1+abc" -> "4.3.15".
    var cut = s.search(/[-+]/);
    if (cut !== -1) s = s.slice(0, cut);
    s = s.trim();
    if (!s) return null;

    var raw = s.split('.');
    var parts = [];
    for (var i = 0; i < raw.length; i++) {
      var seg = raw[i].trim();
      if (!/^[0-9]+$/.test(seg)) return null;   // non-numeric segment -> unknown
      var n = parseInt(seg, 10);
      if (isNaN(n)) return null;
      parts.push(n);
    }
    return parts.length > 0 ? parts : null;
  }

  // Compares two version strings segment-by-segment as integers.
  // Returns 1 (a > b), 0 (a == b), -1 (a < b), or NaN when either side is
  // empty / unparseable. Missing trailing segments count as 0, so
  // "4.3" == "4.3.0".
  function compareVersionStr(a, b) {
    var pa = parseVersionParts(a);
    var pb = parseVersionParts(b);
    if (!pa || !pb) return NaN;

    var n = Math.max(pa.length, pb.length);
    for (var i = 0; i < n; i++) {
      var x = i < pa.length ? pa[i] : 0;
      var y = i < pb.length ? pb[i] : 0;
      if (x > y) return 1;
      if (x < y) return -1;
    }
    return 0;
  }

  // Display-only helper: ensures exactly one leading "v" so the explanatory
  // copy reads consistently even when the tag and AppVersion disagree on the
  // prefix (GitHub tags are "v4.3.9", main.go AppVersion is "4.3.15").
  // Never feed the result back into a comparison.
  function vLabel(s) {
    var t = (s === null || s === undefined) ? '' : String(s).trim();
    if (!t) return '-';
    return (t.charAt(0) === 'v' || t.charAt(0) === 'V') ? t : 'v' + t;
  }

  // -------------------------------------------------------------------------
  // Version card (four-state button)
  // -------------------------------------------------------------------------
  function renderVersionBody() {
    var el = document.getElementById('versionUpdateBody');
    if (!el) return;
    var info = _versionInfo;
    var inFlight = anyInFlight();

    if (!info) {
      el.innerHTML = '<div style="color:var(--text-muted);font-size:13px">正在检查版本…</div>';
      return;
    }
    if (info.error) {
      el.innerHTML =
        '<div style="color:var(--danger,#e5484d);font-size:13px">版本检查失败：' + escapeHtml(info.error) + '</div>' +
        '<button class="btn btn-secondary" style="margin-top:8px" onclick="refreshVersion()">重试</button>';
      return;
    }

    var rawCur = info.current_version || _currentVersion || '';
    var rawLatest = info.latest_version || '';
    var cur = escapeHtml(rawCur || '-');
    var latest = escapeHtml(rawLatest || '-');
    var checked = formatTime(info.checked_at);

    // Only re-compare when the backend says there is nothing to update to.
    // cmp === 1  -> the running build is ahead of the newest published release
    //               (version bumped but never tagged, so no Release exists).
    // cmp === 0  -> genuinely up to date.
    // NaN        -> unknown (missing / unparseable version): stay neutral and
    //               keep the pre-existing "已是最新版本" wording.
    var cmp = info.has_update ? -1 : compareVersionStr(rawCur, rawLatest);
    var localAhead = (!info.has_update && cmp === 1);

    var btn;
    if (inFlight) {
      btn = '<button class="btn btn-secondary" disabled style="opacity:.6">更新进行中…</button>';
    } else if (info.has_update) {
      btn = '<button class="btn btn-primary" onclick="startVersionUpdate()">🔄 更新到 ' + latest + '</button>';
    } else if (localAhead) {
      btn = '<button class="btn btn-secondary" disabled style="opacity:.6">✅ 无需更新</button>';
    } else if (isNaN(cmp)) {
      // UX-P1-10: unknown/unparseable latest version — do NOT claim "已是最新
      // 版本"; offer a manual re-check instead.
      btn = '<button class="btn btn-secondary" onclick="refreshVersion()">🔄 重新检查</button>';
    } else {
      btn = '<button class="btn btn-secondary" disabled style="opacity:.6">✅ 已是最新版本</button>';
    }

    // The "latest" line differs per state so the user is never left thinking
    // the panel is offering them a downgrade.
    var latestLine;
    if (localAhead) {
      latestLine =
        '<div style="font-size:13px;color:var(--text-secondary);margin-top:4px">已发布最新版本：' +
          '<b style="color:var(--text-primary)">' + latest + '</b>' +
          ' <span style="color:var(--success,#3fb950)">● 已是最新</span></div>' +
        '<div style="font-size:11px;color:var(--text-muted);margin-top:4px;line-height:1.5">' +
          '本地版本 <b style="color:var(--text-secondary)">' + escapeHtml(vLabel(rawCur)) + '</b> ' +
          '领先于已发布的 <b style="color:var(--text-secondary)">' + escapeHtml(vLabel(rawLatest)) + '</b>' +
          '（该版本尚未发布），无需操作。</div>';
    } else {
      // marker: warning when an update exists, success only when the two
      // versions are *confirmed* equal. When cmp is NaN (missing/unparseable
      // latest_version, e.g. GitHub degraded) we show no marker at all, which
      // is byte-identical to the previous fallback rendering.
      var marker = '';
      if (info.has_update) {
        marker = ' <span style="color:var(--warning,#f5a623)">● 有新版本</span>';
      } else if (cmp === 0) {
        marker = ' <span style="color:var(--success,#3fb950)">● 已是最新</span>';
      }
      latestLine =
        '<div style="font-size:13px;color:var(--text-secondary);margin-top:4px">最新版本：' +
          '<b style="color:var(--text-primary)">' + latest + '</b>' + marker + '</div>';
    }

    el.innerHTML =
      '<div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:12px">' +
        '<div>' +
          '<div style="font-size:13px;color:var(--text-secondary)">当前版本：<b style="color:var(--text-primary)">' + cur + '</b></div>' +
          latestLine +
          '<div style="font-size:11px;color:var(--text-muted);margin-top:4px">检查时间：' + checked + '</div>' +
        '</div>' +
        '<div>' + btn + '</div>' +
      '</div>';
  }

  // -------------------------------------------------------------------------
  // Status aggregation (local + peers)
  // -------------------------------------------------------------------------
  function statusRowHTML(s) {
    var label = phaseLabel(s.phase);
    var st = phaseStyle(s.phase);
    var pct = Math.max(0, Math.min(100, Number(s.progress) || 0));

    var name = escapeHtml(s.name || s.env || s.node_id || '未知节点');
    var roleTag = s.is_local ? '本机' : '对端';
    var target = escapeHtml(s.target_version || '-');

    var detail = escapeHtml(s.log || '');
    if (s.error) {
      detail = (detail ? detail + ' · ' : '') + '错误：' + escapeHtml(s.error);
    }

    // If the local node's recorded target version disagrees with the version
    // the server is actually running, surface it instead of silently trusting
    // a stale "updated to vX" record (e.g. after an external deploy).
    var mismatchWarn = '';
    if (s.is_local && s.target_version && _currentVersion && s.target_version !== _currentVersion) {
      mismatchWarn = '<div style="margin-top:6px;font-size:11px;color:var(--warning,#f5a623)">⚠️ 记录的更新目标 v' +
        escapeHtml(s.target_version) + ' 与当前运行 v' + escapeHtml(_currentVersion) +
        ' 不一致（可能经外部部署）</div>';
    }

    return '' +
      '<div style="border:1px solid var(--border-color);border-radius:8px;padding:10px;margin-bottom:8px">' +
        '<div style="display:flex;justify-content:space-between;align-items:center;gap:8px">' +
          '<div style="font-size:13px"><b style="color:var(--text-primary)">' + name + '</b> ' +
            '<span style="font-size:11px;color:var(--text-muted)">' + roleTag + ' · 目标 ' + target + '</span></div>' +
          '<div style="font-size:11px;padding:2px 8px;border-radius:10px;background:' + st.bg + ';color:' + st.fg + '">' + label + '</div>' +
        '</div>' +
        '<div style="margin-top:8px;height:6px;background:var(--bg-primary,#0d1117);border-radius:3px;overflow:hidden">' +
          '<div style="height:100%;width:' + pct + '%;background:' + st.bar + ';transition:width .4s"></div>' +
        '</div>' +
        (detail ? '<div style="margin-top:6px;font-size:11px;color:var(--text-muted);line-height:1.5">' + detail + '</div>' : '') +
        mismatchWarn +
      '</div>';
  }

  function renderStatusArea() {
    var el = document.getElementById('updateStatusArea');
    if (!el) return;
    if (!_statusList || _statusList.length === 0) {
      el.innerHTML = '';
      return;
    }

    var total = _statusList.length;
    var done = 0, inflight = 0, failed = 0, unsupported = 0;
    for (var i = 0; i < total; i++) {
      switch (_statusList[i].phase) {
        case 'success': done++; break;
        case 'failed': failed++; break;
        case 'unsupported': unsupported++; break;
        case 'downloading':
        case 'replacing':
        case 'restarting': inflight++; break;
      }
    }

    var summary = '共 ' + total + ' 个节点';
    if (inflight > 0) summary += ' · <span style="color:var(--primary,#58a6ff)">' + inflight + ' 进行中</span>';
    if (done > 0) summary += ' · <span style="color:var(--success,#3fb950)">' + done + ' 已完成</span>';
    if (failed > 0) summary += ' · <span style="color:var(--danger,#e5484d)">' + failed + ' 失败</span>';
    if (unsupported > 0) summary += ' · <span style="color:var(--text-muted,#8b949e)">' + unsupported + ' 不支持</span>';

    var rows = _statusList.map(statusRowHTML).join('');
    el.innerHTML =
      '<div style="font-size:12px;color:var(--text-muted);margin-bottom:10px">更新进度（本机 + 联邦对端）：' + summary + '</div>' +
      rows;
  }

  // -------------------------------------------------------------------------
  // Data fetchers
  // -------------------------------------------------------------------------
  async function refreshVersion() {
    try {
      var r = await authFetch('/api/admin/version/latest');
      var d = await r.json();
      _versionInfo = d;
    } catch (e) {
      // UX-P1-10: don't silently keep a stale "已是最新版本" — surface the
      // failure with a retry action.
      _versionInfo = { error: (e && e.message) || '网络错误' };
    }
    renderVersionBody();
  }

  async function refreshStatus() {
    try {
      var r = await authFetch('/api/admin/update/status');
      if (r.ok) {
        var d = await r.json();
        _statusList = d.statuses || [];
        if (d.current_version) _currentVersion = d.current_version;
      }
    } catch (e) {
      // Keep previous status on transient failure.
    }
    renderStatusArea();
  }

  // Adaptive status polling: fast (2.5s) while in flight, slow (15s) otherwise.
  function scheduleStatusPoll(fast) {
    if (_statusTimer) clearTimeout(_statusTimer);
    var delay = fast ? 2500 : 15000;
    _statusTimer = setTimeout(async function () {
      await refreshStatus();
      scheduleStatusPoll(anyInFlight());
    }, delay);
  }

  // -------------------------------------------------------------------------
  // Actions
  // -------------------------------------------------------------------------
  async function startVersionUpdate() {
    try {
      var r = await authFetch('/api/admin/update/start', { method: 'POST' });
      if (!r.ok) {
        var d = await r.json().catch(function () { return {}; });
        toast(extractError(d), 'error');
        return;
      }
      toast('已启动更新，正在下载并重启…', 'success');
      await refreshStatus();
      scheduleStatusPoll(true);
    } catch (e) {
      toast('启动更新失败：' + (e && e.message ? e.message : e), 'error');
    }
  }

  // -------------------------------------------------------------------------
  // Init
  // -------------------------------------------------------------------------
  function initVersionUpdate() {
    refreshVersion();
    refreshStatus();
    if (_versionTimer) clearInterval(_versionTimer);
    _versionTimer = setInterval(refreshVersion, 5 * 60 * 1000);
    scheduleStatusPoll(false);
  }

  // Expose entry points used by inline onclick handlers.
  window.startVersionUpdate = startVersionUpdate;
  window.refreshVersion = refreshVersion;
  window.refreshUpdateStatus = refreshStatus;

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initVersionUpdate);
  } else {
    initVersionUpdate();
  }
})();
