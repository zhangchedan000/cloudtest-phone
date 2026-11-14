/** 管理页和管家程序共用的数据形状（以后由管家程序接口返回）。 */

export type FieldMode = 'manual' | 'random' | 'model' | 'proxy' | 'image' | 'board' | 'none'
export type FieldApply = 'live' | 'restart' | 'image' | 'board'

export interface FieldDef {
  key: string
  group: string
  label: string
  type: string
  modes: FieldMode[]
  apply: FieldApply
  readonly?: boolean
  note?: string
  unit?: string
  min?: number
  max?: number
  options?: string[]
  pattern?: string
  length?: number
  stage?: 'later'
  sink: { kind: string; names?: string[] }
}

export interface FieldCatalog {
  version: number
  groups: { key: string; label: string }[]
  fields: FieldDef[]
  modes: Record<FieldMode, string>
  apply: Record<FieldApply, string>
}

export interface CpuPart { part: string; midr: string; count: number }

/** 机型库里的一个真实机型。 */
export interface DeviceModel {
  id: string
  schema: number
  verified: boolean
  verifyNote?: string
  gpuFamily: string
  market: string
  values: Record<string, unknown>
  androidVersions: number[]
}

export type PhoneStatus = 'running' | 'stopped' | 'starting' | 'error'

/** 一个字段在某台设备上的取值：用什么模式、手动值是什么。 */
export interface FieldSetting { mode: FieldMode; value?: unknown; locked?: boolean }

/** 一台云手机。只存引用（代理、分组、机型、机器）+ 改过的字段。 */
export interface Phone {
  id: string
  name: string
  hostId: string
  status: PhoneStatus
  androidVersion: number
  modelId: string
  proxyId: string
  groupId: string
  note?: string
  createdAt: string
  lastActiveAt?: string
  fields: Record<string, FieldSetting>
}

export type ProxyProtocol = 'socks5' | 'http' | 'https' | 'ss' | 'vmess' | 'vless' | 'trojan' | 'hysteria2' | 'subscription'

export interface ProxyCheck {
  ok: boolean
  exitIp?: string
  country?: string
  region?: string
  city?: string
  timezone?: string
  latencyMs?: number
  udp?: boolean
  checkedAt: string
  error?: string
}

export interface Proxy {
  id: string
  name: string
  protocol: ProxyProtocol
  address: string
  note?: string
  check?: ProxyCheck
}

export interface Group { id: string; name: string; color: string }

export interface Host {
  id: string
  name: string
  location: string
  arch: string
  gpu: string
  gpuFamily: string
  cpus: number
  memGb: number
  memUsedGb: number
  diskGb: number
  diskUsedGb: number
  online: boolean
  kernel: string
  maxRunning: number
}
