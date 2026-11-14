import {
  AppstoreAddOutlined, ArrowLeftOutlined, BorderOutlined, CameraOutlined, CopyOutlined, HomeOutlined,
  PoweroffOutlined, RedoOutlined, RotateRightOutlined, SoundOutlined, UploadOutlined
} from '@ant-design/icons'
import { App, Button, Descriptions, Drawer, Input, Space, Tabs, Tooltip } from 'antd'
import { screenUrl, type InputRequest } from '@/data/api'
import { PhoneScreen } from '@/components/PhoneScreen'
import { ProxyCell, StatusDot } from '@/components/bits'
import { blocksStart } from '@/data/consistency'
import { useWorkspace } from '@/data/store'
import type { Phone } from '@/data/types'
import { ParamPanel } from './ParamPanel'

/** 单台设备：左边大屏操作，右边参数、检查、信息。 */
export function PhoneDrawer({ phone, onClose }: { phone?: Phone; onClose: () => void }) {
  const ws = useWorkspace()
  const { message } = App.useApp()
  if (!phone) return null
  const live = ws.phones.find((p) => p.id === phone.id) ?? phone
  const issues = ws.issuesOf(live)
  const blocked = blocksStart(issues)
  const model = ws.modelOf(live)
  const host = ws.hostOf(live)
  const running = live.status === 'running'
  const liveMode = ws.mode === 'live'
  const todo = (what: string) => () => message.info(`${what}：还没做`)
  const key = (k: string) => () => (liveMode ? void ws.input(live.id, { kind: 'key', key: k } as InputRequest) : message.info('预览模式：接上管家程序后可用'))

  const controls = [
    { icon: <ArrowLeftOutlined />, tip: '返回', onClick: key('back') }, { icon: <HomeOutlined />, tip: '主页', onClick: key('home') },
    { icon: <BorderOutlined />, tip: '多任务', onClick: key('recents') }, { icon: <SoundOutlined />, tip: '音量 +', onClick: key('volume_up') },
    { icon: <RotateRightOutlined />, tip: '旋转', onClick: todo('旋转') },
    { icon: <CameraOutlined />, tip: '截图（新窗口打开原图）', onClick: () => (liveMode ? void window.open(screenUrl(live.id, Date.now()), '_blank') : message.info('预览模式：接上管家程序后可用')) },
    { icon: <UploadOutlined />, tip: '上传文件到相册', onClick: todo('上传文件') }, { icon: <AppstoreAddOutlined />, tip: '安装 APK', onClick: todo('安装 APK') },
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
              {
                key: 'info', label: '信息', children: (
                  <Descriptions column={1} size="small" bordered items={[
                    { label: '分组', children: ws.groupOf(live)?.name ?? '未分组' },
                    { label: '代理', children: <ProxyCell proxy={ws.proxyOf(live)} /> },
                    { label: '出口 IP', children: ws.proxyOf(live)?.check?.exitIp ?? '—' },
                    { label: '所在机器', children: `${host?.name}（${host?.location}）` },
                    { label: '显卡', children: host?.gpu },
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
