import { App, Button, Modal, Space, Typography } from 'antd'

/**
 * ADB 连接命令（2026-11-15 用户要求）。
 * 云手机的 ADB 不要授权（踩坑 #33），所以不对外开放，只能经 SSH 隧道：
 *   1. ssh -N -L <本机端口>:<手机内部 IP>:5555 root@<服务器>   （窗口开着）
 *   2. adb connect 127.0.0.1:<本机端口>
 * 本机端口按手机 IP 算（10.88.a.b → 20000 + a×256 + b），每台不一样，几台可以同时连。
 */
export function adbPort(ip: string): number | undefined {
  const m = /^\d+\.\d+\.(\d+)\.(\d+)$/.exec(ip)
  if (!m) return undefined
  return 20000 + Number(m[1]) * 256 + Number(m[2])
}

/** SSH 登录的服务器地址：用域名打开的就用这个域名（DNS 指向服务器）；经本机隧道打开的没法知道，给个占位。 */
export function sshHost(hostname: string): string {
  return /^(localhost|127\.|\[?::1\]?$)/.test(hostname) || hostname === '' ? '服务器IP' : hostname
}

export function adbCommands(ip: string, hostname: string): { tunnel: string; connect: string; port: number } | undefined {
  const port = adbPort(ip)
  if (!port) return undefined
  return {
    port,
    tunnel: `ssh -N -L ${port}:${ip}:5555 root@${sshHost(hostname)}`,
    connect: `adb connect 127.0.0.1:${port}`
  }
}

export function AdbDialog({ open, ip, name, onClose }: { open: boolean; ip?: string; name: string; onClose: () => void }) {
  const { message } = App.useApp()
  const cmds = ip ? adbCommands(ip, window.location.hostname) : undefined
  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      message.success('已复制')
    } catch {
      message.warning('浏览器不允许写剪贴板，请手动选中复制')
    }
  }
  return (
    <Modal open={open} title={`ADB 连接：${name}`} onCancel={onClose} footer={null} width={640} destroyOnHidden>
      {!cmds ? (
        <p>还拿不到这台手机的内部地址（开机后才有）。</p>
      ) : (
        <>
          <p>在你自己的电脑上按顺序运行（要能 SSH 登录服务器）：</p>
          <p className="pp-small pp-muted">① 开隧道（这个窗口保持开着，关掉就断开）</p>
          <Space.Compact style={{ width: '100%' }}>
            <Typography.Text code style={{ flex: 1, padding: '6px 8px', whiteSpace: 'nowrap', overflowX: 'auto' }}>{cmds.tunnel}</Typography.Text>
            <Button onClick={() => void copy(cmds.tunnel)}>复制</Button>
          </Space.Compact>
          <p className="pp-small pp-muted" style={{ marginTop: 12 }}>② 另开一个窗口连接</p>
          <Space.Compact style={{ width: '100%' }}>
            <Typography.Text code style={{ flex: 1, padding: '6px 8px' }}>{cmds.connect}</Typography.Text>
            <Button onClick={() => void copy(cmds.connect)}>复制</Button>
          </Space.Compact>
          <Button type="primary" style={{ marginTop: 16 }} onClick={() => void copy(`${cmds.tunnel}\n${cmds.connect}`)}>两条一起复制</Button>
          <p className="pp-small pp-muted" style={{ marginTop: 12 }}>
            连上以后 adb shell、adb install、scrcpy -s 127.0.0.1:{cmds.port} 都能用。每台手机的本机端口不一样，可以同时连几台。
            <br />
            ⚠️ 云手机的 ADB 连接时不需要在手机上点「允许」，谁连上谁就能完全控制：只能走 SSH 隧道，不要在防火墙或反代里开放 5555。
          </p>
        </>
      )}
    </Modal>
  )
}
