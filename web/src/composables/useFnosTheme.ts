import { onBeforeUnmount, onMounted } from 'vue'

const DESKTOP_THEME_KEY = 'DesktopConfig-1000'
const LEGACY_THEME_KEY = 'fnos-theme-mode'

interface ThemeState {
  mode: 'dark' | 'light' | 'system'
  resolved: 'dark' | 'light'
  source: string
}

function systemTheme(): 'dark' | 'light' {
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function parentTheme(): 'dark' | 'light' | null {
  try {
    if (window.parent !== window) {
      const root = window.parent.document.documentElement
      const body = window.parent.document.body
      const value = String(body?.getAttribute('theme-mode') || root?.getAttribute('theme-mode') || root?.dataset.theme || '').toLowerCase()
      if (value === 'dark' || value === 'light') return value
      if (root?.classList.contains('dark')) return 'dark'
    }
  } catch {
    // Cross-origin fnOS hosts are expected; browser preference remains the fallback.
  }
  return null
}

function readTheme(): ThemeState {
  try {
    const raw = localStorage.getItem(DESKTOP_THEME_KEY)
    if (raw) {
      const value = Number((JSON.parse(raw) as { userPreference?: { theme?: number } }).userPreference?.theme)
      if (value === 10) return { mode: 'light', resolved: 'light', source: DESKTOP_THEME_KEY }
      if (value === 20) return { mode: 'dark', resolved: 'dark', source: DESKTOP_THEME_KEY }
      if (value === 30) return { mode: 'system', resolved: systemTheme(), source: DESKTOP_THEME_KEY }
    }
  } catch {
    // Invalid host storage should not prevent the page from rendering.
  }
  const legacy = String(localStorage.getItem(LEGACY_THEME_KEY) || '').toLowerCase()
  if (legacy === 'dark' || legacy === '20') return { mode: 'dark', resolved: 'dark', source: LEGACY_THEME_KEY }
  if (legacy === 'light' || legacy === '10') return { mode: 'light', resolved: 'light', source: LEGACY_THEME_KEY }
  if (legacy === 'system' || legacy === '30') return { mode: 'system', resolved: systemTheme(), source: LEGACY_THEME_KEY }
  const parent = parentTheme()
  return parent
    ? { mode: parent, resolved: parent, source: 'fnos-parent' }
    : { mode: 'system', resolved: systemTheme(), source: 'browser' }
}

function applyTheme(): void {
  const state = readTheme()
  const root = document.documentElement
  root.dataset.themeMode = 'system'
  root.dataset.fnosThemeMode = state.mode
  root.dataset.theme = state.resolved
  root.style.colorScheme = state.resolved
}

export function useFnosTheme(): void {
  const media = matchMedia('(prefers-color-scheme: dark)')
  const onStorage = (event: StorageEvent) => {
    if (event.key === DESKTOP_THEME_KEY || event.key === LEGACY_THEME_KEY) applyTheme()
  }
  let timer = 0
  onMounted(() => {
    applyTheme()
    window.addEventListener('storage', onStorage)
    media.addEventListener('change', applyTheme)
    timer = window.setInterval(applyTheme, 500)
  })
  onBeforeUnmount(() => {
    window.removeEventListener('storage', onStorage)
    media.removeEventListener('change', applyTheme)
    window.clearInterval(timer)
  })
}
