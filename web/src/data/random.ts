import type { DeviceModel, FieldDef } from './types'

/** 「随机」模式的生成规则：都按真实手机的取值范围来，不出现明显假的值。 */

const hex = (n: number) => Array.from({ length: n }, () => Math.floor(Math.random() * 16).toString(16)).join('')
const pick = <T,>(list: readonly T[]): T => list[Math.floor(Math.random() * list.length)]
const int = (min: number, max: number) => min + Math.floor(Math.random() * (max - min + 1))

/** 安卓 10+ 的 WiFi MAC 默认按网络随机化：第一个字节是「本地管理」地址（第二位为 1，最低位为 0）。 */
function randomMac(): string {
  const first = (int(0, 63) << 2) | 0b10
  return [first, ...Array.from({ length: 5 }, () => int(0, 255))].map((b) => b.toString(16).padStart(2, '0')).join(':')
}

function randomSerial(): string {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZ0123456789'
  return Array.from({ length: pick([8, 10, 12]) }, () => pick(chars.split(''))).join('')
}

function randomUuid(): string {
  const h = hex(32)
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-4${h.slice(13, 16)}-${pick(['8', '9', 'a', 'b'])}${h.slice(17, 20)}-${h.slice(20, 32)}`
}

/** 内核版本号：跟机型出厂安卓版本对应的 GKI 格式，例如 5.10.198-android12-9-g<12位哈希>。 */
function randomKernel(model?: DeviceModel): string {
  const shipped = model?.androidVersions[0] ?? 12
  const base = shipped >= 14 ? `6.1.${int(57, 118)}-android14-11` : shipped >= 13 ? `5.15.${int(94, 153)}-android13-8` : `5.10.${int(160, 218)}-android12-9`
  return `${base}-g${hex(12)}`
}

export function randomValue(field: FieldDef, model?: DeviceModel): unknown {
  switch (field.key) {
    case 'androidId': return hex(16)
    case 'serial': return randomSerial()
    case 'wifiMac': return randomMac()
    case 'bluetoothMac': return randomMac()
    case 'gaid': return randomUuid()
    case 'deviceName': return String(model?.values.marketName ?? model?.values.model ?? 'Android')
    case 'batteryLevel': return int(23, 96)
    case 'batteryCharging': return pick(['none', 'none', 'none', 'ac', 'usb'])
    case 'batteryTemp': return Number((int(270, 355) / 10).toFixed(1))
    case 'wifiRssi': return int(-72, -48)
    case 'wifiSsid': return `${pick(['NETGEAR', 'Spectrum', 'ATT', 'xfinitywifi', 'TP-Link', 'MySpectrumWiFi'])}${pick(['', '-5G', '_' + hex(2).toUpperCase(), int(10, 99).toString()])}`
    case 'kernelRelease': return randomKernel(model)
    case 'kernelBuild': return `#1 SMP PREEMPT ${new Date(Date.now() - int(60, 400) * 86400_000).toUTCString().replace('GMT', 'UTC')}`
    default: return undefined
  }
}
