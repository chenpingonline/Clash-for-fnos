<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import BaseModal from '@/components/BaseModal.vue'
import bundledChangelog from '../../../CHANGELOG.md?raw'
import type { AppUpdateInfo } from '@/types/api'
import { parseChangelog } from '@/services/changelog'

const props = defineProps<{ open: boolean; latest?: AppUpdateInfo['latest'] }>()
const emit = defineEmits<{ close: [] }>()
const closeButton = ref<HTMLButtonElement>()
const body = ref<HTMLElement>()
const historyLink = ref<HTMLAnchorElement>()
const currentEntry = parseChangelog(bundledChangelog)[0]
let previousFocus: HTMLElement | null = null
const title = computed(() => props.latest ? `v${String(props.latest.tag || '').replace(/^v/, '')} 更新内容` : '更新日志')
// Render release text through interpolation only; remote HTML must never execute.
const lines = computed(() => (props.latest
  ? props.latest.body?.trim() || '此版本暂未提供更新日志。'
  : currentEntry?.body || '暂无更新日志。'
).split(/\r?\n/).map(text => {
  const heading = text.match(/^(#{1,6})\s+(.+)$/)
  const bullet = text.match(/^\s*[-*+]\s+(.+)$/)
  return { kind: heading ? (heading[1]!.length <= 2 ? 'version' : 'heading') : bullet ? 'bullet' : 'text', text: (heading?.[2] || bullet?.[1] || text).replace(/`([^`]+)`/g, '$1') }
}))
function handleKey(event: KeyboardEvent) {
  if (event.key === 'Escape') { event.preventDefault(); emit('close') }
  if (event.key === 'Tab') {
    event.preventDefault()
    const controls = [closeButton.value, body.value, historyLink.value].filter((element): element is HTMLElement => Boolean(element))
    const current = controls.indexOf(document.activeElement as HTMLElement)
    controls[(current + (event.shiftKey ? -1 : 1) + controls.length) % controls.length]?.focus()
  }
}
watch(() => props.open, async open => {
  if (open) {
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    document.addEventListener('keydown', handleKey)
    await nextTick()
    if (props.open) {
      if (body.value) body.value.scrollTop = 0
      closeButton.value?.focus()
    }
  } else {
    document.removeEventListener('keydown', handleKey)
    previousFocus?.focus()
  }
})
onBeforeUnmount(() => document.removeEventListener('keydown', handleKey))

</script>

<template>
  <BaseModal :open="open" :title="title" @close="emit('close')">
    <template #header>
      <div class="changelog-header">
        <h3>{{ title }}</h3>
        <button ref="closeButton" type="button" class="changelog-close" aria-label="关闭" @click="emit('close')">
          <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
        </button>
      </div>
    </template>
    <p v-if="latest?.publishedAt" class="hint">发布时间：{{ latest.publishedAt.slice(0, 10) }}</p>
    <p v-if="!latest && currentEntry" class="hint">v{{ currentEntry.version }}{{ currentEntry.date ? ` · ${currentEntry.date}` : '' }}</p>
    <div ref="body" class="changelog-body" tabindex="0" aria-label="更新日志内容">
      <template v-for="(line, index) in lines" :key="index">
        <h4 v-if="line.kind === 'version'">{{ line.text }}</h4>
        <h5 v-else-if="line.kind === 'heading'">{{ line.text }}</h5>
        <p v-else-if="line.kind === 'bullet'" class="changelog-bullet">{{ line.text }}</p>
        <p v-else-if="line.text.trim()">{{ line.text }}</p>
      </template>
    </div>
    <div class="changelog-footer"><a ref="historyLink" href="https://github.com/chenpingonline/Clash-for-fnos/blob/master/CHANGELOG.md" target="_blank" rel="noopener noreferrer">查看历史日志 ↗</a></div>
  </BaseModal>
</template>

<style scoped>
.changelog-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.changelog-header h3 { min-width: 0; overflow-wrap: anywhere; }
.changelog-close { display: grid; place-items: center; flex: none; width: 32px; height: 32px; padding: 0; border: 0; background: transparent; box-shadow: none; color: var(--muted); cursor: pointer; }
.changelog-close:hover { color: var(--text); }
.changelog-close:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }

.changelog-body { max-height: min(60vh, 560px); overflow: auto; overscroll-behavior: contain; padding-right: 10px; color: var(--text); font-size: 13px; line-height: 1.8; overflow-wrap: anywhere; }
.changelog-body h4 { margin: 22px 0 10px; padding-top: 16px; border-top: 1px solid var(--line); font-size: 15px; }
.changelog-body h5 { margin: 16px 0 6px; font-size: 13px; }
.changelog-body p { margin: 7px 0; white-space: pre-wrap; }
.changelog-bullet { position: relative; padding-left: 14px; }
.changelog-bullet::before { content: '•'; position: absolute; left: 0; color: var(--muted); }
.changelog-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; border-top: 1px solid var(--line); padding-top: 12px; margin-top: 12px; }
@media (max-width: 480px) {
  .changelog-body { max-height: 48vh; }
}
</style>
