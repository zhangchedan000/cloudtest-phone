import {
  ApartmentOutlined, AppstoreOutlined, BgColorsOutlined, CheckCircleFilled, CloudDownloadOutlined,
  DatabaseOutlined, EyeOutlined, FolderOpenOutlined, GlobalOutlined, InfoCircleOutlined, LoadingOutlined,
  LoginOutlined, PoweroffOutlined, ReloadOutlined, SaveOutlined,
} from '@ant-design/icons'
import { App, Button, Radio, Switch, Tag } from 'antd'
import { useEffect, useState } from 'react'
import { api } from '@/data/api'
import { usePersisted } from '@/data/persist'
import { applyTheme, useTheme } from '@/theme'

/** 客户端（Electron）通过 preload 暴露的设置桥；浏览器里没有就是 undefined。 */
interface ClientPrefs { closeBehavior?: 'quit' | 'minimize'; autoStart?: boolean; clientBuild?: string }
interface PP { getPrefs?: () => Promise<ClientPrefs>; setPref?: (k: string, v: unknown) => void; checkUpdate?: () => void }
const pp = (): PP | undefined => (window as unknown as { pp?: PP }).pp

/**
 * 设置页（照魔云腾：通用 / 存储 / 关于）。2026-11-27 用户要求：点「设置」要进这个页，不再是设备设置；
 * 2026-11-27 重做界面（图标 + 卡片 + 居中），别那么素。
 * 带「仅客户端」的项，在网页里（非 exe）里会提示只有客户端能用。
 */
export function SettingsPage() {
  const { message, modal } = App.useApp()
  const theme = useTheme()
  const [onlyRunning, setOnlyRunning] = usePersisted('pp.onlyRunning', false)
  const [client, setClient] = useState<ClientPrefs | null>(null)
  const inClient = !!pp()?.getPrefs
  useEffect(() => { pp()?.getPrefs?.().then(setClient).catch(() => setClient({})) }, [])
  const setClientPref = (k: keyof ClientPrefs, v: unknown) => { pp()?.setPref?.(k, v); setClient((p) => ({ ...p, [k]: v })) }

  const [ver, setVer] = useState('')
  const [latest, setLatest] = useState<{ canUpdate: boolean; latest?: string; note?: string }>()
  const [upgrading, setUpgrading] = useState(false)
  useEffect(() => { void api.updateStatus().then((r) => { setVer(r.current); setLatest(r) }).catch(() => {}) }, [])
  const upgrade = () => {
    setUpgrading(true)
    void api.startUpdate().then((r) => {
      message.success(`开始升级：${r.from || '当前'} → ${r.to}`)
      const timer = window.setInterval(() => {
        void api.updateStatus().then((s) => { if (s.current && s.current === r.to) { window.clearInterval(timer); message.success('升级完成，正在刷新…'); setTimeout(() => window.location.reload(), 1200) } }).catch(() => {})
      }, 3000)
    }).catch((e: Error) => { message.error(e.message); setUpgrading(false) })
  }

  const resetAll = () => modal.confirm({
    title: '恢复默认设置？',
    content: '把这个软件记住的所有界面偏好（视图、大小、筛选、停在哪页等）清掉，恢复成刚装好的样子。不影响你的手机、代理、分组。',
    okText: '恢复默认', cancelText: '取消',
    onOk: () => { try { for (const k of Object.keys(localStorage)) if (k.startsWith('pp.')) localStorage.removeItem(k) } catch { /* 忽略 */ } window.location.reload() }
  })

  const Row = ({ icon, title, desc, extra, right }: { icon: React.ReactNode; title: string; desc?: string; extra?: string; right: React.ReactNode }) => (
    <div className="pp-set-row">
      <div className="pp-set-row-ico">{icon}</div>
      <div className="pp-set-row-main">
        <div className="pp-set-row-title">{title}</div>
        {desc && <div className="pp-set-row-desc">{desc}</div>}
        {extra && <div className="pp-set-row-extra">{extra}</div>}
      </div>
      <div className="pp-set-row-right">{right}</div>
    </div>
  )
  const onlyClient = inClient ? undefined : '仅客户端（exe）可用'

  return (
    <div className="pp-settings">
      <div className="pp-settings-head">
        <h2>设置</h2>
        <p className="pp-muted">外观、客户端行为、存储和版本信息</p>
      </div>

      <section className="pp-set-card">
        <div className="pp-set-card-head"><BgColorsOutlined /><span>通用</span></div>
        <Row icon={<BgColorsOutlined />} title="深色主题" desc="浅色 / 深色界面"
          right={<Switch checked={theme.mode === 'dark'} onChange={(on) => applyTheme({ ...theme, mode: on ? 'dark' : 'light' })} />} />
        <Row icon={<PoweroffOutlined />} title="关闭行为" desc="点右上角关闭按钮时" extra={onlyClient}
          right={<Radio.Group disabled={!inClient} value={client?.closeBehavior ?? 'minimize'} onChange={(e) => setClientPref('closeBehavior', e.target.value)}
            options={[{ value: 'minimize', label: '缩到托盘' }, { value: 'quit', label: '退出' }]} optionType="button" buttonStyle="solid" />} />
        <Row icon={<LoginOutlined />} title="开机自启动" desc="开机后自动运行客户端" extra={onlyClient}
          right={<Switch disabled={!inClient} checked={!!client?.autoStart} onChange={(on) => setClientPref('autoStart', on)} />} />
        <Row icon={<EyeOutlined />} title="只显示运行中的云机" desc="开启后云机页只显示开着的（关机的隐藏）"
          right={<Switch checked={onlyRunning} onChange={setOnlyRunning} />} />
        <Row icon={<SaveOutlined />} title="记住界面设置" desc="视图、大小、筛选、停在哪页等，关了重开自动恢复"
          right={<Tag color="success" bordered={false}>已开启</Tag>} />
        <Row icon={<GlobalOutlined />} title="语言" desc="界面显示语言" right={<span className="pp-muted">中文</span>} />
      </section>

      <section className="pp-set-card">
        <div className="pp-set-card-head"><DatabaseOutlined /><span>存储</span></div>
        <Row icon={<FolderOpenOutlined />} title="数据存储路径" desc="云手机数据在板子上的位置（在板子 agent 参数 -data 指定）"
          right={<span className="pp-mono pp-small">/mmc/pp-data</span>} />
        <Row icon={<ReloadOutlined />} title="恢复默认设置" desc="清掉记住的界面偏好，恢复成刚装好的样子（不动手机/代理/分组）"
          right={<Button onClick={resetAll}>恢复默认</Button>} />
      </section>

      <section className="pp-set-card">
        <div className="pp-set-card-head"><InfoCircleOutlined /><span>关于</span></div>
        <Row icon={<InfoCircleOutlined />} title="管家版本" desc="板子上管家程序的版本"
          right={<span className="pp-mono pp-small">{ver || '…'}</span>} />
        {client?.clientBuild && <Row icon={<AppstoreOutlined />} title="客户端版本" desc="这个桌面客户端的版本"
          right={<span className="pp-mono pp-small">{client.clientBuild}</span>} />}
        <Row icon={<ApartmentOutlined />} title="技术栈" desc="云测机的三层结构"
          right={<span className="pp-muted pp-small">Electron 客户端 · React 管理页 · Go 管家</span>} />
        <Row icon={<CloudDownloadOutlined />} title="管家在线升级"
          desc={latest?.canUpdate ? `发现新版本 ${latest.latest}` : (latest?.note || '已是最新版本')}
          right={upgrading
            ? <span className="pp-muted"><LoadingOutlined /> 升级中…</span>
            : latest?.canUpdate
              ? <Button type="primary" icon={<CloudDownloadOutlined />} onClick={upgrade}>立即升级</Button>
              : <span className="pp-set-ok"><CheckCircleFilled /> 已是最新</span>} />
      </section>
    </div>
  )
}
