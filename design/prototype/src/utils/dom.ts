export function isElementActuallyVisible(element: HTMLElement | null): element is HTMLElement {
  if (!element || !element.isConnected) return false
  if (element === document.body || element === document.documentElement) return false
  if (element.closest('[hidden]')) return false
  if (element.getAttribute('aria-hidden') === 'true') return false
  if ((element as HTMLButtonElement).disabled) return false
  if (typeof window === 'undefined') return false
  const style = window.getComputedStyle(element)
  if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false
  const checker = (
    element as HTMLElement & {
      checkVisibility?: (options?: { checkOpacity?: boolean; checkVisibilityCSS?: boolean }) => boolean
    }
  ).checkVisibility
  if (typeof checker === 'function') {
    return checker.call(element, { checkOpacity: true, checkVisibilityCSS: true })
  }
  return element.getClientRects().length > 0 || element.offsetParent !== null
}
