// admin-settings.js — Settings, domain binding, proxy API key, SMTP, account

// Set personal API access URLs
// ===== Proxy API Key =====
function generateApiKey() {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  let key = 'sk-';
  for (let i = 0; i < 48; i++) key += chars.charAt(Math.floor(Math.random() * chars.length));
  document.getElementById('proxyApiKey').value = key;
}

async function saveProxyApiKey() {
  const key = document.getElementById('proxyApiKey').value.trim();
  if (key) {
    await authFetch('/api/config', { method: 'POST', body: JSON.stringify({ proxy_api_key: key }) });
    toast('✅ API Key 认证已启用');
  } else {
    toast('请输入 API Key');
    return;
  }
  document.getElementById('proxyApiKey').value = '';
  await loadProxyApiKey();
}

async function clearProxyApiKey() {
  await authFetch('/api/config', { method: 'POST', body: JSON.stringify({ proxy_api_key: '' }) });
  document.getElementById('proxyApiKey').value = '';
  document.getElementById('proxyApiKey').placeholder = '留空表示不启用认证';
  toast('已关闭 API Key 认证');
}

async function loadProxyApiKey() {
  try {
    const r = await authFetch('/api/config');
    const d = await r.json();
    const el = document.getElementById('proxyApiKey');
    el.value = '';
    if (d.proxy_api_key_masked && d.proxy_api_key_masked !== '未配置') {
      el.placeholder = `当前: ${d.proxy_api_key_masked}（输入新值覆盖）`;
    } else {
      el.placeholder = '留空表示不启用认证';
    }
  } catch(e) {}
}

// ===== SMTP =====
// ================================================================
// Module: Settings - SMTP, admin info, proxy API key configuration
// ================================================================
async function loadSmtp() {
  try { const r = await authFetch('/api/smtp/config'); const d = await r.json(); document.getElementById('smtpHost').value = d.host||''; document.getElementById('smtpPort').value = d.port||'587'; document.getElementById('smtpUser').value = d.username||''; document.getElementById('smtpFrom').value = d.from_email||''; document.getElementById('smtpTls').checked = d.use_tls!==false; const passEl = document.getElementById('smtpPass'); if (d.password && d.password === '****') { passEl.value = ''; passEl.placeholder = '•••••••• 已配置，留空保持不变'; } else { passEl.placeholder = 'SMTP 密码'; } } catch(e) {}
}

async function saveSmtp() {
  const data = { host:document.getElementById('smtpHost').value, port:parseInt(document.getElementById('smtpPort').value)||587, username:document.getElementById('smtpUser').value, from_email:document.getElementById('smtpFrom').value, use_tls:document.getElementById('smtpTls').checked };
  const p = document.getElementById('smtpPass').value; if (p) data.password = p;
  try {
    const r = await authFetch('/api/smtp/config', { method: 'POST', body: JSON.stringify(data) });
    const d = await r.json();
    if (d.success) { toast('✅ SMTP 配置已保存', 'success'); await loadSmtp(); }
    else { toast('❌ ' + (d.error || '保存失败'), 'error'); }
  } catch(e) { toast('保存失败: ' + e.message, 'error'); }
}

async function testSmtp() {
  toast('📧 发送测试邮件...', 'info');
  try {
    const r = await authFetch('/api/smtp/test', {method:'POST', body: JSON.stringify({})});
    const d = await r.json();
    if (d.success) toast('✅ ' + d.message, 'success');
    else toast('❌ ' + (d.detail || d.error || d.message || '发送失败'), 'error');
  } catch(e) { toast('❌ 发送失败: ' + e.message, 'error'); }
}

// ===== Account =====
async function loadAdminInfo() { try { const r = await authFetch('/api/admin/info'); const d = await r.json(); document.getElementById('adminEmail').value = d.email||''; } catch(e) {} }

async function updateEmail() { const e = document.getElementById('adminEmail').value.trim(); if (!e) return toast('邮箱不能为空','error'); await authFetch('/api/admin/update-email', {method:'POST', body:JSON.stringify({email:e})}); toast('邮箱已更新'); }

async function changePassword() { const o = document.getElementById('oldPass').value; const n = document.getElementById('newPass').value; if (!o||!n) return toast('请填写密码','error'); try { const r = await authFetch('/api/admin/change-password', {method:'POST', body:JSON.stringify({old_password:o,new_password:n})}); const d = await r.json(); toast(d.success?'✅ 密码已修改':'❌ '+d.detail, d.success?'success':'error'); document.getElementById('oldPass').value=''; document.getElementById('newPass').value=''; } catch(e) { toast(e.message,'error'); } }

async function exportConfig() {
  try {
    const r = await authFetch('/api/config/export');
    if (!r.ok) throw new Error('导出失败');
    const data = await r.json();
    const blob = new Blob([JSON.stringify(data, null, 2)], {type: 'application/json'});
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    const ts = new Date().toISOString().slice(0,10);
    a.download = `openmodelpool-config-${ts}.json`;
    a.click();
    URL.revokeObjectURL(url);
    toast('✅ 配置已导出', 'success');
  } catch(e) { toast('❌ '+e.message, 'error'); }
}

async function importConfig(input) {
  const file = input.files[0];
  if (!file) return;
  if (!confirm(`确认导入 ${file.name}？\n这将覆盖当前所有平台和配置！`)) {
    input.value = '';
    return;
  }
  try {
    const formData = new FormData();
    formData.append('config', file);
    const r = await authFetch('/api/config/import', {method:'POST', body:formData});
    const d = await r.json();
    toast(d.success ? `✅ 导入成功，已恢复 ${d.providers_count} 个平台` : '❌ '+d.detail, d.success?'success':'error');
    if (d.success) setTimeout(()=>location.reload(), 1500);
  } catch(e) { toast('❌ '+e.message, 'error'); }
  input.value = '';
}

    // Domain Binding Functions
    async function refreshDomainStatus() {
      try {
        const r = await authFetch('/api/domain/status');
        const d = await r.json();
        const el = document.getElementById('domainStatus');
        if (!el) return;
        el.style.display = 'block';
        if (d.bound) {
          el.innerHTML = '✅ 已绑定: <strong>' + escapeHtml(d.domain) + '</strong> | 公网地址: <a href="' + escapeAttr(d.public_url) + '" target="_blank">' + escapeHtml(d.public_url) + '</a> | Tunnel ID: ' + escapeHtml(d.tunnel_id);
          el.style.background = 'rgba(16,185,129,0.1)';
          document.getElementById('unbindDomainBtn').style.display = '';
          document.getElementById('cfDomain').value = d.domain;
        } else {
          el.innerHTML = '⚪ 当前未绑定固定域名，使用 Quick Tunnel（临时地址）';
          el.style.background = 'var(--bg-secondary)';
          document.getElementById('unbindDomainBtn').style.display = 'none';
        }
      } catch(e) { console.error(e); }
    }

    async function verifyDomainToken() {
      const token = document.getElementById('cfApiToken').value.trim();
      if (!token) { toast('请输入 API Token', 'error'); return; }
      const el = document.getElementById('tokenVerifyResult');
      el.innerHTML = '⏳ 验证中...';
      try {
        const r = await authFetch('/api/domain/verify', {method:'POST', body:JSON.stringify({api_token:token})});
        const d = await r.json();
        if (d.valid) {
          el.innerHTML = '✅ Token 有效，Account ID: ' + escapeHtml(d.account_id);
          el.style.color = 'var(--success-color, #10b981)';
        } else {
          el.innerHTML = '❌ Token 无效: ' + (extractError(d) || '未知错误');
          el.style.color = 'var(--danger-color, #ef4444)';
        }
      } catch(e) {
        el.innerHTML = '❌ 验证失败: ' + escapeHtml(e.message);
        el.style.color = 'var(--danger-color, #ef4444)';
      }
    }

    async function bindDomain() {
      const token = document.getElementById('cfApiToken').value.trim();
      const domain = document.getElementById('cfDomain').value.trim();
      if (!token) { toast('请输入 API Token', 'error'); return; }
      if (!domain) { toast('请输入要绑定的域名', 'error'); return; }
      if (!confirm('即将为域名 ' + domain + ' 创建 Cloudflare 隧道并配置 DNS，确认继续？')) return;
      
      const btn = document.getElementById('bindDomainBtn');
      btn.disabled = true;
      btn.textContent = '⏳ 绑定中...';
      
      try {
        const r = await authFetch('/api/domain/bind', {method:'POST', body:JSON.stringify({api_token:token, domain:domain})});
        const d = await r.json();
        if (d.success) {
          toast('🎉 域名绑定成功！公网地址: ' + d.public_url, 'success');
          refreshDomainStatus();
          // Also refresh the tunnel URL in the config section
          setTimeout(() => loadConfig(), 1000);
        } else {
          toast('绑定失败: ' + extractError(d), 'error');
        }
      } catch(e) {
        toast('绑定失败: ' + e.message, 'error');
      } finally {
        btn.disabled = false;
        btn.textContent = '🔗 一键绑定域名';
      }
    }

    async function unbindDomain() {
      if (!confirm('确定要解除域名绑定吗？将切回 Quick Tunnel（临时地址）。')) return;
      try {
        const r = await authFetch('/api/domain/unbind', {method:'POST'});
        const d = await r.json();
        if (d.success) {
          toast('已解除绑定，切回 Quick Tunnel', 'success');
          refreshDomainStatus();
          setTimeout(() => loadConfig(), 1000);
        }
      } catch(e) { toast('操作失败: ' + e.message, 'error'); }
    }
    // Load domain status on page load
    setTimeout(() => refreshDomainStatus(), 500);
    
    // ============================================================
    // Shared Network (P2P) UI
    // ============================================================
    let networkStatus = null;

// ===== API URL & Domain/IP Binding =====
// ===== API URL & Domain/IP Binding =====

function initApiUrls() {
  const lanEl = document.getElementById('lanApiUrl');
  const pubEl = document.getElementById('publicApiUrl');
  const hintEl = document.getElementById('publicUrlHint');
  const badgeEl = document.getElementById('publicUrlBadge');
  const domainUrlEl = document.getElementById('domainApiUrl');
  const domainHintEl = document.getElementById('domainUrlHint');
  const domainBadgeEl = document.getElementById('domainUrlBadge');
  const domainQuickCard = document.getElementById('domainBindingQuickCard');
  authFetch('/api/federation/config').then(r => r.json()).then(d => {
    const lanIP = d.lan_ip || '';
    const servicePort = d.service_port || '8000';
    const tunnelDomain = d.tunnel_domain || '';
    const boundIP = d.bound_ip || '';
    const boundPort = d.bound_port || '8000';
    // 1. 局域网地址
    if (lanEl) {
      if (lanIP) {
        lanEl.value = 'http://' + lanIP + ':' + servicePort + '/v1';
        lanEl.style.color = 'var(--text-primary)';
      } else {
        lanEl.value = '无法获取局域网IP';
        lanEl.style.color = 'var(--text-muted)';
      }
    }
    // 2. 公网地址（固定IP）
    if (boundIP) {
      if (pubEl) {
        pubEl.value = 'http://' + boundIP + ':' + boundPort + '/v1';
        pubEl.style.color = 'var(--text-primary)';
      }
      if (hintEl) hintEl.innerHTML = '✅ 固定公网IP地址，可<a href="javascript:void(0)" onclick="showBindMode(\'ip\')" style="color:var(--accent);text-decoration:underline">修改</a>';
      if (badgeEl) badgeEl.textContent = '(已绑定)';
    } else {
      if (pubEl) { pubEl.value = ''; pubEl.placeholder = '未绑定公网IP'; pubEl.style.color = 'var(--text-muted)'; }
      if (hintEl) hintEl.textContent = '在下方绑定您的公网IP获得固定地址';
      if (badgeEl) badgeEl.textContent = '';
    }
    // 3. 固定域名地址
    if (tunnelDomain) {
      const domainUrl = 'https://' + tunnelDomain + '/v1';
      if (domainUrlEl) {
        domainUrlEl.value = domainUrl;
        domainUrlEl.style.color = 'var(--accent-green)';
      }
      if (domainHintEl) domainHintEl.innerHTML = '✅ 已绑定域名，地址永久固定，可<a href="javascript:void(0)" onclick="showBindMode(\'domain\')" style="color:var(--accent);text-decoration:underline">修改</a>';
      if (domainBadgeEl) domainBadgeEl.textContent = '(已绑定)';
    } else {
      if (domainUrlEl) { domainUrlEl.value = ''; domainUrlEl.placeholder = '未绑定域名'; domainUrlEl.style.color = 'var(--text-muted)'; }
      if (domainHintEl) domainHintEl.textContent = '在下方绑定域名获得固定地址';
      if (domainBadgeEl) domainBadgeEl.textContent = '';
    }
    // 4. 绑定引导卡片
    if (domainQuickCard) {
      if (!boundIP || !tunnelDomain) {
        domainQuickCard.style.display = 'block';
        if (boundIP && !tunnelDomain) switchBindMode('domain');
        if (!boundIP && tunnelDomain) switchBindMode('ip');
      } else {
        domainQuickCard.style.display = 'none';
      }
    }
  }).catch(() => {
    if (hintEl) hintEl.textContent = '获取地址信息失败';
    if (domainHintEl) domainHintEl.textContent = '获取地址信息失败';
  });
}

function switchBindMode(mode) {
  const domainMode = document.getElementById('domainBindMode');
  const ipMode = document.getElementById('ipBindMode');
  const tabDomain = document.getElementById('tabDomainBtn');
  const tabIp = document.getElementById('tabIpBtn');
  if (mode === 'domain') {
    if (domainMode) domainMode.style.display = '';
    if (ipMode) ipMode.style.display = 'none';
    if (tabDomain) tabDomain.classList.add('btn-primary');
    if (tabDomain) tabDomain.classList.remove('btn-secondary');
    if (tabIp) tabIp.classList.add('btn-secondary');
    if (tabIp) tabIp.classList.remove('btn-primary');
  } else {
    if (domainMode) domainMode.style.display = 'none';
    if (ipMode) ipMode.style.display = '';
    if (tabDomain) tabDomain.classList.add('btn-secondary');
    if (tabDomain) tabDomain.classList.remove('btn-primary');
    if (tabIp) tabIp.classList.add('btn-primary');
    if (tabIp) tabIp.classList.remove('btn-secondary');
  }
}

function showBindMode(mode) {
  const quickCard = document.getElementById('domainBindingQuickCard');
  if (quickCard) {
    quickCard.style.display = 'block';
    quickCard.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }
  switchBindMode(mode);
}

async function quickBindDomain() {
  const domain = document.getElementById('quickDomainInput').value.trim();
  if (!domain) { toast('请输入域名', 'error'); return; }
  const result = document.getElementById('domainBindResult');
  result.innerHTML = '<span style="color:var(--text-muted)">⏳ 正在绑定域名...</span>';
  try {
    // Use manual bind (no API token needed — for domains already configured via deploy script or external cloudflared)
    const r = await authFetch('/api/domain/manual-bind', {
      method: 'POST',
      body: JSON.stringify({ domain: domain })
    });
    const d = await r.json();
    if (d.error) {
      result.innerHTML = '<span style="color:var(--accent-red)">❌ ' + escapeHtml(d.error) + '</span>';
      toast('绑定失败', 'error');
    } else {
      result.innerHTML = '<span style="color:var(--accent-green)">✅ 域名绑定成功！</span><br><span style="font-size:11px;color:var(--text-muted)">公网地址: ' + escapeHtml(d.public_url || '') + '</span>';
      toast('域名绑定成功', 'success');
      setTimeout(() => { initApiUrls(); refreshDomainStatus(); }, 2000);
    }
  } catch(e) {
    result.innerHTML = '<span style="color:var(--accent-red)">❌ ' + escapeHtml(e.message) + '</span>';
    toast('绑定失败', 'error');
  }
}

async function quickBindIp() {
  const ip = document.getElementById('quickIpInput').value.trim();
  if (!ip) { toast('请输入公网IP', 'error'); return; }
  const result = document.getElementById('domainBindResult');
  result.innerHTML = '<span style="color:var(--text-muted)">⏳ 正在绑定IP...</span>';
  try {
    const r = await authFetch('/api/ip/bind', {
      method: 'POST',
      body: JSON.stringify({ ip: ip, port: '8000' })
    });
    const d = await r.json();
    if (d.error) {
      result.innerHTML = '<span style="color:var(--accent-red)">❌ ' + escapeHtml(d.error) + '</span>';
      toast('绑定失败', 'error');
    } else {
      result.innerHTML = '<span style="color:var(--accent-green)">✅ IP 绑定成功！公网地址: ' + escapeHtml(d.url) + '</span>';
      toast('IP绑定成功', 'success');
      setTimeout(() => initApiUrls(), 1000);
    }
  } catch(e) {
    result.innerHTML = '<span style="color:var(--accent-red)">❌ ' + escapeHtml(e.message) + '</span>';
    toast('绑定失败', 'error');
  }
}

// ===== Discovery Functions =====
let discoveredPlatforms = [];

