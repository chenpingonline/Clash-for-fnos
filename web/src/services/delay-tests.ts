import { APP_PREFIX } from './api'

export type DelayTestResult = {
  name: string
  delay: number
  state: 'done' | 'timeout' | 'error'
  error?: string
}

function parseResult(line: string): DelayTestResult {
  const value: unknown = JSON.parse(line)
  if (!value || typeof value !== 'object') throw new Error('批量测速返回了无效数据')
  const result = value as Partial<DelayTestResult>
  if (typeof result.name !== 'string' || !['done', 'timeout', 'error'].includes(result.state || '')) {
    throw new Error('批量测速返回了无效数据')
  }
  return {
    name: result.name,
    delay: typeof result.delay === 'number' ? result.delay : 0,
    state: result.state as DelayTestResult['state'],
    ...(typeof result.error === 'string' ? { error: result.error } : {}),
  }
}

function responseError(body: string, status: number): Error {
  try {
    const payload = JSON.parse(body) as { error?: unknown; message?: unknown }
    const message = typeof payload.error === 'string' ? payload.error : typeof payload.message === 'string' ? payload.message : ''
    if (message) return new Error(message)
  } catch { /* Use the response text below. */ }
  return new Error(body.trim() || `批量测速失败（HTTP ${status}）`)
}

export async function testDelayBatch(
  names: string[],
  onResult: (result: DelayTestResult) => void,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetch(`${APP_PREFIX}/api/delays`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ names: [...new Set(names)] }),
    signal,
  })
  if (!response.ok) throw responseError(await response.text(), response.status)
  if (!response.body) throw new Error('当前环境不支持流式测速结果')

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  while (true) {
    const { done, value } = await reader.read()
    buffer += decoder.decode(value, { stream: !done })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''
    for (const line of lines) if (line.trim()) onResult(parseResult(line))
    if (done) break
  }
  if (buffer.trim()) onResult(parseResult(buffer))
}
