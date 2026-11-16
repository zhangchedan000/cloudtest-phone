import { DeleteOutlined, ReloadOutlined } from '@ant-design/icons'
import { App, Button, Card, Empty, Modal, Space, Table } from 'antd'
import { useCallback, useEffect, useState } from 'react'
import { api, type Backup } from '@/data/api'
import { useWorkspace } from '@/data/store'

/** 备份列表（在手机窗口「更多 → 备份这台手机」做；恢复在「更多 → 从备份恢复」）。备份不会自动删，占空间了在这里删。 */
export function BackupsCard({ serverId, title }: { serverId?: string; title?: string } = {}) {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [items, setItems] = useState<Backup[]>([])
  const [loading, setLoading] = useState(false)
  const load = useCallback(async () => {
    if (ws.mode !== 'live') return
    setLoading(true)
    try { setItems(await api.backups(serverId)) } catch (e) { message.error((e as Error).message) } finally { setLoading(false) }
  }, [ws.mode, message, serverId])
  useEffect(() => { void load() }, [load])

  const total = items.reduce((n, it) => n + it.sizeMB, 0)
  const size = (mb: number) => (mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${Math.round(mb)} MB`)
  const remove = (b: Backup) => Modal.confirm({
    title: `删除 ${b.phoneName} 在 ${new Date(b.createdAt).toLocaleString('zh-CN', { hour12: false })} 的备份？`,
    content: '删除后不能找回。',
    okButtonProps: { danger: true },
    onOk: async () => { await api.deleteBackup(b.id, serverId).catch((e: Error) => message.error(e.message)); await load() }
  })

  return (
    <Card
      title={`${title ? `${title} · ` : ''}备份（${items.length} 份，${size(total)}）`}
      extra={<Button size="small" icon={<ReloadOutlined />} loading={loading} onClick={() => void load()} />}
    >
      <p className="pp-muted pp-small">在手机窗口「更多 → 备份这台手机」做备份，「更多 → 从备份恢复」恢复到同安卓版本的手机。备份不会自动删除。</p>
      {ws.mode !== 'live' ? <Empty description="预览模式：接上管家程序后可看" /> : (
        <Table<Backup>
          rowKey="id"
          size="small"
          pagination={false}
          dataSource={items}
          columns={[
            { title: '手机', render: (_, b) => <Space size={4}>{b.phoneName}{!ws.phones.some((p) => p.id === b.phoneId) && <span className="pp-muted">（已删除）</span>}</Space> },
            { title: '系统', render: (_, b) => `Android ${b.androidVersion}${b.gapps ? ' · 谷歌服务' : ''}` },
            { title: '时间', render: (_, b) => new Date(b.createdAt).toLocaleString('zh-CN', { hour12: false }) },
            { title: '大小', render: (_, b) => size(b.sizeMB) },
            { title: '备注', render: (_, b) => b.note ?? '' },
            { title: '', width: 50, render: (_, b) => <Button size="small" danger icon={<DeleteOutlined />} onClick={() => remove(b)} /> }
          ]}
        />
      )}
    </Card>
  )
}
