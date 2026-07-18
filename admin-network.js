// admin-network.js — Shared network, federation, peer management

// Load node quota info for personal proxy section
async function loadNodeQuotaInfo() {
  const el = document.getElementById('nodeQuotaInfo');
  if (!el) return;
  try {
    const r = await authFetch('/api/network/status');
    const d = await r.json();
    if (d.enabled) {
      const cp = d.contrib_points || 0;
      const quota = cp > 0 ? (cp * 10).toLocaleString() + ' tokens' : '需先积累贡献积分';
      el.innerHTML = '🌐 节点可用额度: ' + quota + ' <span style="margin-left:12px">📡 已广播到网络</span>';
    } else {
      el.innerHTML = '🌐 节点可用额度: 本地模式（未加入共享网络）';
      el.style.background = 'rgba(108,99,255,.04)';
      el.style.borderColor = 'rgba(108,99,255,.12)';
    }
  } catch(e) {
    el.innerHTML = '🌐 节点额度: 获取失败';
  }
}
// Call on page load
setTimeout(loadNodeQuotaInfo, 1000);

// ============================================================
// Shared Network UI
// ============================================================
async function loadFederationConfig() {
  try {
    const res = await authFetch('/api/federation/config');
    const data = await res.json();
    const relayOn = data.federation_relay_enabled === 'true';
    const relayToggle = document.getElementById('relayToggle');
    if (relayToggle) {
      relayToggle.checked = relayOn;
      updateToggleSlider('relayToggle', 'relayToggleSlider', relayOn);
    }
  } catch(e) {
    console.error('load federation config error:', e);
  }
}

function updateToggleSlider(checkboxId, sliderId, checked) {
  const slider = document.getElementById(sliderId);
  if (!slider) return;
  if (checked) {
    slider.style.background = 'var(--success)';
    slider.querySelector('span').style.left = 'calc(100% - 25px)';
  } else {
    slider.style.background = '#555';
    slider.querySelector('span').style.left = '3px';
  }
}

async function saveRelayToggle() {
  const enabled = document.getElementById('relayToggle').checked;
  updateToggleSlider('relayToggle', 'relayToggleSlider', enabled);
  try {
    const r = await authFetch('/api/federation/config', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({federation_relay_enabled: enabled ? 'true' : 'false'})
    });
    if (r.ok) {
      toast(enabled ? '中继已开启' : '中继已关闭', 'success');
    } else {
      toast('保存失败', 'error');
      document.getElementById('relayToggle').checked = !enabled;
      updateToggleSlider('relayToggle', 'relayToggleSlider', !enabled);
    }
  } catch(e) {
    toast('操作失败: ' + e.message, 'error');
    document.getElementById('relayToggle').checked = !enabled;
    updateToggleSlider('relayToggle', 'relayToggleSlider', !enabled);
  }
}

function refreshFederation() {
  loadFederationConfig();
  toast('已刷新', 'info');
}

// Invite management
async function createInvite() {
  const target = document.getElementById('inviteTarget').value.trim();
  const type = document.getElementById('inviteType').value;
  const inviteePub = target || '*';
  try {
    const res = await authFetch('/api/federation/invites', {
      method: 'POST',
      body: JSON.stringify({
        invitee_pub: inviteePub,
        type: type,
        expires_hours: 168
      })
    });
    if (res.ok) {
      const data = await res.json();
      toast('邀请已创建', 'success');
      loadInvites();
      // Show the encoded invite code
      const code = data.encoded;
      if (code) {
        const inviteMsg = `OpenModelPool Agent 联邦邀请码：\n${code}\n\n请在 OpenModelPool Agent 管理页面中导入此邀请码加入网络。`;
        copyText(inviteMsg);
      }
    } else {
      const data = await res.json().catch(() => ({}));
      toast(extractError(data) || '创建失败', 'error');
    }
  } catch(e) { toast('创建失败', 'error'); }
}

async function loadInvites() {
  try {
    const res = await authFetch('/api/federation/invites');
    const data = await res.json();
    const el = document.getElementById('inviteList');
    if (!data.invites || data.invites.length === 0) {
      el.innerHTML = '<span style="color:var(--text-muted)">暂无邀请记录</span>';
      return;
    }
    let html = '<table style="width:100%;font-size:12px;border-collapse:collapse">';
    html += '<tr style="border-bottom:1px solid var(--border-color)"><th style="text-align:left;padding:4px">邀请码</th><th>类型</th><th>受邀方</th><th>过期时间</th><th>操作</th></tr>';
    for (const inv of data.invites) {
      const typeLabel = {directed:'定向', public:'公开', chain:'链式'}[inv.type] || inv.type;
      const expires = new Date(inv.expires_at).toLocaleString();
      const isExpired = new Date(inv.expires_at) < new Date();
      const status = isExpired ? '<span style="color:var(--danger)">已过期</span>' : '<span style="color:var(--success)">有效</span>';
      html += `<tr style="border-bottom:1px solid var(--border-color)">`;
      html += `<td style="padding:4px;font-family:monospace;font-size:11px;max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${inv.invite_code || ''}">${(inv.invite_code || '').substring(0, 30)}...</td>`;
      html += `<td style="text-align:center">${typeLabel}</td>`;
      html += `<td style="text-align:center;font-size:11px">${inv.invitee_pub === '*' ? '公开' : inv.invitee_pub.substring(0, 12) + '...'}</td>`;
      html += `<td style="text-align:center">${expires}</td>`;
      html += `<td style="text-align:center">${status}</td>`;
      html += `</tr>`;
    }
    html += '</table>';
    el.innerHTML = html;
  } catch(e) {
    document.getElementById('inviteList').innerHTML = '<span style="color:var(--text-muted)">加载失败</span>';
  }
}
// Module: ShareCenter - Share center functionality
// ================================================================
let shareInfoData = null;
let _currentKeyPurpose = 'consumer';
let _shareFilter = 'all';

    async function loadNetworkStatus() {
      try {
        const r = await authFetch('/api/network/status');
        networkStatus = await r.json();
        renderNetworkUI();
      } catch(e) {
        console.warn('network status load failed', e);
      }
    }

    function renderNetworkUI() {
      if (!networkStatus) return;
      const s = networkStatus;
      const isShared = s.mode === 'shared' || s.network_enabled;
      const panel = document.getElementById('networkActivePanel');
      const toggle = document.getElementById('sharedNetworkToggle');
      // Update the status badge to reflect actual shared network state (not federation)
      const statusBadge = document.getElementById('fedEnabledStatus');
      if (statusBadge) {
        statusBadge.textContent = isShared ? '✅ 已加入' : '未加入';
        statusBadge.style.color = isShared ? 'var(--success)' : 'var(--text-muted)';
      }
      if (isShared) {
        if (panel) panel.style.display = '';
        if (toggle) toggle.checked = true;
        updateToggleSlider('sharedNetworkToggle', 'sharedNetworkToggleSlider', true);
        setText('netNodeId', s.node_id || '-');
        setText('netUptime', formatUptime(s.uptime_seconds || 0));
        setText('netPeers', (s.peers_count || 0) + ' / ' + ((s.stats && s.stats.online_peers) || 0) + ' 在线');
        setText('netMode', '共享网络');
        setText('netCredits', (s.contrib_points || 0).toLocaleString());
        setText('netRelayRequests', ((s.stats && s.stats.requests_relayed) || 0).toLocaleString());
        setText('netRequestsReceived', ((s.stats && s.stats.requests_received) || 0).toLocaleString());
        loadNetworkPeers();
        loadNetworkDashboard();
      } else {
        if (panel) panel.style.display = 'none';
        if (toggle) toggle.checked = false;
        updateToggleSlider('sharedNetworkToggle', 'sharedNetworkToggleSlider', false);
      }
    }

    async function loadNetworkPeers() {
      try {
        const r = await authFetch('/api/network/peers');
        const d = await r.json();
        const list = document.getElementById('netPeersList');
        if (!list) return;
        if (!d.peers || d.peers.length === 0) {
          list.innerHTML = '<div style="color:var(--text-muted)">暂无连接节点</div>';
          return;
        }
        let html = '<div style="display:flex;flex-direction:column;gap:8px">';
        d.peers.forEach(p => {
          const statusColor = p.status === 'online' ? 'var(--success)' : p.status === 'degraded' ? 'var(--warning)' : 'var(--error)';
          html += '<div style="background:var(--bg-secondary);border:1px solid var(--border-color);border-radius:8px;padding:10px 14px;display:flex;justify-content:space-between;align-items:center">';
          html += '<div>';
          html += '<div style="font-weight:500;font-size:13px">' + (p.name || p.node_id) + '</div>';
          html += '<div style="font-size:11px;color:var(--text-muted);font-family:monospace">' + p.node_id + '</div>';
          if (p.models && p.models.length > 0) {
            html += '<div style="font-size:11px;color:var(--text-secondary);margin-top:2px">' + p.models.slice(0,3).join(', ') + (p.models.length > 3 ? '...' : '') + '</div>';
          }
          html += '</div>';
          html += '<div style="display:flex;align-items:center;gap:8px">';
          html += '<span style="width:8px;height:8px;border-radius:50%;background:' + statusColor + '"></span>';
          html += '<button class="btn btn-danger btn-sm" onclick="removeNetworkPeer(\'' + p.node_id + '\')" style="padding:3px 8px;font-size:10px">移除</button>';
          html += '</div></div>';
        });
        html += '</div>';
        list.innerHTML = html;
      } catch(e) { console.warn('load peers failed', e); }
    }

    async function loadNetworkDashboard() {
      try {
        const r = await authFetch('/api/network/stats');
        const d = await r.json();
        setText('dashTotalNodes', d.total_nodes || '-');
        setText('dashSuccessRate', d.success_rate ? (d.success_rate * 100).toFixed(1) + '%' : '-');
        setText('dashModelsShared', d.models_shared || 0);
        setText('dashTotalRequests', (d.total_requests || 0).toLocaleString());
      } catch(e) {
        console.warn('network dashboard load failed', e);
      }
      // Load pool quota data
      try {
        const [quotaR, statusR] = await Promise.all([
          authFetch('/api/network/open-key-quota'),
          authFetch('/api/network/status')
        ]);
        const quota = await quotaR.json();
        const status = await statusR.json();
        const maxDaily = status.max_daily_requests || 0;
        const pubPercent = (status.quota_allocation && status.quota_allocation.public_key_percent) || 0;
        const pubDaily = Math.round(maxDaily * pubPercent / 100);
        const globalQuota = quota.global_quota || 0;
        const userQuota = quota.user_quota || 0;
        const contribShare = quota.contribution_share || 0;
        const sharePercent = globalQuota > 0 ? (contribShare * 100).toFixed(1) : '0';
        setText('poolDailyContrib', maxDaily.toLocaleString());
        setText('poolPublicShare', pubPercent + '%');
        setText('poolPublicDaily', pubDaily > 0 ? pubDaily.toLocaleString() + '/天' : '-');
        setText('poolGlobalQuota', globalQuota.toLocaleString());
        setText('poolMyQuota', userQuota.toLocaleString());
        setText('poolSharePercent', sharePercent);
      } catch(e) {
        console.warn('pool quota load failed', e);
      }
    }

    // Disclaimer modal
    async function showNetworkDisclaimer() {
      const modal = document.getElementById('networkDisclaimerModal');
      modal.classList.add('open');
      // Load disclaimer
      try {
        const r = await fetch('/api/network/disclaimer');
        const d = await r.json();
        renderDisclaimer(d);
      } catch(e) {
        toast('加载免责声明失败', 'error');
      }
    }

    function renderDisclaimer(d) {
      const el = document.getElementById('disclaimerContent');
      const defaultSections = [
        {heading: '📢 共享网络说明', content: '• 您的 Provider 将被贡献到全网资源池，其他节点的用户可通过 Public Key 或 Guest Key 调用您的 Provider\n• 您将获得访问其他节点 Provider 的权限\n• 您将获得贡献积分（Contribution Credit），积分不可提现/交易\n• 退出网络后，您的 Provider 不再被共享', is_risk: false},
        {heading: '⚠️ 风险提示', content: '• 其他节点可能不稳定，请求可能失败\n• 您的 API Key 将在加密通道中传输，但无法完全保证安全\n• 贡献积分仅作为声誉指标，无经济价值\n• 请仅贡献您愿意共享的 Provider', is_risk: true},
        {heading: '📊 声誉系统', content: '• 节点声誉等级：S≥95 / A≥80 / B≥60 / C≥40 / D<40\n• 高声誉节点获得更多路由优先权\n• 持续稳定贡献可提升声誉等级', is_risk: false}
      ];
      const sections = (d && d.sections && d.sections.length > 0) ? d.sections : defaultSections;
      let html = '';
      sections.forEach(s => {
        const isRisk = s.is_risk === true;
        html += '<div style="margin-bottom:16px;padding:14px;border-radius:8px;';
        if (isRisk) {
          html += 'background:rgba(239,68,68,.08);border:1px solid rgba(239,68,68,.3)';
        } else {
          html += 'background:var(--bg-secondary);border:1px solid var(--border-color)';
        }
        html += '">';
        html += '<div style="font-weight:600;font-size:14px;margin-bottom:8px;';
        if (isRisk) html += 'color:var(--error)';
        html += '">' + s.heading + '</div>';
        html += '<div style="font-size:13px;line-height:1.7;white-space:pre-line;';
        if (isRisk) html += 'color:#ff6b6b;font-weight:700';
        else html += 'color:var(--text-secondary)';
        html += '">' + s.content + '</div>';
        html += '</div>';
      });
      el.innerHTML = html;
      // Set confirmation text
      const ct = document.getElementById('networkConsentText');
      if (ct && d && d.confirmation_text) ct.textContent = d.confirmation_text;
      else if (ct) ct.textContent = '我已阅读并理解以上说明，自愿承担相关风险';
    }

    function closeNetworkDisclaimer() {
      document.getElementById('networkDisclaimerModal').classList.remove('open');
      document.getElementById('networkConsentCheck').checked = false;
      toggleNetworkConsentBtn();
    }

    function toggleNetworkConsentBtn() {
      const checked = document.getElementById('networkConsentCheck').checked;
      const btn = document.getElementById('networkConsentBtn');
      btn.disabled = !checked;
      btn.style.opacity = checked ? '1' : '0.5';
    }

    async function confirmNetworkJoin() {
      try {
        // Step 1: Record consent
        const r1 = await authFetch('/api/network/consent', {method:'POST', body:JSON.stringify({accepted:true})});
        if (!r1.ok) { toast('同意记录失败', 'error'); return; }
        // Step 2: Enable shared network
        const r2 = await authFetch('/api/network/enable', {method:'POST'});
        const d = await r2.json();
        if (!r2.ok) { toast(extractError(d) || '启用失败', 'error'); return; }
        // Sync federation_enabled to stay consistent with network state
        authFetch('/api/federation/config', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({federation_enabled:'true'})}).catch(()=>{});
        // Update UI state
        document.getElementById('sharedNetworkToggle').checked = true;
        updateToggleSlider('sharedNetworkToggle', 'sharedNetworkToggleSlider', true);
        document.getElementById('quotaAllocation').style.display = 'block';
        
        closeNetworkDisclaimer();
        let msg = '🎉 已加入共享网络！';
        if (d.node_id) msg += ' NodeID: ' + d.node_id;
        toast(msg, 'success');
        await loadNetworkStatus();
        // Load shared-network features now that network is enabled
        window._networkMode = 'shared';
        try { loadFederationConfig(); } catch(e) {}
        try { loadShareInfo(); } catch(e) {}
        try { loadGuestKeys(); } catch(e) {}
        if (typeof startNetworkPolling === 'function') startNetworkPolling();
      } catch(e) {
        toast('加入失败: ' + e.message, 'error');
      }
    }

    async function disableNetwork() {
      if (!confirm('确定要退出共享网络吗？所有连接节点将被清除，回到个人模式。')) return;
      try {
        // Sync federation_enabled off
        authFetch('/api/federation/config', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({federation_enabled:'false'})}).catch(()=>{});
        const r = await authFetch('/api/network/disable', {method:'POST'});
        if (!r.ok) { toast('退出失败', 'error'); return; }
        toast('已退出共享网络，回到个人模式', 'success');
        window._networkMode = 'personal';
        if (typeof stopNetworkPolling === 'function') stopNetworkPolling();
        await loadNetworkStatus();
      } catch(e) {
        toast('退出失败: ' + e.message, 'error');
      }
    }

    async function removeNetworkPeer(nodeId) {
      if (!confirm('确定移除此节点？')) return;
      try {
        const r = await authFetch('/api/network/peers/' + encodeURIComponent(nodeId), {method:'DELETE'});
        if (!r.ok) { toast('移除失败', 'error'); return; }
        toast('节点已移除', 'success');
        await loadNetworkStatus();
      } catch(e) {
        toast('移除失败: ' + e.message, 'error');
      }
    }

    function formatUptime(seconds) {
      if (seconds < 60) return seconds + '秒';
      if (seconds < 3600) return Math.floor(seconds/60) + '分' + (seconds%60) + '秒';
      const h = Math.floor(seconds/3600);
      const m = Math.floor((seconds%3600)/60);
      if (h < 24) return h + '时' + m + '分';
      const d = Math.floor(h/24);
      return d + '天' + (h%24) + '时';
    }

    function setText(id, val) {
      const el = document.getElementById(id);
      if (el) el.textContent = val;
    }

    function startNetworkPolling() {
      if (_netPollTimer) return;
      _netPollTimer = setInterval(async () => {
        try { await loadNetworkStatus(); } catch(e) {
          console.error('网络状态刷新失败:', e);
          clearInterval(_netPollTimer); _netPollTimer = null;
        }
      }, 30000);
    }

    function stopNetworkPolling() {
      if (_netPollTimer) { clearInterval(_netPollTimer); _netPollTimer = null; }
    }

    // ============================================================
    // ============================================================
    
function toggleSharedNetwork() {
  const enabled = document.getElementById('sharedNetworkToggle').checked;
  if (enabled) {
    // Reset the toggle until user confirms disclaimer
    document.getElementById('sharedNetworkToggle').checked = false;
    showNetworkDisclaimer();
  } else {
    // Disabling - show backup reminder with NodeID
    const nodeId = (networkStatus && networkStatus.node_id) || '-';
    const backupMsg = `⚠️ 退出共享网络提醒
您的节点身份信息将被清除：
NodeID: ${nodeId}
请备份以上 NodeID，重新加入时可用于恢复节点身份和贡献积分。
确定要退出吗？`;
    
    if (!confirm(backupMsg)) {
      document.getElementById('sharedNetworkToggle').checked = true;
      return;
    }
    // Sync federation_enabled off before disabling network
    authFetch('/api/federation/config', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({federation_enabled:'false'})}).catch(()=>{});
    authFetch('/api/network/disable', {
      method: 'POST'
    }).then(r => {
      if (r.ok) {
        toast('已退出共享网络，回到个人模式', 'success');
        updateToggleSlider('sharedNetworkToggle', 'sharedNetworkToggleSlider', false);
        document.getElementById('quotaAllocation').style.display = 'none';
        document.getElementById('networkActivePanel').style.display = 'none';
        window._networkMode = 'personal';
        if (typeof stopNetworkPolling === 'function') stopNetworkPolling();
        loadNetworkStatus();
      } else {
        toast('退出失败', 'error');
        document.getElementById('sharedNetworkToggle').checked = true;
      }
    }).catch(e => {
      toast('操作失败: ' + e.message, 'error');
      document.getElementById('sharedNetworkToggle').checked = true;
    });
  }
}

async function saveQuotaAllocation() {
  const guestPercent = parseInt(document.getElementById('guestKeyPercentSlider').value, 10);
  try {
    const r = await authFetch('/api/network/quota-allocation', {
      method: 'PUT',
      body: JSON.stringify({ guest_key_percent: guestPercent })
    });
    const d = await r.json();
    if (d.guest_key_percent !== undefined) {
      toast('✅ 额度分配已保存', 'success');
    } else {
      toast('保存失败', 'error');
    }
  } catch(e) { toast('保存失败: ' + e.message, 'error'); }
}

async function generateFedInvite() {
  try {
    const r = await authFetch('/api/federation/invites', {
      method: 'POST',
      body: JSON.stringify({ invitee_name: '新节点', invite_type: 'federation', expires_in_hours: 72 })
    });
    const d = await r.json();
    if (d.code) {
      document.getElementById('fedInviteCode').value = d.code;
      toast('✅ 邀请码已生成');
    } else {
      toast('生成失败: ' + extractError(d) || '未知错误', 'error');
    }
  } catch(e) { toast('生成失败: ' + e.message, 'error'); }
}

