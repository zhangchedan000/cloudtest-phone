import { Select } from 'antd'
import { LOCAL } from '@/data/api'
import { useWorkspace } from '@/data/store'

/**
 * 选服务器（多台服务器时才显示）：代理库、分组、回收站每台服务器各一份。
 * 只有本机一台时返回 null，页面看起来和以前一样。
 */
export function ServerSelect({ value, onChange }: { value: string; onChange: (serverId: string) => void }) {
  const ws = useWorkspace()
  if (!ws.servers.length) return null
  const online = (sid: string) => ws.hosts.some((h) => (h.serverId ?? LOCAL) === sid && h.online)
  return (
    <Select
      style={{ width: 200 }}
      value={value}
      onChange={onChange}
      options={[LOCAL, ...ws.servers.map((s) => s.id)].map((sid) => ({
        value: sid,
        label: `服务器：${ws.serverName(sid)}${online(sid) ? '' : '（离线）'}`,
        disabled: !online(sid)
      }))}
    />
  )
}

/** 某台服务器上的东西（没标服务器的算本机）。 */
export function onServer<T extends { serverId?: string }>(list: T[], serverId: string): T[] {
  return list.filter((x) => (x.serverId ?? LOCAL) === serverId)
}
