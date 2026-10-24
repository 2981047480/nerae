// Read-only DOM inspection. Caller supplies the current tab and source binding.
export { prepareViewport, auditDom } from '../fix-01/cua_review_v2.mjs'

export async function gotoScenario(ctx, pattern) {
  const { tab } = ctx
  await tab.playwright.getByRole('button', { name: ctx.previewLabel ?? '打开预览控制', exact: true }).click()
  const dialog = tab.playwright.getByRole('dialog', { name: '预览', exact: true })
  await dialog.waitFor({ state: 'visible' })
  await dialog.getByRole('button', { name: pattern }).click()
  await dialog.waitFor({ state: 'hidden' })
  return await tab.playwright.domSnapshot()
}

export async function auditBounds(ctx, name) {
  const value = await ctx.tab.playwright.evaluate(() => {
    const box = element => {
      const r = element.getBoundingClientRect()
      return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height }
    }
    const visible = element => {
      const r = element.getBoundingClientRect()
      return r.width > 0 && r.height > 0 && getComputedStyle(element).visibility !== 'hidden'
    }
    const controls = [...document.querySelectorAll('button,input,textarea,select')].filter(visible).map(element => {
      const ancestors = []
      let parent = element.parentElement
      while (parent && ancestors.length < 6) {
        const style = getComputedStyle(parent)
        ancestors.push({ tag: parent.tagName, className: parent.className, box: box(parent), overflowX: style.overflowX })
        parent = parent.parentElement
      }
      return { tag: element.tagName, name: element.getAttribute('aria-label') || element.id || element.textContent.trim(), box: box(element), ancestors }
    })
    const skip = document.querySelector('a[href="#main"]')
    return {
      viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scrollX, scrollY },
      scale: document.documentElement.dataset.textScale,
      clippedControls: controls.filter(item => item.box.left < -1 || item.box.right > innerWidth + 1),
      smallButtons: controls.filter(item => item.tag === 'BUTTON' && (item.box.width < 44 || item.box.height < 44)),
      fields: controls.filter(item => item.tag !== 'BUTTON').map(({ tag, name, box }) => ({ tag, name, box })),
      skip: skip ? { box: box(skip), focused: document.activeElement === skip, transform: getComputedStyle(skip).transform } : null,
    }
  })
  await ctx.fs.appendFile(`${ctx.evidenceDir}/bounds-audits.jsonl`, JSON.stringify({ name, sourceRevision: ctx.sourceRevision, ...value }) + '\n')
  return value
}
