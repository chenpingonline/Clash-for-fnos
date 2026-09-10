import { TrimApp } from '@trimjs/web-app'
import { onBeforeUnmount, onMounted } from 'vue'

type Theme = 'dark' | 'light'

function systemTheme(): Theme {
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function normalizeTheme(value: unknown): Theme | null {
  if (value === 'dark' || value === 'light') return value
  if (!value || typeof value !== 'object') return null

  const nested = (value as { theme?: unknown }).theme
  if (nested === 'dark' || nested === 'light') return nested
  if (nested && typeof nested === 'object') {
    const theme = (nested as { theme?: unknown }).theme
    if (theme === 'dark' || theme === 'light') return theme
  }
  return null
}

function applyTheme(theme: Theme, mode: Theme | 'system'): void {
  const root = document.documentElement
  root.dataset.themeMode = 'system'
  root.dataset.fnosThemeMode = mode
  root.dataset.theme = theme
  root.style.colorScheme = theme
}

export function useFnosTheme(): void {
  const media = matchMedia('(prefers-color-scheme: dark)')
  const sdk = new TrimApp()
  let disposed = false
  let hostThemeActive = false
  let subscribed = false

  const applyHostTheme = (value: unknown) => {
    if (disposed) return
    const theme = normalizeTheme(value)
    if (!theme) return
    hostThemeActive = true
    applyTheme(theme, theme)
  }
  const applySystemTheme = () => {
    if (!hostThemeActive && !disposed) applyTheme(systemTheme(), 'system')
  }

  onMounted(() => {
    applySystemTheme()
    media.addEventListener('change', applySystemTheme)

    void sdk.getPlatformConfig()
      .then(async (config) => {
        if (disposed) return
        applyHostTheme(config.theme)
        if (sdk.isWeb && !sdk.isStandaloneWeb) {
          await sdk.$on('os/theme', applyHostTheme)
          if (disposed) {
            await sdk.$off('os/theme', applyHostTheme)
            return
          }
          subscribed = true
        }
      })
      .catch(() => {
        // Standalone browsers and older fnOS hosts keep using the media-query fallback.
      })
  })

  onBeforeUnmount(() => {
    disposed = true
    media.removeEventListener('change', applySystemTheme)
    if (subscribed) void sdk.$off('os/theme', applyHostTheme).catch(() => undefined)
  })
}
