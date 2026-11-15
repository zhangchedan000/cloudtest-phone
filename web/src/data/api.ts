import type { FieldSetting, Group, Phone, Proxy, ProxyCheck } from './types'

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

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const token = savedToken()
  const response = await fetch(`/api${path}`, {
    method,
    headers: { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body)
  })
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
  system?: Record<string, unknown>
  network?: boolean
  netHealth?: { ok: boolean; problems?: string[]; healedAt?: string; checkedAt: string }
}

export interface TrashItem { name: string; phoneId: string; kind: 'delete' | 'reset'; at: string; sizeMB: number }

export interface InputRequest { kind: 'tap' | 'swipe' | 'text' | 'key'; x?: number; y?: number; x2?: number; y2?: number; durationMs?: number; text?: string; key?: string }

export const api = {
  host: () => call<HostInfo>('GET', '/host'),
  phones: () => call<PhoneView[]>('GET', '/phones'),
  createPhone: (req: { name: string; modelId: string; androidVersion: number; proxyId: string; groupId: string; note?: string }) => call<Phone>('POST', '/phones', req),
  savePhone: (id: string, req: { fields?: Record<string, FieldSetting>; modelId?: string; proxyId?: string; groupId?: string; name?: string; note?: string }) => call('PUT', `/phones/${id}`, req),
  deletePhone: (id: string) => call('DELETE', `/phones/${id}`),
  start: (id: string) => call('POST', `/phones/${id}/start`),
  stop: (id: string) => call('POST', `/phones/${id}/stop`),
  reset: (id: string, androidVersion?: number) => call('POST', `/phones/${id}/reset`, { renew: false, androidVersion: androidVersion ?? 0 }),
  input: (id: string, req: InputRequest) => call('POST', `/phones/${id}/input`, req),
  proxies: () => call<Proxy[]>('GET', '/proxies'),
  createProxy: (proxy: Omit<Proxy, 'id'>) => call<Proxy>('POST', '/proxies', proxy),
  checkProxy: (id: string) => call<ProxyCheck>('POST', `/proxies/${id}/check`),
  deleteProxy: (id: string) => call('DELETE', `/proxies/${id}`),
  importProxies: (req: { url?: string; text?: string; note?: string }) => call<{ added: number; duplicates: number; skipped?: string[] }>('POST', '/proxies/import', req),
  trash: () => call<TrashItem[]>('GET', '/trash'),
  deleteTrash: (name: string) => call('DELETE', `/trash/${encodeURIComponent(name)}`),
  groups: () => call<Group[]>('GET', '/groups'),
  createGroup: (group: Omit<Group, 'id'>) => call<Group>('POST', '/groups', group),
  updateGroup: (group: Group) => call<Group>('PUT', `/groups/${group.id}`, group),
  deleteGroup: (id: string) => call('DELETE', `/groups/${id}`)
}

/** 上传文件（安装 APK / 传到手机目录），带进度回调。 */
export function upload(path: string, file: File, onProgress?: (percent: number) => void): Promise<{ ok: boolean; path?: string; output?: string }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `/api${path}`)
    const token = savedToken()
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
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

/** 安卓日志（纯文本）。 */
export async function logs(id: string, level: string, lines: number): Promise<string> {
  const token = savedToken()
  const response = await fetch(`/api/phones/${id}/logs?level=${level}&lines=${lines}`, { headers: token ? { Authorization: `Bearer ${token}` } : {} })
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
  const token = savedToken()
  const response = await fetch(screenUrl(id, tick, maxWidth), { headers: token ? { Authorization: `Bearer ${token}` } : {}, signal })
  if (!response.ok) throw new ApiError(`截屏失败（${response.status}）`, response.status)
  const w = Number(response.headers.get('X-Screen-Width'))
  const h = Number(response.headers.get('X-Screen-Height'))
  const url = URL.createObjectURL(await response.blob())
  return { url, device: w > 0 && h > 0 ? { w, h } : undefined }
}

/** 读手机剪贴板。 */
export function getClipboard(id: string): Promise<{ text: string }> {
  return call('GET', `/phones/${id}/clipboard`)
}

/** 把文字放进手机剪贴板；paste=true 时顺便粘贴到手机当前输入框。 */
export function setClipboard(id: string, text: string, paste = false): Promise<{ ok: boolean }> {
  return call('POST', `/phones/${id}/clipboard`, { text, paste })
}

export function writeTokenCookie(token: string): void {
  document.cookie = `pp_token=${encodeURIComponent(token)}; path=/; SameSite=Strict`
}
