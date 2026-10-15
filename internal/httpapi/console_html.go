package httpapi

import (
	"net/http"
)

// consoleHTML is the testing panel.
//
// It is served as one document with no build step and no external assets. That is a
// deliberate constraint: a testing panel that needs a bundler is a panel that stops
// working the moment a toolchain breaks, which is exactly when someone needs it.
//
// The panel is not a product surface. It talks to /console/api, which is
// session-scoped to this deployment, rather than to the public /v1 API.
const consoleHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Storvia console</title>
<style>
  :root {
    --bg: #0f1115; --panel: #171a21; --line: #262b36; --text: #e6e8ee;
    --muted: #9aa3b2; --ok: #4ade80; --warn: #fbbf24; --bad: #f87171;
    --accent: #60a5fa; --drive: #7c5cff;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--text);
    font: 15px/1.55 ui-sans-serif, -apple-system, "Segoe UI", Roboto, sans-serif;
  }
  header {
    display: flex; align-items: center; gap: 16px; flex-wrap: wrap;
    padding: 18px 24px; border-bottom: 1px solid var(--line); background: var(--panel);
  }
  header h1 { font-size: 17px; margin: 0; letter-spacing: .2px; }
  header .spacer { flex: 1; }
  main { padding: 24px; max-width: 1180px; margin: 0 auto; }
  .card {
    background: var(--panel); border: 1px solid var(--line); border-radius: 12px;
    padding: 20px; margin-bottom: 18px;
  }
  .card h2 { font-size: 14px; text-transform: uppercase; letter-spacing: .09em;
    color: var(--muted); margin: 0 0 14px; }
  .row { display: flex; gap: 14px; flex-wrap: wrap; align-items: center; }
  .grid { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); }
  .field { display: flex; flex-direction: column; gap: 5px; }
  .field label { font-size: 12px; color: var(--muted); }
  .field input, .field select {
    background: var(--bg); color: var(--text); border: 1px solid var(--line);
    border-radius: 8px; padding: 9px 11px; font: inherit;
  }
  button {
    background: var(--accent); color: #071021; border: 0; border-radius: 8px;
    padding: 9px 15px; font: inherit; font-weight: 600; cursor: pointer;
  }
  button.ghost { background: transparent; color: var(--text); border: 1px solid var(--line); }
  button.danger { background: var(--bad); color: #1b0505; }
  button:disabled { opacity: .45; cursor: not-allowed; }
  table { width: 100%; border-collapse: collapse; font-size: 13.5px; }
  th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--line);
    vertical-align: top; }
  th { color: var(--muted); font-weight: 600; font-size: 11.5px; text-transform: uppercase;
    letter-spacing: .06em; }
  .pill {
    display: inline-block; padding: 2px 9px; border-radius: 999px; font-size: 12px;
    border: 1px solid var(--line); white-space: nowrap;
  }
  .pill.on { background: rgba(74,222,128,.14); color: var(--ok); border-color: rgba(74,222,128,.4); }
  .pill.off { background: rgba(154,163,178,.12); color: var(--muted); }
  .pill.bad { background: rgba(248,113,113,.14); color: var(--bad); border-color: rgba(248,113,113,.4); }
  .pill.warn { background: rgba(251,191,36,.14); color: var(--warn); border-color: rgba(251,191,36,.4); }
  .pill.drive { background: rgba(124,92,255,.16); color: var(--drive); border-color: rgba(124,92,255,.45); }
  .banner {
    padding: 12px 14px; border-radius: 9px; margin-bottom: 16px; font-size: 14px;
    display: flex; gap: 12px; align-items: center;
  }
  .banner.error { background: rgba(248,113,113,.12); border: 1px solid rgba(248,113,113,.4); color: #fecaca; }
  .banner.ok { background: rgba(74,222,128,.1); border: 1px solid rgba(74,222,128,.35); color: #bbf7d0; }
  .banner.info { background: rgba(96,165,250,.1); border: 1px solid rgba(96,165,250,.35); color: #bfdbfe; }
  .muted { color: var(--muted); font-size: 13px; }
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; }
  .switch { display: flex; align-items: center; gap: 10px; cursor: pointer; user-select: none; }
  .login { max-width: 460px; margin: 14vh auto; text-align: center; }
  .login button { font-size: 16px; padding: 12px 22px; }
  .hidden { display: none !important; }
  code { background: var(--bg); padding: 1px 5px; border-radius: 4px; font-size: 12.5px; }
</style>
</head>
<body>

<!-- Sign-in is the gate for the whole console. Nothing below it is rendered, and
     every console API route rejects an unauthenticated request independently, so a
     signed-out visitor sees a login page rather than a broken dashboard. -->
<div id="login" class="login hidden">
  <div class="card">
    <h1 style="font-size:20px;margin-bottom:8px;">Storvia console</h1>
    <p class="muted" style="margin-top:0;">
      Sign in with the Google account whose Drive this console will use.
      You stay signed in until you sign out.
    </p>
    <p id="login-error" class="banner error hidden"></p>
    <a href="/console/sign-in"><button>Sign in with Google</button></a>
  </div>
</div>

<div id="app" class="hidden">
  <header>
    <h1>Storvia console</h1>
    <span id="route-pill" class="pill"></span>
    <div class="spacer"></div>
    <span id="account" class="muted mono"></span>
    <a href="/console/refresh"><button class="ghost">Refresh</button></a>
    <form method="post" action="/console/sign-out" style="margin:0">
      <button class="ghost">Sign out</button>
    </form>
  </header>

  <main>
    <div id="message"></div>

    <div class="card">
      <h2>Where uploads will go</h2>
      <div class="row">
        <span id="target" class="pill">—</span>
        <span class="muted">
          Google Drive is the default. Uploads only move elsewhere when Drive is
          unavailable and you have accepted the fallback disclosure.
        </span>
      </div>
      <div id="storage-blocked" class="banner error hidden" style="margin:14px 0 0"></div>
    </div>

    <div class="card">
      <h2>Upload a file</h2>
      <p class="muted" style="margin-top:-4px;">
        Runs the full sequence an SDK performs: create, transfer to the provider, verify,
        commit. The provider and file id are reported below.
      </p>
      <div class="row">
        <div class="field" style="flex:1;min-width:260px">
          <label for="file">File</label>
          <input type="file" id="file">
        </div>
        <button id="upload-btn" style="margin-top:22px">Upload</button>
      </div>
      <div id="upload-result" class="hidden" style="margin-top:14px"></div>
    </div>

    <div class="card">
      <h2>Google Drive connection</h2>
      <div class="row">
        <span id="conn-pill" class="pill">—</span>
        <span id="conn-detail" class="muted"></span>
        <div class="spacer"></div>
        <button id="ensure-folder" class="ghost">Create Storvia/ folder</button>
        <button id="disconnect" class="danger ghost">Disconnect</button>
      </div>
      <div id="conn-actions" class="row hidden" style="margin-top:14px">
        <a href="/console/connect"><button>Connect Google Drive</button></a>
      </div>
    </div>

    <div class="card">
      <h2>Operator controls</h2>
      <div class="grid">
        <div class="field">
          <label>Force fallback upload</label>
          <select id="routing">
            <option value="default">Use Google Drive</option>
            <option value="forced">Force fallback provider</option>
            <option value="config">Follow config default</option>
          </select>
          <span id="routing-note" class="muted"></span>
        </div>
        <div class="field">
          <label>Simulate a Drive outage</label>
          <select id="faults">
            <option value="off">Off — use the real Drive path</option>
            <option value="on">On — pretend Drive is unavailable</option>
          </select>
          <span class="muted">Fault injection only. It makes the preflight lie; it does not move real data.</span>
        </div>
        <div class="field">
          <label>Outage-fallback disclosure</label>
          <select id="consent">
            <option value="no">Not accepted</option>
            <option value="yes">Accepted</option>
          </select>
          <span class="muted">Required before any upload can be stored outside Drive.</span>
        </div>
      </div>
      <div id="routing-blocked" class="banner warn hidden" style="margin:14px 0 0"></div>
    </div>

    <div class="card">
      <h2>Stored objects</h2>
      <div id="objects"></div>
    </div>

    <div class="card">
      <h2>Diagnostics</h2>
      <div id="diag" class="grid"></div>
    </div>
  </main>
</div>

<script>
'use strict';

const $ = (id) => document.getElementById(id);
let state = null;

function esc(v) {
  return String(v ?? '').replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function bytes(n) {
  if (!n && n !== 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB'];
  let v = n, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + ' ' + units[i];
}

function show(kind, text) {
  $('message').innerHTML = text
    ? '<div class="banner ' + kind + '">' + text + '</div>' : '';
}

async function api(path, options) {
  const res = await fetch(path, Object.assign({ credentials: 'same-origin' }, options));
  let body = null;
  try { body = await res.json(); } catch (_) { /* an empty body is not fatal */ }
  if (!res.ok) {
    const err = new Error((body && (body.error || body.detail)) || ('request failed: ' + res.status));
    err.status = res.status;
    err.body = body;
    throw err;
  }
  return body;
}

function postJSON(path, payload) {
  return api(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

function pill(text, kind) { return '<span class="pill ' + (kind || 'off') + '">' + esc(text) + '</span>'; }

function render() {
  const s = state;

  // Where uploads go, stated plainly.
  const forced = s.routing.forced;
  $('target').outerHTML = forced
    ? '<span id="target" class="pill warn">' + pill('Forced to fallback', 'warn') + '</span>'
    : '<span id="target" class="pill drive">' + pill('Google Drive', 'drive') + '</span>';

  const st = s.storage;
  const blocked = $('storage-blocked');
  if (!st.accepts_uploads) {
    blocked.classList.remove('hidden');
    blocked.textContent = 'Uploads are currently refused: ' + (st.blocked_reason || 'no reason given');
  } else if (forced && !st.fallback_ready) {
    blocked.classList.remove('hidden');
    blocked.textContent = 'Fallback is forced but no backend is ready. Uploads will be refused.';
  } else {
    blocked.classList.add('hidden');
  }

  // Connection.
  const c = s.connection;
  if (c.connected) {
    $('conn-pill').className = 'pill on';
    $('conn-pill').textContent = 'Connected';
    $('conn-detail').textContent =
      (c.folder_present ? 'Storvia/ folder present' : 'Storvia/ folder missing') +
      (c.subject ? ' · ' + c.subject : '') + (c.granted_scopes ? ' · ' + c.granted_scopes : '');
    $('conn-actions').classList.add('hidden');
  } else {
    $('conn-pill').className = 'pill bad';
    $('conn-pill').textContent = c.status || 'Not connected';
    $('conn-detail').textContent = '';
    $('conn-actions').classList.remove('hidden');
  }

  // Operator controls.
  const routingSelect = $('routing');
  routingSelect.value = forced ? 'forced' : (s.routing.overridden ? 'off' : 'default');
  routingSelect.options[0].value = s.routing.overridden ? 'off' : 'default';
  $('routing-note').textContent = s.routing.overridden
    ? 'Overriding the config default (' + (s.routing.configured ? 'force' : 'no force') + ').'
    : 'Following config (force = ' + s.routing.configured + ').';
  routingSelect.disabled = !s.routing.available;

  const rb = $('routing-blocked');
  if (forced && !s.consent.granted) {
    rb.classList.remove('hidden');
    rb.textContent = 'The fallback is forced but the disclosure has not been accepted. ' +
      'Uploads will be refused — accepting it is required before anything is stored outside Drive.';
  } else {
    rb.classList.add('hidden');
  }

  $('faults').value = s.faults.enabled ? 'on' : 'off';
  $('consent').value = s.consent.granted ? 'yes' : 'no';

  // Objects.
  if (!s.objects.length) {
    $('objects').innerHTML = '<p class="muted">Nothing stored yet. Upload something above.</p>';
  } else {
    let html = '<table><thead><tr><th>Name</th><th>Size</th><th>Provider</th>' +
      '<th>Status</th><th>Provider ref</th><th>Stored</th><th></th></tr></thead><tbody>';
    for (const o of s.objects) {
      const prov = o.provider === 'google_drive'
        ? pill('Google Drive', 'drive') : pill(o.provider, 'on');
      const statusKind = o.status === 'ready' ? 'on' : (o.status === 'failed' ? 'bad' : 'warn');
      html += '<tr>' +
        '<td>' + esc(o.name) + '<div class="muted mono">' + esc(o.logical_key) + '</div></td>' +
        '<td>' + bytes(o.size) + '</td>' +
        '<td>' + prov + '</td>' +
        '<td>' + pill(o.status, statusKind) + '</td>' +
        '<td class="mono">' + esc(o.provider_ref || '—') + '</td>' +
        '<td class="muted">' + new Date(o.created_at).toLocaleString() + '</td>' +
        '<td>' +
          (o.downloadable ? '<a href="/console/api/objects/' + encodeURIComponent(o.id) + '/content"><button class="ghost">Download</button></a> ' : '') +
          '<button class="ghost" data-del="' + esc(o.id) + '">Delete</button>' +
        '</td></tr>';
    }
    $('objects').innerHTML = html + '</tbody></table>';
  }

  // Diagnostics.
  const d = s.diagnostics;
  if (d && typeof d === 'object') {
    $('diag').innerHTML = Object.entries(d)
      .map(([k, v]) => '<div class="field"><label>' + esc(k) + '</label><div class="mono">' +
        esc(typeof v === 'object' ? JSON.stringify(v) : v) + '</div></div>')
      .join('');
  } else {
    $('diag').innerHTML = '<p class="muted">No diagnostics available.</p>';
  }

  $('account').textContent = s.account.user_id + ' @ ' + s.account.tenant_id;
}

async function refresh() {
  try {
    state = await api('/console/api/state');
  } catch (e) {
    if (e.status === 401) { showLogin(); return; }
    show('error', esc(e.message));
    return;
  }
  $('login').classList.add('hidden');
  $('app').classList.remove('hidden');
  render();
}

function showLogin(message) {
  $('app').classList.add('hidden');
  $('login').classList.remove('hidden');
  if (message) {
    const box = $('login-error');
    box.textContent = message;
    box.classList.remove('hidden');
  }
}

$('upload-btn').addEventListener('click', async () => {
  const input = $('file');
  if (!input.files.length) { show('error', 'Choose a file first.'); return; }

  const btn = $('upload-btn');
  btn.disabled = true; btn.textContent = 'Uploading…';
  show('info', 'Transferring to the provider…');

  const form = new FormData();
  form.append('file', input.files[0]);
  try {
    const r = await api('/console/api/upload', { method: 'POST', body: form });
    const prov = r.provider === 'google_drive' ? pill('Google Drive', 'drive') : pill(r.provider, 'warn');
    $('upload-result').classList.remove('hidden');
    $('upload-result').innerHTML =
      '<div class="row">' + prov + pill(r.outcome, 'on') +
      '<span class="muted">' + bytes(r.size) + ' in ' + esc(r.elapsed) + '</span></div>' +
      '<div class="mono muted" style="margin-top:8px">' +
      'object ' + esc(r.object_id) + ' · provider ref ' + esc(r.provider_ref || '—') + '</div>';
    show('ok', 'Stored on ' + esc(r.provider) + '.');
    await refresh();
  } catch (e) {
    $('upload-result').classList.remove('hidden');
    $('upload-result').innerHTML = pill('failed', 'bad') +
      '<div class="muted" style="margin-top:8px">' + esc((e.body && e.body.detail) || e.message) + '</div>';
    show('error', 'The upload failed.');
  } finally {
    btn.disabled = false; btn.textContent = 'Upload';
  }
});

$('routing').addEventListener('change', async (ev) => {
  const value = ev.target.value;
  let forced = null;
  if (value === 'forced') forced = true;
  else if (value === 'off') forced = false;
  try {
    await postJSON('/console/api/routing', { forced });
    show('ok', forced === null
      ? 'Routing now follows the configured default.'
      : (forced ? 'Uploads are being forced to the fallback provider.'
                : 'Uploads are being routed to Google Drive.'));
    await refresh();
  } catch (e) { show('error', esc(e.message)); }
});

$('faults').addEventListener('change', async (ev) => {
  try {
    await postJSON('/console/api/faults', { enabled: ev.target.value === 'on' });
    show('ok', ev.target.value === 'on'
      ? 'Drive outage simulation is on. Uploads will route to the fallback.'
      : 'Drive outage simulation is off. Uploads use the real Drive path.');
    await refresh();
  } catch (e) { show('error', esc(e.message)); }
});

$('consent').addEventListener('change', async (ev) => {
  try {
    await postJSON('/console/api/consent', { granted: ev.target.value === 'yes' });
    await refresh();
  } catch (e) { show('error', esc(e.message)); }
});

$('ensure-folder').addEventListener('click', async () => {
  try {
    await postJSON('/console/api/folder', {});
    show('ok', 'The Storvia/ folder is present in Drive.');
    await refresh();
  } catch (e) { show('error', esc(e.message)); }
});

$('disconnect').addEventListener('click', async () => {
  if (!confirm('Disconnect Google Drive for this console?')) return;
  try {
    await postJSON('/console/api/disconnect', {});
    show('ok', 'Google Drive disconnected.');
    await refresh();
  } catch (e) { show('error', esc(e.message)); }
});

$('objects').addEventListener('click', async (ev) => {
  const id = ev.target.getAttribute && ev.target.getAttribute('data-del');
  if (!id) return;
  if (!confirm('Delete this object from the provider?')) return;
  try {
    await api('/console/api/objects/' + encodeURIComponent(id), { method: 'DELETE' });
    show('ok', 'Deleted.');
    await refresh();
  } catch (e) { show('error', esc(e.message)); }
});

refresh();
</script>
</body>
</html>
`

// ConsoleIndex serves the panel.
//
// It is served without a session on purpose: a signed-out visitor has to be able to
// see the sign-in button. The page itself contains no data, and every value it shows
// comes from a session-gated API call.
func (c *Console) Index(w http.ResponseWriter, r *http.Request) {
	if !c.enabled(w) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The panel must never be cached: it is an operator surface, and a stale copy in
	// a shared browser would show the wrong connection state to the next person.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(consoleHTML))
}

func (c *Console) enabled(w http.ResponseWriter) bool {
	if c.cfg.Enabled {
		return true
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("The console is not enabled in this deployment.\n"))
	return false
}
