/**
 * 电脑键盘直接打字进手机（2026-11-16 用户要求：用电脑键盘打字，和用手机屏幕上的键盘一样）。
 *
 * 手机窗口里放一个看不见的输入框，点一下手机画面它就拿到键盘焦点：
 * - 打的字（英文、电脑输入法打出来的中文）攒一小会儿一起发，安卓那边一次 `input text` / 粘贴；
 * - 回车、退格、Tab、方向键、Esc（= 返回）等按键马上发；
 * - Ctrl+A / C / X / Z 发给手机；Ctrl+V 把电脑剪贴板里的内容粘贴进手机（浏览器直接给，不用授权）。
 * 文字和按键严格按顺序一个一个发（上一个发完再发下一个），不会乱序。
 */
import type { InputRequest } from '@/data/api'

/** 浏览器按键 → 管家的按键名（不在表里的按键不拦，交给输入框照常处理）。 */
export const keyNames: Record<string, string> = {
  Enter: 'enter', Backspace: 'delete', Delete: 'forward_delete', Tab: 'tab', Escape: 'back',
  ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right',
  Home: 'move_home', End: 'move_end', PageUp: 'page_up', PageDown: 'page_down'
}

/** Ctrl（Mac 上是 Command）+ 字母 → 管家的组合键名。Ctrl+V 单独处理（走粘贴）。 */
export const comboNames: Record<string, string> = { a: 'select_all', c: 'copy', x: 'cut', z: 'undo', y: 'redo' }

export type Send = (req: InputRequest) => Promise<void>

/** 按顺序发送；连续打的字攒 delayMs 毫秒合成一次（安卓 input 命令每次要几百毫秒，逐字发太慢）。 */
export class KeyQueue {
  private pending = ''
  private timer: ReturnType<typeof setTimeout> | undefined
  private chain: Promise<void> = Promise.resolve()

  constructor(private send: Send, private delayMs = 120) {}

  /** 打字。 */
  text(s: string) {
    if (!s) return
    this.pending += s
    clearTimeout(this.timer)
    this.timer = setTimeout(() => this.flush(), this.delayMs)
  }

  /** 按键：先把攒着的字发出去，保证顺序。 */
  key(name: string) {
    this.flush()
    this.enqueue({ kind: 'key', key: name } as InputRequest)
  }

  /** 立刻把攒着的字发出去。 */
  flush() {
    clearTimeout(this.timer)
    this.timer = undefined
    if (!this.pending) return
    const text = this.pending
    this.pending = ''
    // 安卓 input text 一次最多 500 字节，长的拆开发
    for (const part of splitText(text, 400)) this.enqueue({ kind: 'text', text: part } as InputRequest)
  }

  /** 排在前面的都发完以后再做 fn（比如粘贴、读剪贴板）。 */
  then(fn: () => Promise<void>): Promise<void> {
    this.flush()
    this.chain = this.chain.then(fn, fn)
    return this.chain
  }

  private enqueue(req: InputRequest) {
    this.chain = this.chain.then(() => this.send(req)).catch(() => undefined)
  }
}

/** 按字符（不切断中文、表情）拆成每段不超过 maxBytes 字节（UTF-8）。 */
export function splitText(text: string, maxBytes: number): string[] {
  const out: string[] = []
  let cur = ''
  let bytes = 0
  for (const ch of text) {
    const b = new TextEncoder().encode(ch).length
    if (bytes + b > maxBytes && cur) {
      out.push(cur)
      cur = ''
      bytes = 0
    }
    cur += ch
    bytes += b
  }
  if (cur) out.push(cur)
  return out
}
