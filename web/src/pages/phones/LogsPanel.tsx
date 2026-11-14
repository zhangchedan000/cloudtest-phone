import { ReloadOutlined } from '@ant-design/icons'
import { Button, Empty, Segmented, Space } from 'antd'
import { useCallback, useEffect, useState } from 'react'
import { logs } from '@/data/api'
import { useWorkspace } from '@/data/store'
import type { Phone } from '@/data/types'

const LEVELS = [{ value: 'E', label: '只看错误' }, { value: 'W', label: '警告和错误' }, { value: 'I', label: '全部（信息）' }]

/** 安卓日志（logcat）：排查开不了机、App 闪退。 */
export function LogsPanel({ phone }: { phone: Phone }) {
  const ws = useWorkspace()
  const [level, setLevel] = useState('W')
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const running = phone.status === 'running' || phone.status === 'starting'

  const load = useCallback(async () => {
    if (ws.mode !== 'live' || !running) return
    setLoading(true)
    try { setText(await logs(phone.id, level, 400)); setError('') } catch (e) { setError((e as Error).message) } finally { setLoading(false) }
  }, [ws.mode, running, phone.id, level])

  useEffect(() => { void load() }, [load])

  if (ws.mode !== 'live') return <Empty description="预览模式：接上管家程序后可看" />
  if (!running) return <Empty description="设备没开机" />
  return (
    <div className="pp-logs">
      <Space>
        <Segmented size="small" value={level} onChange={(v) => setLevel(String(v))} options={LEVELS} />
        <Button size="small" icon={<ReloadOutlined />} loading={loading} onClick={() => void load()}>刷新</Button>
      </Space>
      {error ? <p className="pp-small" style={{ color: 'var(--ant-color-error)' }}>{error}</p> : <pre className="pp-log-text">{text || '（没有日志）'}</pre>}
    </div>
  )
}
