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
}

export interface InputRequest { kind: 'tap' | 'swipe' | 'text' | 'key'; x?: number; y?: number; x2?: number; y2?: number; durationMs?: number; text?: string; key?: string }

export const api = {
  host: () => call<HostInfo>('GET', '/host'),
  phones: () => call<PhoneView[]>('GET', '/phones'),
  createPhone: (req: { name: string; modelId: string; androidVersion: number; proxyId: string; groupId: string; note?: string }) => call<Phone>('POST', '/phones', req),
  savePhone: (id: string, req: { fields?: Record<string, FieldSetting>; modelId?: string; proxyId?: string; groupId?: string; name?: string; note?: string }) => call('PUT', `/phones/${id}`, req),
  deletePhone: (id: string) => call('DELETE', `/phones/${id}`),
  start: (id: string) => call('POST', `/phones/${id}/start`),
  stop: (id: string) => call('POST', `/phones/${id}/stop`),
  input: (id: string, req: InputRequest) => call('POST', `/phones/${id}/input`, req),
  proxies: () => call<Proxy[]>('GET', '/proxies'),
  createProxy: (proxy: Omit<Proxy, 'id'>) => call<Proxy>('POST', '/proxies', proxy),
  checkProxy: (id: string) => call<ProxyCheck>('POST', `/proxies/${id}/check`),
  deleteProxy: (id: string) => call('DELETE', `/proxies/${id}`),
  groups: () => call<Group[]>('GET', '/groups'),
  createGroup: (group: Omit<Group, 'id'>) => call<Group>('POST', '/groups', group)
}

/** 截屏地址（img 标签用；token 走 cookie，所以登录时也写一份 cookie）。 */
export function screenUrl(id: string, tick: number): string {
  return `/api/phones/${id}/screen?t=${tick}`
}

export function writeTokenCookie(token: string): void {
  document.cookie = `pp_token=${encodeURIComponent(token)}; path=/; SameSite=Strict`
}
