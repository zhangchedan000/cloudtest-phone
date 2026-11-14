import fieldsJson from '@profiles/fields.json'
import type { DeviceModel, FieldCatalog, FieldDef, Host, Phone, Proxy } from './types'

/** 字段清单只有一份（profiles/fields.json），这里只是读出来。 */
export const catalog = fieldsJson as unknown as FieldCatalog

const modelFiles = import.meta.glob('@profiles/models/*.json', { eager: true, import: 'default' })
export const models: DeviceModel[] = Object.values(modelFiles as Record<string, DeviceModel>).sort((a, b) => a.id.localeCompare(b.id))

export function fieldsOfGroup(group: string): FieldDef[] {
  return catalog.fields.filter((field) => field.group === group)
}

export function fieldByKey(key: string): FieldDef | undefined {
  return catalog.fields.find((field) => field.key === key)
}

/** 国家 → 默认语言（跟随代理时用）。没列出的国家用英语（美国）。 */
const COUNTRY_LOCALE: Record<string, string> = {
  US: 'en-US', GB: 'en-GB', CA: 'en-CA', AU: 'en-AU', SG: 'en-SG', MY: 'ms-MY', PH: 'en-PH',
  TH: 'th-TH', VN: 'vi-VN', ID: 'id-ID', JP: 'ja-JP', KR: 'ko-KR', DE: 'de-DE', FR: 'fr-FR',
  ES: 'es-ES', IT: 'it-IT', MX: 'es-MX', BR: 'pt-BR', HK: 'zh-HK', TW: 'zh-TW', CN: 'zh-CN'
}

export function localeForCountry(country?: string): string | undefined {
  if (!country) return undefined
  return COUNTRY_LOCALE[country.toUpperCase()] ?? 'en-US'
}

export interface ResolveContext { phone: Phone; model?: DeviceModel; proxy?: Proxy; host?: Host }

/** 算出一个字段在这台设备上「实际生效」的值。 */
export function resolveField(field: FieldDef, ctx: ResolveContext): unknown {
  const setting = ctx.phone.fields[field.key] ?? { mode: field.modes[0] }
  switch (setting.mode) {
    case 'model':
      return ctx.model?.values[field.key]
    case 'proxy': {
      const check = ctx.proxy?.check
      if (!check?.ok) return undefined
      if (field.key === 'timezone') return check.timezone
      if (field.key === 'locale') return localeForCountry(check.country)
      if (field.key === 'simCountry') return check.country
      if (field.key === 'gps') return check.city ? `${check.city}（跟随 IP）` : undefined
      return undefined
    }
    case 'image':
      return field.key === 'androidVersion' ? ctx.phone.androidVersion : undefined
    case 'board':
      return field.key === 'gpuRenderer' ? ctx.host?.gpu : undefined
    case 'none':
      return undefined
    default:
      return setting.value
  }
}

export function formatValue(value: unknown, field: FieldDef): string {
  if (value === undefined || value === null || value === '') return '—'
  if (field.type === 'size' && typeof value === 'object') {
    const size = value as { w: number; h: number }
    return `${size.w} × ${size.h}`
  }
  if (field.type === 'cpu-layout' && Array.isArray(value)) {
    return (value as { part: string; count: number }[]).map((item) => `${item.count}×${item.part.replace('Cortex-', '')}`).join(' + ')
  }
  if (field.type === 'freq-list' && Array.isArray(value)) {
    const unique = [...new Set(value as number[])].map((khz) => `${(khz / 1_000_000).toFixed(2)}GHz`)
    return unique.join(' / ')
  }
  if (Array.isArray(value)) return value.join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  return `${String(value)}${field.unit ? ` ${field.unit}` : ''}`
}
