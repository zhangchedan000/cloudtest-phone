/**
 * H.264 裸流（Annex-B，screenrecord 输出）→ WebCodecs 解码 → 画到 canvas。
 * 只依赖浏览器自带的 VideoDecoder（Chrome / Edge 94+）；不支持时调用方退回截图方式。
 */

/** 在 buf 里从 from 开始找下一个起始码（00 00 01 或 00 00 00 01），返回起始码位置和长度。 */
export function findStartCode(buf: Uint8Array, from: number): { at: number; len: number } | undefined {
  for (let i = from; i + 2 < buf.length; i++) {
    if (buf[i] === 0 && buf[i + 1] === 0) {
      if (buf[i + 2] === 1) return { at: i, len: 3 }
      if (buf[i + 2] === 0 && i + 3 < buf.length && buf[i + 3] === 1) return { at: i, len: 4 }
    }
  }
  return undefined
}

/** 把收到的字节切成一个个 NAL（不含起始码）。最后一个不完整的留在 rest 里等下一块数据。 */
export function splitNals(buf: Uint8Array): { nals: Uint8Array[]; rest: Uint8Array } {
  const nals: Uint8Array[] = []
  let start = findStartCode(buf, 0)
  if (!start) return { nals, rest: buf }
  for (;;) {
    const next = findStartCode(buf, start.at + start.len)
    if (!next) return { nals, rest: buf.subarray(start.at) }
    nals.push(buf.subarray(start.at + start.len, next.at))
    start = next
  }
}

export const nalType = (nal: Uint8Array): number => (nal[0] ?? 0) & 0x1f

/**
 * rest 是不是一个完整的 AUD（分隔符，NAL 类型 9）。管家程序每写完一帧补一个 AUD；
 * 它后面不会马上有起始码，按常规要等下一帧才算完整——但 AUD 长度固定（1~2 字节），可以直接认。
 * 认出来就返回去掉起始码的 AUD，调用方当成一个完整 NAL 处理，这一帧就能立刻显示。
 */
export function trailingAud(rest: Uint8Array): Uint8Array | undefined {
  const sc = findStartCode(rest, 0)
  if (!sc || sc.at !== 0) return undefined
  const nal = rest.subarray(sc.len)
  return (nal.length === 1 || nal.length === 2) && nalType(nal) === 9 ? nal : undefined
}

/** SPS → WebCodecs 的 codec 字符串，例如 avc1.42c01f。 */
export function codecFromSps(sps: Uint8Array): string {
  const hex = (n: number) => n.toString(16).padStart(2, '0')
  return `avc1.${hex(sps[1])}${hex(sps[2])}${hex(sps[3])}`
}

const START = new Uint8Array([0, 0, 0, 1])

/** 把若干 NAL 拼回 Annex-B（解码器没给 description 时按 Annex-B 解）。 */
export function joinAnnexB(nals: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(nals.reduce((n, nal) => n + 4 + nal.length, 0))
  let at = 0
  for (const nal of nals) {
    out.set(START, at)
    out.set(nal, at + 4)
    at += 4 + nal.length
  }
  return out
}

/**
 * 把 NAL 组成一帧一帧：SPS / PPS / SEI 等跟着下一个图像片一起送；
 * 片的 first_mb_in_slice 为 0（第一个比特是 1）表示新的一帧开始。
 */
export class FrameAssembler {
  private pending: Uint8Array[] = []
  private frame: Uint8Array[] = []
  private key = false
  sps?: Uint8Array

  /** 喂一个 NAL，凑够一帧就返回这一帧。 */
  push(nal: Uint8Array): { data: Uint8Array; key: boolean } | undefined {
    const type = nalType(nal)
    if (type === 7) this.sps = nal
    const isSlice = type === 1 || type === 5
    let done: { data: Uint8Array; key: boolean } | undefined
    // AUD：只用来标记「上一帧结束了」，本身不送给解码器
    if (type === 9) return this.frame.length ? this.flush() : undefined
    if (isSlice && (nal[1] & 0x80) !== 0 && this.frame.length) done = this.flush()
    if (!isSlice) {
      if (this.frame.length) done = this.flush()
      this.pending.push(nal)
      return done
    }
    if (!this.frame.length) { this.frame.push(...this.pending); this.pending = [] }
    this.frame.push(nal)
    if (type === 5) this.key = true
    return done
  }

  private flush(): { data: Uint8Array; key: boolean } {
    const out = { data: joinAnnexB(this.frame), key: this.key }
    this.frame = []
    this.key = false
    return out
  }
}

export const videoSupported = (): boolean => typeof window !== 'undefined' && 'VideoDecoder' in window

/**
 * 播放一路视频流，直到 signal 结束或流断开。返回 'ended'（正常结束，可重连）或抛出错误。
 * onFrame：每画出一帧调用一次（调用方用它判断「画面出来了」，之前继续显示截图，免得黑屏）。
 */
export async function playStream(url: string, headers: Record<string, string>, canvas: HTMLCanvasElement, signal: AbortSignal, onFrame?: () => void): Promise<'ended'> {
  const response = await fetch(url, { headers, signal })
  if (!response.ok || !response.body) {
    let message = `画面连接失败（${response.status}）`
    try { message = ((await response.json()) as { error?: string }).error ?? message } catch { /* 非 JSON */ }
    throw new Error(message)
  }
  const ctx = canvas.getContext('2d')
  let decoder: VideoDecoder | undefined
  let gotKey = false
  let ts = 0
  const assembler = new FrameAssembler()
  const reader = response.body.getReader()
  let buf = new Uint8Array(0)
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) return 'ended'
      const merged = new Uint8Array(buf.length + value.length)
      merged.set(buf)
      merged.set(value, buf.length)
      const { nals, rest } = splitNals(merged)
      const aud = trailingAud(rest)
      if (aud) nals.push(aud) // 管家程序标记的帧结尾：不用等下一帧
      buf = aud ? new Uint8Array(0) : new Uint8Array(rest)
      for (const nal of nals) {
        const frame = assembler.push(nal)
        if (!frame) continue
        if (!decoder && assembler.sps) {
          decoder = new VideoDecoder({
            output: (vf) => {
              if (canvas.width !== vf.displayWidth || canvas.height !== vf.displayHeight) {
                canvas.width = vf.displayWidth
                canvas.height = vf.displayHeight
              }
              ctx?.drawImage(vf, 0, 0)
              vf.close()
              onFrame?.()
            },
            error: () => undefined
          })
          decoder.configure({ codec: codecFromSps(assembler.sps), optimizeForLatency: true })
        }
        if (!decoder) continue
        if (!gotKey && !frame.key) continue // 必须从关键帧开始
        gotKey = true
        decoder.decode(new EncodedVideoChunk({ type: frame.key ? 'key' : 'delta', timestamp: ts, data: frame.data }))
        ts += 33_333
      }
    }
  } finally {
    reader.cancel().catch(() => undefined)
    if (decoder && decoder.state !== 'closed') decoder.close()
  }
}
