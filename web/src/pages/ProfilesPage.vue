<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import BaseModal from '@/components/BaseModal.vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { formatBytes, formatTime, normalizeSubscriptionInfo } from '@/services/format'
import { notify } from '@/services/toast'
import type { LocalConfigCandidate, LocalDiscoveryResponse, LocalRuntime, ProfileItem, ProfileJob, ProfilesResponse } from '@/types/api'

type RemoteForm = { name: string; url: string; intervalMinutes: number; autoUpdate: boolean; autoApply: boolean }
const emptyForm = (): RemoteForm => ({ name: '', url: '', intervalMinutes: 360, autoUpdate: true, autoApply: false })
const loading = ref(true), error = ref(''), localLoading = ref(true), localError = ref(''), modal = ref<'remote' | 'file' | null>(null)
const items = ref<ProfileItem[]>([]), discovery = ref<LocalDiscoveryResponse>({}), editing = ref<ProfileItem | null>(null), busyId = ref('')
const profileJobs = reactive<Record<string, ProfileJob>>({})
const form = reactive<RemoteForm>(emptyForm()), fileName = ref('本地配置'), selectedFile = ref<File | null>(null)
const importedPaths = computed(() => new Set(items.value.map(item => item.sourcePath).filter(Boolean)))
let alive = true

function openRemote(item?: ProfileItem) {
  editing.value = item || null
  Object.assign(form, item ? { name: item.name, url: item.url || '', intervalMinutes: item.intervalMinutes ?? 360, autoUpdate: Boolean(item.autoUpdate), autoApply: Boolean(item.autoApply) } : emptyForm())
  modal.value = 'remote'
}
function downloadText(item: ProfileItem) {
  const info = item.lastDownload
  if (!info) return ''
  const attempts = info.attempts || []
  const summary = attempts.map(a => a.skipped ? `${a.label}: 跳过` : a.status ? `${a.label}: HTTP ${a.status}` : `${a.label}: ${a.error || '失败'}`).join('；')
  const ok = Boolean(info.method && info.method !== 'failed' && ((Number(info.status) >= 200 && Number(info.status) < 300) || Number(info.status) === 304))
  return ok ? `最近下载：${info.label || info.method} · ${info.unchanged ? '内容未变化 · ' : `HTTP ${info.status} · `}${Number(info.durationMs || 0)} ms` : `最近下载：${info.label || '更新失败'}${summary ? ` · ${summary}` : ''}`
}
function quota(item: ProfileItem) {
  const info = normalizeSubscriptionInfo(item.subscriptionInfo)
  if (!info) return null
  const used = info.upload + info.download, remain = info.total > 0 ? Math.max(0, info.total - used) : 0
  const percent = info.total > 0 ? Math.max(0, Math.min(100, used / info.total * 100)) : 0
  if (!info.expire) return { used, remain, total: info.total, percent, expire: '长期有效', className: '' }
  const ms = info.expire > 1e12 ? info.expire : info.expire * 1000, days = Math.ceil((ms - Date.now()) / 86_400_000)
  return { used, remain, total: info.total, percent, expire: days < 0 ? '已过期' : days === 0 ? '今天到期' : `${new Date(ms).toLocaleDateString()} · 剩余 ${days} 天`, className: days < 0 ? 'bad' : days <= 7 ? 'warn' : '' }
}
function candidateState(item: LocalConfigCandidate) { return item.readable ? '可读取' : item.permissionDenied || item.exists ? '无读取权限' : '未找到' }
function runtimeTitle(runtime: LocalRuntime) {
  if (runtime.mode === 'managed') return runtime.running ? 'Manager 正在托管 Mihomo Core' : '已选择 Manager 托管模式'
  if (runtime.mode === 'external') return runtime.running ? '本机 Mihomo Core 正在运行' : '已选择本机 Core 模式'
  return runtime.running ? '已检测到 Mihomo Core' : '正在自动检测 Mihomo Core'
}
function runtimeDetail(runtime: LocalRuntime) {
  if (!runtime.running) return runtime.message || '当前未检测到运行中的 Core，请到设置页面检查启动状态'
  const identity = [runtime.pid ? `PID ${runtime.pid}` : '', runtime.binaryVersion ? `v${runtime.binaryVersion.replace(/^v/, '')}` : ''].filter(Boolean).join(' · ')
  return [identity, runtime.configPath ? `配置 ${runtime.configPath}` : runtime.binaryPath].filter(Boolean).join(' · ')
}
function runtimeTag(runtime: LocalRuntime) { return runtime.mode === 'managed' ? '托管模式' : runtime.mode === 'external' ? '本机 Core' : '自动检测' }

async function loadProfiles() {
  try { const data = await api<ProfilesResponse>('/api/profiles'); if (alive) { items.value = data.items || []; error.value = '' } }
  catch (cause) { if (alive) error.value = errorMessage(cause) }
  finally { if (alive) loading.value = false }
}
async function scan() {
  localLoading.value = true
  try { discovery.value = await api<LocalDiscoveryResponse>('/api/local-config/discover'); localError.value = '' }
  catch (cause) { localError.value = errorMessage(cause) }
  finally { localLoading.value = false }
}
async function saveRemote() {
  if (!form.name.trim()) return notify('请输入订阅名称', true)
  if (!form.url.trim()) return notify('请输入订阅 URL', true)
  if (!Number.isFinite(form.intervalMinutes) || form.intervalMinutes < 5) return notify('更新间隔不能小于 5 分钟', true)
  busyId.value = 'remote'
  try {
    const payload = { ...form, name: form.name.trim(), url: form.url.trim() }
    await api(editing.value ? `/api/profiles/${editing.value.id}` : '/api/profiles', jsonRequest(editing.value ? 'PATCH' : 'POST', payload))
    notify(editing.value ? '订阅设置已保存' : '订阅已添加'); modal.value = null; await loadProfiles()
  } catch (cause) { notify(errorMessage(cause), true) }
  finally { busyId.value = '' }
}
async function importFile() {
  if (!selectedFile.value) return notify('请选择文件', true)
  busyId.value = 'file'
  try { await api('/api/profiles/import', jsonRequest('POST', { name: fileName.value.trim() || '本地配置', content: await selectedFile.value.text() })); modal.value = null; notify('已导入'); await loadProfiles() }
  catch (cause) { notify(errorMessage(cause), true) }
  finally { busyId.value = '' }
}
async function importNas(candidate: LocalConfigCandidate, apply: boolean) {
  if (!candidate.token) return
  busyId.value = `local-${candidate.token}-${apply}`
  try { const result = await api<{ sourcePath?: string }>('/api/local-config/import', jsonRequest('POST', { token: candidate.token, apply })); notify(`${apply ? '已导入并应用' : '已导入'}：${result.sourcePath || candidate.path}`); await Promise.all([loadProfiles(), scan()]) }
  catch (cause) { notify(errorMessage(cause), true) }
  finally { busyId.value = '' }
}
async function runProfileJob(item: ProfileItem, operation: 'update' | 'activate') {
  busyId.value = `${operation}-${item.id}`
  try {
    let job = await api<ProfileJob>(`/api/profiles/${item.id}/${operation}`, { method: 'POST' })
    if (!job.jobId) throw new Error('未获取到后台任务')
    profileJobs[item.id] = job
    while (alive) {
      if (job.state === 'done') {
        if (operation === 'update') {
          const dl = job.result?.lastDownload
          notify(dl?.unchanged ? `订阅没有变化 · ${Number(dl.durationMs || 0)} ms` : dl?.label ? `订阅更新完成 · ${dl.label} · ${Number(dl.durationMs || 0)} ms` : '订阅配置已安全更新')
        } else {
          notify(job.result?.unchanged ? '配置内容没有变化，已跳过重复应用' : `配置已应用并同步到 ${job.result?.target || '启动配置'} · ${Number(job.result?.durationMs || 0)} ms`)
        }
        window.setTimeout(() => { if (profileJobs[item.id]?.jobId === job.jobId) delete profileJobs[item.id] }, 1800)
        return
      }
      if (job.state === 'failed') {
        profileJobs[item.id] = { ...job, message: job.error ? `${job.message || '操作失败'}：${job.error}` : job.message }
        throw new Error(job.error || (operation === 'update' ? '订阅更新失败' : '配置应用失败'))
      }
      await new Promise(resolve => window.setTimeout(resolve, 500))
      try {
        job = await api<ProfileJob>(`/api/jobs/${job.jobId}`)
      } catch {
        profileJobs[item.id] = { ...job, state: 'running', message: '暂时无法读取任务状态，正在重试…' }
        await new Promise(resolve => window.setTimeout(resolve, 1500))
        continue
      }
      profileJobs[item.id] = job
    }
  } catch (cause) { notify(errorMessage(cause), true) }
  finally { busyId.value = ''; await loadProfiles() }
}
function update(item: ProfileItem) { return runProfileJob(item, 'update') }
function activate(item: ProfileItem) { return runProfileJob(item, 'activate') }
async function remove(item: ProfileItem) {
  if (!confirm('确定删除这个配置吗？')) return
  try { await api(`/api/profiles/${item.id}`, { method: 'DELETE' }); notify('已删除'); await loadProfiles() }
  catch (cause) { notify(errorMessage(cause), true) }
}
onMounted(() => Promise.all([loadProfiles(), scan()]))
onBeforeUnmount(() => { alive = false })
</script>

<template>
  <div class="card section"><div class="section-head"><div><h2>添加远程订阅</h2><p>下载完整 Mihomo/Clash YAML，应用时自动同步为 Mihomo 启动配置</p></div></div><div class="actions"><button class="small" @click="openRemote()">添加订阅</button><button class="ghost small" @click="modal = 'file'">从当前电脑导入 YAML</button></div></div>
  <div class="card section">
    <div class="section-head"><div><h2>配置列表</h2><p>本机配置与远程订阅统一管理</p></div></div>
    <AsyncState :loading="loading" :error="error">
      <div v-if="items.length" class="profile-list">
        <div v-for="item in items" :key="item.id" class="profile" :class="{ current: item.current }">
          <div>
            <div class="profile-title-line">
              <div class="profile-name">{{ item.current ? '● ' : '' }}{{ item.name }}</div>
            </div>
            <div class="profile-meta">{{ item.type === 'remote' ? '远程订阅' : '本地配置' }} · 更新：{{ formatTime(item.updatedAt) }}</div>
            <div v-if="item.lastError" class="profile-meta error-text">{{ item.lastError }}</div>
            <div v-if="downloadText(item)" class="download-info" :class="item.lastDownload?.method === 'failed' ? 'bad' : 'good'">{{ downloadText(item) }}</div>
          </div>
          <div class="profile-details">
            <template v-if="item.type === 'remote'">
              <template v-for="q in [quota(item)]" :key="item.id">
                <div v-if="q" class="subscription-info">
                  <template v-if="q.total">
                    <div class="quota-line"><span>已用 {{ formatBytes(q.used) }}</span><span>剩余 <strong>{{ formatBytes(q.remain) }}</strong> / {{ formatBytes(q.total) }}</span></div>
                    <div class="quota-track"><span :style="{ width: `${q.percent}%` }" /></div>
                  </template>
                  <div class="quota-expire" :class="q.className">{{ q.expire }}</div>
                </div>
              </template>
            </template>
            <div v-else class="profile-url" :title="item.sourcePath || ''">{{ item.sourcePath || '本地导入' }}</div>
          </div>
          <div class="actions">
            <button v-if="item.type === 'remote'" class="ghost small" :disabled="Boolean(busyId)" @click="update(item)">{{ busyId === `update-${item.id}` ? '处理中…' : '更新' }}</button>
            <button class="success small" :disabled="Boolean(busyId)" @click="activate(item)">{{ busyId === `activate-${item.id}` ? '应用中…' : '应用' }}</button>
            <button v-if="item.type === 'remote'" class="ghost small" :disabled="Boolean(busyId)" @click="openRemote(item)">编辑</button>
            <button class="danger small" :disabled="Boolean(busyId)" @click="remove(item)">删除</button>
          </div>
          <template v-for="job in [profileJobs[item.id]]" :key="job?.jobId || item.id">
            <div v-if="job" class="profile-operation-status profile-operation-row" :class="job.state" role="status" aria-live="polite">
              <span v-if="job.state === 'running'" class="profile-operation-spinner" />
              <span>{{ job.message }}</span>
            </div>
          </template>
        </div>
      </div>
      <div v-else class="empty">还没有配置</div>
    </AsyncState>
  </div>
  <div class="card section local-discovery-card"><div class="section-head"><div><h2>本机 Mihomo 配置</h2><p>自动读取当前 Mihomo 配置；用户文件仅从 fnOS 明确授权的目录读取</p></div><button class="ghost small" @click="scan">重新扫描</button></div><AsyncState :loading="localLoading" :error="localError"><div v-if="discovery.error" class="local-warning">扫描失败：{{ discovery.error }}</div><div class="local-access-summary" :class="discovery.authorizedPaths?.length ? 'active' : 'warn'"><strong>{{ discovery.authorizedPaths?.length ? `已授权 ${discovery.authorizedPaths.length} 个文件夹` : '尚未授权用户文件夹' }}</strong><span v-if="discovery.authorizedPaths?.length"><span v-for="path in discovery.authorizedPaths" :key="path" class="mono">{{ path }}</span></span><span v-else>如需从 NAS 共享目录导入 YAML，请到 fnOS「系统设置 → 应用 → Clash for fnos → 访问权限」添加文件夹。</span></div><div class="local-processes"><div v-if="discovery.runtime" class="local-process" :class="{ managed: discovery.runtime.mode === 'managed' && discovery.runtime.running, none: !discovery.runtime.running }"><div class="local-process-dot" :class="{ off: !discovery.runtime.running }" /><div class="local-process-main"><strong>{{ runtimeTitle(discovery.runtime) }}</strong><div class="mono muted local-process-args" :title="runtimeDetail(discovery.runtime)">{{ runtimeDetail(discovery.runtime) }}</div></div><span class="tag">{{ runtimeTag(discovery.runtime) }}</span></div><template v-else><div v-for="process in discovery.processes || []" :key="process.pid" class="local-process"><div class="local-process-dot" /><div class="local-process-main"><strong>PID {{ process.pid }} · {{ process.exe || 'mihomo' }}</strong><div class="mono muted local-process-args">{{ (process.args || []).join(' ') }}</div></div><span class="tag">{{ process.containerized ? '容器进程' : '主机进程' }}</span></div><div v-if="!discovery.processes?.length" class="local-process none"><div class="local-process-dot off" /><div><strong>未获取到 Mihomo 运行状态</strong><div class="muted">仍会继续检查常见 config.yaml 路径</div></div></div></template></div><div v-if="discovery.candidates?.length" class="local-config-list"><div v-for="candidate in discovery.candidates" :key="candidate.path" class="local-config-row"><div class="local-config-icon">Y</div><div class="local-config-main"><div class="local-config-path mono" :title="candidate.path">{{ candidate.path }}</div><div class="local-config-meta"><span>{{ candidate.source || '检测' }}</span><span v-if="candidate.namespace === 'process-root'">进程根目录</span><span v-if="candidate.size">{{ formatBytes(candidate.size) }}</span><span v-if="candidate.mtime">{{ formatTime(candidate.mtime) }}</span></div></div><div class="local-config-state" :class="candidate.readable ? 'good' : candidate.exists ? 'bad' : 'muted'"><span v-if="importedPaths.has(candidate.path)" class="local-imported">已导入</span> {{ candidateState(candidate) }}</div><div class="actions local-config-actions"><template v-if="candidate.readable && candidate.token"><button class="ghost small" :disabled="Boolean(busyId)" @click="importNas(candidate, false)">导入</button><button class="success small" :disabled="Boolean(busyId)" @click="importNas(candidate, true)">导入并应用</button></template><button v-else class="ghost small" disabled>{{ candidateState(candidate) }}</button></div></div></div></AsyncState></div>
<BaseModal :open="modal === 'remote'" :title="editing ? '编辑订阅' : '添加远程订阅'" @close="modal = null"><div class="hint" style="margin-bottom:14px">{{ editing ? '修改订阅信息后保存；订阅内容将在下次更新时重新下载。' : '填写远程订阅信息，添加后会立即尝试下载一次。' }}</div><div class="form-grid"><div class="field"><label>名称</label><input v-model="form.name" placeholder="例如：机场订阅"></div><div class="field"><label>更新间隔（分钟）</label><input v-model.number="form.intervalMinutes" type="number" min="5"></div><div class="field full"><label>订阅 URL</label><input v-model="form.url" placeholder="https://..."></div><div class="field full"><div class="hint">自动更新顺序：直连 → 当前 Mihomo mixed-port → 系统 HTTP/HTTPS 代理。</div></div><div class="field profile-checkbox-field"><label class="profile-checkbox-label"><input v-model="form.autoUpdate" type="checkbox"><span>自动更新</span></label></div><div class="field profile-checkbox-field"><label class="profile-checkbox-label"><input v-model="form.autoApply" type="checkbox"><span>当前配置更新后自动应用</span></label></div></div><div class="actions" style="margin-top:16px"><button class="small" :disabled="busyId === 'remote'" @click="saveRemote">{{ busyId === 'remote' ? '处理中…' : editing ? '保存修改' : '添加订阅' }}</button><button class="ghost small" @click="modal = null">取消</button></div></BaseModal>
  <BaseModal :open="modal === 'file'" title="从当前电脑导入 YAML" @close="modal = null"><div class="hint" style="margin-bottom:12px">这里选择的是你正在打开 fnOS 的电脑上的文件；NAS 本机配置请使用页面底部的自动扫描。</div><div class="field"><label>名称</label><input v-model="fileName"></div><div class="field" style="margin-top:10px"><label>选择文件</label><input type="file" accept=".yaml,.yml,.txt" @change="selectedFile = ($event.target as HTMLInputElement).files?.[0] || null"></div><div class="actions" style="margin-top:16px"><button :disabled="busyId === 'file'" @click="importFile">{{ busyId === 'file' ? '处理中…' : '导入' }}</button><button class="ghost" @click="modal = null">取消</button></div></BaseModal>
</template>
