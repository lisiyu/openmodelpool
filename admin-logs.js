// admin-logs.js — Request logs and SSE connection

// ===== Request Logs =====
// ================================================================
// Module: LogsAndHealth - Request logs and health status display
// ================================================================

// _logsFilter is the current model/provider filter text (UX-P1-7).
let _logsFilter = '';
// _logsLimit caps rows rendered per page (UX-P1-7).
const _logsPageSize = 200;

async function refreshLogs() {
  const container = document.getElementById('logList');
  if (!container) return;
  try {
    const r = await authFetch('/api/logs');
    const d = await r.json();
    const logs = d.logs || [];
    if (!logs.length) {
      container.innerHTML = '<div style="text-align:center;color:var(--text-muted);padding:20px">暂无请求日志</div>';
      return;
    }
    // UX-P1-8: render the error column only when at least one log carries an
    // error message, so healthy rows stay compact.
    const showError = logs.some(function(l) { return l.error; });
    const kw = _logsFilter.toLowerCase();
    const filtered = kw ? logs.filter(function(l) {
      return String(l.model || '').toLowerCase().indexOf(kw) >= 0 ||
             String(l.provider_name || '').toLowerCase().indexOf(kw) >= 0;
    }) : logs;

    let html = '<div style="font-size:11px;color:var(--text-muted);margin-bottom:8px">共 ' + logs.length + ' 条日志';
    if (filtered.length !== logs.length) html += '，筛选后 ' + filtered.length + ' 条';
    if (filtered.length > _logsPageSize) html += '（仅显示前 ' + _logsPageSize + ' 条）';
    html += '</div>';
    html += '<table class="usage-table"><thead><tr><th>时间</th><th>模型</th><th>平台</th><th>Token</th><th>延迟</th><th>状态</th>' + (showError ? '<th>错误</th>' : '') + '</tr></thead><tbody>';
    const page = filtered.slice(0, _logsPageSize);
    for (const l of page) {
      const statusBadge = l.success ? '<span class="badge badge-green">成功</span>' : '<span class="badge badge-red">失败</span>';
      const time = l.timestamp ? new Date(l.timestamp).toLocaleString('zh-CN',{hour:'2-digit',minute:'2-digit',second:'2-digit'}) : '-';
      const errCell = showError ? '<td style="color:var(--danger,#e5484d);font-size:12px;max-width:240px;word-break:break-all">' + escapeHtml(l.error || '') + '</td>' : '';
      html += `<tr><td>${time}</td><td>${escapeHtml(l.model||'-')}</td><td>${escapeHtml(l.provider_name||'-')}</td><td>${escapeHtml(l.tokens||0)}</td><td>${escapeHtml(l.latency_ms||0)}ms</td><td>${statusBadge}</td>${errCell}</tr>`;
    }
    html += '</tbody></table>';
    container.innerHTML = html;
  } catch(e) {
    // UX-P1-7: make load failures visible with a retry action.
    container.innerHTML =
      '<div style="text-align:center;color:var(--danger,#e5484d);padding:20px">加载日志失败: ' + escapeHtml(e.message || '未知错误') + '</div>' +
      '<div style="text-align:center"><button class="btn btn-secondary" onclick="refreshLogs()">重试</button></div>';
  }
}

// filterLogs is bound to the log filter input (UX-P1-7).
function filterLogs(value) {
  _logsFilter = (value || '').trim();
  refreshLogs();
}


(function connectSSE() {
  if (!authToken) return;
  try {
    const es = new EventSource('/events');
    es.addEventListener('health_change', (e) => {
      try { const d = JSON.parse(e.data); refreshHealth(); toast(`Health: ${d.data.provider_id} → ${d.data.new_status}`, 'info'); } catch(_) {}
    });
    es.addEventListener('config_update', () => { loadStatus(); });
    es.addEventListener('provider_status', () => { loadProviders(); refreshHealth(); });
    es.onerror = () => { es.close(); setTimeout(connectSSE, 5000); };
  } catch(_) {}
})();

