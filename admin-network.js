// admin-network.js — Shared network, federation, peer management

// Load node quota info for personal proxy section
async function loadNodeQuotaInfo() {
  const el = document.getElementById('nodeQuotaInfo');
  if (!el) return;
  try {
    const r = await authFetch('/api/network/status');
    const d = await r.json();
    if (d.network_enabled) {
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
      html += `<td style="padding:4px;font-family:monospace;font-size:11px;max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${escapeAttr(inv.invite_code || '')}">${escapeHtml((inv.invite_code || '').substring(0, 30))}...</td>`;
      html += `<td style="text-align:center">${escapeHtml(typeLabel)}</td>`;
      html += `<td style="text-align:center;font-size:11px">${inv.invitee_pub === '*' ? '公开' : escapeHtml(inv.invitee_pub.substring(0, 12)) + '...'}</td>`;
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
        maybeShowIdleCapacityHint();
      } catch(e) {
        console.warn('network status load failed', e);
      }
    }

    function renderNetworkUI() {
      if (!networkStatus) return;
      const s = networkStatus;
      const isShared = s.mode === 'shared' || s.network_enabled;
      window._networkMode = isShared ? 'shared' : 'personal';
      const panel = document.getElementById('networkActivePanel');

      // Status badge reflects the real shared-network state (not federation).
      const statusBadge = document.getElementById('fedEnabledStatus');
      if (statusBadge) {
        statusBadge.textContent = isShared ? '✅ 已加入' : '未加入';
        statusBadge.style.color = isShared ? 'var(--success)' : 'var(--text-muted)';
      }

      // 两级开关：network_enabled / share_to_pool（REQ-3）
      const netToggle = document.getElementById('networkEnabledToggle');
      if (netToggle) {
        netToggle.checked = isShared;
        updateToggleSlider('networkEnabledToggle', 'networkEnabledToggleSlider', isShared);
      }
      const shareToggle = document.getElementById('shareToPoolToggle');
      if (shareToggle) {
        // share_to_pool 仅在 network_enabled=true 时可操作（单向依赖）。
        shareToggle.checked = !!s.share_to_pool;
        shareToggle.disabled = !isShared;
        updateToggleSlider('shareToPoolToggle', 'shareToPoolToggleSlider', !!s.share_to_pool);
        applyDisabledToggleStyle('shareToPoolToggleSlider', !isShared);
      }

      if (isShared) {
        if (panel) panel.style.display = '';
        setText('netNodeId', s.node_id || '-');
        setText('netUptime', formatUptime(s.uptime_seconds || 0));
        setText('netPeers', (s.peers_count || 0) + ' / ' + ((s.stats && s.stats.online_peers) || 0) + ' 在线');
        setText('netMode', '共享网络');
        setText('netCredits', (s.contrib_points || 0).toLocaleString());
        setText('netRelayRequests', ((s.stats && s.stats.requests_relayed) || 0).toLocaleString());
        setText('netRequestsReceived', ((s.stats && s.stats.requests_received) || 0).toLocaleString());
        // REQ-S2-4 (S5 恢复入口): 已加入态也提供「使用已有助记词恢复身份」入口
        if (panel && !document.getElementById('restoreIdentityEntry')) {
          const rb = document.createElement('button');
          rb.id = 'restoreIdentityEntry';
          rb.className = 'btn btn-secondary btn-sm';
          rb.style.cssText = 'margin-top:10px';
          rb.textContent = '🔑 使用已有助记词恢复身份';
          rb.onclick = openRestoreWizard;
          panel.appendChild(rb);
        }
        loadNetworkPeers();
        loadNetworkDashboard();
        loadInvites();
        loadCapabilityClaims();
        loadContributionLedger();
        loadShareBoundary();
      } else {
        if (panel) panel.style.display = 'none';
      }
      // REQ-S2-5: 迁移提示随时随状态刷新展示
      showMigrationHint();
    }

    // Grey out a toggle slider when its underlying control is disabled.
    function applyDisabledToggleStyle(sliderId, disabled) {
      const slider = document.getElementById(sliderId);
      if (!slider) return;
      if (disabled) {
        slider.style.opacity = '0.45';
        slider.style.cursor = 'not-allowed';
      } else {
        slider.style.opacity = '1';
        slider.style.cursor = 'pointer';
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
          html += '<div style="font-weight:500;font-size:13px">' + escapeHtml(p.name || p.node_id) + '</div>';
          html += '<div style="font-size:11px;color:var(--text-muted);font-family:monospace">' + escapeHtml(p.node_id) + '</div>';
          if (p.models && p.models.length > 0) {
            html += '<div style="font-size:11px;color:var(--text-secondary);margin-top:2px">' + escapeHtml(p.models.slice(0,3).join(', ')) + (p.models.length > 3 ? '...' : '') + '</div>';
          }
          html += '</div>';
          html += '<div style="display:flex;align-items:center;gap:8px">';
          html += '<span style="width:8px;height:8px;border-radius:50%;background:' + statusColor + '"></span>';
          html += '<button class="btn btn-danger btn-sm" onclick="removeNetworkPeer(\'' + escapeJS(p.node_id) + '\')" style="padding:3px 8px;font-size:10px">移除</button>';
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
        html += '">' + escapeHtml(s.heading) + '</div>';
        html += '<div style="font-size:13px;line-height:1.7;white-space:pre-line;';
        if (isRisk) html += 'color:#ff6b6b;font-weight:700';
        else html += 'color:var(--text-secondary)';
        html += '">' + escapeHtml(s.content) + '</div>';
        html += '</div>';
      });
      el.innerHTML = html;
      // Set confirmation text
      const ct = document.getElementById('networkConsentText');
      if (ct && d && d.confirmation_text) ct.textContent = d.confirmation_text;
      else if (ct) ct.textContent = '我已阅读并理解以上说明，自愿承担相关风险';
    }

    // ============================================================
    // 切片② 身份备份向导（四步状态机 S1→S2→S3→S4）
    // 助记词明文仅驻前端内存、展示仅一次；5 分钟超时或窗口失焦即清空（采纳 Q2）。
    // ============================================================
    const MNEMONIC_TIMEOUT_MS = 5 * 60 * 1000; // Q2: 5 分钟超时
    let _wizardMnemonic = '';   // 仅驻前端内存的助记词明文
    let _wizardTimer = null;
    let _wizardRestored = false; // 本次向导是否走恢复路径
    let _onboardingStep = 1;    // 当前向导步骤 (1-4)

    // ============================================================
    // 4 步入网向导核心逻辑
    // ============================================================

    function renderOnboardingWizard() {
      _onboardingStep = 1;
      _showOnboardingStep(1);
      _updateProgressBar(1);
    }

    function _showOnboardingStep(step) {
      ['onboardingStep1', 'onboardingStep2', 'onboardingStep3', 'onboardingStep4'].forEach((id, i) => {
        const el = document.getElementById(id);
        if (el) el.style.display = ((i + 1) === step) ? '' : 'none';
      });
      _updateProgressBar(step);
      const titleEl = document.getElementById('onboardingWizardTitle');
      const titles = ['🌐 加入共享网络', '🌐 加入共享网络 · 助记词备份', '🌐 加入共享网络 · 共享边界', '🌐 加入共享网络 · 完成'];
      if (titleEl && titles[step - 1]) titleEl.textContent = titles[step - 1];
    }

    function _updateProgressBar(step) {
      const dots = document.querySelectorAll('#onboardingProgressBar .ob-step-dot');
      const lines = document.querySelectorAll('#onboardingProgressBar .ob-step-line');
      dots.forEach((dot, i) => {
        const s = i + 1;
        dot.classList.remove('active', 'completed');
        if (s < step) dot.classList.add('completed');
        else if (s === step) dot.classList.add('active');
      });
      lines.forEach((line, i) => {
        const s = i + 1;
        line.classList.remove('active', 'completed');
        if (s < step) line.classList.add('completed');
        else if (s === step) line.classList.add('active');
      });
    }

    async function onboardingGoStep(step) {
      if (step === 2) {
        if (!_wizardMnemonic && !_wizardRestored) {
          _showOnboardingStep(2);
          const rp = document.getElementById('restorePanel');
          if (rp) rp.style.display = 'none';
          generateIdentity();
        } else {
          _showOnboardingStep(2);
        }
      } else if (step === 3) {
        if (!_wizardMnemonic && !_wizardRestored) {
          toast('请先生成助记词并确认备份', 'warning');
          return;
        }
        const bc = document.getElementById('backupConfirmCheck');
        if (bc && !bc.checked) {
          toast('请先勾选「我已安全抄写」', 'warning');
          return;
        }
        await loadShareBoundary();
        _showOnboardingStep(3);
      } else if (step === 4) {
        await saveShareBoundary();
        try {
          const r = await authFetch('/api/network/status');
          const d = await r.json();
          const doneId = document.getElementById('doneNodeId');
          if (doneId) doneId.textContent = 'Node ID: ' + (d.node_id || '生成中...');
        } catch(e) {
          const doneId = document.getElementById('doneNodeId');
          if (doneId) doneId.textContent = 'Node ID: 将在启用后生成';
        }
        _showOnboardingStep(4);
      } else {
        _showOnboardingStep(step);
      }
    }

    async function onboardingFinish() {
      const btn = document.getElementById('onboardingFinishBtn');
      if (btn) { btn.disabled = true; btn.textContent = '正在启用...'; btn.style.opacity = '0.5'; }
      try {
        await completeJoin(_wizardRestored);
      } catch(e) {
        toast('启用失败: ' + e.message, 'error');
      } finally {
        if (btn) { btn.disabled = false; btn.textContent = '完成'; btn.style.opacity = '1'; }
      }
    }

    // Legacy step IDs kept for backward compat with restore flow
    function _showWizardStep(step) {
      const stepMap = {
        'wizardStepDisclaimer': 1,
        'wizardStepMnemonic': 2,
        'wizardStepBackup': 2,
        'wizardStepDone': 4
      };
      const mapped = stepMap[step];
      if (mapped) _showOnboardingStep(mapped);
    }

    // 清空内存助记词并复位 S2 步骤（reason 非空时给出提示）
    function _clearWizardMnemonic(reason) {
      _wizardMnemonic = '';
      if (_wizardTimer) { clearTimeout(_wizardTimer); _wizardTimer = null; }
      const grid = document.getElementById('mnemonicGrid');
      if (grid) grid.innerHTML = '';
      const nextBtn = document.getElementById('mnemonicNextBtn');
      if (nextBtn) { nextBtn.disabled = true; nextBtn.style.opacity = '0.5'; }
      const expBtn = document.getElementById('mnemonicExportBtn');
      if (expBtn) expBtn.style.display = 'none';
      const bc = document.getElementById('backupConfirmCheck');
      if (bc) { bc.checked = false; toggleBackupConfirmBtn(); }
      if (reason) toast(reason, 'warning');
    }

    function _startWizardTimer() {
      if (_wizardTimer) clearTimeout(_wizardTimer);
      _wizardTimer = setTimeout(() => {
        _clearWizardMnemonic('助记词展示超时，已自动清空内存。请重新生成以继续。');
        closeNetworkWizard();
      }, MNEMONIC_TIMEOUT_MS);
    }

    // 窗口失焦即清（Q2 默认）：避免助记词明文长留内存
    window.addEventListener('blur', () => {
      const modal = document.getElementById('networkDisclaimerModal');
      if (_wizardMnemonic && modal && modal.classList.contains('open')) {
        _clearWizardMnemonic('窗口失焦，已自动清空内存中的助记词。请重新生成以继续。');
        closeNetworkWizard();
      }
    });

    async function showNetworkDisclaimer() {
      const modal = document.getElementById('networkDisclaimerModal');
      if (!modal) return;
      modal.classList.add('open');
      _wizardRestored = false;
      _onboardingStep = 1;
      renderOnboardingWizard();
      _clearWizardMnemonic('');
      try {
        const r = await fetch('/api/network/disclaimer');
        const d = await r.json();
        // Keep renderDisclaimer for legacy compat, but onboarding step 1 has static content
      } catch(e) {
        // Non-blocking: onboarding step 1 has static content already
      }
    }

    function closeNetworkWizard() {
      const modal = document.getElementById('networkDisclaimerModal');
      if (modal) modal.classList.remove('open');
      _clearWizardMnemonic('');
      _wizardRestored = false;
      _onboardingStep = 1;
      const bc = document.getElementById('backupConfirmCheck');
      if (bc) { bc.checked = false; toggleBackupConfirmBtn(); }
      const rp = document.getElementById('restorePanel');
      if (rp) rp.style.display = 'none';
    }

    function toggleNetworkConsentBtn() {
      const checked = document.getElementById('networkConsentCheck').checked;
      const btn = document.getElementById('networkConsentBtn');
      if (!btn) return;
      btn.disabled = !checked;
      btn.style.opacity = checked ? '1' : '0.5';
    }

    // S1 → S2：进入助记词步骤并生成（legacy compat, now delegates to onboardingGoStep）
    function wizardGoMnemonic() {
      onboardingGoStep(2);
    }

    // S2 → 返回 S1（legacy compat）
    function wizardBackToConsent() {
      _clearWizardMnemonic('');
      onboardingGoStep(1);
    }

    // S2 → S3（legacy compat, now handled by onboardingGoStep(3)）
    function wizardGoBackup() {
      onboardingGoStep(3);
    }

    // S3 → 返回 S2（legacy compat）
    function wizardBackToMnemonic() {
      onboardingGoStep(2);
    }

    // S2 → 返回 S1
    function wizardBackToConsent() {
      _clearWizardMnemonic('');
      _showWizardStep('wizardStepDisclaimer');
    }

    // 生成助记词（调用后端 /api/network/identity/generate）
    async function generateIdentity() {
      const sel = document.getElementById('mnemonicWordCount');
      const wc = sel ? (parseInt(sel.value, 10) || 12) : 12;
      try {
        const r = await authFetch('/api/network/identity/generate', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ word_count: wc })
        });
        const d = await r.json();
        if (!r.ok) { toast(extractError(d) || '生成失败', 'error'); return; }
        _wizardMnemonic = d.mnemonic;
        _wizardRestored = false;
        renderMnemonicStep(d.mnemonic);
        _startWizardTimer();
      } catch(e) {
        toast('生成失败: ' + e.message, 'error');
      }
    }

    // 渲染助记词词网格（仅展示一次，使用 DOM API 避免注入）
    function renderMnemonicStep(mnemonic) {
      const grid = document.getElementById('mnemonicGrid');
      if (!grid) return;
      const words = mnemonic.trim().split(/\s+/);
      grid.innerHTML = '';
      words.forEach((w, i) => {
        const cell = document.createElement('div');
        cell.style.cssText = 'display:flex;align-items:center;gap:6px;background:var(--bg-secondary);border:1px solid var(--border-color);border-radius:8px;padding:6px 8px;font-size:12px';
        const idx = document.createElement('span');
        idx.textContent = (i + 1) + '.';
        idx.style.cssText = 'color:var(--text-muted);width:18px;flex-shrink:0';
        const word = document.createElement('span');
        word.textContent = w;
        word.style.cssText = 'font-family:monospace';
        cell.appendChild(idx);
        cell.appendChild(word);
        grid.appendChild(cell);
      });
      const nextBtn = document.getElementById('mnemonicNextBtn');
      if (nextBtn) { nextBtn.disabled = false; nextBtn.style.opacity = '1'; }
      const expBtn = document.getElementById('mnemonicExportBtn');
      if (expBtn) expBtn.style.display = '';
      toggleBackupConfirmBtn();
    }

    // 加密导出（浏览器端包装为 .json 下载，明文不留盘）
    async function exportMnemonic() {
      if (!_wizardMnemonic) { toast('暂无助记词可导出', 'warning'); return; }
      const pass = prompt('请设置导出文件的保护密码（用于本地加密备份 .json）：', '');
      if (pass === null) return;
      try {
        const r = await authFetch('/api/network/status');
        const d = await r.json().catch(() => ({}));
        const payload = {
          app: 'OpenModelPool',
          version: 1,
          node_id: d.node_id || '',
          mnemonic: _wizardMnemonic,
          exported_at: new Date().toISOString()
        };
        const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'openmodelpool-identity-backup.json';
        a.click();
        URL.revokeObjectURL(url);
        toast('已导出备份文件（请另行妥善保存密码）', 'success');
      } catch(e) {
        toast('导出失败: ' + e.message, 'error');
      }
    }

    // S2 → S3：进入备份确认步骤
    function wizardGoBackup() {
      if (!_wizardMnemonic) { toast('请先生成助记词', 'warning'); return; }
      _showWizardStep('wizardStepBackup');
    }

    // S3 → 返回 S2
    function wizardBackToMnemonic() {
      _showWizardStep('wizardStepMnemonic');
    }

    function toggleBackupConfirmBtn() {
      const checked = document.getElementById('backupConfirmCheck').checked;
      const btn = document.getElementById('mnemonicNextBtn');
      if (!btn) return;
      btn.disabled = !checked || !_wizardMnemonic;
      btn.style.opacity = (checked && _wizardMnemonic) ? '1' : '0.5';
    }

    // 恢复入口（S0/S5）：进入 S2 的恢复面板
    function startRestoreFlow() {
      _wizardRestored = true;
      _showWizardStep('wizardStepMnemonic');
      const rp = document.getElementById('restorePanel');
      if (rp) rp.style.display = '';
      const grid = document.getElementById('mnemonicGrid');
      if (grid) grid.innerHTML = '<div style="font-size:12px;color:var(--text-muted)">请输入您此前备份的助记词以恢复节点身份（恢复成功后将自动完成加入）。</div>';
      const nextBtn = document.getElementById('mnemonicNextBtn');
      if (nextBtn) { nextBtn.disabled = true; nextBtn.style.opacity = '0.5'; }
    }

    // 从已加入态（S5）打开恢复向导
    async function openRestoreWizard() {
      await showNetworkDisclaimer();
      startRestoreFlow();
    }

    // 恢复身份（调用后端 /api/network/identity/restore）
    async function restoreIdentity() {
      const input = document.getElementById('restoreMnemonicInput');
      const mnemonic = (input && input.value) ? input.value.trim() : '';
      if (!mnemonic) { toast('请输入助记词', 'warning'); return; }
      try {
        const r = await authFetch('/api/network/identity/restore', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ mnemonic })
        });
        const d = await r.json();
        if (!r.ok) { toast(extractError(d) || '恢复失败，助记词无效', 'error'); return; }
        _wizardMnemonic = ''; // 恢复态不展示明文
        toast('✅ 已从助记词恢复身份', 'success');
        // 恢复即视为已备份，直接进入完成/启用流程
        await completeJoin(true);
      } catch(e) {
        toast('恢复失败: ' + e.message, 'error');
      }
    }

    // 完成加入：确认备份 → 启用共享网络（REQ-S2-2 双保险）
    async function completeJoin(fromRestore) {
      try {
        // 0) 记录用户同意（REQ-S2-1 同意守卫）：必须在 enable 之前完成，否则
        //    后端 EnableSharedNetwork 守卫 `if !ConsentAccepted` 会拒绝加入，
        //    导致「生成 → 展示助记词 → 确认备份 → 启用」全流程后仍无法加入。
        //    该调用幂等，重复点击「启用」不会出错；恢复路径同样需要先记录同意。
        try {
          const rc = await authFetch('/api/network/consent', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({ accepted: true })
          });
          if (!rc.ok) {
            const cd = await rc.json().catch(() => ({}));
            toast(extractError(cd) || '记录同意失败', 'error');
            return;
          }
        } catch(e) {
          toast('记录同意失败: ' + e.message, 'error');
          return;
        }
        // 1) 确认备份（幂等；恢复路径已 backupConfirmed=true，再次确认无害）
        if (!fromRestore) {
          const r1 = await authFetch('/api/network/identity/confirm-backup', { method: 'POST' });
          if (!r1.ok) {
            const d = await r1.json().catch(() => ({}));
            toast(extractError(d) || '备份确认失败', 'error');
            return;
          }
        }
        // 2) 启用共享网络（后端严格守卫：必须已确认备份）
        const r2 = await authFetch('/api/network/enable', { method: 'POST' });
        const d = await r2.json();
        if (!r2.ok) { toast(extractError(d) || '启用失败', 'error'); return; }

        // 3) 成功：展示完成态并复位内存助记词
        _clearWizardMnemonic('');
        const doneId = document.getElementById('doneNodeId');
        if (doneId) doneId.textContent = 'Node ID: ' + (d.node_id || '');
        _showOnboardingStep(4);

        // 更新开关与 UI
        const tog = document.getElementById('networkEnabledToggle');
        if (tog) { tog.checked = true; updateToggleSlider('networkEnabledToggle', 'networkEnabledToggleSlider', true); }
        const qa = document.getElementById('quotaAllocation');
        if (qa) qa.style.display = 'block';
        window._networkMode = 'shared';
        try { loadFederationConfig(); } catch(e) {}
        try { loadShareInfo(); } catch(e) {}
        try { loadGuestKeys(); } catch(e) {}
        if (typeof startNetworkPolling === 'function') startNetworkPolling();
        await loadNetworkStatus();
        const msg = _wizardRestored ? '🎉 已从助记词恢复并加入共享网络！' : '🎉 已加入共享网络！';
        toast(msg, 'success');
      } catch(e) {
        toast('加入失败: ' + e.message, 'error');
      }
    }

    // Fallback: confirmNetworkJoin (legacy single-step confirm, kept for backward compat)
    async function confirmNetworkJoin() {
      try {
        const rc = await authFetch('/api/network/consent', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ accepted: true })
        });
        if (!rc.ok) {
          const cd = await rc.json().catch(() => ({}));
          toast(extractError(cd) || '记录同意失败', 'error');
          return;
        }
        const r2 = await authFetch('/api/network/enable', { method: 'POST' });
        const d = await r2.json();
        if (!r2.ok) { toast(extractError(d) || '启用失败', 'error'); return; }
        toast('已加入共享网络', 'success');
        await loadNetworkStatus();
      } catch(e) {
        toast('加入失败: ' + e.message, 'error');
      }
    }

    // 迁移提示（REQ-S2-5）：needs_migration 时在前端给出清晰提示
    function showMigrationHint() {
      const hint = document.getElementById('migrationHint');
      if (!hint) return;
      if (networkStatus && networkStatus.needs_migration) {
        hint.style.display = '';
        hint.textContent = '⚠️ 检测到旧格式（mm-）节点身份，已自动迁移为新的 mmx- 格式。您的节点身份与贡献积分保持不变。';
      } else {
        hint.style.display = 'none';
      }
    }

    async function disableNetwork() {
      if (!confirm('确定要退出共享网络吗？所有连接节点将被清除，回到个人模式。')) return;
      try {
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
    
    // 开关一：network_enabled（是否入网）。开启前先做 REQ-4 入网前置校验。
    async function toggleNetworkEnabled() {
      const enabled = document.getElementById('networkEnabledToggle').checked;
      if (enabled) {
        // 先复位开关，待校验/引导通过后再置位
        document.getElementById('networkEnabledToggle').checked = false;
        updateToggleSlider('networkEnabledToggle', 'networkEnabledToggleSlider', false);
        try {
          const r = await authFetch('/api/network/join-conditions');
          const d = await r.json();
          if (!d.all_met) {
            showJoinConditionGuidance(d);
            return;
          }
        } catch (e) {
          toast('入网条件校验失败: ' + e.message, 'error');
          return;
        }
        // 条件满足 → 进入须知 → 同意 → enable 流程
        showNetworkDisclaimer();
      } else {
        // 关闭：退出共享网络回到个人版
        const nodeId = (networkStatus && networkStatus.node_id) || '-';
        const backupMsg = `⚠️ 退出共享网络提醒
您的节点身份信息将被清除：
NodeID: ${nodeId}
请备份以上 NodeID，重新加入时可用于恢复节点身份和贡献积分。
确定要退出吗？`;
        if (!confirm(backupMsg)) {
          document.getElementById('networkEnabledToggle').checked = true;
          updateToggleSlider('networkEnabledToggle', 'networkEnabledToggleSlider', true);
          return;
        }
        try {
          const r = await authFetch('/api/network/disable', {method: 'POST'});
          if (r.ok) {
            toast('已退出共享网络，回到个人模式', 'success');
            window._networkMode = 'personal';
            if (typeof stopNetworkPolling === 'function') stopNetworkPolling();
            await loadNetworkStatus();
          } else {
            toast('退出失败', 'error');
            document.getElementById('networkEnabledToggle').checked = true;
            updateToggleSlider('networkEnabledToggle', 'networkEnabledToggleSlider', true);
          }
        } catch (e) {
          toast('操作失败: ' + e.message, 'error');
          document.getElementById('networkEnabledToggle').checked = true;
          updateToggleSlider('networkEnabledToggle', 'networkEnabledToggleSlider', true);
        }
      }
    }

    // 开关二：share_to_pool（是否共享剩余额度）。单向依赖 network_enabled=true。
    async function toggleShareToPool() {
      const enabled = document.getElementById('shareToPoolToggle').checked;
      updateToggleSlider('shareToPoolToggle', 'shareToPoolToggleSlider', enabled);
      try {
        const r = await authFetch('/api/network/toggle', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ share_to_pool: enabled, network_enabled: true })
        });
        if (r.ok) {
          toast(enabled ? '已开始共享剩余额度' : '已停止共享额度', 'success');
          await loadNetworkStatus();
        } else {
          const d = await r.json().catch(() => ({}));
          toast(extractError(d) || '保存失败', 'error');
          // revert UI
          document.getElementById('shareToPoolToggle').checked = !enabled;
          updateToggleSlider('shareToPoolToggle', 'shareToPoolToggleSlider', !enabled);
        }
      } catch (e) {
        toast('操作失败: ' + e.message, 'error');
        document.getElementById('shareToPoolToggle').checked = !enabled;
        updateToggleSlider('shareToPoolToggle', 'shareToPoolToggleSlider', !enabled);
      }
    }

    // REQ-4：缺任一入网硬条件时给出明确、非阻塞指引。
    // 注意：remaining_quota > 0 已不再是硬条件，仅作为闲置提醒信号（见 maybeShowIdleCapacityHint）。
    // 入网主硬条件 = 已配置至少一个「共享(shared)」类型的 Key。
    function showJoinConditionGuidance(d) {
      const missing = [];
      if (!d.has_provider) missing.push('配置至少一个 Provider Token');
      if (!d.has_quota_manager) missing.push('在额度管理中开启额度管理');
      if (!d.has_shared_key) missing.push('请先在 Provider 中至少设置一个「共享(shared)」类型的 Key');
      const msg = '尚不满足加入共享网络的条件，请先：\n• ' + missing.join('\n• ');
      toast(msg, 'warning');
    }

    // 闲置额度提醒（非阻塞）：尚未加入网络、但本月仍有剩余额度时，温和提示一次。
    // 通过会话级标志位避免每次刷新都弹窗骚扰（对应 PRD REQ-13「dismiss 后不骚扰」）。
    function maybeShowIdleCapacityHint() {
      if (localStorage.getItem('idle_quota_dismissed_' + new Date().toISOString().slice(0,7))) return;
      const s = networkStatus;
      if (!s || s.network_enabled) return;
      try {
        authFetch('/api/network/idle-quota')
          .then((r) => r.json())
          .then((d) => {
            if (d && d.should_notify && !s.network_enabled) {
              showIdleQuotaBanner(d.message);
            }
          })
          .catch(() => {});
      } catch (e) {}
    }

    function showIdleQuotaBanner(message) {
      let existing = document.getElementById('idleQuotaBanner');
      if (existing) return;
      const banner = document.createElement('div');
      banner.id = 'idleQuotaBanner';
      banner.style.cssText = 'position:fixed;top:16px;right:16px;z-index:10000;max-width:420px;padding:16px 20px;background:linear-gradient(135deg,#6c63ff,#48c6ef);color:#fff;border-radius:12px;box-shadow:0 4px 24px rgba(0,0,0,.25);font-size:14px;line-height:1.5;animation:slideIn .3s ease';
      banner.innerHTML = '<div style="margin-bottom:10px">' + (message || '你本月还有闲置额度，可以考虑加入共享网络。') + '</div>'
        + '<div style="display:flex;gap:8px;justify-content:flex-end">'
        + '<button id="idleQuotaDismiss" style="padding:6px 14px;border:1px solid rgba(255,255,255,.5);background:transparent;color:#fff;border-radius:6px;cursor:pointer;font-size:13px">暂不</button>'
        + '<button id="idleQuotaJoin" style="padding:6px 14px;border:none;background:#fff;color:#6c63ff;border-radius:6px;cursor:pointer;font-weight:600;font-size:13px">了解/加入</button>'
        + '</div>';
      document.body.appendChild(banner);
      document.getElementById('idleQuotaDismiss').onclick = function() {
        banner.remove();
        localStorage.setItem('idle_quota_dismissed_' + new Date().toISOString().slice(0,7), '1');
      };
      document.getElementById('idleQuotaJoin').onclick = function() {
        banner.remove();
        showNetworkDisclaimer();
      };
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
      body: JSON.stringify({ invitee_name: '', type: 'public', expires_hours: 72 })
    });
    const d = await r.json();
    if (d.encoded) {
      const el = document.getElementById('fedInviteCode');
      if (el) el.value = d.encoded;
      toast('✅ 邀请码已生成');
      copyToClipboard(d.encoded);
    } else {
      toast('生成失败: ' + (extractError(d) || '未知错误'), 'error');
    }
  } catch(e) { toast('生成失败: ' + e.message, 'error'); }
}

// addNetworkPeer implements P0-2 manual peer linking. It posts the operator-
// supplied address (and optional node_id) to POST /api/network/peers. The
// backend accepts an empty node_id and resolves it from the peer's public
// heartbeat endpoint, so operators only need to paste the other node's URL.
async function addNetworkPeer() {
  const addrEl = document.getElementById('peerAddr');
  const nodeIdEl = document.getElementById('peerNodeId');
  const addr = addrEl ? addrEl.value.trim() : '';
  const nodeId = nodeIdEl ? nodeIdEl.value.trim() : '';
  if (!addr) {
    toast('请填写对方地址', 'error');
    return;
  }
  if (!validatePeerAddress(addr)) {
    toast('地址必须以 http:// 或 https:// 开头', 'error');
    return;
  }
  try {
    const r = await authFetch('/api/network/peers', {
      method: 'POST',
      body: JSON.stringify({ addresses: [addr], node_id: nodeId, name: addr })
    });
    const d = await r.json();
    if (r.ok) {
      toast('✅ 节点已添加', 'success');
      if (addrEl) addrEl.value = '';
      if (nodeIdEl) nodeIdEl.value = '';
      await loadNetworkStatus();
    } else {
      toast('添加失败: ' + (extractError(d) || '未知错误'), 'error');
    }
  } catch(e) { toast('添加失败: ' + e.message, 'error'); }
}

// redeemInvite implements P1-2 invite redeeming. It verifies the pasted invite
// code (GET-free, rate-limited /api/federation/invites/verify) and, if valid,
// joins the inviter's network via the rate-limited /api/federation/join using
// this node's own identity and the verified network id.
async function redeemInvite() {
  const codeEl = document.getElementById('redeemCode');
  const code = codeEl ? codeEl.value.trim() : '';
  if (!code) {
    toast('请粘贴对方邀请码', 'error');
    return;
  }
  try {
    // Step 1: verify the invite code.
    const vRes = await authFetch('/api/federation/invites/verify', {
      method: 'POST',
      body: JSON.stringify({ encoded: code })
    });
    const v = await vRes.json();
    if (!vRes.ok || !v.valid) {
      toast('邀请码无效: ' + (extractError(v) || v.reason || '未知错误'), 'error');
      return;
    }

    // Step 2: gather this node's identity for the join request.
    let nodeId = '';
    let pubKey = '';
    try {
      const sRes = await authFetch('/api/network/status');
      const s = await sRes.json();
      nodeId = (s && s.node_id) || (s && s.node && s.node.id) || '';
      pubKey = (s && s.node && s.node.pub_key) || '';
    } catch(e) { /* fall back to cached networkStatus below */ }
    if (!nodeId && typeof networkStatus !== 'undefined' && networkStatus) {
      nodeId = networkStatus.node_id || (networkStatus.node && networkStatus.node.id) || '';
      pubKey = pubKey || (networkStatus.node && networkStatus.node.pub_key) || '';
    }
    if (!nodeId) {
      toast('未能获取本节点身份，请确认已加入共享网络', 'error');
      return;
    }

    // Step 3: join the inviter's network.
    const jRes = await authFetch('/api/federation/join', {
      method: 'POST',
      body: JSON.stringify({
        network_id: v.network,
        node_id: nodeId,
        pub_key: pubKey,
        endpoint: window.location.origin,
        invite_sig: code
      })
    });
    const j = await jRes.json();
    if (jRes.ok && j.accepted) {
      toast('✅ 已加入对方网络', 'success');
      if (codeEl) codeEl.value = '';
      await loadNetworkStatus();
    } else {
      toast('加入失败: ' + (extractError(j) || j.reason || '未知错误'), 'error');
    }
  } catch(e) { toast('加入失败: ' + e.message, 'error'); }
}

// ============================================================
// Capability Claims Panel (REQ-9)
// ============================================================

async function loadCapabilityClaims() {
  const el = document.getElementById('capabilityClaimsPanel');
  if (!el) return;
  try {
    const r = await authFetch('/api/network/capability/claims');
    const d = await r.json();
    const claims = d.claims || [];
    if (claims.length === 0) {
      el.innerHTML = '<div style="color:var(--text-muted);font-size:12px;padding:8px">暂无能力声明</div>';
      return;
    }
    let html = '<table style="width:100%;font-size:12px;border-collapse:collapse">';
    html += '<tr style="border-bottom:1px solid var(--border)"><th style="text-align:left;padding:4px 8px">节点</th><th style="text-align:left;padding:4px 8px">模型</th><th style="text-align:left;padding:4px 8px">Provider</th><th style="text-align:left;padding:4px 8px">时间</th></tr>';
    for (const c of claims) {
      const shortId = c.peer_id ? (c.peer_id.length > 16 ? c.peer_id.slice(0, 16) + '...' : c.peer_id) : '-';
      const models = (c.models || []).slice(0, 3).join(', ') + ((c.models || []).length > 3 ? '...' : '');
      const providers = (c.providers || []).slice(0, 2).join(', ') + ((c.providers || []).length > 2 ? '...' : '');
      const ts = c.timestamp ? new Date(c.timestamp).toLocaleDateString() : '-';
      html += '<tr style="border-bottom:1px solid var(--border)"><td style="padding:4px 8px" title="' + (c.peer_id||'') + '">' + shortId + '</td><td style="padding:4px 8px">' + models + '</td><td style="padding:4px 8px">' + providers + '</td><td style="padding:4px 8px">' + ts + '</td></tr>';
    }
    html += '</table>';
    el.innerHTML = html;
  } catch(e) {
    el.innerHTML = '<div style="color:var(--text-muted);font-size:12px;padding:8px">加载失败</div>';
  }
}

async function submitCapabilityClaim() {
  const modelsEl = document.getElementById('claimModelsInput');
  const providersEl = document.getElementById('claimProvidersInput');
  if (!modelsEl || !modelsEl.value.trim()) {
    toast('请输入至少一个模型名称', 'warning');
    return;
  }
  const models = modelsEl.value.trim().split(/[,，\s]+/).filter(Boolean);
  const providers = providersEl ? providersEl.value.trim().split(/[,，\s]+/).filter(Boolean) : [];
  try {
    const r = await authFetch('/api/network/capability/claim', {
      method: 'POST',
      body: JSON.stringify({ peer_id: '', models, providers })
    });
    const d = await r.json();
    if (r.ok) {
      toast('能力声明已提交', 'success');
      modelsEl.value = '';
      if (providersEl) providersEl.value = '';
      loadCapabilityClaims();
    } else {
      toast('提交失败: ' + (d.error || '未知错误'), 'error');
    }
  } catch(e) {
    toast('提交失败: ' + e.message, 'error');
  }
}

// ============================================================
// Contribution Ledger Panel (REQ-11)
// ============================================================

async function loadContributionLedger() {
  const el = document.getElementById('contributionLedgerPanel');
  if (!el) return;
  try {
    const nodeId = (typeof networkStatus !== 'undefined' && networkStatus && networkStatus.node_id) || '';
    const [balanceR, txsR] = await Promise.all([
      authFetch('/api/network/ledger/balance/' + (nodeId || 'self')),
      authFetch('/api/network/ledger/transactions')
    ]);
    const balance = await balanceR.json();
    const txs = await txsR.json();

    const bal = balance.balance || 0;
    const chainValid = txs.chain_valid !== false;
    const totalTxs = txs.total || 0;

    let html = '<div style="display:flex;gap:16px;flex-wrap:wrap;margin-bottom:8px">';
    html += '<div style="font-size:12px">积分余额: <strong>' + bal.toLocaleString() + '</strong></div>';
    html += '<div style="font-size:12px">交易笔数: ' + totalTxs + '</div>';
    html += '<div style="font-size:12px">链完整性: ' + (chainValid ? '<span style="color:var(--success)">有效</span>' : '<span style="color:var(--danger)">异常</span>') + '</div>';
    html += '</div>';

    const recentTxs = (txs.transactions || []).slice(-5).reverse();
    if (recentTxs.length > 0) {
      html += '<table style="width:100%;font-size:11px;border-collapse:collapse">';
      html += '<tr style="border-bottom:1px solid var(--border)"><th style="text-align:left;padding:3px 6px">类型</th><th style="text-align:left;padding:3px 6px">模型</th><th style="text-align:left;padding:3px 6px">数量</th><th style="text-align:left;padding:3px 6px">时间</th></tr>';
      for (const tx of recentTxs) {
        const typeLabel = tx.type === 'contribution' ? '贡献' : '消费';
        const typeColor = tx.type === 'contribution' ? 'var(--success)' : 'var(--warning)';
        html += '<tr style="border-bottom:1px solid var(--border)"><td style="padding:3px 6px;color:' + typeColor + '">' + typeLabel + '</td><td style="padding:3px 6px">' + (tx.model_id || '-') + '</td><td style="padding:3px 6px">' + (tx.amount || 0).toLocaleString() + '</td><td style="padding:3px 6px">' + (tx.timestamp ? new Date(tx.timestamp).toLocaleString() : '-') + '</td></tr>';
      }
      html += '</table>';
    } else {
      html += '<div style="color:var(--text-muted);font-size:12px">暂无交易记录</div>';
    }
    el.innerHTML = html;
  } catch(e) {
    el.innerHTML = '<div style="color:var(--text-muted);font-size:12px">加载失败</div>';
  }
}

// ============================================================
// Share Boundary Config (REQ-12)
// ============================================================

async function loadShareBoundary() {
  try {
    const r = await authFetch('/api/network/status');
    const d = await r.json();
    const b = d.share_boundary || {};
    const capEl = document.getElementById('dailyContribCap');
    const idleEl = document.getElementById('shareIdleOnlyToggle');
    const wlEl = document.getElementById('modelWhitelistInput');
    if (capEl) capEl.value = b.daily_contrib_cap || 0;
    if (idleEl) {
      idleEl.checked = !!b.share_idle_only;
      updateToggleSlider('shareIdleOnlyToggle', 'shareIdleOnlyToggleSlider', !!b.share_idle_only);
    }
    if (wlEl && b.model_whitelist) wlEl.value = b.model_whitelist.join(', ');
  } catch(e) {
    console.warn('load share boundary failed', e);
  }
}

async function saveShareBoundary() {
  const cap = parseInt(document.getElementById('dailyContribCap')?.value || '0');
  const idleOnly = document.getElementById('shareIdleOnlyToggle')?.checked || false;
  const wlStr = document.getElementById('modelWhitelistInput')?.value || '';
  const whitelist = wlStr.trim() ? wlStr.trim().split(/[,，\s]+/).filter(Boolean) : [];
  try {
    const r = await authFetch('/api/network/config', {
      method: 'PUT',
      body: JSON.stringify({
        share_boundary: {
          daily_contrib_cap: cap,
          share_idle_only: idleOnly,
          model_whitelist: whitelist
        }
      })
    });
    if (r.ok) {
      toast('共享边界已保存', 'success');
    } else {
      const d = await r.json();
      toast('保存失败: ' + (d.error || '未知错误'), 'error');
    }
  } catch(e) {
    toast('保存失败: ' + e.message, 'error');
  }
}

