import { describe, expect, it } from 'vitest'
import { models } from './catalog'
import { blocksStart, checkPhone } from './consistency'
import { hosts, phones, proxies } from './mock'
import type { Phone } from './types'

const model = (id: string) => models.find((m) => m.id === id)
const proxy = (id: string) => proxies.find((p) => p.id === id)
const host = (id: string) => hosts.find((h) => h.id === id)
const codes = (phone: Phone, overrides: Partial<Parameters<typeof checkPhone>[0]> = {}) =>
  checkPhone({ phone, model: model(phone.modelId), proxy: proxy(phone.proxyId), host: host(phone.hostId), others: phones, ...overrides }).map((i) => i.code)

describe('一致性检查', () => {
  it('没有代理禁止开机', () => {
    const issues = checkPhone({ phone: phones[0], model: model('poco-x4-gt'), proxy: undefined, host: host('rk3588-01') })
    expect(issues.map((i) => i.code)).toContain('proxy-missing')
    expect(blocksStart(issues)).toBe(true)
  })

  it('代理检测不通禁止开机', () => {
    expect(codes(phones[2])).toContain('proxy-down')
  })

  it('机型显卡和机器显卡对不上要报错', () => {
    const fake = { ...model('poco-x4-gt')!, gpuFamily: 'Adreno 830' }
    const issues = checkPhone({ phone: phones[0], model: fake, proxy: proxy('p-us-1'), host: host('rk3588-01') })
    expect(issues.find((i) => i.code === 'gpu-mismatch')?.level).toBe('error')
  })

  it('机型库没有这个安卓版本的真机指纹要提醒，有的不提醒', () => {
    const base = { ...phones[0], modelId: 'xiaomi-13t' }
    const run = (androidVersion: number, fields = base.fields) =>
      checkPhone({ phone: { ...base, androidVersion, fields }, model: model('xiaomi-13t'), proxy: proxy('p-us-1'), host: host('rk3588-01') }).map((i) => i.code)
    expect(run(16)).toContain('no-real-fingerprint')
    expect(run(15)).not.toContain('no-real-fingerprint')
    expect(run(16, { ...base.fields, fingerprint: { mode: 'manual', value: 'Xiaomi/aristotle_global/aristotle:16/X/Y:user/release-keys' } })).not.toContain('no-real-fingerprint')
  })

  it('CPU 不是同一代的机型要警告，没有 CPU 目录数据的给提示', () => {
    const run = (modelId: string) =>
      checkPhone({ phone: { ...phones[0], modelId }, model: model(modelId), proxy: proxy('p-us-1'), host: host('rk3588-01') }).map((i) => i.code)
    expect(run('nothing-phone-2a')).toContain('cpu-mismatch')
    expect(run('xiaomi-13t')).not.toContain('cpu-mismatch')
  })

  it('Mali-G610 机型放在 RK3588 上不报显卡问题', () => {
    expect(codes(phones[0], { host: host('rk3588-01') })).not.toContain('gpu-mismatch')
  })

  it('开发机（软件渲染）只提示，不报显卡错误', () => {
    const list = codes(phones[0])
    expect(list).toContain('dev-host')
    expect(list).not.toContain('gpu-mismatch')
  })

  it('手动时区/语言和代理国家不一致要警告', () => {
    const list = codes(phones[1])
    expect(list).toContain('timezone')
    expect(list).toContain('locale')
  })

  it('跟随代理时不报时区问题', () => {
    expect(codes(phones[0])).not.toContain('timezone')
  })

  it('国行机型配美国 IP 给提示', () => {
    // 机型库里已经没有国行机型了（2026-11-15 删了 Redmi K50），这里造一个国行的测提示逻辑
    const cn = { ...model('xiaomi-12t')!, market: 'CN' }
    expect(codes(phones[1], { model: cn })).toContain('market')
    expect(codes(phones[1])).not.toContain('market')
  })

  it('代理不支持 UDP 给警告', () => {
    expect(codes(phones[1])).toContain('proxy-no-udp')
  })

  it('Android ID 重复要报错', () => {
    expect(codes(phones[2])).toContain('duplicate-androidId')
  })

  it('健康的设备不会被禁止开机', () => {
    const issues = checkPhone({ phone: phones[0], model: model('poco-x4-gt'), proxy: proxy('p-us-1'), host: host('rk3588-01'), others: phones.filter((p) => p.id !== 'ph-003') })
    expect(blocksStart(issues)).toBe(false)
  })
})
