// 云测机桌面客户端（Electron 外壳，2026-11-27 用户要求做成客户端）。
//
// 它是个干净的桌面程序，加载板子上管家的管理界面（管家自带网页，端口 8443/HTTPS）。
// 相比浏览器，客户端把烦人的东西都内部处理好：
//   - 自动信任板子的自签证书（不弹「不安全」/ 证书警告）；
//   - 把板子地址当「安全来源」，实时画面视频解码(WebCodecs)直接能用，不用再给浏览器加命令行标记；
//   - 没有地址栏书签栏，就是一个管理程序。
// 界面本身是管家网页那套（照魔云腾改版的，改网页端客户端自动跟着变）。
//
// 服务器管理：第一次打开让用户「添加服务器」（把板子加进来，可加多块），连上就用；地址存在本地。

const { app, BrowserWindow, Menu, shell, ipcMain } = require('electron')
const fs = require('fs')
const path = require('path')
const crypto = require('crypto')

const CONFIG = path.join(app.getPath('userData'), 'config.json')

function loadConfig() {
  try {
    const c = JSON.parse(fs.readFileSync(CONFIG, 'utf8'))
    if (!Array.isArray(c.servers)) c.servers = []
    return c
  } catch { return { servers: [], current: '' } }
}
function saveConfig(c) {
  try { fs.mkdirSync(path.dirname(CONFIG), { recursive: true }); fs.writeFileSync(CONFIG, JSON.stringify(c, null, 2)) } catch { /* ignore */ }
}

// 规整地址：补 https://、默认端口 8443。
function normalizeServer(raw) {
  let s = String(raw || '').trim()
  if (!s) return ''
  if (!/^https?:\/\//i.test(s)) s = 'https://' + s
  try {
    const u = new URL(s)
    if (!u.port && u.protocol === 'https:') u.port = '8443'
    return u.origin
  } catch { return '' }
}

// 把所有已添加服务器的地址都标成「安全来源」+ 忽略自签证书错误（视频能播、不弹警告）。
// 必须在 app ready 之前设好（Chromium 启动时读命令行）。
function applySecurity(servers) {
  app.commandLine.appendSwitch('ignore-certificate-errors')
  const origins = []
  for (const sv of servers) {
    if (!sv.url) continue
    origins.push(sv.url)
    try { const u = new URL(sv.url); origins.push('http://' + u.hostname + ':8099') } catch { /* ignore */ }
  }
  if (origins.length) app.commandLine.appendSwitch('unsafely-treat-insecure-origin-as-secure', origins.join(','))
}

let win
let currentServer = ''

function buildMenu() {
  Menu.setApplicationMenu(Menu.buildFromTemplate([
    {
      label: '云测机',
      submenu: [
        { label: '服务器管理 / 添加板子', click: () => openSetup() },
        { label: '刷新', accelerator: 'CmdOrCtrl+R', click: () => win && win.reload() },
        { type: 'separator' },
        { label: '退出', role: 'quit' }
      ]
    },
    {
      label: '视图',
      submenu: [
        { label: '放大', role: 'zoomIn' },
        { label: '缩小', role: 'zoomOut' },
        { label: '实际大小', role: 'resetZoom' },
        { label: '全屏', role: 'togglefullscreen' },
        { type: 'separator' },
        { label: '开发者工具', accelerator: 'F12', click: () => win && win.webContents.toggleDevTools() }
      ]
    }
  ]))
}

let quitting = false

function createWindow() {
  const c = loadConfig()
  const b = c.bounds || {}
  win = new BrowserWindow({
    width: b.width || 1360, height: b.height || 860,
    x: typeof b.x === 'number' ? b.x : undefined, y: typeof b.y === 'number' ? b.y : undefined,
    minWidth: 980, minHeight: 620,
    title: '云测机', backgroundColor: '#f5f6f8', autoHideMenuBar: true,
    webPreferences: { preload: path.join(__dirname, 'preload.js'), spellcheck: false }
  })
  if (b.maximized) win.maximize()
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (currentServer && url.startsWith(currentServer)) return { action: 'allow' }
    shell.openExternal(url)
    return { action: 'deny' }
  })
  // 记住窗口大小和位置：改动时存起来，下次打开照上次的（2026-11-27 用户要求）
  const saveBounds = () => {
    if (!win || win.isDestroyed()) return
    const cfg = loadConfig()
    cfg.bounds = win.isMaximized() ? { ...(cfg.bounds || {}), maximized: true } : { ...win.getBounds(), maximized: false }
    saveConfig(cfg)
  }
  let t
  const debounced = () => { clearTimeout(t); t = setTimeout(saveBounds, 500) }
  win.on('resize', debounced)
  win.on('move', debounced)
  win.on('maximize', saveBounds)
  win.on('unmaximize', saveBounds)
  // 关闭行为：退出（默认）或最小化（点 X 只最小化、程序继续跑）
  win.on('close', (e) => {
    saveBounds()
    if (!quitting && (loadConfig().closeBehavior === 'minimize')) { e.preventDefault(); win.minimize() }
  })
  showStartup()
}

// 开机自启动（Windows / macOS）：按设置开关
function applyAutoStart() {
  try { app.setLoginItemSettings({ openAtLogin: !!loadConfig().autoStart }) } catch { /* 忽略 */ }
}

// 启动时：有「当前服务器」就直接连；否则打开服务器管理页让用户添加/选择。
function showStartup() {
  const c = loadConfig()
  const cur = c.servers.find((s) => s.id === c.current)
  if (cur) loadServer(cur)
  else openSetup()
}

function loadServer(sv) {
  currentServer = sv.url
  win.loadURL(sv.url)
}

function openSetup() {
  win.loadFile(path.join(__dirname, 'setup.html'))
}

// ---- 服务器管理的通道（setup.html 通过 preload 调）----
ipcMain.handle('pp-get-servers', () => {
  const c = loadConfig()
  return { servers: c.servers, current: c.current }
})
ipcMain.handle('pp-add-server', (_e, { name, url }) => {
  const u = normalizeServer(url)
  if (!u) return { error: '地址不对，填板子的 IP，例如 192.168.68.194' }
  const c = loadConfig()
  if (c.servers.some((s) => s.url === u)) return { error: '这台已经加过了' }
  const sv = { id: crypto.randomUUID(), name: String(name || '').trim() || u.replace(/^https?:\/\//, ''), url: u }
  c.servers.push(sv)
  saveConfig(c)
  return { servers: c.servers, current: c.current }
})
ipcMain.handle('pp-delete-server', (_e, id) => {
  const c = loadConfig()
  c.servers = c.servers.filter((s) => s.id !== id)
  if (c.current === id) c.current = ''
  saveConfig(c)
  return { servers: c.servers, current: c.current }
})
ipcMain.on('pp-connect', (_e, id) => {
  const c = loadConfig()
  const sv = c.servers.find((s) => s.id === id)
  if (!sv) return
  c.current = id
  saveConfig(c)
  loadServer(sv)
})

// ---- 设置页用的通道：关闭行为 / 开机自启动（preload 暴露成 window.pp.getPrefs / setPref）----
ipcMain.handle('pp-get-prefs', () => {
  const c = loadConfig()
  return { closeBehavior: c.closeBehavior === 'minimize' ? 'minimize' : 'quit', autoStart: !!c.autoStart }
})
ipcMain.on('pp-set-pref', (_e, key, value) => {
  const c = loadConfig()
  if (key === 'closeBehavior') c.closeBehavior = value === 'minimize' ? 'minimize' : 'quit'
  if (key === 'autoStart') c.autoStart = !!value
  saveConfig(c)
  if (key === 'autoStart') applyAutoStart()
})

// 自签证书：直接信任（都是用户自己局域网里的板子）。
app.on('certificate-error', (event, _wc, _url, _err, _cert, callback) => {
  event.preventDefault()
  callback(true)
})

applySecurity(loadConfig().servers)

app.on('before-quit', () => { quitting = true })

app.whenReady().then(() => {
  buildMenu()
  applyAutoStart()
  createWindow()
  app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createWindow() })
})

app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
