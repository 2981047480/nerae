// Pass the current context explicitly: do not capture a tab handle across REPL cells.
export async function prepareViewport(ctx, width, height) {
  const { tab, viewport, fs, evidenceDir } = ctx
  await viewport.set({ width, height })
  await tab.getAXState({ emit: false })
  const before = await tab.playwright.evaluate(() => ({
    width: innerWidth, height: innerHeight, dpr: devicePixelRatio,
    y: scrollY, docHeight: document.documentElement.scrollHeight,
  }))
  if (before.dpr <= 0 || before.width !== width || before.height !== height) {
    throw new Error(`CSS viewport mismatch: ${JSON.stringify(before)}`)
  }
  const after = await tab.playwright.evaluate(() => ({ width: innerWidth, height: innerHeight, dpr: devicePixelRatio, y: scrollY }))
  await fs.appendFile(`${evidenceDir}/calibrations.jsonl`, JSON.stringify({ before, after }) + '\n')
  if (after.dpr !== before.dpr || after.width !== width || after.height !== height) {
    throw new Error(`Viewport not ready: ${JSON.stringify(after)}`)
  }
}

export async function gotoScenario(ctx, pattern) {
  const { tab } = ctx
  await tab.playwright.getByRole('button', { name: '预览', exact: true }).click()
  await tab.playwright.getByRole('dialog', { name: '预览', exact: true }).waitFor({ state: 'visible' })
  await tab.playwright.getByRole('dialog', { name: '预览', exact: true }).getByRole('button', { name: pattern }).click()
  await tab.playwright.getByRole('dialog').waitFor({ state: 'hidden' })
  return await tab.playwright.domSnapshot()
}

export async function auditDom(ctx, name) {
  const value = await ctx.tab.playwright.evaluate(() => {
    const rect = e => { const r = e.getBoundingClientRect(); return { width: r.width, height: r.height } }
    const visible = e => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(e).visibility !== 'hidden' }
    const ids = Array.from(document.querySelectorAll('[id]')).map(e => e.id)
    return {
      width: innerWidth, height: innerHeight, dpr: devicePixelRatio,
      scrollWidth: document.documentElement.scrollWidth,
      mainCount: document.querySelectorAll('main').length,
      skipTargetExists: Boolean(document.getElementById('main')),
      duplicateIds: ids.filter((x, i) => ids.indexOf(x) !== i),
      smallButtons: Array.from(document.querySelectorAll('button')).filter(visible)
        .map(e => ({ name: e.getAttribute('aria-label') || e.textContent.trim(), ...rect(e) }))
        .filter(e => e.width < 44 || e.height < 44),
      fields: Array.from(document.querySelectorAll('input,textarea,select')).filter(visible)
        .map(e => ({ name: e.getAttribute('aria-label') || e.id, element: rect(e), hit: rect(e.closest('label') || e) })),
    }
  })
  await ctx.fs.appendFile(`${ctx.evidenceDir}/dom-audits.jsonl`, JSON.stringify({ name, sourceRevision: ctx.sourceRevision, ...value }) + '\n')
  return value
}
