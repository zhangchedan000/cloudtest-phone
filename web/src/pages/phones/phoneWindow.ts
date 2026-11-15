import type { Phone } from '@/data/types'

/**
 * 每台手机单独弹出一个浏览器窗口（2026-11-15 用户要求：像竞品那样，开一台弹一个窗口，可以同时操作好几台，主页面照常用）。
 * 同一台手机再点一次：已经开着的窗口会被带到前面，不会重复开。
 * 浏览器拦了弹窗返回 false，调用方改在页面里打开并提示。
 */
export function openPhoneWindow(phone: Pick<Phone, 'id'>): boolean {
  const w = Math.min(480, screen.availWidth)
  const h = Math.min(960, screen.availHeight - 40)
  const win = window.open(`${location.pathname}#window=${phone.id}`, `pp-phone-${phone.id}`, `popup,width=${w},height=${h}`)
  if (!win) return false
  win.focus()
  return true
}

/** 当前页面是不是某台手机的弹出窗口（#window=<id>）。 */
export function windowPhoneId(hash = location.hash): string | undefined {
  const m = /^#window=([\w-]+)$/.exec(hash)
  return m?.[1]
}
