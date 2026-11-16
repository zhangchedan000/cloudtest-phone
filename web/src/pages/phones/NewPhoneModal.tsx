import { Alert, Checkbox, Form, Input, InputNumber, Modal, Select } from 'antd'
import { catalog } from '@/data/catalog'
import { checkPhone } from '@/data/consistency'
import { randomValue } from '@/data/random'
import { useWorkspace } from '@/data/store'
import type { FieldSetting, Phone } from '@/data/types'

interface Values { name: string; count: number; hostId: string; modelId: string; androidVersion: number; proxyId: string; groupId: string; gapps: boolean }

/** 新建云手机：选机器、机型、代理、分组；随机字段自动生成，时区/语言/位置默认跟随代理。 */
export function NewPhoneModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const ws = useWorkspace()
  const [form] = Form.useForm<Values>()
  const hostId = Form.useWatch('hostId', form)
  const modelId = Form.useWatch('modelId', form)
  const androidVersion = Form.useWatch('androidVersion', form)
  const host = ws.hosts.find((h) => h.id === hostId)
  // 这台机器 + 选中的安卓版本有没有「谷歌服务」镜像
  const gappsOK = !!host?.gappsVersions?.includes(androidVersion)
  // 代理库、分组每台服务器各一份：只列选中机器所在服务器上的
  const same = <T extends { serverId?: string }>(x: T) => (x.serverId ?? 'local') === (host?.serverId ?? 'local')
  const model = ws.models.find((m) => m.id === modelId)
  const gpuWarn = host && model && host.gpuFamily !== 'software' && host.gpuFamily !== model.gpuFamily

  const create = (v: Values) => {
    for (let i = 0; i < v.count; i++) {
      const fields: Record<string, FieldSetting> = {}
      for (const field of catalog.fields) {
        const mode = field.modes.includes('proxy') ? 'proxy' : field.modes[0]
        fields[field.key] = { mode, value: mode === 'random' ? randomValue(field, model) : undefined }
      }
      const phone: Phone = {
        id: `ph-${Date.now().toString(36)}-${i}`, name: v.count > 1 ? `${v.name}-${String(i + 1).padStart(2, '0')}` : v.name,
        hostId: v.hostId, status: 'stopped', androidVersion: v.androidVersion, modelId: v.modelId, proxyId: v.proxyId, groupId: v.groupId,
        gapps: v.gapps && gappsOK, createdAt: new Date().toISOString(), fields
      }
      checkPhone({ phone, model, proxy: ws.proxies.filter(same).find((p) => p.id === v.proxyId), host, others: ws.phones.filter(same) })
      ws.addPhone(phone)
    }
    form.resetFields()
    onClose()
  }

  return (
    <Modal open={open} title="新建云手机" onCancel={onClose} onOk={() => form.submit()} okText="创建" destroyOnHidden>
      <Form form={form} layout="vertical" onFinish={create} onValuesChange={(changed) => { if ('hostId' in changed) form.setFieldsValue({ proxyId: undefined, groupId: undefined }) }} initialValues={{ name: '手机-', count: 1, hostId: ws.hosts[0]?.id, modelId: ws.models[0]?.id, androidVersion: 14, gapps: true, groupId: ws.groups.filter(same)[0]?.id }}>
        <Form.Item label="名称" name="name" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item label="数量" name="count"><InputNumber min={1} max={20} /></Form.Item>
        <Form.Item label="所在机器" name="hostId" rules={[{ required: true }]}><Select options={ws.hosts.map((h) => ({ value: h.id, label: `${h.name}（${h.gpu}）${h.online ? '' : ' · 离线'}`, disabled: !h.online }))} /></Form.Item>
        <Form.Item label="机型" name="modelId" rules={[{ required: true }]}><Select options={ws.models.map((m) => ({ value: m.id, label: `${String(m.values.marketName)} · ${m.gpuFamily}${m.verified ? '' : ' · 待核实'}` }))} /></Form.Item>
        {gpuWarn && <Alert type="error" showIcon title="这个机型的显卡和机器对不上，会露馅" className="pp-alert" />}
        <Form.Item label="安卓版本" name="androidVersion"><Select options={(host?.androidVersions?.length ? host.androidVersions : [14]).map((v) => ({ value: v, label: `Android ${v}` }))} /></Form.Item>
        <Form.Item name="gapps" valuePropName="checked" extra={gappsOK ? '开机带谷歌服务（GMS / Play 商店），可登谷歌账号。镜像较大、开机稍慢。' : '这台机器没装当前安卓版本的谷歌服务镜像（在服务器上运行 host/build-gapps-image.sh 生成）。'}>
          <Checkbox disabled={!gappsOK}>带谷歌服务（GMS / Play 商店）</Checkbox>
        </Form.Item>
        <Form.Item label="代理" name="proxyId" rules={[{ required: true, message: '必须分配代理：没有代理禁止开机' }]}><Select options={ws.proxies.filter(same).map((p) => ({ value: p.id, label: p.name }))} /></Form.Item>
        <Form.Item label="分组" name="groupId"><Select allowClear options={ws.groups.filter(same).map((g) => ({ value: g.id, label: g.name }))} /></Form.Item>
      </Form>
    </Modal>
  )
}
