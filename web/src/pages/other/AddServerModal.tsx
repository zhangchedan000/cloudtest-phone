import { App, Form, Input, Modal } from 'antd'
import { useState } from 'react'
import { api } from '@/data/api'
import { useWorkspace } from '@/data/store'

/** 添加设备（服务器）：填地址 + 口令，连上后它的手机、代理、分组都会出现在页面上。机器页和云手机页共用。 */
export function AddServerModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [busy, setBusy] = useState(false)
  const [form] = Form.useForm<{ name?: string; url: string; token: string }>()

  const add = async (v: { name?: string; url: string; token: string }) => {
    setBusy(true)
    try {
      const sv = await api.addServer(v)
      message.success(`已添加「${sv.name}」`)
      onClose()
      form.resetFields()
      await ws.refresh()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal open={open} title="添加设备" okText="连接并添加" confirmLoading={busy} onOk={() => form.submit()} onCancel={onClose} destroyOnHidden>
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
      <p className="pp-muted pp-small">添加时会先用口令连一下那台，地址或口令不对会直接提示。以后上新盒子都在这儿加。</p>
    </Modal>
  )
}
