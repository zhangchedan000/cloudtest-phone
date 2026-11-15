import { describe, expect, it } from 'vitest'
import { adbCommands, adbPort, sshHost } from './AdbDialog'

describe('ADB 连接命令', () => {
  it('每台手机的本机端口不一样', () => {
    expect(adbPort('10.88.1.2')).toBe(20258)
    expect(adbPort('10.88.1.3')).toBe(20259)
    expect(adbPort('10.88.2.2')).toBe(20514)
    expect(adbPort('不是IP')).toBeUndefined()
  })

  it('用域名打开时 SSH 用这个域名，本机隧道打开时给占位', () => {
    expect(sshHost('phone.peachshop.cyou')).toBe('phone.peachshop.cyou')
    expect(sshHost('127.0.0.1')).toBe('服务器IP')
    expect(sshHost('localhost')).toBe('服务器IP')
  })

  it('生成两条命令', () => {
    expect(adbCommands('10.88.1.2', 'phone.peachshop.cyou')).toEqual({
      port: 20258,
      tunnel: 'ssh -N -L 20258:10.88.1.2:5555 root@phone.peachshop.cyou',
      connect: 'adb connect 127.0.0.1:20258'
    })
  })
})
