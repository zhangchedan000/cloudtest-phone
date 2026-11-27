import { CloudDownloadOutlined, LoadingOutlined } from '@ant-design/icons'
import { App, Button, Modal } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { api } from '@/data/api'
import { useWorkspace } from '@/data/store'

/**
 * 在线升级（2026-11-27 用户要求：做一个一键升级，以后不用手动上板）。
 * 左侧栏底部一个「升级」按钮：有新版本时点一下角标；弹窗里显示当前 / 最新版本，一键升级。
 * 升级时管家会下载新程序、校验、后台换新重启；页面这边轮询，等新版本起来就自动刷新。
 */
export function UpdateButton() {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [open, setOpen] = useState(false)
  const [st, setSt] = useState<{ current: string; latest?: string; canUpdate: boolean; note?: string } | null>(null)
  const [loading, setLoading] = useState(false)
  const [phase, setPhase] = useState<'idle' | 'upgrading'>('idle')
  const [hasNew, setHasNew] = useState(false)
  const poll = useRef<number | undefined>(undefined)

  const live = ws.mode === 'live'

  // 开着就悄悄查一次有没有新版本（给角标）；连不上 GitHub 就当没有，不打扰。
  useEffect(() => {
    if (!live) return
    let dead = false
    void api.updateStatus().then((r) => { if (!dead) setHasNew(!!r.canUpdate) }).catch(() => {})
    return () => { dead = true }
  }, [live])

  useEffect(() => () => { if (poll.current) window.clearInterval(poll.current) }, [])

  const refresh = () => {
    setLoading(true)
    void api.updateStatus().then((r) => { setSt(r); setHasNew(!!r.canUpdate); if (r.upgrading) setPhase('upgrading') })
      .catch((e: Error) => setSt({ current: '?', canUpdate: false, note: e.message }))
      .finally(() => setLoading(false))
  }

  const onOpen = () => {
    if (!live) { message.info('预览模式：连上板子后才能升级'); return }
    setOpen(true); setSt(null); refresh()
  }

  // 开始升级：下发后，管家会重启；这边每 3 秒问一次版本，等新版本起来就刷新页面。
  const start = () => {
    setLoading(true)
    void api.startUpdate().then((r) => {
      message.success(`开始升级：${r.from || '当前'} → ${r.to}`)
      setPhase('upgrading')
      const target = r.to
      let misses = 0
      poll.current = window.setInterval(() => {
        void api.updateStatus().then((s) => {
          misses = 0
          // 新版本起来了（版本号变成目标版本，或不再是旧版本）
          if (s.current && s.current === target) {
            window.clearInterval(poll.current)
            message.success('升级完成，正在刷新…')
            setTimeout(() => window.location.reload(), 1200)
          }
        }).catch(() => {
          // 管家重启中，连不上是正常的；连丢太久也不慌，继续等
          misses++
        })
      }, 3000)
    }).catch((e: Error) => { message.error(e.message); setPhase('idle') }).finally(() => setLoading(false))
  }

  return (
    <>
      <button className="pp-rail-item" onClick={onOpen}>
        <span className="pp-rail-ico">
          {phase === 'upgrading' ? <LoadingOutlined /> : <CloudDownloadOutlined />}
          {hasNew && phase === 'idle' && <span className="pp-rail-dot" />}
        </span>
        <span className="pp-rail-label">升级</span>
      </button>
      <Modal open={open} onCancel={() => setOpen(false)} footer={null} destroyOnHidden title="在线升级" width={420}>
        {phase === 'upgrading'
          ? <div style={{ padding: '12px 0' }}>
            <p><LoadingOutlined /> 正在升级，大约 20~60 秒。管家会自己换新、重启、体检；新版本起来后页面会<b>自动刷新</b>。</p>
            <p className="pp-muted pp-small">这期间管理页可能会断一下，是正常的，不用关窗口。运行中的手机不受影响。</p>
          </div>
          : loading && !st
            ? <div className="pp-center" style={{ padding: 24 }}><LoadingOutlined /> 查询中…</div>
            : <div style={{ padding: '4px 0' }}>
              <p>当前版本：<b>{st?.current || '?'}</b></p>
              {st?.latest && <p>最新版本：<b>{st.latest}</b></p>}
              {st?.canUpdate
                ? <p style={{ color: '#1677ff' }}>有新版本，点下面升级。</p>
                : <p className="pp-muted">{st?.note || '已是最新版本。'}</p>}
              <div style={{ marginTop: 14, display: 'flex', gap: 8 }}>
                <Button type="primary" icon={<CloudDownloadOutlined />} loading={loading} disabled={!st?.canUpdate} onClick={start}>
                  立即升级
                </Button>
                <Button onClick={refresh} loading={loading}>重新检查</Button>
              </div>
              <p className="pp-muted pp-small" style={{ marginTop: 12 }}>
                升级只换管家程序本身（管理界面跟着更新），不动手机、不动代理、不动数据。新版本起不来会自动换回旧的。
              </p>
            </div>}
      </Modal>
    </>
  )
}
