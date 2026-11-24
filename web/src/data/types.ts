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
  /** 按安卓版本（"14"/"15"/"16"）存的真机系统指纹；没有的版本开机时只能用镜像的编译号拼，带 eng.* 字样。 */
  fingerprints?: Record<string, string>
  /** 用哪份真机 CPU 目录模板（profiles/cpu/<名>.json）；没有的机型 /sys/devices/system/cpu 还是板子的。 */
  cpuTree?: string
  /** CPU 和板子不是同一代（指令集伪装不了）时的说明。 */
  cpuMismatch?: string
  /** consistent：真机显卡就是板子的（干活一面天然一致）；disguised：显卡不同，靠伪装。留空当 consistent。 */
  tier?: 'consistent' | 'disguised'
  /** 来自本地机型库（导入的），不是管家程序内置的。 */
  local?: boolean
  /** 导入来源：ours（我们采集的真机包）/ mytos（魔云腾机型包）/ dir */
  source?: string
  imeiTacs?: string[]
  /** 真机显卡字符串（伪装档用）。 */
  gpu?: { vendor: string; renderer: string; version: string; extensions?: string; egl?: string }
  /** 真机 ro.hardware（qcom、mt6989……） */
  hardware?: string
  /** 真机系统特性列表（pm list features，「名字」或「名字=版本」） */
  features?: string[]
  /** 真机 /data 总字节数 */
  dataBytes?: number
}

export type PhoneStatus = 'running' | 'stopped' | 'starting' | 'error'

/** 一个字段在某台设备上的取值：用什么模式、手动值是什么。 */
export interface FieldSetting { mode: FieldMode; value?: unknown; locked?: boolean }

/** 一台云手机。只存引用（代理、分组、机型、机器）+ 改过的字段。 */
export interface Phone {
  /** 在哪台服务器上（多台服务器时；本机 = 'local' 或没有）。 */
  serverId?: string
  id: string
  name: string
  hostId: string
  status: PhoneStatus
  androidVersion: number
  modelId: string
  proxyId: string
  groupId: string
  note?: string
  /** 带谷歌服务（GMS / Play 商店）：开机用谷歌服务镜像，需要机器装了对应镜像。 */
  gapps?: boolean
  adb?: boolean
  createdAt: string
  lastActiveAt?: string
  fields: Record<string, FieldSetting>
  /** 管家程序返回：开机后设置（时区、语言）的结果。 */
  settings?: { state: 'pending' | 'applied' | 'failed'; applied?: string[]; error?: string }
  /** 这台挂的所有代理（可以随时切换）；proxyId 是当前在用的那个。 */
  proxyIds?: string[]
  /** 管家程序返回：容器网段里的 IP（服务器内部地址，ADB 经 SSH 隧道连它）。 */
  ip?: string
  /** 管家程序返回：网络模块开着时，这台设备为什么没有出口（没有就是正常）。 */
  network?: string
  /** 管家程序返回：最近一次采到的 CPU（占整台机器的百分比）、内存（MB）。 */
  usage?: { cpuPercent: number; memMB: number; memLimitMB: number }
  /** 管家程序返回：最近一次上网检查（每 2 分钟从手机里试连外网）。 */
  net?: { ok: boolean; error?: string; at: string }
  /** 管家程序返回：正在做 / 最近做完的备份或恢复（后台做，几十秒到几分钟）。 */
  dataJob?: { kind: 'backup' | 'restore'; state: 'running' | 'done' | 'failed'; error?: string; backupId?: string; at: string }
}

export type ProxyProtocol = 'socks5' | 'http' | 'https' | 'ss' | 'vmess' | 'vless' | 'trojan' | 'hysteria2' | 'subscription' | 'clash'

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
  /** 在哪台服务器上（多台服务器时；本机 = 'local' 或没有）。 */
  serverId?: string
  id: string
  name: string
  protocol: ProxyProtocol
  address: string
  note?: string
  check?: ProxyCheck
}

export interface Group { id: string; name: string; color: string; serverId?: string }

export interface Host {
  /** 在哪台服务器上（多台服务器时；本机 = 'local' 或没有）。 */
  serverId?: string
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
  /** 网络模块（mihomo）开没开；没开时云手机直接用服务器网络，只适合开发测试。 */
  networkEnabled?: boolean
  /** 网络模块的宿主机健康检查（策略路由、mihomo）；ok=false 时云手机可能都上不了网。 */
  netHealth?: { ok: boolean; problems?: string[]; healedAt?: string; checkedAt: string }
  /** 这台机器带正面小屏（魔云腾 Q1 盒子带，服务器一般没有）。 */
  oled?: boolean
  /** 小屏当前点亮（false = 熄屏）。 */
  oledOn?: boolean
  /** 别的服务器：网页地址；连不上时的原因。 */
  url?: string
  error?: string
  /** 这台机器装了哪些安卓版本的镜像。 */
  androidVersions?: number[]
  /** 这台机器装了哪些安卓版本的「谷歌服务」镜像（带 GMS / Play 商店）；空 = 不支持谷歌服务。 */
  gappsVersions?: number[]
}
