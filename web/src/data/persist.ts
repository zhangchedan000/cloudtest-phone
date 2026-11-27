import { useState } from 'react'

/**
 * usePersisted：和 useState 用法一样，但值会存到 localStorage，关了重开自动恢复（2026-11-27 用户要求：
 * 软件里凡是调过的设置都要记住，不能每次都回到默认）。
 * 用于视图模式、窗口/投屏大小滑块、分组筛选、刷新间隔、停在哪页等「偏好」，不用于一次性状态（勾选、弹窗开关）。
 * 无痕模式 / 存不了时自动退回内存态，不报错。
 */
export function usePersisted<T>(key: string, initial: T): [T, (v: T | ((prev: T) => T)) => void] {
  const [val, setVal] = useState<T>(() => {
    try {
      const s = localStorage.getItem(key)
      if (s != null) return JSON.parse(s) as T
    } catch { /* 无痕模式等：用默认 */ }
    return initial
  })
  const set = (v: T | ((prev: T) => T)) => {
    setVal((prev) => {
      const next = typeof v === 'function' ? (v as (prev: T) => T)(prev) : v
      try { localStorage.setItem(key, JSON.stringify(next)) } catch { /* 忽略 */ }
      return next
    })
  }
  return [val, set]
}
