import type { Group, Host, Phone, Proxy } from './types'

/** 界面预览用的假数据。接上管家程序后由接口返回，这个文件只留给测试用。 */

export const hosts: Host[] = [
  { id: 'ashburn', name: '阿什本开发机', location: '美国 · 阿什本', arch: 'arm64', gpu: '无（软件渲染）', gpuFamily: 'software', cpus: 4, memGb: 23, memUsedGb: 5.6, diskGb: 184, diskUsedGb: 14, online: true, kernel: '6.17.0-1018-oracle', maxRunning: 4 },
  { id: 'rk3588-01', name: 'RK3588 盒子 1', location: '本地机房', arch: 'arm64', gpu: 'ARM Mali-G610', gpuFamily: 'Mali-G610', cpus: 8, memGb: 16, memUsedGb: 0, diskGb: 512, diskUsedGb: 0, online: false, kernel: '—（待采购）', maxRunning: 5 }
]

export const groups: Group[] = [
  { id: 'g-shop-a', name: '店铺 A 引流', color: '#2f6bff' },
  { id: 'g-shop-b', name: '店铺 B 引流', color: '#0f9d8a' },
  { id: 'g-test', name: '测试', color: '#e8590c' }
]

const now = '2026-11-14T17:30:00Z'

export const proxies: Proxy[] = [
  { id: 'p-us-1', name: '美国住宅 · 达拉斯', protocol: 'socks5', address: 'socks5://***@res-us-1:1080', check: { ok: true, exitIp: '72.14.xx.21', country: 'US', region: 'Texas', city: 'Dallas', timezone: 'America/Chicago', latencyMs: 182, udp: true, checkedAt: now } },
  { id: 'p-us-2', name: '美国住宅 · 纽约', protocol: 'vless', address: 'vless://***@node-ny:443', check: { ok: true, exitIp: '98.116.xx.7', country: 'US', region: 'New York', city: 'New York', timezone: 'America/New_York', latencyMs: 214, udp: false, checkedAt: now } },
  { id: 'p-us-3', name: '美国住宅 · 洛杉矶', protocol: 'http', address: 'http://***@res-la:8080', check: { ok: false, checkedAt: now, error: '认证失败（407）' } },
  { id: 'p-sub-1', name: '订阅 · Clash 美国节点组', protocol: 'subscription', address: 'https://sub.***/clash', note: '18 个节点' }
]

export const phones: Phone[] = [
  {
    id: 'ph-001', name: 'TK-A01', hostId: 'ashburn', status: 'running', androidVersion: 14, modelId: 'poco-x4-gt', proxyId: 'p-us-1', groupId: 'g-shop-a', createdAt: now, lastActiveAt: now,
    fields: {
      androidId: { mode: 'random', value: '9f3c1a7e52b04d18' }, serial: { mode: 'random', value: 'R7XK29QF' },
      wifiMac: { mode: 'random', value: '7c:2a:31:9e:04:5b' }, bluetoothMac: { mode: 'random', value: '7c:2a:31:9e:04:5c' },
      deviceName: { mode: 'random', value: 'POCO X4 GT' }, batteryLevel: { mode: 'random', value: 83 }, batteryCharging: { mode: 'manual', value: 'none' },
      timezone: { mode: 'proxy' }, locale: { mode: 'proxy' }, gps: { mode: 'proxy' }, kernelRelease: { mode: 'random', value: '5.10.198-android12-9-gc2f7e1a40b6d' }
    }
  },
  {
    id: 'ph-002', name: 'TK-A02', hostId: 'ashburn', status: 'stopped', androidVersion: 14, modelId: 'redmi-k50', proxyId: 'p-us-2', groupId: 'g-shop-a', createdAt: now,
    fields: {
      androidId: { mode: 'random', value: '1b8e44c0d9a27f63' }, serial: { mode: 'random', value: 'K2HW81PZ' },
      timezone: { mode: 'manual', value: 'Asia/Shanghai' }, locale: { mode: 'manual', value: 'zh-CN' }, batteryLevel: { mode: 'random', value: 56 }
    }
  },
  {
    id: 'ph-003', name: 'TK-B01', hostId: 'rk3588-01', status: 'stopped', androidVersion: 14, modelId: 'xiaomi-13t', proxyId: 'p-us-3', groupId: 'g-shop-b', createdAt: now,
    fields: { androidId: { mode: 'random', value: '9f3c1a7e52b04d18' }, timezone: { mode: 'proxy' }, locale: { mode: 'proxy' } }
  },
  {
    id: 'ph-004', name: '测试机', hostId: 'ashburn', status: 'starting', androidVersion: 14, modelId: 'poco-x4-gt', proxyId: 'p-us-1', groupId: 'g-test', createdAt: now,
    fields: { timezone: { mode: 'proxy' }, locale: { mode: 'proxy' } }
  }
]
