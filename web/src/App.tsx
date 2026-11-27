import { ApiOutlined, AppstoreOutlined, BulbOutlined, DesktopOutlined, MobileOutlined, ProfileOutlined, SettingOutlined } from '@ant-design/icons'
import { Input, Spin, Tooltip } from 'antd'
import { useEffect, useState, type ReactNode } from 'react'
import { applyTheme, useTheme } from '@/theme'
import { HostsPage } from '@/pages/other/HostsPage'
import { ModelsPage } from '@/pages/other/ModelsPage'
import { ProxiesPage } from '@/pages/other/ProxiesPage'
import { PhonesPage } from '@/pages/phones/PhonesPage'
import { ConsolePage } from '@/pages/phones/ConsolePage'
import { useWorkspace } from '@/data/store'
import { PhoneDrawer } from '@/pages/phones/PhoneDrawer'
import { windowPhoneId } from '@/pages/phones/phoneWindow'
import { UpdateButton } from '@/pages/other/UpdateButton'

/** 页面登记表：侧边栏和标题都从这里读（同 Prism 的 registry）。 */
const PAGES: { key: string; label: string; icon: ReactNode; render: () => ReactNode }[] = [
  { key: 'phones', label: '云机', icon: <MobileOutlined />, render: () => <PhonesPage /> },
  { key: 'console', label: '多控', icon: <AppstoreOutlined />, render: () => <ConsolePage /> },
  { key: 'hosts', label: '主机', icon: <DesktopOutlined />, render: () => <HostsPage /> },
  { key: 'models', label: '机型', icon: <ProfileOutlined />, render: () => <ModelsPage /> },
  { key: 'proxies', label: '代理', icon: <ApiOutlined />, render: () => <ProxiesPage /> }
]

/** 单台手机的弹出窗口：只有这台的画面和操作按钮（没有侧边栏）。 */
function PhoneWindow({ id }: { id: string }) {
  const ws = useWorkspace()
  const phone = ws.phones.find((p) => p.id === id)
  useEffect(() => { if (phone) document.title = `${phone.name} · 云测机` }, [phone?.name])
  if (ws.mode === 'loading') return <Spin className="pp-login" />
  if (ws.mode === 'login') return <div className="pp-login"><b>请先在主页面登录</b></div>
  if (!phone) return <div className="pp-login"><b>找不到这台手机（可能已经删除）</b></div>
  return <PhoneDrawer phone={phone} bare onClose={() => window.close()} />
}

export function App() {
  const popupId = windowPhoneId()
  if (popupId) return <PhoneWindow id={popupId} />
  return <MainApp />
}

function MainApp() {
  const [page, setPage] = useState(() => (location.hash.slice(1) || 'phones'))
  const theme = useTheme()
  const ws = useWorkspace()
  const current = PAGES.find((p) => p.key === page) ?? PAGES[0]
  const go = (key: string) => { setPage(key); history.replaceState(null, '', `#${key}`) }
  useEffect(() => {
    const onHash = () => setPage(location.hash.slice(1) || 'phones')
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  return (
    <div className="pp-shell">
      <aside className="pp-rail">
        <div className="pp-rail-brand"><MobileOutlined /></div>
        <nav className="pp-rail-nav">
          {PAGES.map((p) => (
            <button key={p.key} className={`pp-rail-item ${p.key === current.key ? 'on' : ''}`} onClick={() => go(p.key)}>
              <span className="pp-rail-ico">{p.icon}</span>
              <span className="pp-rail-label">{p.label}</span>
            </button>
          ))}
        </nav>
        <div className="pp-rail-foot">
          <Tooltip placement="right" title={theme.mode === 'dark' ? '浅色' : '深色'}>
            <button className="pp-rail-item" onClick={() => applyTheme({ ...theme, mode: theme.mode === 'dark' ? 'light' : 'dark' })}>
              <span className="pp-rail-ico"><BulbOutlined /></span>
              <span className="pp-rail-label">主题</span>
            </button>
          </Tooltip>
          <UpdateButton />
          <button className="pp-rail-item" onClick={() => go('hosts')}>
            <span className="pp-rail-ico"><SettingOutlined /></span>
            <span className="pp-rail-label">设置</span>
          </button>
        </div>
      </aside>
      <main className="pp-main">
        <div className="pp-content pp-content-flush">
          {ws.mode === 'loading' && <Spin className="pp-login" />}
          {ws.mode === 'login' && (
            <div className="pp-login">
              <b>这台机器设置了访问口令</b>
              <Input.Password placeholder="填写管家程序的 PP_TOKEN" onPressEnter={(e) => void ws.login(e.currentTarget.value)} />
              <span className="pp-muted pp-small">按回车登录；口令保存在这个浏览器里</span>
            </div>
          )}
          {(ws.mode === 'live' || ws.mode === 'demo') && current.render()}
        </div>
      </main>
    </div>
  )
}
