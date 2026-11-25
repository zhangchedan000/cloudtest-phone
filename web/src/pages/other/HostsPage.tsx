import { CloudServerOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons'
import { Alert, App, Button, Card, Descriptions, Input, Modal, Progress, Space, Switch, Tag } from 'antd'
import { useState, Fragment } from 'react'
import { api, LOCAL } from '@/data/api'
import { useWorkspace } from '@/data/store'
import { AddServerModal } from './AddServerModal'
import { TrashCard } from './TrashCard'
import { BackupsCard } from './BackupsCard'

/**
 * 机器：每台机器上跑一个管家程序，一个页面管所有机器（2026-11-15 加多台服务器）。
 * 打开管理页的这台是「本机」；别的服务器在这里添加（地址 + 口令），添加后它的手机、代理、分组都会出现在页面上。
 */
export function HostsPage() {
  const ws = useWorkspace()
  const [adding, setAdding] = useState(false)
  const live = ws.mode === 'live'

  // 改名：本机（LOCAL）存到那台管家自己身上（PUT /api/host），别的服务器改这个页面里的显示名。
  const rename = (sid: string, old: string, remote: boolean) => {
    let name = old
    Modal.confirm({
      title: '机器改名',
      content: <Input defaultValue={old} maxLength={40} onChange={(e) => { name = e.target.value }} />,
      onOk: async () => {
        const n = name.trim()
        if (!n) return
        if (remote) await api.updateServer(sid, { name: n })
        else await api.renameHost(n, sid)
        await ws.refresh()
      }
    })
  }
  const remove = (sid: string, name: string) => Modal.confirm({
    title: `从列表里去掉「${name}」？`,
    content: '只是这个页面不再显示它，那台服务器上的云手机照常运行、数据不动，以后可以再添加回来。',
    okButtonProps: { danger: true },
    onOk: async () => { await api.deleteServer(sid); await ws.refresh() }
  })

  return (
    <div className="pp-page">
      <div className="pp-toolbar">
        <span className="pp-muted">一台机器（服务器）上跑一个管家程序。添加别的服务器后，它的手机、代理、分组都会出现在这个页面里，画面和操作直接连那台，不经过这台中转。</span>
        <Button type="primary" icon={<PlusOutlined />} disabled={!live} onClick={() => setAdding(true)}>添加设备</Button>
      </div>
      <div className="pp-grid pp-grid-wide">
        {ws.hosts.map((h) => {
          const phones = ws.phones.filter((p) => p.hostId === h.id)
          const running = phones.filter((p) => p.status === 'running').length
          const sid = h.serverId ?? LOCAL
          const remote = sid !== LOCAL
          return (
            <Card
              key={h.id}
              size="small"
              title={<Space><CloudServerOutlined />{h.name}{!remote && ws.servers.length > 0 && <Tag>本机</Tag>}</Space>}
              extra={<Space size={4}>
                {h.online ? <Tag color="green">在线</Tag> : <Tag color="red">连不上</Tag>}
                <Button size="small" type="text" icon={<EditOutlined />} title="改名" disabled={!live || (!remote && !h.online)} onClick={() => rename(sid, h.name, remote)} />
                {remote && <Button size="small" type="text" danger icon={<DeleteOutlined />} title="从列表去掉" onClick={() => remove(sid, h.name)} />}
              </Space>}
            >
              {!h.online && <Alert type="error" showIcon className="pp-alert" title="连不上这台服务器" description={`${h.url ?? ''}：${h.error ?? ''}。检查地址、口令（服务器上 /etc/prism-phone/agent.env 的 PP_TOKEN），以及那台的管家程序在不在运行。`} />}
              {h.online && h.gpuFamily === 'software' && <Alert type="info" showIcon className="pp-alert" title="开发机：没有显卡（软件渲染），只用于开发测试，不要登录真实账号。" />}
              {h.online && <>
                <Descriptions column={1} size="small" items={[
                  ...(remote ? [{ label: '地址', children: h.url }] : []),
                  { label: '位置', children: h.location },
                  { label: '架构 / 内核', children: `${h.arch} · ${h.kernel}` },
                  { label: '显卡', children: h.gpu },
                  { label: '云手机', children: `${phones.length} 台，运行中 ${running} / 同时最多 ${h.maxRunning}` },
                  ...(h.oled ? [{ label: '正面小屏', children: <OledSwitch sid={sid} on={h.oledOn ?? false} /> }] : [])
                ]} />
                <div className="pp-meter"><span>内存</span><Progress percent={Math.round((h.memUsedGb / h.memGb) * 100)} format={() => `${h.memUsedGb} / ${h.memGb} GB`} /></div>
                <div className="pp-meter"><span>硬盘</span><Progress percent={Math.round((h.diskUsedGb / h.diskGb) * 100)} format={() => `${h.diskUsedGb} / ${h.diskGb} GB`} /></div>
              </>}
            </Card>
          )
        })}
      </div>
      {ws.hosts.filter((h) => h.online).map((h) => (
        <Fragment key={h.id}>
          <BackupsCard serverId={h.serverId ?? LOCAL} title={ws.hosts.length > 1 ? h.name : undefined} />
          <TrashCard serverId={h.serverId ?? LOCAL} title={ws.hosts.length > 1 ? h.name : undefined} />
        </Fragment>
      ))}

      <AddServerModal open={adding} onClose={() => setAdding(false)} />
    </div>
  )
}

/** 正面小屏开关：点一下开/关这台机器正面的小屏。熄屏后机器照常跑，只是屏不亮（晚上想关就关）。 */
function OledSwitch({ sid, on }: { sid: string; on: boolean }) {
  const { message } = App.useApp()
  const [checked, setChecked] = useState(on)
  const [busy, setBusy] = useState(false)
  const toggle = async (next: boolean) => {
    setBusy(true)
    try {
      await api.setOled(next, sid)
      setChecked(next)
      message.success(next ? '小屏已点亮' : '小屏已熄灭')
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return <Switch checked={checked} loading={busy} onChange={(v) => void toggle(v)} checkedChildren="亮" unCheckedChildren="灭" />
}
