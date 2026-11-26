import type { FieldSetting, Group, Phone, Proxy, ProxyCheck, DeviceModel } from './types'

/**
 * 管家程序的接口。管理页由管家程序自己提供，所以地址就是当前网页的 /api/。
 * 设置了 PP_TOKEN 的机器要在页面上填一次 token（存在浏览器里）。
 */
const TOKEN_KEY = 'pp_token'

export function savedToken(): string {
  try { return localStorage.getItem(TOKEN_KEY) ?? '' } catch { return '' }
}

export function saveToken(token: string): void {
  try { localStorage.setItem(TOKEN_KEY, token) } catch { /* 无痕模式：只在本次打开有效 */ }
}

export class ApiError extends Error {
  constructor(message: string, readonly status: number) { super(message) }
}

/**
 * 多台服务器（2026-11-15）：打开管理页的这台叫「本机」（LOCAL），别的服务器的地址和口令由本机记着（/api/servers）。
 * 页面直接连每一台（画面、操作不经本机中转）。每台手机记住自己在哪台服务器上，
 * 所以 /phones/<id>/… 的请求自动发到那台、带那台的口令；代理、分组等按调用时给的服务器 ID 发。
 */
export const LOCAL = 'local'
export interface ServerRef { id: string; name: string; url: string; token: string }
const remotes = new Map<string, ServerRef>()
const phoneServer = new Map<string, string>()

export function setRemoteServers(list: ServerRef[]): void {
  remotes.clear()
  for (const s of list) remotes.set(s.id, s)
}
export function rememberPhoneServer(phoneId: string, serverId: string): void { phoneServer.set(phoneId, serverId) }
export function serverOfPhone(phoneId: string): string { return phoneServer.get(phoneId) ?? LOCAL }
/** 服务器的网页地址（本机返回当前网页的地址）。 */
export function serverUrl(serverId: string | undefined): string {
  const r = serverId && serverId !== LOCAL ? remotes.get(serverId) : undefined
  return r ? r.url : location.origin
}

function endpoint(path: string, serverId?: string): { base: string; token: string } {
  const m = serverId ? null : /^\/phones\/([^/?]+)/.exec(path)
  const sid = serverId ?? (m ? serverOfPhone(m[1]) : LOCAL)
  const r = sid !== LOCAL ? remotes.get(sid) : undefined
  return r ? { base: r.url, token: r.token } : { base: '', token: savedToken() }
}
/** 接口的完整地址（按手机或给定的服务器选机器）。 */
export function apiUrl(path: string, serverId?: string): string { return `${endpoint(path, serverId).base}/api${path}` }
/** 带上那台服务器的口令。 */
export function authHeaders(path: string, serverId?: string): Record<string, string> {
  const t = endpoint(path, serverId).token
  return t ? { Authorization: `Bearer ${t}` } : {}
}

async function call<T>(method: string, path: string, body?: unknown, serverId?: string): Promise<T> {
  let response: Response
  try {
    response = await fetch(apiUrl(path, serverId), {
      method,
      headers: { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...authHeaders(path, serverId) },
      body: body === undefined ? undefined : JSON.stringify(body)
    })
  } catch {
    // 浏览器只给一句 Failed to fetch：换成看得懂的话
    throw new ApiError(`连不上${serverId && serverId !== LOCAL ? '这台服务器' : '管家程序'}（网络不通，或者那台的管家程序没在运行）`, 0)
  }
  const text = await response.text()
  let data: unknown
  try { data = text ? JSON.parse(text) : undefined } catch { data = undefined }
  if (!response.ok) {
    const message = (data as { error?: string } | undefined)?.error ?? `管家程序返回 ${response.status}`
    throw new ApiError(message, response.status)
  }
  return data as T
}

/** 管家程序返回的设备：在页面用的 Phone 上多了检查结果。 */
export interface PhoneView extends Phone { ip?: string; issues?: { level: string; field?: string; message: string }[] }

export interface HostInfo {
  id: string
  name: string
  gpuFamily: string
  devHost: boolean
  maxRunning: number
  androidVersions: number[]
  gappsVersions?: number[]
  system?: Record<string, unknown>
  network?: boolean
  netHealth?: { ok: boolean; problems?: string[]; healedAt?: string; checkedAt: string }
  /** 这台机器带正面小屏（魔云腾 Q1 盒子带，服务器一般没有） */
  oled?: boolean
  /** 小屏当前点亮（false = 熄屏） */
  oledOn?: boolean
}

export interface Backup { id: string; phoneId: string; phoneName: string; modelId: string; androidVersion: number; gapps: boolean; note?: string; createdAt: string; kind: 'image' | 'dir'; sizeMB: number }
export interface TrashItem { name: string; phoneId: string; kind: 'delete' | 'reset' | 'restore'; at: string; sizeMB: number }

export interface InputRequest { kind: 'tap' | 'swipe' | 'text' | 'key' | 'rotate'; /** 坐标是横屏画面上的（群控换算用） */ landscape?: boolean; x?: number; y?: number; x2?: number; y2?: number; durationMs?: number; text?: string; key?: string; /** 电脑键盘打字：按人的节奏发、中文粘完放回原剪贴板 */ human?: boolean }

export const api = {
  host: (sid?: string) => call<HostInfo>('GET', '/host', undefined, sid),
  renameHost: (name: string, sid?: string) => call<{ name: string }>('PUT', '/host', { name }, sid),
  /** 开/关这台机器正面的小屏（魔云腾 Q1 盒子）。on=true 点亮，false 熄屏。 */
  setOled: (on: boolean, sid?: string) => call<{ on: boolean }>('POST', '/oled', { on }, sid),
  phones: (sid?: string) => call<PhoneView[]>('GET', '/phones', undefined, sid),
  createPhone: (req: { name: string; modelId: string; androidVersion: number; proxyId: string; groupId: string; note?: string; gapps?: boolean }, sid?: string) => call<Phone>('POST', '/phones', req, sid),
  savePhone: (id: string, req: { fields?: Record<string, FieldSetting>; modelId?: string; proxyId?: string; proxyIds?: string[]; groupId?: string; name?: string; note?: string; gapps?: boolean; adb?: boolean }) => call('PUT', `/phones/${id}`, req),
  /** 切换这台当前用的代理（开着的手机时区马上跟着换）。 */
  switchProxy: (id: string, proxyId: string) => call<{ proxyId: string; timezone?: string; warning?: string }>('POST', `/phones/${id}/proxy`, { proxyId }),
  importParams: (id: string, dump: string, sid?: string) => call<{ imported: string[]; missing: string[] }>('POST', `/phones/${id}/import-params`, { dump }, sid),
  deletePhone: (id: string) => call('DELETE', `/phones/${id}`),
  start: (id: string) => call('POST', `/phones/${id}/start`),
  stop: (id: string) => call('POST', `/phones/${id}/stop`),
  reset: (id: string, androidVersion?: number) => call('POST', `/phones/${id}/reset`, { renew: false, androidVersion: androidVersion ?? 0 }),
  renew: (id: string) => call('POST', `/phones/${id}/renew`),
  input: (id: string, req: InputRequest) => call<{ ok: boolean; landscape?: boolean; wantLandscape?: boolean }>('POST', `/phones/${id}/input`, req),
  apps: (id: string) => call<{ apps: { package: string; name: string; activity?: string; user: boolean; icon?: string }[] }>('GET', `/phones/${id}/apps`),
  appAction: (id: string, action: 'launch' | 'uninstall', pkg: string, activity?: string) => call<{ ok: boolean }>('POST', `/phones/${id}/apps`, { action, package: pkg, activity }),
  proxies: (sid?: string) => call<Proxy[]>('GET', '/proxies', undefined, sid),
  createProxy: (proxy: Omit<Proxy, 'id'>, sid?: string) => call<Proxy>('POST', '/proxies', proxy, sid),
  checkProxy: (id: string, sid?: string) => call<ProxyCheck>('POST', `/proxies/${id}/check`, undefined, sid),
  deleteProxy: (id: string, sid?: string) => call('DELETE', `/proxies/${id}`, undefined, sid),
  importProxies: (req: { url?: string; text?: string; note?: string }, sid?: string) => call<{ added: number; duplicates: number; skipped?: string[] }>('POST', '/proxies/import', req, sid),
  backups: (sid?: string) => call<Backup[]>('GET', '/backups', undefined, sid),
  /** 在后台备份（开着的手机会先关机，备份完再开回来）。 */
  backup: (id: string, note: string) => call('POST', `/phones/${id}/backup`, { note }),
  /** 在后台把备份恢复到这台（这台现在的数据挪进回收站）。 */
  restore: (id: string, backupId: string) => call('POST', `/phones/${id}/restore`, { backupId }),
  deleteBackup: (bid: string, sid?: string) => call('DELETE', `/backups/${encodeURIComponent(bid)}`, undefined, sid),
  trash: (sid?: string) => call<TrashItem[]>('GET', '/trash', undefined, sid),
  deleteTrash: (name: string, sid?: string) => call('DELETE', `/trash/${encodeURIComponent(name)}`, undefined, sid),
  groups: (sid?: string) => call<Group[]>('GET', '/groups', undefined, sid),
  createGroup: (group: Omit<Group, 'id'>, sid?: string) => call<Group>('POST', '/groups', group, sid),
  updateGroup: (group: Group, sid?: string) => call<Group>('PUT', `/groups/${group.id}`, group, sid),
  deleteGroup: (id: string, sid?: string) => call('DELETE', `/groups/${id}`, undefined, sid),
  /** 机型库（内置 + 本地导入的）。 */
  models: (sid?: string) => call<DeviceModel[]>('GET', '/models', undefined, sid),
  /** 重新读本地机型库（手动往 <数据目录>/models 放了档案之后）。 */
  reloadModels: (sid?: string) => call<{ builtin: number; local: number }>('POST', '/models/reload', undefined, sid),
  /** 别的服务器（只在本机上管）。 */
  servers: () => call<ServerRef[]>('GET', '/servers', undefined, LOCAL),
  addServer: (req: { name?: string; url: string; token: string }) => call<ServerRef>('POST', '/servers', req, LOCAL),
  updateServer: (id: string, req: { name?: string; token?: string }) => call<ServerRef>('PUT', `/servers/${id}`, req, LOCAL),
  deleteServer: (id: string) => call('DELETE', `/servers/${id}`, undefined, LOCAL)
}

/** 上传文件（安装 APK / 传到手机目录），带进度回调。 */
export function upload(path: string, file: File, onProgress?: (percent: number) => void, serverId?: string): Promise<{ ok: boolean; path?: string; output?: string }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', apiUrl(path, serverId))
    for (const [k, v] of Object.entries(authHeaders(path, serverId))) xhr.setRequestHeader(k, v)
    xhr.upload.onprogress = (event) => { if (event.lengthComputable) onProgress?.(Math.round((event.loaded / event.total) * 100)) }
    xhr.onload = () => {
      let data: { error?: string } & Record<string, unknown> = {}
      try { data = JSON.parse(xhr.responseText) } catch { /* 非 JSON */ }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as { ok: boolean })
      else reject(new ApiError(data.error ?? `上传失败（${xhr.status}）`, xhr.status))
    }
    xhr.onerror = () => reject(new ApiError('上传失败：网络断了', 0))
    const form = new FormData()
    form.append('file', file)
    xhr.send(form)
  })
}

export interface ModelImportResult { output: string[]; builtin: number; local: number }

/** 导入机型包（我们采集的 / 魔云腾的 zip，可以多个）到这台服务器的本地机型库。 */
export async function importModels(files: File[], serverId?: string): Promise<ModelImportResult> {
  const form = new FormData()
  for (const f of files) form.append('file', f)
  let response: Response
  try {
    response = await fetch(apiUrl('/models/import', serverId), { method: 'POST', headers: authHeaders('/models/import', serverId), body: form })
  } catch {
    throw new ApiError('上传失败：连不上管家程序', 0)
  }
  const data = await response.json().catch(() => ({})) as Partial<ModelImportResult> & { error?: string }
  if (!response.ok) throw new ApiError([data.error ?? `导入失败（${response.status}）`, ...(data.output ?? [])].join('\n'), response.status)
  return { output: data.output ?? [], builtin: data.builtin ?? 0, local: data.local ?? 0 }
}

/** 批量结果：每台一条。 */
export interface BatchResult { id: string; ok: boolean; error?: string; path?: string }

/**
 * 批量装 App / 批量传文件：文件只从电脑上传一次，服务器再分发到每台。
 * 图片、视频放进相册（DCIM），其他文件放下载目录。
 */
export async function batchUpload(kind: 'apk' | 'file', ids: string[], file: File, onProgress?: (percent: number) => void): Promise<BatchResult[]> {
  const dir = /^(image|video)\//.test(file.type) ? 'DCIM' : 'Download'
  // 每台服务器传一次（选中的手机分在几台服务器上就传几次）
  const bySid = new Map<string, string[]>()
  for (const id of ids) bySid.set(serverOfPhone(id), [...(bySid.get(serverOfPhone(id)) ?? []), id])
  const out: BatchResult[] = []
  let done = 0
  for (const [sid, group] of bySid) {
    const path = kind === 'apk' ? `/batch/apk?ids=${group.join(',')}` : `/batch/files?ids=${group.join(',')}&dir=${dir}`
    const data = (await upload(path, file, (pct) => onProgress?.(Math.round((done * 100 + pct) / bySid.size)), sid)) as unknown as { results?: BatchResult[] }
    out.push(...(data.results ?? []))
    done++
  }
  return out
}

/** 安卓日志（纯文本）。 */
export async function logs(id: string, level: string, lines: number): Promise<string> {
  const path = `/phones/${id}/logs?level=${level}&lines=${lines}`
  const response = await fetch(apiUrl(path), { headers: authHeaders(path) })
  const text = await response.text()
  if (!response.ok) {
    let message = `读日志失败（${response.status}）`
    try { message = (JSON.parse(text) as { error?: string }).error ?? message } catch { /* 非 JSON */ }
    throw new ApiError(message, response.status)
  }
  return text
}

/** 截屏地址（img 标签用；token 走 cookie，所以登录时也写一份 cookie）。不带 maxWidth = 原图 PNG。 */
export function screenUrl(id: string, tick: number, maxWidth?: number): string {
  return `/api/phones/${id}/screen?t=${tick}${maxWidth ? `&w=${maxWidth}` : ''}`
}

/**
 * 取一张缩小的截图（JPEG，比原图 PNG 小十倍左右，国内直连也能很快出来）。
 * 返回图片的 blob 地址和手机真实分辨率（响应头 X-Screen-Width/Height；旧版管家程序没有这两个头，返回 undefined）。
 */
export async function screenshot(id: string, tick: number, maxWidth: number, signal?: AbortSignal): Promise<{ url: string; device?: { w: number; h: number } }> {
  const path = `/phones/${id}/screen?t=${tick}&w=${maxWidth}`
  const response = await fetch(apiUrl(path), { headers: authHeaders(path), signal })
  if (!response.ok) throw new ApiError(`截屏失败（${response.status}）`, response.status)
  const w = Number(response.headers.get('X-Screen-Width'))
  const h = Number(response.headers.get('X-Screen-Height'))
  const url = URL.createObjectURL(await response.blob())
  return { url, device: w > 0 && h > 0 ? { w, h } : undefined }
}

/** 新窗口打开原图截图（别的服务器要带口令，所以先取回来再打开）。 */
export async function openRawScreenshot(id: string): Promise<void> {
  const win = window.open('', '_blank')
  const path = `/phones/${id}/screen?t=${Date.now()}`
  const response = await fetch(apiUrl(path), { headers: authHeaders(path) })
  if (!response.ok) { win?.close(); throw new ApiError(`截屏失败（${response.status}）`, response.status) }
  const url = URL.createObjectURL(await response.blob())
  if (win) win.location.href = url
}

/** 读手机剪贴板。 */
export function getClipboard(id: string): Promise<{ text: string }> {
  return call('GET', `/phones/${id}/clipboard`)
}

/** 把文字放进手机剪贴板；paste=true 时顺便粘贴到手机当前输入框。 */
export function setClipboard(id: string, text: string, paste = false): Promise<{ ok: boolean }> {
  return call('POST', `/phones/${id}/clipboard`, { text, paste })
}

/** 授权电脑的 ADB 公钥（adbkey.pub 的内容），之后 adb connect 不再 unauthorized。 */
export function authorizeAdbKey(id: string, key: string): Promise<{ ok: boolean }> {
  return call('POST', `/phones/${id}/adb-key`, { key })
}

export function writeTokenCookie(token: string): void {
  document.cookie = `pp_token=${encodeURIComponent(token)}; path=/; SameSite=Strict`
}
