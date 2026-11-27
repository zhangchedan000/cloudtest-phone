import {
  AppstoreAddOutlined, AppstoreOutlined, ArrowLeftOutlined, BorderOutlined, CameraOutlined, CodeOutlined, CopyOutlined, DeleteOutlined, EditOutlined, EnvironmentOutlined, HomeOutlined, MoreOutlined,
  PoweroffOutlined, RedoOutlined, ReloadOutlined, RotateRightOutlined, SoundOutlined, UploadOutlined, VideoCameraOutlined
} from '@ant-design/icons'
import { App, Avatar, Button, Descriptions, Drawer, Dropdown, Empty, Input, InputNumber, List, Modal, Popover, Select, Space, Spin, Switch, Tabs, Tag, Tooltip } from 'antd'
import { api, getClipboard, openRawScreenshot, serverUrl, setClipboard, upload, type InputRequest } from '@/data/api'
import { useEffect, useRef, useState } from 'react'
import { PhoneScreen } from '@/components/PhoneScreen'
import { KeyboardInput } from '@/components/KeyboardInput'
import { ProxyCell, StatusDot } from '@/components/bits'
import { blocksStart } from '@/data/consistency'
import { useWorkspace } from '@/data/store'
import type { Phone } from '@/data/types'
import { AdbDialog } from './AdbDialog'
import { BackupDialog } from './BackupDialog'
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
  const [appsOpen, setAppsOpen] = useState(false)
  const [locOpen, setLocOpen] = useState(false)
  const [adbOpen, setAdbOpen] = useState(false)
  const [dragging, setDragging] = useState(false)
  const recorder = useRef<MediaRecorder | null>(null)
  const [recording, setRecording] = useState(false)
  const [backupMode, setBackupMode] = useState<'backup' | 'restore' | null>(null)
  // 备份 / 恢复做完了提示一次
  const job = phone ? ws.phones.find((p) => p.id === phone.id)?.dataJob : undefined
  const lastJob = useRef<string>('')
  useEffect(() => {
    if (!job) return
    const sig = `${job.kind}-${job.state}-${job.at}`
    if (lastJob.current && lastJob.current !== sig && job.state !== 'running') {
      const what = job.kind === 'backup' ? '备份' : '恢复'
      if (job.state === 'done') message.success(`${what}完成`)
      else message.error(`${what}失败：${job.error ?? ''}`)
    }
    lastJob.current = sig
  }, [job, message])
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
  // 录屏：直接录浏览器里正在播的实时画面（不占服务器，不影响实时画面），停下后下载成视频文件
  const toggleRecord = () => {
    if (recorder.current) { recorder.current.stop(); return }
    const canvas = document.querySelector<HTMLCanvasElement>('.pp-detail-screen canvas')
    if (!canvas || canvas.style.display === 'none' || typeof MediaRecorder === 'undefined') { message.warning('实时画面还没出来（或者浏览器不支持录屏），等画面出来再录'); return }
    const type = ['video/webm;codecs=vp9', 'video/webm;codecs=vp8', 'video/webm'].find((t) => MediaRecorder.isTypeSupported(t)) ?? ''
    const rec = new MediaRecorder(canvas.captureStream(30), type ? { mimeType: type, videoBitsPerSecond: 4_000_000 } : undefined)
    const chunks: Blob[] = []
    rec.ondataavailable = (e) => { if (e.data.size) chunks.push(e.data) }
    rec.onstop = () => {
      recorder.current = null
      setRecording(false)
      const a = document.createElement('a')
      a.href = URL.createObjectURL(new Blob(chunks, { type: 'video/webm' }))
      a.download = `${live.name}-录屏-${new Date().toLocaleString('zh-CN', { hour12: false }).replace(/[/: ]/g, '')}.webm`
      a.click()
      setTimeout(() => URL.revokeObjectURL(a.href), 10_000)
      message.success('录屏已下载')
    }
    rec.start(1000)
    recorder.current = rec
    setRecording(true)
    message.info('开始录屏，再点一次「录屏」停止并下载')
  }
  const rotate = () => {
    if (!liveMode) { message.info('预览模式：接上管家程序后可用'); return }
    void ws.input(live.id, { kind: 'rotate' }).then((res) => {
      if (!res) return
      if (res.wantLandscape && !res.landscape) message.info({ content: '已设成横屏，但当前界面只能竖屏（比如桌面）；打开支持横屏的 App 会自动横过来', key: 'pp-rotate' })
      else message.success({ content: res.landscape ? '已切到横屏' : '已切回竖屏', key: 'pp-rotate' })
    })
  }
  const key = (k: string) => () => (liveMode ? void ws.input(live.id, { kind: 'key', key: k } as InputRequest) : message.info('预览模式：接上管家程序后可用'))

  const controls = [
    { icon: <ArrowLeftOutlined />, tip: '返回', onClick: key('back') }, { icon: <HomeOutlined />, tip: '主页', onClick: key('home') },
    { icon: <BorderOutlined />, tip: '多任务', onClick: key('recents') }, { icon: <SoundOutlined />, tip: '音量 +', onClick: key('volume_up') },
    { icon: <SoundOutlined style={{ opacity: 0.55, transform: 'scale(0.8)' }} />, tip: '音量 -', onClick: key('volume_down') },
    { icon: <RotateRightOutlined />, tip: '横竖屏切换（桌面是锁竖屏的，打开支持横屏的 App 再转）', onClick: rotate },
    { icon: <CameraOutlined />, tip: '截图（新窗口打开原图）', onClick: () => (liveMode ? void openRawScreenshot(live.id).catch((e: Error) => message.error(e.message)) : message.info('预览模式：接上管家程序后可用')) },
    { icon: <VideoCameraOutlined style={recording ? { color: '#ff4d4f' } : undefined} />, tip: recording ? '录屏中：点一下停止并下载' : '录屏（录浏览器里的实时画面，停止后下载成视频文件）', onClick: () => (liveMode ? toggleRecord() : message.info('预览模式：接上管家程序后可用')) },
    { icon: <UploadOutlined />, tip: '上传文件（图片、视频进相册，其他进下载目录；也可以直接把文件拖进窗口）', onClick: () => (liveMode ? fileInput.current?.click() : message.info('预览模式：接上管家程序后可用')) },
    { icon: <AppstoreOutlined />, tip: '应用列表（打开 / 卸载手机里的 App）', onClick: () => (liveMode ? setAppsOpen(true) : message.info('预览模式：接上管家程序后可用')) },
    { icon: <EnvironmentOutlined />, tip: '虚拟定位（给手机设一个假的 GPS 位置）', onClick: () => (liveMode ? setLocOpen(true) : message.info('预览模式：接上管家程序后可用')) },
    { icon: <AppstoreAddOutlined />, tip: '安装 APK（也可以直接把 APK 拖进窗口）', onClick: () => (liveMode ? apkInput.current?.click() : message.info('预览模式：接上管家程序后可用')) },
    { icon: <CopyOutlined />, tip: '剪贴板（电脑 ↔ 手机互相复制，支持中文）', onClick: () => (liveMode ? setClipOpen(true) : message.info('预览模式：接上管家程序后可用')) },
    { icon: <CodeOutlined />, tip: 'ADB 连接命令（复制到自己电脑上用）', onClick: () => (liveMode ? setAdbOpen(true) : message.info('预览模式：接上管家程序后可用')) }
  ]

  const title = <Space size={10}>{mode === 'edit' && <span className="pp-muted">编辑</span>}<b>{live.name}</b><StatusDot status={live.status} />{live.dataJob?.state === 'running' && <span className="pp-small" style={{ color: '#1677ff' }}>{live.dataJob.kind === 'backup' ? '备份中…' : '恢复中…'}</span>}{mode === 'edit' && <span className="pp-muted">{String(model?.values.marketName ?? '')} · Android {live.androidVersion}</span>}</Space>
  const followers = ws.followers[live.id] ?? []
  const others = ws.phones.filter((p) => p.id !== live.id)
  const groupPicker = (
    <div style={{ width: 300 }}>
      <p className="pp-small pp-muted" style={{ marginTop: 0 }}>在这个窗口里的点击、滑动、按键、打字、粘贴，下面选中的手机会同时跟着做（不同分辨率按比例换算）。只对开着的手机生效。</p>
      <Select mode="multiple" style={{ width: '100%' }} placeholder="选要跟着做的手机" value={followers} allowClear
        options={others.map((p) => ({ value: p.id, label: `${p.name}${p.status === 'running' ? '' : '（没开机）'}` }))}
        onChange={(ids: string[]) => ws.setFollowers(live.id, ids)} />
      <Space style={{ marginTop: 8 }}>
        <Button size="small" onClick={() => ws.setFollowers(live.id, others.filter((p) => p.status === 'running').map((p) => p.id))}>全部开着的</Button>
        {live.groupId && <Button size="small" onClick={() => ws.setFollowers(live.id, others.filter((p) => p.groupId === live.groupId && p.status === 'running').map((p) => p.id))}>同组开着的</Button>}
        <Button size="small" onClick={() => ws.setFollowers(live.id, [])}>停止群控</Button>
      </Space>
    </div>
  )
  const extra = (<Space size={6}>
          {mode === 'view' && liveMode && <Popover trigger="click" placement="bottomRight" title="群控（一控多）" content={groupPicker}>
            <Button type={followers.length ? 'primary' : 'default'}>{followers.length ? `群控中 ${followers.length} 台` : '群控'}</Button>
          </Popover>}
          {mode === 'view' && onEdit && <Button icon={<EditOutlined />} onClick={onEdit}>编辑</Button>}
          {running || live.status === 'starting'
            ? <Button danger icon={<PoweroffOutlined />} onClick={() => ws.setStatus([live.id], 'stopped')}>关机</Button>
            : <Tooltip title={blocked ? '有错误没解决，禁止开机（点「编辑」看「检查」）' : ''}><Button type="primary" disabled={blocked} icon={<PoweroffOutlined />} onClick={() => ws.setStatus([live.id], 'running')}>开机</Button></Tooltip>}
          <Dropdown
            disabled={!liveMode}
            menu={{
              items: [
                ...(running || live.status === 'starting' ? [{ key: 'restart', label: '重启', icon: <RedoOutlined /> }, { type: 'divider' as const }] : []),
                { key: 'backup', label: '备份这台手机…' },
                { key: 'restore', label: '从备份恢复…' },
                { type: 'divider' as const },
                { key: 'renew', label: '一键新机（重新随机身份，保留机型和数据）' },
                { key: 'reset', label: '一键重置（清空数据，恢复出厂）' },
                ...(host?.androidVersions ?? []).filter((v) => v !== live.androidVersion).map((v) => ({ key: `flash-${v}`, label: `刷机：换成 Android ${v}（清空数据）` }))
              ],
              onClick: ({ key }) => {
                if (key === 'backup' || key === 'restore') { setBackupMode(key); return }
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
        {mode === 'view' && <div
          className={`pp-detail-screen${dragging ? ' pp-dropping' : ''}`}
          // 把文件拖进窗口：APK 就安装，其他文件上传（图片、视频进相册）
          onDragOver={(e) => { if (running && liveMode && e.dataTransfer.types.includes('Files')) { e.preventDefault(); setDragging(true) } }}
          onDragLeave={(e) => { if (e.currentTarget === e.target) setDragging(false) }}
          onDrop={(e) => {
            e.preventDefault(); setDragging(false)
            for (const f of Array.from(e.dataTransfer.files)) void sendFile(f.name.toLowerCase().endsWith('.apk') ? 'apk' : 'file', f)
          }}
        >
          <KeyboardInput id={live.id} enabled={running && liveMode}>
            <PhoneScreen id={live.id} status={live.status} name={`${String(model?.values.marketName ?? '')} · Android ${live.androidVersion}`} size="large" seed={live.name.length + live.id.charCodeAt(5)} />
          </KeyboardInput>
          <div className="pp-detail-controls">
            {controls.map((c) => <Tooltip key={c.tip} title={c.tip} placement="left"><Button disabled={!running} icon={c.icon} onClick={c.onClick} /></Tooltip>)}
          </div>
          <input ref={apkInput} type="file" accept=".apk" hidden onChange={(e) => { void sendFile('apk', e.target.files?.[0]); e.target.value = '' }} />
          <input ref={fileInput} type="file" hidden onChange={(e) => { void sendFile('file', e.target.files?.[0]); e.target.value = '' }} />
          {uploading && <p className="pp-small pp-center">上传：{uploading}</p>}
          <p className="pp-muted pp-small pp-center pp-screen-hint">点一下 = 点击，按住拖动 = 滑动；直接打字输入、Ctrl+V 粘贴；长文字 / 复制互通点右侧「剪贴板」</p>
          <ClipboardDialog open={clipOpen} phoneId={live.id} onClose={() => setClipOpen(false)} />
          <AppsDialog open={appsOpen} phoneId={live.id} onClose={() => setAppsOpen(false)} />
          <LocationDialog open={locOpen} phoneId={live.id} onClose={() => setLocOpen(false)} />
        </div>}
        <BackupDialog phone={live} mode={backupMode} onClose={() => setBackupMode(null)} />
        <AdbDialog open={adbOpen} id={live.id} running={running} adbOn={!!live.adb} onEnable={() => ws.assign([live.id], { adb: true })} ip={live.ip} name={live.name} host={new URL(serverUrl(live.serverId)).hostname} onClose={() => setAdbOpen(false)} />
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
                    {
                      label: 'USB 调试（ADB）', children: <Space wrap>
                        <Switch size="small" checked={!!live.adb} disabled={!liveMode}
                          onChange={(on) => void ws.assign([live.id], { adb: on }).then(() => message.success(on ? '已打开 USB 调试' : '已关闭 USB 调试'))} />
                        <span className="pp-muted pp-small">平时关着（真手机普通用户都是关的，App 能看到这个开关）；要用 ADB 时打开，开着的手机马上生效</span>
                      </Space>
                    },
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
      <div className="pp-window-head">
        <Space size={10} wrap>{title}{mode === 'view' && liveMode && <ProxySwitcher phone={live} />}</Space>
        {extra}
      </div>
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

/** 常见 App 的友好名（APK 里的中文名 / 图标 shell 取不到，v1 先用这个 + 包名末段兜底）。 */
const APP_NAMES: Record<string, string> = {
  'com.android.vending': 'Play 商店', 'com.android.chrome': 'Chrome', 'com.google.android.youtube': 'YouTube',
  'com.openai.chatgpt': 'ChatGPT', 'com.android.settings': '设置', 'com.android.camera2': '相机',
  'com.android.gallery3d': '相册', 'com.google.android.gm': 'Gmail', 'com.google.android.apps.maps': 'Google 地图',
  'com.android.dialer': '电话', 'com.google.android.dialer': '电话', 'com.android.contacts': '通讯录',
  'com.android.calendar': '日历', 'com.google.android.calendar': '日历', 'com.android.deskclock': '时钟',
  'com.android.messaging': '短信', 'com.google.android.apps.messaging': '短信', 'com.android.documentsui': '文件',
  'com.google.android.apps.photos': 'Google 相册', 'com.whatsapp': 'WhatsApp', 'com.facebook.katana': 'Facebook',
  'com.instagram.android': 'Instagram', 'com.zhiliaoapp.musically': 'TikTok', 'com.ss.android.ugc.trill': 'TikTok',
  'com.twitter.android': 'X (Twitter)', 'com.google.android.googlequicksearchbox': 'Google', 'com.android.soundrecorder': '录音机'
}
type AppItem = { package: string; name: string; activity?: string; user: boolean; icon?: string }
/** 显示名：常见 App 用友好中文名，否则用手机里真正的标签，再兜底包名末段。 */
function appName(a: AppItem | string): string {
  const pkg = typeof a === 'string' ? a : a.package
  if (APP_NAMES[pkg]) return APP_NAMES[pkg]
  if (typeof a !== 'string' && a.name && a.name !== pkg) return a.name
  const seg = pkg.split('.').filter((s) => !['android', 'com', 'app', 'apps', 'google'].includes(s)).pop() ?? pkg
  return seg.charAt(0).toUpperCase() + seg.slice(1)
}

/** 应用列表：列出手机里有桌面图标的 App，图标 + 名字，一键打开 / 卸载（2026-11-26 用户要求，对齐魔云腾）。 */
function AppsDialog({ open, phoneId, onClose }: { open: boolean; phoneId: string; onClose: () => void }) {
  const { message, modal } = App.useApp()
  const [apps, setApps] = useState<AppItem[] | null>(null)
  const [busy, setBusy] = useState('')
  const load = () => {
    setApps(null)
    void api.apps(phoneId).then((r) => setApps(r.apps ?? [])).catch((e: Error) => { message.error(e.message); setApps([]) })
  }
  useEffect(() => { if (open) load() }, [open, phoneId]) // eslint-disable-line react-hooks/exhaustive-deps
  const act = async (action: 'launch' | 'uninstall', a: AppItem) => {
    setBusy(action + a.package)
    try {
      await api.appAction(phoneId, action, a.package, a.activity)
      if (action === 'launch') { message.success(`已打开 ${appName(a)}`); onClose() } else { message.success('已卸载'); load() }
    } catch (e) { message.error((e as Error).message) } finally { setBusy('') }
  }
  return (
    <Modal open={open} onCancel={onClose} footer={null} destroyOnHidden width={440}
      title={<Space>应用列表<Button size="small" icon={<ReloadOutlined />} onClick={load}>刷新</Button></Space>}>
      {apps === null ? <div className="pp-center" style={{ padding: 40 }}><Spin tip="读取应用中…"><div style={{ padding: 20 }} /></Spin></div>
        : apps.length === 0 ? <Empty description="没拿到应用列表（手机刚开机或没装 App）" />
          : <List size="small" dataSource={apps} style={{ maxHeight: '60vh', overflow: 'auto' }}
            renderItem={(a) => (
              <List.Item actions={[
                <Button key="o" size="small" type="link" loading={busy === 'launch' + a.package} onClick={() => void act('launch', a)}>打开</Button>,
                <Button key="u" size="small" type="link" danger icon={<DeleteOutlined />} loading={busy === 'uninstall' + a.package}
                  onClick={() => modal.confirm({ title: `卸载 ${appName(a)}？`, content: a.package, okButtonProps: { danger: true }, onOk: () => act('uninstall', a) })} />
              ]}>
                <List.Item.Meta
                  avatar={<Avatar shape="square" size={40} src={a.icon} style={a.icon ? undefined : { background: '#1677ff' }}>{appName(a).charAt(0)}</Avatar>}
                  title={<Space size={6}>{appName(a)}{a.user && <Tag color="blue">装的</Tag>}</Space>}
                  description={<span className="pp-muted pp-small">{a.package}</span>} />
              </List.Item>
            )} />}
      <p className="pp-muted pp-small" style={{ marginTop: 8 }}>蓝色「装的」= 你自己安装的；其余系统自带。</p>
    </Modal>
  )
}

/** 常用城市经纬度（含跨境），点一下填进输入框。 */
const LOC_PRESETS: { name: string; lat: number; lng: number }[] = [
  { name: '北京', lat: 39.9042, lng: 116.4074 }, { name: '上海', lat: 31.2304, lng: 121.4737 },
  { name: '广州', lat: 23.1291, lng: 113.2644 }, { name: '深圳', lat: 22.5431, lng: 114.0579 },
  { name: '香港', lat: 22.3193, lng: 114.1694 }, { name: '台北', lat: 25.0330, lng: 121.5654 },
  { name: '东京', lat: 35.6762, lng: 139.6503 }, { name: '首尔', lat: 37.5665, lng: 126.9780 },
  { name: '新加坡', lat: 1.3521, lng: 103.8198 }, { name: '曼谷', lat: 13.7563, lng: 100.5018 },
  { name: '纽约', lat: 40.7128, lng: -74.0060 }, { name: '洛杉矶', lat: 34.0522, lng: -118.2437 },
  { name: '伦敦', lat: 51.5074, lng: -0.1278 }, { name: '巴黎', lat: 48.8566, lng: 2.3522 },
  { name: '迪拜', lat: 25.2048, lng: 55.2708 }, { name: '悉尼', lat: -33.8688, lng: 151.2093 }
]

/** 虚拟定位：给手机设一个假的 GPS 位置（2026-11-27 用户要求，对齐魔云腾）。 */
function LocationDialog({ open, phoneId, onClose }: { open: boolean; phoneId: string; onClose: () => void }) {
  const { message } = App.useApp()
  const [lat, setLat] = useState<number | null>(null)
  const [lng, setLng] = useState<number | null>(null)
  const [cur, setCur] = useState<{ lat: number; lng: number } | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!open) return
    void api.location(phoneId).then((r) => {
      setCur(r.location)
      if (r.location) { setLat(r.location.lat); setLng(r.location.lng) }
    }).catch(() => {})
  }, [open, phoneId])
  const set = async () => {
    if (lat == null || lng == null) { message.warning('先填经纬度，或点一个城市'); return }
    setBusy(true)
    try { await api.setLocation(phoneId, lat, lng); setCur({ lat, lng }); message.success('定位已设置') }
    catch (e) { message.error((e as Error).message) } finally { setBusy(false) }
  }
  const clear = async () => {
    setBusy(true)
    try { await api.clearLocation(phoneId); setCur(null); message.success('已关闭虚拟定位') }
    catch (e) { message.error((e as Error).message) } finally { setBusy(false) }
  }
  return (
    <Modal open={open} onCancel={onClose} footer={null} destroyOnHidden width={440} title="虚拟定位">
      <p className="pp-muted pp-small">给手机设一个假的 GPS 位置，手机里的 App（地图、点外卖、按位置的）读到的就是这个点。常用于跨境测试。</p>
      <div style={{ margin: '10px 0' }}>
        <Space wrap size={[6, 6]}>
          {LOC_PRESETS.map((c) => (
            <Button key={c.name} size="small" onClick={() => { setLat(c.lat); setLng(c.lng) }}>{c.name}</Button>
          ))}
        </Space>
      </div>
      <Space>
        <span className="pp-muted pp-small">纬度</span>
        <InputNumber value={lat} onChange={(v) => setLat(v)} min={-90} max={90} step={0.0001} style={{ width: 130 }} placeholder="lat" />
        <span className="pp-muted pp-small">经度</span>
        <InputNumber value={lng} onChange={(v) => setLng(v)} min={-180} max={180} step={0.0001} style={{ width: 130 }} placeholder="lng" />
      </Space>
      <div style={{ marginTop: 14, display: 'flex', gap: 8 }}>
        <Button type="primary" icon={<EnvironmentOutlined />} loading={busy} onClick={set}>设置定位</Button>
        <Button danger disabled={!cur} loading={busy} onClick={clear}>关闭定位</Button>
      </div>
      {cur
        ? <p className="pp-small" style={{ marginTop: 10, color: '#52c41a' }}>当前已定位到：{cur.lat.toFixed(4)}, {cur.lng.toFixed(4)}（关机重开也会自动恢复）</p>
        : <p className="pp-muted pp-small" style={{ marginTop: 10 }}>当前没有设虚拟定位。</p>}
      <p className="pp-muted pp-small" style={{ marginTop: 2 }}>换城市直接点一下就切。关闭后 App 不再更新到这个位置；个别 App 可能还缓存着上一个，重开手机彻底清掉。</p>
    </Modal>
  )
}
