import { AppstoreOutlined, DeleteOutlined, DownOutlined, PlusOutlined, PoweroffOutlined, ReloadOutlined, SearchOutlined, UnorderedListOutlined } from '@ant-design/icons'
import { Alert, App, Button, Card, Checkbox, Dropdown, Empty, Input, Modal, Segmented, Select, Space, Table, Tooltip } from 'antd'
import { useMemo, useState } from 'react'
import { PhoneScreen } from '@/components/PhoneScreen'
import { IssueBadge, ProxyCell, StatusDot } from '@/components/bits'
import { blocksStart } from '@/data/consistency'
import { useWorkspace } from '@/data/store'
import type { Phone } from '@/data/types'
import { GroupsModal } from './GroupsModal'
import { NewPhoneModal } from './NewPhoneModal'
import { PhoneDrawer } from './PhoneDrawer'

export function PhonesPage() {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [view, setView] = useState<'cards' | 'list'>('cards')
  const [groupId, setGroupId] = useState<string>('all')
  const [hostId, setHostId] = useState<string>('all')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [managingGroups, setManagingGroups] = useState(false)
  const [open, setOpen] = useState<Phone>()
  const [creating, setCreating] = useState(false)

  const list = useMemo(() => ws.phones.filter((p) =>
    (groupId === 'all' || p.groupId === groupId) && (hostId === 'all' || p.hostId === hostId) &&
    (!query || `${p.name} ${String(ws.modelOf(p)?.values.marketName ?? '')}`.toLowerCase().includes(query.toLowerCase()))
  ), [ws, groupId, hostId, query])

  const running = ws.phones.filter((p) => p.status === 'running').length
  const withErrors = ws.phones.filter((p) => blocksStart(ws.issuesOf(p))).length

  const start = (ids: string[]) => {
    const ok = ids.filter((id) => !blocksStart(ws.issuesOf(ws.phones.find((p) => p.id === id)!)))
    const skipped = ids.length - ok.length
    ws.setStatus(ok, 'running')
    message[skipped ? 'warning' : 'success'](skipped ? `开机 ${ok.length} 台，${skipped} 台有错误被跳过` : `开机 ${ok.length} 台（一台一台开，不会同时启动）`)
  }

  const batch = [
    { key: 'start', label: '开机', icon: <PoweroffOutlined />, onClick: () => start(selected) },
    { key: 'stop', label: '关机', icon: <PoweroffOutlined />, onClick: () => ws.setStatus(selected, 'stopped') },
    { key: 'renew', label: '一键新机（重新随机，保留机型）', icon: <ReloadOutlined />, onClick: () => message.info('这个功能还没开放') },
    { key: 'reset', label: '一键重置（清空数据，恢复出厂）', icon: <ReloadOutlined />, onClick: () => Modal.confirm({ title: `重置 ${selected.length} 台云手机？`, content: '手机里的 App、账号登录、相册全部清空，恢复成刚创建的样子。旧数据挪到服务器的回收站，误操作还能找回。参数和代理不变。', okButtonProps: { danger: true }, onOk: () => ws.resetPhones(selected) }) },
    { type: 'divider' as const },
    ...ws.groups.map((g) => ({ key: `group-${g.id}`, label: `移到分组：${g.name}`, onClick: () => ws.assign(selected, { groupId: g.id }) })),
    { type: 'divider' as const },
    ...ws.proxies.map((p) => ({ key: `proxy-${p.id}`, label: `换代理：${p.name}`, onClick: () => ws.assign(selected, { proxyId: p.id }) })),
    { type: 'divider' as const },
    { key: 'delete', label: '删除', danger: true, icon: <DeleteOutlined />, onClick: () => Modal.confirm({ title: `删除 ${selected.length} 台云手机？`, content: '数据（账号登录状态、App、相册）会一起删除；服务器上会先挪到回收站，误删还能找回。', okButtonProps: { danger: true }, onOk: () => { ws.removePhones(selected); setSelected([]) } }) }
  ]

  const toggle = (id: string, on: boolean) => setSelected((s) => (on ? [...s, id] : s.filter((x) => x !== id)))

  return (
    <div className="pp-page">
      <div className="pp-stats">
        <Stat label="云手机" value={ws.phones.length} />
        <Stat label="运行中" value={running} tone="success" />
        <Stat label="有错误（禁止开机）" value={withErrors} tone={withErrors ? 'danger' : undefined} />
        <Stat label="机器在线" value={`${ws.hosts.filter((h) => h.online).length} / ${ws.hosts.length}`} />
      </div>

      {ws.mode === 'live' && ws.hosts[0] && !ws.hosts[0].networkEnabled && (
        <Alert type="warning" showIcon className="pp-alert" title="网络模块没开：云手机直接用服务器自己的网络，只适合开发测试。正式使用前在服务器上运行 host/network.sh，并给管家程序加 -mihomo-api 参数。" />
      )}
      <div className="pp-toolbar">
        <Space wrap>
          <Segmented value={view} onChange={(v) => setView(v as 'cards' | 'list')} options={[{ value: 'cards', icon: <AppstoreOutlined />, label: '画面' }, { value: 'list', icon: <UnorderedListOutlined />, label: '列表' }]} />
          <Select style={{ width: 150 }} value={groupId} onChange={setGroupId} options={[{ value: 'all', label: '全部分组' }, ...ws.groups.map((g) => ({ value: g.id, label: g.name }))]} />
          <Button onClick={() => setManagingGroups(true)}>分组管理</Button>
          <Select style={{ width: 150 }} value={hostId} onChange={setHostId} options={[{ value: 'all', label: '全部机器' }, ...ws.hosts.map((h) => ({ value: h.id, label: h.name }))]} />
          <Input allowClear prefix={<SearchOutlined />} placeholder="名称 / 机型" value={query} onChange={(e) => setQuery(e.target.value)} style={{ width: 200 }} />
        </Space>
        <Space wrap>
          {selected.length > 0 && <span className="pp-muted">已选 {selected.length} 台</span>}
          <Dropdown disabled={!selected.length} menu={{ items: batch }}><Button>批量操作 <DownOutlined /></Button></Dropdown>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>新建云手机</Button>
        </Space>
      </div>

      {list.length === 0 ? <Empty description="没有符合条件的云手机" /> : view === 'cards' ? (
        <div className="pp-grid">
          {list.map((p) => {
            const issues = ws.issuesOf(p)
            return (
              <Card key={p.id} className={`pp-card ${selected.includes(p.id) ? 'pp-card-selected' : ''}`} size="small" variant="outlined">
                <div className="pp-card-top">
                  <Checkbox checked={selected.includes(p.id)} onChange={(e) => toggle(p.id, e.target.checked)} />
                  <b className="pp-card-name">{p.name}</b>
                  <StatusDot status={p.status} />
                </div>
                <div className="pp-card-screen" onClick={() => setOpen(p)} role="button" tabIndex={0}>
                  <PhoneScreen id={p.id} status={p.status} name={p.name} seed={p.name.length + p.id.charCodeAt(5)} />
                </div>
                <div className="pp-card-meta">
                  <div>{String(ws.modelOf(p)?.values.marketName ?? '未选机型')} · A{p.androidVersion}</div>
                  <div><ProxyCell proxy={ws.proxyOf(p)} /></div>
                  <div className="pp-card-row"><span className="pp-chip" style={{ borderColor: ws.groupOf(p)?.color }}>{ws.groupOf(p)?.name ?? '未分组'}</span><IssueBadge issues={issues} /></div>
                </div>
                <div className="pp-card-actions">
                  {p.status === 'running'
                    ? <Button size="small" onClick={() => ws.setStatus([p.id], 'stopped')}>关机</Button>
                    : <Tooltip title={blocksStart(issues) ? '有错误，禁止开机' : ''}><Button size="small" type="primary" disabled={blocksStart(issues)} onClick={() => start([p.id])}>开机</Button></Tooltip>}
                  <Button size="small" onClick={() => setOpen(p)}>打开</Button>
                </div>
              </Card>
            )
          })}
        </div>
      ) : (
        <Table
          rowKey="id"
          dataSource={list}
          pagination={false}
          rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys as string[]) }}
          onRow={(p) => ({ onDoubleClick: () => setOpen(p) })}
          columns={[
            { title: '名称', dataIndex: 'name', render: (name: string, p) => <a onClick={() => setOpen(p)}>{name}</a> },
            { title: '状态', render: (_, p) => <StatusDot status={p.status} /> },
            { title: '机型', render: (_, p) => String(ws.modelOf(p)?.values.marketName ?? '—') },
            { title: '安卓', render: (_, p) => `Android ${p.androidVersion}` },
            { title: '代理', render: (_, p) => <ProxyCell proxy={ws.proxyOf(p)} /> },
            { title: '分组', render: (_, p) => ws.groupOf(p)?.name ?? '未分组' },
            { title: '机器', render: (_, p) => ws.hostOf(p)?.name },
            { title: '检查', render: (_, p) => <IssueBadge issues={ws.issuesOf(p)} /> }
          ]}
        />
      )}

      <PhoneDrawer phone={open} onClose={() => setOpen(undefined)} />
      <GroupsModal open={managingGroups} onClose={() => setManagingGroups(false)} />
      <NewPhoneModal open={creating} onClose={() => setCreating(false)} />
    </div>
  )
}

function Stat({ label, value, tone }: { label: string; value: number | string; tone?: 'success' | 'danger' }) {
  return <div className={`pp-stat ${tone ? `pp-stat-${tone}` : ''}`}><span>{label}</span><b>{value}</b></div>
}
