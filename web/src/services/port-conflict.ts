export function portConflict(error: string) {
  if (!/address already in use|EADDRINUSE|端口占用：/i.test(error)) return null
  const match = error.match(/\b(tcp|udp)(?:[46])?\s+[^\s()]+:(\d{1,5})(?=[\s):]|$)/i)
  if (!match) return null
  const port = Number(match[2])
  if (port < 1 || port > 65535) return null
  const protocol = match[1]!.toLowerCase()
  return { port, protocol, command: `sudo ss -${protocol === 'udp' ? 'lunp' : 'ltnp'} 'sport = :${port}'` }
}
