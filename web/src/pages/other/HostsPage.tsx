import { TrashCard } from './TrashCard'
import { Alert, Card, Descriptions, Progress, Tag } from 'antd'
import { useWorkspace } from '@/data/store'

/** 机器：每台 ARM 机器上跑一个管家程序。一个页面管所有机器，不用每个盒子开一个后台。 */
export function HostsPage() {
  const ws = useWorkspace()
  return (
    <div className="pp-page">
      <div className="pp-grid pp-grid-wide">
        {ws.hosts.map((h) => {
          const phones = ws.phones.filter((p) => p.hostId === h.id)
          const running = phones.filter((p) => p.status === 'running').length
          return (
            <Card key={h.id} size="small" title={h.name} extra={h.online ? <Tag color="green">在线</Tag> : <Tag>离线</Tag>}>
              {h.gpuFamily === 'software' && <Alert type="info" showIcon className="pp-alert" title="开发机：没有显卡（软件渲染），只用于开发测试，不要登录真实账号。" />}
              <Descriptions column={1} size="small" items={[
                { label: '位置', children: h.location },
                { label: '架构 / 内核', children: `${h.arch} · ${h.kernel}` },
                { label: '显卡', children: h.gpu },
                { label: '云手机', children: `${phones.length} 台，运行中 ${running} / 同时最多 ${h.maxRunning}` }
              ]} />
              <div className="pp-meter"><span>内存</span><Progress percent={Math.round((h.memUsedGb / h.memGb) * 100)} format={() => `${h.memUsedGb} / ${h.memGb} GB`} /></div>
              <div className="pp-meter"><span>硬盘</span><Progress percent={Math.round((h.diskUsedGb / h.diskGb) * 100)} format={() => `${h.diskUsedGb} / ${h.diskGb} GB`} /></div>
            </Card>
          )
        })}
      </div>
      <TrashCard />
    </div>
  )
}
