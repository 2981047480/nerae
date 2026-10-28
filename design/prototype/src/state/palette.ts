import { ref } from 'vue'

/** Appearance only. No workout, service or consent state is stored here. */
export const paletteOptions = [
  {
    id: 'studio', title: '墨石工作室', summary: '瓷白、墨石和熔橙，内容与服务优先', recommended: true,
    colors: {
      canvas: '#F3F4F1', surface: '#FFFFFF', ink: '#252C29', 'ink-rgb': '37,44,41', muted: '#606A63',
      line: '#DEE2DC', 'line-strong': '#929F94', soft: '#E9EDE7', accent: '#A33F27', 'accent-soft': '#F9E7DF',
      action: '#F08B65', 'action-hover': '#E47A53', 'on-accent': '#252C29', 'on-ink': '#FFFFFF',
      recorded: '#EEF1E9', placeholder: '#62705F', positive: '#44623C', warning: '#7A581D', error: '#A83236',
      'error-soft': '#FBE6E4', 'warning-soft': '#F5EBD3', 'positive-soft': '#E6EDDF',
      'avatar-surface': '#E0E6D8', 'avatar-ink': '#46563C', scrim: 'rgba(20,28,23,.52)',
      feature: '#252C29', 'feature-text': '#F6F8F2', 'feature-muted': '#C0CAC1', 'feature-line': '#49584C',
      'feature-accent': '#FF9A75', 'feature-on-accent': '#252C29',
    },
  },
] as const
export type PaletteId = typeof paletteOptions[number]['id']
export const selectedPalette = ref<PaletteId>('studio')
const storageKey = 'nerae.design.palette.v1'

function applyPalette(id: PaletteId): void {
  if (typeof document === 'undefined') return
  const palette = paletteOptions.find(option => option.id === id)!
  document.documentElement.dataset.palette = palette.id
  document.documentElement.style.colorScheme = 'light'
  for (const [key, value] of Object.entries(palette.colors)) document.documentElement.style.setProperty(`--${key}`, value)
}
export function initializePalette(): void {
  if (typeof window === 'undefined') return
  try {
    const saved = window.localStorage.getItem(storageKey)
    if (paletteOptions.some(option => option.id === saved)) selectedPalette.value = saved as PaletteId
  } catch { /* A blocked appearance store does not block the app. */ }
  applyPalette(selectedPalette.value)
}
export function setPalette(id: PaletteId): void {
  selectedPalette.value = id
  applyPalette(id)
  try { window.localStorage.setItem(storageKey, id) } catch { /* Apply for this preview. */ }
}
