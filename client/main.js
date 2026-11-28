// 云测机桌面客户端（Electron 外壳，2026-11-27 用户要求做成客户端）。
//
// 它是个干净的桌面程序，加载板子上管家的管理界面（管家自带网页，端口 8443/HTTPS）。
// 相比浏览器，客户端把烦人的东西都内部处理好：
//   - 自动信任板子的自签证书（不弹「不安全」/ 证书警告）；
//   - 板子走 https:8443 本就是安全来源，实时画面视频解码(WebCodecs)直接能用，不用再加命令行标记
//     （不会弹「不受支持的命令行标记」警告条）；
//   - 没有地址栏书签栏，就是一个管理程序；
//   - 点 X 缩小到系统托盘（后台继续跑），托盘里才真退出（2026-11-27 用户要求）；
//   - 自己检查更新、下载新安装包、静默装好重启（不用每次手动下，2026-11-27 用户要求）。
// 界面本身是管家网页那套（照魔云腾改版的，改网页端客户端自动跟着变）。
//
// 服务器管理：第一次打开让用户「添加服务器」（把板子加进来，可加多块），连上就用；地址存在本地。

const { app, BrowserWindow, Menu, Tray, nativeImage, dialog, shell, ipcMain, net } = require('electron')
const fs = require('fs')
const path = require('path')
const crypto = require('crypto')
const { spawn } = require('child_process')

const CONFIG = path.join(app.getPath('userData'), 'config.json')
const ICON = path.join(__dirname, 'icon.png')

// 自更新：从固定标签 client-latest 的 release 资产取版本号和安装包（和管家在线升级一个套路）。
const REPO = 'zhangchedan000/cloudtest-phone'
const REL_BASE = `https://github.com/${REPO}/releases/download/client-latest`
const VER_URL = `${REL_BASE}/client.version`
const SETUP_URL = `${REL_BASE}/yuncs-client-setup-1.0.0.exe`
const RELEASE_PAGE = `https://github.com/${REPO}/releases/tag/client-latest`

// 本机构建号（CI 打包时写进 buildinfo.json；开发态没有就是 dev，dev 不自动检查更新）。
function currentBuild() {
  try { return String(JSON.parse(fs.readFileSync(path.join(__dirname, 'buildinfo.json'), 'utf8')).build || 'dev') } catch { return 'dev' }
}

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

// 忽略板子自签证书的错误（https:8443 用）。必须在 app ready 之前设好（Chromium 启动时读命令行）。
// 板子走 https:8443 本就是「安全来源」，视频解码(WebCodecs)直接能用，不用再加命令行标记——
// 以前给每个服务器硬塞一条 http://IP:8099 当安全来源，会弹「不受支持的命令行标记」警告，已去掉
// （2026-11-28 用户要求）。只有用户真用 http 地址的服务器才加这个标记（那是他自己的选择）。
function applySecurity(servers) {
  app.commandLine.appendSwitch('ignore-certificate-errors')
  const origins = servers.map((sv) => String(sv && sv.url || '')).filter((u) => /^http:\/\//i.test(u))
  if (origins.length) app.commandLine.appendSwitch('unsafely-treat-insecure-origin-as-secure', origins.join(','))
}

let win
let tray
let currentServer = ''
let quitting = false

function buildMenu() {
  Menu.setApplicationMenu(Menu.buildFromTemplate([
    {
      label: '云测机',
      submenu: [
        { label: '服务器管理 / 添加板子', click: () => { showWindow(); openSetup() } },
        { label: '刷新', accelerator: 'CmdOrCtrl+R', click: () => win && win.reload() },
        { label: '检查更新', click: () => checkUpdate({ manual: true }) },
        { type: 'separator' },
        { label: '退出', click: () => reallyQuit() }
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

// 系统托盘：点 X 缩到这里，后台继续跑；托盘菜单里才真退出。
function createTray() {
  if (tray) return
  try {
    const img = nativeImage.createFromPath(ICON)
    tray = new Tray(img.isEmpty() ? nativeImage.createEmpty() : img)
    tray.setToolTip('云测机')
    tray.setContextMenu(Menu.buildFromTemplate([
      { label: '显示主界面', click: () => showWindow() },
      { label: '检查更新', click: () => checkUpdate({ manual: true }) },
      { type: 'separator' },
      { label: '退出', click: () => reallyQuit() }
    ]))
    tray.on('double-click', () => showWindow())
    tray.on('click', () => showWindow())
  } catch { /* 托盘建不出来就算了，不影响主程序 */ }
}

function showWindow() {
  if (!win || win.isDestroyed()) { createWindow(); return }
  if (win.isMinimized()) win.restore()
  win.show()
  win.focus()
}

function reallyQuit() {
  quitting = true
  app.quit()
}

function createWindow() {
  const c = loadConfig()
  const b = c.bounds || {}
  win = new BrowserWindow({
    width: b.width || 1360, height: b.height || 860,
    x: typeof b.x === 'number' ? b.x : undefined, y: typeof b.y === 'number' ? b.y : undefined,
    minWidth: 980, minHeight: 620,
    title: '云测机', backgroundColor: '#f5f6f8', autoHideMenuBar: true, icon: ICON,
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
  // 关闭行为：点 X 默认缩到托盘（后台继续跑）；设置里选「退出」才真关。
  win.on('close', (e) => {
    saveBounds()
    if (!quitting && loadConfig().closeBehavior !== 'quit') {
      e.preventDefault()
      win.hide()
      if (!win._trayNoticeShown) {
        win._trayNoticeShown = true
        if (tray && process.platform === 'win32') {
          try { tray.displayBalloon({ title: '云测机还在后台运行', content: '点托盘图标可重新打开；要彻底退出，右键托盘→退出。' }) } catch { /* 忽略 */ }
        }
      }
    }
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

// ---- 自动更新（仿管家在线升级：比对构建号 → 下载安装包 → 静默装 → 重启）----
let updating = false

// 用 electron net 取文本（自动跟随 GitHub 的重定向）。
function fetchText(url) {
  return new Promise((resolve, reject) => {
    const req = net.request({ url, redirect: 'follow' })
    req.on('response', (res) => {
      if (res.statusCode >= 400) { reject(new Error('HTTP ' + res.statusCode)); return }
      let buf = ''
      res.on('data', (c) => { buf += c.toString() })
      res.on('end', () => resolve(buf.trim()))
    })
    req.on('error', reject)
    req.end()
  })
}

// 下载文件到本地，onProgress(0~1)。
function downloadFile(url, dest, onProgress) {
  return new Promise((resolve, reject) => {
    const req = net.request({ url, redirect: 'follow' })
    req.on('response', (res) => {
      if (res.statusCode >= 400) { reject(new Error('HTTP ' + res.statusCode)); return }
      const total = Number(res.headers['content-length'] || 0)
      let got = 0
      const out = fs.createWriteStream(dest)
      res.on('data', (c) => { got += c.length; out.write(c); if (total && onProgress) onProgress(got / total) })
      res.on('end', () => { out.end(() => resolve()) })
      res.on('error', reject)
    })
    req.on('error', reject)
    req.end()
  })
}

async function checkUpdate({ manual } = {}) {
  if (updating) { if (manual) dialog.showMessageBox(win, { message: '更新正在进行中…' }); return }
  const cur = currentBuild()
  if (cur === 'dev' && !manual) return   // 开发态不自动打扰
  let latest
  try { latest = await fetchText(VER_URL) } catch (e) {
    if (manual) dialog.showMessageBox(win, { type: 'error', message: '检查更新失败', detail: '连不上更新服务器：' + e.message })
    return
  }
  if (!latest || latest === cur) {
    if (manual) dialog.showMessageBox(win, { type: 'info', message: '已是最新版本', detail: '当前版本：' + cur })
    return
  }
  const pick = dialog.showMessageBoxSync(win, {
    type: 'info', buttons: ['现在更新', '以后再说'], defaultId: 0, cancelId: 1,
    message: '发现新版本', detail: `有新版客户端可用（${latest}）。现在下载并安装吗？\n安装时会自动关闭本程序、装好后重开。`
  })
  if (pick !== 0) return
  updating = true
  const dest = path.join(app.getPath('temp'), 'yuncs-client-setup.exe')
  try {
    if (win && !win.isDestroyed()) win.setProgressBar(0)
    await downloadFile(SETUP_URL, dest, (p) => { if (win && !win.isDestroyed()) win.setProgressBar(p) })
    if (win && !win.isDestroyed()) win.setProgressBar(-1)
    if (!fs.existsSync(dest) || fs.statSync(dest).size < 1024 * 1024) throw new Error('下载的安装包不完整')
    // 静默安装（NSIS /S），装完自动重开；先启动安装器再退出本程序，免得文件被占用。
    const child = spawn(dest, ['/S'], { detached: true, stdio: 'ignore' })
    child.unref()
    setTimeout(() => reallyQuit(), 800)
  } catch (e) {
    updating = false
    if (win && !win.isDestroyed()) win.setProgressBar(-1)
    const pick2 = dialog.showMessageBoxSync(win, {
      type: 'error', buttons: ['打开下载页', '取消'], defaultId: 0,
      message: '自动更新失败', detail: e.message + '\n\n可以手动去下载页下载最新安装包。'
    })
    if (pick2 === 0) shell.openExternal(RELEASE_PAGE)
  }
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

// ---- 设置页用的通道：关闭行为 / 开机自启动 / 客户端更新（preload 暴露成 window.pp.*）----
ipcMain.handle('pp-get-prefs', () => {
  const c = loadConfig()
  return { closeBehavior: c.closeBehavior === 'quit' ? 'quit' : 'minimize', autoStart: !!c.autoStart, clientBuild: currentBuild() }
})
ipcMain.on('pp-set-pref', (_e, key, value) => {
  const c = loadConfig()
  if (key === 'closeBehavior') c.closeBehavior = value === 'quit' ? 'quit' : 'minimize'
  if (key === 'autoStart') c.autoStart = !!value
  saveConfig(c)
  if (key === 'autoStart') applyAutoStart()
})
ipcMain.on('pp-check-update', () => checkUpdate({ manual: true }))

// 自签证书：直接信任（都是用户自己局域网里的板子）。
app.on('certificate-error', (event, _wc, _url, _err, _cert, callback) => {
  event.preventDefault()
  callback(true)
})

applySecurity(loadConfig().servers)

app.on('before-quit', () => { quitting = true })

// 单实例：再开一个就把已开的窗口拉到前台，不开第二个。
if (!app.requestSingleInstanceLock()) {
  app.quit()
} else {
  app.on('second-instance', () => showWindow())
  app.whenReady().then(() => {
    buildMenu()
    createTray()
    applyAutoStart()
    createWindow()
    setTimeout(() => checkUpdate({}), 4000)   // 启动几秒后静默查一次更新
    app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createWindow(); else showWindow() })
  })
}

// 缩到托盘后窗口是「隐藏」不是「关闭」，所以这里只有真退出时才会触发；保险起见非 mac 直接退。
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
