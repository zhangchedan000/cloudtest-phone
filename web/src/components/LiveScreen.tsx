import { LoadingOutlined } from '@ant-design/icons'
import { useEffect, useRef, useState, type PointerEvent } from 'react'
import { savedToken, screenUrl } from '@/data/api'
import { useWorkspace } from '@/data/store'
import { playStream, videoSupported } from './h264'

type VideoState = 'off' | 'connecting' | 'playing' | 'failed'

/**
 * 运行中设备的实时画面。
 * - 大屏：视频流（H.264 + 浏览器 WebCodecs 解码），浏览器不支持或连不上时退回定时截屏（1 秒一张）。
 * - 卡片小图：定时截屏（5 秒一张）。
 * 在画面上点一下 = 点击，按住拖动 = 滑动。坐标按手机的真实分辨率换算，画面缩放多少都点得准。
 */
export function LiveScreen({ id, size }: { id: string; size: 'thumb' | 'large' }) {
  const ws = useWorkspace()
  const [tick, setTick] = useState(0)
  const [loaded, setLoaded] = useState(false)
  const [failed, setFailed] = useState(false)
  const [device, setDevice] = useState<{ w: number; h: number }>()
  const [video, setVideo] = useState<VideoState>(size === 'large' && videoSupported() ? 'connecting' : 'off')
  const [videoError, setVideoError] = useState('')
  const down = useRef<{ x: number; y: number; at: number } | null>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const wantVideo = video === 'connecting' || video === 'playing'

  // 截屏：视频在放的时候不再截；否则大屏 1 秒、小图 5 秒一张，图片加载完才请求下一张。
  useEffect(() => {
    if (video === 'playing') return
    const timer = setTimeout(() => setTick((t) => t + 1), size === 'large' ? 1000 : 5000)
    return () => clearTimeout(timer)
  }, [tick, size, video])

  // 视频流：先靠一张截图知道手机分辨率，再按一半分辨率要视频；180 秒到时间自动重连。
  useEffect(() => {
    if (!wantVideo || !device || !canvas.current) return
    const abort = new AbortController()
    const el = canvas.current
    const token = savedToken()
    const w = Math.round(device.w / 2)
    const h = Math.round(device.h / 2)
    void (async () => {
      while (!abort.signal.aborted) {
        try {
          setVideo('playing')
          await playStream(`/api/phones/${id}/stream?w=${w}&h=${h}&br=2000000`, token ? { Authorization: `Bearer ${token}` } : {}, el, abort.signal)
        } catch (error) {
          if (abort.signal.aborted) return
          setVideoError((error as Error).message)
          setVideo('failed')
          return
        }
      }
    })()
    return () => abort.abort()
  }, [id, device, wantVideo])

  const toDevice = (event: PointerEvent<HTMLElement>) => {
    if (!device) return undefined
    const rect = event.currentTarget.getBoundingClientRect()
    return {
      x: Math.round(((event.clientX - rect.left) / rect.width) * device.w),
      y: Math.round(((event.clientY - rect.top) / rect.height) * device.h)
    }
  }

  const interactive = size === 'large'
  const pointer = interactive ? {
    onPointerDown: (event: PointerEvent<HTMLElement>) => { const p = toDevice(event); if (p) down.current = { ...p, at: Date.now() } },
    onPointerUp: (event: PointerEvent<HTMLElement>) => {
      const start = down.current
      const end = toDevice(event)
      down.current = null
      if (!start || !end) return
      const moved = Math.hypot(end.x - start.x, end.y - start.y)
      void (moved < 12
        ? ws.input(id, { kind: 'tap', x: start.x, y: start.y })
        : ws.input(id, { kind: 'swipe', x: start.x, y: start.y, x2: end.x, y2: end.y, durationMs: Math.max(100, Date.now() - start.at) })
      ).then(() => setTick((t) => t + 1))
    }
  } : {}

  const showVideo = video === 'playing'
  return (
    <div className="pp-live">
      {!showVideo && (
        <img
          className="pp-live-img"
          src={screenUrl(id, tick)}
          alt=""
          draggable={false}
          style={{ visibility: loaded ? 'visible' : 'hidden', cursor: interactive ? 'pointer' : 'default' }}
          onLoad={(e) => { setLoaded(true); setFailed(false); const el = e.currentTarget; if (el.naturalWidth) setDevice((d) => d ?? { w: el.naturalWidth, h: el.naturalHeight }) }}
          onError={() => setFailed(true)}
          {...pointer}
        />
      )}
      {wantVideo && <canvas ref={canvas} className="pp-live-img" style={{ display: showVideo ? undefined : 'none', cursor: 'pointer' }} {...pointer} />}
      {!loaded && !showVideo && <div className="pp-phone-off">{failed ? <span>画面还没出来（可能在开机）</span> : <><LoadingOutlined /><span>加载画面…</span></>}</div>}
      {video === 'failed' && size === 'large' && <div className="pp-live-note" title={videoError}>视频流不可用，已改用截图</div>}
    </div>
  )
}
