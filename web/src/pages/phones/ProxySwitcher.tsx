import { App, Select, Tag } from 'antd'
import { useState } from 'react'
import { api } from '@/data/api'
import { useWorkspace } from '@/data/store'
import type { Phone, Proxy } from '@/data/types'

/**
 * 一台手机挂多个代理、随时切换（2026-11-15 用户要求「一台手机可以用多个 IP」）。
 * - ProxySwitcher：手机窗口上的下拉框，选一个就切换（开着的手机时区马上跟着换）。
 * - ProxyListEditor：「编辑 → 信息」里管理这台挂哪些代理。
 */

function proxyLabel(px: Proxy | undefined, id: string): string {
  if (!px) return id
  const c = px.check
  if (!c) return `${px.name} · 未检测`
  if (!c.ok) return `${px.name} · 不通`
  const place = [c.city, c.country].filter(Boolean).join('，')
  return place ? `${px.name} · ${place}` : px.name
}

/** 和这台手机在同一台服务器上的代理（每台服务器有自己的代理库）。 */
export function serverProxies(all: Proxy[], phone: Phone): Proxy[] {
  return all.filter((p) => (p.serverId ?? 'local') === (phone.serverId ?? 'local'))
}

export function phoneProxyIds(phone: Phone): string[] {
  const list = phone.proxyIds?.length ? phone.proxyIds : phone.proxyId ? [phone.proxyId] : []
  return list.includes(phone.proxyId) || !phone.proxyId ? list : [phone.proxyId, ...list]
}

export function ProxySwitcher({ phone }: { phone: Phone }) {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [busy, setBusy] = useState(false)
  const ids = phoneProxyIds(phone)
  const switchTo = async (proxyId: string) => {
    if (proxyId === phone.proxyId) return
    setBusy(true)
    try {
      const res = await api.switchProxy(phone.id, proxyId)
      if (res.warning) message.warning(`已切换，但：${res.warning}`)
      else message.success(res.timezone ? `已切换，时区换成 ${res.timezone}（正在用的 App 可能要重新连一下）` : '已切换')
      await ws.refresh()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Select
      className="pp-proxy-switch"
      size="small"
      value={phone.proxyId}
      loading={busy}
      disabled={ws.mode !== 'live' || busy}
      onChange={(v) => void switchTo(v)}
      options={ids.map((id) => ({ value: id, label: proxyLabel(serverProxies(ws.proxies, phone).find((p) => p.id === id), id) }))}
      popupMatchSelectWidth={false}
      prefix={<Tag color="blue" bordered={false}>IP</Tag>}
      title={ids.length > 1 ? '这台挂了多个代理，选一个就切换' : '只挂了一个代理：在「编辑 → 信息」里可以再加'}
    />
  )
}

export function ProxyListEditor({ phone }: { phone: Phone }) {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [busy, setBusy] = useState(false)
  const save = async (list: string[]) => {
    if (!list.length) { message.warning('至少要挂一个代理'); return }
    setBusy(true)
    try {
      await api.savePhone(phone.id, { proxyIds: list })
      await ws.refresh()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Select
      mode="multiple"
      style={{ width: '100%' }}
      value={phoneProxyIds(phone)}
      loading={busy}
      disabled={ws.mode !== 'live' || busy}
      onChange={(v) => void save(v)}
      options={serverProxies(ws.proxies, phone).map((p) => ({ value: p.id, label: `${proxyLabel(p, p.id)}${p.id === phone.proxyId ? '（在用）' : ''}` }))}
      placeholder="选这台可以用的代理（可以多选）"
    />
  )
}
