// admin-ledger.js — 贡献账本透明度面板（P2-2(ii)）
//
// 消费三个既有后端端点（均 admin 鉴权 + 限流）：
//   GET /api/admin/ledger/transparency        聚合视图：按节点 / 按模型的贡献量、链完整性
//   GET /api/admin/ledger/contribution-quota  每个贡献者「贡献 ↔ 赚得免费额度」明细
//   GET /api/admin/ledger/export?format=csv|json  研究者可直接下载的原始账本
//
// 公益口径：额度是 1:1 等额记账，不是货币——不可交易、不可提现、无手续费、
// 额度用尽也不会拒绝服务（回落社区免费池）。面板文案必须与该口径一致。

// 面板首次加载与手动刷新的统一入口。
async function loadLedgerTransparency() {
  const body = document.getElementById('ledgerPanelBody');
  if (!body) return;
  body.innerHTML = '<div style="color:var(--text-muted);font-size:13px">正在加载账本…</div>';

  let t = null;
  let q = null;
  try {
    const r = await authFetch('/api/admin/ledger/transparency');
    t = await r.json();
  } catch (e) {
    body.innerHTML = '<div style="color:var(--text-muted);font-size:13px">账本暂不可用：' +
      escapeHtml(e && e.message ? e.message : String(e)) + '</div>';
    return;
  }
  // 额度明细是补充信息：tracker 未就绪（503）时面板照常展示聚合视图。
  try {
    const rq = await authFetch('/api/admin/ledger/contribution-quota');
    q = await rq.json();
  } catch (e) {
    q = null;
  }

  body.innerHTML =
    renderLedgerSummary(t, q) +
    renderLedgerBars('按模型：算力用在哪些模型上', t && t.by_model) +
    renderLedgerBars('按节点：算力从哪些节点来', t && t.by_peer) +
    renderContributorTable(q);
}

// 顶部四格：累计贡献 / 记录数 / 参与节点 / 交易链完整性。
function renderLedgerSummary(t, q) {
  const chainOk = !!(t && t.chain_valid);
  const peers = (t && t.by_peer) ? Object.keys(t.by_peer).length : 0;
  const cell = function(value, label, color) {
    return '<div style="padding:12px 8px;background:var(--bg-secondary);border-radius:8px;text-align:center">' +
      '<div style="font-size:20px;font-weight:700;line-height:1.2;color:' + color + '">' + value + '</div>' +
      '<div style="font-size:11px;color:var(--text-muted);margin-top:4px">' + label + '</div>' +
      '</div>';
  };
  let html = '<div style="display:grid;grid-template-columns:repeat(4,1fr);gap:10px;margin-bottom:14px">';
  html += cell(formatTokens((t && t.total_tokens) || 0), '累计贡献 Token', 'var(--info)');
  html += cell(String((t && t.contribution_count) || 0), '贡献记录数', 'var(--text-primary)');
  html += cell(String(peers), '参与节点', 'var(--text-primary)');
  html += cell(chainOk ? '完整' : '异常', '交易链校验', chainOk ? 'var(--success)' : 'var(--error)');
  html += '</div>';

  if (q) {
    const total = q.total_contributed_tokens || 0;
    const used = q.total_consumed_tokens || 0;
    const left = q.total_remaining_tokens || 0;
    html += '<div style="display:flex;gap:10px;flex-wrap:wrap;margin-bottom:14px;font-size:12px">' +
      '<span class="badge badge-blue">贡献总额 ' + formatTokens(total) + '</span>' +
      '<span class="badge badge-yellow">已兑用 ' + formatTokens(used) + '</span>' +
      '<span class="badge badge-green">剩余额度 ' + formatTokens(left) + '</span>' +
      '</div>';
  }
  html += '<div style="font-size:11px;color:var(--text-muted);line-height:1.7;margin-bottom:16px">' +
    '额度按 1:1 等额记账，不是代币：不可交易、不可提现、无手续费、不通胀。' +
    '额度用尽不会被拒绝服务，请求会回落到社区免费池。' +
    '</div>';
  return html;
}

// 横向条形列表：按占比排序，最多展示 8 行，其余折叠为「其他」。
function renderLedgerBars(title, mapping) {
  const entries = [];
  if (mapping) {
    for (const k in mapping) {
      if (Object.prototype.hasOwnProperty.call(mapping, k)) entries.push([k, mapping[k] || 0]);
    }
  }
  entries.sort(function(a, b) { return b[1] - a[1]; });

  let html = '<div style="border:1px solid var(--border-color);border-radius:10px;padding:14px;margin-bottom:12px">' +
    '<div style="font-size:13px;font-weight:600;margin-bottom:10px">' + escapeHtml(title) + '</div>';
  if (entries.length === 0) {
    html += '<div style="font-size:12px;color:var(--text-muted)">暂无记录</div></div>';
    return html;
  }

  const shown = entries.slice(0, 8);
  const rest = entries.slice(8);
  let restSum = 0;
  rest.forEach(function(e) { restSum += e[1]; });
  if (restSum > 0) shown.push(['其他 ' + rest.length + ' 项', restSum]);

  const max = shown[0][1] || 1;
  shown.forEach(function(e) {
    const pct = Math.max(2, Math.round((e[1] / max) * 100));
    html += '<div style="margin-bottom:8px">' +
      '<div style="display:flex;justify-content:space-between;font-size:12px;margin-bottom:3px">' +
        '<span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:70%">' + escapeHtml(e[0]) + '</span>' +
        '<span style="color:var(--text-muted);font-family:monospace">' + formatTokens(e[1]) + '</span>' +
      '</div>' +
      '<div style="height:6px;background:var(--bg-secondary);border-radius:3px;overflow:hidden">' +
        '<div style="height:100%;width:' + pct + '%;background:var(--info);border-radius:3px"></div>' +
      '</div>' +
    '</div>';
  });
  html += '</div>';
  return html;
}

// 贡献者明细表：贡献 ↔ 赚得 ↔ 已用 ↔ 剩余。
function renderContributorTable(q) {
  let html = '<div style="border:1px solid var(--border-color);border-radius:10px;padding:14px">' +
    '<div style="font-size:13px;font-weight:600;margin-bottom:10px">贡献者额度明细</div>';
  const rows = (q && q.contributors) || [];
  if (!rows.length) {
    html += '<div style="font-size:12px;color:var(--text-muted)">暂无贡献者记录</div></div>';
    return html;
  }
  html += '<div style="overflow-x:auto"><table style="width:100%;border-collapse:collapse;font-size:12px">' +
    '<thead><tr style="color:var(--text-muted);text-align:left">' +
      '<th style="padding:6px 8px;font-weight:500">节点</th>' +
      '<th style="padding:6px 8px;font-weight:500;text-align:right">贡献</th>' +
      '<th style="padding:6px 8px;font-weight:500;text-align:right">赚得额度</th>' +
      '<th style="padding:6px 8px;font-weight:500;text-align:right">已兑用</th>' +
      '<th style="padding:6px 8px;font-weight:500;text-align:right">剩余</th>' +
    '</tr></thead><tbody>';
  rows.slice(0, 50).forEach(function(row) {
    html += '<tr style="border-top:1px solid var(--border-color)">' +
      '<td style="padding:6px 8px;font-family:monospace">' + escapeHtml(row.peer_id || '-') + '</td>' +
      '<td style="padding:6px 8px;text-align:right">' + formatTokens(row.contributed_tokens || 0) + '</td>' +
      '<td style="padding:6px 8px;text-align:right">' + formatTokens(row.earned_free_quota || 0) + '</td>' +
      '<td style="padding:6px 8px;text-align:right;color:var(--warning)">' + formatTokens(row.consumed_quota || 0) + '</td>' +
      '<td style="padding:6px 8px;text-align:right;color:var(--success)">' + formatTokens(row.remaining_quota || 0) + '</td>' +
    '</tr>';
  });
  html += '</tbody></table></div>';
  if (rows.length > 50) {
    html += '<div style="font-size:11px;color:var(--text-muted);margin-top:8px">仅展示前 50 位，完整数据请用下方导出。</div>';
  }
  html += '</div>';
  return html;
}

// 导出走 fetch + Blob：下载端点需要 Authorization 头，直接 <a href> 会 401。
async function exportLedger(format) {
  const fmt = (format === 'csv') ? 'csv' : 'json';
  try {
    const r = await authFetch('/api/admin/ledger/export?format=' + fmt);
    const blob = await r.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'omp-ledger-' + new Date().toISOString().slice(0, 10) + '.' + fmt;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    setTimeout(function() { URL.revokeObjectURL(url); }, 1000);
    toast('账本已导出（' + fmt.toUpperCase() + '）', 'success');
  } catch (e) {
    toast('导出失败：' + (e && e.message ? e.message : String(e)), 'error');
  }
}
