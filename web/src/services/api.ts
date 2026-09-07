import type { ApiErrorPayload } from '@/types/api'

export const APP_PREFIX = location.pathname.startsWith('/app/clash-for-fnos')
  ? '/app/clash-for-fnos'
  : ''

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`${APP_PREFIX}${path}`, options)
  const contentType = response.headers.get('content-type') ?? ''
  const data: unknown = contentType.includes('json')
    ? await response.json()
    : await response.text()

  if (!response.ok) {
    const payload = typeof data === 'object' && data !== null ? data as ApiErrorPayload : null
    throw new Error(payload?.error ?? payload?.message ?? String(data || `HTTP ${response.status}`))
  }
  if (path.startsWith('/api/') && !contentType.includes('json')) {
    throw new Error('接口返回了非 JSON 响应，请检查 fnOS Gateway 与应用服务状态')
  }
  return data as T
}

export const jsonRequest = (method: string, body?: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  ...(body === undefined ? {} : { body: JSON.stringify(body) }),
})

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException
    ? error.name === 'AbortError'
    : Boolean(error && typeof error === 'object' && 'name' in error && error.name === 'AbortError')
}
