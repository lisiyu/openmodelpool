// admin-logs.js — Request logs and SSE connection

// ===== Request Logs =====
// ================================================================
// Module: LogsAndHealth - Request logs and health status display
// ================================================================
async function refreshLogs() {
  try {
    const r = await authFetch('/api/logs');
    const d = await r.json();
    const logs = d.logs || [];
    const container = document.getElementById('logList');
    if (!logs.length) {
      container.innerHTML = '<div style="text-align:center;color:var(--text-muted);padding:20px">暂无请求日志</div>';
      return;
    }
    let html = '<table class="usage-table"><thead><tr><th>时间</th><th>模型</th><th>平台</th><th>Token</th><th>延迟</th><th>状态</th></tr></thead><tbody>';
    for (const l of logs) {
      const statusBadge = l.success ? '<span class="badge badge-green">成功</span>' : '<span class="badge badge-red">失败</span>';
      const time = l.timestamp ? new Date(l.timestamp).toLocaleString('zh-CN',{hour:'2-digit',minute:'2-digit',second:'2-digit'}) : '-';
      html += `<tr><td>${time}</td><td>${l.model||'-'}</td><td>${l.provider_name||'-'}</td><td>${l.tokens||0}</td><td>${l.latency_ms||0}ms</td><td>${statusBadge}</td></tr>`;
    }
    html += '</tbody></table>';
    container.innerHTML = html;
  } catch(e) { console.error(e); }
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

