import { APP_PREFIX } from './api'
import type { ProfileJob } from '@/types/api'

export function streamProfileJob(
  jobId: string,
  onUpdate: (job: ProfileJob) => void,
  signal?: AbortSignal,
): Promise<ProfileJob> {
  return new Promise((resolve, reject) => {
    const source = new EventSource(`${APP_PREFIX}/api/jobs/${encodeURIComponent(jobId)}/stream`)
    let finished = false

    const close = () => {
      if (finished) return
      finished = true
      source.close()
      signal?.removeEventListener('abort', abort)
    }
    const abort = () => {
      close()
      reject(new DOMException('The operation was aborted', 'AbortError'))
    }

    source.onmessage = event => {
      try {
        const job = JSON.parse(event.data) as ProfileJob
        onUpdate(job)
        if (job.state === 'done' || job.state === 'failed') {
          close()
          resolve(job)
        }
      } catch {
        close()
        reject(new Error('后台任务返回了无效状态'))
      }
    }
    source.onerror = () => {
      if (!finished) onUpdate({ jobId, state: 'running', message: '任务状态连接暂时中断，正在自动重连…' })
    }

    if (signal?.aborted) abort()
    else signal?.addEventListener('abort', abort, { once: true })
  })
}
