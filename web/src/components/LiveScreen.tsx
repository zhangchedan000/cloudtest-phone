import { LoadingOutlined } from '@ant-design/icons'
import { useEffect, useRef, useState, type PointerEvent } from 'react'
import { apiUrl, authHeaders, screenshot } from '@/data/api'
import { useWorkspace } from '@/data/store'
import { playStream, videoSupported } from './h264'

type VideoState = 'off' | 'connecting' | 'playing' | 'failed'

/** 画质档位：分辨率（相对手机原分辨率）+ 码率。网慢选流畅，局域网可以选高清。 */
export const QUALITIES = {
  smooth: { label: '流畅', scale: 1 / 3, bitRate: 800_000 },
  normal: { label: '标准', scale: 1 / 2, bitRate: 2_000_000 },
  hd: { label: '高清', scale: 1, bitRate: 6_000_000 }
} as const
export type Quality = keyof typeof QUALITIES
const QUALITY_KEY = 'pp-stream-quality'
function savedQuality(): Quality {
  try { const q = localStorage.getItem(QUALITY_KEY); if (q && q in QUALITIES) return q as Quality } catch { /* 无痕模式等 */ }
  return 'normal'
}

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
  // 同一台手机的画面在别的窗口也开着：两边会互相顶掉，这边退回截图，不再反复抢
  const [videoBusy, setVideoBusy] = useState(false)
  const [quality, setQuality] = useState<Quality>(savedQuality)
  // 视频连上了不等于画面出来了：第一帧解出来之前继续显示截图，免得一打开是黑屏
  const [gotFrame, setGotFrame] = useState(false)
  const down = useRef<{ x: number; y: number; landscape: boolean; at: number; moveAt: number } | null>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const wantVideo = video === 'connecting' || video === 'playing'

  // 截屏：视频画面出来以后不再截；否则大屏 1 秒、小图 5 秒一张，上一张取回来才请求下一张。
  // 要的是缩小的 JPEG（大屏 540 宽、小图 270 宽），比原图 PNG 小十倍左右，国内直连也能很快出来；
  // 手机真实分辨率从响应头拿（点击坐标、视频尺寸都按它算）。
  const [shot, setShot] = useState<string>()
  const shotRef = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (video === 'playing' && gotFrame) return
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    void screenshot(id, tick, size === 'large' ? 540 : 270, abort.signal)
      .then(({ url, device: d }) => {
        if (shotRef.current) URL.revokeObjectURL(shotRef.current)
        shotRef.current = url
        setShot(url)
        setLoaded(true)
        setFailed(false)
        if (d) setDevice((prev) => prev ?? d)
      })
      .catch(() => { if (!abort.signal.aborted) setFailed(true) })
      .finally(() => { if (!abort.signal.aborted) timer = setTimeout(() => setTick((t) => t + 1), size === 'large' ? 1000 : 5000) })
    return () => { abort.abort(); if (timer) clearTimeout(timer) }
  }, [id, tick, size, video, gotFrame])
  useEffect(() => () => { if (shotRef.current) URL.revokeObjectURL(shotRef.current) }, [])

  // 视频流：先靠一张截图知道手机分辨率，再按画质档位要视频；180 秒到时间自动重连，换档位立即重连。
  useEffect(() => {
    if (!wantVideo || !device || !canvas.current) return
    const abort = new AbortController()
    const el = canvas.current
    const q = QUALITIES[quality]
    const w = Math.round(device.w * q.scale)
    const h = Math.round(device.h * q.scale)
    setVideoBusy(false)
    void (async () => {
      let short = 0 // 连续几次「刚连上就断」
      while (!abort.signal.aborted) {
        try {
          setVideo('playing')
          // 手机可能在别的服务器上：地址和口令按手机所在的服务器
          const path = `/phones/${id}/stream?w=${w}&h=${h}&br=${q.bitRate}`
          const began = Date.now()
          await playStream(apiUrl(path), authHeaders(path), el, abort.signal, () => { setGotFrame(true); setLandscape(el.width > el.height) })
          if (abort.signal.aborted) return
          // 正常结束（180 秒到时、转屏）马上重连；刚连上就断多半是别的窗口也在看这台、把这边顶掉了：
          // 退避重连，连续 4 次就停，改用截图（以前立刻重连，两个窗口互相踢个不停、白耗 CPU）
          if (Date.now() - began >= 2000) {
            short = 0
            continue
          }
          short++
          if (short >= 4) {
            setVideoBusy(true)
            setVideo('failed')
            return
          }
          await new Promise((r) => setTimeout(r, Math.min(1000 * 2 ** short, 8000)))
        } catch (error) {
          if (abort.signal.aborted) return
          setVideoError((error as Error).message)
          setVideo('failed')
          return
        }
      }
    })()
    return () => abort.abort()
  }, [id, device, wantVideo, quality])

  // 画面实际是横的还是竖的（转屏以后视频 / 截图是横的，见管家 rotate.go）
  const [landscape, setLandscape] = useState(false)
  const frameSize = (el: HTMLElement) => el instanceof HTMLCanvasElement ? { w: el.width, h: el.height } : el instanceof HTMLImageElement ? { w: el.naturalWidth, h: el.naturalHeight } : { w: 0, h: 0 }
  const toDevice = (event: PointerEvent<HTMLElement>) => {
    if (!device) return undefined
    const el = event.currentTarget
    // 画面横竖和手机分辨率不一致时按转过来的宽高算
    const f = frameSize(el)
    const land = f.w > 0 && f.h > 0 && f.w > f.h
    const dw = land === (device.w > device.h) ? device.w : device.h
    const dh = land === (device.w > device.h) ? device.h : device.w
    // object-fit: contain 会留黑边：按画面实际占的区域算
    const r = el.getBoundingClientRect()
    let left = r.left, top = r.top, width = r.width, height = r.height
    if (f.w > 0 && f.h > 0) {
      const scale = Math.min(r.width / f.w, r.height / f.h)
      width = f.w * scale; height = f.h * scale
      left += (r.width - width) / 2; top += (r.height - height) / 2
    }
    const clamp = (v: number, max: number) => Math.min(max, Math.max(0, Math.round(v)))
    return {
      x: clamp(((event.clientX - left) / width) * dw, dw),
      y: clamp(((event.clientY - top) / height) * dh, dh),
      landscape: land
    }
  }

  const interactive = size === 'large'
  const pointer = interactive ? {
    // 按下就「捕获」这根指针：之后即使拖出画面外，移动和松手也还归这里，拖动跟手不丢（修「按住拖动页面不滚」）
    onPointerDown: (event: PointerEvent<HTMLElement>) => {
      const p = toDevice(event)
      if (!p) return
      down.current = { ...p, at: Date.now(), moveAt: 0 }
      try { event.currentTarget.setPointerCapture(event.pointerId) } catch { /* 忽略 */ }
    },
    // 记录「真正开始拖」的时刻（按住不动的那段不算进滑动时长，否则会变成超慢拖拽、安卓不滚）
    onPointerMove: (event: PointerEvent<HTMLElement>) => {
      const d = down.current
      if (!d || d.moveAt) return
      const p = toDevice(event)
      if (p && Math.hypot(p.x - d.x, p.y - d.y) >= 8) d.moveAt = Date.now()
    },
    onPointerUp: (event: PointerEvent<HTMLElement>) => {
      const start = down.current
      const end = toDevice(event)
      down.current = null
      try { event.currentTarget.releasePointerCapture(event.pointerId) } catch { /* 忽略 */ }
      if (!start || !end) return
      const moved = Math.hypot(end.x - start.x, end.y - start.y)
      // 滑动时长只算拖动那段，并限制在适合滚动 / 翻页的区间（60~600ms）：快划就是甩动翻页，慢拖也能跟手滚
      const dragMs = Math.min(600, Math.max(60, Date.now() - (start.moveAt || start.at)))
      void (moved < 12
        ? ws.input(id, { kind: 'tap', x: start.x, y: start.y, landscape: start.landscape })
        : ws.input(id, { kind: 'swipe', x: start.x, y: start.y, x2: end.x, y2: end.y, durationMs: dragMs, landscape: start.landscape })
      ).then(() => setTick((t) => t + 1))
    }
  } : {}

  const showVideo = video === 'playing' && gotFrame
  return (
    <div className={`pp-live${landscape ? ' pp-live-landscape' : ''}`}>
      {!showVideo && shot && (
        <img
          className="pp-live-img"
          src={shot}
          alt=""
          draggable={false}
          style={{ visibility: loaded ? 'visible' : 'hidden', cursor: interactive ? 'pointer' : 'default' }}
          // 旧版管家程序不给真实分辨率的响应头，但那时返回的是原图，图片尺寸就是手机分辨率
          onLoad={(e) => { const el = e.currentTarget; if (el.naturalWidth) { setDevice((d) => d ?? { w: el.naturalWidth, h: el.naturalHeight }); if (!showVideo) setLandscape(el.naturalWidth > el.naturalHeight) } }}
          {...pointer}
        />
      )}
      {wantVideo && <canvas ref={canvas} className="pp-live-img" style={{ display: showVideo ? undefined : 'none', cursor: 'pointer' }} {...pointer} />}
      {!loaded && !showVideo && <div className="pp-phone-off">{failed ? <span>画面还没出来（可能在开机）</span> : <><LoadingOutlined /><span>加载画面…</span></>}</div>}
      {showVideo && (
        <div className="pp-live-quality">
          {(Object.keys(QUALITIES) as Quality[]).map((k) => (
            <button key={k} type="button" className={k === quality ? 'on' : ''} onClick={() => { setQuality(k); try { localStorage.setItem(QUALITY_KEY, k) } catch { /* 忽略 */ } }}>
              {QUALITIES[k].label}
            </button>
          ))}
        </div>
      )}
      {video === 'failed' && size === 'large' && <div className="pp-live-note" title={videoError}>{videoBusy ? '这台手机的画面在别的窗口开着，这里改用截图' : '视频流不可用，已改用截图'}</div>}
    </div>
  )
}
