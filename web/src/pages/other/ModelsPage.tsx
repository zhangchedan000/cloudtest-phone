import { Card, Descriptions, Tag } from 'antd'
import { catalog, fieldByKey, formatValue } from '@/data/catalog'
import { useWorkspace } from '@/data/store'

const SHOWN = ['model', 'device', 'socModel', 'cpuParts', 'cpuMaxFreq', 'memTotal', 'resolution', 'refreshRate']

/** 机型库：真实机型的参数档案（profiles/models/*.json）。显卡要和机器对得上才能用。 */
export function ModelsPage() {
  const ws = useWorkspace()
  const gpus = new Set(ws.hosts.filter((h) => h.gpuFamily !== 'software').map((h) => h.gpuFamily))
  return (
    <div className="pp-page">
      <div className="pp-toolbar"><span className="pp-muted">机型档案来自真机或固件备份。「待核实」的数值还没有用真机确认。字段共 {catalog.fields.length} 项，全部可以在设备上单独修改。</span></div>
      <div className="pp-grid pp-grid-wide">
        {ws.models.map((m) => (
          <Card key={m.id} size="small" title={String(m.values.marketName)} extra={m.verified ? <Tag color="green">已核实</Tag> : <Tag color="orange">待核实</Tag>}>
            <div className="pp-card-row" style={{ marginBottom: 8 }}>
              <Tag color={gpus.has(m.gpuFamily) ? 'blue' : 'default'}>{m.gpuFamily}</Tag>
              <Tag>{m.market === 'CN' ? '国行' : '海外版'}</Tag>
              <Tag>Android {m.androidVersions.join(' / ')}</Tag>
              <span className="pp-muted pp-small">{ws.phones.filter((p) => p.modelId === m.id).length} 台在用</span>
            </div>
            <Descriptions column={1} size="small" items={SHOWN.map((key) => {
              const field = fieldByKey(key)!
              return { label: field.label, children: <span className="pp-mono pp-small">{formatValue(m.values[key], field)}</span> }
            })} />
          </Card>
        ))}
      </div>
    </div>
  )
}
