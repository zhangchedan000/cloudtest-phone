import { CloudServerOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons'
import { Alert, App, Button, Card, Descriptions, Form, Input, Modal, Progress, Space, Tag } from 'antd'
import { useState } from 'react'
import { api, LOCAL } from '@/data/api'
import { useWorkspace } from '@/data/store'
import { TrashCard } from './TrashCard'

/**
 * 机器：每台机器上跑一个管家程序，一个页面管所有机器（2026-11-15 加多台服务器）。
 * 打开管理页的这台是「本机」；别的服务器在这里添加（地址 + 口令），添加后它的手机、代理、分组都会出现在页面上。
 */
export function HostsPage() {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [adding, setAdding] = useState(false)
  const [busy, setBusy] = useState(false)
  const [form] = Form.useForm<{ name?: string; url: string; token: string }>()
  const live = ws.mode === 'live'

  const add = async (v: { name?: string; url: string; token: string }) => {
    setBusy(true)
    try {
      const sv = await api.addServer(v)
      message.success(`已添加「${sv.name}」`)
      setAdding(false)
      form.resetFields()
      await ws.refresh()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const rename = (sid: string, old: string) => {
    let name = old
    Modal.confirm({
      title: '服务器改名',
      content: <Input defaultValue={old} onChange={(e) => { name = e.target.value }} />,
      onOk: async () => { await api.updateServer(sid, { name }); await ws.refresh() }
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
        <Button type="primary" icon={<PlusOutlined />} disabled={!live} onClick={() => setAdding(true)}>添加服务器</Button>
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
                {remote && <Button size="small" type="text" icon={<EditOutlined />} title="改名" onClick={() => rename(sid, h.name)} />}
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
                  { label: '云手机', children: `${phones.length} 台，运行中 ${running} / 同时最多 ${h.maxRunning}` }
                ]} />
                <div className="pp-meter"><span>内存</span><Progress percent={Math.round((h.memUsedGb / h.memGb) * 100)} format={() => `${h.memUsedGb} / ${h.memGb} GB`} /></div>
                <div className="pp-meter"><span>硬盘</span><Progress percent={Math.round((h.diskUsedGb / h.diskGb) * 100)} format={() => `${h.diskUsedGb} / ${h.diskGb} GB`} /></div>
              </>}
            </Card>
          )
        })}
      </div>
      {ws.hosts.filter((h) => h.online).map((h) => (
        <TrashCard key={`trash-${h.id}`} serverId={h.serverId ?? LOCAL} title={ws.hosts.length > 1 ? h.name : undefined} />
      ))}

      <Modal open={adding} title="添加服务器" okText="连接并添加" confirmLoading={busy} onOk={() => form.submit()} onCancel={() => setAdding(false)} destroyOnHidden>
        <Form form={form} layout="vertical" onFinish={(v) => void add(v)}>
          <Form.Item label="地址" name="url" rules={[{ required: true, message: '填那台服务器管理页的地址' }]} extra="那台服务器管理页的网址，例如 https://phone2.example.com（要用 HTTPS 域名，和这台一样经反代打开）">
            <Input placeholder="https://" />
          </Form.Item>
          <Form.Item label="口令" name="token" rules={[{ required: true, message: '填那台的口令' }]} extra="那台服务器 /etc/prism-phone/agent.env 里的 PP_TOKEN；没设口令的服务器不能添加（不安全）">
            <Input.Password />
          </Form.Item>
          <Form.Item label="名称（可不填）" name="name" extra="不填就用那台服务器自己的机器名">
            <Input placeholder="例如：美国板子 1" />
          </Form.Item>
        </Form>
        <p className="pp-muted pp-small">添加时会先用口令连一下那台，地址或口令不对会直接提示。</p>
      </Modal>
    </div>
  )
}
