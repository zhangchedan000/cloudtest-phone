import {
  AppstoreAddOutlined, ArrowLeftOutlined, BorderOutlined, CameraOutlined, CopyOutlined, HomeOutlined,
  PoweroffOutlined, RedoOutlined, RotateRightOutlined, SoundOutlined, UploadOutlined
} from '@ant-design/icons'
import { App, Button, Descriptions, Drawer, Dropdown, Input, Modal, Space, Tabs, Tooltip } from 'antd'
import { screenUrl, upload, type InputRequest } from '@/data/api'
import { useRef, useState } from 'react'
import { PhoneScreen } from '@/components/PhoneScreen'
import { ProxyCell, StatusDot } from '@/components/bits'
import { blocksStart } from '@/data/consistency'
import { useWorkspace } from '@/data/store'
import type { Phone } from '@/data/types'
import { LogsPanel } from './LogsPanel'
import { ParamPanel } from './ParamPanel'

/** 单台设备：左边大屏操作，右边参数、检查、信息。 */
export function PhoneDrawer({ phone, onClose }: { phone?: Phone; onClose: () => void }) {
  const ws = useWorkspace()
  const { message } = App.useApp()
  // hooks 要在 return 之前
  const apkInput = useRef<HTMLInputElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState<string>('')
  if (!phone) return null
  const live = ws.phones.find((p) => p.id === phone.id) ?? phone
  const issues = ws.issuesOf(live)
  const blocked = blocksStart(issues)
  const model = ws.modelOf(live)
  const host = ws.hostOf(live)
  const running = live.status === 'running'
  const liveMode = ws.mode === 'live'
  const sendFile = async (kind: 'apk' | 'file', file: File | undefined) => {
    if (!file) return
    // 图片、视频放进相册（DCIM），其他文件放下载目录
    const dir = /^(image|video)\//.test(file.type) ? 'DCIM' : 'Download'
    const path = kind === 'apk' ? `/phones/${live.id}/apk` : `/phones/${live.id}/files?dir=${dir}`
    try {
      setUploading(`${file.name} 0%`)
      const result = await upload(path, file, (p) => setUploading(`${file.name} ${p}%${p === 100 ? (kind === 'apk' ? '，安装中…' : '，处理中…') : ''}`))
      message.success(kind === 'apk' ? `已安装 ${file.name}` : `已上传到 ${result.path}`)
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setUploading('')
    }
  }
  const todo = (what: string) => () => message.info(`${what}：还没做`)
  const key = (k: string) => () => (liveMode ? void ws.input(live.id, { kind: 'key', key: k } as InputRequest) : message.info('预览模式：接上管家程序后可用'))

  const controls = [
    { icon: <ArrowLeftOutlined />, tip: '返回', onClick: key('back') }, { icon: <HomeOutlined />, tip: '主页', onClick: key('home') },
    { icon: <BorderOutlined />, tip: '多任务', onClick: key('recents') }, { icon: <SoundOutlined />, tip: '音量 +', onClick: key('volume_up') },
    { icon: <RotateRightOutlined />, tip: '旋转', onClick: todo('旋转') },
    { icon: <CameraOutlined />, tip: '截图（新窗口打开原图）', onClick: () => (liveMode ? void window.open(screenUrl(live.id, Date.now()), '_blank') : message.info('预览模式：接上管家程序后可用')) },
    { icon: <UploadOutlined />, tip: '上传文件（图片、视频进相册，其他进下载目录）', onClick: () => (liveMode ? fileInput.current?.click() : message.info('预览模式：接上管家程序后可用')) },
    { icon: <AppstoreAddOutlined />, tip: '安装 APK', onClick: () => (liveMode ? apkInput.current?.click() : message.info('预览模式：接上管家程序后可用')) },
    { icon: <CopyOutlined />, tip: '剪贴板', onClick: todo('剪贴板') }
  ]

  return (
    <Drawer
      open
      size={1180}
      onClose={onClose}
      destroyOnHidden
      title={<Space size={12}><b>{live.name}</b><StatusDot status={live.status} /><span className="pp-muted">{String(model?.values.marketName ?? '')} · Android {live.androidVersion}</span></Space>}
      extra={
        <Space>
          <Dropdown
            disabled={!liveMode}
            menu={{
              items: [
                { key: 'reset', label: '一键重置（清空数据，恢复出厂）' },
                { type: 'divider' as const },
                ...(host?.androidVersions ?? []).filter((v) => v !== live.androidVersion).map((v) => ({ key: `flash-${v}`, label: `刷机：换成 Android ${v}（清空数据）` }))
              ],
              onClick: ({ key }) => {
                const version = key.startsWith('flash-') ? Number(key.slice(6)) : undefined
                Modal.confirm({
                  title: version ? `把 ${live.name} 刷成 Android ${version}？` : `重置 ${live.name}？`,
                  content: '手机里的 App、账号登录、相册全部清空。旧数据挪到服务器回收站，误操作还能找回。参数和代理不变。',
                  okButtonProps: { danger: true },
                  onOk: () => ws.resetPhones([live.id], version)
                })
              }
            }}
          >
            <Button>重置 / 刷机</Button>
          </Dropdown>
          {running ? (
            <>
              <Button icon={<RedoOutlined />} onClick={() => void ws.setStatus([live.id], 'stopped').then(() => ws.setStatus([live.id], 'running'))}>重启</Button>
              <Button danger icon={<PoweroffOutlined />} onClick={() => ws.setStatus([live.id], 'stopped')}>关机</Button>
            </>
          ) : (
            <Tooltip title={blocked ? '有错误没解决，禁止开机（见「检查」）' : ''}>
              <Button type="primary" disabled={blocked} icon={<PoweroffOutlined />} onClick={() => ws.setStatus([live.id], 'running')}>开机</Button>
            </Tooltip>
          )}
        </Space>
      }
    >
      <div className="pp-detail">
        <div className="pp-detail-screen">
          <PhoneScreen id={live.id} status={live.status} name={live.name} size="large" seed={live.name.length + live.id.charCodeAt(5)} />
          <div className="pp-detail-controls">
            {controls.map((c) => <Tooltip key={c.tip} title={c.tip} placement="left"><Button disabled={!running} icon={c.icon} onClick={c.onClick} /></Tooltip>)}
          </div>
          <input ref={apkInput} type="file" accept=".apk" hidden onChange={(e) => { void sendFile('apk', e.target.files?.[0]); e.target.value = '' }} />
          <input ref={fileInput} type="file" hidden onChange={(e) => { void sendFile('file', e.target.files?.[0]); e.target.value = '' }} />
          {uploading && <p className="pp-small pp-center">上传：{uploading}</p>}
          <Input.Search
            className="pp-send-text"
            disabled={!running || !liveMode}
            placeholder="发送文字（英文、数字、符号）"
            enterButton="发送"
            onSearch={(text, _event, info) => { if (text && info?.source !== 'clear') void ws.input(live.id, { kind: 'text', text }) }}
          />
          <p className="pp-muted pp-small pp-center">在画面上点一下 = 点击，按住拖动 = 滑动</p>
        </div>
        <div className="pp-detail-side">
          <Tabs
            items={[
              { key: 'params', label: '参数', children: <ParamPanel phone={live} onSaved={(restart) => message.success(restart && running ? '已保存，正在重启这台安卓' : '已保存')} /> },
              { key: 'logs', label: '日志', children: <LogsPanel phone={live} /> },
              {
                key: 'info', label: '信息', children: (
                  <Descriptions column={1} size="small" bordered items={[
                    { label: '分组', children: ws.groupOf(live)?.name ?? '未分组' },
                    { label: '代理', children: <ProxyCell proxy={ws.proxyOf(live)} /> },
                    { label: '出口 IP', children: ws.proxyOf(live)?.check?.exitIp ?? '—' },
                    { label: '所在机器', children: `${host?.name}（${host?.location}）` },
                    { label: '显卡', children: host?.gpu },
                    { label: '时区 / 语言', children: !live.settings ? '—' : live.settings.state === 'pending' ? '等开机完成后设置…' : live.settings.state === 'applied' ? '已设置' : `设置失败：${live.settings.error ?? ''}` },
                    { label: '创建时间', children: live.createdAt.slice(0, 16).replace('T', ' ') },
                    { label: '备注', children: live.note ?? '—' }
                  ]} />
                )
              }
            ]}
          />
        </div>
      </div>
    </Drawer>
  )
}
