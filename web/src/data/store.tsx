import { App } from 'antd'
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, ApiError, LOCAL, rememberPhoneServer, saveToken, savedToken, setClipboard, setRemoteServers, writeTokenCookie, type HostInfo, type InputRequest, type ServerRef } from './api'
import { scaleInput, screenOf } from './groupControl'
import { models } from './catalog'
import { checkPhone, type Issue } from './consistency'
import * as mock from './mock'
import type { DeviceModel, FieldSetting, Group, Host, Phone, PhoneStatus, Proxy } from './types'

/**
 * 所有页面共用的数据。
 * - live：管理页由管家程序提供，数据都走接口（/api/…），操作真的作用在云手机上。
 * - demo：直接用 vite 打开（没有管家程序）时，用内存里的假数据，方便看界面。
 * - login：管家程序设置了 token，要先在页面上填。
 * 规矩（同 Prism）：代理库、分组库、机型库各一份，设备只存引用。
 */
export type WorkspaceMode = 'loading' | 'live' | 'demo' | 'login'

interface Workspace {
  mode: WorkspaceMode
  phones: Phone[]
  proxies: Proxy[]
  groups: Group[]
  hosts: Host[]
  /** 别的服务器（本机之外）。 */
  servers: ServerRef[]
  /** 服务器显示名（本机叫这台机器的名字）。 */
  serverName: (serverId?: string) => string
  models: DeviceModel[]
  modelOf: (phone: Phone) => DeviceModel | undefined
  proxyOf: (phone: Phone) => Proxy | undefined
  hostOf: (phone: Phone) => Host | undefined
  groupOf: (phone: Phone) => Group | undefined
  issuesOf: (phone: Phone) => Issue[]
  setStatus: (ids: string[], status: PhoneStatus) => Promise<void>
  saveFields: (id: string, fields: Record<string, FieldSetting>, patch?: Partial<Phone>) => Promise<void>
  assign: (ids: string[], patch: { proxyId?: string; groupId?: string; gapps?: boolean; adb?: boolean }) => Promise<void>
  addPhone: (phone: Phone) => Promise<void>
  removePhones: (ids: string[]) => Promise<void>
  resetPhones: (ids: string[], androidVersion?: number) => Promise<void>
  renewPhones: (ids: string[]) => Promise<void>
  addProxy: (proxy: Proxy) => Promise<void>
  /** 检测代理：同时最多 6 个，每测完一个马上更新那一行；onProgress(已完成, 总数) */
  checkProxies: (ids: string[], serverId?: string, onProgress?: (done: number, total: number) => void) => Promise<void>
  removeProxy: (id: string, serverId?: string) => Promise<void>
  saveGroup: (group: Omit<Group, 'id'> & { id?: string }) => Promise<void>
  removeGroup: (id: string, serverId?: string) => Promise<void>
  importProxies: (req: { url?: string; text?: string; note?: string }, serverId?: string) => Promise<{ added: number; duplicates: number; skipped?: string[] }>
  /** 返回领头这台的结果（旋转时有 landscape：转完是不是横的） */
  input: (id: string, req: InputRequest) => Promise<{ landscape?: boolean; wantLandscape?: boolean } | undefined>
  /** 粘贴文字到手机当前输入框（群控时其他手机一起粘贴）。 */
  paste: (id: string, text: string) => Promise<void>
  /** 群控：主控手机 → 跟着做的手机。 */
  followers: Record<string, string[]>
  setFollowers: (id: string, ids: string[]) => void
  login: (token: string) => Promise<void>
  refresh: () => Promise<void>
}

const WorkspaceContext = createContext<Workspace | null>(null)

const DEV_DIRECT: Proxy = { id: 'direct-dev', name: '直连（仅开发机）', protocol: 'http', address: '不经代理，仅用于开发测试', check: { ok: true, checkedAt: '' } }

/** 管家程序的机器信息 → 页面用的机器卡片（硬件数字来自自检，没有就显示 0）。 */
function hostFromInfo(info: HostInfo, serverId: string = LOCAL, url?: string): Host {
  const sys = (info.system ?? {}) as Record<string, unknown>
  const num = (v: unknown) => (typeof v === 'number' ? Math.round(v * 10) / 10 : 0)
  return {
    serverId, url, id: info.id, name: info.name || info.id, location: info.devHost ? '开发机' : '—', arch: String(sys.arch ?? 'arm64'),
    gpu: info.gpuFamily === 'software' ? '无（软件渲染）' : info.gpuFamily, gpuFamily: info.gpuFamily,
    cpus: num(sys.cpus), memGb: num(sys.memGb), memUsedGb: num(num(sys.memGb) - num(sys.memAvailGb)), diskGb: num(sys.diskGb), diskUsedGb: num(num(sys.diskGb) - num(sys.diskFreeGb)),
    online: true, kernel: String(sys.kernel ?? '—'), maxRunning: info.maxRunning, networkEnabled: info.network === true, netHealth: info.netHealth, androidVersions: [...(info.androidVersions ?? [])].sort((a, b) => a - b),
    gappsVersions: [...(info.gappsVersions ?? [])].sort((a, b) => a - b), oled: info.oled, oledOn: info.oledOn
  }
}

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const { message } = App.useApp()
  const [mode, setMode] = useState<WorkspaceMode>('loading')
  const [phones, setPhones] = useState<Phone[]>([])
  const [proxies, setProxies] = useState<Proxy[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [hosts, setHosts] = useState<Host[]>([])
  const [servers, setServers] = useState<ServerRef[]>([])
  const [followers, setFollowersState] = useState<Record<string, string[]>>({})

  const load = useCallback(async () => {
    try {
      const [host, list, proxyList, groupList, serverList] = await Promise.all([
        api.host(), api.phones(), api.proxies(), api.groups(), api.servers().catch(() => [] as ServerRef[])
      ])
      setRemoteServers(serverList ?? [])
      setServers(serverList ?? [])
      // 别的服务器一起读；连不上的显示成离线，不影响其他的
      const remote = await Promise.all((serverList ?? []).map(async (sv) => {
        try {
          const [h, ps, px, gs] = await Promise.all([api.host(sv.id), api.phones(sv.id), api.proxies(sv.id), api.groups(sv.id)])
          return { sv, h, ps: ps ?? [], px: px ?? [], gs: gs ?? [] }
        } catch (error) {
          return { sv, error: (error as Error).message }
        }
      }))
      const allHosts: Host[] = [hostFromInfo(host)]
      const allPhones: Phone[] = (list ?? []).map((p) => ({ ...p, serverId: LOCAL, fields: p.fields ?? {} }))
      // 开发机允许「直连（仅开发）」，管家程序认这个固定 ID；正式机器没有这一项。
      const allProxies: Proxy[] = [...(proxyList ?? []), ...(host.devHost ? [DEV_DIRECT] : [])].map((p) => ({ ...p, serverId: LOCAL }))
      const allGroups: Group[] = (groupList ?? []).map((g) => ({ ...g, serverId: LOCAL }))
      for (const r of remote) {
        if ('error' in r) {
          allHosts.push({ ...hostFromInfo({ id: `offline-${r.sv.id}`, name: r.sv.name, gpuFamily: '—', devHost: false, maxRunning: 0, androidVersions: [] }, r.sv.id, r.sv.url), online: false, error: r.error })
          continue
        }
        allHosts.push(hostFromInfo(r.h, r.sv.id, r.sv.url))
        allPhones.push(...r.ps.map((p) => ({ ...p, serverId: r.sv.id, fields: p.fields ?? {} })))
        allProxies.push(...[...r.px, ...(r.h.devHost ? [DEV_DIRECT] : [])].map((p) => ({ ...p, serverId: r.sv.id })))
        allGroups.push(...r.gs.map((g) => ({ ...g, serverId: r.sv.id })))
      }
      for (const p of allPhones) rememberPhoneServer(p.id, p.serverId ?? LOCAL)
      setHosts(allHosts)
      setPhones(allPhones)
      setProxies(allProxies)
      setGroups(allGroups)
      setMode('live')
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) { setMode('login'); return }
      if (error instanceof ApiError && error.status !== 404) throw error
      // 没有 /api：页面不是管家程序提供的，进预览模式。
      setPhones(mock.phones); setProxies(mock.proxies); setGroups(mock.groups); setHosts(mock.hosts)
      setMode('demo')
    }
  }, [])

  useEffect(() => {
    const token = savedToken()
    if (token) writeTokenCookie(token)
    void load().catch((error: Error) => message.error(error.message))
  }, [load, message])

  // 运行状态会变（开机完成、容器退出），live 模式下定时刷新。
  useEffect(() => {
    if (mode !== 'live') return
    const timer = setInterval(() => { void load().catch(() => undefined) }, 5000)
    return () => clearInterval(timer)
  }, [mode, load])

  const modelOf = useCallback((phone: Phone) => models.find((m) => m.id === phone.modelId), [])
  const same = (a?: string, b?: string) => (a ?? LOCAL) === (b ?? LOCAL)
  const proxyOf = useCallback((phone: Phone) => proxies.find((p) => p.id === phone.proxyId && same(p.serverId, phone.serverId)), [proxies])
  const hostOf = useCallback((phone: Phone) => hosts.find((h) => h.id === phone.hostId) ?? (mode === 'live' ? hosts[0] : undefined), [hosts, mode])
  const groupOf = useCallback((phone: Phone) => groups.find((g) => g.id === phone.groupId && same(g.serverId, phone.serverId)), [groups])
  const issuesOf = useCallback(
    (phone: Phone) => [
      ...checkPhone({ phone, model: modelOf(phone), proxy: proxyOf(phone), host: hostOf(phone), others: phones }),
      // 管家程序算出来的网络出口问题（网络模块开着才有）：没有出口禁止开机。
      ...(phone.network ? [{ level: 'error' as const, code: 'network', message: phone.network }] : [])
    ],
    [phones, modelOf, proxyOf, hostOf]
  )

  const value = useMemo<Workspace>(() => {
    const live = mode === 'live'
    /** 对每台设备做一次操作；失败的报出来，其余照常，最后刷新。 */
    const each = async (ids: string[], what: string, fn: (id: string) => Promise<unknown>, oneByOne = false) => {
      // 开机、重置一台一台来，不让服务器同时启动一堆安卓
      const results: PromiseSettledResult<unknown>[] = []
      if (oneByOne) {
        for (const id of ids) results.push(await fn(id).then((value) => ({ status: 'fulfilled' as const, value }), (reason: unknown) => ({ status: 'rejected' as const, reason })))
      } else {
        results.push(...await Promise.allSettled(ids.map(fn)))
      }
      const failed = results.flatMap((r, i) => (r.status === 'rejected' ? [`${phones.find((p) => p.id === ids[i])?.name ?? ''}${(r.reason as Error).message}`] : []))
      if (failed.length) message.error(`${what}失败：${failed.join('；')}`)
      await load().catch(() => undefined)
    }
    return {
      mode, phones, proxies, groups, hosts, servers, models, modelOf, proxyOf, hostOf, groupOf, issuesOf,
      serverName: (sid) => (!sid || sid === LOCAL ? hosts.find((h) => (h.serverId ?? LOCAL) === LOCAL)?.name ?? '本机' : servers.find((s) => s.id === sid)?.name ?? sid),
      setStatus: async (ids, status) => {
        if (!live) { setPhones((list) => list.map((p) => (ids.includes(p.id) ? { ...p, status } : p))); return }
        if (status === 'running') setPhones((list) => list.map((p) => (ids.includes(p.id) ? { ...p, status: 'starting' } : p)))
        await each(ids, status === 'running' ? '开机' : '关机', (id) => (status === 'running' ? api.start(id) : api.stop(id)), status === 'running')
      },
      saveFields: async (id, fields, patch) => {
        if (!live) { setPhones((list) => list.map((p) => (p.id === id ? { ...p, ...patch, fields } : p))); return }
        await each([id], '保存', (one) => api.savePhone(one, { fields, modelId: patch?.modelId, name: patch?.name, note: patch?.note }))
      },
      assign: async (ids, patch) => {
        if (!live) { setPhones((list) => list.map((p) => (ids.includes(p.id) ? { ...p, ...patch } : p))); return }
        await each(ids, '修改', (id) => api.savePhone(id, patch))
      },
      addPhone: async (phone) => {
        if (!live) { setPhones((list) => [...list, phone]); return }
        // 字段由管家程序按机型生成，页面只传基本信息。
        // 建在选中的机器所在的服务器上
        const sid = hosts.find((h) => h.id === phone.hostId)?.serverId ?? LOCAL
        await each([phone.id], '创建', () => api.createPhone({ name: phone.name, modelId: phone.modelId, androidVersion: phone.androidVersion, proxyId: phone.proxyId, groupId: phone.groupId, note: phone.note, gapps: phone.gapps }, sid))
      },
      resetPhones: async (ids, androidVersion) => {
        if (!live) { message.info('预览模式：接上管家程序后可用'); return }
        await each(ids, androidVersion ? '刷机' : '重置', (id) => api.reset(id, androidVersion), true)
      },
      renewPhones: async (ids) => {
        if (!live) { message.info('预览模式：接上管家程序后可用'); return }
        await each(ids, '一键新机', (id) => api.renew(id))
      },
      removePhones: async (ids) => {
        if (!live) { setPhones((list) => list.filter((p) => !ids.includes(p.id))); return }
        await each(ids, '删除', (id) => api.deletePhone(id))
      },
      addProxy: async (proxy) => {
        if (!live) { setProxies((list) => [...list, proxy]); return }
        await each([proxy.id], '添加代理', () => api.createProxy({ name: proxy.name, protocol: proxy.protocol, address: proxy.address, note: proxy.note }, proxy.serverId))
      },
      checkProxies: async (ids, sid, onProgress) => {
        if (!live) { message.info('预览模式：接上管家程序后才能检测'); return }
        // 以前一次全发出去、全部测完才刷新页面：一百多个节点要等好几分钟，期间一直显示「未检测」，像是测不通。
        // 现在同时最多 6 个，测完一个这一行马上显示结果（不通的显示原因）
        let next = 0, done = 0
        const failed: string[] = []
        const worker = async () => {
          while (next < ids.length) {
            const id = ids[next++]
            try {
              const check = await api.checkProxy(id, sid)
              setProxies((list) => list.map((p) => (p.id === id && (p.serverId ?? LOCAL) === (sid ?? p.serverId ?? LOCAL) ? { ...p, check } : p)))
            } catch (error) {
              failed.push(`${proxies.find((p) => p.id === id)?.name ?? id}：${(error as Error).message}`)
            }
            onProgress?.(++done, ids.length)
          }
        }
        await Promise.all(Array.from({ length: Math.min(6, ids.length) }, worker))
        if (failed.length) message.error(`检测出错：${failed.slice(0, 5).join('；')}${failed.length > 5 ? ` 等 ${failed.length} 个` : ''}`)
        await load().catch(() => undefined)
      },
      removeProxy: async (id, sid) => {
        if (!live) { setProxies((list) => list.filter((p) => p.id !== id)); return }
        await each([id], '删除代理', () => api.deleteProxy(id, sid))
      },
      importProxies: async (req, sid) => {
        if (!live) throw new Error('预览模式：接上管家程序后才能导入')
        const result = await api.importProxies(req, sid)
        await load().catch(() => undefined)
        return result
      },
      saveGroup: async (group) => {
        if (!live) {
          setGroups((list) => (group.id ? list.map((g) => (g.id === group.id ? { ...g, ...group } as Group : g)) : [...list, { ...group, id: `g-${Date.now().toString(36)}` }]))
          return
        }
        await each([group.id ?? 'new'], '保存分组', () => (group.id ? api.updateGroup(group as Group, group.serverId) : api.createGroup(group, group.serverId)))
      },
      removeGroup: async (id, sid) => {
        if (!live) {
          setGroups((list) => list.filter((g) => g.id !== id))
          setPhones((list) => list.map((p) => (p.groupId === id ? { ...p, groupId: '' } : p)))
          return
        }
        await each([id], '删除分组', () => api.deleteGroup(id, sid))
      },
      input: async (id, req) => {
        if (!live) return undefined
        const lead = phones.find((p) => p.id === id)
        const from = screenOf(lead && modelOf(lead)?.values)
        const others = (followers[id] ?? []).flatMap((fid) => {
          const f = phones.find((p) => p.id === fid && p.status === 'running')
          return f ? [api.input(fid, scaleInput(req, from, screenOf(modelOf(f)?.values))).then(() => true, () => false)] : []
        })
        let res: { landscape?: boolean; wantLandscape?: boolean } | undefined
        try { res = await api.input(id, req) } catch (error) { message.error((error as Error).message) }
        const ok = await Promise.all(others)
        if (ok.includes(false)) message.warning({ content: `群控：${ok.filter((x) => !x).length} 台没跟上`, key: 'pp-follow' })
        return res
      },
      paste: async (id, text) => {
        if (!live) return
        const others = (followers[id] ?? []).filter((fid) => phones.some((p) => p.id === fid && p.status === 'running'))
          .map((fid) => setClipboard(fid, text, true).then(() => true, () => false))
        try { await setClipboard(id, text, true) } catch (error) { message.error((error as Error).message) }
        const ok = await Promise.all(others)
        if (ok.includes(false)) message.warning({ content: `群控：${ok.filter((x) => !x).length} 台没跟上`, key: 'pp-follow' })
      },
      followers,
      setFollowers: (id, ids) => setFollowersState((m) => ({ ...m, [id]: ids.filter((x) => x !== id) })),
      login: async (token) => {
        saveToken(token)
        writeTokenCookie(token)
        await load()
      },
      refresh: load
    }
  }, [mode, phones, proxies, groups, hosts, servers, followers, modelOf, proxyOf, hostOf, groupOf, issuesOf, load, message])

  return <WorkspaceContext.Provider value={value}>{children}</WorkspaceContext.Provider>
}

export function useWorkspace(): Workspace {
  const value = useContext(WorkspaceContext)
  if (!value) throw new Error('useWorkspace 必须在 WorkspaceProvider 里用')
  return value
}
