import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'
import { models } from './catalog'
import { checkPhone, type Issue } from './consistency'
import * as mock from './mock'
import type { DeviceModel, FieldSetting, Group, Host, Phone, PhoneStatus, Proxy } from './types'

/**
 * 所有页面共用的数据。现在是内存里的假数据；接上管家程序后，这里改成调接口，页面不用动。
 * 规矩（同 Prism）：代理库、分组库、机型库各一份，设备只存引用。
 */
interface Workspace {
  phones: Phone[]
  proxies: Proxy[]
  groups: Group[]
  hosts: Host[]
  models: DeviceModel[]
  modelOf: (phone: Phone) => DeviceModel | undefined
  proxyOf: (phone: Phone) => Proxy | undefined
  hostOf: (phone: Phone) => Host | undefined
  groupOf: (phone: Phone) => Group | undefined
  issuesOf: (phone: Phone) => Issue[]
  setStatus: (ids: string[], status: PhoneStatus) => void
  saveFields: (id: string, fields: Record<string, FieldSetting>, patch?: Partial<Phone>) => void
  assign: (ids: string[], patch: { proxyId?: string; groupId?: string }) => void
  addPhone: (phone: Phone) => void
  removePhones: (ids: string[]) => void
  addProxy: (proxy: Proxy) => void
}

const WorkspaceContext = createContext<Workspace | null>(null)

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const [phones, setPhones] = useState<Phone[]>(mock.phones)
  const [proxies, setProxies] = useState<Proxy[]>(mock.proxies)
  const [groups] = useState<Group[]>(mock.groups)
  const [hosts] = useState<Host[]>(mock.hosts)

  const modelOf = useCallback((phone: Phone) => models.find((m) => m.id === phone.modelId), [])
  const proxyOf = useCallback((phone: Phone) => proxies.find((p) => p.id === phone.proxyId), [proxies])
  const hostOf = useCallback((phone: Phone) => hosts.find((h) => h.id === phone.hostId), [hosts])
  const groupOf = useCallback((phone: Phone) => groups.find((g) => g.id === phone.groupId), [groups])
  const issuesOf = useCallback(
    (phone: Phone) => checkPhone({ phone, model: modelOf(phone), proxy: proxyOf(phone), host: hostOf(phone), others: phones }),
    [phones, modelOf, proxyOf, hostOf]
  )

  const value = useMemo<Workspace>(() => ({
    phones, proxies, groups, hosts, models, modelOf, proxyOf, hostOf, groupOf, issuesOf,
    setStatus: (ids, status) => setPhones((list) => list.map((p) => (ids.includes(p.id) ? { ...p, status } : p))),
    saveFields: (id, fields, patch) => setPhones((list) => list.map((p) => (p.id === id ? { ...p, ...patch, fields } : p))),
    assign: (ids, patch) => setPhones((list) => list.map((p) => (ids.includes(p.id) ? { ...p, ...patch } : p))),
    addPhone: (phone) => setPhones((list) => [...list, phone]),
    removePhones: (ids) => setPhones((list) => list.filter((p) => !ids.includes(p.id))),
    addProxy: (proxy) => setProxies((list) => [...list, proxy])
  }), [phones, proxies, groups, hosts, modelOf, proxyOf, hostOf, groupOf, issuesOf])

  return <WorkspaceContext.Provider value={value}>{children}</WorkspaceContext.Provider>
}

export function useWorkspace(): Workspace {
  const value = useContext(WorkspaceContext)
  if (!value) throw new Error('useWorkspace 必须在 WorkspaceProvider 里用')
  return value
}
