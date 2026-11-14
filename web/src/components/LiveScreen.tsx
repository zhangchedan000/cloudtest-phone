import { LoadingOutlined } from '@ant-design/icons'
import { useEffect, useRef, useState, type PointerEvent } from 'react'
import { screenUrl } from '@/data/api'
import { useWorkspace } from '@/data/store'

/**
 * 运行中设备的实时画面（定时截屏）。在画面上点一下 = 点击，按住拖动 = 滑动。
 * 坐标按图片真实像素换算，所以不管画面缩放多少都点得准。
 */
export function LiveScreen({ id, size }: { id: string; size: 'thumb' | 'large' }) {
  const ws = useWorkspace()
  const [tick, setTick] = useState(0)
  const [loaded, setLoaded] = useState(false)
  const [failed, setFailed] = useState(false)
  const down = useRef<{ x: number; y: number; at: number } | null>(null)
  const img = useRef<HTMLImageElement>(null)

  // 大屏 1 秒一张，卡片小图 5 秒一张；图片加载完才请求下一张，不会堆积。
  useEffect(() => {
    const timer = setTimeout(() => setTick((t) => t + 1), size === 'large' ? 1000 : 5000)
    return () => clearTimeout(timer)
  }, [tick, size])

  const toDevice = (event: PointerEvent<HTMLImageElement>) => {
    const el = img.current
    if (!el || !el.naturalWidth) return undefined
    const rect = el.getBoundingClientRect()
    return {
      x: Math.round(((event.clientX - rect.left) / rect.width) * el.naturalWidth),
      y: Math.round(((event.clientY - rect.top) / rect.height) * el.naturalHeight)
    }
  }

  const interactive = size === 'large'
  return (
    <div className="pp-live">
      <img
        ref={img}
        className="pp-live-img"
        src={screenUrl(id, tick)}
        alt=""
        draggable={false}
        style={{ visibility: loaded ? 'visible' : 'hidden', cursor: interactive ? 'pointer' : 'default' }}
        onLoad={() => { setLoaded(true); setFailed(false) }}
        onError={() => setFailed(true)}
        onPointerDown={interactive ? (event) => { const p = toDevice(event); if (p) down.current = { ...p, at: Date.now() } } : undefined}
        onPointerUp={interactive ? (event) => {
          const start = down.current
          const end = toDevice(event)
          down.current = null
          if (!start || !end) return
          const moved = Math.hypot(end.x - start.x, end.y - start.y)
          void (moved < 12
            ? ws.input(id, { kind: 'tap', x: start.x, y: start.y })
            : ws.input(id, { kind: 'swipe', x: start.x, y: start.y, x2: end.x, y2: end.y, durationMs: Math.max(100, Date.now() - start.at) })
          ).then(() => setTick((t) => t + 1))
        } : undefined}
      />
      {!loaded && <div className="pp-phone-off">{failed ? <span>画面还没出来（可能在开机）</span> : <><LoadingOutlined /><span>加载画面…</span></>}</div>}
    </div>
  )
}
