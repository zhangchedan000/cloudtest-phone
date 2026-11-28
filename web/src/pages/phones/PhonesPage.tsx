import { AppstoreAddOutlined, AppstoreOutlined, DeleteOutlined, DownOutlined, EditOutlined, PlusOutlined, PoweroffOutlined, ReloadOutlined, SearchOutlined, TeamOutlined, UnorderedListOutlined, UploadOutlined } from '@ant-design/icons'
import { Alert, App, Button, Card, Checkbox, Dropdown, Empty, Input, Modal, Segmented, Select, Slider, Space, Table, Tag, Tooltip } from 'antd'
import { useMemo, useRef, useState } from 'react'
import { batchUpload, type BatchResult } from '@/data/api'
import { usePersisted } from '@/data/persist'
import { DataTable } from '@/components/DataTable'
import { PhoneScreen } from '@/components/PhoneScreen'
import { IssueBadge, ProxyCell, StatusDot } from '@/components/bits'
import { blocksStart } from '@/data/consistency'
import { useWorkspace } from '@/data/store'
import type { Host, Phone } from '@/data/types'
import { AddServerModal } from '../other/AddServerModal'
import { GroupsModal } from './GroupsModal'
import { NewPhoneModal } from './NewPhoneModal'
import { PhoneDrawer } from './PhoneDrawer'
import { openPhoneWindow } from './phoneWindow'

export function PhonesPage() {
  const ws = useWorkspace()
  const { message } = App.useApp()
  // 这些「偏好」存本地，关了重开保持（usePersisted）：视图、分组筛选、搜索、隐藏空坑位、窗口 / 投屏大小
  const [view, setView] = usePersisted<'cards' | 'list'>('pp.phones.view', 'cards')
  const [groupId, setGroupId] = usePersisted<string>('pp.phones.group', 'all')
  const [hostId, setHostId] = usePersisted<string>('pp.phones.host', 'all')
  const [query, setQuery] = usePersisted('pp.phones.query', '')
  const [hostQuery, setHostQuery] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [managingGroups, setManagingGroups] = useState(false)
  const [hideEmpty, setHideEmpty] = usePersisted('pp.phones.hideEmpty', false)
  const [slotW, setSlotW] = usePersisted('pp.phones.slotW', 210)
  // 投屏大小 = 手机画面占卡片宽度的百分比（55~100）；画面始终竖屏 1:2，放大就整体按竖版变大
  const [screenPct, setScreenPct] = usePersisted('pp.phones.screenPct', 82)
  // 每台机器固定给几个「空坑位」快捷创建入口（不按同时并发数限制，想建多少建多少）
  const EMPTY_SLOTS = 8
  const [open, setOpen] = useState<Phone>()
  const [openMode, setOpenMode] = useState<'view' | 'edit'>('view')
  // 打开手机：单独弹一个窗口（可以同时开好几台）；浏览器拦了弹窗就在页面里打开
  const view_ = (p: Phone) => {
    if (ws.mode === 'live' && openPhoneWindow(p)) return
    if (ws.mode === 'live') message.warning('浏览器拦住了弹出窗口：点地址栏右边的图标，选「始终允许」，之后每台手机都会单独弹出窗口')
    setOpenMode('view'); setOpen(p)
  }
  const edit = (p: Phone) => { setOpenMode('edit'); setOpen(p) }
  // 点卡片画面：关机的只开机（卡片状态变「开机中」即可，不自动弹窗口，2026-11-28 用户要求——
  // 想看画面等开好后再点一下卡片）；开着的点一下放大（打开大屏）。
  const clickScreen = (p: Phone) => {
    if (p.status === 'stopped' || p.status === 'error') {
      if (blocksStart(ws.issuesOf(p))) { message.warning('这台有错误没解决，禁止开机：点「编辑」看「检查」'); return }
      start([p.id])
    } else view_(p)
  }
  const [creating, setCreating] = useState(false)
  const [presetHost, setPresetHost] = useState<string>()
  const [presetName, setPresetName] = useState<string>()
  const addPhoneOn = (hid?: string, name?: string) => { setPresetHost(hid); setPresetName(name); setCreating(true) }
  const [addingDevice, setAddingDevice] = useState(false)
  // 批量装 App / 传文件：文件只上传一次，服务器分发到选中的每台
  const apkInput = useRef<HTMLInputElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const [batchBusy, setBatchBusy] = useState('')
  const [batchDone, setBatchDone] = useState<{ title: string; results: BatchResult[] }>()

  // 「只显示运行中」开关（设置页里开）
  const [onlyRunning] = usePersisted('pp.onlyRunning', false)
  const list = useMemo(() => ws.phones.filter((p) =>
    (groupId === 'all' || `${p.serverId ?? 'local'}|${p.groupId}` === groupId) && (hostId === 'all' || p.hostId === hostId) &&
    (!onlyRunning || p.status === 'running' || p.status === 'starting') &&
    (!query || `${p.name} ${String(ws.modelOf(p)?.values.marketName ?? '')}`.toLowerCase().includes(query.toLowerCase()))
  ), [ws, groupId, hostId, query, onlyRunning])

  // 上网健康检查：运行中但连不上外网的手机（管家程序每 2 分钟查一次）
  const offline = ws.phones.filter((p) => p.status === 'running' && p.net && !p.net.ok)
  // 左侧设备列表：在线的排前面
  const onlineHosts = ws.hosts.filter((h) => h.online).length
  const hostHits = ws.hosts.filter((h) => !hostQuery || h.name.toLowerCase().includes(hostQuery.toLowerCase()))

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

  // 选中的手机在不在同一台服务器上（代理、分组每台服务器各一份，跨服务器不能一起换）
  const selServers = [...new Set(selected.map((id) => ws.phones.find((p) => p.id === id)?.serverId ?? 'local'))]
  const selSid = selServers.length === 1 ? selServers[0] : undefined
  const sameSel = <T extends { serverId?: string }>(x: T) => selSid !== undefined && (x.serverId ?? 'local') === selSid
  const batch = [
    { key: 'start', label: '开机', icon: <PoweroffOutlined />, onClick: () => start(selected) },
    { key: 'stop', label: '关机', icon: <PoweroffOutlined />, onClick: () => ws.setStatus(selected, 'stopped') },
    { key: 'renew', label: '一键新机（重新随机身份，保留机型和数据）', icon: <ReloadOutlined />, onClick: () => Modal.confirm({ title: `一键新机 ${selected.length} 台？`, content: '把序列号、WiFi/蓝牙 MAC、Android ID、广告 ID 等「随机」身份重新生成一遍（锁定的字段不变），机型、App 数据、代理都保留。重启这些手机后生效。', onOk: () => ws.renewPhones(selected) }) },
    { key: 'reset', label: '一键重置（清空数据，恢复出厂）', icon: <ReloadOutlined />, onClick: () => Modal.confirm({ title: `重置 ${selected.length} 台云手机？`, content: '手机里的 App、账号登录、相册全部清空，恢复成刚创建的样子。旧数据挪到服务器的回收站，误操作还能找回。参数和代理不变。', okButtonProps: { danger: true }, onOk: () => ws.resetPhones(selected) }) },
    { type: 'divider' as const },
    { key: 'apk', label: '安装 APK（选中的每台，只上传一次）', icon: <AppstoreAddOutlined />, onClick: () => apkInput.current?.click() },
    { key: 'files', label: '上传文件（图片视频进相册，其他进下载目录）', icon: <UploadOutlined />, onClick: () => fileInput.current?.click() },
    { type: 'divider' as const },
    ...(selSid === undefined && selected.length ? [{ key: 'mixed', label: '选中的手机在不同服务器上：换分组、换代理要分开选', disabled: true }] : []),
    ...ws.groups.filter(sameSel).map((g) => ({ key: `group-${g.id}`, label: `移到分组：${g.name}`, onClick: () => ws.assign(selected, { groupId: g.id }) })),
    { type: 'divider' as const },
    ...ws.proxies.filter(sameSel).map((p) => ({ key: `proxy-${p.id}`, label: `换代理：${p.name}`, onClick: () => ws.assign(selected, { proxyId: p.id }) })),
    { type: 'divider' as const },
    { key: 'delete', label: '删除', danger: true, icon: <DeleteOutlined />, onClick: () => Modal.confirm({ title: `删除 ${selected.length} 台云手机？`, content: '数据（账号登录状态、App、相册）会一起删除；服务器上会先挪到回收站，误删还能找回。', okButtonProps: { danger: true }, onOk: () => { ws.removePhones(selected); setSelected([]) } }) }
  ]

  const toggle = (id: string, on: boolean) => setSelected((s) => (on ? [...s, id] : s.filter((x) => x !== id)))

  // 坑位网格里渲染一台已创建的手机（num = 坑位编号 #1 #2 #3…，照魔云腾）
  const renderPhoneCard = (p: Phone, num: number) => {
    const issues = ws.issuesOf(p)
    return (
      <Card key={p.id} className={`pp-card ${selected.includes(p.id) ? 'pp-card-selected' : ''}`} size="small" variant="outlined">
        <div className="pp-card-top">
          <Checkbox checked={selected.includes(p.id)} onChange={(e) => toggle(p.id, e.target.checked)} />
          <span className="pp-card-num">#{num}</span>
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
  }

  // 坑位网格里渲染一个空坑位（num = 坑位编号，点「创建」新建、默认名按编号顺延）
  const renderEmptySlot = (h: Host, i: number, num: number) => (
    <div key={`empty-${h.id}-${i}`} className="pp-card pp-slot-empty" onClick={() => addPhoneOn(h.id, `手机-${num}`)} role="button" tabIndex={0}>
      <span className="pp-slot-num pp-muted">#{num}</span>
      <PlusOutlined className="pp-slot-plus" />
      <span className="pp-muted">空坑位</span>
      <Button size="small" type="primary" ghost icon={<PlusOutlined />}>创建</Button>
    </div>
  )

  // 一台机器的坑位区：已创建的手机 + 空坑位（空坑位数 = 同时最多 − 这台机器上的手机数）
  const renderHostSection = (h: Host) => {
    const mine = list.filter((p) => p.hostId === h.id)
    const total = ws.phones.filter((p) => p.hostId === h.id).length
    const empties = EMPTY_SLOTS
    const ids = mine.map((p) => p.id)
    const sel = ids.filter((id) => selected.includes(id))
    return (
      <section key={h.id} className="pp-host-section">
        <div className="pp-host-section-head">
          <Checkbox
            checked={ids.length > 0 && sel.length === ids.length}
            indeterminate={sel.length > 0 && sel.length < ids.length}
            onChange={(e) => setSelected((s) => (e.target.checked ? [...new Set([...s, ...ids])] : s.filter((x) => !ids.includes(x))))}
          />
          <StatusDot status={h.online ? 'running' : 'stopped'} />
          <b>{h.name}</b>
          <span className="pp-muted pp-small">{total} 个实例 · 运行 {ws.phones.filter((p) => p.hostId === h.id && p.status === 'running').length}</span>
          <Button size="small" type="text" className="pp-host-section-toggle" onClick={() => setHideEmpty((v) => !v)}>{hideEmpty ? '显示空坑位' : `隐藏空坑位(${empties})`}</Button>
        </div>
        {mine.length === 0 && hideEmpty ? (
          <Empty description="这台机器还没有云手机" image={Empty.PRESENTED_IMAGE_SIMPLE}><Button type="primary" icon={<PlusOutlined />} onClick={() => addPhoneOn(h.id)}>创建</Button></Empty>
        ) : (
          <div className="pp-grid" style={{ ['--slot-w' as string]: `${slotW}px`, ['--screen-pct' as string]: `${screenPct}%` }}>
            {mine.map((p, i) => renderPhoneCard(p, i + 1))}
            {!hideEmpty && Array.from({ length: empties }, (_, i) => renderEmptySlot(h, i, mine.length + i + 1))}
          </div>
        )}
      </section>
    )
  }

  const shownHosts = hostId === 'all' ? ws.hosts.filter((h) => h.online) : ws.hosts.filter((h) => h.id === hostId)

  return (
    <div className="pp-page pp-page-flush">
      {ws.mode === 'live' && ws.hosts.filter((h) => h.netHealth && !h.netHealth.ok).map((h) => (
        <Alert key={`net-${h.id}`} type="error" showIcon className="pp-alert" title={`${h.name}：网络模块有问题，这台服务器上的云手机可能都上不了网`} description={(h.netHealth?.problems ?? []).join('；')} />
      ))}
      {ws.mode === 'live' && ws.hosts.filter((h) => !h.online).map((h) => (
        <Alert key={`off-${h.id}`} type="warning" showIcon className="pp-alert" title={`服务器「${h.name}」连不上，它的手机暂时不显示`} description={h.error} />
      ))}
      {ws.mode === 'live' && offline.length > 0 && (
        <Alert type="error" showIcon className="pp-alert" title={`${offline.length} 台运行中的手机上不了外网：${offline.map((p) => p.name).join('、')}`} description="每 2 分钟从手机里试连一次外网。先在「代理 IP」里检测这几台用的代理；所有手机都连不上的话，多半是网络模块的问题。" />
      )}
      {ws.mode === 'live' && ws.hosts[0] && !ws.hosts[0].networkEnabled && (
        <Alert type="warning" showIcon className="pp-alert" title="网络模块没开：云手机直接用服务器自己的网络，只适合开发测试。正式使用前在服务器上运行 host/network.sh，并给管家程序加 -mihomo-api 参数。" />
      )}

      <div className="pp-phones-layout">
        <aside className="pp-hostlist">
          <div className="pp-hostlist-head">
            <b className="pp-hostlist-title">设备列表</b>
            <Button size="small" type="text" icon={<TeamOutlined />} onClick={() => setManagingGroups(true)}>分组</Button>
          </div>
          <Button block type="primary" icon={<PlusOutlined />} disabled={ws.mode !== 'live'} onClick={() => setAddingDevice(true)} className="pp-adddevice">添加设备</Button>
          <div className="pp-hostlist-counts">
            <span><i className="pp-dot-on" /> 在线 {onlineHosts}</span>
            <span><i className="pp-dot-off" /> 离线 {ws.hosts.length - onlineHosts}</span>
            <span className="pp-muted">总 {ws.hosts.length}</span>
          </div>
          <Input allowClear size="small" prefix={<SearchOutlined />} placeholder="搜索 IP / 备注" value={hostQuery} onChange={(e) => setHostQuery(e.target.value)} />
          <div className="pp-hostlist-items">
            <button className={`pp-host-item ${hostId === 'all' ? 'pp-host-item-active' : ''}`} onClick={() => setHostId('all')}>
              <AppstoreOutlined />
              <div className="pp-host-item-main"><div className="pp-host-item-name">全部设备</div><div className="pp-muted pp-small">{ws.phones.length} 台云手机</div></div>
            </button>
            {/* 照魔云腾：设备归到「未分组」组下面 */}
            <div className="pp-hostgroup-head"><DownOutlined className="pp-hostgroup-caret" /><span>未分组</span><span className="pp-muted">({hostHits.length})</span></div>
            {hostHits.map((h) => {
              const total = ws.phones.filter((p) => p.hostId === h.id).length
              const run = ws.phones.filter((p) => p.hostId === h.id && p.status === 'running').length
              return (
                <button key={h.id} className={`pp-host-item pp-host-device ${hostId === h.id ? 'pp-host-item-active' : ''}`} onClick={() => setHostId(h.id)}>
                  <span className={`pp-host-q ${h.online ? 'pp-host-q-on' : ''}`}><span className="pp-host-q-letter">{h.name.replace(/[^A-Za-z0-9]/g, '').slice(0, 1).toUpperCase() || 'Q'}</span><i className={h.online ? 'pp-dot-on' : 'pp-dot-off'} /></span>
                  <div className="pp-host-item-main">
                    <div className="pp-host-item-name">{h.name}</div>
                    <div className="pp-muted pp-small">{h.diskGb ? `${h.diskUsedGb}/${h.diskGb}GB · ` : ''}{total} 台 · 运行 {run}</div>
                  </div>
                </button>
              )
            })}
          </div>
        </aside>

        <div className="pp-phones-main">
          <div className="pp-toolbar">
            <Space wrap>
              <Segmented value={view} onChange={(v) => setView(v as 'cards' | 'list')} options={[{ value: 'cards', icon: <AppstoreOutlined />, label: '画面' }, { value: 'list', icon: <UnorderedListOutlined />, label: '列表' }]} />
              <Select style={{ width: 150 }} value={groupId} onChange={setGroupId} options={[{ value: 'all', label: '全部分组' }, ...ws.groups.map((g) => ({ value: `${g.serverId ?? 'local'}|${g.id}`, label: ws.servers.length ? `${g.name}（${ws.serverName(g.serverId)}）` : g.name }))]} />
              <Button onClick={() => setManagingGroups(true)}>分组管理</Button>
              <Input allowClear prefix={<SearchOutlined />} placeholder="名称 / 机型" value={query} onChange={(e) => setQuery(e.target.value)} style={{ width: 180 }} />
              {view === 'cards' && <span className="pp-slot-slider"><span className="pp-muted pp-small">窗口大小</span><Slider min={170} max={360} value={slotW} onChange={setSlotW} style={{ width: 96 }} tooltip={{ open: false }} /></span>}
              {view === 'cards' && <span className="pp-slot-slider"><span className="pp-muted pp-small">投屏大小</span><Slider min={55} max={100} value={screenPct} onChange={setScreenPct} style={{ width: 96 }} tooltip={{ open: false }} /></span>}
            </Space>
            <Space wrap>
              {selected.length > 0 && <span className="pp-muted">已选 {selected.length} 台</span>}
              <Dropdown disabled={!selected.length || !!batchBusy} menu={{ items: batch }}><Button loading={!!batchBusy}>批量操作 <DownOutlined /></Button></Dropdown>
              {batchBusy && <span className="pp-muted">{batchBusy}</span>}
              <input ref={apkInput} type="file" accept=".apk" hidden onChange={(e) => { void runBatch('apk', e.target.files?.[0]); e.target.value = '' }} />
              <input ref={fileInput} type="file" hidden onChange={(e) => { void runBatch('file', e.target.files?.[0]); e.target.value = '' }} />
              <Button type="primary" icon={<PlusOutlined />} onClick={() => { const hid = hostId === 'all' ? undefined : hostId; const n = ws.phones.filter((p) => !hid || p.hostId === hid).length + 1; addPhoneOn(hid, `手机-${n}`) }}>新建云手机</Button>
            </Space>
          </div>

          {view === 'list' ? (
            list.length === 0 ? <Empty description="没有符合条件的云手机" /> : (
              <DataTable<Phone>
                tableId="phones"
                rowKey="id"
                dataSource={list}
                pagination={false}
                scroll={{ x: 1450 }}
                rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys as string[]) }}
                onRow={(p) => ({ onDoubleClick: () => view_(p) })}
                columns={[
                  { key: 'name', title: '名称', dataIndex: 'name', width: 150, render: (name: string, p) => <a onClick={() => view_(p)}>{name}</a> },
                  { key: 'status', title: '状态', width: 90, sortValue: (p) => p.status, render: (_, p) => <StatusDot status={p.status} /> },
                  { key: 'model', title: '机型', width: 160, sortValue: (p) => String(ws.modelOf(p)?.values.marketName ?? ''), render: (_, p) => <Space size={4}>{String(ws.modelOf(p)?.values.marketName ?? '—')}{p.gapps && <Tag color="blue" className="pp-small">谷歌</Tag>}</Space> },
                  { key: 'android', title: '安卓', width: 90, sortValue: (p) => p.androidVersion, render: (_, p) => `Android ${p.androidVersion}` },
                  { key: 'proxy', title: '代理', width: 150, sortValue: (p) => ws.proxyOf(p)?.name ?? '', render: (_, p) => <ProxyCell proxy={ws.proxyOf(p)} /> },
                  { key: 'exitip', title: '出口 IP', width: 130, sortValue: (p) => ws.proxyOf(p)?.check?.exitIp ?? '', render: (_, p) => { const ip = ws.proxyOf(p)?.check?.exitIp; return ip ? <span className="pp-small">{ip}</span> : <span className="pp-muted">—</span> } },
                  { key: 'net', title: '上网', width: 90, sortValue: (p) => (p.status !== 'running' || !p.net ? -1 : p.net.ok ? 1 : 0), render: (_, p) => (p.status !== 'running' ? <span className="pp-muted">—</span> : !p.net ? <span className="pp-muted">—</span> : p.net.ok ? <Tag color="success" className="pp-small">正常</Tag> : <Tooltip title={p.net.error}><Tag color="error" className="pp-small">上不了网</Tag></Tooltip>) },
                  { key: 'group', title: '分组', width: 110, sortValue: (p) => ws.groupOf(p)?.name ?? '', render: (_, p) => ws.groupOf(p)?.name ?? '未分组' },
                  { key: 'usage', title: '占用', width: 150, sortValue: (p) => p.usage?.cpuPercent ?? -1, render: (_, p) => (p.usage ? usageText(p.usage) : '—') },
                  { key: 'host', title: '机器', width: 140, sortValue: (p) => ws.hostOf(p)?.name ?? '', render: (_, p) => ws.hostOf(p)?.name },
                  { key: 'check', title: '检查', width: 90, sortValue: (p) => ws.issuesOf(p).length, render: (_, p) => <IssueBadge issues={ws.issuesOf(p)} /> },
                  { key: 'actions', title: '', width: 150, resizable: false, reorderable: false, render: (_, p) => <Space size={4}>{p.status === 'running' || p.status === 'starting' ? <><Button size="small" onClick={() => view_(p)}>打开</Button><Button size="small" onClick={() => ws.setStatus([p.id], 'stopped')}>关机</Button></> : <Button size="small" type="primary" disabled={blocksStart(ws.issuesOf(p))} onClick={() => start([p.id])}>开机</Button>}<Button size="small" onClick={() => edit(p)}>编辑</Button></Space> }
                ]}
              />
            )
          ) : shownHosts.length === 0 ? (
            <Empty description="没有在线的机器" />
          ) : (
            <div className="pp-host-sections">{shownHosts.map(renderHostSection)}</div>
          )}
        </div>
      </div>

      <Modal open={!!batchDone} title={batchDone?.title} footer={null} onCancel={() => setBatchDone(undefined)}>
        <Table size="small" pagination={false} rowKey="id" dataSource={batchDone?.results ?? []} columns={[
          { title: '手机', render: (_, r: BatchResult) => ws.phones.find((p) => p.id === r.id)?.name ?? r.id },
          { title: '结果', render: (_, r: BatchResult) => (r.ok ? <Tag color="success">成功</Tag> : <Tag color="error">失败</Tag>) },
          { title: '说明', render: (_, r: BatchResult) => r.error ?? r.path ?? '' }
        ]} />
      </Modal>
      <PhoneDrawer phone={open} mode={openMode} onEdit={() => setOpenMode('edit')} onClose={() => setOpen(undefined)} />
      <GroupsModal open={managingGroups} onClose={() => setManagingGroups(false)} />
      <NewPhoneModal open={creating} presetHostId={presetHost} presetName={presetName} onClose={() => setCreating(false)} />
      <AddServerModal open={addingDevice} onClose={() => setAddingDevice(false)} />
    </div>
  )
}

/** 「CPU 12% · 内存 0.9 / 3 GB」 */
function usageText(u: NonNullable<Phone['usage']>): string {
  const gb = (mb: number) => (mb / 1024).toFixed(mb >= 10240 ? 0 : 1)
  return `CPU ${Math.round(u.cpuPercent)}% · 内存 ${gb(u.memMB)} / ${gb(u.memLimitMB)} GB`
}
