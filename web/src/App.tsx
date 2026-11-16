import { ApiOutlined, BulbOutlined, CloudServerOutlined, DesktopOutlined, MobileOutlined, ProfileOutlined } from '@ant-design/icons'
import { Button, Input, Spin, Tooltip } from 'antd'
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

/** 页面登记表：侧边栏和标题都从这里读（同 Prism 的 registry）。 */
const PAGES: { key: string; label: string; icon: ReactNode; render: () => ReactNode }[] = [
  { key: 'phones', label: '云手机', icon: <MobileOutlined />, render: () => <PhonesPage /> },
  { key: 'console', label: '控制台', icon: <DesktopOutlined />, render: () => <ConsolePage /> },
  { key: 'proxies', label: '代理 IP', icon: <ApiOutlined />, render: () => <ProxiesPage /> },
  { key: 'models', label: '机型库', icon: <ProfileOutlined />, render: () => <ModelsPage /> },
  { key: 'hosts', label: '机器', icon: <CloudServerOutlined />, render: () => <HostsPage /> }
]

/** 单台手机的弹出窗口：只有这台的画面和操作按钮（没有侧边栏）。 */
function PhoneWindow({ id }: { id: string }) {
  const ws = useWorkspace()
  const phone = ws.phones.find((p) => p.id === id)
  useEffect(() => { if (phone) document.title = `${phone.name} · 云手机` }, [phone?.name])
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
      <aside className="pp-sidebar">
        <div className="pp-brand"><MobileOutlined /> 云手机</div>
        <nav>
          {PAGES.map((p) => (
            <button key={p.key} className={`pp-nav ${p.key === current.key ? 'pp-nav-active' : ''}`} onClick={() => go(p.key)}>{p.icon}<span>{p.label}</span></button>
          ))}
        </nav>
        <div className="pp-sidebar-foot">{ws.mode === 'live' ? `已连接 · ${ws.hosts[0]?.name ?? ''}` : ws.mode === 'demo' ? '预览模式 · 假数据' : ''}</div>
      </aside>
      <main className="pp-main">
        <header className="pp-header">
          <h1>{current.label}</h1>
          <Tooltip title={theme.mode === 'dark' ? '浅色' : '深色'}>
            <Button type="text" icon={<BulbOutlined />} onClick={() => applyTheme({ ...theme, mode: theme.mode === 'dark' ? 'light' : 'dark' })} />
          </Tooltip>
        </header>
        <div className="pp-content">
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
