import { CloudDownloadOutlined, LoadingOutlined } from '@ant-design/icons'
import { App, Button, Radio, Switch } from 'antd'
import { useEffect, useState } from 'react'
import { api } from '@/data/api'
import { usePersisted } from '@/data/persist'
import { applyTheme, useTheme } from '@/theme'

/** 客户端（Electron）通过 preload 暴露的设置桥；浏览器里没有就是 undefined。 */
interface ClientPrefs { closeBehavior?: 'quit' | 'minimize'; autoStart?: boolean }
interface PP { getPrefs?: () => Promise<ClientPrefs>; setPref?: (k: string, v: unknown) => void }
const pp = (): PP | undefined => (window as unknown as { pp?: PP }).pp

/**
 * 设置页（照魔云腾：存储 / 通用 / 关于）。2026-11-27 用户要求：点「设置」要进这个页，不再是设备设置。
 * - 通用：主题、关闭行为（退出/最小化，客户端）、开机自启动（客户端）、只显示运行中、语言。
 * - 存储：数据存储路径（板子上，只读）、恢复默认设置（清掉本地记住的偏好）。
 * - 关于：当前版本、技术栈、在线升级。
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
    onOk: () => { try { for (const k of Object.keys(localStorage)) if (k.startsWith('pp.')) localStorage.removeItem(k) } catch { /* 忽略 */ } window.location.reload() }
  })

  const Row = ({ title, desc, extra, right }: { title: string; desc?: string; extra?: string; right: React.ReactNode }) => (
    <div className="pp-set-row">
      <div className="pp-set-row-main"><div className="pp-set-row-title">{title}</div>{desc && <div className="pp-muted pp-small">{desc}</div>}{extra && <div className="pp-muted pp-small" style={{ color: 'var(--fb-warning-text, #c06a00)' }}>{extra}</div>}</div>
      <div className="pp-set-row-right">{right}</div>
    </div>
  )
  const onlyClient = inClient ? undefined : '仅客户端（exe）可用'

  return (
    <div className="pp-settings pp-page">
      <section className="pp-set-card">
        <h3>通用</h3>
        <Row title="深色主题" desc="浅色 / 深色界面" right={<Switch checked={theme.mode === 'dark'} onChange={(on) => applyTheme({ ...theme, mode: on ? 'dark' : 'light' })} />} />
        <Row title="关闭行为" desc="点右上角关闭按钮时" extra={onlyClient} right={
          <Radio.Group disabled={!inClient} value={client?.closeBehavior ?? 'quit'} onChange={(e) => setClientPref('closeBehavior', e.target.value)}
            options={[{ value: 'quit', label: '退出' }, { value: 'minimize', label: '最小化' }]} optionType="button" />
        } />
        <Row title="开机自启动" desc="开机后自动运行客户端" extra={onlyClient} right={<Switch disabled={!inClient} checked={!!client?.autoStart} onChange={(on) => setClientPref('autoStart', on)} />} />
        <Row title="只显示运行中的云机" desc="开启后云机页只显示开着的（关机的隐藏）" right={<Switch checked={onlyRunning} onChange={setOnlyRunning} />} />
        <Row title="记住界面设置" desc="视图、大小、筛选、停在哪页等，关了重开自动恢复" right={<span className="pp-muted pp-small">已开启</span>} />
        <Row title="语言" desc="界面显示语言" right={<span className="pp-muted">中文</span>} />
      </section>

      <section className="pp-set-card">
        <h3>存储</h3>
        <Row title="数据存储路径" desc="云手机数据在板子上的位置（在板子 agent 参数 -data 指定）" right={<span className="pp-mono pp-small">/mmc/pp-data</span>} />
        <Row title="恢复默认设置" desc="清掉记住的界面偏好，恢复成刚装好的样子（不动手机/代理/分组）" right={<Button onClick={resetAll}>恢复默认</Button>} />
      </section>

      <section className="pp-set-card">
        <h3>关于</h3>
        <Row title="当前版本" right={<span className="pp-mono">{ver || '…'}</span>} />
        <Row title="技术栈" right={<span className="pp-muted pp-small">Electron 客户端 · React 管理页 · Go 管家</span>} />
        <Row title="在线升级" desc={latest?.canUpdate ? `有新版本 ${latest.latest}` : (latest?.note || '已是最新版本')} right={
          upgrading ? <span className="pp-muted"><LoadingOutlined /> 升级中…</span>
            : <Button type="primary" icon={<CloudDownloadOutlined />} disabled={!latest?.canUpdate} onClick={upgrade}>{latest?.canUpdate ? '立即升级' : '已是最新'}</Button>
        } />
      </section>
    </div>
  )
}
