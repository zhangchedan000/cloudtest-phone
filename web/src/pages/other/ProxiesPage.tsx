import { DeleteOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { Button, Form, Input, Modal, Select, Space, Table, Tag } from 'antd'
import { useState } from 'react'

import { useWorkspace } from '@/data/store'
import type { Proxy, ProxyProtocol } from '@/data/types'

const PROTOCOLS: ProxyProtocol[] = ['socks5', 'http', 'https', 'ss', 'vmess', 'vless', 'trojan', 'hysteria2', 'subscription']

/** 代理库：每条代理只存一份，设备只引用。底层用 mihomo，协议都支持。 */
export function ProxiesPage() {
  const ws = useWorkspace()
  const [adding, setAdding] = useState(false)
  const [form] = Form.useForm<{ name: string; protocol: ProxyProtocol; address: string }>()
  const usage = (id: string) => ws.phones.filter((p) => p.proxyId === id).length
  const [checking, setChecking] = useState<string[]>([])
  const check = async (ids: string[]) => {
    setChecking(ids)
    try { await ws.checkProxies(ids) } finally { setChecking([]) }
  }

  return (
    <div className="pp-page">
      <div className="pp-toolbar">
        <span className="pp-muted">代理不通时，用它的云手机禁止开机，不会走本机网络。HTTP / HTTPS / SOCKS5 现在就能检测，其他协议要等网络模块。</span>
        <Space>
          <Button icon={<ReloadOutlined />} loading={checking.length > 1} onClick={() => void check(ws.proxies.filter((p) => p.id !== 'direct-dev').map((p) => p.id))}>全部检测</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>添加代理</Button>
        </Space>
      </div>
      <Table<Proxy>
        rowKey="id"
        dataSource={ws.proxies.filter((p) => p.id !== 'direct-dev')}
        pagination={false}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: '协议', dataIndex: 'protocol', render: (v: string) => <Tag>{v === 'subscription' ? 'Clash 订阅' : v}</Tag> },
          { title: '地址', dataIndex: 'address', render: (v: string) => <span className="pp-mono pp-small">{v}</span> },
          { title: '出口 IP', render: (_, p) => p.check?.exitIp ?? '—' },
          { title: '地区 / 时区', render: (_, p) => (p.check?.ok ? `${p.check.country} ${p.check.city} · ${p.check.timezone}` : '—') },
          { title: '延迟', render: (_, p) => (p.check?.latencyMs ? `${p.check.latencyMs} ms` : '—') },
          { title: 'UDP', render: (_, p) => (p.check?.ok ? (p.check.udp ? <Tag color="green">支持</Tag> : <Tag color="orange">不支持（屏蔽 QUIC）</Tag>) : '—') },
          { title: '状态', render: (_, p) => (!p.check ? <Tag>未检测</Tag> : p.check.ok ? <Tag color="green">可用</Tag> : <Tag color="red">{p.check.error}</Tag>) },
          { title: '使用中', render: (_, p) => `${usage(p.id)} 台` },
          {
            title: '操作', render: (_, p) => (
              <Space size={4}>
                <Button size="small" loading={checking.includes(p.id)} onClick={() => void check([p.id])}>检测</Button>
                <Button size="small" danger icon={<DeleteOutlined />} disabled={usage(p.id) > 0} title={usage(p.id) > 0 ? '还有设备在用，先换代理' : '删除'} onClick={() => Modal.confirm({ title: `删除代理「${p.name}」？`, okButtonProps: { danger: true }, onOk: () => ws.removeProxy(p.id) })} />
              </Space>
            )
          }
        ]}
      />
      <Modal open={adding} title="添加代理" onCancel={() => setAdding(false)} onOk={() => form.submit()} destroyOnHidden>
        <Form form={form} layout="vertical" initialValues={{ protocol: 'socks5' }} onFinish={(v) => { void ws.addProxy({ id: `p-${Date.now().toString(36)}`, ...v }); setAdding(false) }}>
          <Form.Item label="名称" name="name" rules={[{ required: true }]}><Input placeholder="例如：美国住宅 · 达拉斯" /></Form.Item>
          <Form.Item label="协议" name="protocol"><Select options={PROTOCOLS.map((p) => ({ value: p, label: p === 'subscription' ? 'Clash 订阅链接' : p }))} /></Form.Item>
          <Form.Item label="地址" name="address" rules={[{ required: true }]} extra="支持 socks5://用户:密码@主机:端口、分享链接（vmess:// vless:// trojan:// ss:// hy2://）、订阅链接"><Input.TextArea autoSize /></Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
