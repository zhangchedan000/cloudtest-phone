import {
  AppstoreAddOutlined, ArrowLeftOutlined, BorderOutlined, CameraOutlined, CopyOutlined, HomeOutlined,
  PoweroffOutlined, RedoOutlined, RotateRightOutlined, SoundOutlined, UploadOutlined
} from '@ant-design/icons'
import { App, Button, Descriptions, Drawer, Space, Tabs, Tooltip } from 'antd'
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
  const todo = (what: string) => () => message.info(`${what}：接上管家程序后可用（预览版）`)

  const controls = [
    { icon: <ArrowLeftOutlined />, tip: '返回' }, { icon: <HomeOutlined />, tip: '主页' }, { icon: <BorderOutlined />, tip: '多任务' },
    { icon: <SoundOutlined />, tip: '音量' }, { icon: <RotateRightOutlined />, tip: '旋转' }, { icon: <CameraOutlined />, tip: '截图' },
    { icon: <UploadOutlined />, tip: '上传视频到相册（自动清理元数据）' }, { icon: <AppstoreAddOutlined />, tip: '安装 APK' }, { icon: <CopyOutlined />, tip: '剪贴板' }
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
              <Button icon={<RedoOutlined />} onClick={() => message.success('已发送重启')}>重启</Button>
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
          <PhoneScreen status={live.status} name={live.name} size="large" seed={live.name.length + live.id.charCodeAt(5)} />
          <div className="pp-detail-controls">
            {controls.map((c) => <Tooltip key={c.tip} title={c.tip} placement="left"><Button disabled={!running} icon={c.icon} onClick={todo(c.tip)} /></Tooltip>)}
          </div>
          <p className="pp-muted pp-small pp-center">点击/拖动画面 = 手指操作（走虚拟触摸屏）<br />键盘输入走虚拟键盘设备，手机里不装辅助 App</p>
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
