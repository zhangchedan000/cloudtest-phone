import { CloudDownloadOutlined, ReloadOutlined, SearchOutlined, PlusOutlined } from '@ant-design/icons'
import { App, Button, Form, Input, Modal, Select, Space, Tag, Tooltip } from 'antd'
import { useMemo, useState } from 'react'

import { LOCAL } from '@/data/api'
import { DataTable } from '@/components/DataTable'
import { onServer, ServerSelect } from '@/components/ServerSelect'
import { useWorkspace } from '@/data/store'
import type { Proxy } from '@/data/types'
import { AddProxyModal } from './AddProxyModal'

/** 代理库：每条代理只存一份，设备只引用。底层用 mihomo，协议都支持。 */
export function ProxiesPage() {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [sid, setSid] = useState(LOCAL)
  const [tab, setTab] = useState<'proxies' | 'subs'>('proxies')
  const [query, setQuery] = useState('')
  const [typeFilter, setTypeFilter] = useState<string>('all')
  const [countryFilter, setCountryFilter] = useState<string>('all')
  const [selected, setSelected] = useState<string[]>([])
  const [adding, setAdding] = useState(false)
  const [importing, setImporting] = useState(false)
  const [importBusy, setImportBusy] = useState(false)
  const [importForm] = Form.useForm<{ url?: string; text?: string; note?: string }>()
  const [checking, setChecking] = useState<string[]>([])
  const [progress, setProgress] = useState<{ done: number; total: number }>()

  const all = onServer(ws.proxies, sid).filter((p) => p.id !== 'direct-dev')
  const subs = all.filter((p) => p.protocol === 'subscription')
  const proxies = all.filter((p) => p.protocol !== 'subscription')
  const boundPhones = (id: string) => onServer(ws.phones, sid).filter((p) => p.proxyId === id)

  // 统计（只统计「代理」tab 里的）
  const ok = proxies.filter((p) => p.check?.ok).length
  const bad = proxies.filter((p) => p.check && !p.check.ok).length
  const unchecked = proxies.filter((p) => !p.check).length

  // 筛选下拉的可选项
  const types = [...new Set(proxies.map((p) => p.protocol))]
  const countries = [...new Set(proxies.map((p) => p.check?.country).filter(Boolean) as string[])]

  const shown = useMemo(() => proxies.filter((p) =>
    (typeFilter === 'all' || p.protocol === typeFilter) &&
    (countryFilter === 'all' || p.check?.country === countryFilter) &&
    (!query || `${p.name} ${p.address} ${p.check?.exitIp ?? ''} ${p.note ?? ''}`.toLowerCase().includes(query.toLowerCase()))
  ), [proxies, typeFilter, countryFilter, query])

  const doImport = async (v: { url?: string; text?: string; note?: string }) => {
    if (!v.url?.trim() && !v.text?.trim()) { message.warning('填订阅地址，或者粘贴订阅内容'); return }
    setImportBusy(true)
    try {
      const r = await ws.importProxies(v, sid)
      const extra = [r.duplicates ? `${r.duplicates} 个已存在跳过` : '', r.skipped?.length ? `${r.skipped.length} 个不支持跳过` : ''].filter(Boolean).join('，')
      message.success(`导入了 ${r.added} 个节点${extra ? `（${extra}）` : ''}`)
      setImporting(false); importForm.resetFields()
    } catch (error) { message.error((error as Error).message) } finally { setImportBusy(false) }
  }

  const check = async (ids: string[]) => {
    if (!ids.length) return
    setChecking(ids)
    if (ids.length > 1) setProgress({ done: 0, total: ids.length })
    try {
      await ws.checkProxies(ids, sid, (done, total) => { if (total > 1) setProgress({ done, total }) })
    } finally { setChecking([]); setProgress(undefined) }
  }

  const del = (p: Proxy) => {
    if (boundPhones(p.id).length > 0) { message.warning('还有设备在用这条代理，先给那些设备换代理'); return }
    Modal.confirm({ title: `删除代理「${p.name}」？`, okButtonProps: { danger: true }, onOk: () => ws.removeProxy(p.id, sid) })
  }

  const columns = [
    { key: 'type', title: '类型', width: 150, sortValue: (p: Proxy) => p.protocol, render: (_: unknown, p: Proxy) => (
      <div><Tag>{protoLabel(p)}</Tag><div className="pp-muted pp-small pp-ellip">{p.note || p.name}</div></div>
    ) },
    { key: 'addr', title: '代理地址', width: 220, sortValue: (p: Proxy) => proxyAddr(p), render: (_: unknown, p: Proxy) => <span className="pp-mono pp-small">{proxyAddr(p)}</span> },
    { key: 'exit', title: '出口 IP / 地区', width: 190, sortValue: (p: Proxy) => p.check?.exitIp ?? '', render: (_: unknown, p: Proxy) => (p.check?.ok ? (
      <div><div className="pp-small">{flag(p.check.country)} {p.check.exitIp}</div><div className="pp-muted pp-small pp-ellip">{[p.check.country, p.check.city].filter(Boolean).join(' · ')}</div></div>
    ) : <span className="pp-muted">—</span>) },
    { key: 'latency', title: '延迟', width: 90, sortValue: (p: Proxy) => p.check?.latencyMs ?? 1e9, render: (_: unknown, p: Proxy) => (p.check?.latencyMs ? `${p.check.latencyMs} ms` : <span className="pp-muted">—</span>) },
    { key: 'status', title: '状态', width: 100, sortValue: (p: Proxy) => (!p.check ? -1 : p.check.ok ? 1 : 0), render: (_: unknown, p: Proxy) => statusTag(p) },
    { key: 'bound', title: '绑定窗口', width: 150, sortValue: (p: Proxy) => boundPhones(p.id).length, render: (_: unknown, p: Proxy) => {
      const names = boundPhones(p.id).map((x) => x.name)
      return names.length ? <Tooltip title={names.join('、')}><span className="pp-ellip pp-small">{names.length === 1 ? names[0] : `${names.length} 台`}</span></Tooltip> : <span className="pp-muted">未绑定</span>
    } },
    { key: 'checked', title: '最后检测', width: 120, sortValue: (p: Proxy) => p.check?.checkedAt ?? '', render: (_: unknown, p: Proxy) => (p.check?.checkedAt ? <span className="pp-small">{shortTime(p.check.checkedAt)}</span> : <span className="pp-muted">—</span>) },
    { key: 'actions', title: '操作', width: 150, resizable: false, reorderable: false, render: (_: unknown, p: Proxy) => (
      <Space size={4}>
        <Button size="small" type="link" loading={!progress && checking.includes(p.id)} onClick={() => void check([p.id])}>检测</Button>
        <Button size="small" type="link" danger onClick={() => del(p)}>删除</Button>
      </Space>
    ) },
  ]

  return (
    <div className="pp-page">
      <div className="pp-proxy-tabs">
        <button className={`pp-proxy-tab ${tab === 'proxies' ? 'pp-proxy-tab-active' : ''}`} onClick={() => setTab('proxies')}>代理 {proxies.length}</button>
        <button className={`pp-proxy-tab ${tab === 'subs' ? 'pp-proxy-tab-active' : ''}`} onClick={() => setTab('subs')}>订阅 {subs.length}</button>
      </div>

      <div className="pp-toolbar">
        <Space wrap>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>添加代理</Button>
          <Button icon={<CloudDownloadOutlined />} onClick={() => setImporting(true)}>批量导入</Button>
          <Button icon={<ReloadOutlined />} loading={!!progress} onClick={() => void check(proxies.map((p) => p.id))}>{progress ? `检测中 ${progress.done}/${progress.total}` : '全部检测'}</Button>
          <Button disabled={!selected.length || !!progress} onClick={() => void check(selected)}>检测所选{selected.length ? `（${selected.length}）` : ''}</Button>
        </Space>
        <Space wrap>
          <ServerSelect value={sid} onChange={setSid} />
          <Select style={{ width: 120 }} value={typeFilter} onChange={setTypeFilter} options={[{ value: 'all', label: '全部类型' }, ...types.map((t) => ({ value: t, label: t.toUpperCase() }))]} />
          <Select style={{ width: 130 }} value={countryFilter} onChange={setCountryFilter} options={[{ value: 'all', label: '全部国家' }, ...countries.map((c) => ({ value: c, label: `${flag(c)} ${c}` }))]} />
          <Input allowClear prefix={<SearchOutlined />} placeholder="搜索 IP / 备注" value={query} onChange={(e) => setQuery(e.target.value)} style={{ width: 180 }} />
        </Space>
      </div>

      {tab === 'proxies' ? (
        <>
          <div className="pp-stats">
            <Stat label="代理总数" value={proxies.length} />
            <Stat label="可用" value={ok} tone="success" />
            <Stat label="失效" value={bad} tone={bad ? 'danger' : undefined} />
            <Stat label="未检测" value={unchecked} />
          </div>
          <DataTable<Proxy>
            tableId="proxies"
            rowKey="id"
            dataSource={shown}
            pagination={false}
            scroll={{ x: 1150 }}
            rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys as string[]) }}
            columns={columns}
          />
        </>
      ) : (
        <DataTable<Proxy>
          tableId="proxy-subs"
          rowKey="id"
          dataSource={subs}
          pagination={false}
          locale={{ emptyText: '还没有订阅。点「批量导入」添加 Clash 订阅链接。' }}
          columns={[
            { key: 'name', title: '名称', dataIndex: 'name', width: 200 },
            { key: 'addr', title: '订阅地址', width: 360, render: (_: unknown, p: Proxy) => <span className="pp-mono pp-small pp-ellip">{p.address}</span> },
            { key: 'actions', title: '操作', width: 100, resizable: false, reorderable: false, render: (_: unknown, p: Proxy) => <Button size="small" type="link" danger onClick={() => del(p)}>删除</Button> },
          ]}
        />
      )}

      <AddProxyModal open={adding} onClose={() => setAdding(false)} serverId={sid} />

      <Modal open={importing} title="批量导入 / 订阅" okText="导入" confirmLoading={importBusy} onCancel={() => setImporting(false)} onOk={() => importForm.submit()} destroyOnHidden>
        <p className="pp-muted pp-small">订阅里的节点会拆成代理库里的一条条代理，每台云手机固定用其中一个。支持 Clash 订阅和分享链接列表；已经有的节点自动跳过。</p>
        <Form form={importForm} layout="vertical" onFinish={(v) => void doImport(v)}>
          <Form.Item label="订阅地址" name="url" extra="由服务器去下载"><Input placeholder="https://…" /></Form.Item>
          <Form.Item label="或者粘贴订阅内容 / 多行分享链接" name="text"><Input.TextArea autoSize={{ minRows: 3, maxRows: 10 }} placeholder={'vless://…\ntrojan://…'} /></Form.Item>
          <Form.Item label="备注（可选）" name="note"><Input placeholder="例如：XX 机场 美国" /></Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: 'success' | 'danger' }) {
  return <div className={`pp-stat ${tone ? `pp-stat-${tone}` : ''}`}><span>{label}</span><b>{value}</b></div>
}

function statusTag(p: Proxy) {
  if (!p.check) return <span className="pp-proxy-dot pp-dot-idle">● 未检测</span>
  if (p.check.ok) return <span className="pp-proxy-dot pp-dot-ok">● 可用</span>
  return <Tooltip title={p.check.error}><span className="pp-proxy-dot pp-dot-bad">● 失效</span></Tooltip>
}

/** 协议标签文字。Clash 节点显示底层类型。 */
function protoLabel(p: Proxy): string {
  if (p.protocol === 'clash') return `CLASH · ${clashNode(p.address).type?.toUpperCase() ?? '?'}`
  if (p.protocol === 'subscription') return '订阅链接'
  return p.protocol.toUpperCase()
}

/** 可读的 主机:端口（去掉账号密码）。 */
function proxyAddr(p: Proxy): string {
  if (p.protocol === 'clash') { const n = clashNode(p.address); return n.server ? `${n.server}:${n.port ?? ''}` : '（节点配置）' }
  const m = p.address.match(/^[a-z0-9]+:\/\/(?:[^@/]*@)?([^/?#\s]+)/i)
  if (m) return m[1]
  return p.address.length > 44 ? `${p.address.slice(0, 44)}…` : p.address
}

function clashNode(address: string): { type?: string; server?: string; port?: number } {
  try { return JSON.parse(address) as { type?: string; server?: string; port?: number } } catch { return {} }
}

/** 2 位国家码转国旗 emoji；不是 2 位就不显示。 */
function flag(cc?: string): string {
  if (!cc || cc.length !== 2 || !/^[a-z]{2}$/i.test(cc)) return ''
  return [...cc.toUpperCase()].map((c) => String.fromCodePoint(127397 + c.charCodeAt(0))).join('')
}

/** ISO 时间 → 「今天 10:38」或「11/09 13:58」。 */
function shortTime(iso: string): string {
  const d = new Date(iso); if (isNaN(d.getTime())) return '—'
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  const now = new Date()
  if (d.toDateString() === now.toDateString()) return `今天 ${hm}`
  return `${String(d.getMonth() + 1).padStart(2, '0')}/${String(d.getDate()).padStart(2, '0')} ${hm}`
}
