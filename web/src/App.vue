<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, type Component } from 'vue'
import NavIcon from '@/components/NavIcon.vue'
import CoreStartupChoice from '@/components/CoreStartupChoice.vue'
import ToastHost from '@/components/ToastHost.vue'
import DashboardPage from '@/pages/DashboardPage.vue'
import ProxiesPage from '@/pages/ProxiesPage.vue'
import ProfilesPage from '@/pages/ProfilesPage.vue'
import ConfigPage from '@/pages/ConfigPage.vue'
import RulesPage from '@/pages/RulesPage.vue'
import ConnectionsPage from '@/pages/ConnectionsPage.vue'
import LogsPage from '@/pages/LogsPage.vue'
import SettingsPage from '@/pages/SettingsPage.vue'
import { useCoreHealth } from '@/composables/useCoreHealth'
import { useFnosTheme } from '@/composables/useFnosTheme'
import { api, APP_PREFIX } from '@/services/api'
import { isBrowserHistoryShortcut } from '@/services/navigation'
import type { AppIconsResponse, SystemStatus } from '@/types/api'

type PageName = 'dashboard' | 'proxies' | 'profiles' | 'config' | 'rules' | 'connections' | 'logs' | 'settings'
const pages: Array<{ name: PageName; label: string; component: Component }> = [
  { name: 'dashboard', label: '仪表盘', component: DashboardPage },
  { name: 'proxies', label: '代理节点', component: ProxiesPage },
  { name: 'profiles', label: '订阅配置', component: ProfilesPage },
  { name: 'config', label: '配置文件', component: ConfigPage },
  { name: 'rules', label: '规则', component: RulesPage },
  { name: 'connections', label: '连接', component: ConnectionsPage },
  { name: 'logs', label: '日志', component: LogsPage },
  { name: 'settings', label: '设置', component: SettingsPage },
]

const readPage = (): PageName => {
  const value = location.hash.slice(1).split('?')[0] as PageName
  return pages.some(item => item.name === value) ? value : 'dashboard'
}
const current = ref<PageName>(readPage())
const refreshKey = ref(0)
const activePageRef = ref<{ refreshPage?: () => void | Promise<void> } | null>(null)
const startupSystem = ref<SystemStatus | null>(null)
const activePage = computed(() => pages.find(item => item.name === current.value) || pages[0]!)
const { footer, refresh } = useCoreHealth()
useFnosTheme()
let timer = 0
let startupTimer = 0
const scrollTimers = new Map<HTMLElement, number>()

function markScrollActivity(event: Event) {
  const target = event.target
  if (!(target instanceof HTMLElement)) return
  target.classList.add('is-scrolling')
  const previous = scrollTimers.get(target)
  if (previous) window.clearTimeout(previous)
  scrollTimers.set(target, window.setTimeout(() => {
    target.classList.remove('is-scrolling')
    scrollTimers.delete(target)
  }, 800))
}

function onHashChange() {
  current.value = readPage()
  refreshKey.value += 1
}

function navigate(page: PageName, event: MouseEvent) {
  event.preventDefault()
  if (page === current.value) return
  history.replaceState(history.state, '', `#${page}`)
  current.value = page
  refreshKey.value += 1
}

function blockBrowserHistoryShortcut(event: KeyboardEvent) {
  if (!isBrowserHistoryShortcut(event)) return
  event.preventDefault()
  event.stopPropagation()
}

function blockBrowserMouseNavigation(event: MouseEvent) {
  if (event.button !== 3 && event.button !== 4) return
  event.preventDefault()
  event.stopPropagation()
}

function refreshActivePage() {
  if (activePageRef.value?.refreshPage) {
    void activePageRef.value.refreshPage()
    return
  }
  refreshKey.value += 1
}

function syncIcon(iconId: string) {
  if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(iconId)) return
  let link = document.querySelector<HTMLLinkElement>('link[rel~="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    link.type = 'image/png'
    document.head.appendChild(link)
  }
  link.href = `${location.origin}${APP_PREFIX}/icons/${iconId}_64.png?v=${Date.now()}`
}

async function checkStartupCoreChoice() {
  window.clearTimeout(startupTimer)
  try {
    startupSystem.value = await api<SystemStatus>('/api/system/status')
    if (['idle', 'checking'].includes(startupSystem.value.bootstrap?.state || 'idle')) {
      startupTimer = window.setTimeout(() => checkStartupCoreChoice().catch(() => undefined), 1000)
    }
  } catch {
    startupSystem.value = null
  }
}

async function onCoreSelected() {
  await Promise.all([refresh().catch(() => undefined), checkStartupCoreChoice()])
  refreshKey.value += 1
}

onMounted(() => {
  window.addEventListener('hashchange', onHashChange)
  window.addEventListener('keydown', blockBrowserHistoryShortcut, true)
  window.addEventListener('pointerdown', blockBrowserMouseNavigation, true)
  window.addEventListener('auxclick', blockBrowserMouseNavigation, true)
  document.addEventListener('scroll', markScrollActivity, true)
  refresh().catch(() => undefined)
  checkStartupCoreChoice().catch(() => undefined)
  timer = window.setInterval(() => refresh().catch(() => undefined), 30_000)
  api<AppIconsResponse>('/api/app/icons').then(value => syncIcon(value.selected || value.defaultId || 'cat-orbit')).catch(() => syncIcon('cat-orbit'))
})
onBeforeUnmount(() => {
  window.removeEventListener('hashchange', onHashChange)
  window.removeEventListener('keydown', blockBrowserHistoryShortcut, true)
  window.removeEventListener('pointerdown', blockBrowserMouseNavigation, true)
  window.removeEventListener('auxclick', blockBrowserMouseNavigation, true)
  document.removeEventListener('scroll', markScrollActivity, true)
  scrollTimers.forEach((scrollTimer, element) => {
    window.clearTimeout(scrollTimer)
    element.classList.remove('is-scrolling')
  })
  scrollTimers.clear()
  window.clearInterval(timer)
  window.clearTimeout(startupTimer)
})
</script>

<template>
  <aside class="sidebar">
    <nav>
      <a v-for="page in pages" :key="page.name" :href="`#${page.name}`" class="nav-item" :class="{ active: current === page.name }" :title="page.label" :aria-current="current === page.name ? 'page' : undefined" @click="navigate(page.name, $event)">
        <span class="nav-icon" aria-hidden="true"><NavIcon :name="page.name" /></span><span>{{ page.label }}</span>
      </a>
    </nav>
    <div class="sidebar-footer">
      <div class="core-dot" :class="footer.className" />
      <div><strong>{{ footer.state }}</strong><small>{{ footer.version }}</small></div>
    </div>
  </aside>
  <main class="main">
    <header class="topbar">
      <h1>{{ activePage.label }}</h1>
      <div class="top-actions"><div id="page-actions" class="page-actions" /><button class="ghost" @click="refreshActivePage">刷新</button></div>
    </header>
    <section class="content" :class="{ 'config-content': current === 'config', 'logs-content': current === 'logs' }" @scroll.passive="markScrollActivity">
      <component :is="activePage.component" :key="`${current}-${refreshKey}`" ref="activePageRef" />
    </section>
  </main>
  <ToastHost />
  <CoreStartupChoice :open="startupSystem?.bootstrap?.state === 'choice-required'" :system="startupSystem" @selected="onCoreSelected" />
</template>
