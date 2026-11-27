// 预加载脚本：给「服务器管理」页（setup.html）一套可靠的通道，和主进程打交道（比靠 URL 跳转稳）。
const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('pp', {
  // 拿服务器列表 + 当前连的是哪个：{ servers: [{id,name,url}], current }
  getServers: () => ipcRenderer.invoke('pp-get-servers'),
  // 加一台服务器（板子）：{name, url} → 返回最新列表
  addServer: (name, url) => ipcRenderer.invoke('pp-add-server', { name, url }),
  // 删一台
  deleteServer: (id) => ipcRenderer.invoke('pp-delete-server', id),
  // 连接某台（主进程去加载它的界面）
  connect: (id) => ipcRenderer.send('pp-connect', id),
  // 设置页：关闭行为 / 开机自启动 / 客户端构建号（getPrefs 返回里带 clientBuild）
  getPrefs: () => ipcRenderer.invoke('pp-get-prefs'),
  setPref: (key, value) => ipcRenderer.send('pp-set-pref', key, value),
  // 检查客户端自身更新（下载新安装包、静默装、重启）
  checkUpdate: () => ipcRenderer.send('pp-check-update')
})
