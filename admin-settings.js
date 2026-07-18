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
          el.innerHTML = '✅ 已绑定: <strong>' + d.domain + '</strong> | 公网地址: <a href="' + d.public_url + '" target="_blank">' + d.public_url + '</a> | Tunnel ID: ' + d.tunnel_id;
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
          el.innerHTML = '✅ Token 有效，Account ID: ' + d.account_id;
          el.style.color = 'var(--success-color, #10b981)';
        } else {
          el.innerHTML = '❌ Token 无效: ' + extractError(d) || '未知错误';
          el.style.color = 'var(--danger-color, #ef4444)';
        }
      } catch(e) {
        el.innerHTML = '❌ 验证失败: ' + e.message;
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

    async function quickSetPublicUrl() {
      const url = document.getElementById('quickPublicUrlInput').value.trim();
      if (!url) { 
        toast('请输入完整的域名 URL', 'error'); 
        return; 
      }
      if (!url.startsWith('http://') && !url.startsWith('https://')) {
        toast('URL 必须以 http:// 或 https:// 开头', 'error');
        return;
      }
      
      const btn = document.getElementById('quickSetBtn');
      const result = document.getElementById('quickSetResult');
      btn.disabled = true;
      btn.textContent = '⏳ 处理中...';
      result.innerHTML = '';
      
      try {
        const r = await authFetch('/api/config', {
          method: 'POST',
          body: JSON.stringify({ public_url: url })
        });
        const d = await r.json();
        
        if (url.startsWith('https://')) {
          result.innerHTML = '<div style="padding:8px;background:rgba(46,204,113,.1);border-radius:6px;color:#2ecc71">' +
            '✅ 已保存！服务正在申请 Let\'s Encrypt 证书并启动 HTTPS...<br>' +
            '<span style="font-size:10px">DNS 生效后（通常 1-5 分钟），访问 <a href="' + url + '" target="_blank" style="color:#2ecc71">' + url + '</a> 验证</span>' +
            '</div>';
          // 显示解绑按钮
          document.getElementById('quickUnbindBtn').style.display = '';
          document.getElementById('quickSetBtn').style.display = 'none';
        } else {
          result.innerHTML = '<div style="padding:8px;background:rgba(46,204,113,.1);border-radius:6px;color:#2ecc71">' +
            '✅ 已保存！服务将使用 HTTP 模式（适合隧道场景）' +
            '</div>';
        }
        
        toast('配置已保存', 'success');
        setTimeout(() => loadConfig(), 1000);
        
      } catch(e) {
        result.innerHTML = '<div style="padding:8px;background:rgba(231,76,60,.1);border-radius:6px;color:#e74c3c">❌ 保存失败: ' + e.message + '</div>';
        toast('保存失败', 'error');
      } finally {
        btn.disabled = false;
        btn.textContent = '保存并启用';
      }
    }

    async function quickUnbindDomain() {
      if (!confirm('确定要解绑当前域名吗？服务将切换到 HTTP 模式（或隧道模式）。')) {
        return;
      }
      
      const btn = document.getElementById('quickUnbindBtn');
      const result = document.getElementById('quickSetResult');
      btn.disabled = true;
      btn.textContent = '⏳ 解绑中...';
      result.innerHTML = '';
      
      try {
        const r = await authFetch('/api/config', {
          method: 'POST',
          body: JSON.stringify({ public_url: '' })
        });
        const d = await r.json();
        
        result.innerHTML = '<div style="padding:8px;background:rgba(52,152,219,.1);border-radius:6px;color:#3498db">' +
          '✅ 已解绑域名！服务正在切换到 HTTP 模式...<br>' +
          '<span style="font-size:10px">如需重新绑定，请在上方输入新的域名 URL</span>' +
          '</div>';
        
        toast('域名已解绑', 'success');
        
        // 隐藏解绑按钮，显示绑定按钮
        document.getElementById('quickUnbindBtn').style.display = 'none';
        document.getElementById('quickSetBtn').style.display = '';
        document.getElementById('quickPublicUrlInput').value = '';
        
        setTimeout(() => {
          loadConfig();
        }, 1000);
        
      } catch(e) {
        result.innerHTML = '<div style="padding:8px;background:rgba(231,76,60,.1);border-radius:6px;color:#e74c3c">❌ 解绑失败: ' + e.message + '</div>';
        toast('解绑失败', 'error');
      } finally {
        btn.disabled = false;
        btn.textContent = '解绑域名';
      }
    }
    // 页面加载时检查当前是否有绑定域名
    setTimeout(() => {
      authFetch('/api/config').then(r => r.json()).then(d => {
        const publicUrl = d.public_url || '';
        const input = document.getElementById('quickPublicUrlInput');
        const bindBtn = document.getElementById('quickSetBtn');
        const unbindBtn = document.getElementById('quickUnbindBtn');
        
        if (publicUrl && publicUrl.startsWith('https://')) {
          // 已绑定 HTTPS 域名
          if (input) input.value = publicUrl;
          if (bindBtn) bindBtn.style.display = 'none';
          if (unbindBtn) unbindBtn.style.display = '';
        } else {
          // 未绑定或 HTTP 模式
          if (input) input.value = publicUrl || '';
          if (bindBtn) bindBtn.style.display = '';
          if (unbindBtn) unbindBtn.style.display = 'none';
        }
      }).catch(() => {});
    }, 500);
// ===== Discovery Functions =====
let discoveredPlatforms = [];

