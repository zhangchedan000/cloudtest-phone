// 云测机桌面客户端（Electron 外壳，2026-11-27 用户要求做成客户端）。
//
// 它就是一个干净的桌面窗口，加载板子上管家的管理界面（管家自带网页，端口 8443/HTTPS）。
// 相比浏览器，客户端把那些烦人的东西都内部处理好了：
//   - 自动信任板子的自签证书（不再弹「不安全」/ 证书警告）；
//   - 把板子地址当成「安全来源」，实时画面的视频解码（WebCodecs）直接能用，不用再给浏览器加命令行标记；
//   - 没有浏览器地址栏、书签栏，就是一个管理程序。
// 界面本身还是管家网页那套（以后照魔云腾重做，改的是网页端，客户端自动跟着变）。

const { app, BrowserWindow, Menu, dialog, shell, session } = require('electron')
const fs = require('fs')
const path = require('path')

const CONFIG = path.join(app.getPath('userData'), 'config.json')

function loadConfig() {
  try { return JSON.parse(fs.readFileSync(CONFIG, 'utf8')) } catch { return {} }
}
function saveConfig(c) {
  try { fs.mkdirSync(path.dirname(CONFIG), { recursive: true }); fs.writeFileSync(CONFIG, JSON.stringify(c, null, 2)) } catch { /* ignore */ }
}

// 规整用户填的地址：补上 https://、默认端口 8443。
function normalizeServer(raw) {
  let s = (raw || '').trim()
  if (!s) return ''
  if (!/^https?:\/\//i.test(s)) s = 'https://' + s
  try {
    const u = new URL(s)
    if (!u.port && u.protocol === 'https:') u.port = '8443'
    return u.origin
  } catch { return '' }
}

// 把配置里的板子地址当成「安全来源」，并忽略它的自签证书错误——WebCodecs 视频才能用、不弹证书警告。
function applySecurity(server) {
  app.commandLine.appendSwitch('ignore-certificate-errors')
  if (server) {
    // 板子的 http(备用) 和 https 都标成安全来源
    let origins = server
    try { const u = new URL(server); origins += ',http://' + u.hostname + ':8099' } catch { /* ignore */ }
    app.commandLine.appendSwitch('unsafely-treat-insecure-origin-as-secure', origins)
    app.commandLine.appendSwitch('disable-features', 'InsecureDownloadWarnings')
  }
}

let win
let currentServer = ''

function buildMenu() {
  const template = [
    {
      label: '云测机',
      submenu: [
        { label: '连接设置（换板子地址）', click: () => openSetup() },
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
  ]
  Menu.setApplicationMenu(Menu.buildFromTemplate(template))
}

function createWindow() {
  win = new BrowserWindow({
    width: 1360,
    height: 860,
    minWidth: 960,
    minHeight: 600,
    title: '云测机',
    backgroundColor: '#0f1115',
    autoHideMenuBar: true,
    webPreferences: {
      // 管家界面同源访问自己的 API，不涉及跨域；关掉一些对 LAN 自签场景没必要的限制。
      webSecurity: true,
      spellcheck: false
    }
  })
  // 新开窗口（比如「截图新窗口打开原图」）用系统浏览器打开，别在客户端里乱开
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (currentServer && url.startsWith(currentServer)) return { action: 'allow' }
    shell.openExternal(url)
    return { action: 'deny' }
  })
  loadServerOrSetup()
}

function loadServerOrSetup() {
  if (currentServer) win.loadURL(currentServer)
  else openSetup()
}

function openSetup() {
  win.loadFile(path.join(__dirname, 'setup.html'), { query: { current: currentServer } })
}

// 渲染进程（setup.html）保存地址时通过加载一个特殊 URL 通知主进程。
app.on('web-contents-created', (_e, contents) => {
  contents.on('will-navigate', (e, url) => {
    if (url.startsWith('app://set-server/')) {
      e.preventDefault()
      const raw = decodeURIComponent(url.slice('app://set-server/'.length))
      const srv = normalizeServer(raw)
      if (!srv) { dialog.showErrorBox('地址不对', '请填板子的地址，例如 192.168.68.194'); return }
      currentServer = srv
      const c = loadConfig(); c.server = srv; saveConfig(c)
      win.loadURL(srv)
    }
  })
})

// 自签证书：直接信任（都是用户自己局域网里的板子）
app.on('certificate-error', (event, _wc, _url, _err, _cert, callback) => {
  event.preventDefault()
  callback(true)
})

// 安全开关必须在 app ready 之前设好（Chromium 启动时才读命令行），所以在这里（模块加载时）就读配置、设开关。
currentServer = normalizeServer(loadConfig().server || '')
applySecurity(currentServer)

app.whenReady().then(() => {
  buildMenu()
  createWindow()
  app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createWindow() })
})

app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
