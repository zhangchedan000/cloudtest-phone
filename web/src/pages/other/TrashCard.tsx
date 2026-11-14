import { DeleteOutlined, ReloadOutlined } from '@ant-design/icons'
import { App, Button, Card, Empty, Modal, Space, Table } from 'antd'
import { useCallback, useEffect, useState } from 'react'
import { api, type TrashItem } from '@/data/api'
import { useWorkspace } from '@/data/store'

/** 回收站：删除、重置、刷机留下的旧数据。超过 30 天自动彻底删除（管家程序参数 -trash-days）。 */
export function TrashCard() {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [items, setItems] = useState<TrashItem[]>([])
  const [loading, setLoading] = useState(false)
  const load = useCallback(async () => {
    if (ws.mode !== 'live') return
    setLoading(true)
    try { setItems(await api.trash()) } catch (e) { message.error((e as Error).message) } finally { setLoading(false) }
  }, [ws.mode, message])
  useEffect(() => { void load() }, [load])

  const total = items.reduce((n, it) => n + it.sizeMB, 0)
  const nameOf = (id: string) => ws.phones.find((p) => p.id === id)?.name ?? `已删除的设备 ${id.slice(0, 6)}`
  const remove = (names: string[]) => Modal.confirm({
    title: names.length > 1 ? `彻底删除回收站里的 ${names.length} 份旧数据？` : '彻底删除这份旧数据？',
    content: '删除后不能找回。',
    okButtonProps: { danger: true },
    onOk: async () => {
      for (const name of names) await api.deleteTrash(name).catch((e: Error) => message.error(e.message))
      await load()
    }
  })

  return (
    <Card
      title={`回收站（${items.length} 份，${total >= 1024 ? `${(total / 1024).toFixed(1)} GB` : `${Math.round(total)} MB`}）`}
      extra={<Space><Button size="small" icon={<ReloadOutlined />} loading={loading} onClick={() => void load()} />{items.length > 0 && <Button size="small" danger onClick={() => remove(items.map((i) => i.name))}>全部清空</Button>}</Space>}
    >
      <p className="pp-muted pp-small">删除、重置、刷机时手机的旧数据先放这里，误操作还能找回；超过 30 天自动彻底删除。</p>
      {ws.mode !== 'live' ? <Empty description="预览模式：接上管家程序后可看" /> : (
        <Table<TrashItem>
          rowKey="name"
          size="small"
          pagination={false}
          dataSource={items}
          columns={[
            { title: '设备', render: (_, it) => nameOf(it.phoneId) },
            { title: '原因', render: (_, it) => (it.kind === 'reset' ? '重置 / 刷机' : '删除设备') },
            { title: '时间', render: (_, it) => new Date(it.at).toLocaleString() },
            { title: '大小', render: (_, it) => `${Math.round(it.sizeMB)} MB` },
            { title: '', width: 50, render: (_, it) => <Button size="small" danger icon={<DeleteOutlined />} onClick={() => remove([it.name])} /> }
          ]}
        />
      )}
    </Card>
  )
}
