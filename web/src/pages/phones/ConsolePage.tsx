import { CaretRightOutlined, DeleteOutlined, EditOutlined, EyeOutlined, FolderOutlined, MobileOutlined, PlusOutlined, PoweroffOutlined, RedoOutlined, ReloadOutlined, SearchOutlined, UnorderedListOutlined, UploadOutlined } from '@ant-design/icons'
import { App, Badge, Button, Checkbox, Drawer, Dropdown, Empty, Input, Modal, Select, Space, Table, Tabs, Tag, Tooltip } from 'antd'
import { useEffect, useMemo, useRef, useState } from 'react'
import { batchUpload } from '@/data/api'
import { PhoneScreen } from '@/components/PhoneScreen'
import { IssueBadge, ProxyCell, StatusDot } from '@/components/bits'
import { blocksStart } from '@/data/consistency'
import { useWorkspace } from '@/data/store'
import type { Phone } from '@/data/types'
import { GroupsModal } from './GroupsModal'
import { NewPhoneModal } from './NewPhoneModal'
import { PhoneDrawer } from './PhoneDrawer'
import { openPhoneWindow } from './phoneWindow'

/** 一条批量任务记录（任务列表里显示）。 */
interface Task { id: number; at: Date; what: string; count: number; state: 'running' | 'done' | 'failed'; detail?: string }

/**
 * 控制台：仿魔云腾「边缘计算管理平台」客户端的布局——
 * 左边设备/分组树，右边顶部批量操作栏，下面列表 / 网格视图，网格下每台有 ▶ ■，右上角任务列表。
 * 只是另一种排布方式，用的还是同一套管家接口和组件。
 */
export function ConsolePage() {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [groupKey, setGroupKey] = useState('all')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [collapsed, setCollapsed] = useState<string[]>([])
  const [tab, setTab] = useState<'grid' | 'list'>('grid')
  const [refreshSec, setRefreshSec] = useState(0)
  const [tasks, setTasks] = useState<Task[]>([])
  const [tasksOpen, setTasksOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [groupsOpen, setGroupsOpen] = useState(false)
  const [editing, setEditing] = useState<Phone>()
  const apkInput = useRef<HTMLInputElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const nextId = useRef(1)

  // 刷新间隔：0 = 跟默认（每 5 秒），其它按选的秒数额外刷新
  useEffect(() => {
    if (!refreshSec) return
    const t = setInterval(() => { void ws.refresh() }, refreshSec * 1000)
    return () => clearInterval(t)
  }, [refreshSec, ws])

  const keyOf = (p: { serverId?: string; groupId?: string }) => `${p.serverId ?? 'local'}|${p.groupId ?? ''}`
  const matches = (p: Phone) => !query || `${p.name} ${String(ws.modelOf(p)?.values.marketName ?? '')}`.toLowerCase().includes(query.toLowerCase())
  const list = useMemo(() => ws.phones.filter((p) => (groupKey === 'all' || keyOf(p) === groupKey) && matches(p)), [ws.phones, groupKey, query])

  // 树：每个分组下挂它的手机；没分组的单独一组
  const tree = useMemo(() => {
    const groups = ws.groups.map((g) => ({ key: keyOf({ serverId: g.serverId, groupId: g.id }), name: ws.servers.length ? `${g.name}（${ws.serverName(g.serverId)}）` : g.name, color: g.color }))
    const known = new Set(groups.map((g) => g.key))
    const loose = ws.phones.filter((p) => !known.has(keyOf(p)))
    const nodes = groups.map((g) => ({ ...g, phones: ws.phones.filter((p) => keyOf(p) === g.key && matches(p)) }))
    if (loose.length) nodes.push({ key: 'none', name: '未分组', color: '', phones: loose.filter(matches) })
    return nodes
  }, [ws.groups, ws.phones, ws.servers, query])

  /** 跑一个批量任务，记进任务列表。 */
  const runTask = async (what: string, ids: string[], fn: () => Promise<unknown>) => {
    if (!ids.length) { message.warning('先勾选手机'); return }
    const id = nextId.current++
    setTasks((t) => [{ id, at: new Date(), what, count: ids.length, state: 'running' } as Task, ...t].slice(0, 100))
    try {
      await fn()
      setTasks((t) => t.map((x) => (x.id === id ? { ...x, state: 'done' } : x)))
    } catch (e) {
      setTasks((t) => t.map((x) => (x.id === id ? { ...x, state: 'failed', detail: (e as Error).message } : x)))
    }
  }

  const startable = (ids: string[]) => ids.filter((id) => { const p = ws.phones.find((x) => x.id === id); return p && !blocksStart(ws.issuesOf(p)) })
  const running = (ids: string[]) => ids.filter((id) => ws.phones.find((x) => x.id === id)?.status === 'running')

  const batchStart = (ids: string[]) => { const ok = startable(ids); if (ok.length < ids.length) message.warning(`${ids.length - ok.length} 台有错误，跳过`); void runTask('批量开机', ok, () => ws.setStatus(ok, 'running')) }
  const batchStop = (ids: string[]) => void runTask('批量关机', ids, () => ws.setStatus(ids, 'stopped'))
  const batchRestart = (ids: string[]) => void runTask('批量重启', running(ids), async () => { const r = running(ids); await ws.setStatus(r, 'stopped'); await ws.setStatus(r, 'running') })
  const batchScreen = (ids: string[]) => {
    const r = ws.phones.filter((p) => ids.includes(p.id) && p.status === 'running')
    if (!r.length) { message.warning('选中的手机没有在运行的'); return }
    let blocked = 0
    for (const p of r) if (!openPhoneWindow(p)) blocked++
    if (blocked) message.warning('浏览器拦住了弹出窗口：在地址栏右边选「始终允许」后再点一次')
    setTasks((t) => [{ id: nextId.current++, at: new Date(), what: '批量投屏', count: r.length, state: 'done' as const } as Task, ...t])
  }
  /** 危险操作先做一道算术题（仿魔云腾客户端的「算术校验」，防手滑）。 */
  const mathConfirm = (title: string, content: string, onOk: () => void) => {
    const a = 2 + Math.floor(Math.random() * 18), b = 2 + Math.floor(Math.random() * 18)
    let answer = ''
    Modal.confirm({
      title, okButtonProps: { danger: true },
      content: <div><p>{content}</p><p>防误操作，请算一下：<b>{a} + {b} = ?</b></p><Input autoFocus onChange={(e) => { answer = e.target.value }} /></div>,
      onOk: () => { if (Number(answer.trim()) !== a + b) { message.error('算错了，没有执行'); return } onOk() }
    })
  }
  const batchDelete = (ids: string[]) => { if (!ids.length) { message.warning('先勾选手机'); return } mathConfirm(`删除 ${ids.length} 台云手机？`, '数据会先挪到服务器回收站，误删还能找回。', () => { void runTask('批量删除', ids, () => ws.removePhones(ids)); setSelected([]) }) }
  const batchReset = (ids: string[]) => { if (!ids.length) { message.warning('先勾选手机'); return } mathConfirm(`重置 ${ids.length} 台云手机？`, '手机里的 App、账号、相册全部清空，旧数据挪到回收站。参数和代理不变。', () => void runTask('批量重置', ids, () => ws.resetPhones(ids))) }
  const batchRenew = (ids: string[]) => void runTask('一键新机', ids, () => ws.renewPhones(ids))

  /** 云机右键菜单：常用的放前面。 */
  const contextMenu = (p: Phone) => ({
    items: [
      ...(p.status === 'running' ? [{ key: 'open', label: '打开投屏' }, { key: 'restart', label: '重启' }, { key: 'stop', label: '关机' }] : [{ key: 'start', label: '开机', disabled: blocksStart(ws.issuesOf(p)) }]),
      { key: 'edit', label: '编辑参数 / 导入真机参数' },
      { key: 'copyip', label: '复制出口 IP' },
      { type: 'divider' as const },
      { key: 'renew', label: '一键新机（换身份，保留数据）' },
      { key: 'reset', label: '一键重置（清空数据）', danger: true },
      { key: 'delete', label: '删除', danger: true }
    ],
    onClick: ({ key }: { key: string }) => {
      if (key === 'open') openPhoneWindow(p)
      else if (key === 'start') batchStart([p.id])
      else if (key === 'restart') batchRestart([p.id])
      else if (key === 'stop') batchStop([p.id])
      else if (key === 'edit') setEditing(p)
      else if (key === 'copyip') { const ip = ws.proxyOf(p)?.check?.exitIp; if (ip) { void navigator.clipboard.writeText(ip); message.success(`已复制 ${ip}`) } else message.info('这台的代理还没检测出出口 IP') }
      else if (key === 'renew') batchRenew([p.id])
      else if (key === 'reset') batchReset([p.id])
      else if (key === 'delete') batchDelete([p.id])
    }
  })
  const batchUploadFile = async (kind: 'apk' | 'file', file?: File) => {
    if (!file) return
    const ids = running(selected)
    if (!ids.length) { message.warning('选中的手机没有在运行的，先开机'); return }
    await runTask(kind === 'apk' ? `批量安装 ${file.name}` : `批量上传 ${file.name}`, ids, async () => {
      const res = await batchUpload(kind, ids, file)
      const bad = res.filter((r) => !r.ok)
      if (bad.length) throw new Error(`${bad.length} 台失败：${bad.map((r) => r.error).join('；')}`)
    })
  }

  const allIds = list.map((p) => p.id)
  const allChecked = allIds.length > 0 && allIds.every((id) => selected.includes(id))
  const toggle = (id: string, on: boolean) => setSelected((s) => (on ? [...new Set([...s, id])] : s.filter((x) => x !== id)))
  const runningTasks = tasks.filter((t) => t.state === 'running').length

  return (
    <div className="pp-console">
      {/* 左：设备列表（分组树） */}
      <aside className="pp-console-side">
        <div className="pp-console-side-title">设备列表</div>
        <Space.Compact style={{ width: '100%' }}>
          <Input allowClear prefix={<SearchOutlined />} placeholder="搜索设备…" value={query} onChange={(e) => setQuery(e.target.value)} />
          <Tooltip title="分组管理"><Button icon={<PlusOutlined />} onClick={() => setGroupsOpen(true)}>组</Button></Tooltip>
          <Button onClick={() => void ws.refresh()}>刷新</Button>
        </Space.Compact>
        <div className="pp-console-tree">
          <div className={`pp-tree-row pp-tree-group ${groupKey === 'all' ? 'pp-tree-active' : ''}`} onClick={() => setGroupKey('all')}>
            <FolderOutlined /> <span className="pp-tree-name">全部</span><span className="pp-muted">({ws.phones.length})</span>
          </div>
          {tree.map((g) => (
            <div key={g.key}>
              <div className={`pp-tree-row pp-tree-group ${groupKey === g.key ? 'pp-tree-active' : ''}`} onClick={() => setGroupKey(g.key)}>
                <CaretRightOutlined rotate={collapsed.includes(g.key) ? 0 : 90} onClick={(e) => { e.stopPropagation(); setCollapsed((c) => (c.includes(g.key) ? c.filter((x) => x !== g.key) : [...c, g.key])) }} />
                <span className="pp-tree-dot" style={{ background: g.color || 'var(--fb-text-muted)' }} />
                <span className="pp-tree-name">{g.name}</span><span className="pp-muted">({g.phones.length})</span>
              </div>
              {!collapsed.includes(g.key) && g.phones.map((p) => (
                <Dropdown key={p.id} trigger={['contextMenu']} menu={contextMenu(p)}>
                <div className="pp-tree-row pp-tree-phone" onClick={() => (p.status === 'running' ? openPhoneWindow(p) : setEditing(p))}>
                  <Checkbox checked={selected.includes(p.id)} onClick={(e) => e.stopPropagation()} onChange={(e) => toggle(p.id, e.target.checked)} />
                  <MobileOutlined /> <span className="pp-tree-name">{p.name}</span><StatusDot status={p.status} />
                </div>
                </Dropdown>
              ))}
            </div>
          ))}
        </div>
        <Button block icon={<PlusOutlined />} onClick={() => setCreating(true)}>新建云手机</Button>
      </aside>

      {/* 右：批量栏 + 视图 */}
      <section className="pp-console-main">
        <div className="pp-console-bar">
          <Checkbox checked={allChecked} indeterminate={!allChecked && selected.length > 0} onChange={(e) => setSelected(e.target.checked ? allIds : [])} />
          <Button icon={<EditOutlined />} onClick={() => setCreating(true)}>批量创建</Button>
          <Button icon={<EyeOutlined />} onClick={() => batchScreen(selected)}>批量投屏</Button>
          <Dropdown menu={{ items: [{ key: 'apk', label: '安装 APK' }, { key: 'file', label: '上传文件（图片视频进相册）' }], onClick: ({ key }) => (key === 'apk' ? apkInput.current?.click() : fileInput.current?.click()) }}>
            <Button icon={<UploadOutlined />}>批量上传</Button>
          </Dropdown>
          <Button icon={<PoweroffOutlined />} onClick={() => batchStart(selected)}>批量开机</Button>
          <Button icon={<RedoOutlined />} onClick={() => batchRestart(selected)}>批量重启</Button>
          <Button onClick={() => batchStop(selected)}>批量关机</Button>
          <Dropdown menu={{ items: [{ key: 'renew', label: '批量一键新机（换身份，保留数据）' }, { key: 'reset', label: '批量重置（清空数据）', danger: true }], onClick: ({ key }) => (key === 'renew' ? batchRenew(selected) : batchReset(selected)) }}>
            <Button>更多</Button>
          </Dropdown>
          <Button danger icon={<DeleteOutlined />} onClick={() => batchDelete(selected)}>批量删除</Button>
          <Button icon={<ReloadOutlined />} onClick={() => void ws.refresh()}>刷新</Button>
          <span className="pp-muted">刷新间隔</span>
          <Select style={{ width: 90 }} value={refreshSec} onChange={setRefreshSec} options={[{ value: 0, label: '默认' }, { value: 1, label: '1 秒' }, { value: 3, label: '3 秒' }, { value: 10, label: '10 秒' }]} />
          <span style={{ flex: 1 }} />
          {selected.length > 0 && <span className="pp-muted">已选 {selected.length} 台</span>}
          <Badge count={runningTasks} size="small"><Button icon={<UnorderedListOutlined />} onClick={() => setTasksOpen(true)}>任务列表</Button></Badge>
          <input ref={apkInput} type="file" accept=".apk" hidden onChange={(e) => { void batchUploadFile('apk', e.target.files?.[0]); e.target.value = '' }} />
          <input ref={fileInput} type="file" hidden onChange={(e) => { void batchUploadFile('file', e.target.files?.[0]); e.target.value = '' }} />
        </div>

        <Tabs activeKey={tab} onChange={(k) => setTab(k as 'grid' | 'list')} items={[{ key: 'list', label: '列表视图' }, { key: 'grid', label: '网格视图' }]} />

        {list.length === 0 ? <Empty description="没有手机" /> : tab === 'grid' ? (
          <div className="pp-console-grid">
            {list.map((p) => (
              <Dropdown key={p.id} trigger={['contextMenu']} menu={contextMenu(p)}>
              <div className={`pp-console-tile ${selected.includes(p.id) ? 'pp-console-tile-on' : ''}`}>
                <div className="pp-console-screen" onClick={() => (p.status === 'running' ? openPhoneWindow(p) : setEditing(p))}>
                  <PhoneScreen id={p.id} status={p.status} name={p.name} seed={p.name.length + p.id.charCodeAt(5)} />
                </div>
                <div className="pp-console-tile-foot">
                  <Checkbox checked={selected.includes(p.id)} onChange={(e) => toggle(p.id, e.target.checked)} />
                  <Tooltip title={`${String(ws.modelOf(p)?.values.marketName ?? '')} · A${p.androidVersion}`}><span className="pp-tree-name">{p.name}</span></Tooltip>
                  {p.status === 'running' || p.status === 'starting'
                    ? <Tooltip title="打开"><Button size="small" type="text" icon={<CaretRightOutlined />} onClick={() => openPhoneWindow(p)} /></Tooltip>
                    : <Tooltip title={blocksStart(ws.issuesOf(p)) ? '有错误不能开机' : '开机'}><Button size="small" type="text" disabled={blocksStart(ws.issuesOf(p))} icon={<CaretRightOutlined />} onClick={() => batchStart([p.id])} /></Tooltip>}
                  <Tooltip title="关机"><Button size="small" type="text" disabled={p.status !== 'running' && p.status !== 'starting'} icon={<span className="pp-stop-square" />} onClick={() => batchStop([p.id])} /></Tooltip>
                </div>
              </div>
              </Dropdown>
            ))}
          </div>
        ) : (
          <Table
            size="small" rowKey="id" dataSource={list} pagination={false}
            rowSelection={{ selectedRowKeys: selected, onChange: (k) => setSelected(k as string[]) }}
            onRow={(p) => ({ onDoubleClick: () => (p.status === 'running' ? openPhoneWindow(p) : setEditing(p)) })}
            columns={[
              { title: '名称', render: (_, p) => <a onClick={() => setEditing(p)}>{p.name}</a> },
              { title: '状态', render: (_, p) => <StatusDot status={p.status} /> },
              { title: '机型', render: (_, p) => <Space size={4}>{String(ws.modelOf(p)?.values.marketName ?? '—')}{p.gapps && <Tag color="blue">谷歌</Tag>}</Space> },
              { title: '安卓', render: (_, p) => `A${p.androidVersion}` },
              { title: '代理', render: (_, p) => <ProxyCell proxy={ws.proxyOf(p)} /> },
              { title: '出口 IP', render: (_, p) => ws.proxyOf(p)?.check?.exitIp ?? '—' },
              { title: '内网 IP', render: (_, p) => p.ip ?? '—' },
              { title: '检查', render: (_, p) => <IssueBadge issues={ws.issuesOf(p)} /> },
              { title: '', render: (_, p) => <Space size={4}>{p.status === 'running' ? <><Button size="small" onClick={() => openPhoneWindow(p)}>打开</Button><Button size="small" onClick={() => batchStop([p.id])}>关机</Button></> : <Button size="small" type="primary" disabled={blocksStart(ws.issuesOf(p))} onClick={() => batchStart([p.id])}>开机</Button>}<Button size="small" onClick={() => setEditing(p)}>编辑</Button></Space> }
            ]}
          />
        )}
      </section>

      <Drawer title="任务列表" open={tasksOpen} onClose={() => setTasksOpen(false)} width={460} extra={<Button size="small" onClick={() => setTasks((t) => t.filter((x) => x.state === 'running'))}>清空已完成</Button>}>
        {tasks.length === 0 ? <Empty description="还没有任务" /> : (
          <Table size="small" rowKey="id" pagination={false} dataSource={tasks} columns={[
            { title: '时间', render: (_, t: Task) => t.at.toLocaleTimeString() },
            { title: '操作', render: (_, t: Task) => `${t.what}（${t.count} 台）` },
            { title: '状态', render: (_, t: Task) => (t.state === 'running' ? <Tag color="processing">进行中</Tag> : t.state === 'done' ? <Tag color="success">完成</Tag> : <Tooltip title={t.detail}><Tag color="error">失败</Tag></Tooltip>) }
          ]} />
        )}
      </Drawer>
      <PhoneDrawer phone={editing} mode="edit" onClose={() => setEditing(undefined)} />
      <GroupsModal open={groupsOpen} onClose={() => setGroupsOpen(false)} />
      <NewPhoneModal open={creating} onClose={() => setCreating(false)} />
    </div>
  )
}
