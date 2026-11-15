import { DeleteOutlined } from '@ant-design/icons'
import { App, Button, ColorPicker, Input, Modal, Space, Table } from 'antd'
import { useState } from 'react'
import { LOCAL } from '@/data/api'
import { onServer, ServerSelect } from '@/components/ServerSelect'
import { useWorkspace } from '@/data/store'
import type { Group } from '@/data/types'

/** 分组管理：新建、改名、改颜色、删除（组里的设备变成未分组）。 */
export function GroupsModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const ws = useWorkspace()
  const { message } = App.useApp()
  const [name, setName] = useState('')
  const [sid, setSid] = useState(LOCAL)
  const count = (id: string) => onServer(ws.phones, sid).filter((p) => p.groupId === id).length
  const add = async () => {
    if (!name.trim()) return
    await ws.saveGroup({ name: name.trim(), color: '#2f6bff', serverId: sid })
    setName('')
  }
  return (
    <Modal open={open} title="分组管理" footer={null} onCancel={onClose} destroyOnHidden>
      <div style={{ marginBottom: 12 }}><ServerSelect value={sid} onChange={setSid} /></div>
      <Space.Compact style={{ width: '100%', marginBottom: 12 }}>
        <Input value={name} placeholder="新分组名称，例如：店铺 A" onChange={(e) => setName(e.target.value)} onPressEnter={() => void add()} />
        <Button type="primary" onClick={() => void add()}>新建</Button>
      </Space.Compact>
      <Table<Group>
        rowKey="id"
        size="small"
        pagination={false}
        dataSource={onServer(ws.groups, sid)}
        columns={[
          { title: '颜色', width: 60, render: (_, g) => <ColorPicker size="small" value={g.color} onChangeComplete={(c) => void ws.saveGroup({ ...g, color: c.toHexString() })} /> },
          { title: '名称', render: (_, g) => <Input size="small" defaultValue={g.name} onBlur={(e) => { const v = e.target.value.trim(); if (v && v !== g.name) void ws.saveGroup({ ...g, name: v }); else if (!v) message.warning('分组名不能为空') }} /> },
          { title: '设备', width: 60, render: (_, g) => `${count(g.id)} 台` },
          { title: '', width: 50, render: (_, g) => <Button size="small" danger icon={<DeleteOutlined />} onClick={() => Modal.confirm({ title: `删除分组「${g.name}」？`, content: count(g.id) ? `组里的 ${count(g.id)} 台设备会变成未分组，设备本身不受影响。` : undefined, okButtonProps: { danger: true }, onOk: () => ws.removeGroup(g.id, sid) })} /> }
        ]}
      />
    </Modal>
  )
}
