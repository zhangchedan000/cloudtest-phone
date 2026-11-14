import { LoadingOutlined, PoweroffOutlined } from '@ant-design/icons'
import type { PhoneStatus } from '@/data/types'

/**
 * 手机画面。现在是预览用的示意画面；接上管家程序后，运行中的设备在这里显示实时视频流。
 * size：thumb（卡片小图）/ large（大屏操作）。
 */
export function PhoneScreen({ status, name, size = 'thumb', seed = 0 }: { status: PhoneStatus; name: string; size?: 'thumb' | 'large'; seed?: number }) {
  const hue = (seed * 47) % 360
  return (
    <div className={`pp-phone pp-phone-${size}`}>
      <div className="pp-phone-screen" style={status === 'running' ? { background: `linear-gradient(160deg, hsl(${hue} 70% 62%), hsl(${(hue + 60) % 360} 65% 38%))` } : undefined}>
        {status === 'running' && (
          <>
            <div className="pp-phone-status"><span>9:41</span><span>WiFi ▮▮▮ 83%</span></div>
            <div className="pp-phone-apps">
              {Array.from({ length: size === 'large' ? 16 : 8 }, (_, i) => <span key={i} className="pp-phone-app" style={{ background: `hsl(${(hue + i * 37) % 360} 70% 92%)` }} />)}
            </div>
            <div className="pp-phone-dock" />
          </>
        )}
        {status === 'stopped' && <div className="pp-phone-off"><PoweroffOutlined /><span>已关机</span></div>}
        {status === 'starting' && <div className="pp-phone-off"><LoadingOutlined /><span>开机中…</span></div>}
        {status === 'error' && <div className="pp-phone-off pp-phone-error"><span>启动失败</span></div>}
      </div>
      {size === 'large' && <div className="pp-phone-caption">{name}</div>}
    </div>
  )
}
