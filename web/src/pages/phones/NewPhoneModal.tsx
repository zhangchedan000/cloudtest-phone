import { Alert, Checkbox, Form, Input, InputNumber, Modal, Select } from 'antd'
import { catalog } from '@/data/catalog'
import { checkPhone } from '@/data/consistency'
import { randomValue } from '@/data/random'
import { useWorkspace } from '@/data/store'
import type { FieldSetting, Phone } from '@/data/types'

interface Values { name: string; count: number; hostId: string; modelId: string; androidVersion: number; proxyId: string; groupId: string; gapps: boolean }

/** 新建云手机（照魔云腾创建云机布局：左边两栏表单 + 右边坑位图）。随机字段自动生成，时区/语言/位置默认跟随代理。 */
export function NewPhoneModal({ open, onClose, presetHostId }: { open: boolean; onClose: () => void; presetHostId?: string }) {
  const ws = useWorkspace()
  const [form] = Form.useForm<Values>()
  const hostId = Form.useWatch('hostId', form)
  const modelId = Form.useWatch('modelId', form)
  const androidVersion = Form.useWatch('androidVersion', form)
  const count = Form.useWatch('count', form) ?? 1
  const host = ws.hosts.find((h) => h.id === hostId)
  // 这台机器 + 选中的安卓版本有没有「谷歌服务」镜像
  const gappsOK = !!host?.gappsVersions?.includes(androidVersion)
  // 代理库、分组每台服务器各一份：只列选中机器所在服务器上的
  const same = <T extends { serverId?: string }>(x: T) => (x.serverId ?? 'local') === (host?.serverId ?? 'local')
  const model = ws.models.find((m) => m.id === modelId)
  const gpuWarn = host && model && host.gpuFamily !== 'software' && host.gpuFamily !== model.gpuFamily

  // 右边坑位图：这台机器已有的手机占满前面的坑，接下来 count 个高亮成「新建」
  const occupied = host ? ws.phones.filter((p) => p.hostId === host.id).length : 0
  const slotCount = host ? Math.max(host.maxRunning, occupied + count) : 0

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
    <Modal
      open={open}
      width={760}
      title={<div><div>新建云手机</div>{host && <div className="pp-muted pp-small" style={{ fontWeight: 400 }}>{host.name}{host.gpu ? ` · ${host.gpu}` : ''}</div>}</div>}
      onCancel={onClose}
      onOk={() => form.submit()}
      okText="创建"
      destroyOnHidden
    >
      <Form form={form} layout="vertical" onFinish={create} onValuesChange={(changed) => { if ('hostId' in changed) form.setFieldsValue({ proxyId: undefined, groupId: undefined }) }} initialValues={{ name: '手机-', count: 1, hostId: presetHostId ?? ws.hosts.find((h) => h.online)?.id ?? ws.hosts[0]?.id, modelId: ws.models[0]?.id, androidVersion: 14, gapps: true, groupId: ws.groups.filter(same)[0]?.id }}>
        <div className="pp-newphone">
          <div className="pp-newphone-form">
            <div className="pp-form-2col">
              <Form.Item label="名称" name="name" rules={[{ required: true }]}><Input placeholder="留空自动取名" /></Form.Item>
              <Form.Item label="数量" name="count"><InputNumber min={1} max={20} style={{ width: '100%' }} /></Form.Item>
              <Form.Item label="机型" name="modelId" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={ws.models.map((m) => ({ value: m.id, label: `${String(m.values.marketName)} · ${m.gpuFamily}${m.verified ? '' : ' · 待核实'}` }))} /></Form.Item>
              <Form.Item label="安卓版本（镜像）" name="androidVersion"><Select options={(host?.androidVersions?.length ? host.androidVersions : [14]).map((v) => ({ value: v, label: `Android ${v}` }))} /></Form.Item>
              <Form.Item label="代理" name="proxyId" rules={[{ required: true, message: '必须分配代理：没有代理禁止开机' }]}><Select options={ws.proxies.filter(same).map((p) => ({ value: p.id, label: p.name }))} /></Form.Item>
              <Form.Item label="分组" name="groupId"><Select allowClear options={ws.groups.filter(same).map((g) => ({ value: g.id, label: g.name }))} /></Form.Item>
            </div>
            {gpuWarn && <Alert type="error" showIcon title="这个机型的显卡和机器对不上，会露馅" className="pp-alert" />}
            <Form.Item name="gapps" valuePropName="checked" className="pp-newphone-gapps" extra={gappsOK ? '开机带谷歌服务（GMS / Play 商店），可登谷歌账号。镜像较大、开机稍慢。' : '这台机器没装当前安卓版本的谷歌服务镜像（在服务器上运行 host/build-gapps-image.sh 生成）。'}>
              <Checkbox disabled={!gappsOK}>带谷歌服务（GMS / Play 商店）</Checkbox>
            </Form.Item>
          </div>

          <aside className="pp-newphone-slots">
            <Form.Item label="机器" name="hostId" rules={[{ required: true }]}><Select options={ws.hosts.map((h) => ({ value: h.id, label: h.online ? h.name : `${h.name}（离线）`, disabled: !h.online }))} /></Form.Item>
            {host && (
              <>
                <div className="pp-newslot-head"><span>坑位</span><span className="pp-muted pp-small">已选 {count}</span></div>
                <div className="pp-newslot-grid">
                  {Array.from({ length: slotCount }, (_, i) => {
                    const n = i + 1
                    const state = n <= occupied ? 'occupied' : n <= occupied + count ? 'picked' : 'empty'
                    return <div key={n} className={`pp-newslot pp-newslot-${state}`}>{n}</div>
                  })}
                </div>
                <div className="pp-newslot-legend">
                  <span><i style={{ background: 'var(--fb-accent-soft)', border: '1px solid var(--fb-accent)' }} />新建</span>
                  <span><i style={{ background: 'var(--fb-chip)' }} />已用</span>
                  <span><i style={{ background: 'var(--fb-surface-muted)', border: '1px solid var(--fb-line)' }} />空坑位</span>
                </div>
                <div className="pp-muted pp-small" style={{ marginTop: 10 }}>这台机器已有 {occupied} 台，同时最多跑 {host.maxRunning} 台。</div>
              </>
            )}
          </aside>
        </div>
      </Form>
    </Modal>
  )
}
