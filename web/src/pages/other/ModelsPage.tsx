import { ImportOutlined, InboxOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import { Alert, App, Button, Descriptions, Input, Modal, Select, Space, Table, Tag, Tooltip, Upload, type UploadFile } from 'antd'
import { useMemo, useState } from 'react'
import { api, importModels, LOCAL } from '@/data/api'
import { fieldByKey, formatValue } from '@/data/catalog'
import { useWorkspace } from '@/data/store'
import type { DeviceModel } from '@/data/types'
import { ServerSelect } from '@/components/ServerSelect'

const SHOWN = ['brand', 'model', 'device', 'socModel', 'cpuParts', 'cpuMaxFreq', 'memTotal', 'resolution', 'refreshRate']
const SOURCE: Record<string, string> = { capture: '真机采集', mytos: '魔云腾包', dir: '目录' }

const str = (v: unknown) => (v === undefined || v === null ? '' : String(v))

/**
 * 机型库（2026-11-24 改成表格，照魔云腾客户端的机型列表做）：
 * 内置机型（程序里带的，profiles/models）+ 本地导入的（真机采集包 / 魔云腾机型包，存在 <数据目录>/models）。
 * 档位：一致档 = 真机显卡就是板子的 Mali-G610，深度检测也对得上；伪装档 = 显卡不同，只改了名字。
 */
export function ModelsPage() {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const live = ws.mode === 'live'
  const [query, setQuery] = useState('')
  const [android, setAndroid] = useState<number | undefined>()
  const [tier, setTier] = useState<string | undefined>()
  const [source, setSource] = useState<string | undefined>()
  const [importing, setImporting] = useState(false)
  const [busy, setBusy] = useState(false)
  const [files, setFiles] = useState<UploadFile[]>([])
  const [output, setOutput] = useState<string[]>([])
  const [serverId, setServerId] = useState(LOCAL)

  const gpus = useMemo(() => new Set(ws.hosts.filter((h) => h.online && h.gpuFamily !== 'software').map((h) => h.gpuFamily)), [ws.hosts])
  const versions = useMemo(() => [...new Set(ws.models.flatMap((m) => m.androidVersions))].sort((a, b) => a - b), [ws.models])
  const inUse = (m: DeviceModel) => ws.phones.filter((p) => p.modelId === m.id).length
  const tierOf = (m: DeviceModel) => m.tier ?? 'consistent'

  const list = ws.models.filter((m) => {
    if (android && !m.androidVersions.includes(android)) return false
    if (tier && tierOf(m) !== tier) return false
    if (source === 'builtin' && m.local) return false
    if (source === 'local' && !m.local) return false
    const q = query.trim().toLowerCase()
    if (!q) return true
    return [m.id, m.values.marketName, m.values.model, m.values.brand, m.values.socModel, m.gpuFamily].some((v) => str(v).toLowerCase().includes(q))
  })

  const reload = async () => {
    setBusy(true)
    try {
      const r = await api.reloadModels(serverId)
      await ws.refreshModels()
      message.success(`机型库：内置 ${r.builtin} 款、本地 ${r.local} 款`)
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const doImport = async () => {
    setBusy(true)
    setOutput([])
    try {
      const r = await importModels(files.map((f) => f.originFileObj as File), serverId)
      setOutput(r.output)
      setFiles([])
      await ws.refreshModels()
      message.success(`导入完成：现在内置 ${r.builtin} 款、本地 ${r.local} 款`)
    } catch (error) {
      setOutput((error as Error).message.split('\n'))
      message.error('导入失败，看下面的输出')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="pp-page">
      <div className="pp-toolbar">
        <Space wrap>
          <Input allowClear prefix={<SearchOutlined />} placeholder="搜机型 / 型号 / 芯片" style={{ width: 220 }} value={query} onChange={(e) => setQuery(e.target.value)} />
          <Select allowClear placeholder="安卓版本" style={{ width: 120 }} value={android} onChange={setAndroid} options={versions.map((v) => ({ value: v, label: `Android ${v}` }))} />
          <Select allowClear placeholder="档位" style={{ width: 110 }} value={tier} onChange={setTier} options={[{ value: 'consistent', label: '一致档' }, { value: 'disguised', label: '伪装档' }]} />
          <Select allowClear placeholder="来源" style={{ width: 110 }} value={source} onChange={setSource} options={[{ value: 'builtin', label: '内置' }, { value: 'local', label: '导入的' }]} />
          <span className="pp-muted">共 {ws.models.length} 款，显示 {list.length} 款</span>
        </Space>
        <Space>
          <ServerSelect value={serverId} onChange={setServerId} />
          <Tooltip title="手动往 <数据目录>/models 放了机型档案后，点这里让管家重新读"><Button icon={<ReloadOutlined />} disabled={!live} loading={busy && !importing} onClick={() => void reload()}>重新加载</Button></Tooltip>
          <Button type="primary" icon={<ImportOutlined />} disabled={!live} onClick={() => { setOutput([]); setImporting(true) }}>导入机型</Button>
        </Space>
      </div>

      <Table<DeviceModel>
        rowKey="id"
        size="small"
        dataSource={list}
        pagination={{ pageSize: 50, hideOnSinglePage: true }}
        expandable={{ expandedRowRender: (m) => <ModelDetail model={m} /> }}
        columns={[
          {
            title: '机型', key: 'name', sorter: (a, b) => str(a.values.marketName).localeCompare(str(b.values.marketName)),
            render: (_, m) => <div><div>{str(m.values.marketName) || m.id}</div><div className="pp-muted pp-small pp-mono">{str(m.values.model)}{m.values.device ? ` · ${str(m.values.device)}` : ''}</div></div>
          },
          {
            title: '芯片 / 显卡', key: 'soc',
            render: (_, m) => <div><div className="pp-small">{str(m.values.socModel) || '—'}</div><Tag color={gpus.has(m.gpuFamily) ? 'blue' : 'default'}>{m.gpuFamily}</Tag></div>
          },
          {
            title: '档位', key: 'tier', filters: undefined,
            render: (_, m) => tierOf(m) === 'consistent'
              ? <Tooltip title="真机显卡就是板子的，显卡测试、跑分都对得上"><Tag color="green">一致档</Tag></Tooltip>
              : <Tooltip title={`真机显卡是 ${m.gpuFamily}，板子不是：只改了名字，深度检测（渲染结果、Vulkan、跑分）会露馅`}><Tag color="orange">伪装档</Tag></Tooltip>
          },
          {
            title: 'CPU', key: 'cpu',
            render: (_, m) => m.cpuMismatch
              ? <Tooltip title={m.cpuMismatch}><Tag color="red">指令集不同</Tag></Tooltip>
              : m.cpuTree
                ? <Tooltip title={`整个 CPU 目录换成真机的（${m.cpuTree}）`}><Tag color="green">真机目录</Tag></Tooltip>
                : <Tooltip title="还没有这款的真机 CPU 目录：/sys/devices/system/cpu 是板子的（RK3588）"><Tag>板子的</Tag></Tooltip>
          },
          {
            title: '安卓', key: 'android',
            render: (_, m) => <Space size={2} wrap>{m.androidVersions.map((v) => (
              <Tooltip key={v} title={m.fingerprints?.[String(v)] ? `真机指纹：${m.fingerprints[String(v)]}` : '这个版本没有真机指纹：开机后指纹带 eng.* 字样'}>
                <Tag color={m.fingerprints?.[String(v)] ? 'default' : 'warning'}>{v}</Tag>
              </Tooltip>
            ))}</Space>
          },
          { title: '地区', key: 'market', render: (_, m) => (m.market === 'CN' ? '国行' : m.market ? '海外版' : '—') },
          {
            title: '来源', key: 'source',
            render: (_, m) => (m.local ? <Tag color="purple">导入 · {SOURCE[m.source ?? ''] ?? '本地'}</Tag> : <Tag>内置</Tag>)
          },
          { title: '核实', key: 'verified', render: (_, m) => (m.verified ? <Tag color="green">已核实</Tag> : <Tooltip title={m.verifyNote}><Tag color="orange">待核实</Tag></Tooltip>) },
          { title: '在用', key: 'use', align: 'right', sorter: (a, b) => inUse(a) - inUse(b), render: (_, m) => inUse(m) || <span className="pp-muted">0</span> }
        ]}
      />

      <Modal
        open={importing}
        title="导入机型"
        width={640}
        okText="导入"
        okButtonProps={{ disabled: !files.length, loading: busy }}
        cancelText="关闭"
        onOk={() => void doImport()}
        onCancel={() => { setImporting(false); setFiles([]) }}
      >
        <Alert type="info" showIcon className="pp-alert" title="支持两种机型包" description={<>
          <div>· 真机采集包：电脑连上真手机，运行 <span className="pp-mono">host/models/collect-model.py</span> 得到的 zip（录得最全，推荐）</div>
          <div>· 魔云腾机型包：魔云腾客户端下载的机型 zip</div>
          <div>显卡是 {[...gpus].join(' / ') || '板子显卡'} 的进「一致档」，其他的进「伪装档」。同一款再导一次会覆盖。</div>
        </>} />
        <Upload.Dragger multiple accept=".zip" fileList={files} beforeUpload={() => false} onChange={({ fileList }) => setFiles(fileList)}>
          <p className="ant-upload-drag-icon"><InboxOutlined /></p>
          <p>把机型包（.zip）拖到这里，或点这里选，可以一次多个</p>
        </Upload.Dragger>
        {output.length > 0 && <pre className="pp-log-text" style={{ marginTop: 12, maxHeight: 260, overflow: 'auto' }}>{output.join('\n')}</pre>}
      </Modal>
    </div>
  )
}

function ModelDetail({ model: m }: { model: DeviceModel }) {
  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      {m.cpuMismatch && <Alert type="warning" showIcon title={m.cpuMismatch} />}
      <Descriptions column={{ xs: 1, md: 2, xl: 3 }} size="small" items={[
        ...SHOWN.flatMap((key) => {
          const field = fieldByKey(key)
          return field && m.values[key] !== undefined ? [{ label: field.label, children: <span className="pp-mono pp-small">{formatValue(m.values[key], field)}</span> }] : []
        }),
        { label: 'IMEI 型号码', children: m.imeiTacs?.length ? <span className="pp-mono pp-small">{m.imeiTacs.length} 个（{m.imeiTacs.slice(0, 3).join('、')}{m.imeiTacs.length > 3 ? '…' : ''}）</span> : <span className="pp-muted">没有：IMEI 用随机的 35 开头</span> },
        { label: '机型 ID', children: <span className="pp-mono pp-small">{m.id}</span> },
        ...(m.verifyNote ? [{ label: '说明', children: <span className="pp-small">{m.verifyNote}</span> }] : [])
      ]} />
      {m.fingerprints && Object.keys(m.fingerprints).length > 0 && (
        <Descriptions column={1} size="small" items={Object.entries(m.fingerprints).sort().map(([v, fp]) => ({ label: `Android ${v} 指纹`, children: <span className="pp-mono pp-small">{fp}</span> }))} />
      )}
    </Space>
  )
}
