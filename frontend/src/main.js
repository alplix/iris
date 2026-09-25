import './style.css'

const $ = s => document.querySelector(s)
const $$ = s => [...document.querySelectorAll(s)]

let state = {
  page: 'dashboard',
  hosts: [],
  snaps: {},
  hist: {},
  selectedHost: null,
  theme: localStorage.getItem('iris-theme') || 'dark',
  accent: (() => { try { return localStorage.getItem('iris-accent') || 'violet' } catch (e) { return 'violet' } })(),
  toasts: [],
  searchQuery: '',
  modal: null,
  daemonStatus: 'unknown',
  daemonInfo: null,
  stats: {},
  xfers: {},
  energy: {},
  disk: {},
  prefs: {},
  about: {},
  filterStatus: 'all',
  lastUpdate: null,
  lang: 'en',
  languages: []
}

let prevSnaps = {}
let notifPermission = 'default'

let dict = {}

// T looks a UI string up in the active language and fills {name} placeholders
// from params. Unknown keys render as the key itself so gaps are easy to spot.
function T(key, params) {
  let s = dict[key]
  if (s === undefined) return key
  if (params) for (const k of Object.keys(params)) s = s.split('{' + k + '}').join(params[k])
  return s
}

// Language codes are valid BCP 47 tags, so they double as the date locale.
function locale() { return state.lang || 'en' }

function pickLanguage(preferred) {
  const codes = state.languages.map(l => l.code)
  for (const tag of preferred) {
    const base = String(tag || '').toLowerCase().split('-')[0]
    if (codes.includes(base)) return base
  }
  return 'en'
}

async function setLanguage(code, remember) {
  try { dict = await api('GetTranslations', code) || {} } catch (e) { dict = {} }
  state.lang = code
  document.documentElement.lang = code
  if (remember) { try { await api('SetLanguage', code) } catch (e) {} }
}

async function loadLanguage() {
  try { state.languages = await api('GetLanguages') || [] } catch (e) { state.languages = [] }
  let code = ''
  try { code = await api('GetLanguage') } catch (e) {}
  const chosen = !!code
  if (!code) code = pickLanguage(navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language])
  // Remember an auto-detected language as well, so the tray menu (built by
  // the Go side) follows it.
  await setLanguage(code, !chosen)
}

function toast(msg, type = 'info') {
  const id = Date.now()
  state.toasts.push({ id, msg, type })
  renderToasts()
  setTimeout(() => {
    state.toasts = state.toasts.filter(t => t.id !== id)
    renderToasts()
  }, 3500)
}

function renderToasts() {
  const wrap = $('.toast-wrap')
  if (!wrap) return
  wrap.innerHTML = state.toasts.map(t => `<div class="toast ${t.type}">${esc(t.msg)}</div>`).join('')
}

function esc(s) { const d = document.createElement('div'); d.textContent = s ?? ''; return d.innerHTML }

function jsq(s) { return esc(String(s ?? '')) }

function fmtCredit(v) {
  v = v || 0
  if (v >= 1e9) return (v / 1e9).toFixed(2) + ' B'
  if (v >= 1e6) return (v / 1e6).toFixed(2) + ' M'
  if (v >= 1e4) return (v / 1e3).toFixed(1) + ' k'
  if (v >= 100) return Math.round(v).toString()
  return v.toFixed(1)
}

function fmtBytes(b) {
  b = b || 0
  if (b < 1024) return b + ' B'
  const units = ['KB', 'MB', 'GB', 'TB']
  let u = -1, n = b
  do { n /= 1024; u++ } while (n >= 1024 && u < units.length - 1)
  return n.toFixed(1) + ' ' + units[u]
}

function fmtDuration(s) {
  if (!s || s <= 0) return '-'
  const sec = Math.floor(s)
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${String(m).padStart(2, '0')}m`
  if (m > 0) return `${m}m ${String(sec % 60).padStart(2, '0')}s`
  return `${sec}s`
}

function fmtAgo(ts) {
  if (!ts) return '-'
  const d = (Date.now() / 1000) - ts
  if (d < 60) return T('ui.justNow')
  if (d < 3600) return T('ui.minAgo', { n: Math.floor(d / 60) })
  if (d < 86400) return T('ui.hAgo', { n: Math.floor(d / 3600) })
  return T('ui.dAgo', { n: Math.floor(d / 86400) })
}

function fmtTime(ts) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString(locale(), { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function fmtDeadline(ts) {
  if (!ts) return '-'
  const diff = ts - Date.now() / 1000
  if (diff <= 0) return `<span class="badge error">${esc(T('ui.overdue'))}</span>`
  if (diff < 86400) return `<span class="badge paused">${fmtDuration(diff)}</span>`
  if (diff < 604800) return `${Math.round(diff / 86400)}d`
  return fmtTime(ts)
}

function fmtFlops(v) {
  if (!v) return '?'
  const f = typeof v === 'number' ? v : parseFloat(v) || 0
  if (f >= 1e12) return (f / 1e12).toFixed(1) + ' TFLOPS'
  if (f >= 1e9) return (f / 1e9).toFixed(1) + ' GFLOPS'
  if (f >= 1e6) return (f / 1e6).toFixed(1) + ' MFLOPS'
  return f.toFixed(0) + ' FLOPS'
}

function projColor(url) {
  let h = 0
  url = url || ''
  for (let i = 0; i < url.length; i++) h = h * 31 + url.charCodeAt(i)
  return `hsl(${Math.abs(h) % 360}, 65%, 55%)`
}

function statusBadge(s) {
  const map = { running: 'running', paused: 'paused', queued: 'queued', downloading: 'download', uploading: 'upload', error: 'error', ready: 'ready' }
  return `<span class="badge ${map[s] || 'queued'}">${esc(T('st.' + s))}</span>`
}

function spark(values, w = 200, h = 36, color = 'var(--indigo)') {
  if (!values || values.length < 2) return '<svg class="spark"></svg>'
  const min = Math.min(...values), max = Math.max(...values)
  const span = (max - min) || 1
  const pts = values.map((v, i) => `${(i / (values.length - 1)) * w},${h - 4 - ((v - min) / span) * (h - 10)}`).join(' ')
  return `<svg class="spark" viewBox="0 0 ${w} ${h}" preserveAspectRatio="none"><polyline points="${pts}" fill="none" stroke="${color}" stroke-width="2.5" vector-effect="non-scaling-stroke" stroke-linejoin="round" stroke-linecap="round"/></svg>`
}

function fleetSummary() {
  const list = Object.values(state.snaps)
  const online = list.filter(s => s.online)
  return {
    total: state.hosts.length,
    online: online.length,
    running: online.reduce((a, s) => a + (s.totals?.running || 0), 0),
    paused: online.reduce((a, s) => a + (s.totals?.paused || 0), 0),
    queued: online.reduce((a, s) => a + (s.totals?.queued || 0), 0),
    errors: online.reduce((a, s) => a + (s.totals?.errors || 0), 0),
    downloads: online.reduce((a, s) => a + (s.totals?.downloads || 0), 0),
    uploads: online.reduce((a, s) => a + (s.totals?.uploads || 0), 0),
    rac: online.reduce((a, s) => a + (s.totals?.rac || 0), 0),
    credit: online.reduce((a, s) => a + (s.totals?.credit || 0), 0)
  }
}

async function api(method, ...args) {
  const fn = window.go.main.App[method]
  if (!fn) throw new Error(`Method ${method} not found`)
  return await fn(...args)
}

async function refreshHosts() {
  state.hosts = await api('GetHosts')
  for (const h of state.hosts) {
    try {
      const snap = await api('GetSnapshot', h.id)
      if (snap) state.snaps[h.id] = snap
      const hist = await api('GetHistory', h.id)
      if (hist) state.hist[h.id] = hist
    } catch (e) {}
  }
  state.lastUpdate = Date.now()
  if (state.page === 'settings') loadSettingsData()
  checkNotifications()
  if (!state.modal && !isEditing()) render()
}

// The page fades in when it changes; background refreshes must not replay it.
let animateNext = true
// A new page starts at the top; only background refreshes keep the position.
let scrollToTop = false

function setPage(page) {
  animateNext = true
  scrollToTop = true
  state.page = page
  state.modal = null
  state.searchQuery = ''
  state.filterStatus = 'all'
  if (page === 'stats') loadStats()
  if (page === 'settings') loadSettingsData()
  if (page === 'assistant') loadAssistantState().then(render)
  render()
}

function render() {
  // Rebuilding the DOM would reset the scroll position of the page.
  const scroll = scrollToTop ? 0 : ($('.content')?.scrollTop || 0)
  scrollToTop = false
  renderShell()
  renderContent()
  const content = $('.content')
  if (content && scroll) {
    content.style.scrollBehavior = 'auto'
    content.scrollTop = scroll
    content.style.scrollBehavior = ''
  }
  if (!animateNext) $('.content-inner')?.classList.add('no-anim')
  animateNext = false
}

// True while the user is typing or choosing in a form control; a background
// refresh must not rebuild the page underneath them.
function isEditing() {
  const el = document.activeElement
  return !!el && el.closest?.('#app') && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName)
}

function renderShell() {
  const app = $('#app')
  const fs = fleetSummary()
  app.innerHTML = `
    <div class="shell">
      <aside class="sidebar">
        <div class="side-logo">
          <div class="logo-icon">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="currentColor" opacity="0.95"><ellipse cx="12" cy="5.5" rx="2.2" ry="4"/><ellipse cx="8.5" cy="7" rx="2" ry="3.5" transform="rotate(-25 8.5 7)"/><ellipse cx="15.5" cy="7" rx="2" ry="3.5" transform="rotate(25 15.5 7)"/><ellipse cx="7" cy="10.5" rx="1.8" ry="3" transform="rotate(-40 7 10.5)"/><ellipse cx="17" cy="10.5" rx="1.8" ry="3" transform="rotate(40 17 10.5)"/><line x1="12" y1="11" x2="12" y2="22" stroke="currentColor" stroke-width="1.8" fill="none" opacity="0.7"/><path d="M12 15c-2 1.5-4 1-5 0" stroke="currentColor" stroke-width="1.2" fill="none" opacity="0.5"/><path d="M12 17.5c1.8 1.5 3.5 1 4.5 0" stroke="currentColor" stroke-width="1.2" fill="none" opacity="0.5"/></svg>
          </div>
          <div class="logo-text">
            <div class="logo-title">Iris</div>
            <div class="logo-sub">${esc(T('app.tagline'))}</div>
          </div>
        </div>
        <nav class="nav">
          <div class="nav-item ${state.page === 'dashboard' ? 'active' : ''}" data-page="dashboard">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>
            <span>${esc(T('nav.dash'))}</span>
          </div>
          <div class="nav-item ${state.page === 'assistant' ? 'active' : ''}" data-page="assistant">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2a4 4 0 0 1 4 4c0 1.1-.45 2.1-1.17 2.83A5 5 0 0 1 17 13v1a1 1 0 0 1-1 1h-1v3a3 3 0 0 1-6 0v-3H8a1 1 0 0 1-1-1v-1a5 5 0 0 1 2.17-4.17A4 4 0 0 1 8 6a4 4 0 0 1 4-4Z"/><path d="M9 10h.01M15 10h.01"/></svg>
            <span>${esc(T('nav.assistant'))}</span>
          </div>
          <div class="nav-item ${state.page === 'tasks' ? 'active' : ''}" data-page="tasks">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2h11"/></svg>
            <span>${esc(T('nav.tasks'))}</span>
            ${fs.running > 0 ? `<span class="nav-count num">${fs.running}</span>` : ''}
          </div>
          <div class="nav-item ${state.page === 'projects' ? 'active' : ''}" data-page="projects">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z"/></svg>
            <span>${esc(T('nav.projects'))}</span>
          </div>
          <div class="nav-item ${state.page === 'transfers' ? 'active' : ''}" data-page="transfers">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M7 16V4m0 0L3 8m4-4l4 4m6 0v12m0 0l4-4m-4 4l-4-4"/></svg>
            <span>${esc(T('nav.transfers'))}</span>
            ${fs.downloads + fs.uploads > 0 ? `<span class="nav-count num">${fs.downloads + fs.uploads}</span>` : ''}
          </div>
          <div class="nav-item ${state.page === 'messages' ? 'active' : ''}" data-page="messages">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><path d="M14 2v6h6"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/></svg>
            <span>${esc(T('nav.messages'))}</span>
          </div>
          <div class="nav-item ${state.page === 'stats' ? 'active' : ''}" data-page="stats">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 20V10"/><path d="M18 20V4"/><path d="M6 20v-4"/></svg>
            <span>${esc(T('nav.stats'))}</span>
          </div>
          <div class="nav-item ${state.page === 'hosts' ? 'active' : ''}" data-page="hosts">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>
            <span>${esc(T('nav.hosts'))}</span>
          </div>
          <div class="nav-item ${state.page === 'settings' ? 'active' : ''}" data-page="settings">
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/></svg>
            <span>${esc(T('nav.settings'))}</span>
          </div>
        </nav>
        <div class="side-foot">
          <span>${esc(T('app.footer', { v: state.about.version || '1.0.0' }))}</span>
          <span class="credit">${T('app.codedBy', { a: '<b>Alperen Yavuz</b>' })}</span>
        </div>
      </aside>
      <div class="main">
        <div class="topbar">
          <div style="flex:1;min-width:0">
            <div class="page-title">${pageTitle()}</div>
            <div class="page-sub">${pageSub()}</div>
          </div>
          ${state.lastUpdate ? `<span class="faint num last-up" title="${esc(T('ui.lastRefreshed'))}">${fmtAgo(state.lastUpdate / 1000)}</span>` : ''}
          <button class="btn sm" onclick="window._manualRefresh()" title="${esc(T('ui.refreshNow'))}">⟳</button>
          <span class="dot ${fs.online > 0 ? 'on' : 'off'}" title="${fs.online}/${fs.total} ${esc(T('ui.online'))}"></span>
          <button class="btn sm" onclick="window._toggleTheme()" title="${esc(T('ui.toggleTheme'))}">
            ${state.theme === 'dark' ? '☀️' : '🌙'}
          </button>
        </div>
        <div class="content" id="content"></div>
      </div>
    </div>
    <div class="toast-wrap"></div>
    ${state.modal ? state.modal : ''}
  `
  for (const el of $$('.nav-item')) {
    el.addEventListener('click', () => setPage(el.dataset.page))
  }
  renderToasts()
}

function pageTitle() {
  const map = { dashboard: 'page.dash', assistant: 'page.assistant', tasks: 'page.tasks', projects: 'page.projects', transfers: 'page.transfers', messages: 'page.messages', stats: 'page.stats', hosts: 'page.hosts', settings: 'page.settings' }
  return esc(T(map[state.page] || 'page.dash'))
}

function pageSub() {
  const fs = fleetSummary()
  return esc(T('ui.pageSub', { n: state.hosts.length, on: fs.online, run: fs.running }))
}

function renderContent() {
  const c = $('#content')
  if (!c) return
  switch (state.page) {
    case 'dashboard': c.innerHTML = renderDashboard(); break
    case 'assistant': c.innerHTML = renderAssistant(); break
    case 'tasks': c.innerHTML = renderTasks(); break
    case 'projects': c.innerHTML = renderProjects(); break
    case 'transfers': c.innerHTML = renderTransfers(); break
    case 'messages': c.innerHTML = renderMessages(); break
    case 'stats': c.innerHTML = renderStats(); break
    case 'hosts': c.innerHTML = renderHosts(); break
    case 'settings': c.innerHTML = renderSettings(); break
    default: c.innerHTML = renderDashboard()
  }
}

function renderDashboard() {
  const fs = fleetSummary()

  let serverCards = ''
  for (const h of state.hosts) {
    const snap = state.snaps[h.id]
    const online = snap?.online
    const hist = (state.hist[h.id] || [])
    const runHist = hist.map(p => p.running)
    const sparkline = runHist.length > 1 ? spark(runHist) : ''
    const projects = (snap?.projects || []).slice(0, 4).map(p =>
      `<span class="chip"><span class="chip-dot" style="background:${projColor(p.url)}"></span><span class="trunc">${esc(p.name || p.url)}</span></span>`
    ).join('')

    serverCards += `
      <div class="card hoverable" style="display:flex;flex-direction:column;gap:12px">
        <div class="spread">
          <div class="row" style="min-width:0">
            <span class="dot ${online ? 'on' : 'off'}" style="flex-shrink:0"></span>
            <div style="min-width:0">
              <div class="trunc" style="font-weight:700;font-size:15px">${esc(h.name)}${h.demo ? `<span class="chip plain" style="margin-left:8px">${esc(T('ui.demo'))}</span>` : ''}</div>
              <div class="faint num">${esc(h.host)}:${h.port}</div>
            </div>
          </div>
          ${online && snap.version ? `<span class="chip indigo num">v${esc(snap.version)}</span>` : ''}
        </div>
        ${online ? `
          <div class="row wrap" style="gap:14px">
            <span><b class="num">${snap.totals?.running || 0}</b> <span class="muted">${esc(T('dash.running'))}</span></span>
            <span><b class="num">${snap.totals?.paused || 0}</b> <span class="muted">${esc(T('dash.paused'))}</span></span>
            <span><b class="num">${snap.totals?.queued || 0}</b> <span class="muted">${esc(T('dash.queue'))}</span></span>
            ${(snap.totals?.errors || 0) > 0 ? `<span class="badge error">${esc(T('ui.errors', { n: snap.totals.errors }))}</span>` : ''}
          </div>
          <div class="spread faint">
            <span>${esc(T('dash.racLbl'))} <b class="num muted">${fmtCredit(snap.totals?.rac)}</b></span>
            <span>${esc(T('dash.creditLbl'))} <b class="num muted">${fmtCredit(snap.totals?.credit)}</b></span>
          </div>
          ${sparkline ? `<div class="spark-wrap"><span class="faint" style="font-size:11px">${esc(T('ui.activity'))}</span>${sparkline}</div>` : ''}
          <div class="row wrap" style="gap:6px">${projects}</div>
        ` : `
          <div class="empty" style="padding:18px 10px">
            <b>${esc(snap?.error || T('ui.waiting'))}</b>
          </div>
        `}
      </div>
    `
  }

  let feed = []
  for (const [hid, s] of Object.entries(state.snaps)) {
    if (!s.online) continue
    for (const m of (s.messages || []).slice(-8)) {
      feed.push({ ...m, hostId: hid })
    }
  }
  feed.sort((a, b) => b.time - a.time || b.seq - a.seq)
  const recent = feed.slice(0, 10)

  return `<div class="content-inner">
    <div class="stats-grid">
      <div class="card hoverable">
        <div class="row"><div class="stat-icon"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/></svg></div><div><div class="stat-label">${esc(T('dash.servers'))}</div><div class="stat-value num">${fs.online}/${fs.total}</div><div class="faint">${esc(T('ui.online'))}</div></div></div>
      </div>
      <div class="card hoverable">
        <div class="row"><div class="stat-icon alt"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg></div><div><div class="stat-label">${esc(T('dash.activeTasks'))}</div><div class="stat-value num">${fs.running}</div><div class="faint">${esc(T('ui.pausedQueued', { p: fs.paused, q: fs.queued }))}</div></div></div>
      </div>
      <div class="card hoverable">
        <div class="row"><div class="stat-icon soft"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 20V10"/><path d="M18 20V4"/><path d="M6 20v-4"/></svg></div><div><div class="stat-label">${esc(T('dash.fleetRac'))}</div><div class="stat-value num gain-pos">${fmtCredit(fs.rac)}</div><div class="faint">${esc(T('dash.racHint'))}</div></div></div>
      </div>
      <div class="card hoverable">
        <div class="row"><div class="stat-icon alt"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 9H4.5a2.5 2.5 0 010-5H6"/><path d="M18 9h1.5a2.5 2.5 0 000-5H18"/><path d="M4 22h16"/><path d="M10 14.66V17c0 .55-.47.98-.97 1.21C7.85 18.75 7 20 7 22"/><path d="M14 14.66V17c0 .55.47.98.97 1.21C16.15 18.75 17 20 17 22"/><path d="M18 2H6v7a6 6 0 0012 0V2z"/></svg></div><div><div class="stat-label">${esc(T('dash.totalCredit'))}</div><div class="stat-value num">${fmtCredit(fs.credit)}</div><div class="faint">${esc(T('ui.acrossProjects'))}</div></div></div>
      </div>
    </div>
    <div class="cards-grid">${serverCards || `<div class="card"><div class="empty"><b>${esc(T('dash.noServers'))}</b><span class="faint">${esc(T('dash.noServersHint'))}</span></div></div>`}</div>
    ${fs.errors > 0 ? `<div class="card" style="border-color:rgba(239,68,68,0.4)"><div class="card-head"><h2 class="card-title" style="color:var(--err)">${esc(T('ui.taskErrors'))}</h2></div><div class="faint">${esc(T('ui.taskErrorsBody', { n: fs.errors }))}</div></div>` : ''}
    ${recent.length > 0 ? `
      <div class="card">
        <div class="card-head"><h2 class="card-title">${esc(T('dash.recent'))}</h2></div>
        ${recent.map(m => `<div class="msg-line"><span class="msg-pri-${Math.min(m.pri || 1, 3)} num faint" style="flex-shrink:0">${fmtAgo(m.time)}</span><span class="grow trunc" title="${jsq(m.body)}">${esc(m.body)}</span></div>`).join('')}
      </div>
    ` : ''}
  </div>`
}

function renderTasks() {
  const order = { running: 0, paused: 1, downloading: 2, uploading: 3, queued: 4, ready: 5, error: 6 }
  let rows = ''
  for (const [hid, snap] of Object.entries(state.snaps)) {
    if (!snap.online) continue
    const host = state.hosts.find(h => h.id === hid)
    const tasks = (snap.tasks || []).slice().sort((a, b) => (order[a.status] ?? 9) - (order[b.status] ?? 9) || (b.progress || 0) - (a.progress || 0))
    for (const t of tasks) {
      if (state.filterStatus !== 'all' && t.status !== state.filterStatus) continue
      if (state.searchQuery) {
        const q = state.searchQuery.toLowerCase()
        if (!(t.name || '').toLowerCase().includes(q) && !(t.projectName || '').toLowerCase().includes(q) && !(host?.name || '').toLowerCase().includes(q)) continue
      }
      const pct = Math.round((t.progress || 0) * 100)
      rows += `<tr>
        <td class="task-name trunc" title="${jsq(t.name)}">${esc(t.name)}</td>
        <td>${esc(t.projectName || '-')}</td>
        <td>${statusBadge(t.status)}</td>
        <td class="num">${fmtDeadline(t.deadline)}</td>
        <td><div class="progress"><div class="fill" style="width:${pct}%"></div></div><span class="faint num">${pct}%</span></td>
        <td class="num">${fmtDuration(t.elapsed)}</td>
        <td class="num">${fmtDuration(t.eta)}</td>
        <td>${esc(t.resources || '-')}</td>
        <td><span class="chip plain trunc">${esc(host?.name || hid)}</span></td>
        <td>
          <div class="row" style="gap:4px">
            ${t.status === 'running' ? `<button class="btn sm" onclick="window._taskOp('${hid}','${t.name}','suspend')" title="${esc(T('ui.pause'))}">⏸</button>` : ''}
            ${t.status !== 'running' && t.status !== 'downloading' && t.status !== 'uploading' ? `<button class="btn sm" onclick="window._taskOp('${hid}','${t.name}','resume')" title="${esc(T('ui.resume'))}">▶</button>` : ''}
            ${t.status === 'error' ? `<button class="btn sm danger" onclick="window._taskOp('${hid}','${t.name}','abort')" title="${esc(T('ui.abort'))}">✕</button>` : ''}
          </div>
        </td>
      </tr>`
    }
  }
  const filterLabels = { all: 'tasks.fAll', running: 'tasks.fRunning', paused: 'tasks.fPaused', queued: 'tasks.fQueued', error: 'tasks.fError' }
  const filterButtons = ['all', 'running', 'paused', 'downloading', 'uploading', 'queued', 'error', 'ready'].map(s =>
    `<button class="btn sm ${state.filterStatus === s ? 'primary' : ''}" onclick="window._setFilter('${s}')">${esc(T(filterLabels[s] || 'st.' + s))}</button>`
  ).join('')
  return `<div class="content-inner">
    <div class="card">
      <div class="card-head">
        <h2 class="card-title">${esc(T('ui.allTasks'))}</h2>
        <input class="input" style="width:240px;padding:6px 12px" placeholder="${esc(T('tasks.searchPh'))}" value="${jsq(state.searchQuery)}" oninput="window._searchTasks(this.value)">
      </div>
      <div class="row wrap" style="gap:6px;margin-bottom:14px">${filterButtons}</div>
      ${rows ? `<div class="tbl-wrap"><table class="tbl" style="min-width:1080px"><thead><tr><th>${esc(T('tbl.name'))}</th><th>${esc(T('tbl.project'))}</th><th>${esc(T('tbl.status'))}</th><th>${esc(T('tbl.deadline'))}</th><th>${esc(T('tbl.progress'))}</th><th>${esc(T('tbl.elapsed'))}</th><th>${esc(T('tbl.eta'))}</th><th>${esc(T('tbl.resources'))}</th><th>${esc(T('tbl.server'))}</th><th>${esc(T('tbl.actions'))}</th></tr></thead><tbody>${rows}</tbody></table></div>` : `<div class="empty"><b>${esc(T('tasks.empty'))}</b><span class="faint">${esc(T('tasks.emptyHint'))}</span></div>`}
    </div>
  </div>`
}

function renderProjects() {
  let cards = ''
  for (const [hid, snap] of Object.entries(state.snaps)) {
    if (!snap.online) continue
    const host = state.hosts.find(h => h.id === hid)
    for (const p of snap.projects || []) {
      cards += `
        <div class="card hoverable" style="display:flex;flex-direction:column;gap:10px">
          <div class="row">
            <div class="proj-avatar" style="background:${projColor(p.url)};width:36px;height:36px;border-radius:11px;display:grid;place-items:center;color:#fff;font-weight:700;font-size:14px;flex-shrink:0">${esc((p.name || '?')[0])}</div>
            <div style="min-width:0;flex:1"><div class="trunc" style="font-weight:700;font-size:14px">${esc(p.name)}</div><div class="faint num trunc" style="font-size:11px">${esc(p.url)}</div></div>
          </div>
          <div class="spread faint num" style="font-size:12px">
            <span>${esc(T('ui.hostRac'))}: <b>${fmtCredit(p.hostRac)}</b></span>
            <span>${esc(T('ui.hostCredit'))}: <b>${fmtCredit(p.hostCredit)}</b></span>
          </div>
          <div class="spread faint num" style="font-size:12px">
            <span>${esc(T('ui.userCredit'))}: <b>${fmtCredit(p.userCredit)}</b></span>
            <span>${esc(T('ui.rac'))}: <b>${fmtCredit(p.rac)}</b></span>
          </div>
          ${(() => {
            const ev = lastEventFor(snap, p.url)
            if (!ev) return ''
            const pri = Math.min(ev.pri || 1, 3)
            return `<div class="evt-last ${pri >= 2 ? 'msg-pri-' + pri : 'faint'}" title="${jsq(ev.body)}" onclick="window._setPage('messages')"><span class="faint">${esc(T('evt.lastEvent'))}:</span> ${esc(ev.body)}</div>`
          })()}
          <div class="row wrap" style="gap:6px">
            <span class="chip plain">${esc(host?.name || hid)}</span>
            ${p.pending ? `<span class="badge queued">${esc(T('ui.updating'))}</span>` : ''}
            ${p.suspended ? `<span class="badge paused">${esc(T('bd.suspended'))}</span>` : ''}
            ${p.noMoreWork ? `<span class="badge queued">${esc(T('ui.noMoreWork'))}</span>` : ''}
          </div>
          <div class="row wrap" style="gap:4px;margin-top:4px">
            ${p.suspended
              ? `<button class="btn sm" onclick="window._projectOp('${hid}','${p.url}','resume')">${esc(T('proj.btnResume'))}</button>`
              : `<button class="btn sm" onclick="window._projectOp('${hid}','${p.url}','suspend')">${esc(T('proj.btnSuspend'))}</button>`
            }
            <button class="btn sm" onclick="window._projectOp('${hid}','${p.url}','update')">${esc(T('proj.btnUpdate'))}</button>
            ${p.noMoreWork
              ? `<button class="btn sm" onclick="window._projectOp('${hid}','${p.url}','allowmorework')">${esc(T('proj.btnAllow'))}</button>`
              : `<button class="btn sm" onclick="window._projectOp('${hid}','${p.url}','nomorework')">${esc(T('proj.btnNoMore'))}</button>`
            }
            <button class="btn sm danger" onclick="window._projectOp('${hid}','${p.url}','detach')">${esc(T('proj.detachBtn'))}</button>
          </div>
        </div>
      `
    }
  }
  return `<div class="content-inner">
    <div style="display:flex;justify-content:flex-end"><button class="btn primary" onclick="window._showAttachProject()">+ ${esc(T('proj.add'))}</button></div>
    <div class="cards-grid">${cards || `<div class="card"><div class="empty"><b>${esc(T('proj.empty'))}</b><span class="faint">${esc(T('proj.emptyHint'))}</span></div></div>`}</div>
  </div>`
}

function renderTransfers() {
  let rows = ''
  for (const [hid, snap] of Object.entries(state.snaps)) {
    if (!snap.online) continue
    const host = state.hosts.find(h => h.id === hid)
    for (const t of snap.transfers || []) {
      const pct = Math.round((t.progress || 0) * 100)
      rows += `<tr>
        <td class="trunc" title="${jsq(t.name)}" style="max-width:220px">${esc(t.name)}</td>
        <td>${esc(t.projectName || '-')}</td>
        <td><span class="badge ${t.upload ? 'upload' : 'download'}">${esc(T(t.upload ? 'stats.up' : 'stats.down'))}</span></td>
        <td><div class="progress"><div class="fill" style="width:${pct}%"></div></div><span class="faint num">${pct}%</span></td>
        <td class="num">${fmtBytes(t.done)} / ${fmtBytes(t.total)}</td>
        <td><span class="chip plain trunc">${esc(host?.name || hid)}</span></td>
        <td>
          <div class="row" style="gap:4px">
            ${t.paused ? `<button class="btn sm" onclick="window._transferOp('${hid}','${t.name}','retry')">${esc(T('trns.retry'))}</button>` : ''}
            <button class="btn sm danger" onclick="window._transferOp('${hid}','${t.name}','abort')">${esc(T('ui.abort'))}</button>
          </div>
        </td>
      </tr>`
    }
  }
  return `<div class="content-inner">
    <div class="card">
      <div class="card-head"><h2 class="card-title">${esc(T('nav.transfers'))}</h2></div>
      ${rows ? `<div class="tbl-wrap"><table class="tbl"><thead><tr><th>${esc(T('tbl.name'))}</th><th>${esc(T('tbl.project'))}</th><th>${esc(T('tbl.type'))}</th><th>${esc(T('tbl.progress'))}</th><th>${esc(T('trns.size'))}</th><th>${esc(T('tbl.server'))}</th><th>${esc(T('tbl.actions'))}</th></tr></thead><tbody>${rows}</tbody></table></div>` : `<div class="empty"><b>${esc(T('trns.empty'))}</b><span class="faint">${esc(T('trns.emptyHint'))}</span></div>`}
    </div>
  </div>`
}

// ---- Event log ---------------------------------------------------------
// Everything the clients logged (every scheduler contact and what the project
// answered, transfers, errors), newest first, readable in full: filter by
// severity and project, search, and copy it for a bug report. This is where
// a project that "does nothing" explains itself.
const evt = { sev: 'all', project: '', q: '' }

function evtCollect() {
  const names = {}
  const rows = []
  for (const [hid, snap] of Object.entries(state.snaps)) {
    if (!snap.online) continue
    for (const p of snap.projects || []) names[p.url] = p.name || p.url
    for (const m of snap.messages || []) rows.push({ ...m, hostId: hid })
  }
  rows.sort((a, b) => b.time - a.time || b.seq - a.seq)
  const q = evt.q.trim().toLowerCase()
  const shown = rows.filter(m => {
    const pri = m.pri || 1
    if (evt.sev === 'err' && pri < 3) return false
    if (evt.sev === 'warn' && pri < 2) return false
    if (evt.project && m.project !== evt.project) return false
    if (q && !((m.body || '') + ' ' + (names[m.project] || '')).toLowerCase().includes(q)) return false
    return true
  })
  return { rows, shown, names }
}

function evtListHTML() {
  const { shown, names } = evtCollect()
  const multiHost = Object.values(state.snaps).filter(s => s.online).length > 1
  if (shown.length === 0) {
    return `<div class="empty"><b>${esc(T('msg.empty'))}</b><span class="faint">${esc(T('msg.emptyHint'))}</span></div>`
  }
  return shown.slice(0, 300).map(m => {
    const pri = Math.min(m.pri || 1, 3)
    const host = state.hosts.find(h => h.id === m.hostId)
    const proj = names[m.project] || ''
    return `<div class="evt-row">
      <span class="num faint evt-time">${fmtTime(m.time)}</span>
      <span class="evt-body ${pri >= 2 ? 'msg-pri-' + pri : ''}">${esc(m.body)}</span>
      <span class="evt-tags">${proj ? `<span class="chip plain">${esc(proj)}</span>` : ''}${multiHost ? `<span class="chip plain">${esc(host?.name || m.hostId)}</span>` : ''}</span>
    </div>`
  }).join('')
}

function evtCountText() {
  const { rows, shown } = evtCollect()
  return T('evt.count', { n: shown.length === rows.length ? rows.length : `${shown.length} / ${rows.length}` })
}

function renderMessages() {
  const { rows, names } = evtCollect()
  const projOpts = Object.entries(names)
    .filter(([url]) => rows.some(m => m.project === url))
    .map(([url, name]) => `<option value="${jsq(url)}" ${evt.project === url ? 'selected' : ''}>${esc(name)}</option>`).join('')
  const sevBtn = (v, label) => `<button class="btn sm ${evt.sev === v ? 'primary' : ''}" onclick="window._evtSev('${v}')">${esc(label)}</button>`

  return `<div class="content-inner">
    <div class="card">
      <div class="card-head"><h2 class="card-title">${esc(T('nav.messages'))}</h2><span class="chip plain num" id="evt-count">${esc(evtCountText())}</span></div>
      <p class="faint" style="margin:0 0 12px;font-size:12.5px">${esc(T('evt.hint'))}</p>
      <div class="row wrap" style="gap:8px;margin-bottom:12px">
        ${sevBtn('all', T('msg.lvlAll'))}${sevBtn('warn', T('msg.lvlWarn'))}${sevBtn('err', T('msg.lvlErr'))}
        <select class="select" style="max-width:220px" onchange="window._evtProject(this.value)" aria-label="${esc(T('evt.allProjects'))}">
          <option value="">${esc(T('evt.allProjects'))}</option>${projOpts}
        </select>
        <input class="input grow" style="min-width:160px" id="evt-search" value="${jsq(evt.q)}" placeholder="${esc(T('msg.searchPh'))}" oninput="window._evtSearch(this.value)">
        <button class="btn sm" onclick="window._evtCopy()">${esc(T('evt.copy'))}</button>
      </div>
      <div id="evt-list">${evtListHTML()}</div>
    </div>
  </div>`
}

window._evtSev = (v) => { evt.sev = v; render() }
window._evtProject = (v) => { evt.project = v; render() }
// Typing only refreshes the list and counter (a full render would drop the
// caret out of the search box on every keystroke).
window._evtSearch = (v) => {
  evt.q = v
  const list = $('#evt-list'); if (list) list.innerHTML = evtListHTML()
  const cnt = $('#evt-count'); if (cnt) cnt.textContent = evtCountText()
}
window._evtCopy = async () => {
  const { shown, names } = evtCollect()
  const text = shown.map(m => `${new Date(m.time * 1000).toLocaleString(locale())}  ${(m.pri || 1) >= 3 ? '[ERROR] ' : (m.pri || 1) === 2 ? '[NOTICE] ' : ''}${names[m.project] ? names[m.project] + ': ' : ''}${m.body}`).join('\n')
  try {
    await navigator.clipboard.writeText(text)
  } catch (e) {
    const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select()
    try { document.execCommand('copy') } catch (e2) {}
    ta.remove()
  }
  toast(T('evt.copied'), 'ok')
}

// lastEventFor: a project's newest event-log line, so a project that does
// nothing shows why right on its card instead of only in another page.
function lastEventFor(snap, url) {
  let best = null
  for (const m of snap.messages || []) {
    if (m.project !== url) continue
    if (!best || m.time > best.time || (m.time === best.time && m.seq > best.seq)) best = m
  }
  return best
}

function svgLineChart(data, width, height, color) {
  if (!data || data.length < 2) return `<div class="empty" style="padding:18px"><b class="faint">${esc(T('ui.notEnoughData'))}</b></div>`
  const pad = { l: 50, r: 10, t: 10, b: 28 }
  const cw = width - pad.l - pad.r
  const ch = height - pad.t - pad.b
  const vals = data.map(d => d.v)
  const maxV = Math.max(...vals, 1)
  const niceMax = Math.ceil(maxV / Math.pow(10, Math.floor(Math.log10(maxV)))) * Math.pow(10, Math.floor(Math.log10(maxV)))
  const yMax = (niceMax < maxV ? niceMax * 2 : niceMax) || maxV

  const pts = data.map((d, i) => {
    const x = pad.l + (i / (data.length - 1)) * cw
    const y = pad.t + ch - (d.v / yMax) * ch
    return `${x},${y}`
  })

  const areaPath = `M${pad.l},${pad.t + ch} L${pts.join(' L')} L${pad.l + cw},${pad.t + ch} Z`
  const linePath = `M${pts.join(' L')}`

  const gradId = 'g' + Math.random().toString(36).substr(2, 6)

  let gridLines = ''
  for (let i = 0; i <= 4; i++) {
    const y = pad.t + (ch / 4) * i
    const v = yMax - (yMax / 4) * i
    gridLines += `<line x1="${pad.l}" y1="${y}" x2="${pad.l + cw}" y2="${y}" stroke="var(--border)" stroke-width="0.8" stroke-dasharray="4,4"/>`
    gridLines += `<text x="${pad.l - 6}" y="${y + 4}" text-anchor="end" fill="var(--text-faint)" font-size="10" font-family="JetBrains Mono,monospace">${fmtCredit(v)}</text>`
  }

  const firstLabel = data[0]?.l || ''
  const midLabel = data[Math.floor(data.length / 2)]?.l || ''
  const lastLabel = data[data.length - 1]?.l || ''

  return `<svg viewBox="0 0 ${width} ${height}" class="linechart" preserveAspectRatio="xMidYMid meet">
    <defs><linearGradient id="${gradId}" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="${color}" stop-opacity="0.3"/><stop offset="100%" stop-color="${color}" stop-opacity="0.02"/></linearGradient></defs>
    ${gridLines}
    <path d="${areaPath}" fill="url(#${gradId})"/>
    <path d="${linePath}" fill="none" stroke="${color}" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>
    <text x="${pad.l}" y="${height - 4}" fill="var(--text-faint)" font-size="10" font-family="JetBrains Mono,monospace">${firstLabel}</text>
    <text x="${pad.l + cw / 2}" y="${height - 4}" text-anchor="middle" fill="var(--text-faint)" font-size="10" font-family="JetBrains Mono,monospace">${midLabel}</text>
    <text x="${pad.l + cw}" y="${height - 4}" text-anchor="end" fill="var(--text-faint)" font-size="10" font-family="JetBrains Mono,monospace">${lastLabel}</text>
  </svg>`
}

// Stacked daily bars of estimated energy (kWh): CPU below, GPU on top.
function svgKwhBars(days, width, height, cpuColor, gpuColor) {
  if (!days || days.length === 0) return ''
  const pad = { l: 50, r: 10, t: 10, b: 28 }
  const cw = width - pad.l - pad.r
  const ch = height - pad.t - pad.b
  const maxV = Math.max(...days.map(d => d.kwh), 0.001)
  const yMax = maxV * 1.15
  const barW = Math.max(3, (cw / days.length) - 3)
  const fmt = v => v >= 10 ? v.toFixed(0) : v.toFixed(2)
  let grid = ''
  for (let i = 0; i <= 4; i++) {
    const y = pad.t + (ch / 4) * i
    const v = yMax - (yMax / 4) * i
    grid += `<line x1="${pad.l}" y1="${y}" x2="${pad.l + cw}" y2="${y}" stroke="var(--border)" stroke-width="0.8" stroke-dasharray="4,4"/>`
    grid += `<text x="${pad.l - 6}" y="${y + 4}" text-anchor="end" fill="var(--text-faint)" font-size="10" font-family="JetBrains Mono,monospace">${fmt(v)}</text>`
  }
  let bars = ''
  days.forEach((d, i) => {
    const x = pad.l + (i / days.length) * cw + 1
    const hc = (d.cpuKWh / yMax) * ch
    const hg = (d.gpuKWh / yMax) * ch
    bars += `<rect x="${x}" y="${pad.t + ch - hc}" width="${barW}" height="${hc}" fill="${cpuColor}" rx="2" opacity="0.8"/>`
    bars += `<rect x="${x}" y="${pad.t + ch - hc - hg}" width="${barW}" height="${hg}" fill="${gpuColor}" rx="2" opacity="0.8"/>`
    if (i % Math.ceil(days.length / 8) === 0) {
      const lab = (d.day || '').slice(4, 6) + '/' + (d.day || '').slice(6, 8)
      bars += `<text x="${x + barW / 2}" y="${height - 8}" text-anchor="middle" fill="var(--text-faint)" font-size="10" font-family="JetBrains Mono,monospace">${lab}</text>`
    }
  })
  return `<svg viewBox="0 0 ${width} ${height}" class="linechart" preserveAspectRatio="xMidYMid meet">${grid}${bars}</svg>`
}


// ---- Nature that withers with the carbon you emit -------------------------
// Each tree stands for the ~21 kg of CO2 one tree absorbs in a year; every
// 21 kg emitted, the next tree withers. It plays once when the page opens (the
// scene fades from healthy to its real state) and can be replayed.
const NATURE_TREES = 7
const KG_PER_TREE = 21

function hexMix(a, b, t) {
  const p = h => [1, 3, 5].map(i => parseInt(h.slice(i, i + 2), 16))
  const x = p(a), y = p(b)
  return '#' + x.map((v, i) => Math.round(v + (y[i] - v) * Math.max(0, Math.min(1, t))).toString(16).padStart(2, '0')).join('')
}

function natureScene(kg) {
  const trees = kg / KG_PER_TREE
  const decay = Math.min(1, trees / NATURE_TREES)
  const el = (tag, attrs, inner = '') => `<${tag} ${attrs}>${inner}</${tag}>`
  let out = ''
  // sky, sun, ground
  out += `<rect x="0" y="0" width="800" height="260" data-fill="${hexMix('#bae6fd', '#d6d3d1', decay)}" style="fill:#bae6fd"/>`
  out += `<circle cx="690" cy="52" r="26" data-fill="${hexMix('#fde047', '#9ca3af', decay)}" style="fill:#fde047"/>`
  out += `<path d="M0 205 Q200 185 400 200 T800 195 V260 H0 Z" data-fill="${hexMix('#4ade80', '#a8935a', decay)}" style="fill:#4ade80"/>`
  out += `<path d="M0 225 Q260 210 520 224 T800 218 V260 H0 Z" data-fill="${hexMix('#22c55e', '#8a6d3b', decay)}" style="fill:#22c55e"/>`
  // flowers droop and grey with the overall decay
  for (let i = 0; i < 11; i++) {
    const x = 30 + i * 72 + (i % 3) * 9
    const y = 236 + (i % 2) * 6
    const lean = (i % 2 ? 1 : -1) * decay * 70
    out += `<g data-rot="${lean.toFixed(0)}" style="transform-origin:${x}px ${y}px;transform:rotate(0deg)">
      <line x1="${x}" y1="${y}" x2="${x}" y2="${y - 16}" stroke="#15803d" stroke-width="2"/>
      <circle cx="${x}" cy="${y - 18}" r="5" data-fill="${hexMix(['#f472b6', '#fbbf24', '#a78bfa'][i % 3], '#9ca3af', decay)}" style="fill:${['#f472b6', '#fbbf24', '#a78bfa'][i % 3]}"/></g>`
  }
  // trees
  let withered = 0
  for (let i = 0; i < NATURE_TREES; i++) {
    const f = Math.max(0, Math.min(1, trees - i))
    if (f >= 1) withered++
    const x = 62 + i * 112
    const h = 62 + ((i * 37) % 30)
    const base = 208 + (i % 2) * 4
    const top = base - h
    const col = f < 0.75 ? hexMix('#22c55e', '#a16207', f / 0.75) : '#78350f'
    const vis = f < 0.75 ? 1 : Math.max(0, 1 - (f - 0.75) / 0.25)
    const sway = f >= 1 ? '' : 'sway'
    out += `<g>
      <path d="M${x - 5} ${base} L${x - 3} ${top + 8} L${x + 3} ${top + 8} L${x + 5} ${base} Z" data-fill="${hexMix('#78350f', '#6b7280', f)}" style="fill:#78350f"/>
      <g data-op="${f.toFixed(2)}" style="opacity:0;transition:opacity 3s ease">
        <line x1="${x}" y1="${top + 14}" x2="${x - 22}" y2="${top - 6}" stroke="#57534e" stroke-width="3" stroke-linecap="round"/>
        <line x1="${x}" y1="${top + 20}" x2="${x + 24}" y2="${top - 2}" stroke="#57534e" stroke-width="3" stroke-linecap="round"/>
        <line x1="${x}" y1="${top + 6}" x2="${x + 4}" y2="${top - 22}" stroke="#57534e" stroke-width="3" stroke-linecap="round"/>
      </g>
      <g class="${sway}" data-scale="${vis.toFixed(2)}" style="transform-box:fill-box;transform-origin:50% 100%;transform:scale(1);transition:transform 3s ease">
        <circle cx="${x}" cy="${top - 6}" r="30" data-fill="${col}" style="fill:#22c55e"/>
        <circle cx="${x - 20}" cy="${top + 8}" r="21" data-fill="${col}" style="fill:#22c55e"/>
        <circle cx="${x + 20}" cy="${top + 8}" r="21" data-fill="${col}" style="fill:#22c55e"/>
      </g>`
    out += `</g>`
    if (f > 0 && f < 1) {
      for (let k = 0; k < 3; k++) {
        out += `<ellipse class="nleaf" cx="${x + (k - 1) * 16}" cy="${top + 4}" rx="4" ry="2.2" fill="${col}" style="animation-delay:${(k * 1.7 + i * 0.6).toFixed(1)}s"/>`
      }
    }
  }
  return { svg: out, withered, decay }
}

function playNature(replay) {
  const svg = document.querySelector('#nature-svg')
  if (!svg) return
  svg.querySelectorAll('[data-fill]').forEach(n => { if (!n.dataset.start) n.dataset.start = n.style.fill })
  const apply = on => {
    svg.querySelectorAll('[data-fill]').forEach(n => { n.style.transition = 'fill 3s ease'; if (on) n.style.fill = n.dataset.fill })
    svg.querySelectorAll('[data-op]').forEach(n => { n.style.opacity = on ? n.dataset.op : '0' })
    svg.querySelectorAll('[data-scale]').forEach(n => { n.style.transform = `scale(${on ? n.dataset.scale : 1})` })
    svg.querySelectorAll('[data-rot]').forEach(n => { n.style.transition = 'transform 3s ease'; n.style.transform = `rotate(${on ? n.dataset.rot : 0}deg)` })
    svg.classList.toggle('play', on)
  }
  if (replay) {
    // Snap back to the healthy scene, then fade to the real state again.
    svg.querySelectorAll('[data-fill]').forEach(n => { n.style.transition = 'none'; n.style.fill = n.dataset.start || n.style.fill })
    apply(false)
    void svg.getBoundingClientRect()
  }
  setTimeout(() => apply(true), 80)
}
window._playNature = () => playNature(true)

function natureCard(kg) {
  const sc = natureScene(kg)
  const shown = Math.min(NATURE_TREES, kg / KG_PER_TREE)
  const line = T('energy.nature', { v: shown.toLocaleString(locale(), { maximumFractionDigits: 1 }), n: NATURE_TREES, k: KG_PER_TREE })
  return `<div class="card nature" style="display:flex;flex-direction:column;gap:8px">
    <div class="card-head"><h3 class="card-title" style="font-size:14px">${esc(T('energy.natureTitle'))}</h3><button class="btn sm" onclick="window._playNature()">↻ ${esc(T('energy.replay'))}</button></div>
    <svg id="nature-svg" viewBox="0 0 800 260" class="nature-svg" preserveAspectRatio="xMidYMid slice" role="img" aria-label="${jsq(line)}">${sc.svg}</svg>
    <div class="faint" style="font-size:12px">${esc(line)}</div>
  </div>`
}

function energyCard(h) {
  const e = state.energy[h.id]
  if (!e) return ''
  if (!e.supported) {
    return `<div class="card"><div class="card-head"><h3 class="card-title" style="font-size:14px">${esc(T('energy.title'))}</h3><span class="chip plain">${esc(h.name)}</span></div><div class="faint" style="font-size:12px;margin-top:6px">${esc(T('energy.na'))}</div></div>`
  }
  const nf = (v, d) => (v || 0).toLocaleString(locale(), { maximumFractionDigits: d })
  const tile = (label, value, unit) => `<div style="flex:1;min-width:120px"><div class="faint" style="font-size:11px;text-transform:uppercase;letter-spacing:.04em">${esc(label)}</div><div class="num" style="font-size:22px;font-weight:700">${value}<span class="faint" style="font-size:12px;font-weight:600"> ${esc(unit)}</span></div></div>`
  const days = (e.days || []).slice(-30)
  return `
    <div class="card" style="display:flex;flex-direction:column;gap:12px">
      <div class="card-head"><h3 class="card-title" style="font-size:14px">${esc(T('energy.title'))}</h3><span class="chip plain">${esc(h.name)}</span></div>
      <div class="row wrap" style="gap:16px">
        ${tile(T('energy.total'), nf(e.totalKWh, e.totalKWh < 10 ? 2 : 1), 'kWh')}
        ${tile(T('energy.co2'), nf(e.totalKgCO2, e.totalKgCO2 < 10 ? 2 : 1), 'kg')}
        ${tile(T('energy.today'), nf(e.todayKWh, 2), 'kWh')}
      </div>
      <div class="faint" style="font-size:12px">${esc(T('energy.car', { v: nf(e.carKm, 0) }))}</div>
      ${days.length ? `
        <div class="row" style="gap:16px;padding-left:16px">
          <span class="row" style="gap:5px"><span style="width:10px;height:10px;border-radius:3px;background:#6366f1"></span><span class="faint" style="font-size:11px">${esc(T('energy.cpu'))} ${nf(e.cpuKWh, 2)} kWh</span></span>
          <span class="row" style="gap:5px"><span style="width:10px;height:10px;border-radius:3px;background:#a78bfa"></span><span class="faint" style="font-size:11px">${esc(T('energy.gpu'))} ${nf(e.gpuKWh, 2)} kWh${e.gpuModelled ? ' *' : ''}</span></span>
        </div>
        ${svgKwhBars(days, 800, 200, '#6366f1', '#a78bfa')}` : ''}
      ${e.gpuModelled ? `<div class="faint" style="font-size:11px">* ${esc(T('energy.gpuModelled'))}</div>` : ''}
      <div class="faint" style="font-size:11px">${esc(T('energy.note'))} (${nf(e.cpuWatts, 0)} W CPU, ${nf(e.gpuWatts, 0)} W GPU, ${nf(e.gridG, 0)} g CO₂/kWh)</div>
    </div>`
}

function svgBarChart(data, width, height, upColor, downColor) {
  if (!data || data.length === 0) return `<div class="empty" style="padding:18px"><b class="faint">${esc(T('ui.noXferHistory'))}</b></div>`
  const pad = { l: 50, r: 10, t: 10, b: 28 }
  const cw = width - pad.l - pad.r
  const ch = height - pad.t - pad.b
  const maxV = Math.max(...data.map(d => Math.max(d.up, d.down)), 1)
  const yMax = maxV * 1.15
  const barW = Math.max(2, (cw / data.length) - 2)

  let bars = ''
  let gridLines = ''
  for (let i = 0; i <= 4; i++) {
    const y = pad.t + (ch / 4) * i
    const v = yMax - (yMax / 4) * i
    gridLines += `<line x1="${pad.l}" y1="${y}" x2="${pad.l + cw}" y2="${y}" stroke="var(--border)" stroke-width="0.8" stroke-dasharray="4,4"/>`
    gridLines += `<text x="${pad.l - 6}" y="${y + 4}" text-anchor="end" fill="var(--text-faint)" font-size="10" font-family="JetBrains Mono,monospace">${fmtBytes(Math.round(v))}</text>`
  }

  data.forEach((d, i) => {
    const x = pad.l + (i / data.length) * cw + 1
    const hUp = (d.up / yMax) * ch
    const hDown = (d.down / yMax) * ch
    bars += `<rect x="${x}" y="${pad.t + ch - hUp}" width="${barW}" height="${hUp}" fill="${upColor}" rx="2" opacity="0.75"/>`
    bars += `<rect x="${x + barW + 1}" y="${pad.t + ch - hDown}" width="${barW}" height="${hDown}" fill="${downColor}" rx="2" opacity="0.75"/>`
  })

  return `<svg viewBox="0 0 ${width} ${height}" class="linechart" preserveAspectRatio="xMidYMid meet">
    ${gridLines}${bars}
  </svg>`
}

async function loadStats() {
  for (const h of state.hosts) {
    try { state.stats[h.id] = await api('GetStats', h.id) } catch (e) { state.stats[h.id] = [] }
    try { state.xfers[h.id] = await api('GetXferHistory', h.id) } catch (e) { state.xfers[h.id] = [] }
    try { state.disk[h.id] = await api('GetDiskUsage', h.id) } catch (e) { state.disk[h.id] = null }
    try { state.energy[h.id] = await api('GetEnergy', h.id) } catch (e) { state.energy[h.id] = null }
  }
}

function renderStats() {
  loadStats().then(() => {
    const c = $('#content')
    if (c && state.page === 'stats') { c.innerHTML = renderStatsInner(); playNature(false) }
  })
  return `<div class="content-inner"><div class="card"><div class="empty"><b>${esc(T('ui.loadingStats'))}</b></div></div></div>`
}

function renderStatsInner() {
  const PALETTE = ['#7c3aed', '#6366f1', '#a78bfa', '#818cf8', '#c084fc', '#8b5cf6', '#4f46e5', '#6d28d9']
  let creditCharts = ''
  let xferData = []
  let diskCards = ''

  for (const h of state.hosts) {
    const series = state.stats[h.id] || []
    for (const s of series) {
      const color = PALETTE[Math.abs((s.url || '').length * 47) % PALETTE.length]
      const data = (s.daily || []).map(p => ({ v: p.hostCredit, l: (p.day || '').slice(4, 6) + '/' + (p.day || '').slice(6, 8) }))
      creditCharts += `
        <div class="card" style="display:flex;flex-direction:column;gap:8px">
          <div class="card-head"><h3 class="card-title" style="font-size:14px"><span style="display:inline-block;width:10px;height:10px;border-radius:3px;background:${color};flex-shrink:0"></span> ${esc(s.name || s.url)}</h3><span class="chip plain">${esc(h.name)}</span></div>
          ${svgLineChart(data, 800, 220, color)}
        </div>
      `
    }
  }

  let xferChart = ''
  // Sum every server's traffic per day (a later server must not replace an earlier one).
  const byDay = new Map()
  for (const h of state.hosts) {
    for (const d of state.xfers[h.id] || []) {
      const day = Math.floor((d.when || 0) / 86400)
      const e = byDay.get(day) || { up: 0, down: 0, when: d.when || 0 }
      e.up += d.up || 0
      e.down += d.down || 0
      byDay.set(day, e)
    }
  }
  xferData = [...byDay.entries()].sort((a, b) => a[0] - b[0]).map(([, e]) => ({
    up: e.up, down: e.down,
    l: new Date(e.when * 1000).toLocaleDateString(locale(), { month: 'short', day: 'numeric' })
  }))
  if (xferData.length > 0) {
    xferChart = `
      <div class="card" style="display:flex;flex-direction:column;gap:8px">
        <div class="card-head"><h3 class="card-title" style="font-size:14px">${esc(T('ui.xferHistory'))}</h3></div>
        <div class="row" style="gap:16px;padding-left:16px;margin-bottom:4px">
          <span class="row" style="gap:5px"><span style="width:10px;height:10px;border-radius:3px;background:#6366f1"></span><span class="faint" style="font-size:11px">${esc(T('stats.down'))}</span></span>
          <span class="row" style="gap:5px"><span style="width:10px;height:10px;border-radius:3px;background:#a78bfa"></span><span class="faint" style="font-size:11px">${esc(T('stats.up'))}</span></span>
        </div>
        ${svgBarChart(xferData, 800, 220, '#a78bfa', '#6366f1')}
      </div>
    `
  }

  const colors = ['#7c3aed', '#6366f1', '#a78bfa', '#818cf8', '#c084fc', '#8b5cf6']
  for (const h of state.hosts) {
    const du = state.disk[h.id]
    if (!du) continue
    const used = (du.total || 0) - (du.free || 0)
    const pct = du.total > 0 ? Math.round((used / du.total) * 100) : 0
    let projectBars = ''
    for (let i = 0; i < (du.projects || []).length; i++) {
      const p = du.projects[i]
      const ppct = du.total > 0 ? Math.round(((p.diskUsage || 0) / du.total) * 100) : 0
      const short = (p.url || '').replace('https://', '').replace('http://', '').split('/')[0]
      projectBars += `<div style="display:flex;align-items:center;gap:8px;font-size:12px">
        <span style="width:8px;height:8px;border-radius:3px;background:${colors[i % colors.length]};flex-shrink:0"></span>
        <span class="grow trunc">${esc(short)}</span>
        <span class="num faint">${fmtBytes(p.diskUsage)} (${ppct}%)</span>
      </div>`
    }
    diskCards += `
      <div class="card" style="display:flex;flex-direction:column;gap:10px">
        <div class="card-head"><h3 class="card-title" style="font-size:14px">${esc(T('ui.diskUsage'))}</h3><span class="chip plain">${esc(h.name)}</span></div>
        <div class="progress striped" style="height:10px"><div class="fill" style="width:${pct}%"></div></div>
        <div class="spread faint num" style="font-size:12px"><span>${esc(T('ui.bytesUsed', { v: fmtBytes(used) }))}</span><span>${esc(T('ui.bytesFree', { v: fmtBytes(du.free) }))}</span><span>${esc(T('ui.bytesTotal', { v: fmtBytes(du.total) }))}</span></div>
        ${projectBars ? `<div style="display:flex;flex-direction:column;gap:5px;margin-top:6px">${projectBars}</div>` : ''}
      </div>
    `
  }

  let energyCards = ''
  let totalKg = 0
  for (const h of state.hosts) {
    energyCards += energyCard(h)
    totalKg += state.energy[h.id]?.totalKgCO2 || 0
  }
  const natureHTML = energyCards && totalKg > 0 ? natureCard(totalKg) : ''

  const hasData = creditCharts || xferChart || diskCards || energyCards
  return `<div class="content-inner">
    ${energyCards ? `<div class="section-title"><h2>${esc(T('energy.title'))}</h2></div>${natureHTML ? `<div style="margin-bottom:14px">${natureHTML}</div>` : ''}<div class="cards-grid">${energyCards}</div>` : ''}
    ${creditCharts ? `<div class="section-title"><h2>${esc(T('ui.creditHistory'))}</h2></div><div style="display:flex;flex-direction:column;gap:14px">${creditCharts}</div>` : ''}
    ${xferChart ? `<div style="display:flex;flex-direction:column;gap:14px">${xferChart}</div>` : ''}
    ${diskCards ? `<div class="section-title"><h2>${esc(T('ui.diskUsage'))}</h2></div><div class="cards-grid">${diskCards}</div>` : ''}
    ${!hasData ? `<div class="card"><div class="empty"><b>${esc(T('stats.noData'))}</b><span class="faint">${esc(T('ui.noStatsHint'))}</span></div></div>` : ''}
  </div>`
}

function renderHosts() {
  let cards = ''
  for (const h of state.hosts) {
    const snap = state.snaps[h.id]
    const online = snap?.online
    const gpus = snap?.hostInfo?.gpus || []
    cards += `
      <div class="card hoverable" style="display:flex;flex-direction:column;gap:10px">
        <div class="spread">
          <div class="row">
            <span class="dot ${online ? 'on' : 'off'}"></span>
            <div><div style="font-weight:700;font-size:15px">${esc(h.name)}</div><div class="faint num">${esc(h.host)}:${h.port}</div></div>
          </div>
          <div class="row" style="gap:6px">
            <button class="btn sm" onclick="window._showEditHost('${h.id}')">${esc(T('common.edit'))}</button>
            <button class="btn sm" onclick="window._testHost('${h.id}')">${esc(T('hosts.test'))}</button>
            <button class="btn sm danger" onclick="window._removeHost('${h.id}')">${esc(T('common.del'))}</button>
          </div>
        </div>
        ${online ? `
          <div class="faint num" style="font-size:12px">${esc(snap.hostInfo?.os || '')} · ${esc(snap.hostInfo?.cpu || '')} · ${snap.hostInfo?.cores || 0} ${esc(T('hosts.cores'))}</div>
          ${gpus.length ? `<div class="faint" style="font-size:12px">GPU: ${gpus.map(g => g.names.join(', ')).join(' | ')}</div>` : ''}
        ` : `<div class="faint">${esc(snap?.error || T('ui.offline'))}</div>`}
      </div>
    `
  }
  return `<div class="content-inner">
    <div style="display:flex;justify-content:flex-end"><button class="btn primary" onclick="window._showAddHost()">+ ${esc(T('hosts.add'))}</button></div>
    <div class="cards-grid">${cards || `<div class="card"><div class="empty"><b>${esc(T('hosts.empty'))}</b><span class="faint">${esc(T('hosts.emptyHint'))}</span></div></div>`}</div>
  </div>`
}

async function loadSettingsData() {
  const hosts = state.hosts.filter(h => !h.demo)
  for (const h of hosts) {
    try { state.prefs[h.id] = await api('GetPrefs', h.id) } catch (e) { state.prefs[h.id] = {} }
  }
  await loadAssistantFlags()
}

function daemonLabel() {
  const map = { running: 'set.running', stopped: 'set.stopped' }
  return T(map[state.daemonStatus] || 'ui.unknown')
}

function languageOptions() {
  return state.languages.map(l => `<option value="${esc(l.code)}" ${l.code === state.lang ? 'selected' : ''}>${esc(l.name)}</option>`).join('')
}

function modeBtn(mode, cur, fn) {
  return `<button class="btn sm ${cur === mode ? 'primary' : ''}" onclick="${fn}">${esc(T('run.' + mode))}</button>`
}

function renderSettings() {
  const di = state.daemonInfo
  const found = di?.found
  const exe = di?.exe || T('ui.notFound')
  const dataDir = di?.dataDir || T('ui.na')
  const hint = di?.hint || ''

  let hwCards = ''
  for (const [hid, snap] of Object.entries(state.snaps)) {
    if (!snap?.online || !snap.hostInfo) continue
    const hi = snap.hostInfo
    const host = state.hosts.find(h => h.id === hid)
    const gpus = (hi.gpus || []).map(g => `<div class="row" style="gap:6px;font-size:12px"><span style="width:8px;height:8px;border-radius:3px;background:#7c3aed;flex-shrink:0"></span><span class="grow">${esc((g.names || []).join(', '))}</span><span class="num faint">${fmtBytes(g.vram || 0)}</span></div>`).join('')
    hwCards += `
      <div class="card" style="display:flex;flex-direction:column;gap:10px">
        <div class="card-head"><h3 class="card-title" style="font-size:14px">${esc(host?.name || hid)}</h3><span class="chip indigo num">v${esc(snap.version || '?')}</span></div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:8px 16px;font-size:12px">
          <div class="faint">OS</div><div class="num">${esc(hi.os || '?')} ${esc(hi.osVersion || '')}</div>
          <div class="faint">CPU</div><div class="num">${esc(hi.cpu || '?')}</div>
          <div class="faint">${esc(T('ui.hwCores'))}</div><div class="num">${hi.cores || 0}</div>
          <div class="faint">FLOPS</div><div class="num">${fmtFlops(hi.flops)}</div>
          <div class="faint">RAM</div><div class="num">${fmtBytes(hi.memory || 0)}</div>
          <div class="faint">Disk</div><div class="num">${esc(T('ui.diskTotalFree', { t: fmtBytes(hi.diskTotal || 0), f: fmtBytes(hi.diskFree || 0) }))}</div>
        </div>
        ${gpus ? `<div style="display:flex;flex-direction:column;gap:5px;margin-top:4px"><div class="faint" style="font-size:11px;text-transform:uppercase;letter-spacing:0.5px">${esc(T('ui.gpus'))}</div>${gpus}</div>` : ''}
      </div>
    `
  }

  let prefsCards = ''
  for (const h of state.hosts) {
    if (h.demo) continue
    const snap = state.snaps[h.id]
    if (!snap?.online) continue
    const prefCount = Object.keys(state.prefs[h.id] || {}).length
    prefsCards += `
      <div class="card" style="display:flex;flex-direction:column;gap:10px">
        <div class="card-head"><h3 class="card-title" style="font-size:14px">${esc(h.name)}</h3><span class="chip plain">${esc(T('ui.overrides', { n: prefCount }))}</span></div>
        <div class="row" style="gap:8px;flex-wrap:wrap">
          ${modeBtn('always', snap.taskMode, `window._setClientOp('${h.id}','setRunMode','always')`)}
          ${modeBtn('auto', snap.taskMode, `window._setClientOp('${h.id}','setRunMode','auto')`)}
          ${modeBtn('never', snap.taskMode, `window._setClientOp('${h.id}','setRunMode','never')`)}
          <span class="faint" style="font-size:12px">${esc(T('ui.runMode'))}</span>
        </div>
        <div class="row" style="gap:8px;flex-wrap:wrap">
          ${modeBtn('always', snap.netMode, `window._setClientOp('${h.id}','setNetworkMode','always')`)}
          ${modeBtn('auto', snap.netMode, `window._setClientOp('${h.id}','setNetworkMode','auto')`)}
          ${modeBtn('never', snap.netMode, `window._setClientOp('${h.id}','setNetworkMode','never')`)}
          <span class="faint" style="font-size:12px">${esc(T('set.netTitle'))}</span>
        </div>
        <div class="row" style="gap:6px;flex-wrap:wrap">
          <button class="btn sm" onclick="window._setClientOp('${h.id}','benchmarks','')">${esc(T('set.bench'))}</button>
          <button class="btn sm" onclick="window._openPrefs('${h.id}')">${esc(T('ui.editPrefs'))}</button>
        </div>
      </div>
    `
  }

  const fleetHosts = state.hosts.filter(h => !h.demo)
  const fleetCard = fleetHosts.length > 0 ? `
    <div class="card" style="display:flex;flex-direction:column;gap:10px">
      <div class="card-head"><h3 class="card-title" style="font-size:14px">${esc(T('ui.fleetControl'))}</h3><span class="chip plain">${esc(T('ui.nServers', { n: fleetHosts.length }))}</span></div>
      <div class="row" style="gap:8px;flex-wrap:wrap">
        <button class="btn sm" onclick="window._setAllMode('setRunMode','always')">${esc(T('ui.runX', { m: T('run.always') }))}</button>
        <button class="btn sm" onclick="window._setAllMode('setRunMode','auto')">${esc(T('ui.runX', { m: T('run.auto') }))}</button>
        <button class="btn sm" onclick="window._setAllMode('setRunMode','never')">${esc(T('ui.runX', { m: T('run.never') }))}</button>
        <button class="btn sm" onclick="window._setAllMode('setNetworkMode','always')">${esc(T('ui.netX', { m: T('run.always') }))}</button>
        <button class="btn sm" onclick="window._setAllMode('setNetworkMode','auto')">${esc(T('ui.netX', { m: T('run.auto') }))}</button>
        <button class="btn sm" onclick="window._setAllMode('setNetworkMode','never')">${esc(T('ui.netX', { m: T('run.never') }))}</button>
        <button class="btn sm" onclick="window._setAllMode('benchmarks','')">${esc(T('ui.benchAll'))}</button>
      </div>
    </div>
  ` : ''

  const about = state.about
  return `<div class="content-inner">
    <div class="card">
      <div class="card-head"><h2 class="card-title">${esc(T('set.localTitle'))}</h2></div>
      <div style="display:flex;flex-direction:column;gap:12px">
        <div class="row" style="gap:12px;flex-wrap:wrap">
          <span class="dot ${state.daemonStatus === 'running' ? 'on' : 'off'}"></span>
          <span style="font-weight:700">${esc(T('ui.status'))}: <span class="num">${esc(daemonLabel())}</span></span>
        </div>
        <div class="faint num" style="font-size:12px">${esc(T('ui.path'))}: ${esc(exe)}</div>
        <div class="faint num" style="font-size:12px">${esc(T('ui.data'))}: ${esc(dataDir)}</div>
        ${hint ? `<div class="faint" style="font-size:12px">${esc(hint)}</div>` : ''}
        <div class="row" style="gap:8px;margin-top:6px">
          <button class="btn primary" onclick="window._startDaemon()" ${!found ? 'disabled' : ''}>${esc(T('ui.start'))}</button>
          <button class="btn danger" onclick="window._stopDaemon()" ${!found ? 'disabled' : ''}>${esc(T('ui.stop'))}</button>
        </div>
      </div>
    </div>
    <div class="card">
      <div class="card-head"><h2 class="card-title">${esc(T('set.lang'))}</h2></div>
      <select class="select" id="lang-select" onchange="window._setLang(this.value)" aria-label="${esc(T('set.lang'))}">${languageOptions()}</select>
    </div>
    <div class="card">
      <div class="card-head"><h2 class="card-title">${esc(T('theme.title'))}</h2></div>
      <div style="display:flex;flex-direction:column;gap:14px">
        <div class="row wrap" style="gap:8px">
          <button class="btn sm ${state.theme === 'light' ? 'primary' : ''}" onclick="window._setThemeMode('light')">☀️ ${esc(T('cmd.lightTheme'))}</button>
          <button class="btn sm ${state.theme === 'dark' ? 'primary' : ''}" onclick="window._setThemeMode('dark')">🌙 ${esc(T('cmd.darkTheme'))}</button>
        </div>
        <div class="theme-grid">
          ${Object.entries(ACCENTS).map(([id, [a, b]]) => `<button class="theme-swatch ${state.accent === id ? 'sel' : ''}" onclick="window._setAccent('${id}')" aria-pressed="${state.accent === id}"><span class="dot" style="background:linear-gradient(135deg,${a},${b})"></span>${esc(T('theme.' + id))}</button>`).join('')}
        </div>
      </div>
    </div>
    ${fleetCard}
    ${prefsCards ? `<div class="section-title"><h2>${esc(T('nav.prefs'))}</h2></div><div style="display:flex;flex-direction:column;gap:14px">${prefsCards}</div>` : ''}
    ${hwCards ? `<div class="section-title"><h2>${esc(T('ui.hardware'))}</h2></div><div class="cards-grid">${hwCards}</div>` : ''}
    ${asst.available ? `<div class="card">
      <div class="card-head"><h2 class="card-title">${esc(T('asst.title'))}</h2><span class="chip ${asst.enabled ? 'indigo' : 'plain'}">${esc(T(asst.enabled ? 'asst.enabled' : 'asst.off'))}</span></div>
      <div style="display:flex;flex-direction:column;gap:10px">
        <p class="faint" style="margin:0;font-size:12px">${esc(T('asst.disabledBody'))}</p>
        <div class="row" style="gap:8px">
          ${asst.enabled
            ? `<button class="btn sm" onclick="window._setPage('assistant')">${esc(T('asst.title'))} →</button>
               <button class="btn sm danger" onclick="window._assistantDisable()">${esc(T('asst.disable'))}</button>`
            : `<button class="btn sm primary" onclick="window._assistantEnable()">${esc(T('asst.enable'))}</button>`}
        </div>
      </div>
    </div>` : ''}
    <div class="card">
      <div class="card-head"><h2 class="card-title">${esc(T('set.notifT'))}</h2></div>
      <div style="display:flex;flex-direction:column;gap:10px">
        <div class="faint" style="font-size:12px">${esc(T('ui.status'))}: <span class="num">${esc(notifPermission)}</span></div>
        <div class="row" style="gap:8px">
          <button class="btn sm" onclick="window._requestNotifPermission()">${esc(T('notif.enable'))}</button>
          <button class="btn sm" onclick="window._testNotif()">${esc(T('ui.sendTest'))}</button>
        </div>
      </div>
    </div>
    <div class="card">
      <div class="card-head"><h2 class="card-title">${esc(T('about.title'))}</h2></div>
      <div style="color:var(--text-soft)">
        <p><b>${esc(about.name || 'Iris')} v${esc(about.version || '1.0.0')}</b> - ${esc(T('about.desc'))}</p>
        <p>${esc(T('about.built'))}</p>
        <p>${T('app.codedBy', { a: '<b>Alperen Yavuz</b>' })}</p>
        ${about.repo ? `<p class="faint"><a class="link" href="${jsq(about.repo)}" target="_blank">${esc(about.repo)}</a></p>` : ''}
      </div>
    </div>
  </div>`
}

// ---- AI Assistant (Tilvar) --------------------------------------------
// A thin client over the chat session Go keeps in memory (internal/assistant):
// this module only mirrors what the last Ask/Confirm/Cancel call returned, it
// never re-fetches history on its own background poll, so a slow network
// never interrupts someone mid-sentence.
let asst = { available: false, enabled: false, history: [], sending: false, pendingAction: null, error: '' }

// loadAssistantFlags is the cheap half (no history fetch) — used by the
// Settings page's consent card, which polls in the background and has no
// need to pull the whole conversation just to show an on/off switch.
async function loadAssistantFlags() {
  try { asst.available = await api('AssistantAvailable') } catch (e) { asst.available = false }
  asst.enabled = asst.available ? await api('AssistantEnabled').catch(() => false) : false
}

async function loadAssistantState() {
  await loadAssistantFlags()
  asst.history = asst.enabled ? (await api('AssistantHistory').catch(() => [])) || [] : []
}

function assistantOpLabel(op) {
  const map = {
    suspend: 'asst.opProjectSuspend', resume: 'asst.opProjectResume', update: 'asst.opProjectUpdate',
    detach: 'asst.opProjectDetach', nomorework: 'asst.opProjectNoMore', allowmorework: 'asst.opProjectAllow'
  }
  return T(map[op] || op)
}

// assistantActionText turns the structured ActionCard Go sends into a
// sentence in the user's own language — the model never gets to phrase this
// itself, so a prompt injection can at most name a real host/project/op, all
// of which resolve() has already checked exist.
function assistantActionText(a) {
  if (!a) return ''
  switch (a.tool) {
    case 'project_op':
      return T('asst.actionProjectOp', { op: assistantOpLabel(a.op), project: a.project, host: a.host })
    case 'client_op':
    case 'client_op_all': {
      const host = a.tool === 'client_op_all' ? T('asst.allServers') : a.host
      if (a.op === 'benchmarks') return T('asst.actionBenchmark', { host })
      const mode = T('run.' + a.mode)
      return a.op === 'setNetworkMode' ? T('asst.actionNetMode', { mode, host }) : T('asst.actionRunMode', { mode, host })
    }
    case 'set_prefs': {
      const fields = Object.entries(a.fields || {}).map(([k, v]) => `${k}=${v}`).join(', ')
      return T('asst.actionPrefs', { host: a.host, fields })
    }
    default:
      return a.tool
  }
}

function assistantClarifyText(c) {
  if (!c) return T('asst.clarifyInvalid')
  switch (c.kind) {
    case 'host_not_found': return T('asst.clarifyHostNotFound', { name: c.name })
    case 'project_not_found': return T('asst.clarifyProjectNotFound', { name: c.name, host: c.host })
    case 'unsupported_tool': return T('asst.clarifyUnsupported')
    default: return T('asst.clarifyInvalid')
  }
}

function assistantSupportLinks() {
  return `<div class="faint" style="font-size:11px;margin-top:10px">
    ${esc(T('asst.support'))}:
    <a class="link" href="#" onclick="window._openUrl('https://coff.ee/alplix');return false">${esc(T('asst.linkCoffee'))}</a> ·
    <a class="link" href="#" onclick="window._openUrl('https://athena.org.tr');return false">${esc(T('asst.linkAthena'))}</a> ·
    <a class="link" href="#" onclick="window._openUrl('https://iris.athena.org.tr');return false">${esc(T('asst.linkIrisSite'))}</a>
  </div>`
}

// assistantGuide is the full explanation the user asked for — what the
// assistant can and can't do, and where its data goes — shown before it's
// even turned on, and always reachable afterwards from the chat page too.
function assistantGuide() {
  return `<div style="display:flex;flex-direction:column;gap:12px">
    <div>
      <div style="font-weight:700;font-size:13px">${esc(T('asst.guideDoTitle'))}</div>
      <p style="color:var(--text-soft);margin:4px 0 0;font-size:12.5px">${esc(T('asst.guideDoBody'))}</p>
    </div>
    <div>
      <div style="font-weight:700;font-size:13px">${esc(T('asst.guideSafeTitle'))}</div>
      <p style="color:var(--text-soft);margin:4px 0 0;font-size:12.5px">${esc(T('asst.guideSafeBody'))}</p>
    </div>
    <div>
      <div style="font-weight:700;font-size:13px">${esc(T('asst.guidePrivacyTitle'))}</div>
      <p style="color:var(--text-soft);margin:4px 0 0;font-size:12.5px">${esc(T('asst.guidePrivacyBody'))}</p>
    </div>
  </div>`
}

function assistantExampleChips() {
  const examples = [T('asst.ex1'), T('asst.ex2'), T('asst.ex3'), T('asst.ex4')]
  return `<div>
    <div class="faint" style="font-size:11px;text-transform:uppercase;letter-spacing:.5px;margin-bottom:6px">${esc(T('asst.guideExamplesTitle'))}</div>
    <div class="row wrap" style="gap:6px">
      ${examples.map(e => `<button class="btn sm" onclick="window._assistantExample(${JSON.stringify(e).replace(/"/g, '&quot;')})">${esc(e)}</button>`).join('')}
    </div>
  </div>`
}

function renderAssistant() {
  if (!asst.available) {
    return `<div class="content-inner"><div class="card"><div class="empty"><b>${esc(T('asst.unavailable'))}</b></div></div></div>`
  }
  if (!asst.enabled) {
    return `<div class="content-inner">
      <div class="card" style="max-width:600px;margin:0 auto;display:flex;flex-direction:column;gap:14px">
        <div class="card-head"><h2 class="card-title">${esc(T('asst.disabledTitle'))}</h2></div>
        <p style="color:var(--text-soft);margin:0;font-size:13px">${esc(T('asst.disabledBody'))}</p>
        <div class="row" style="gap:8px">
          <button class="btn primary" onclick="window._assistantEnable()">${esc(T('asst.enable'))}</button>
        </div>
        <div style="border-top:1px solid var(--border);padding-top:12px">
          <div style="font-weight:700;font-size:13px;margin-bottom:6px">${esc(T('asst.guideTitle'))}</div>
          ${assistantGuide()}
        </div>
        ${assistantSupportLinks()}
      </div>
    </div>`
  }
  return `<div class="content-inner">
    <div class="card asst-card">
      <div class="card-head">
        <h2 class="card-title">${esc(T('asst.title'))}</h2>
        <div class="row" style="gap:8px">
          <button class="btn sm" onclick="window._assistantReset()">${esc(T('asst.newChat'))}</button>
          <button class="btn sm danger" onclick="window._assistantDisable()">${esc(T('asst.disable'))}</button>
        </div>
      </div>
      <div id="asst-log" class="asst-log">${renderAssistantLogInner()}</div>
      <div class="row" style="gap:8px;margin-top:12px">
        <input class="input grow" id="asst-input" placeholder="${esc(T('asst.placeholder'))}" ${asst.sending ? 'disabled' : ''}
          onkeydown="if(event.key==='Enter'&&!event.shiftKey){event.preventDefault();window._assistantSend()}">
        <button class="btn primary" id="asst-send" onclick="window._assistantSend()" ${asst.sending ? 'disabled' : ''}>${esc(T('asst.send'))}</button>
      </div>
      <div class="faint" style="font-size:11px;margin-top:10px">${esc(T('asst.poweredBy'))}</div>
      ${assistantSupportLinks()}
    </div>
    <div class="card" style="max-width:820px;margin:14px auto 0;display:flex;flex-direction:column;gap:14px">
      <div class="card-head"><h2 class="card-title" style="font-size:14px">${esc(T('asst.guideTitle'))}</h2></div>
      ${assistantGuide()}
      ${assistantExampleChips()}
    </div>
  </div>`
}

function renderAssistantLogInner() {
  if (asst.history.length === 0 && !asst.pendingAction && !asst.sending) {
    return `<div class="empty" style="padding:30px"><b>${esc(T('asst.welcome'))}</b></div>`
  }
  let html = asst.history.map(m => `<div class="asst-msg ${esc(m.role)}"><div class="asst-bubble">${esc(m.content)}</div></div>`).join('')
  if (asst.sending) {
    html += `<div class="asst-msg assistant"><div class="asst-bubble faint">${esc(T('asst.thinking'))}</div></div>`
  }
  if (asst.pendingAction) {
    html += `<div class="asst-action-card">
      <div class="faint" style="font-size:11px;text-transform:uppercase;letter-spacing:.5px">${esc(T('asst.confirmTitle'))}</div>
      <div style="font-weight:700;margin:5px 0 12px">${esc(assistantActionText(asst.pendingAction))}</div>
      <div class="row" style="gap:8px">
        <button class="btn sm" onclick="window._assistantCancelAction()">${esc(T('common.cancel'))}</button>
        <button class="btn sm primary" onclick="window._assistantConfirmAction()">${esc(T('asst.confirm'))}</button>
      </div>
    </div>`
  }
  if (asst.error) {
    html += `<div class="asst-msg assistant"><div class="asst-bubble" style="color:var(--err)">${esc(asst.error)}</div></div>`
  }
  return html
}

// Redraws only the log + input, so a message round-trip never disturbs the
// rest of the page (or gets clobbered by the periodic background refresh's
// full render() — that one just redraws the same state idempotently anyway).
function renderAssistantLog() {
  const el = $('#asst-log')
  if (!el) return
  el.innerHTML = renderAssistantLogInner()
  el.scrollTop = el.scrollHeight
  const input = $('#asst-input'), btn = $('#asst-send')
  if (input) input.disabled = asst.sending
  if (btn) btn.disabled = asst.sending
}

window._assistantEnable = async () => {
  try {
    await api('SetAssistantEnabled', true)
    await loadAssistantState()
    render()
  } catch (e) { toast(e.message || T('ui.opFailed'), 'err') }
}

window._assistantDisable = async () => {
  try { await api('SetAssistantEnabled', false) } catch (e) {}
  asst.enabled = false; asst.history = []; asst.pendingAction = null; asst.error = ''
  render()
}

window._assistantReset = async () => {
  try { await api('AssistantReset') } catch (e) {}
  asst.history = []; asst.pendingAction = null; asst.error = ''
  renderAssistantLog()
}

window._assistantSend = async () => {
  const input = $('#asst-input')
  const text = (input?.value || '').trim()
  if (!text || asst.sending) return
  input.value = ''
  asst.error = ''
  asst.history.push({ role: 'user', content: text })
  asst.sending = true
  renderAssistantLog()
  try {
    const r = await api('AssistantAsk', text)
    asst.sending = false
    if (r.action) asst.pendingAction = r.action
    else if (r.clarify) asst.history.push({ role: 'assistant', content: assistantClarifyText(r.clarify) })
    else if (r.text) asst.history.push({ role: 'assistant', content: r.text })
  } catch (e) {
    asst.sending = false
    asst.error = e.message || T('ui.opFailed')
  }
  renderAssistantLog()
  input?.focus()
}

window._assistantConfirmAction = async () => {
  const a = asst.pendingAction
  if (!a) return
  asst.pendingAction = null
  asst.history.push({ role: 'assistant', content: '✓ ' + assistantActionText(a) })
  renderAssistantLog()
  try {
    await api('AssistantConfirm', a.id)
    toast(T('asst.actionDone'), 'ok')
    await refreshHosts()
  } catch (e) {
    toast(T('asst.actionFailed', { e: e.message || T('ui.opFailed') }), 'err')
  }
}

window._assistantCancelAction = () => {
  const a = asst.pendingAction
  if (!a) return
  asst.pendingAction = null
  try { api('AssistantCancel', a.id) } catch (e) {}
  asst.history.push({ role: 'assistant', content: T('asst.actionCancelled') })
  renderAssistantLog()
}

// Fills the input with an example prompt rather than sending it straight
// away, so someone new to this can read and edit it first.
window._assistantExample = (text) => {
  const input = $('#asst-input')
  if (!input) return
  input.value = text
  input.focus()
}

function checkNotifications() {
  if (notifPermission !== 'granted') return
  for (const [hid, snap] of Object.entries(state.snaps)) {
    const prev = prevSnaps[hid]
    const host = state.hosts.find(h => h.id === hid)
    const name = host?.name || hid
    if (!prev) continue
    if (snap.online && !prev.online) {
      new Notification(T('notif.title'), { body: T('ui.backOnline', { h: name }) })
    }
    if (!snap.online && prev.online) {
      new Notification(T('notif.title'), { body: T('notif.offline', { h: name }) })
    }
    const errs = snap.totals?.errors || 0
    const perrs = prev.errors || 0
    if (errs > perrs) {
      new Notification(T('notif.title'), { body: T('ui.tasksErrored', { h: name, n: errs - perrs }) })
    }
  }
  prevSnaps = {}
  for (const [hid, snap] of Object.entries(state.snaps)) {
    prevSnaps[hid] = { online: snap.online, errors: snap.totals?.errors || 0 }
  }
}

window._setPage = setPage

window._setLang = async (code) => {
  await setLanguage(code, true)
  render()
}

// Colour themes: an accent palette on top of the light/dark choice. Each has
// a swatch (its two brand colours) and a translated name (theme.<id>).
const ACCENTS = {
  violet: ['#7c3aed', '#6366f1'],
  ocean: ['#0284c7', '#06b6d4'],
  emerald: ['#059669', '#14b8a6'],
  rose: ['#e11d48', '#ec4899'],
  amber: ['#d97706', '#ea580c'],
  graphite: ['#475569', '#64748b']
}

window._setAccent = (id) => {
  if (!ACCENTS[id]) return
  state.accent = id
  try { localStorage.setItem('iris-accent', id) } catch (e) {}
  applyTheme()
  render()
}

window._setThemeMode = (mode) => {
  state.theme = mode
  try { localStorage.setItem('iris-theme', mode) } catch (e) {}
  applyTheme()
  render()
}

window._toggleTheme = () => {
  state.theme = state.theme === 'dark' ? 'light' : 'dark'
  localStorage.setItem('iris-theme', state.theme)
  applyTheme()
  render()
}

window._manualRefresh = async () => {
  try { await api('RefreshAll') } catch (e) {}
  await refreshHosts()
  if (state.page === 'stats') loadStats()
}

window._setFilter = (status) => {
  state.filterStatus = status
  render()
}

window._searchTasks = (q) => {
  state.searchQuery = q
  const c = $('#content')
  if (c && state.page === 'tasks') c.innerHTML = renderTasks()
}

window._taskOp = async (hostId, name, op) => {
  try {
    await api('TaskOp', hostId, name, op)
    const done = { suspend: 'tasks.toastPause', resume: 'tasks.toastResume', abort: 'tasks.toastAbort' }
    toast(T(done[op] || 'set.saved'), 'ok')
    await refreshHosts()
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._projectOp = async (hostId, url, op) => {
  if (op === 'detach') {
    const proj = (state.snaps[hostId]?.projects || []).find(p => p.url === url)
    const host = state.hosts.find(h => h.id === hostId)
    const msg = `${T('proj.detachW', { p: proj?.name || url, h: host?.name || hostId })}
${T('proj.detachN')}`
    if (!confirm(msg)) return
  }
  try {
    await api('ProjectOp', hostId, url, op)
    const done = { update: 'proj.opUpdate', suspend: 'proj.opSuspend', resume: 'proj.opResume', nomorework: 'proj.opNoMore', allowmorework: 'proj.opAllow', detach: 'proj.opDetach' }
    toast(T(done[op] || 'set.saved'), 'ok')
    await refreshHosts()
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._transferOp = async (hostId, name, op) => {
  try {
    await api('TransferOp', hostId, name, op)
    toast(T(op === 'retry' ? 'trns.toastRetry' : 'trns.toastAbort'), 'ok')
    await refreshHosts()
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._setClientOp = async (hostId, op, mode) => {
  try {
    await api('ClientOp', hostId, op, mode)
    const msg = op === 'benchmarks' ? T('set.benchOk')
      : op === 'setNetworkMode' ? T('set.netSet', { m: T('run.' + mode) })
      : T('ui.runModeSet', { m: T('run.' + mode) })
    toast(msg, 'ok')
    await refreshHosts()
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._setAllMode = async (op, mode) => {
  try {
    const failed = await api('ClientOpAll', op, mode)
    await refreshHosts()
    const names = Object.keys(failed || {})
    if (!names.length) toast(T('cmd.appliedTo', { n: state.hosts.filter(h => !h.demo).length }), 'ok')
    else toast(T('ui.failedOn', { names: names.slice(0, 3).join(', ') + (names.length > 3 ? '…' : '') }), 'err')
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._removeHost = async (id) => {
  const h = state.hosts.find(x => x.id === id)
  if (!h || !confirm(T('hosts.removeB', { n: h.name, h: h.host }))) return
  await api('RemoveHost', id)
  delete state.snaps[id]
  await refreshHosts()
  toast(T('hosts.removed', { h: h.name }), 'ok')
}

window._testHost = async (id) => {
  const h = state.hosts.find(h => h.id === id)
  if (!h) return
  try {
    const ver = await api('TestHost', h.host, h.port, h.password)
    toast(T('hosts.testOk', { h: h.name, v: ver }), 'ok')
  } catch (e) {
    toast(e.message || T('ui.connFailed'), 'err')
  }
}

window._showAddHost = () => {
  state.modal = `
    <div class="modal-overlay" onclick="if(event.target===this)window._closeModal()">
      <div class="modal" style="max-width:420px">
        <div class="modal-head"><h3>${esc(T('hosts.add'))}</h3><button class="btn icon" onclick="window._closeModal()">✕</button></div>
        <div class="field"><label class="label">${esc(T('hosts.name'))}</label><input class="input" id="add-name" placeholder="${esc(T('hosts.namePh'))}"></div>
        <div class="field"><label class="label">${esc(T('hosts.addr'))}</label><input class="input" id="add-host" placeholder="${esc(T('hosts.addrPh'))}"></div>
        <div class="field"><label class="label">${esc(T('hosts.portL'))}</label><input class="input num" id="add-port" value="31418"></div>
        <div class="field"><label class="label">${esc(T('hosts.pass'))}</label><input class="input" id="add-pass" type="password" placeholder="${esc(T('hosts.passPh'))}"></div>
        <div class="modal-foot">
          <button class="btn" onclick="window._closeModal()">${esc(T('common.cancel'))}</button>
          <button class="btn primary" onclick="window._doAddHost()">${esc(T('ui.add'))}</button>
        </div>
      </div>
    </div>
  `
  render()
}

window._showEditHost = (id) => {
  const h = state.hosts.find(h => h.id === id)
  if (!h) return
  state.modal = `
    <div class="modal-overlay" onclick="if(event.target===this)window._closeModal()">
      <div class="modal" style="max-width:420px">
        <div class="modal-head"><h3>${esc(T('hosts.edit'))}</h3><button class="btn icon" onclick="window._closeModal()">✕</button></div>
        <div class="field"><label class="label">${esc(T('hosts.name'))}</label><input class="input" id="edit-name" value="${jsq(h.name)}"></div>
        <div class="field"><label class="label">${esc(T('hosts.addr'))}</label><input class="input" id="edit-host" value="${jsq(h.host)}"></div>
        <div class="field"><label class="label">${esc(T('hosts.portL'))}</label><input class="input num" id="edit-port" value="${h.port}"></div>
        <div class="field"><label class="label">${esc(T('hosts.pass'))}</label><input class="input" id="edit-pass" type="password" value="${jsq(h.password)}" placeholder="${esc(T('hosts.passPh'))}"></div>
        <div class="modal-foot">
          <button class="btn" onclick="window._closeModal()">${esc(T('common.cancel'))}</button>
          <button class="btn primary" onclick="window._doEditHost('${h.id}')">${esc(T('hosts.save'))}</button>
        </div>
      </div>
    </div>
  `
  render()
}

// ---- Add project: catalog picker ------------------------------------------

// The catalog of BOINC projects and the picker's state live outside `state`
// so that typing in the search box never re-renders the whole dialog.
let catalog = []
let cat = { q: '', area: '', shown: [], sel: null, cfg: null, err: '', checking: false }

function hostPlatform() {
  const id = $('#attach-host')?.value
  return (id && state.snaps[id]?.hostInfo?.platform) || ''
}

function supportsPlatform(p, plat) {
  return !!plat && (p.platforms || []).includes(plat)
}

function catalogFiltered() {
  const q = cat.q.trim().toLowerCase()
  const plat = hostPlatform()
  const list = catalog.filter(p =>
    (!cat.area || p.area === cat.area) &&
    (!q || [p.name, p.area, p.sub, p.description, p.home].join(' ').toLowerCase().includes(q)))
  // Projects that have applications for this server come first.
  return list.sort((a, b) => (supportsPlatform(b, plat) - supportsPlatform(a, plat)) || a.name.localeCompare(b.name))
}

function renderCatalog() {
  const listEl = $('#cat-list')
  if (!listEl) return
  const plat = hostPlatform()
  cat.shown = catalogFiltered()
  $('#cat-count').textContent = T('cat.count', { n: cat.shown.length })
  listEl.innerHTML = cat.shown.length ? cat.shown.map((p, i) => {
    let badge = ''
    if (plat && (p.platforms || []).length) {
      badge = supportsPlatform(p, plat)
        ? `<span class="badge ready">✓ ${esc(T('cat.runs'))}</span>`
        : `<span class="badge paused">${esc(T('cat.noRun'))}</span>`
    }
    return `<div class="cat-item ${cat.sel && cat.sel.url === p.url ? 'sel' : ''}" onclick="window._catPick(${i})">
      <div class="grow" style="min-width:0">
        <div class="cat-name">${esc(p.name)}${p.source === 'community' ? ` <span class="chip plain">${esc(T('cat.community'))}</span>` : ''}</div>
        <div class="faint trunc" style="font-size:11.5px">${esc(p.area || '')}${p.sub ? ' · ' + esc(p.sub) : ''}</div>
        <div class="cat-desc">${esc(p.description || '')}</div>
      </div>${badge}</div>`
  }).join('') : `<div class="empty" style="padding:18px"><b class="faint">${esc(T('cat.none'))}</b></div>`
}

function renderCatalogDetail() {
  const el = $('#cat-detail')
  if (!el) return
  const p = cat.sel
  if (!p) { el.innerHTML = `<div class="faint" style="font-size:12px">${esc(T('cat.pickHint'))}</div>`; return }
  const plat = hostPlatform()
  const lines = []
  if (cat.checking) lines.push(`<span class="faint">${esc(T('cat.checking'))}</span>`)
  else if (cat.err) lines.push(`<span style="color:var(--err)">${esc(T('cat.unreachable', { e: cat.err }))}</span>`)
  else if (cat.cfg) {
    lines.push(`<span style="color:var(--ok)">✓ ${esc(T('cat.reachable'))}</span>`)
    if (cat.cfg.accountCreationDisabled) lines.push(`<span style="color:var(--warn)">${esc(T('cat.signupClosed'))}</span>`)
  }
  const plats = (cat.cfg?.platforms || []).length ? cat.cfg.platforms : (p.platforms || [])
  if (plat && plats.length) {
    lines.push(plats.includes(plat)
      ? `<span style="color:var(--ok)">✓ ${esc(T('cat.platformOk', { p: plat }))}</span>`
      : `<span style="color:var(--warn)">${esc(T('cat.platformNo', { p: plat }))}</span>`)
  }
  const web = p.web || p.url
  el.innerHTML = `<div class="cat-detail">
    <div class="faint" style="font-size:11px;text-transform:uppercase;letter-spacing:.5px">${esc(T('cat.selected'))}</div>
    <div style="font-weight:700;font-size:15px">${esc(p.name)}</div>
    <div style="color:var(--text-soft)">${esc(p.description || '')}</div>
    ${p.home ? `<div class="faint" style="font-size:12px">${esc(p.home)}</div>` : ''}
    <div class="row wrap" style="gap:8px;margin:4px 0">
      <button class="btn sm" onclick="window._openUrl('${jsq(web)}')">${esc(T('cat.website'))} ↗</button>
      <button class="btn sm" onclick="window._openUrl('${jsq(p.url.replace(/\/?$/, '/') + 'create_account_form.php')}')">${esc(T('cat.createAccount'))} ↗</button>
    </div>
    <div style="display:flex;flex-direction:column;gap:3px;font-size:12px">${lines.join('')}</div>
  </div>`
}

window._openUrl = async (url) => {
  try { await api('OpenURL', url) } catch (e) { toast(e.message || T('ui.opFailed'), 'err') }
}

window._catSearch = (q) => { cat.q = q; renderCatalog() }
window._catArea = (a) => { cat.area = a; renderCatalog() }
window._catHost = () => { renderCatalog(); renderCatalogDetail() }

window._catPick = async (i) => {
  const p = cat.shown[i]
  if (!p) return
  cat.sel = p; cat.cfg = null; cat.err = ''; cat.checking = true
  const url = $('#attach-url'); if (url) url.value = p.url
  const name = $('#attach-name'); if (name && !name.value) name.placeholder = p.name
  renderCatalog(); renderCatalogDetail()
  // Ask the project's server the way the BOINC manager does: it proves the
  // project is alive and tells which platforms it has applications for.
  try {
    const cfg = await api('GetProjectConfig', p.url)
    if (cat.sel === p) { cat.cfg = cfg; cat.checking = false }
  } catch (e) {
    if (cat.sel === p) { cat.err = String(e.message || e).slice(0, 120); cat.checking = false }
  }
  if (cat.sel === p) renderCatalogDetail()
}

window._showAttachProject = async () => {
  try { catalog = await api('GetProjectCatalog') || [] } catch (e) { catalog = [] }
  cat = { q: '', area: '', shown: [], sel: null, cfg: null, err: '', checking: false }
  attachUseKey = false
  const hostOpts = state.hosts.map(h => `<option value="${h.id}">${esc(h.name)}</option>`).join('')
  const areas = [...new Set(catalog.map(p => p.area).filter(Boolean))].sort()
  const areaOpts = `<option value="">${esc(T('cat.allAreas'))}</option>` + areas.map(a => `<option value="${esc(a)}">${esc(a)}</option>`).join('')
  state.modal = `
    <div class="modal-overlay" onclick="if(event.target===this)window._closeModal()">
      <div class="modal" style="max-width:680px">
        <div class="modal-head"><h3>${esc(T('proj.newTitle'))}</h3><button class="btn icon" onclick="window._closeModal()">✕</button></div>
        <div class="field"><label class="label">${esc(T('proj.server'))}</label><select class="select" id="attach-host" onchange="window._catHost()">${hostOpts}</select></div>
        <div class="field">
          <label class="label">${esc(T('proj.catalog'))}</label>
          <div class="row" style="gap:8px">
            <input class="input" id="cat-q" placeholder="${esc(T('proj.searchCat'))}" oninput="window._catSearch(this.value)" autocomplete="off">
            <select class="select" id="cat-area" style="max-width:230px" aria-label="${esc(T('cat.area'))}" onchange="window._catArea(this.value)">${areaOpts}</select>
          </div>
          <div id="cat-count" class="faint" style="font-size:11px;margin:6px 0 4px"></div>
          <div id="cat-list" class="cat-list"></div>
        </div>
        <div id="cat-detail" class="field"></div>
        <div class="faint" style="font-size:11px;margin:14px 0 6px;text-transform:uppercase;letter-spacing:.5px">${esc(T('cat.custom'))}</div>
        <div class="field"><label class="label">${esc(T('proj.url'))}</label><input class="input" id="attach-url" placeholder="${esc(T('proj.urlPh'))}"></div>
        <div id="attach-auth-fields">${renderAttachAuthFields()}</div>
        <div class="field"><label class="label">${esc(T('ui.displayName'))}</label><input class="input" id="attach-name"></div>
        <div id="attach-status" class="faint" style="font-size:11px;min-height:14px;margin-bottom:2px"></div>
        <div class="modal-foot">
          <button class="btn" onclick="window._closeModal()">${esc(T('common.cancel'))}</button>
          <button class="btn primary" onclick="window._doAttach()">${esc(T('proj.attach'))}</button>
        </div>
      </div>
    </div>
  `
  render()
  renderCatalog()
  renderCatalogDetail()
}

// The default (and recommended) way to attach is e-mail + password: it looks
// up the account key and attaches in one step. An already-known account key
// remains available for people who have it, one click away.
let attachUseKey = false

function renderAttachAuthFields() {
  if (attachUseKey) {
    return `
      <div class="field"><label class="label">${esc(T('proj.key'))}</label><input class="input" id="attach-auth" placeholder="${esc(T('proj.keyPh'))}"></div>
      <div class="faint" style="font-size:11px;margin:-8px 0 12px">${esc(T('proj.keyHint'))}</div>
      <button type="button" class="btn sm" style="margin-bottom:13px" onclick="window._attachToggleKey()">${esc(T('proj.useEmailInstead'))}</button>
    `
  }
  return `
    <div class="field"><label class="label">${esc(T('proj.email'))}</label><input class="input" id="attach-email" type="email" placeholder="you@example.com"></div>
    <div class="field"><label class="label">${esc(T('proj.pass'))}</label><input class="input" id="attach-pass" type="password"></div>
    <button type="button" class="btn sm" style="margin-bottom:13px" onclick="window._attachToggleKey()">${esc(T('proj.useKey'))}</button>
  `
}

window._attachToggleKey = () => {
  attachUseKey = !attachUseKey
  const el = $('#attach-auth-fields')
  if (el) el.innerHTML = renderAttachAuthFields()
  const status = $('#attach-status')
  if (status) status.innerHTML = ''
}

window._doAttach = async () => {
  const hostId = $('#attach-host')?.value
  const url = ($('#attach-url')?.value || '').trim()
  const name = $('#attach-name')?.value || ''
  const status = $('#attach-status')
  const fail = (msg) => { if (status) status.innerHTML = `<span style="color:var(--err)">${esc(msg)}</span>` }
  if (!hostId || !url) { fail(T('proj.needAttach')); return }

  let auth
  if (attachUseKey) {
    auth = ($('#attach-auth')?.value || '').trim()
    if (!auth) { fail(T('proj.needKey')); return }
  } else {
    const email = ($('#attach-email')?.value || '').trim()
    const pass = $('#attach-pass')?.value || ''
    if (!email || !pass) { fail(T('proj.needLookup')); return }
    if (status) status.innerHTML = `<span class="faint">${esc(T('proj.verifying'))}</span>`
    try {
      auth = await api('LookupAccount', url, email, pass)
    } catch (e) {
      fail(e.message || T('ui.notFound'))
      return
    }
  }

  try {
    await api('Attach', hostId, url, auth, name)
    state.modal = null
    await refreshHosts()
    toast(T('proj.attaching'), 'ok')
  } catch (e) {
    fail(e.message || T('ui.opFailed'))
  }
}

window._closeModal = () => { state.modal = null; render() }

window._doAddHost = async () => {
  const name = $('#add-name')?.value || T('ui.unnamed')
  const host = $('#add-host')?.value || 'localhost'
  const port = parseInt($('#add-port')?.value) || 31418
  const pass = $('#add-pass')?.value || ''
  try {
    await api('AddHost', name, host, port, pass)
    state.modal = null
    await refreshHosts()
    toast(T('hosts.added'), 'ok')
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._doEditHost = async (id) => {
  const name = $('#edit-name')?.value || T('ui.unnamed')
  const host = $('#edit-host')?.value || 'localhost'
  const port = parseInt($('#edit-port')?.value) || 31418
  const pass = $('#edit-pass')?.value || ''
  try {
    await api('UpdateHost', id, name, host, port, pass)
    state.modal = null
    await refreshHosts()
    toast(T('hosts.updated'), 'ok')
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

const ENERGY_KEYS = ['cpu_watts', 'gpu_watts', 'grid_gco2_kwh']
const ECO_KEYS = ['max_ncpus_pct', 'no_gpu']

window._openPrefs = async (hostId) => {
  const h = state.hosts.find(x => x.id === hostId)
  if (!h) return
  let prefs = {}
  try { prefs = await api('GetPrefs', hostId) } catch (e) {}
  const txt = Object.entries(prefs || {}).filter(([k]) => k !== 'real_apps_enabled' && k !== 'host_name' && !ENERGY_KEYS.includes(k) && !ECO_KEYS.includes(k)).map(([k, v]) => `${k}=${v}`).join('\n')
  const hostNameVal = prefs?.host_name || ''
  const enVal = k => prefs?.[k] || ''
  const noGpu = ['1', 'true', 'yes'].includes(String(prefs?.no_gpu || '').toLowerCase())
  const realAppsOn = prefs?.real_apps_enabled !== '0' // on unless explicitly turned off
  state.modal = `
    <div class="modal-overlay" onclick="if(event.target===this)window._closeModal()">
      <div class="modal" style="max-width:540px">
        <div class="modal-head"><h3>${esc(T('ui.globalPrefs', { name: h.name }))}</h3><button class="btn icon" onclick="window._closeModal()">✕</button></div>
        <div class="card" style="background:var(--bg-warn,rgba(234,88,12,0.08));border:1px solid rgba(234,88,12,0.35);margin-bottom:12px">
          <label class="row" style="gap:8px;align-items:flex-start;cursor:pointer">
            <input type="checkbox" id="real-apps-toggle" ${realAppsOn ? 'checked' : ''} onchange="window._toggleRealApps('${hostId}', this.checked)" style="margin-top:3px">
            <span style="font-weight:700">${esc(T('ui.realAppsToggle'))}</span>
          </label>
          <p class="faint" style="margin:6px 0 0;font-size:12px">${esc(T('ui.realAppsWarning'))}</p>
        </div>
        <div style="margin-bottom:12px">
          <label style="font-weight:700;display:block;margin-bottom:4px">${esc(T('ui.hostNameLabel'))}</label>
          <input class="input" id="host-name-input" type="text" maxlength="64" value="${jsq(hostNameVal)}" placeholder="${jsq(h.name)}" style="width:100%">
          <p class="faint" style="margin:4px 0 0;font-size:12px">${esc(T('ui.hostNameHint'))}</p>
        </div>
        <div style="margin-bottom:12px">
          <label style="font-weight:700;display:block;margin-bottom:4px">${esc(T('energy.section'))}</label>
          <div class="row wrap" style="gap:8px">
            <label style="flex:1;min-width:130px;font-size:12px">${esc(T('energy.setCpu'))}<input class="input" id="en-cpu_watts" type="number" min="1" step="1" value="${jsq(enVal('cpu_watts'))}" placeholder="65" style="width:100%"></label>
            <label style="flex:1;min-width:130px;font-size:12px">${esc(T('energy.setGpu'))}<input class="input" id="en-gpu_watts" type="number" min="1" step="1" value="${jsq(enVal('gpu_watts'))}" placeholder="150" style="width:100%"></label>
            <label style="flex:1;min-width:130px;font-size:12px">${esc(T('energy.setGrid'))}<input class="input" id="en-grid_gco2_kwh" type="number" min="1" step="1" value="${jsq(enVal('grid_gco2_kwh'))}" placeholder="475" style="width:100%"></label>
          </div>
          <p class="faint" style="margin:4px 0 0;font-size:12px">${esc(T('energy.setHint'))}</p>
        </div>
        <div style="margin-bottom:12px">
          <label style="font-weight:700;display:block;margin-bottom:4px">${esc(T('energy.eco'))}</label>
          <div class="row wrap" style="gap:12px;align-items:center">
            <label style="flex:1;min-width:150px;font-size:12px">${esc(T('energy.ecoCpu'))}<input class="input" id="eco-max_ncpus_pct" type="number" min="1" max="100" step="1" value="${jsq(enVal('max_ncpus_pct'))}" placeholder="100" style="width:100%"></label>
            <label class="row" style="gap:6px;font-size:12px;cursor:pointer"><input type="checkbox" id="eco-no_gpu" ${noGpu ? 'checked' : ''}>${esc(T('energy.ecoGpu'))}</label>
          </div>
          <p class="faint" style="margin:4px 0 0;font-size:12px">${esc(T('energy.ecoHint'))}</p>
        </div>
        <div class="faint" style="font-size:12px;margin-bottom:10px">${esc(T('ui.prefsHelp'))}</div>
        <textarea class="input kv" id="prefs-text" rows="14" spellcheck="false">${jsq(txt)}</textarea>
        <div class="modal-foot">
          <button class="btn" onclick="window._resetPrefs('${hostId}')">${esc(T('ui.resetDefaults'))}</button>
          <div style="flex:1"></div>
          <button class="btn" onclick="window._closeModal()">${esc(T('common.cancel'))}</button>
          <button class="btn primary" onclick="window._savePrefs('${hostId}')">${esc(T('hosts.save'))}</button>
        </div>
      </div>
    </div>
  `
  render()
}

// _toggleRealApps sets the experimental toggle immediately (it isn't queued
// with the raw key=value textarea's Save button) since it carries its own
// risk warning right next to it and should take effect the moment someone
// answers it, the same way the AI assistant's own consent switch does.
window._toggleRealApps = async (hostId, on) => {
  try {
    let prefs = {}
    try { prefs = await api('GetPrefs', hostId) } catch (e) {}
    const fields = Object.entries(prefs || {}).filter(([k]) => k !== 'real_apps_enabled').map(([k, v]) => [k, v])
    fields.push(['real_apps_enabled', on ? '1' : '0'])
    await api('SetPrefs', hostId, fields)
    toast(T('set.saved'), 'ok')
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._savePrefs = async (hostId) => {
  const raw = $('#prefs-text')?.value || ''
  const fields = []
  for (const line of raw.split('\n')) {
    const i = line.indexOf('=')
    if (i < 0) continue
    const k = line.slice(0, i).trim()
    const v = line.slice(i + 1).trim()
    if (!k || k === 'real_apps_enabled' || k === 'host_name' || ENERGY_KEYS.includes(k) || ECO_KEYS.includes(k)) continue // has its own checkbox, applied immediately by _toggleRealApps
    fields.push([k, v])
  }
  fields.push(['real_apps_enabled', $('#real-apps-toggle')?.checked ? '1' : '0'])
  const hostName = ($('#host-name-input')?.value || '').trim()
  if (hostName) fields.push(['host_name', hostName])
  {
    const pct = Number(($('#eco-max_ncpus_pct')?.value || '').trim())
    if (pct > 0 && pct < 100) fields.push(['max_ncpus_pct', String(Math.round(pct))])
    if ($('#eco-no_gpu')?.checked) fields.push(['no_gpu', '1'])
  }
  for (const k of ENERGY_KEYS) {
    const v = ($('#en-' + k)?.value || '').trim()
    if (v && Number(v) > 0) fields.push([k, v])
  }
  try {
    await api('SetPrefs', hostId, fields)
    state.modal = null
    toast(T('set.saved'), 'ok')
    await refreshHosts()
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._resetPrefs = async (hostId) => {
  try {
    await api('SetPrefs', hostId, [])
    state.modal = null
    toast(T('set.cleared'), 'ok')
    await refreshHosts()
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._startDaemon = async () => {
  try {
    await api('StartDaemon')
    state.daemonStatus = await api('GetDaemonStatus')
    render()
    toast(T('set.localStarted'), 'ok')
  } catch (e) {
    toast(e.message || T('set.localFailed'), 'err')
  }
}

window._stopDaemon = async () => {
  try {
    await api('StopDaemon')
    state.daemonStatus = await api('GetDaemonStatus')
    render()
    toast(T('set.localStopped'), 'ok')
  } catch (e) {
    toast(e.message || T('ui.opFailed'), 'err')
  }
}

window._requestNotifPermission = async () => {
  if (!('Notification' in window)) { toast(T('ui.notifUnsupported'), 'err'); return }
  const perm = await Notification.requestPermission()
  notifPermission = perm
  render()
  if (perm === 'granted') toast(T('ui.notifEnabled'), 'ok')
  else toast(T('notif.denied'), 'info')
}

window._testNotif = () => {
  if (notifPermission === 'granted') {
    new Notification(T('notif.title'), { body: T('ui.notifWorking') })
  } else {
    toast(T('ui.notifFirst'), 'info')
  }
}

function applyTheme() {
  const root = document.documentElement
  root.setAttribute('data-theme', state.theme)
  if (state.accent && state.accent !== 'violet' && ACCENTS[state.accent]) root.setAttribute('data-accent', state.accent)
  else root.removeAttribute('data-accent')
}

async function init() {
  applyTheme()
  if ('Notification' in window) notifPermission = Notification.permission
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && state.modal) window._closeModal()
  })
  await loadLanguage()
  render()
  if (window.runtime?.EventsOn) {
    window.runtime.EventsOn('notice', (n) => {
      const kind = n?.kind || ''
      const h = n?.hostName || ''
      const body = kind === 'deadline' ? T('notif.dl', { n: n.arg || '' })
        : kind === 'error' ? T('ui.tasksErrored', { h, n: n.count || 1 })
        : kind === 'offline' ? T('notif.offline', { h })
        : (n?.body || T('ui.taskNotice'))
      const title = T('notif.title')
      if (kind === 'deadline' || kind === 'error') toast(body, 'err')
      else if (kind === 'offline') toast(body, 'info')
      if (notifPermission === 'granted') new Notification(title, { body })
    })
  }
  try {
    state.daemonStatus = await api('GetDaemonStatus')
    state.daemonInfo = await api('DetectDaemon')
    state.about = await api('GetVersion')
  } catch (e) {}
  await refreshHosts()
  setInterval(refreshHosts, 4000)
}

init()