// admin-share.js — Share center, guest keys, QR codes

function generateQRCode(containerId, text) {
  const container = document.getElementById(containerId);
  if (!container) return;
  container.innerHTML = '';
  try {
    new QRCode(container, {
      text: text,
      width: 128,
      height: 128,
      colorDark: "#6c63ff",
      colorLight: "#ffffff",
      correctLevel: QRCode.CorrectLevel.H
    });
  } catch(e) { console.error('QR code generation failed:', e); }
}

async function loadShareInfo() {
  try {
    const r = await authFetch('/api/share/info');
    shareInfoData = await r.json();
  } catch(e) { console.error('Failed to load share info:', e); }
}

    function getShareApiUrl() {
      // Priority: current domain (if public) > backend public_api_url > proxy_api_url > fallback
      const host = window.location.host;
      const isPublicDomain = host && !host.startsWith('localhost') && !host.startsWith('127.0.0.1') && !host.match(/^\d+\.\d+\.\d+\.\d+$/);
      if (isPublicDomain) {
        return window.location.origin + '/v1';
      }
      return (shareInfoData && shareInfoData.public_api_url) || (shareInfoData && shareInfoData.proxy_api_url) || window.location.origin + '/v1';
    }

function copyText(text) {
  if (!text) return toast('没有可复制的内容', 'error');
  function doFallbackCopy(t) {
    var ta = document.createElement('textarea');
    ta.value = t;
    ta.style.cssText = 'position:fixed;left:-9999px;top:-9999px';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    try {
      document.execCommand('copy');
      toast('已复制到剪贴板');
    } catch(e) {
      toast('复制失败，请手动复制', 'error');
    }
    document.body.removeChild(ta);
  }
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(function() {
      toast('已复制到剪贴板');
    }).catch(function() {
      doFallbackCopy(text);
    });
  } else {
    doFallbackCopy(text);
  }
}

    // Load algorithm params when network status loads
    async function loadGuestKeys() {
      const el = document.getElementById('guestKeysList');
      if (!el) return;
      try {
        const res = await authFetch('/api/network/guest-keys');
        if (!res.ok) {
          el.innerHTML = '<span style="color:var(--error)">加载失败 <button class="btn btn-secondary btn-sm" onclick="loadGuestKeys()" style="font-size:10px;padding:2px 8px">重试</button></span>';
          return;
        }
        const data = await res.json();
        let keys = data.keys || [];
        // Sort by issued_at descending (newest first)
        keys.sort((a, b) => {
          const ta = a.issued_at || '';
          const tb = b.issued_at || '';
          return tb.localeCompare(ta);
        });
        // Filter by type
        if (_shareFilter && _shareFilter !== 'all') {
          keys = keys.filter(k => {
            const note = k.note || '';
            const isCollab = note.startsWith('[协作]');
            return _shareFilter === 'collaborator' ? isCollab : !isCollab;
          });
        }
        if (keys.length === 0) {
          const filterText = _shareFilter === 'consumer' ? '使用者' : _shareFilter === 'collaborator' ? '协作者' : '';
          el.innerHTML = '<div style="color:var(--text-muted);text-align:center;padding:16px">' + (filterText ? '暂无' + filterText + '类型的 Key' : '暂无 Guest Key') + '，点击上方按钮创建<br><span style="font-size:10px;margin-top:4px;display:block">💡 公共池额度全网均分，本地额度可逐 Key 设定</span></div>';
          return;
        }
        el.innerHTML = keys.map(k => {
          const keyVal = k.key || k;
          const revoked = !!k.revoked;
          const note = k.note || '';
          const isCollab = note.startsWith('[协作]');
          const displayNote = isCollab ? note.replace(/^\[协作\]\s*/, '') : note;
          const displayName = displayNote || ('🔑 ' + keyVal.substring(0, 20) + '...');

          const typeBadge = isCollab
            ? '<span style="font-size:10px;padding:1px 6px;background:rgba(99,102,241,.12);color:#6366f1;border-radius:4px">🤝 协作者</span>'
            : '<span style="font-size:10px;padding:1px 6px;background:rgba(108,99,255,.12);color:var(--accent-start);border-radius:4px">🔑 使用者</span>';
          const parts = [];
          if (k.quota > 0) parts.push(k.quota.toLocaleString() + '/天');
          if (k.quota_hourly > 0) parts.push(k.quota_hourly.toLocaleString() + '/时');
          if (k.quota_per_request > 0) parts.push(k.quota_per_request.toLocaleString() + '/次');
          if (k.rpm > 0) parts.push(k.rpm + ' RPM');
          const quota = parts.length > 0 ? parts.join(' · ') : '不限';
          const issuedAt = k.issued_at ? k.issued_at.substring(0, 10) : '';
          const expires = k.expires_at ? k.expires_at.substring(0, 10) : '永久';
          const statusBadge = revoked
            ? '<span style="font-size:10px;padding:1px 6px;background:rgba(239,68,68,.12);color:#ef4444;border-radius:4px">❌ 已撤销</span>'
            : '<span style="font-size:10px;padding:1px 6px;background:rgba(34,197,94,.12);color:#22c55e;border-radius:4px">✅ 有效</span>';
          const safeKey = keyVal.replace(/'/g, "\\'");
          const escapedKey = keyVal.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
          const dateInfo = issuedAt ? '创建于 ' + issuedAt + (expires !== '永久' ? ' · 到期 ' + expires : ' · 永久有效') : (expires !== '永久' ? '到期 ' + expires : '永久有效');
          return `
          <div data-gk-key="${escapedKey}" style="padding:12px 16px;margin-bottom:8px;background:var(--bg-secondary);border-radius:10px;border:1px solid ${revoked ? 'rgba(239,68,68,.2)' : 'var(--border-color, rgba(255,255,255,.1))'};box-shadow:0 2px 8px rgba(0,0,0,.1);transition:all .2s ease;cursor:default;${revoked ? 'opacity:0.55;' : ''}">
            <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:4px">
              <div style="font-size:12px;font-weight:500;display:flex;align-items:center;gap:6px;min-width:0;flex:1">
                <span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">🏷️ ${displayName}</span>
                ${typeBadge}
              </div>
              <div style="display:flex;align-items:center;gap:4px;flex-shrink:0;margin-left:8px">
                ${statusBadge}
              </div>
            </div>
            <div style="display:flex;justify-content:space-between;align-items:center">
              <div style="color:var(--text-muted);font-size:10px">${dateInfo} · ${quota}</div>
              <div style="display:flex;gap:4px;flex-shrink:0">
                <button class="btn btn-secondary btn-sm" onclick="copyText('${safeKey}')" title="复制 Key" style="font-size:10px;padding:3px 8px">📋</button>
                ${!revoked ? `<button class="btn btn-primary btn-sm" onclick="shareExistingKey('${safeKey}')" title="分享" style="font-size:10px;padding:3px 8px">📤 分享</button>` : ''}
                ${!revoked ? `<button class="btn btn-secondary btn-sm" onclick="editGuestKeyQuota('${safeKey}')" title="编辑额度" style="font-size:10px;padding:3px 8px">✏️额度</button>` : ''}
                ${!revoked ? `<button class="btn btn-secondary btn-sm" onclick="editGuestKeyAdvanced('${safeKey}')" title="高级编辑" style="font-size:10px;padding:3px 8px">⚙️高级</button>` : ''}
                ${!revoked ? `<button class="btn btn-danger btn-sm" onclick="revokeGuestKey('${safeKey}')" title="撤销" style="font-size:10px;padding:3px 8px">撤销</button>` : ''}
                ${revoked ? `<button class="btn btn-danger btn-sm" onclick="deleteGuestKey('${safeKey}')" title="永久删除" style="font-size:10px;padding:3px 8px">🗑️ 删除</button>` : ''}
              </div>
            </div>
          </div>`;
        }).join('');
      } catch(e) {
        el.innerHTML = '<span style="color:var(--error)">加载失败: ' + e.message + ' <button class="btn btn-secondary btn-sm" onclick="loadGuestKeys()" style="font-size:10px;padding:2px 8px">重试</button></span>';
      }
    }

    async function editGuestKeyQuota(key) {
      const panelId = 'gkEdit_' + key.replace(/[^a-zA-Z0-9]/g, '_');
      const existing = document.getElementById(panelId);
      if (existing) { existing.remove(); return; }
      let rec = {};
      try {
        const res = await authFetch('/api/network/guest-keys');
        const data = await res.json();
        rec = (data.keys || []).find(k => k.key === key) || {};
      } catch(e) {}
      const note = rec.note || key.substring(0, 16) + '...';
      const panel = document.createElement('div');
      panel.id = panelId;
      panel.style.cssText = 'margin:4px 0 8px 0;padding:14px;background:rgba(108,99,255,.06);border:1px solid rgba(108,99,255,.2);border-radius:8px';
      panel.innerHTML = `
        <div style="font-size:12px;font-weight:600;margin-bottom:10px">✏️ 编辑额度: ${note}</div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;margin-bottom:10px">
          <div>
            <label style="font-size:11px;color:var(--text-muted);display:block;margin-bottom:3px">每日上限 (tokens)</label>
            <input id="${panelId}_quota" class="form-input" type="number" value="${rec.quota||0}" min="0" style="font-size:12px;height:32px">
          </div>
          <div>
            <label style="font-size:11px;color:var(--text-muted);display:block;margin-bottom:3px">每小时上限 (tokens)</label>
            <input id="${panelId}_hourly" class="form-input" type="number" value="${rec.quota_hourly||0}" min="0" style="font-size:12px;height:32px">
          </div>
          <div>
            <label style="font-size:11px;color:var(--text-muted);display:block;margin-bottom:3px">单次上限 (tokens)</label>
            <input id="${panelId}_perReq" class="form-input" type="number" value="${rec.quota_per_request||0}" min="0" style="font-size:12px;height:32px">
          </div>
          <div>
            <label style="font-size:11px;color:var(--text-muted);display:block;margin-bottom:3px">每分钟请求数 (RPM)</label>
            <input id="${panelId}_rpm" class="form-input" type="number" value="${rec.rpm||0}" min="0" style="font-size:12px;height:32px">
          </div>
        </div>
        <div style="display:flex;gap:8px;justify-content:flex-end">
          <button class="btn btn-secondary btn-sm" onclick="document.getElementById('${panelId}').remove()" style="font-size:11px;padding:4px 12px">取消</button>
          <button class="btn btn-primary btn-sm" onclick="saveGuestKeyQuota('${key.replace(/'/g, "\\'")}', '${panelId}')" style="font-size:11px;padding:4px 12px">💾 保存</button>
        </div>`;
      const keyDiv = document.querySelector('[data-gk-key="' + CSS.escape(key) + '"]');
      if (keyDiv) {
        keyDiv.insertAdjacentElement('afterend', panel);
      } else {
        document.getElementById('guestKeysList').insertAdjacentElement('beforebegin', panel);
      }
    }

    async function saveGuestKeyQuota(key, panelId) {
      const body = {
        quota: parseInt(document.getElementById(panelId + '_quota').value, 10) || 0,
        quota_hourly: parseInt(document.getElementById(panelId + '_hourly').value, 10) || 0,
        quota_per_request: parseInt(document.getElementById(panelId + '_perReq').value, 10) || 0,
        rpm: parseInt(document.getElementById(panelId + '_rpm').value, 10) || 0
      };
      try {
        const res = await authFetch('/api/network/guest-keys/' + encodeURIComponent(key) + '/quota', {
          method: 'PUT',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(body)
        });
        if (res.ok) {
          toast('额度已更新', 'success');
          document.getElementById(panelId).remove();
          await loadGuestKeys();
        } else {
          const err = await res.json().catch(() => ({}));
          toast('更新失败: ' + extractError(err), 'error');
        }
      } catch(e) {
        toast('更新失败: ' + e.message, 'error');
      }
    }

    async function generateGuestKey() {
      if (document.getElementById('guestKeyForm')) {
        document.getElementById('guestKeyForm').remove();
        return;
      }
      const purposeLabel = '🔑 创建 Guest Key';
      const purposeColor = 'rgba(108,99,255,.06)';
      const purposeBorder = 'rgba(108,99,255,.2)';
      const formHtml = `
        <div id="guestKeyForm" style="padding:16px;margin-bottom:8px;background:${purposeColor};border:1px solid ${purposeBorder};border-radius:10px">
          <div style="font-size:14px;font-weight:600;margin-bottom:14px;display:flex;align-items:center;gap:6px">${purposeLabel}</div>
          <div style="margin-bottom:14px;padding:12px;background:rgba(108,99,255,.04);border:1px solid rgba(108,99,255,.12);border-radius:8px">
            <label style="font-size:12px;font-weight:500;display:block;margin-bottom:4px">备注名称 <span style="color:var(--error)">*</span></label>
            <input id="gkNote" class="form-input" style="font-size:12px;height:34px;width:100%;box-sizing:border-box" placeholder="如：给小明的试用 Key">
            <div style="font-size:10px;color:var(--text-muted);margin-top:4px">💡 给 Key 起个名字方便后续管理</div>
          </div>
          <div style="margin-bottom:14px;padding:12px;background:rgba(108,99,255,.04);border:1px solid rgba(108,99,255,.12);border-radius:8px">
            <div style="margin-bottom:10px">
              <label style="font-size:12px;font-weight:500;display:flex;align-items:center;gap:6px;cursor:pointer">
                <input type="checkbox" id="gkUnlimited" onchange="document.getElementById('gkQuota').disabled=this.checked;document.getElementById('gkQuotaHourly').disabled=this.checked;if(this.checked){document.getElementById('gkQuota').value=0;document.getElementById('gkQuotaHourly').value=0;}" style="width:15px;height:15px;cursor:pointer">
                不限额度
              </label>
            </div>
            <div style="margin-bottom:10px">
              <label style="font-size:12px;font-weight:500;display:block;margin-bottom:4px">每日上限 (tokens)</label>
              <input id="gkQuota" class="form-input" type="number" value="50000" min="0" style="font-size:12px;height:34px;width:100%;box-sizing:border-box">
              <div style="display:flex;gap:4px;margin-top:5px;flex-wrap:wrap">
                <span style="font-size:10px;color:var(--text-muted);line-height:24px">快捷:</span>
                <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkQuota').value=10000" style="font-size:10px;padding:2px 8px;height:24px">1万</button>
                <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkQuota').value=100000" style="font-size:10px;padding:2px 8px;height:24px">10万</button>
                <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkQuota').value=1000000" style="font-size:10px;padding:2px 8px;height:24px">100万</button>
                <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkQuota').value=0" style="font-size:10px;padding:2px 8px;height:24px">不限</button>
              </div>
            </div>
            <div>
              <label style="font-size:12px;font-weight:500;display:block;margin-bottom:4px">每小时上限 (tokens)</label>
              <input id="gkQuotaHourly" class="form-input" type="number" value="0" min="0" style="font-size:12px;height:34px;width:100%;box-sizing:border-box">
              <div style="display:flex;gap:4px;margin-top:5px;flex-wrap:wrap">
                <span style="font-size:10px;color:var(--text-muted);line-height:24px">快捷:</span>
                <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkQuotaHourly').value=5000" style="font-size:10px;padding:2px 8px;height:24px">5千</button>
                <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkQuotaHourly').value=10000" style="font-size:10px;padding:2px 8px;height:24px">1万</button>
                <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkQuotaHourly').value=50000" style="font-size:10px;padding:2px 8px;height:24px">5万</button>
                <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkQuotaHourly').value=0" style="font-size:10px;padding:2px 8px;height:24px">不限</button>
              </div>
            </div>
          </div>
          <div style="margin-bottom:14px;border:1px solid rgba(108,99,255,.12);border-radius:8px;overflow:hidden">
            <div onclick="var el=document.getElementById('gkRateDetails');if(el.style.display==='none'){el.style.display='block';this.querySelector('span.arrow').textContent='▾';}else{el.style.display='none';this.querySelector('span.arrow').textContent='▸';}" style="padding:10px 12px;background:rgba(108,99,255,.06);cursor:pointer;font-size:12px;font-weight:500;display:flex;align-items:center;gap:4px;user-select:none">
              <span class="arrow" style="font-size:10px">▸</span> 速率控制 <span style="font-size:10px;color:var(--text-muted);font-weight:400">(可选)</span>
            </div>
            <div id="gkRateDetails" style="display:none;padding:12px;background:rgba(108,99,255,.02)">
              <div style="margin-bottom:10px">
                <label style="font-size:12px;font-weight:500;display:block;margin-bottom:4px">单次请求上限 (tokens)</label>
                <input id="gkQuotaPerRequest" class="form-input" type="number" value="0" min="0" style="font-size:12px;height:34px;width:100%;box-sizing:border-box" placeholder="0=不限">
              </div>
              <div>
                <label style="font-size:12px;font-weight:500;display:block;margin-bottom:4px">每分钟请求数 (RPM)</label>
                <input id="gkRpm" class="form-input" type="number" value="0" min="0" style="font-size:12px;height:34px;width:100%;box-sizing:border-box" placeholder="0=不限">
              </div>
            </div>
          </div>
          <div style="margin-bottom:14px;padding:12px;background:rgba(108,99,255,.04);border:1px solid rgba(108,99,255,.12);border-radius:8px">
            <label style="font-size:12px;font-weight:500;display:block;margin-bottom:6px">有效期</label>
            <div style="display:flex;gap:4px;margin-bottom:8px;flex-wrap:wrap">
              <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkExpDays').value=7" style="font-size:10px;padding:2px 10px;height:26px">7天</button>
              <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkExpDays').value=30" style="font-size:10px;padding:2px 10px;height:26px">30天</button>
              <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkExpDays').value=90" style="font-size:10px;padding:2px 10px;height:26px">90天</button>
              <button type="button" class="btn btn-secondary btn-sm" onclick="document.getElementById('gkExpDays').value=0" style="font-size:10px;padding:2px 10px;height:26px">永久</button>
            </div>
            <div style="display:flex;align-items:center;gap:6px">
              <span style="font-size:11px;color:var(--text-muted)">自定义:</span>
              <input id="gkExpDays" class="form-input" type="number" value="30" min="0" style="font-size:12px;height:34px;width:80px">
              <span style="font-size:11px;color:var(--text-muted)">天 <span style="font-size:10px">(0=永久)</span></span>
            </div>
          </div>

          <div style="display:flex;gap:8px;justify-content:flex-end">
            <button class="btn btn-secondary btn-sm" onclick="document.getElementById('guestKeyForm').remove()" style="font-size:12px;padding:6px 16px">取消</button>
            <button class="btn btn-primary btn-sm" onclick="doGenerateGuestKey()" style="font-size:12px;padding:6px 16px">✅ 签发</button>
          </div>
        </div>`;
      document.getElementById('guestKeysList').insertAdjacentHTML('beforebegin', formHtml);
    }

    async function doGenerateGuestKey() {
      const expDays = parseInt(document.getElementById('gkExpDays').value) || 0;
      let note = document.getElementById('gkNote').value.trim();
      const unlimited = document.getElementById('gkUnlimited').checked;
      const quota = unlimited ? 0 : (parseInt(document.getElementById('gkQuota').value, 10) || 0);
      const quotaHourly = unlimited ? 0 : (parseInt(document.getElementById('gkQuotaHourly').value, 10) || 0);
      const quotaPerRequest = parseInt(document.getElementById('gkQuotaPerRequest').value, 10) || 0;
      const rpm = parseInt(document.getElementById('gkRpm').value, 10) || 0;
      if (!note) { toast('请填写备注名称', 'error'); return; }
      try {
        const body = { exp_days: expDays, quota: quota, quota_hourly: quotaHourly, quota_per_request: quotaPerRequest, rpm: rpm, note: note };
        const res = await authFetch('/api/network/guest-keys', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(body)
        });
        if (res.ok) {
          const d = await res.json();
          const formEl = document.getElementById('guestKeyForm');
          if (formEl) formEl.remove();
          if (d.key) {
            toast('Guest Key 已签发，点击 Key 右侧的 📤 按钮进行分享', 'success');
            await loadGuestKeys();
          }
        } else {
          const err = await res.json();
          toast('签发失败: ' + extractError(err), 'error');
        }
      } catch(e) { toast('签发失败: ' + e.message, 'error'); }
    }

    function filterShareKeys(filter) {
      _shareFilter = filter;
      document.querySelectorAll('.share-filter').forEach(btn => {
        if (btn.dataset.filter === filter) {
          btn.classList.remove('btn-secondary');
          btn.classList.add('btn-primary', 'active');
        } else {
          btn.classList.remove('btn-primary', 'active');
          btn.classList.add('btn-secondary');
        }
      });
      loadGuestKeys();
    }

    function showSharePanel(keyData, purpose) {
      const panel = document.getElementById('sharePanel');
      if (!panel) return;
      const apiUrl = getShareApiUrl();
      const apiKey = (shareInfoData && shareInfoData.proxy_api_key) || '';
      const guestKey = keyData.key || '';
      const baseUrl = window.location.origin;
      const purposeLabel = purpose === 'collaborator' ? '🤝 协作者' : '🔑 使用者';
      let shareText = '';
      let inviteLink = '';
      let qrContent = '';
      let showInviteLink = false;
      if (purpose === 'collaborator') {
        inviteLink = baseUrl + '/login?collaborate=' + encodeURIComponent(guestKey);
        qrContent = inviteLink;
        showInviteLink = true;
        shareText = '邀请你一起维护我的 OpenModelPool Agent 节点 👇\n\n邀请链接: ' + inviteLink + '\n\n你的身份凭证 (Guest Key): ' + guestKey + '\n\n首次登录时可用此 Key 验证身份并设置密码。';
        // Mark key as collaborator type
        authFetch('/api/network/guest-keys/' + encodeURIComponent(guestKey) + '/mark-collaborator', { method: 'POST' }).catch(()=>{});
      } else {
        qrContent = JSON.stringify({ api_url: apiUrl, api_key: guestKey, platform: "OpenModelPool Agent" });
        shareText = '我用 OpenModelPool 搭了个 AI 聚合网关，拿去用 👇\n\nAPI 地址: ' + apiUrl + '\nAPI Key: ' + guestKey;
      }
      const escapedKey = guestKey.replace(/'/g, "\\'");
      const escapedText = shareText.replace(/`/g, '\\`').replace(/\$/g, '\\$');
      const escapedInviteLink = inviteLink.replace(/'/g, "\\'");
      let panelHtml = `
        <div style="padding:20px;background:linear-gradient(135deg,rgba(34,197,94,.06),rgba(34,197,94,.02));border:1px solid rgba(34,197,94,.25);border-radius:12px;box-shadow:0 4px 20px rgba(34,197,94,.08)">
          <div style="font-size:13px;font-weight:600;margin-bottom:12px;display:flex;align-items:center;justify-content:space-between">
            <span>🎉 Key 已创建: ${guestKey.substring(0, 24)}...</span>
            <span style="font-size:10px;padding:2px 8px;background:rgba(108,99,255,.12);color:var(--accent-start);border-radius:4px">${purposeLabel}</span>
          </div>
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:4px">📡 API 地址</label>
            <div style="display:flex;gap:6px">
              <input class="form-input" value="${apiUrl}" readonly style="flex:1;font-family:monospace;font-size:11px;background:var(--bg-secondary);height:32px">
              <button class="btn btn-secondary btn-sm" onclick="copyText('${apiUrl.replace(/'/g, "\\'")}')" style="flex-shrink:0">📋</button>
            </div>
          </div>
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:4px">🔑 Guest Key</label>
            <div style="display:flex;gap:6px">
              <input class="form-input" value="${guestKey}" readonly style="flex:1;font-family:monospace;font-size:11px;background:var(--bg-secondary);height:32px">
              <button class="btn btn-secondary btn-sm" onclick="copyText('${escapedKey}')" style="flex-shrink:0">📋</button>
            </div>
          </div>`;
      if (showInviteLink) {
        panelHtml += `
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:4px">🔗 邀请链接</label>
            <div style="display:flex;gap:6px">
              <input class="form-input" value="${inviteLink}" readonly style="flex:1;font-family:monospace;font-size:11px;background:var(--bg-secondary);height:32px">
              <button class="btn btn-secondary btn-sm" onclick="copyText('${escapedInviteLink}')" style="flex-shrink:0">📋</button>
            </div>
          </div>`;
      }
      panelHtml += `
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:6px">📡 分享渠道</label>
            <div style="display:flex;gap:6px;flex-wrap:wrap">
              <button class="btn btn-secondary btn-sm" onclick="navigator.clipboard.writeText(this.dataset.text).then(()=>toast('已复制，请粘贴到微信发送')).catch(()=>toast('复制失败，请手动复制','error'))" data-text="${escapedText}" style="font-size:11px;padding:4px 12px">📱 微信</button>
              <button class="btn btn-secondary btn-sm" onclick="navigator.clipboard.writeText(this.dataset.text).then(()=>toast('已复制，请粘贴到 Telegram 发送')).catch(()=>toast('复制失败，请手动复制','error'))" data-text="${escapedText}" style="font-size:11px;padding:4px 12px">✈️ Telegram</button>
              <button class="btn btn-secondary btn-sm" onclick="window.location.href='mailto:?subject=' + encodeURIComponent('OpenModelPool 分享') + '&body=' + encodeURIComponent(this.dataset.text)" data-text="${escapedText}" style="font-size:11px;padding:4px 12px">📧 邮件</button>
              <button class="btn btn-secondary btn-sm" onclick="navigator.clipboard.writeText(this.dataset.text).then(()=>toast('已复制全部内容')).catch(()=>toast('复制失败，请手动复制','error'))" data-text="${escapedText}" style="font-size:11px;padding:4px 12px">📋 复制全部</button>
            </div>
          </div>
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:4px">💬 预设文案</label>
            <textarea class="form-input" rows="3" readonly style="font-size:11px;resize:none;background:var(--bg-secondary);width:100%;box-sizing:border-box">${shareText}</textarea>
          </div>
          <div style="display:flex;gap:16px;align-items:flex-start">
            <div style="text-align:center">
              <div style="font-size:11px;color:var(--text-muted);margin-bottom:6px">扫码获取</div>
              <div id="qrSharePanel" style="display:inline-block;padding:8px;background:#fff;border-radius:8px"></div>
            </div>
          </div>
          <div style="margin-top:12px;display:flex;gap:8px;justify-content:flex-end;align-items:center">
            <span id="shareLockStatus" style="font-size:11px;color:var(--text-muted)"></span>
            <button class="btn btn-primary btn-sm" id="confirmShareTypeBtn" onclick="confirmShareType('${escapedKey}','${purpose}')" style="font-size:11px;padding:6px 16px;background:linear-gradient(135deg,var(--accent-start),var(--accent-end))">✅ 确认分享方式</button>
            <button class="btn btn-secondary btn-sm" onclick="document.getElementById('sharePanel').style.display='none';document.getElementById('sharePanel').innerHTML=''" style="font-size:11px;padding:4px 12px">收起</button>
          </div>
        </div>`;
      panel.innerHTML = panelHtml;
      panel.style.display = '';
      // Check if already locked
      const lockedType = _shareTypeLocks['${escapedKey}'];
      if (lockedType) {
        _applyShareLock(lockedType);
      }
      // Generate QR code
      setTimeout(() => { generateQRCode('qrSharePanel', qrContent); }, 100);
      // Scroll to panel
      panel.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }
    // Share type lock state
    const _shareTypeLocks = {};

    async function confirmShareType(guestKey, purpose) {
      try {
        // Call backend to lock the share type
        const endpoint = purpose === 'collaborator' 
          ? '/api/network/guest-keys/' + encodeURIComponent(guestKey) + '/mark-collaborator'
          : '/api/network/guest-keys/' + encodeURIComponent(guestKey) + '/share-type';
        const body = purpose === 'collaborator' ? undefined : JSON.stringify({share_type: 'consumer'});
        const opts = { method: 'POST' };
        if (body) opts.body = body;
        const r = await authFetch(endpoint, opts);
        if (!r.ok) { const e = await r.json().catch(()=>({})); throw new Error(e.error || e.detail || '锁定失败'); }
        _shareTypeLocks[guestKey] = purpose;
        _applyShareLock(purpose);
        toast('分享方式已锁定：' + (purpose === 'collaborator' ? '🤝 协作者' : '🔑 使用者'), 'success');
      } catch(e) {
        toast('锁定失败: ' + e.message, 'error');
      }
    }

    function _applyShareLock(purpose) {
      const btn = document.getElementById('confirmShareTypeBtn');
      const status = document.getElementById('shareLockStatus');
      if (btn) { btn.disabled = true; btn.textContent = '✅ 已锁定'; btn.style.opacity = '0.6'; }
      if (status) { status.textContent = '🔒 已锁定为' + (purpose === 'collaborator' ? '协作者' : '使用者'); status.style.color = 'var(--success)'; }
    }

    function shareExistingKey(escapedKey) {
      // Show a dialog to choose share type
      const dialogId = 'shareDialog_' + escapedKey.replace(/[^a-zA-Z0-9]/g, '_');
      const existing = document.getElementById(dialogId);
      if (existing) { existing.remove(); return; }
      const dialogHtml = `
        <div id="${dialogId}" style="padding:12px 16px;background:var(--bg-secondary);border:1px solid var(--border-color, rgba(255,255,255,.1));border-top:none;border-radius:0 0 10px 10px;margin-top:-9px;margin-bottom:8px">
          <div style="font-size:12px;font-weight:600;margin-bottom:10px;color:var(--accent-start)">📤 选择分享方式</div>
          <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
            <button class="btn btn-primary btn-sm" onclick="document.getElementById('${dialogId}').remove();showSharePanelForExistingKey('${escapedKey}','consumer')" style="font-size:11px;padding:6px 14px">🔑 分享给使用者</button>
            <button class="btn btn-secondary btn-sm" onclick="document.getElementById('${dialogId}').remove();showSharePanelForExistingKey('${escapedKey}','collaborator')" style="font-size:11px;padding:6px 14px;border-color:rgba(99,102,241,.4);color:#818cf8">🤝 邀请协作者</button>
            <button class="btn btn-secondary btn-sm" onclick="document.getElementById('${dialogId}').remove()" style="font-size:11px;padding:4px 10px;opacity:.7">✕ 关闭</button>
          </div>
        </div>`;
      const keyRow = document.querySelector('[data-gk-key="' + escapedKey.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;') + '"]');
      if (keyRow) {
        // Remove rounded bottom corners from key card so it merges with the dialog
        keyRow.style.borderRadius = '10px 10px 0 0';
        keyRow.style.marginBottom = '0';
        keyRow.insertAdjacentHTML('afterend', dialogHtml);
      }
    }

    function showSharePanelForExistingKey(guestKey, purpose) {
      const keyData = { key: guestKey };
      const panel = document.getElementById('sharePanel');
      if (!panel) return;
      const apiUrl = getShareApiUrl();
      const baseUrl = window.location.origin;
      const purposeLabel = purpose === 'collaborator' ? '🤝 协作者' : '🔑 使用者';
      let shareText = '';
      let inviteLink = '';
      let qrContent = '';
      let showInviteLink = false;
      if (purpose === 'collaborator') {
        inviteLink = baseUrl + '/login?collaborate=' + encodeURIComponent(guestKey);
        qrContent = inviteLink;
        showInviteLink = true;
        shareText = '邀请你一起维护我的 OpenModelPool Agent 节点 👇\n\n邀请链接: ' + inviteLink + '\n\n你的身份凭证 (Guest Key): ' + guestKey + '\n\n首次登录时可用此 Key 验证身份并设置密码。';
        // Mark key as collaborator type
        authFetch('/api/network/guest-keys/' + encodeURIComponent(guestKey) + '/mark-collaborator', { method: 'POST' }).catch(()=>{});
      } else {
        qrContent = JSON.stringify({ api_url: apiUrl, api_key: guestKey, platform: "OpenModelPool Agent" });
        shareText = '我用 OpenModelPool 搭了个 AI 聚合网关，拿去用 👇\n\nAPI 地址: ' + apiUrl + '\nAPI Key: ' + guestKey;
      }
      const escapedKey = guestKey.replace(/'/g, "\\'");
      const escapedInviteLink = inviteLink.replace(/'/g, "\\'");
      let panelHtml = `
        <div style="padding:20px;background:linear-gradient(135deg,rgba(34,197,94,.06),rgba(34,197,94,.02));border:1px solid rgba(34,197,94,.25);border-radius:12px;box-shadow:0 4px 20px rgba(34,197,94,.08)">
          <div style="font-size:13px;font-weight:600;margin-bottom:12px;display:flex;align-items:center;justify-content:space-between">
            <span>📤 分享 Key: ${guestKey.substring(0, 24)}...</span>
            <span style="font-size:10px;padding:2px 8px;background:rgba(108,99,255,.12);color:var(--accent-start);border-radius:4px">${purposeLabel}</span>
          </div>
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:4px">📡 API 地址</label>
            <div style="display:flex;gap:6px">
              <input class="form-input" value="${apiUrl}" readonly style="flex:1;font-family:monospace;font-size:11px;background:var(--bg-secondary);height:32px">
              <button class="btn btn-secondary btn-sm" onclick="copyText('${apiUrl.replace(/'/g, "\\'")}')" style="flex-shrink:0">📋</button>
            </div>
          </div>
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:4px">🔑 Guest Key</label>
            <div style="display:flex;gap:6px">
              <input class="form-input" value="${guestKey}" readonly style="flex:1;font-family:monospace;font-size:11px;background:var(--bg-secondary);height:32px">
              <button class="btn btn-secondary btn-sm" onclick="copyText('${escapedKey}')" style="flex-shrink:0">📋</button>
            </div>
          </div>`;
      if (showInviteLink) {
        panelHtml += `
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:4px">🔗 邀请链接</label>
            <div style="display:flex;gap:6px">
              <input class="form-input" value="${inviteLink}" readonly style="flex:1;font-family:monospace;font-size:11px;background:var(--bg-secondary);height:32px">
              <button class="btn btn-secondary btn-sm" onclick="copyText('${escapedInviteLink}')" style="flex-shrink:0">📋</button>
            </div>
          </div>`;
      }
      const escapedText = shareText.replace(/`/g, '\\`').replace(/\$/g, '\\$');
      panelHtml += `
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:6px">📡 分享渠道</label>
            <div style="display:flex;gap:6px;flex-wrap:wrap">
              <button class="btn btn-secondary btn-sm" onclick="navigator.clipboard.writeText(this.dataset.text).then(()=>toast('已复制，请粘贴到微信发送')).catch(()=>toast('复制失败，请手动复制','error'))" data-text="${escapedText}" style="font-size:11px;padding:4px 12px">📱 微信</button>
              <button class="btn btn-secondary btn-sm" onclick="navigator.clipboard.writeText(this.dataset.text).then(()=>toast('已复制，请粘贴到 Telegram 发送')).catch(()=>toast('复制失败，请手动复制','error'))" data-text="${escapedText}" style="font-size:11px;padding:4px 12px">✈️ Telegram</button>
              <button class="btn btn-secondary btn-sm" onclick="window.location.href='mailto:?subject=' + encodeURIComponent('OpenModelPool 分享') + '&body=' + encodeURIComponent(this.dataset.text)" data-text="${escapedText}" style="font-size:11px;padding:4px 12px">📧 邮件</button>
              <button class="btn btn-secondary btn-sm" onclick="navigator.clipboard.writeText(this.dataset.text).then(()=>toast('已复制全部内容')).catch(()=>toast('复制失败，请手动复制','error'))" data-text="${escapedText}" style="font-size:11px;padding:4px 12px">📋 复制全部</button>
            </div>
          </div>
          <div style="margin-bottom:10px">
            <label style="font-size:11px;font-weight:500;display:block;margin-bottom:4px">💬 预设文案</label>
            <textarea class="form-input" rows="3" readonly style="font-size:11px;resize:none;background:var(--bg-secondary);width:100%;box-sizing:border-box">${shareText}</textarea>
          </div>
          <div style="display:flex;gap:16px;align-items:flex-start">
            <div style="text-align:center">
              <div style="font-size:11px;color:var(--text-muted);margin-bottom:6px">扫码获取</div>
              <div id="qrSharePanel" style="display:inline-block;padding:8px;background:#fff;border-radius:8px"></div>
            </div>
          </div>
          <div style="margin-top:12px;display:flex;gap:8px;justify-content:flex-end;align-items:center">
            <span id="shareLockStatus" style="font-size:11px;color:var(--text-muted)"></span>
            <button class="btn btn-primary btn-sm" id="confirmShareTypeBtn" onclick="confirmShareType('${escapedKey}','${purpose}')" style="font-size:11px;padding:6px 16px;background:linear-gradient(135deg,var(--accent-start),var(--accent-end))">✅ 确认分享方式</button>
            <button class="btn btn-secondary btn-sm" onclick="document.getElementById('sharePanel').style.display='none';document.getElementById('sharePanel').innerHTML=''" style="font-size:11px;padding:4px 12px">收起</button>
          </div>
        </div>`;
      panel.innerHTML = panelHtml;
      panel.style.display = '';
      // Check if already locked
      const lockedType = _shareTypeLocks['${escapedKey}'];
      if (lockedType) { _applyShareLock(lockedType); }
      setTimeout(() => { generateQRCode('qrSharePanel', qrContent); }, 100);
      panel.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }

    async function deleteGuestKey(key) {
      if (!confirm('确定永久删除此已撤销的 Guest Key？此操作不可恢复。')) return;
      try {
        const res = await authFetch('/api/network/guest-keys/' + encodeURIComponent(key) + '/permanent', { method: 'DELETE' });
        if (res.ok) {
          toast('已永久删除', 'success');
          await loadGuestKeys();
        } else {
          const err = await res.json();
          toast('删除失败: ' + extractError(err), 'error');
        }
      } catch(e) {
        toast('删除失败: ' + e.message, 'error');
      }
    }

    async function revokeGuestKey(key) {
      if (!confirm('确定撤销此 Guest Key？')) return;
      try {
        const res = await authFetch('/api/network/guest-keys/' + encodeURIComponent(key), { method: 'DELETE' });
        if (res.ok) {
          toast('已撤销', 'success');
          await loadGuestKeys();
        } else {
          const err = await res.json().catch(() => ({}));
          toast('撤销失败: ' + extractError(err), 'error');
          await loadGuestKeys();
        }
      } catch(e) {
        toast('撤销失败: ' + e.message, 'error');
        try { await loadGuestKeys(); } catch(_) {}
      }
    }
    // Load network status on page load
    // Initial load: just check mode once
    setTimeout(async () => {
      await loadNetworkStatus();
      // Only start polling if in shared mode
      if (window._networkMode === 'shared' || (networkStatus && (networkStatus.mode === 'shared' || networkStatus.network_enabled))) {
        startNetworkPolling();
      }
    }, 300);
    let _netPollTimer = null;

