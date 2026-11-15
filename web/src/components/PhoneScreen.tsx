import { LoadingOutlined, PoweroffOutlined } from '@ant-design/icons'
import type { PhoneStatus } from '@/data/types'
import { useWorkspace } from '@/data/store'
import { LiveScreen } from './LiveScreen'

/**
 * 手机画面。接上管家程序（live）时显示实时截屏并可操作；预览模式显示示意画面。
 * size：thumb（卡片小图）/ large（大屏操作）。
 */
export function PhoneScreen({ id, status, name, size = 'thumb', seed = 0, startHint = false, startBlocked = false }: { id?: string; status: PhoneStatus; name: string; size?: 'thumb' | 'large'; seed?: number; startHint?: boolean; startBlocked?: boolean }) {
  const ws = useWorkspace()
  const hue = (seed * 47) % 360
  return (
    <div className={`pp-phone pp-phone-${size}`}>
      <div className="pp-phone-screen" style={status === 'running' && ws.mode !== 'live' ? { background: `linear-gradient(160deg, hsl(${hue} 70% 62%), hsl(${(hue + 60) % 360} 65% 38%))` } : undefined}>
        {status === 'running' && ws.mode === 'live' && id && <LiveScreen id={id} size={size} />}
        {status === 'running' && ws.mode !== 'live' && (
          <>
            <div className="pp-phone-status"><span>9:41</span><span>WiFi ▮▮▮ 83%</span></div>
            <div className="pp-phone-apps">
              {Array.from({ length: size === 'large' ? 16 : 8 }, (_, i) => <span key={i} className="pp-phone-app" style={{ background: `hsl(${(hue + i * 37) % 360} 70% 92%)` }} />)}
            </div>
            <div className="pp-phone-dock" />
          </>
        )}
        {status === 'stopped' && (startHint
          // 卡片上：关机时整个画面就是开机按钮
          ? <div className={`pp-phone-off pp-phone-start ${startBlocked ? 'pp-phone-start-blocked' : ''}`}><span className="pp-phone-start-btn"><PoweroffOutlined /></span><span>{startBlocked ? '有错误，不能开机' : '点击开机'}</span></div>
          : <div className="pp-phone-off"><PoweroffOutlined /><span>已关机</span></div>)}
        {status === 'starting' && <div className="pp-phone-off"><LoadingOutlined /><span>开机中…</span></div>}
        {status === 'error' && <div className="pp-phone-off pp-phone-error"><span>启动失败</span>{startHint && <span className="pp-small">点击重新开机</span>}</div>}
      </div>
      {size === 'large' && <div className="pp-phone-caption">{name}</div>}
    </div>
  )
}
