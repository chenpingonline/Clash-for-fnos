<script setup lang="ts">
import { computed, ref } from 'vue'
import BaseModal from '@/components/BaseModal.vue'
import { useOperationProgress } from '@/composables/useOperationProgress'
import { api, errorMessage, jsonRequest } from '@/services/api'
import type { NetworkSettingsResponse } from '@/types/api'
import { portConflict } from '@/services/port-conflict'
import { notify } from '@/services/toast'
const operation = useOperationProgress()
const operationMessage = operation.message

const props = defineProps<{ error: string; managed?: boolean }>()
const emit = defineEmits<{ updated: [] }>()
const loaded = ref(false)
const dialog = ref(false), busy = ref(false), dialogError = ref(''), saved = ref(false)
const portFields = ref<Array<{ key: string; label: string; port: number | string; enabled: boolean; conflict: boolean }>>([])
const ports = [
  { key: 'mixed', label: '混合代理端口', yaml: 'mixed-port' },
  { key: 'controller', label: 'Controller API 端口', yaml: 'Controller' },
  { key: 'socks', label: 'SOCKS5 代理端口', yaml: 'socks-port' },
  { key: 'http', label: 'HTTP(S) 代理端口', yaml: 'port' },
  { key: 'redir', label: 'Redir 透明代理端口', yaml: 'redir-port' },
  { key: 'tproxy', label: 'TProxy 透明代理端口', yaml: 'tproxy-port' },
]
async function editPort() {
  operation.show(''); loaded.value = false; dialog.value = true; busy.value = true; dialogError.value = ''; saved.value = false
  try {
    const result = await api<NetworkSettingsResponse>('/api/network/settings')
    const settings = result.settings as Record<string, { port?: number; enabled?: boolean }> | null | undefined
    const matching = ports.filter(item => settings?.[item.key]?.port === conflict.value?.port)
    const detected = matching.find(item => props.error.includes(item.yaml)) || matching[0]
    if (!detected) throw new Error('当前配置与冲突信息不一致，请刷新页面后重试，或在“设置 → 网络与端口”修改。')
    const keys = ['controller', 'mixed', ...(!['controller', 'mixed'].includes(detected.key) ? [detected.key] : [])]
    portFields.value = keys.map(key => {
      const value = settings?.[key]
      if (!value || !Number.isInteger(value.port) || Number(value.port) < 1 || Number(value.port) > 65535) throw new Error('无法读取启动端口，请刷新后重试。')
      return { key, label: ports.find(item => item.key === key)!.label, port: value.port!, enabled: key === 'controller' || value.enabled !== false, conflict: detected.key === key }
    })
    loaded.value = true
  } catch (cause) { dialogError.value = errorMessage(cause) }
  finally { busy.value = false }
}
async function savePort(start: boolean) {
  if (busy.value || !loaded.value) return
  operation.show('')
  const payload: Record<string, { enabled: boolean; port: number }> = {}
  const used = new Map<number, string>()
  for (const field of portFields.value) {
    const port = Number(field.port)
    if (!Number.isInteger(port) || port < 1 || port > 65535) { dialogError.value = `${field.label}请输入 1–65535 的整数端口`; return }
    if (field.enabled && used.has(port)) { dialogError.value = `端口冲突：${field.label}与${used.get(port)}不能同时使用 ${port}`; return }
    if (field.enabled) used.set(port, field.label)
    payload[field.key] = { enabled: field.enabled, port }
  }
  busy.value = true; dialogError.value = ''; saved.value = false
  try {
    await operation.request('/api/network/settings', jsonRequest('PUT', payload), '/api/network/settings/status', '正在校验端口并保存配置…')
    saved.value = true
    if (start) {
      await operation.request('/api/core/start', { method: 'POST' }, '/api/core/operation/status', '端口已保存 → 正在启动 Core…')
      operation.show('Core 已启动 → 正在验证 Controller 连接…')
      await api('/api/status')
    }
    operation.show(start ? '保存完成 → 启动完成 → 连接成功' : '端口校验通过 → 配置已保存')
    notify(start ? '端口已保存，托管 Core 已启动' : '启动端口已保存，可稍后启动内核')
    dialog.value = false; emit('updated')
  } catch (cause) { dialogError.value = `${saved.value ? '端口已保存，启动失败：' : ''}${errorMessage(cause)}`; operation.show(dialogError.value) }
  finally { busy.value = false }
}
const conflict = computed(() => portConflict(props.error))
const steps = computed(() => conflict.value ? [
  { title: '1. 查找占用进程', command: conflict.value.command, note: '在 NAS 的 SSH 终端执行，查看输出中的进程名和 pid=数字。' },
  { title: '2. 核对进程用途', command: 'ps -p PID -o pid,ppid,user,args', note: '将命令中的 PID 替换为上一步查到的进程号，确认它是可以停止的服务。' },
  { title: '3. 停止已确认的进程', command: 'sudo kill -TERM PID', note: '同样替换 PID。等待几秒，再执行第一条命令，确认端口已释放，然后点击“重新检测”或“启动内核”。' },
] : [])
async function copy(command: string) {
  try { await navigator.clipboard.writeText(command); notify('命令已复制；含 PID 的命令请先替换进程号') }
  catch { notify('无法自动复制，请选中命令手动复制', true) }
}
</script>

<template>
  <section v-if="conflict" class="port-conflict-help" aria-label="端口占用解决方法">
    <h3>{{ conflict.port }} / {{ conflict.protocol.toUpperCase() }} 端口被占用</h3>
    <p class="hint">可直接修改托管 Core 的启动端口，保留原有服务。保存时备份并校验配置，启动后自动验证连接。</p>
    <button v-if="managed" type="button" @click="editPort">修改端口</button>
    <p v-else class="hint">外部 Core 请通过原服务修改配置，或在 Core 设置中切换为托管模式。</p>
    <details><summary>高级排查（SSH 命令）</summary>
    <div v-for="step in steps" :key="step.title" class="port-conflict-step">
      <strong>{{ step.title }}</strong><p class="hint">{{ step.note }}</p>
      <div class="port-conflict-command"><code>{{ step.command }}</code><button class="ghost small" type="button" :aria-label="`复制${step.title.slice(3)}命令`" @click="copy(step.command)">复制</button></div>
    </div>
    <p class="hint">如果占用者是 Docker 容器，请在飞牛 Docker 页面停止对应容器或修改端口映射，不要直接结束 docker-proxy。受服务管理器控制的进程可能自动重启，应通过原服务停止。</p>
    <details><summary>进程仍未退出时</summary><p class="hint">先重新查询并核对 PID，确认仍是同一进程后才强制结束；强制结束不会执行正常清理。</p><div class="port-conflict-command"><code>sudo kill -KILL PID</code><button class="ghost small" type="button" aria-label="复制强制结束命令" @click="copy('sudo kill -KILL PID')">复制</button></div></details>
    <details><summary>系统没有 ss 命令时</summary><p class="hint">可用 netstat 查看监听列表，找到本地地址末尾为 :{{ conflict.port }} 的行及其 PID/程序名。</p><div class="port-conflict-command"><code>sudo netstat -tulnp</code><button class="ghost small" type="button" aria-label="复制备用排查命令" @click="copy('sudo netstat -tulnp')">复制</button></div></details>
    </details>
    <BaseModal :open="dialog" title="修改启动端口" :closable="!busy" @close="dialog = false">
      <p class="hint">Core 停止时也可修改两个启动端口。保存前统一检查冲突，启动时自动连接新的 Controller 地址。</p>
      <div class="recovery-port-grid">
        <div v-for="field in portFields" :key="field.key" class="field">
          <label :for="`recovery-${field.key}`">{{ field.label }} <span v-if="field.conflict" class="error-text">原端口 {{ conflict.port }} 冲突</span><span v-else-if="!field.enabled" class="hint">（未启用）</span></label>
          <input :id="`recovery-${field.key}`" v-model="field.port" type="number" min="1" max="65535" step="1" :disabled="busy">
        </div>
      </div>
      <div v-if="dialogError && !operationMessage" class="local-warning" role="alert">{{ dialogError }}</div>
      <div class="actions"><button class="ghost" :disabled="busy" @click="dialog = false">取消</button><button class="ghost" :disabled="busy || !loaded" @click="savePort(false)">保存</button><button :disabled="busy || !loaded" @click="savePort(true)">{{ busy ? '处理中…' : '保存并启动' }}</button><span v-if="operationMessage" class="inline-operation-state" :class="{ 'error-text': dialogError }" role="status" aria-live="polite">{{ operationMessage }}</span></div>
    </BaseModal>
  </section>
</template>

<style scoped>
.port-conflict-help{margin:12px 0;padding:14px;border:1px solid var(--line);border-radius:10px;background:var(--panel2)}
.recovery-port-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.recovery-port-grid .field{min-width:0}.recovery-port-grid label{line-height:1.6;min-height:39px}.recovery-port-grid label span{display:block}
.field{margin:12px 0}.actions{margin-top:16px}
h3{margin:0;font-size:13px}.hint{line-height:1.6;margin:6px 0}.port-conflict-step{margin-top:12px}.port-conflict-step>strong{font-size:12px}
.port-conflict-command{display:flex;align-items:center;gap:10px;padding:8px 10px;background:var(--panel);border:1px solid var(--line);border-radius:7px}.port-conflict-command code{flex:1;min-width:0;overflow-wrap:anywhere;white-space:pre-wrap;font-size:12px;user-select:text}.port-conflict-command button{flex:none}details{margin-top:10px}summary{cursor:pointer;font-size:12px;color:var(--muted)}
</style>
