/**
 * 群控（2026-11-16 用户要求，参考魔云腾的「一控多」）：在一台手机窗口里的点击、滑动、按键、打字、粘贴，
 * 同时发给勾选的其他手机。各台分辨率可能不同，坐标按屏幕比例换算。
 */
import type { InputRequest } from './api'

export interface ScreenSize { w: number; h: number }

/** 机型档案里的分辨率（没有就按 1080×2400 算）。 */
export function screenOf(values: Record<string, unknown> | undefined): ScreenSize {
  const r = values?.resolution as { w?: unknown; h?: unknown } | undefined
  const w = Number(r?.w), h = Number(r?.h)
  return w > 0 && h > 0 ? { w, h } : { w: 1080, h: 2400 }
}

/** 把主控手机上的一次操作换算到另一台手机（坐标按比例，其他原样）。 */
export function scaleInput(req: InputRequest, from: ScreenSize, to: ScreenSize): InputRequest {
  if (req.kind !== 'tap' && req.kind !== 'swipe') return req
  // 横屏时画面宽高和机型的竖屏宽高是反的
  if (req.landscape) { from = { w: from.h, h: from.w }; to = { w: to.h, h: to.w } }
  const sx = (v?: number) => (v === undefined ? v : Math.round((v * to.w) / from.w))
  const sy = (v?: number) => (v === undefined ? v : Math.round((v * to.h) / from.h))
  return { ...req, x: sx(req.x), y: sy(req.y), x2: sx(req.x2), y2: sy(req.y2) }
}
