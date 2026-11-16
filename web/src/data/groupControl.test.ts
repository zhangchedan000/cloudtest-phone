import { describe, expect, it } from 'vitest'
import { scaleInput, screenOf } from './groupControl'

describe('群控坐标换算', () => {
  it('点击、滑动按屏幕比例换算，按键和文字原样', () => {
    const from = { w: 1080, h: 2400 }, to = { w: 1220, h: 2712 }
    expect(scaleInput({ kind: 'tap', x: 540, y: 1200 }, from, to)).toMatchObject({ x: 610, y: 1356 })
    expect(scaleInput({ kind: 'swipe', x: 0, y: 2400, x2: 1080, y2: 0, durationMs: 300 }, from, to)).toMatchObject({ x: 0, y: 2712, x2: 1220, y2: 0, durationMs: 300 })
    expect(scaleInput({ kind: 'text', text: '你好' }, from, to)).toEqual({ kind: 'text', text: '你好' })
  })
  it('机型分辨率', () => {
    expect(screenOf({ resolution: { w: 1080, h: 2460 } })).toEqual({ w: 1080, h: 2460 })
    expect(screenOf({})).toEqual({ w: 1080, h: 2400 })
  })
})
