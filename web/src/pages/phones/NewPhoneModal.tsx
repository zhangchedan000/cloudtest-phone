import { Alert, Checkbox, Form, Input, InputNumber, Modal, Select } from 'antd'
import { api } from '@/data/api'
import { catalog } from '@/data/catalog'
import { checkPhone } from '@/data/consistency'
import { randomValue } from '@/data/random'
import { useWorkspace } from '@/data/store'
import type { FieldSetting, Phone } from '@/data/types'

interface Values { name: string; count: number; hostId: string; modelId: string; androidVersion: number; proxyId: string; groupId: string; gapps: boolean; region?: string }

/** 国家 / 地区（用于 GPS 定位，照魔云腾）：选了就把这台的虚拟定位设到该地，value = "纬度,经度"。空 = 不单独设（语言 / 时区本来就跟代理走）。 */
const REGIONS: { label: string; v: string }[] = [
  { label: '美国 · 洛杉矶', v: '34.0522,-118.2437' }, { label: '美国 · 纽约', v: '40.7128,-74.0060' },
  { label: '英国 · 伦敦', v: '51.5074,-0.1278' }, { label: '德国 · 法兰克福', v: '50.1109,8.6821' },
  { label: '法国 · 巴黎', v: '48.8566,2.3522' }, { label: '日本 · 东京', v: '35.6762,139.6503' },
  { label: '韩国 · 首尔', v: '37.5665,126.9780' }, { label: '新加坡', v: '1.3521,103.8198' },
  { label: '香港', v: '22.3193,114.1694' }, { label: '台湾 · 台北', v: '25.0330,121.5654' },
  { label: '中国 · 北京', v: '39.9042,116.4074' }, { label: '中国 · 上海', v: '31.2304,121.4737' },
  { label: '阿联酋 · 迪拜', v: '25.2048,55.2708' }, { label: '澳大利亚 · 悉尼', v: '-33.8688,151.2093' },
  { label: '加拿大 · 多伦多', v: '43.6532,-79.3832' }, { label: '泰国 · 曼谷', v: '13.7563,100.5018' }
]

/** 新建云手机（照魔云腾创建云机布局：左边两栏表单 + 右边坑位图）。随机字段自动生成，时区/语言/位置默认跟随代理。 */
export function NewPhoneModal({ open, onClose, presetHostId, presetName }: { open: boolean; onClose: () => void; presetHostId?: string; presetName?: string }) {
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
  // 安卓版本和机型联动：版本可选项 = 这台机器有镜像的版本；选机型时自动默认到「机型原生且有镜像」的最高版本。
  const availVers = host?.androidVersions?.length ? host.androidVersions : [14]
  const bestVer = (vs: number[] | undefined) => {
    const mv = (vs ?? []).filter((v) => availVers.includes(v))
    return (mv.length ? mv : availVers).slice().sort((a, b) => b - a)[0]
  }
  const verOffModel = !!(model?.androidVersions?.length && androidVersion && !model.androidVersions.includes(androidVersion))

  // 右边坑位图：这台机器已有的手机占满前面的坑，接下来 count 个高亮成「新建」。
  // 不按「同时最多」限制坑位数量——想建多少建多少；格子最少排 8 个、最多排 40 个好看。
  const occupied = host ? ws.phones.filter((p) => p.hostId === host.id).length : 0
  const slotCount = host ? Math.min(Math.max(occupied + count, 8), 40) : 0

  const create = async (v: Values) => {
    // 国家/地区：选了就把虚拟定位设到该地（建完这台之后设；语言/时区仍跟代理走）
    const ll = v.region ? v.region.split(',').map(Number) : null
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
      const created = await ws.addPhone(phone)
      if (created && ll && ll.length === 2 && !Number.isNaN(ll[0]) && !Number.isNaN(ll[1])) void api.setLocation(created.id, ll[0], ll[1]).catch(() => { /* 定位设置失败不挡创建 */ })
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
      <Form form={form} layout="vertical" onFinish={create} onValuesChange={(changed) => {
        if ('hostId' in changed) form.setFieldsValue({ proxyId: undefined, groupId: undefined })
        if ('modelId' in changed) { const v = bestVer(ws.models.find((m) => m.id === changed.modelId)?.androidVersions); if (v) form.setFieldValue('androidVersion', v) }
      }} initialValues={{ name: presetName ?? '手机-', count: 1, hostId: presetHostId ?? ws.hosts.find((h) => h.online)?.id ?? ws.hosts[0]?.id, modelId: ws.models[0]?.id, androidVersion: bestVer(ws.models[0]?.androidVersions) ?? 14, gapps: true, groupId: ws.groups.filter(same)[0]?.id }}>
        <div className="pp-newphone">
          <div className="pp-newphone-form">
            <div className="pp-form-2col">
              <Form.Item label="名称" name="name" rules={[{ required: true }]}><Input placeholder="留空自动取名" /></Form.Item>
              <Form.Item label="数量" name="count"><InputNumber min={1} max={20} style={{ width: '100%' }} /></Form.Item>
              <Form.Item label="机型" name="modelId" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={ws.models.map((m) => ({ value: m.id, label: `${String(m.values.marketName)} · ${m.gpuFamily}${m.verified ? '' : ' · 待核实'}` }))} /></Form.Item>
              <Form.Item label="安卓版本（镜像）" name="androidVersion" extra={verOffModel ? `这款机型出厂是 Android ${model?.androidVersions?.join('/')}，选更高版本相当于「升级后」，部分数据用兜底，原生版最稳。` : undefined}><Select options={availVers.map((v) => ({ value: v, label: `Android ${v}${model?.androidVersions?.includes(v) ? '（机型原生）' : ''}` }))} /></Form.Item>
              <Form.Item label="代理" name="proxyId" rules={[{ required: true, message: '必须分配代理：没有代理禁止开机' }]}><Select options={ws.proxies.filter(same).map((p) => ({ value: p.id, label: p.name }))} /></Form.Item>
              <Form.Item label="分组" name="groupId"><Select allowClear options={ws.groups.filter(same).map((g) => ({ value: g.id, label: g.name }))} /></Form.Item>
              <Form.Item label="国家/地区（GPS 定位）" name="region" tooltip="选了就把这台的虚拟定位设到该地区；语言、时区本来就跟着代理走"><Select allowClear showSearch optionFilterProp="label" placeholder="跟随代理（不单独设）" options={REGIONS.map((r) => ({ value: r.v, label: r.label }))} /></Form.Item>
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
                <div className="pp-muted pp-small" style={{ marginTop: 10 }}>这台机器已有 {occupied} 台，想建多少建多少。</div>
              </>
            )}
          </aside>
        </div>
      </Form>
    </Modal>
  )
}
