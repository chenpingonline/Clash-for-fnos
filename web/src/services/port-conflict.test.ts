import { describe, expect, it } from 'vitest'
import { portConflict } from './port-conflict'

describe('port conflict diagnostics', () => {
  it.each([
    ['托管 Core 端口占用：Controller (tcp 127.0.0.1:9090): bind: address already in use', 9090, 'tcp', "sudo ss -ltnp 'sport = :9090'"],
    ['托管 Core 端口占用：mixed-port (udp 0.0.0.0:7890): address already in use', 7890, 'udp', "sudo ss -lunp 'sport = :7890'"],
    ['listen tcp [::]:7891: bind: address already in use', 7891, 'tcp', "sudo ss -ltnp 'sport = :7891'"],
  ])('extracts protocol and port from %s', (error, port, protocol, command) => {
    expect(portConflict(error as string)).toEqual({ port, protocol, command })
  })
  it.each([
    '托管 Core 无法监听 Controller (tcp 127.0.0.1:9090): permission denied',
    '托管 Core 无法监听 (tcp 192.0.2.1:9090): cannot assign requested address',
    '启动失败，请检查端口占用、配置及日志',
    '托管 Core 已停止',
    'listen tcp :65536: address already in use',
    'listen tcp :0: address already in use',
    'listen tcp :7890;rm: address already in use',
  ])('does not generate commands for %s', error => expect(portConflict(error)).toBeNull())
})
