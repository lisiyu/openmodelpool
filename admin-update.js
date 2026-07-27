// admin-update.js - One-click version update UI for the admin page.
//
// Responsibilities (per PRD P0-1 / P0-3 and ARCH §3):
//   * Poll GET /api/admin/version/latest every 5 minutes and render a
//     three-state update button (disabled / "已是最新" / "更新到 vX.Y.Z").
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
  // Version card (three-state button)
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

    var cur = escapeHtml(info.current_version || _currentVersion || '-');
    var latest = escapeHtml(info.latest_version || '-');
    var checked = formatTime(info.checked_at);

    var btn;
    if (inFlight) {
      btn = '<button class="btn btn-secondary" disabled style="opacity:.6">更新进行中…</button>';
    } else if (info.has_update) {
      btn = '<button class="btn btn-primary" onclick="startVersionUpdate()">🔄 更新到 ' + latest + '</button>';
    } else {
      btn = '<button class="btn btn-secondary" disabled style="opacity:.6">✅ 已是最新版本</button>';
    }

    el.innerHTML =
      '<div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:12px">' +
        '<div>' +
          '<div style="font-size:13px;color:var(--text-secondary)">当前版本：<b style="color:var(--text-primary)">' + cur + '</b></div>' +
          '<div style="font-size:13px;color:var(--text-secondary);margin-top:4px">最新版本：<b style="color:var(--text-primary)">' + latest + '</b>' +
            (info.has_update ? ' <span style="color:var(--warning,#f5a623)">● 有新版本</span>' : '') + '</div>' +
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
      if (r.ok) {
        var d = await r.json();
        _versionInfo = d;
      }
    } catch (e) {
      // Keep previous info on transient failure.
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
