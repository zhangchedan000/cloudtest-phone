import { App } from 'antd'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { getClipboard } from '@/data/api'
import { useWorkspace } from '@/data/store'
import { comboNames, keyNames, KeyQueue } from './keyboardBridge'

const isASCII = (s: string) => /^[\x20-\x7e]*$/.test(s)

/**
 * 包住手机画面：点一下画面，电脑键盘就直接打字进手机（见 keyboardBridge.ts）；
 * 剪贴板自动互通：Ctrl+V 把电脑剪贴板粘贴进手机；手机里复制的内容（长按复制、Ctrl+C）自动放到电脑剪贴板。
 */
export function KeyboardInput({ id, enabled, children }: { id: string; enabled: boolean; children: ReactNode }) {
  const { message } = App.useApp()
  // 工作区对象每次刷新都会换，用 ref 拿最新的，打字队列不跟着重建（不然攒着的字会丢）
  const ws = useWorkspace()
  const wsRef = useRef(ws)
  wsRef.current = ws
  const box = useRef<HTMLTextAreaElement>(null)
  const [active, setActive] = useState(false)
  // 我们自己放进手机剪贴板的（电脑粘贴过去的、中文打字走剪贴板的），手机→电脑同步时不要再搬回来
  const pushed = useRef<string | null>(null)
  const seen = useRef<string | null>(null)
  const queue = useMemo(() => new KeyQueue(async (req) => {
    // 中文走剪贴板粘贴，粘完管家会把手机原来的剪贴板放回去；以防万一也记下来，别同步回电脑
    if (req.kind === 'text' && req.text && !isASCII(req.text)) pushed.current = req.text
    await wsRef.current.input(id, req)
  }), [id])

  // 手机 → 电脑：键盘连着手机（窗口在前台）时，每 3 秒看一眼手机剪贴板，变了就放到电脑剪贴板
  const syncFromPhone = useMemo(() => async (announce: boolean) => {
    let text = ''
    try { text = (await getClipboard(id)).text } catch { return }
    if (seen.current === null) { seen.current = text; return } // 第一次只记下来，不动电脑剪贴板
    if (!text || text === seen.current || text === pushed.current) { seen.current = text; return }
    seen.current = text
    try {
      await navigator.clipboard.writeText(text)
      if (announce) message.success({ content: '手机里复制的内容已放到电脑剪贴板', key: 'pp-clip-sync', duration: 2 })
    } catch { /* 浏览器不让写（不是 https / 窗口不在前台）：下次再试 */ }
  }, [id, message])
  useEffect(() => {
    if (!enabled || !active) return
    void syncFromPhone(false)
    const t = setInterval(() => { if (document.hasFocus()) void syncFromPhone(true) }, 3000)
    return () => clearInterval(t)
  }, [enabled, active, syncFromPhone])

  const focus = () => { if (enabled) box.current?.focus({ preventScroll: true }) }

  return (
    <div className="pp-kbd" onPointerUp={focus}>
      {children}
      <textarea
        ref={box}
        className="pp-kbd-box"
        aria-label="用电脑键盘给手机打字"
        autoComplete="off" autoCorrect="off" autoCapitalize="off" spellCheck={false}
        onFocus={() => setActive(true)}
        onBlur={() => { queue.flush(); setActive(false) }}
        onKeyDown={(e) => {
          if (e.nativeEvent.isComposing || e.key === 'Process') return // 输入法正在拼字
          if (e.ctrlKey || e.metaKey) {
            const k = e.key.toLowerCase()
            if (k === 'v') return // 交给 onPaste
            const combo = comboNames[k]
            if (combo) {
              e.preventDefault()
              queue.key(combo)
              if (combo === 'copy' || combo === 'cut') void queue.then(async () => { await new Promise((r) => setTimeout(r, 300)); await syncFromPhone(true) })
            }
            return
          }
          const name = keyNames[e.key]
          if (name) { e.preventDefault(); queue.key(name) }
        }}
        onInput={(e) => {
          const ev = e.nativeEvent as InputEvent
          if (ev.isComposing) return
          if (ev.inputType === 'insertText' || ev.inputType === 'insertReplacementText') queue.text(ev.data ?? '')
          e.currentTarget.value = ''
        }}
        onCompositionEnd={(e) => { queue.text(e.data); e.currentTarget.value = '' }}
        onPaste={(e) => {
          e.preventDefault()
          const text = e.clipboardData.getData('text')
          if (!text) return
          pushed.current = text
          void queue.then(async () => {
            await wsRef.current.paste(id, text)
          })
        }}
      />
    </div>
  )
}
