import { LockOutlined, ReloadOutlined, UnlockOutlined } from '@ant-design/icons'
import { Alert, Button, Collapse, Input, InputNumber, Select, Space, Tag, Tooltip } from 'antd'
import { useMemo, useState } from 'react'
import { catalog, fieldsOfGroup, formatValue, resolveField } from '@/data/catalog'
import { checkPhone } from '@/data/consistency'
import { randomValue } from '@/data/random'
import { useWorkspace } from '@/data/store'
import type { FieldDef, FieldMode, FieldSetting, Phone } from '@/data/types'

const APPLY_COLOR: Record<string, string> = { live: 'green', restart: 'blue', image: 'default', board: 'default' }

/** 参数面板：每个字段都能看到实际值，也都能改（字段清单来自 profiles/fields.json）。 */
export function ParamPanel({ phone, onSaved }: { phone: Phone; onSaved?: (restartNeeded: boolean) => void }) {
  const ws = useWorkspace()
  const [draft, setDraft] = useState<Record<string, FieldSetting>>(phone.fields)
  const [modelId, setModelId] = useState(phone.modelId)
  const draftPhone: Phone = { ...phone, fields: draft, modelId }
  const model = ws.models.find((m) => m.id === modelId)
  const ctx = { phone: draftPhone, model, proxy: ws.proxyOf(phone), host: ws.hostOf(phone) }
  const issues = checkPhone({ ...ctx, others: ws.phones })
  const flagged = new Map<string, 'error' | 'warning'>()
  for (const issue of issues) for (const key of issue.fields ?? []) if (issue.level !== 'info' && flagged.get(key) !== 'error') flagged.set(key, issue.level as 'error' | 'warning')

  const changed = useMemo(() => {
    const keys = new Set([...Object.keys(draft), ...Object.keys(phone.fields)])
    return [...keys].filter((key) => JSON.stringify(draft[key]) !== JSON.stringify(phone.fields[key]))
  }, [draft, phone.fields])
  const restartNeeded = modelId !== phone.modelId || changed.some((key) => catalog.fields.find((f) => f.key === key)?.apply === 'restart')

  const setField = (key: string, next: FieldSetting) => setDraft((d) => ({ ...d, [key]: next }))
  const settingOf = (field: FieldDef): FieldSetting => draft[field.key] ?? { mode: field.modes[0] }

  const randomizeAll = () => {
    setDraft((d) => {
      const next = { ...d }
      for (const field of catalog.fields) {
        const s = next[field.key] ?? { mode: field.modes[0] }
        if (s.mode === 'random' && !s.locked) next[field.key] = { ...s, value: randomValue(field, model) }
      }
      return next
    })
  }

  return (
    <div className="pp-params">
      <div className="pp-params-head">
        <Space wrap>
          <span className="pp-muted">机型</span>
          <Select
            style={{ width: 260 }}
            value={modelId}
            onChange={setModelId}
            options={ws.models.map((m) => ({ value: m.id, label: `${String(m.values.marketName)}（${String(m.values.model)}）${m.verified ? '' : ' · 待核实'}` }))}
          />
          <Tooltip title="所有「随机」模式、没锁定的字段重新生成一次（相当于一键新机，不改机型）"><Button icon={<ReloadOutlined />} onClick={randomizeAll}>随机字段全部重新生成</Button></Tooltip>
        </Space>
      </div>

      {issues.filter((i) => i.level !== 'info').map((issue) => (
        <Alert key={issue.code} type={issue.level === 'error' ? 'error' : 'warning'} showIcon title={issue.message} className="pp-alert" />
      ))}
      {issues.filter((i) => i.level === 'info').map((issue) => (
        <Alert key={issue.code} type="info" showIcon title={issue.message} className="pp-alert" />
      ))}

      <Collapse
        defaultActiveKey={catalog.groups.map((g) => g.key)}
        ghost
        items={catalog.groups.map((group) => ({
          key: group.key,
          label: <b>{group.label}</b>,
          children: (
            <div className="pp-field-table">
              {fieldsOfGroup(group.key).map((field) => {
                const setting = settingOf(field)
                const value = resolveField(field, { ...ctx, phone: { ...draftPhone, fields: { ...draft, [field.key]: setting } } })
                const flag = flagged.get(field.key) ?? (field.key === 'model' ? flagged.get('model') : undefined)
                return (
                  <div key={field.key} className={`pp-field-row ${flag ? `pp-flag-${flag}` : ''} ${changed.includes(field.key) ? 'pp-changed' : ''}`}>
                    <div className="pp-field-label">
                      {field.label}
                      {field.note && <Tooltip title={field.note}><span className="pp-help">?</span></Tooltip>}
                    </div>
                    <div className="pp-field-value">
                      <FieldEditor field={field} setting={setting} display={formatValue(value, field)} onChange={(v) => setField(field.key, { ...setting, value: v })} />
                    </div>
                    <div className="pp-field-mode">
                      {field.modes.length > 1 && !field.readonly ? (
                        <Select
                          size="small"
                          style={{ width: 112 }}
                          value={setting.mode}
                          onChange={(mode: FieldMode) => {
                            const seeded = mode === 'random' ? randomValue(field, model) : mode === 'manual' ? (setting.value ?? (value as unknown)) : setting.value
                            setField(field.key, { ...setting, mode, value: seeded })
                          }}
                          options={field.modes.map((m) => ({ value: m, label: catalog.modes[m] }))}
                        />
                      ) : <span className="pp-muted pp-small">{catalog.modes[setting.mode]}</span>}
                      {setting.mode === 'random' && !setting.locked && (
                        <Tooltip title="重新随机"><Button size="small" type="text" icon={<ReloadOutlined />} onClick={() => setField(field.key, { ...setting, value: randomValue(field, model) })} /></Tooltip>
                      )}
                      {setting.mode === 'random' && (
                        <Tooltip title={setting.locked ? '已锁定：一键新机时不变' : '锁定：一键新机时不变'}>
                          <Button size="small" type="text" icon={setting.locked ? <LockOutlined /> : <UnlockOutlined />} onClick={() => setField(field.key, { ...setting, locked: !setting.locked })} />
                        </Tooltip>
                      )}
                    </div>
                    <div className="pp-field-apply">
                      <Tag color={APPLY_COLOR[field.apply]}>{catalog.apply[field.apply]}</Tag>
                      {field.stage === 'later' && <Tag color="orange">后期</Tag>}
                    </div>
                  </div>
                )
              })}
            </div>
          )
        }))}
      />

      <div className="pp-params-foot">
        <span className="pp-muted">{changed.length || modelId !== phone.modelId ? `改了 ${changed.length + (modelId !== phone.modelId ? 1 : 0)} 项${restartNeeded ? '，部分要重启这台安卓才生效' : ''}` : '没有改动'}</span>
        <Space>
          <Button onClick={() => { setDraft(phone.fields); setModelId(phone.modelId) }} disabled={!changed.length && modelId === phone.modelId}>撤销</Button>
          <Button type="primary" disabled={!changed.length && modelId === phone.modelId} onClick={() => { ws.saveFields(phone.id, draft, { modelId }); onSaved?.(restartNeeded) }}>
            {restartNeeded && phone.status === 'running' ? '保存并重启' : '保存'}
          </Button>
        </Space>
      </div>
    </div>
  )
}

function FieldEditor({ field, setting, display, onChange }: { field: FieldDef; setting: FieldSetting; display: string; onChange: (value: unknown) => void }) {
  if (setting.mode !== 'manual' || field.readonly) return <span className={display === '—' ? 'pp-muted' : 'pp-mono'}>{display}</span>
  const value = setting.value
  if (field.type === 'int' || field.type === 'float') {
    return <InputNumber size="small" style={{ width: 160 }} min={field.min} max={field.max} step={field.type === 'float' ? 0.1 : 1} value={typeof value === 'number' ? value : undefined} onChange={(v) => onChange(v ?? undefined)} addonAfter={field.unit} />
  }
  if (field.type === 'enum' && field.options) {
    return <Select size="small" style={{ width: 160 }} value={value as string} onChange={onChange} options={field.options.map((o) => ({ value: o, label: o }))} />
  }
  if (typeof value === 'object' && value !== null) {
    return <Input.TextArea size="small" autoSize value={JSON.stringify(value)} onChange={(e) => { try { onChange(JSON.parse(e.target.value)) } catch { /* 等用户输完 */ } }} />
  }
  return <Input size="small" value={value === undefined ? '' : String(value)} onChange={(e) => onChange(e.target.value)} placeholder={field.pattern ? `格式：${field.pattern}` : undefined} />
}
