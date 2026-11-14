import { CloudDownloadOutlined, DeleteOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { App, Button, Form, Input, Modal, Select, Space, Table, Tag } from 'antd'
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
  const { message } = App.useApp()
  const [checking, setChecking] = useState<string[]>([])
  const [importing, setImporting] = useState(false)
  const [importBusy, setImportBusy] = useState(false)
  const [importForm] = Form.useForm<{ url?: string; text?: string; note?: string }>()
  const doImport = async (v: { url?: string; text?: string; note?: string }) => {
    if (!v.url?.trim() && !v.text?.trim()) { message.warning('填订阅地址，或者粘贴订阅内容'); return }
    setImportBusy(true)
    try {
      const r = await ws.importProxies(v)
      const extra = [r.duplicates ? `${r.duplicates} 个已存在跳过` : '', r.skipped?.length ? `${r.skipped.length} 个不支持跳过` : ''].filter(Boolean).join('，')
      message.success(`导入了 ${r.added} 个节点${extra ? `（${extra}）` : ''}`)
      setImporting(false)
      importForm.resetFields()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setImportBusy(false)
    }
  }
  const check = async (ids: string[]) => {
    setChecking(ids)
    try { await ws.checkProxies(ids) } finally { setChecking([]) }
  }

  return (
    <div className="pp-page">
      <div className="pp-toolbar">
        <span className="pp-muted">代理不通时，用它的云手机禁止开机，不会走本机网络。HTTP / HTTPS / SOCKS5 随时能检测；vless、trojan、Clash 节点等要开网络模块才能检测。</span>
        <Space>
          <Button icon={<ReloadOutlined />} loading={checking.length > 1} onClick={() => void check(ws.proxies.filter((p) => p.id !== 'direct-dev').map((p) => p.id))}>全部检测</Button>
          <Button icon={<CloudDownloadOutlined />} onClick={() => setImporting(true)}>导入订阅</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>添加代理</Button>
        </Space>
      </div>
      <Table<Proxy>
        rowKey="id"
        dataSource={ws.proxies.filter((p) => p.id !== 'direct-dev')}
        pagination={false}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: '协议', dataIndex: 'protocol', render: (v: string, p) => <Tag>{v === 'subscription' ? '订阅链接' : v === 'clash' ? `Clash 节点 · ${clashType(p.address)}` : v}</Tag> },
          { title: '地址', dataIndex: 'address', render: (v: string, p) => <span className="pp-mono pp-small">{p.protocol === 'clash' ? clashServer(v) : v.length > 60 ? `${v.slice(0, 60)}…` : v}</span> },
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
      <Modal open={importing} title="导入订阅" okText="导入" confirmLoading={importBusy} onCancel={() => setImporting(false)} onOk={() => importForm.submit()} destroyOnHidden>
        <p className="pp-muted pp-small">订阅里的节点会拆成代理库里的一条条代理，每台云手机固定用其中一个。支持 Clash 订阅和分享链接列表；已经有的节点自动跳过。</p>
        <Form form={importForm} layout="vertical" onFinish={(v) => void doImport(v)}>
          <Form.Item label="订阅地址" name="url" extra="由服务器去下载"><Input placeholder="https://…" /></Form.Item>
          <Form.Item label="或者粘贴订阅内容 / 多行分享链接" name="text"><Input.TextArea autoSize={{ minRows: 3, maxRows: 10 }} placeholder={'vless://…\ntrojan://…'} /></Form.Item>
          <Form.Item label="备注（可选）" name="note"><Input placeholder="例如：XX 机场 美国" /></Form.Item>
        </Form>
      </Modal>
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

/** Clash 节点的地址列存的是节点配置（JSON），表格里只显示类型和服务器。 */
function clashNode(address: string): { type?: string; server?: string; port?: number } {
  try { return JSON.parse(address) as { type?: string; server?: string; port?: number } } catch { return {} }
}
function clashType(address: string): string { return clashNode(address).type ?? '?' }
function clashServer(address: string): string { const n = clashNode(address); return n.server ? `${n.server}:${n.port ?? ''}` : '（节点配置）' }
