import { localeForCountry } from './catalog'
import type { DeviceModel, Host, Phone, Proxy } from './types'

export type IssueLevel = 'error' | 'warning' | 'info'

export interface Issue {
  level: IssueLevel
  /** 稳定的标识，测试和界面都用它。 */
  code: string
  message: string
  /** 涉及的字段，界面里标红/标黄。 */
  fields?: string[]
}

export interface CheckInput {
  phone: Phone
  model?: DeviceModel
  proxy?: Proxy
  host?: Host
  /** 其他设备，用来查重复的 ID。 */
  others?: Phone[]
}

/** error 级别的问题会禁止开机。 */
export function blocksStart(issues: Issue[]): boolean {
  return issues.some((issue) => issue.level === 'error')
}

/**
 * 一致性检查：把竞品露馅的那些矛盾组合都提前查出来。
 * 规则越多越好，但每条都要说清楚「为什么是问题」。
 */
export function checkPhone({ phone, model, proxy, host, others = [] }: CheckInput): Issue[] {
  const issues: Issue[] = []

  // 1. 代理：缺失或不可用 → 禁止开机，绝不走真实网络
  if (!proxy) {
    issues.push({ level: 'error', code: 'proxy-missing', message: '没有分配代理。没有代理的设备禁止开机，避免暴露机房真实 IP。' })
  } else if (proxy.check && !proxy.check.ok) {
    issues.push({ level: 'error', code: 'proxy-down', message: `代理「${proxy.name}」检测不通（${proxy.check.error ?? '连接失败'}），禁止开机。` })
  } else if (!proxy.check) {
    issues.push({ level: 'warning', code: 'proxy-unchecked', message: `代理「${proxy.name}」还没检测过，开机前会先检测。` })
  }
  if (proxy?.check?.ok && proxy.check.udp === false) {
    issues.push({ level: 'warning', code: 'proxy-no-udp', message: '代理不支持 UDP：会屏蔽 QUIC，App 自动改走 TCP（不会泄露，但少数功能可能变慢）。' })
  }

  // 2. 机型
  if (!model) {
    issues.push({ level: 'error', code: 'model-missing', message: '没有选择机型。' })
  } else {
    if (host?.gpuFamily === 'software') {
      issues.push({ level: 'info', code: 'dev-host', message: '这台机器没有显卡（软件渲染），只用于开发测试，不要登录真实账号。' })
    } else if (host && model.gpuFamily !== host.gpuFamily && model.tier === 'disguised' && model.gpu?.renderer) {
      // 伪装档：显卡名由转接库照真机回答，Vulkan / OpenCL / 渲染结果还是板子的（2026-11-24）
      issues.push({ level: 'warning', code: 'gpu-disguised', fields: ['model', 'gpuRenderer'], message: `伪装档：显卡名对外报 ${model.gpu.renderer}（实际是 ${host.gpuFamily}）。Vulkan、OpenCL、渲染结果、跑分还是板子的，深度检测会露馅。` })
    } else if (host && model.gpuFamily !== host.gpuFamily) {
      issues.push({ level: 'error', code: 'gpu-mismatch', fields: ['model', 'gpuRenderer'], message: `机型「${String(model.values.marketName ?? model.id)}」的显卡是 ${model.gpuFamily}，这台机器是 ${host.gpuFamily}。显卡名来自真实驱动改不了，会直接露馅。` })
    }
    if (!model.androidVersions.includes(phone.androidVersion)) {
      issues.push({ level: 'warning', code: 'android-version', fields: ['androidVersion'], message: `这款机型官方系统是 Android ${model.androidVersions.join('/')}，没有 Android ${phone.androidVersion}。` })
    }
    // 机型库里没有这个安卓版本的真机指纹（也没手动填 / 导入指纹）：系统指纹只能用镜像的编译号拼，带 eng.* 工程版字样（2026-11-23）
    const manualFp = phone.fields.fingerprint
    const hasManualFp = manualFp?.mode === 'manual' && String(manualFp.value ?? '').trim() !== ''
    if (model.androidVersions.includes(phone.androidVersion) && model.fingerprints && !model.fingerprints[String(phone.androidVersion)] && !hasManualFp) {
      const have = Object.keys(model.fingerprints).sort().join('/')
      issues.push({ level: 'warning', code: 'no-real-fingerprint', fields: ['androidVersion'], message: `机型库里还没有这款机型 Android ${phone.androidVersion} 的真机指纹，系统指纹会带工程版字样（eng.*）。建议用 Android ${have}，或在指纹一栏导入真机的。` })
    }
    // CPU（踩坑 #147）：和板子不是同一代的指令集改不了；没有真机 CPU 目录数据的，/sys/devices/system/cpu 还是板子的
    if (model.cpuMismatch) {
      issues.push({ level: 'warning', code: 'cpu-mismatch', fields: ['model'], message: `CPU 对不上：${model.cpuMismatch}` })
    } else if (!model.cpuTree && host?.gpuFamily !== 'software') {
      issues.push({ level: 'info', code: 'no-cpu-tree', fields: ['model'], message: '机型库里还没有这款机型的真机 CPU 目录数据，App 查 /sys/devices/system/cpu 看到的频率表、调频驱动还是板子的。' })
    }
    if (!model.verified) {
      issues.push({ level: 'info', code: 'model-unverified', message: '这个机型档案还没用真机/固件核实，部分数值可能不准。' })
    }
    const country = proxy?.check?.country
    if (country && model.market === 'CN' && country !== 'CN') {
      issues.push({ level: 'info', code: 'market', fields: ['model'], message: `这是国行型号，配 ${country} 的 IP 不算常见，可以换成同款的海外型号。` })
    }
  }

  // 3. 地区：时区、语言要和代理 IP 一致
  const check = proxy?.check
  const tz = phone.fields.timezone
  if (check?.ok && check.timezone && tz?.mode === 'manual' && tz.value && tz.value !== check.timezone) {
    issues.push({ level: 'warning', code: 'timezone', fields: ['timezone'], message: `时区 ${String(tz.value)} 和代理 IP 所在地（${check.timezone}）不一致。` })
  }
  const locale = phone.fields.locale
  const expected = localeForCountry(check?.country)
  if (check?.ok && expected && locale?.mode === 'manual' && typeof locale.value === 'string') {
    const region = locale.value.split('-')[1]
    if (region && check.country && region.toUpperCase() !== check.country.toUpperCase()) {
      issues.push({ level: 'warning', code: 'locale', fields: ['locale'], message: `语言 ${locale.value} 和代理国家 ${check.country} 不一致（建议 ${expected}）。` })
    }
  }

  // 4. 唯一标识不能和别的设备重复
  for (const key of ['androidId', 'serial', 'wifiMac', 'bluetoothMac', 'gaid']) {
    const value = phone.fields[key]?.value
    if (!value) continue
    const twin = others.find((other) => other.id !== phone.id && other.fields[key]?.value === value)
    if (twin) issues.push({ level: 'error', code: `duplicate-${key}`, fields: [key], message: `和设备「${twin.name}」的 ${key} 重复，会被判定为同一台手机。` })
  }

  // 5. 还没做的功能
  const later = Object.entries(phone.fields).filter(([, setting]) => setting.mode === 'manual' && setting.value !== undefined).map(([key]) => key)
    .filter((key) => ['sensors', 'wifiSsid', 'wifiRssi', 'simOperator', 'simCountry'].includes(key))
  if (later.length > 0) issues.push({ level: 'info', code: 'later', fields: later, message: '传感器、WiFi、SIM 卡这几项后期才做，现在改了暂不生效。' })

  return issues
}
