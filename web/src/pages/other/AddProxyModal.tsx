import { Form, Input, Modal, Segmented } from 'antd'
import { useState } from 'react'
import { useWorkspace } from '@/data/store'
import type { ProxyProtocol } from '@/data/types'

type Kind = 'socks5' | 'http' | 'https'
interface Fields { kind: Kind; host: string; port?: string; user?: string; pass?: string; note?: string }

/** 从「快速粘贴」里拆出代理：host:port:user:pass 或 socks5://user:pass@host:port 等。 */
function parsePaste(raw: string): Partial<Fields> | null {
  const s = raw.trim()
  if (!s) return null
  // 带协议的 URL 形式
  const u = s.match(/^(socks5|socks|http|https):\/\/(?:([^:@/]+)(?::([^@/]*))?@)?([^:/?#\s]+):(\d+)/i)
  if (u) return { kind: (u[1].toLowerCase() === 'socks' ? 'socks5' : u[1].toLowerCase()) as Kind, user: u[2], pass: u[3], host: u[4], port: u[5] }
  // host:port:user:pass 或 host:port
  const parts = s.split(/[:\s]+/).filter(Boolean)
  if (parts.length >= 2 && /^\d+$/.test(parts[1])) return { host: parts[0], port: parts[1], user: parts[2], pass: parts[3] }
  return null
}

/** 添加代理：上面「快速粘贴」自动拆解，下面手填；类型 HTTP/HTTPS/SOCKS5。 */
export function AddProxyModal({ open, onClose, serverId }: { open: boolean; onClose: () => void; serverId: string }) {
  const ws = useWorkspace()
  const [form] = Form.useForm<Fields>()
  const [paste, setPaste] = useState('')

  const onPaste = (v: string) => {
    setPaste(v)
    const p = parsePaste(v)
    if (p) form.setFieldsValue({ ...p, kind: (p.kind ?? form.getFieldValue('kind') ?? 'socks5') })
  }

  const save = (v: Fields) => {
    const auth = v.user ? `${encodeURIComponent(v.user)}${v.pass ? ':' + encodeURIComponent(v.pass) : ''}@` : ''
    const address = `${v.kind}://${auth}${v.host}${v.port ? ':' + v.port : ''}`
    const name = v.note?.trim() || `${v.kind.toUpperCase()} · ${v.host}`
    void ws.addProxy({ id: `p-${Date.now().toString(36)}`, name, protocol: v.kind as ProxyProtocol, address, note: v.note?.trim() || undefined, serverId })
    form.resetFields(); setPaste(''); onClose()
  }

  return (
    <Modal open={open} width={560} title="添加代理" okText="保存" onCancel={() => { form.resetFields(); setPaste(''); onClose() }} onOk={() => form.submit()} destroyOnHidden>
      <Form form={form} layout="vertical" initialValues={{ kind: 'socks5' }} onFinish={save}>
        <Form.Item label="快速粘贴" extra="支持 host:port:user:pass 或 socks5://user:pass@host:port，粘上去自动拆到下面">
          <Input.TextArea value={paste} onChange={(e) => onPaste(e.target.value)} autoSize={{ minRows: 2, maxRows: 4 }} placeholder={'host:port:user:pass\nsocks5://user:pass@host:port'} />
        </Form.Item>
        <Form.Item label="类型" name="kind"><Segmented options={[{ label: 'HTTP', value: 'http' }, { label: 'HTTPS', value: 'https' }, { label: 'SOCKS5', value: 'socks5' }]} /></Form.Item>
        <div className="pp-form-2col">
          <Form.Item label="代理地址" name="host" rules={[{ required: true, message: '填主机' }]}><Input placeholder="proxy.example.com" /></Form.Item>
          <Form.Item label="端口" name="port" rules={[{ required: true, message: '填端口' }]}><Input placeholder="1080" /></Form.Item>
          <Form.Item label="用户名" name="user"><Input placeholder="可不填" /></Form.Item>
          <Form.Item label="密码" name="pass"><Input.Password placeholder="可不填" /></Form.Item>
        </div>
        <Form.Item label="备注" name="note" extra="不填就用「类型 · 主机」当名字"><Input placeholder="例如：住宅 IP、BM 专用" /></Form.Item>
      </Form>
      <p className="pp-muted pp-small">保存后在列表里点「检测」测出口 IP、地区和延迟。vmess/vless/trojan/ss/hy2 等分享链接用「批量导入」。</p>
    </Modal>
  )
}
