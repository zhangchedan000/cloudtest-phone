import { describe, expect, it, vi } from 'vitest'
import type { InputRequest } from '@/data/api'
import { KeyQueue, splitText } from './keyboardBridge'

describe('KeyQueue', () => {
  it('连续打的字合成一次发，按键前先把字发出去，顺序不乱', async () => {
    vi.useFakeTimers()
    const sent: InputRequest[] = []
    const q = new KeyQueue(async (r) => { sent.push(r) }, 100)
    q.text('he'); q.text('llo')
    q.key('enter')
    q.text('你好')
    await vi.advanceTimersByTimeAsync(150)
    await q.then(async () => undefined)
    expect(sent).toEqual([
      { kind: 'text', text: 'hello', human: true },
      { kind: 'key', key: 'enter' },
      { kind: 'text', text: '你好', human: true }
    ])
    vi.useRealTimers()
  })

  it('一次发失败不影响后面的', async () => {
    const sent: string[] = []
    const q = new KeyQueue(async (r) => { if (r.key === 'tab') throw new Error('x'); sent.push(r.key ?? r.text ?? '') }, 0)
    q.key('tab'); q.key('enter')
    await q.then(async () => undefined)
    expect(sent).toEqual(['enter'])
  })
})

describe('splitText', () => {
  it('按字节拆，不切断中文', () => {
    expect(splitText('ab你好c', 4)).toEqual(['ab', '你', '好c'])
    expect(splitText('', 4)).toEqual([])
  })
})
