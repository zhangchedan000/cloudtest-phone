import { App, Alert, Input, Modal, Radio, Space, Spin } from 'antd'
import { useEffect, useState } from 'react'
import { api, type Backup } from '@/data/api'
import type { Phone } from '@/data/types'

/**
 * 备份和恢复（2026-11-16 用户要求，参考魔云腾）。
 * - 备份：这台手机的全部数据（App、登录状态、相册）存一份。开着的会先关机，备份完自动开回来。
 * - 恢复：把一份备份放到这台手机（同安卓版本、同谷歌服务设置才行），这台现在的数据挪进回收站。
 * 都在服务器后台做，窗口标题旁边显示进度。
 */
export function BackupDialog({ phone, mode, onClose }: { phone: Phone; mode: 'backup' | 'restore' | null; onClose: () => void }) {
  const { message } = App.useApp()
  const [note, setNote] = useState('')
  const [list, setList] = useState<Backup[] | null>(null)
  const [pick, setPick] = useState<string>()
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (mode !== 'restore') return
    setList(null); setPick(undefined)
    api.backups(phone.serverId).then(setList, (e: Error) => { message.error(e.message); setList([]) })
  }, [mode, phone.serverId, message])

  const running = phone.status === 'running' || phone.status === 'starting'
  const usable = (list ?? []).filter((b) => b.androidVersion === phone.androidVersion && b.gapps === !!phone.gapps)
  const others = (list ?? []).length - usable.length

  const ok = async () => {
    setBusy(true)
    try {
      if (mode === 'backup') {
        await api.backup(phone.id, note)
        message.success('开始备份了，进度看窗口标题旁边')
      } else if (pick) {
        await api.restore(phone.id, pick)
        message.success('开始恢复了，进度看窗口标题旁边')
      }
      onClose()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal open={mode !== null} destroyOnHidden onCancel={onClose} onOk={() => void ok()} confirmLoading={busy}
      okText={mode === 'backup' ? '开始备份' : '恢复到这台'} okButtonProps={{ disabled: mode === 'restore' && !pick, danger: mode === 'restore' }}
      title={mode === 'backup' ? `备份 ${phone.name}` : `从备份恢复到 ${phone.name}`}>
      {mode === 'backup' && <Space direction="vertical" style={{ width: '100%' }}>
        <p style={{ margin: 0 }}>把这台手机的全部数据（装的 App、账号登录状态、相册、设置）存一份，以后可以恢复到这台，或者放到另一台同安卓版本的手机上。</p>
        {running && <Alert type="info" showIcon message="这台开着：会先关机再备份，备份完自动开回来（大概一两分钟，这段时间画面会断开）。" />}
        <Input placeholder="备注（可以不填），比如：登录好了谷歌账号" value={note} onChange={(e) => setNote(e.target.value)} maxLength={100} />
      </Space>}
      {mode === 'restore' && <Space direction="vertical" style={{ width: '100%' }}>
        <Alert type="warning" showIcon message="这台手机现在的数据会被换成备份里的（现在的数据挪进服务器回收站，恢复错了还能找回）。开着的会先关机，恢复完自动开回来。" />
        {list === null ? <Spin /> : usable.length === 0
          ? <p className="pp-muted">没有能恢复到这台的备份{others ? `（有 ${others} 份是别的安卓版本或谷歌服务设置不同，不能用）` : ''}。</p>
          : <Radio.Group value={pick} onChange={(e) => setPick(e.target.value as string)} style={{ width: '100%' }}>
            <Space direction="vertical" style={{ width: '100%' }}>
              {usable.map((b) => <Radio key={b.id} value={b.id}>
                <b>{b.phoneName}</b>{b.phoneId === phone.id ? '（就是这台）' : ''} · {new Date(b.createdAt).toLocaleString('zh-CN', { hour12: false })} · {b.sizeMB >= 1024 ? `${(b.sizeMB / 1024).toFixed(1)} GB` : `${Math.round(b.sizeMB)} MB`}
                {b.note && <span className="pp-muted"> · {b.note}</span>}
              </Radio>)}
            </Space>
          </Radio.Group>}
        {pick && usable.find((b) => b.id === pick)?.phoneId !== phone.id && <Alert type="info" showIcon message="这份备份来自另一台手机：会带上那台的 App 登录状态和 App 里的设备标识，适合换手机、搬数据；同一个账号不要在两台上同时用。" />}
      </Space>}
    </Modal>
  )
}
