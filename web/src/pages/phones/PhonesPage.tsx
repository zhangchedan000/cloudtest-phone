import { AppstoreAddOutlined, AppstoreOutlined, DeleteOutlined, DownOutlined, EditOutlined, PlusOutlined, PoweroffOutlined, ReloadOutlined, SearchOutlined, UnorderedListOutlined, UploadOutlined } from '@ant-design/icons'
import { Alert, App, Button, Card, Checkbox, Dropdown, Empty, Input, Modal, Segmented, Select, Space, Table, Tag, Tooltip } from 'antd'
import { useMemo, useRef, useState } from 'react'
import { batchUpload, type BatchResult } from '@/data/api'
import { PhoneScreen } from '@/components/PhoneScreen'
import { IssueBadge, ProxyCell, StatusDot } from '@/components/bits'
import { blocksStart } from '@/data/consistency'
import { useWorkspace } from '@/data/store'
import type { Phone } from '@/data/types'
import { GroupsModal } from './GroupsModal'
import { NewPhoneModal } from './NewPhoneModal'
import { PhoneDrawer } from './PhoneDrawer'
import { openPhoneWindow } from './phoneWindow'

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
  const [openMode, setOpenMode] = useState<'view' | 'edit'>('view')
  // 打开手机：单独弹一个窗口（可以同时开好几台）；浏览器拦了弹窗就在页面里打开
  const view_ = (p: Phone) => {
    if (ws.mode === 'live' && openPhoneWindow(p)) return
    if (ws.mode === 'live') message.warning('浏览器拦住了弹出窗口：点地址栏右边的图标，选「始终允许」，之后每台手机都会单独弹出窗口')
    setOpenMode('view'); setOpen(p)
  }
  const edit = (p: Phone) => { setOpenMode('edit'); setOpen(p) }
  // 点卡片画面：关机的直接开机，开着的放大（打开大屏）
  const clickScreen = (p: Phone) => {
    if (p.status === 'stopped' || p.status === 'error') {
      if (blocksStart(ws.issuesOf(p))) { message.warning('这台有错误没解决，禁止开机：点「编辑」看「检查」'); return }
      start([p.id])
      // 开机时就把窗口弹出来：先显示「开机中…」，开好自动出画面
      if (ws.mode === 'live') openPhoneWindow(p)
    } else view_(p)
  }
  const [creating, setCreating] = useState(false)
  // 批量装 App / 传文件：文件只上传一次，服务器分发到选中的每台
  const apkInput = useRef<HTMLInputElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const [batchBusy, setBatchBusy] = useState('')
  const [batchDone, setBatchDone] = useState<{ title: string; results: BatchResult[] }>()

  const list = useMemo(() => ws.phones.filter((p) =>
    (groupId === 'all' || p.groupId === groupId) && (hostId === 'all' || p.hostId === hostId) &&
    (!query || `${p.name} ${String(ws.modelOf(p)?.values.marketName ?? '')}`.toLowerCase().includes(query.toLowerCase()))
  ), [ws, groupId, hostId, query])

  const running = ws.phones.filter((p) => p.status === 'running').length
  const withErrors = ws.phones.filter((p) => blocksStart(ws.issuesOf(p))).length
  // 上网健康检查：运行中但连不上外网的手机（管家程序每 2 分钟查一次）
  const offline = ws.phones.filter((p) => p.status === 'running' && p.net && !p.net.ok)

  const start = (ids: string[]) => {
    const ok = ids.filter((id) => !blocksStart(ws.issuesOf(ws.phones.find((p) => p.id === id)!)))
    const skipped = ids.length - ok.length
    ws.setStatus(ok, 'running')
    message[skipped ? 'warning' : 'success'](skipped ? `开机 ${ok.length} 台，${skipped} 台有错误被跳过` : `开机 ${ok.length} 台（一台一台开，不会同时启动）`)
  }

  const runBatch = async (kind: 'apk' | 'file', file: File | undefined) => {
    if (!file) return
    const ids = selected.filter((id) => ws.phones.find((p) => p.id === id)?.status === 'running')
    const skipped = selected.length - ids.length
    if (!ids.length) { message.warning('选中的手机都没在运行，先开机'); return }
    const what = kind === 'apk' ? '安装' : '上传'
    try {
      setBatchBusy(`${file.name} 上传中 0%`)
      const results = await batchUpload(kind, ids, file, (pct) => setBatchBusy(pct < 100 ? `${file.name} 上传中 ${pct}%` : `上传完了，正在${what}到 ${ids.length} 台手机…`))
      setBatchDone({ title: `${what} ${file.name}：成功 ${results.filter((r) => r.ok).length} / ${results.length} 台${skipped ? `（另有 ${skipped} 台没开机，跳过）` : ''}`, results })
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setBatchBusy('')
    }
  }

  const batch = [
    { key: 'start', label: '开机', icon: <PoweroffOutlined />, onClick: () => start(selected) },
    { key: 'stop', label: '关机', icon: <PoweroffOutlined />, onClick: () => ws.setStatus(selected, 'stopped') },
    { key: 'renew', label: '一键新机（重新随机，保留机型）', icon: <ReloadOutlined />, onClick: () => message.info('这个功能还没开放') },
    { key: 'reset', label: '一键重置（清空数据，恢复出厂）', icon: <ReloadOutlined />, onClick: () => Modal.confirm({ title: `重置 ${selected.length} 台云手机？`, content: '手机里的 App、账号登录、相册全部清空，恢复成刚创建的样子。旧数据挪到服务器的回收站，误操作还能找回。参数和代理不变。', okButtonProps: { danger: true }, onOk: () => ws.resetPhones(selected) }) },
    { type: 'divider' as const },
    { key: 'apk', label: '安装 APK（选中的每台，只上传一次）', icon: <AppstoreAddOutlined />, onClick: () => apkInput.current?.click() },
    { key: 'files', label: '上传文件（图片视频进相册，其他进下载目录）', icon: <UploadOutlined />, onClick: () => fileInput.current?.click() },
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

      {ws.mode === 'live' && ws.hosts[0]?.netHealth && !ws.hosts[0].netHealth.ok && (
        <Alert type="error" showIcon className="pp-alert" title="网络模块有问题，云手机可能都上不了网" description={(ws.hosts[0].netHealth.problems ?? []).join('；')} />
      )}
      {ws.mode === 'live' && offline.length > 0 && (
        <Alert type="error" showIcon className="pp-alert" title={`${offline.length} 台运行中的手机上不了外网：${offline.map((p) => p.name).join('、')}`} description="每 2 分钟从手机里试连一次外网。先在「代理 IP」里检测这几台用的代理；所有手机都连不上的话，多半是网络模块的问题。" />
      )}
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
          <Dropdown disabled={!selected.length || !!batchBusy} menu={{ items: batch }}><Button loading={!!batchBusy}>批量操作 <DownOutlined /></Button></Dropdown>
          {batchBusy && <span className="pp-muted">{batchBusy}</span>}
          <input ref={apkInput} type="file" accept=".apk" hidden onChange={(e) => { void runBatch('apk', e.target.files?.[0]); e.target.value = '' }} />
          <input ref={fileInput} type="file" hidden onChange={(e) => { void runBatch('file', e.target.files?.[0]); e.target.value = '' }} />
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
                <Tooltip title={p.status === 'running' ? '点击放大' : p.status === 'stopped' || p.status === 'error' ? '点击开机' : ''}>
                <div className="pp-card-screen" onClick={() => clickScreen(p)} role="button" tabIndex={0}>
                  <PhoneScreen id={p.id} status={p.status} name={p.name} seed={p.name.length + p.id.charCodeAt(5)} startHint startBlocked={blocksStart(issues)} />
                </div>
                </Tooltip>
                <div className="pp-card-meta">
                  <div>{String(ws.modelOf(p)?.values.marketName ?? '未选机型')} · A{p.androidVersion}</div>
                  <div><ProxyCell proxy={ws.proxyOf(p)} /></div>
                  <div className="pp-card-row"><span className="pp-chip" style={{ borderColor: ws.groupOf(p)?.color }}>{ws.groupOf(p)?.name ?? '未分组'}</span><IssueBadge issues={issues} /></div>
                  {p.usage && <div className="pp-card-row pp-small pp-muted">{usageText(p.usage)}</div>}
                  {p.status === 'running' && p.net && !p.net.ok && <Tooltip title={p.net.error}><Tag color="error" className="pp-small">上不了网</Tag></Tooltip>}
                </div>
                <div className="pp-card-actions">
                  {(p.status === 'running' || p.status === 'starting') && <Button size="small" onClick={() => ws.setStatus([p.id], 'stopped')}>关机</Button>}
                  <Button size="small" icon={<EditOutlined />} onClick={() => edit(p)}>编辑</Button>
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
          onRow={(p) => ({ onDoubleClick: () => view_(p) })}
          columns={[
            { title: '名称', dataIndex: 'name', render: (name: string, p) => <a onClick={() => view_(p)}>{name}</a> },
            { title: '状态', render: (_, p) => <StatusDot status={p.status} /> },
            { title: '机型', render: (_, p) => String(ws.modelOf(p)?.values.marketName ?? '—') },
            { title: '安卓', render: (_, p) => `Android ${p.androidVersion}` },
            { title: '代理', render: (_, p) => <ProxyCell proxy={ws.proxyOf(p)} /> },
            { title: '分组', render: (_, p) => ws.groupOf(p)?.name ?? '未分组' },
            { title: '占用', render: (_, p) => (p.usage ? usageText(p.usage) : '—') },
            { title: '机器', render: (_, p) => ws.hostOf(p)?.name },
            { title: '检查', render: (_, p) => <IssueBadge issues={ws.issuesOf(p)} /> },
            { title: '', render: (_, p) => <Space size={4}>{p.status === 'running' || p.status === 'starting' ? <Button size="small" onClick={() => ws.setStatus([p.id], 'stopped')}>关机</Button> : <Button size="small" type="primary" disabled={blocksStart(ws.issuesOf(p))} onClick={() => start([p.id])}>开机</Button>}<Button size="small" onClick={() => edit(p)}>编辑</Button></Space> }
          ]}
        />
      )}

      <Modal open={!!batchDone} title={batchDone?.title} footer={null} onCancel={() => setBatchDone(undefined)}>
        <Table size="small" pagination={false} rowKey="id" dataSource={batchDone?.results ?? []} columns={[
          { title: '手机', render: (_, r: BatchResult) => ws.phones.find((p) => p.id === r.id)?.name ?? r.id },
          { title: '结果', render: (_, r: BatchResult) => (r.ok ? <Tag color="success">成功</Tag> : <Tag color="error">失败</Tag>) },
          { title: '说明', render: (_, r: BatchResult) => r.error ?? r.path ?? '' }
        ]} />
      </Modal>
      <PhoneDrawer phone={open} mode={openMode} onEdit={() => setOpenMode('edit')} onClose={() => setOpen(undefined)} />
      <GroupsModal open={managingGroups} onClose={() => setManagingGroups(false)} />
      <NewPhoneModal open={creating} onClose={() => setCreating(false)} />
    </div>
  )
}

function Stat({ label, value, tone }: { label: string; value: number | string; tone?: 'success' | 'danger' }) {
  return <div className={`pp-stat ${tone ? `pp-stat-${tone}` : ''}`}><span>{label}</span><b>{value}</b></div>
}

/** 「CPU 12% · 内存 0.9 / 3 GB」 */
function usageText(u: NonNullable<Phone['usage']>): string {
  const gb = (mb: number) => (mb / 1024).toFixed(mb >= 10240 ? 0 : 1)
  return `CPU ${Math.round(u.cpuPercent)}% · 内存 ${gb(u.memMB)} / ${gb(u.memLimitMB)} GB`
}
