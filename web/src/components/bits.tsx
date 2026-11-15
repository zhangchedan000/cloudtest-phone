import { Badge, Tag, Tooltip } from 'antd'
import type { Issue } from '@/data/consistency'
import type { PhoneStatus, Proxy } from '@/data/types'

const STATUS: Record<PhoneStatus, { text: string; color: string }> = {
  running: { text: '运行中', color: 'var(--fb-success-dot)' },
  stopped: { text: '已关机', color: 'var(--fb-idle)' },
  starting: { text: '开机中', color: 'var(--fb-attention)' },
  error: { text: '异常', color: 'var(--fb-danger)' }
}

export function StatusDot({ status }: { status: PhoneStatus }) {
  const s = STATUS[status]
  return <span className="pp-status"><i style={{ background: s.color }} />{s.text}</span>
}

const FLAG: Record<string, string> = { US: '🇺🇸', GB: '🇬🇧', SG: '🇸🇬', CA: '🇨🇦', DE: '🇩🇪', JP: '🇯🇵', HK: '🇭🇰', CN: '🇨🇳' }

export function ProxyCell({ proxy }: { proxy?: Proxy }) {
  if (!proxy) return <span className="pp-danger-text">未分配代理</span>
  const check = proxy.check
  if (!check) return <span className="pp-muted">{proxy.name} · 未检测</span>
  if (!check.ok) return <Tooltip title={check.error}><span className="pp-danger-text">{proxy.name} · 不通</span></Tooltip>
  // 直连（仅开发）等没有城市 / 地区的，显示代理名，免得只剩一个逗号
  const place = [check.city, check.region].filter(Boolean).join('，') || proxy.name
  return (
    <Tooltip title={[proxy.name, check.exitIp, check.timezone, check.latencyMs != null ? `${check.latencyMs}ms` : ''].filter(Boolean).join('｜')}>
      <span>{FLAG[check.country ?? ''] ?? '🌐'} {place}</span>
    </Tooltip>
  )
}

export function IssueBadge({ issues }: { issues: Issue[] }) {
  const errors = issues.filter((i) => i.level === 'error').length
  const warnings = issues.filter((i) => i.level === 'warning').length
  if (!errors && !warnings) return <Tag color="success" variant="filled">检查通过</Tag>
  return (
    <Tooltip title={issues.filter((i) => i.level !== 'info').map((i) => i.message).join('\n')}>
      <span className="pp-issues">
        {errors > 0 && <Badge color="var(--fb-danger)" text={`${errors} 个错误`} />}
        {warnings > 0 && <Badge color="var(--fb-warning-strong)" text={`${warnings} 个提醒`} />}
      </span>
    </Tooltip>
  )
}
