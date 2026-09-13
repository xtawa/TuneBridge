package api

import "net/http"

// NewSetupPageHandler serves the mobile-friendly Netease setup and login companion page.
func NewSetupPageHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(setupPageHTML))
	})
}

const setupPageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#121212">
<meta name="apple-mobile-web-app-capable" content="yes">
<title>TuneBridge - 网易云登录设置</title>
<style>
:root {
  color-scheme: light dark;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}
body {
  margin: 0;
  background: #f5f5f7;
  color: #171719;
  -webkit-font-smoothing: antialiased;
}
main {
  max-width: 520px;
  margin: auto;
  padding: 24px 16px 60px;
}
h1 {
  margin: 8px 0 4px;
  font-size: 26px;
  font-weight: 700;
}
.sub {
  color: #666;
  font-size: 14px;
  margin: 0 0 20px;
}
.card {
  background: #fff;
  border-radius: 16px;
  padding: 20px;
  margin-bottom: 18px;
  box-shadow: 0 1px 4px rgba(0,0,0,0.06);
}
.status-pill {
  display: inline-block;
  padding: 4px 10px;
  border-radius: 12px;
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 12px;
}
.status-unauth {
  background: #ffecd2;
  color: #c44000;
}
.status-auth {
  background: #e1f5e8;
  color: #1b7e3f;
}
.qr-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin: 16px 0;
}
.qr-box {
  width: 210px;
  height: 210px;
  background: #eee;
  border-radius: 14px;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  position: relative;
}
.qr-box img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.qr-expired-overlay {
  position: absolute;
  inset: 0;
  background: rgba(0,0,0,0.7);
  color: #fff;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  font-size: 14px;
  cursor: pointer;
}
.qr-status {
  margin: 12px 0 6px;
  font-size: 15px;
  font-weight: 600;
  text-align: center;
}
.btn {
  display: block;
  width: 100%;
  box-sizing: border-box;
  font: inherit;
  font-size: 16px;
  font-weight: 600;
  border-radius: 12px;
  border: 0;
  padding: 14px;
  text-align: center;
  text-decoration: none;
  cursor: pointer;
  margin-top: 10px;
}
.btn-primary {
  background: #007aff;
  color: #fff;
}
.btn-secondary {
  background: #e5e5ea;
  color: #171719;
}
.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
details {
  border-top: 1px solid #eee;
  padding-top: 14px;
  margin-top: 14px;
}
summary {
  font-size: 15px;
  font-weight: 600;
  color: #007aff;
  cursor: pointer;
  outline: none;
}
textarea, input[type="text"] {
  width: 100%;
  box-sizing: border-box;
  font: inherit;
  font-size: 14px;
  padding: 12px;
  border: 1px solid #ccc;
  border-radius: 10px;
  margin-top: 10px;
  background: #fafafa;
}
textarea {
  min-height: 80px;
  resize: vertical;
}
.hint {
  font-size: 12px;
  color: #888;
  margin-top: 6px;
}
#cookie-msg {
  margin-top: 10px;
  font-size: 14px;
  font-weight: 600;
}
@media (prefers-color-scheme: dark) {
  body {
    background: #121212;
    color: #f5f5f7;
  }
  .card {
    background: #1e1e20;
    box-shadow: 0 1px 4px rgba(0,0,0,0.3);
  }
  .status-unauth {
    background: #4d2600;
    color: #ffaa5b;
  }
  .status-auth {
    background: #10381d;
    color: #6ed68f;
  }
  .qr-box {
    background: #2c2c2e;
  }
  .btn-secondary {
    background: #2c2c2e;
    color: #f5f5f7;
  }
  details {
    border-top-color: #333;
  }
  textarea, input[type="text"] {
    background: #2a2a2c;
    border-color: #444;
    color: #fff;
  }
  .sub, .hint {
    color: #aaa;
  }
}
</style>
</head>
<body>
<main>
  <h1>TuneBridge</h1>
  <p class="sub">网易云音乐登录配置 · 原生二维码授权与凭据导入</p>

  <div class="card">
    <div id="login-badge" class="status-pill status-unauth">检查会话中…</div>

    <div class="qr-container">
      <div class="qr-box" id="qr-box">
        <span id="qr-placeholder" style="color:#888;font-size:14px">正在加载…</span>
        <img id="qr-img" style="display:none" alt="网易云扫码登录二维码">
        <div id="qr-expired" class="qr-expired-overlay" style="display:none" onclick="startQRLogin()">
          <span>二维码已过期</span>
          <span style="font-size:12px;text-decoration:underline">点击刷新</span>
        </div>
      </div>
      <div class="qr-status" id="qr-status">正在生成二维码…</div>
      <a id="app-btn" class="btn btn-secondary" style="display:none" target="_blank" rel="noopener">打开网易云 App 授权</a>
    </div>

    <button id="refresh-btn" class="btn btn-secondary" onclick="startQRLogin()">刷新二维码</button>

    <details id="cookie-accordion">
      <summary>遇到扫码风控？使用 Cookie / MUSIC_U 导入</summary>
      <div style="margin-top:12px">
        <label for="cookie-input" style="font-size:14px;font-weight:600">MUSIC_U 或完整 Cookie：</label>
        <textarea id="cookie-input" placeholder="粘贴 MUSIC_U=xxx... 或纯 MUSIC_U 值" spellcheck="false" autocomplete="off" autocapitalize="none"></textarea>
        <div class="hint">在电脑浏览器登录网易云音乐，从开发者工具中复制 MUSIC_U 的值粘贴至此处。服务端将调用官方接口二次校验并通过 AES-GCM 加密落库。</div>
        <button id="cookie-submit" class="btn btn-primary" onclick="submitCookie()">验证并保存</button>
        <div id="cookie-msg"></div>
      </div>
    </details>
  </div>
</main>

<script>
let pollTimer = null;
let currentKey = '';

async function fetchJSON(url, opt = {}) {
  const res = await fetch(url, { credentials: 'same-origin', ...opt });
  if (!res.ok) {
    let err = 'HTTP ' + res.status;
    try { const j = await res.json(); if (j.error) err = j.error; } catch(_) {}
    throw new Error(err);
  }
  return res.json();
}

async function checkStatus() {
  try {
    const data = await fetchJSON('/api/sources/netease/status');
    const badge = document.getElementById('login-badge');
    if (data.logged_in) {
      badge.textContent = '● 已登录网易云';
      badge.className = 'status-pill status-auth';
    } else {
      badge.textContent = '○ 未登录';
      badge.className = 'status-pill status-unauth';
    }
  } catch(e) {}
}

async function startQRLogin() {
  if (pollTimer) clearInterval(pollTimer);
  const statusEl = document.getElementById('qr-status');
  const imgEl = document.getElementById('qr-img');
  const placeholder = document.getElementById('qr-placeholder');
  const expiredOverlay = document.getElementById('qr-expired');
  const appBtn = document.getElementById('app-btn');

  expiredOverlay.style.display = 'none';
  imgEl.style.display = 'none';
  placeholder.style.display = 'block';
  placeholder.textContent = '正在获取二维码…';
  statusEl.textContent = '正在连接网易云…';
  appBtn.style.display = 'none';

  try {
    const qr = await fetchJSON('/api/sources/netease/login/qr', { method: 'POST' });
    currentKey = qr.key;
    imgEl.src = qr.image_data;
    imgEl.style.display = 'block';
    placeholder.style.display = 'none';
    statusEl.textContent = '请使用网易云音乐 App 扫码';

    if (qr.url) {
      appBtn.href = qr.url;
      appBtn.style.display = 'block';
    }

    pollTimer = setInterval(pollQRStatus, 1500);
  } catch (err) {
    placeholder.textContent = '加载失败';
    statusEl.textContent = '二维码生成失败，请刷新或使用 Cookie 导入';
  }
}

async function pollQRStatus() {
  if (!currentKey) return;
  const statusEl = document.getElementById('qr-status');
  const expiredOverlay = document.getElementById('qr-expired');

  try {
    const res = await fetchJSON('/api/sources/netease/login/qr/' + encodeURIComponent(currentKey));
    if (res.status === 'waiting') {
      statusEl.textContent = '等待扫码…';
    } else if (res.status === 'awaiting_confirmation') {
      statusEl.textContent = '已扫码，请在手机上点击确认登录';
    } else if (res.status === 'authorized') {
      clearInterval(pollTimer);
      statusEl.textContent = '登录成功！会话已安全加密存储';
      statusEl.style.color = '#1b7e3f';
      checkStatus();
    } else if (res.status === 'expired') {
      clearInterval(pollTimer);
      expiredOverlay.style.display = 'flex';
      statusEl.textContent = '二维码已过期，请刷新';
    }
  } catch (err) {
    clearInterval(pollTimer);
    statusEl.textContent = '轮询检查中断：' + err.message;
  }
}

async function submitCookie() {
  const input = document.getElementById('cookie-input');
  const btn = document.getElementById('cookie-submit');
  const msg = document.getElementById('cookie-msg');
  const val = input.value.trim();

  if (!val) {
    msg.textContent = '请输入 Cookie 或 MUSIC_U';
    msg.style.color = '#c44000';
    return;
  }

  btn.disabled = true;
  msg.textContent = '正在进行二次账号验证…';
  msg.style.color = '#666';

  try {
    const body = val.includes('=') ? { cookie: val } : { music_u: val };
    const res = await fetchJSON('/api/sources/netease/login/cookie', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    if (res.status === 'authorized') {
      msg.textContent = '验证成功！会话已安全加密保存至 SQLite';
      msg.style.color = '#1b7e3f';
      input.value = '';
      checkStatus();
    }
  } catch (err) {
    msg.textContent = '导入失败：' + err.message;
    msg.style.color = '#c44000';
  } finally {
    btn.disabled = false;
  }
}

window.addEventListener('DOMContentLoaded', () => {
  checkStatus();
  startQRLogin();
});
</script>
</body>
</html>`
