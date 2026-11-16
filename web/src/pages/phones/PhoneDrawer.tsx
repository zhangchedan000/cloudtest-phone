import {
  AppstoreAddOutlined, ArrowLeftOutlined, BorderOutlined, CameraOutlined, CodeOutlined, CopyOutlined, EditOutlined, HomeOutlined, MoreOutlined,
  PoweroffOutlined, RedoOutlined, RotateRightOutlined, SoundOutlined, UploadOutlined
} from '@ant-design/icons'
import { App, Button, Descriptions, Drawer, Dropdown, Input, Modal, Space, Switch, Tabs, Tooltip } from 'antd'
import { getClipboard, openRawScreenshot, serverUrl, setClipboard, upload, type InputRequest } from '@/data/api'
import { useRef, useState } from 'react'
import { PhoneScreen } from '@/components/PhoneScreen'
import { ProxyCell, StatusDot } from '@/components/bits'
import { blocksStart } from '@/data/consistency'
import { useWorkspace } from '@/data/store'
import type { Phone } from '@/data/types'
import { AdbDialog } from './AdbDialog'
import { ProxyListEditor, ProxySwitcher } from './ProxySwitcher'
import { LogsPanel } from './LogsPanel'
import { ParamPanel } from './ParamPanel'

/**
 * 单台设备的窗口，两种样子（2026-11-15 用户要求分开）：
 * - view（点卡片画面打开）：只有手机大屏、操作按钮、发送文字，右上角「编辑」进参数；
 * - edit（卡片上的「编辑」）：参数、日志、信息。
 */
export function PhoneDrawer({ phone, mode = 'view', onClose, onEdit, bare = false }: { phone?: Phone; mode?: 'view' | 'edit'; onClose: () => void; onEdit?: () => void; bare?: boolean }) {
  const ws = useWorkspace()
  const { message } = App.useApp()
  // hooks 要在 return 之前
  const apkInput = useRef<HTMLInputElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState<string>('')
  const [clipOpen, setClipOpen] = useState(false)
  const [adbOpen, setAdbOpen] = useState(false)
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
    { icon: <CameraOutlined />, tip: '截图（新窗口打开原图）', onClick: () => (liveMode ? void openRawScreenshot(live.id).catch((e: Error) => message.error(e.message)) : message.info('预览模式：接上管家程序后可用')) },
    { icon: <UploadOutlined />, tip: '上传文件（图片、视频进相册，其他进下载目录）', onClick: () => (liveMode ? fileInput.current?.click() : message.info('预览模式：接上管家程序后可用')) },
    { icon: <AppstoreAddOutlined />, tip: '安装 APK', onClick: () => (liveMode ? apkInput.current?.click() : message.info('预览模式：接上管家程序后可用')) },
    { icon: <CopyOutlined />, tip: '剪贴板（电脑 ↔ 手机互相复制，支持中文）', onClick: () => (liveMode ? setClipOpen(true) : message.info('预览模式：接上管家程序后可用')) },
    { icon: <CodeOutlined />, tip: 'ADB 连接命令（复制到自己电脑上用）', onClick: () => (liveMode ? setAdbOpen(true) : message.info('预览模式：接上管家程序后可用')) }
  ]

  const title = <Space size={10}>{mode === 'edit' && <span className="pp-muted">编辑</span>}<b>{live.name}</b><StatusDot status={live.status} />{mode === 'edit' && <span className="pp-muted">{String(model?.values.marketName ?? '')} · Android {live.androidVersion}</span>}</Space>
  const extra = (<Space size={6}>
          {mode === 'view' && onEdit && <Button icon={<EditOutlined />} onClick={onEdit}>编辑</Button>}
          {running || live.status === 'starting'
            ? <Button danger icon={<PoweroffOutlined />} onClick={() => ws.setStatus([live.id], 'stopped')}>关机</Button>
            : <Tooltip title={blocked ? '有错误没解决，禁止开机（点「编辑」看「检查」）' : ''}><Button type="primary" disabled={blocked} icon={<PoweroffOutlined />} onClick={() => ws.setStatus([live.id], 'running')}>开机</Button></Tooltip>}
          <Dropdown
            disabled={!liveMode}
            menu={{
              items: [
                ...(running || live.status === 'starting' ? [{ key: 'restart', label: '重启', icon: <RedoOutlined /> }, { type: 'divider' as const }] : []),
                { key: 'renew', label: '一键新机（重新随机身份，保留机型和数据）' },
                { key: 'reset', label: '一键重置（清空数据，恢复出厂）' },
                ...(host?.androidVersions ?? []).filter((v) => v !== live.androidVersion).map((v) => ({ key: `flash-${v}`, label: `刷机：换成 Android ${v}（清空数据）` }))
              ],
              onClick: ({ key }) => {
                if (key === 'restart') { void ws.setStatus([live.id], 'stopped').then(() => ws.setStatus([live.id], 'running')); return }
                if (key === 'renew') { Modal.confirm({ title: `一键新机 ${live.name}？`, content: '序列号、WiFi/蓝牙 MAC、Android ID、广告 ID 等「随机」身份重新生成一遍，机型、App 数据、代理都保留。重启后生效。', onOk: () => ws.renewPhones([live.id]) }); return }
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
            <Button icon={<MoreOutlined />}>更多</Button>
          </Dropdown>
        </Space>)
  const body = (
      <div className="pp-detail">
        {mode === 'view' && <div className="pp-detail-screen">
          <ProxySwitcher phone={live} />
          <PhoneScreen id={live.id} status={live.status} name={`${String(model?.values.marketName ?? '')} · Android ${live.androidVersion}`} size="large" seed={live.name.length + live.id.charCodeAt(5)} />
          <div className="pp-detail-controls">
            {controls.map((c) => <Tooltip key={c.tip} title={c.tip} placement="left"><Button disabled={!running} icon={c.icon} onClick={c.onClick} /></Tooltip>)}
          </div>
          <input ref={apkInput} type="file" accept=".apk" hidden onChange={(e) => { void sendFile('apk', e.target.files?.[0]); e.target.value = '' }} />
          <input ref={fileInput} type="file" hidden onChange={(e) => { void sendFile('file', e.target.files?.[0]); e.target.value = '' }} />
          {uploading && <p className="pp-small pp-center">上传：{uploading}</p>}
          <Input.Search
            className="pp-send-text"
            disabled={!running || !liveMode}
            placeholder="发送文字到手机输入框（中文、英文都可以）"
            enterButton="发送"
            onSearch={(text, _event, info) => { if (text && info?.source !== 'clear') void ws.input(live.id, { kind: 'text', text }) }}
          />
          <p className="pp-muted pp-small pp-center">在画面上点一下 = 点击，按住拖动 = 滑动</p>
          <ClipboardDialog open={clipOpen} phoneId={live.id} onClose={() => setClipOpen(false)} />
        </div>}
        <AdbDialog open={adbOpen} id={live.id} running={running} ip={live.ip} name={live.name} host={new URL(serverUrl(live.serverId)).hostname} onClose={() => setAdbOpen(false)} />
        {mode === 'edit' && <div className="pp-detail-side">
          <Tabs
            items={[
              { key: 'params', label: '参数', children: <ParamPanel phone={live} onSaved={(restart) => message.success(restart && running ? '已保存，正在重启这台安卓' : '已保存')} /> },
              { key: 'logs', label: '日志', children: <LogsPanel phone={live} /> },
              {
                key: 'info', label: '信息', children: (
                  <Descriptions column={1} size="small" bordered items={[
                    { label: '分组', children: ws.groupOf(live)?.name ?? '未分组' },
                    { label: '当前代理', children: <ProxyCell proxy={ws.proxyOf(live)} /> },
                    { label: '挂的代理（可多个）', children: <><ProxyListEditor phone={live} /><div className="pp-muted pp-small" style={{ marginTop: 4 }}>在手机窗口上方的下拉框里随时切换，开着的手机时区会马上跟着换</div></> },
                    { label: '出口 IP', children: ws.proxyOf(live)?.check?.exitIp ?? '—' },
                    { label: '所在机器', children: `${host?.name}（${host?.location}）` },
                    { label: '内部 IP（ADB 用）', children: live.ip ? <Space>{live.ip}<Button size="small" onClick={() => setAdbOpen(true)}>ADB 连接命令</Button></Space> : '—' },
                    { label: '显卡', children: host?.gpu },
                    {
                      label: '谷歌服务', children: <Space wrap>
                        <Switch size="small" checked={!!live.gapps} disabled={!liveMode || (!live.gapps && !host?.gappsVersions?.includes(live.androidVersion))}
                          onChange={(on) => void ws.assign([live.id], { gapps: on }).then(() => message.success(running ? '已切换，重启这台手机后生效' : '已切换，下次开机生效'))} />
                        <span className="pp-muted pp-small">{host?.gappsVersions?.includes(live.androidVersion) ? 'GMS / Play 商店，下次开机生效。来回切换后若有 App 一直崩，是另一套系统留下的数据，重置这台手机即可' : '这台机器没装这个安卓版本的谷歌服务镜像'}</span>
                      </Space>
                    },
                    { label: '时区 / 语言', children: !live.settings ? '—' : live.settings.state === 'pending' ? '等开机完成后设置…' : live.settings.state === 'applied' ? '已设置' : `设置失败：${live.settings.error ?? ''}` },
                    { label: '创建时间', children: live.createdAt.slice(0, 16).replace('T', ' ') },
                    { label: '备注', children: live.note ?? '—' }
                  ]} />
                )
              }
            ]}
          />
        </div>}
      </div>
  )
  // 独立弹出窗口（每台手机一个，可以同时开好几个）：没有抽屉外框，铺满窗口
  if (bare) return (
    <div className="pp-window">
      <div className="pp-window-head">{title}{extra}</div>
      <div className="pp-window-body">{body}</div>
    </div>
  )
  return (
    <Drawer open size={mode === 'view' ? 600 : 860} onClose={onClose} destroyOnHidden title={title} extra={extra}>
      {body}
    </Drawer>
  )
}

/**
 * 剪贴板互通：电脑 ↔ 手机互相复制，支持中文。
 * - 电脑 → 手机：文字放进手机剪贴板（在手机上长按输入框粘贴），或直接粘贴到手机当前输入框。
 * - 手机 → 电脑：取回手机剪贴板，同时复制到电脑剪贴板。
 * 读写电脑剪贴板要浏览器允许（https 或本机地址才行）；不允许时就在框里手动复制粘贴。
 */
function ClipboardDialog({ open, phoneId, onClose }: { open: boolean; phoneId: string; onClose: () => void }) {
  const { message } = App.useApp()
  const [text, setText] = useState('')
  const [busy, setBusy] = useState('')
  const run = async (what: string, fn: () => Promise<void>) => {
    setBusy(what)
    try { await fn() } catch (error) { message.error((error as Error).message) } finally { setBusy('') }
  }
  const fromPC = () => run('read', async () => {
    try {
      setText(await navigator.clipboard.readText())
    } catch {
      message.warning('浏览器不允许读电脑剪贴板：请在下面的框里按 Ctrl+V 粘贴')
    }
  })
  const toPhone = (paste: boolean) => run(paste ? 'paste' : 'set', async () => {
    if (!text) { message.warning('先填要发送的文字'); return }
    await setClipboard(phoneId, text, paste)
    message.success(paste ? '已粘贴到手机当前的输入框' : '已放进手机剪贴板：在手机上长按输入框 →「粘贴」')
  })
  const fromPhone = () => run('get', async () => {
    const { text: got } = await getClipboard(phoneId)
    if (!got) { message.info('手机剪贴板是空的'); return }
    setText(got)
    try {
      await navigator.clipboard.writeText(got)
      message.success('已取回，并复制到电脑剪贴板')
    } catch {
      message.success('已取回（浏览器不允许写电脑剪贴板，请在框里手动复制）')
    }
  })
  return (
    <Modal open={open} title="剪贴板（电脑 ↔ 手机）" onCancel={onClose} footer={null} destroyOnHidden>
      <Input.TextArea value={text} onChange={(e) => setText(e.target.value)} rows={6} maxLength={100_000} placeholder="要发给手机的文字，或者从手机取回的文字（中文、换行都可以）" />
      <Space wrap style={{ marginTop: 12 }}>
        <Button loading={busy === 'read'} onClick={() => void fromPC()}>读电脑剪贴板</Button>
        <Button type="primary" loading={busy === 'set'} onClick={() => void toPhone(false)}>放进手机剪贴板</Button>
        <Button loading={busy === 'paste'} onClick={() => void toPhone(true)}>直接粘贴到手机输入框</Button>
        <Button loading={busy === 'get'} onClick={() => void fromPhone()}>取回手机剪贴板</Button>
      </Space>
      <p className="pp-muted pp-small" style={{ marginTop: 12 }}>
        「直接粘贴」会打进手机上当前光标所在的输入框（先在画面上点一下输入框）。手机复制的东西点「取回」拿到电脑。
      </p>
    </Modal>
  )
}
