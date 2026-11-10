import { TrimApp } from '@trimjs/web-app'

export function followTheme() {
  const media = window.matchMedia('(prefers-color-scheme: dark)')
  let hasHostTheme = false

  function apply(theme: 'light' | 'dark') {
    document.documentElement.dataset.theme = theme
  }

  function applyBrowserTheme() {
    if (!hasHostTheme) apply(media.matches ? 'dark' : 'light')
  }

  function applyHostTheme(theme: unknown) {
    if (theme !== 'light' && theme !== 'dark') return
    hasHostTheme = true
    apply(theme)
  }

  applyBrowserTheme()
  media.addEventListener('change', applyBrowserTheme)

  if (window.parent !== window) {
    try {
      const sdk = new TrimApp()
      if (sdk.isWeb && !sdk.isStandaloneWeb) {
        let receivedEvent = false
        void sdk.$on('os/theme', (theme) => {
          if (theme === 'light' || theme === 'dark') {
            receivedEvent = true
            applyHostTheme(theme)
          }
        }).catch(() => {})
        void sdk.getPlatformConfig().then(({ theme }) => {
          if (!receivedEvent) applyHostTheme(theme)
        }).catch(() => {})
      }
    } catch {
      applyBrowserTheme()
    }
  }
}
