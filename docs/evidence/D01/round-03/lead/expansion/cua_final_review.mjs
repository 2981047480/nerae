// Lead QA only. Call in cua_repl with a current tab, viewport, fs and frozen revision.
// Product state changes happen exclusively through observed UI controls.
import { gotoScenario } from '../fix-02/cua_review_v3.mjs'
import { captureCurrentViewport } from './cua_full_matrix.mjs'

export async function waitForSettledView(ctx) {
  // Terminal checks must also wait for TrainingView's focus transition and
  // mounted content animations, not only ordinary page view transitions.
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const settled = await ctx.tab.playwright.evaluate(() => {
      if (document.querySelector('[class*="-enter-active"],[class*="-leave-active"]')) return false
      const title = document.querySelector('main h1') ?? document.querySelector('main h2') ?? document.querySelector('main')
      if (!title) return false
      let element = title
      while (element) {
        if (Number(getComputedStyle(element).opacity) < .999) return false
        element = element.parentElement
      }
      return true
    })
    if (settled) return
    if (attempt === 39) throw Error('Terminal view still transitioning; inspect current UI before accepting')
    await new Promise(resolve => setTimeout(resolve, 50))
  }
}

export async function closeObservedSheet(ctx) {
  const dialog = ctx.tab.playwright.getByRole('dialog')
  if (!await dialog.isVisible()) return
  const close = dialog.getByRole('button', { name: '关闭', exact: true }).first()
  if (!await close.isVisible()) throw Error('Read current modal and choose an explicit exit before continuing')
  await close.click()
  await dialog.waitFor({ state: 'hidden' })
}

export async function resetSample(ctx, { width = 390, height = 900 } = {}) {
  await closeObservedSheet(ctx)
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
  await dialog.waitFor({ state: 'visible' })
  await dialog.getByRole('button', { name: '重置预览数据', exact: true }).click()
  await dialog.getByRole('button', { name: '确认重置', exact: true }).click()
  await ctx.tab.playwright.locator('main .view-enter-active').waitFor({ state: 'hidden' })
  await ctx.viewport.set({ width, height })
  return ctx.tab.playwright.domSnapshot()
}

export async function recordFlow(ctx, name) {
  if (!/^[a-f0-9]{40}$/.test(ctx.sourceRevision ?? '')) throw Error('Explicit frozen revision required')
  await waitForSettledView(ctx)
  // User actions can start browser smooth scrolling after focus restoration.
  // Poll actual DOM geometry for a stable terminal view; motion probes run
  // separately immediately after the trigger, before this terminal capture.
  let previousViewport = null
  let stableObservations = 0
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const viewport = JSON.stringify(await ctx.tab.playwright.evaluate(() =>
      ({ width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scrollX, scrollY })))
    stableObservations = viewport === previousViewport ? stableObservations + 1 : 0
    if (stableObservations >= 2) break
    if (attempt === 29) throw Error('Terminal viewport did not settle; preserve current evidence and inspect UI')
    previousViewport = viewport
    await new Promise(resolve => setTimeout(resolve, 50))
  }
  const snapshot = await ctx.tab.playwright.domSnapshot()
  await ctx.fs.writeFile(`${ctx.evidenceDir}/${name}.txt`, snapshot, { flag: 'wx' })
  let capture
  try { capture = await captureCurrentViewport(ctx, name) }
  catch (error) {
    if (!String(error).includes('Current viewport changed during capture; excluded:')) throw error
    await ctx.tab.playwright.domSnapshot()
    capture = await captureCurrentViewport(ctx, `${name}-settled`)
  }
  return { snapshot, path: capture.path }
}

async function selectObservedScenario(ctx, pattern, scenarioIndex, navigation) {
  if (navigation) {
    await ctx.tab.playwright.getByRole('navigation', { name: navigation.landmark, exact: true })
      .getByRole('button', { name: navigation.button, exact: true }).click()
    return ctx.tab.playwright.domSnapshot()
  }
  if (scenarioIndex === undefined) return gotoScenario(ctx, pattern)
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
  // The rendered preview inventory lists student messages first and coach messages second.
  await dialog.getByRole('button', { name: pattern }).nth(scenarioIndex).click()
  await dialog.waitFor({ state: 'hidden' })
  return ctx.tab.playwright.domSnapshot()
}

async function selectObservedRole(ctx, role) {
  if (!role) return
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
  await dialog.getByRole('button', { name: role, exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
}

export async function sweepIsolatedStates(ctx, family, pattern, { scenarioIndex, role, navigation, afterScenario } = {}) {
  await resetSample(ctx)
  await selectObservedRole(ctx, role)
  await selectObservedScenario(ctx, pattern, scenarioIndex, navigation)
  if (afterScenario) await afterScenario(ctx)
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  const labels = await ctx.tab.playwright.evaluate(() =>
    [...document.querySelectorAll('.state-chip')].map(element => element.textContent.trim()))
  await closeObservedSheet(ctx)
  const rows = []
  for (const [index, label] of labels.entries()) {
    await resetSample(ctx)
    await selectObservedRole(ctx, role)
    await selectObservedScenario(ctx, pattern, scenarioIndex, navigation)
    if (afterScenario) await afterScenario(ctx)
    await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
    const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
    await dialog.getByRole('button', { name: label, exact: true }).click()
    if (await dialog.isVisible()) await closeObservedSheet(ctx)
    await ctx.tab.playwright.locator('main .view-enter-active').waitFor({ state: 'hidden' })
    const name = `state-${family}-${String(index).padStart(2, '0')}`
    const evidence = await recordFlow(ctx, name)
    const row = {
      family, label, snapshot: `${name}.txt`, screenshot: evidence.path,
      headingsAndFeedback: evidence.snapshot.split('\n').filter(line => /heading|paragraph:|status|alert/.test(line)),
    }
    rows.push(row)
    await ctx.fs.appendFile(`${ctx.evidenceDir}/state-cases.jsonl`, JSON.stringify({ sourceRevision: ctx.sourceRevision, resetBeforeEachState: true, ...row }) + '\n')
  }
  const result = { family, sourceRevision: ctx.sourceRevision, resetBeforeEachState: true, accepted: false, rows }
  await ctx.fs.appendFile(`${ctx.evidenceDir}/states-isolated-summary.jsonl`, JSON.stringify(result) + '\n')
  return result
}

export async function inspectMotionAndFocus(ctx, label) {
  // The read-only browser scope does not expose document.getAnimations().
  // Observe real trigger/results and computed styles; never claim frame/fps measurement.
  const result = await ctx.tab.playwright.evaluate(() => {
    const rect = element => {
      const r = element.getBoundingClientRect()
      return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height }
    }
    const describe = element => {
      const style = getComputedStyle(element)
      return {
        tag: element.tagName, id: element.id, class: element.className,
        name: element.getAttribute('aria-label') || element.textContent.slice(0, 100),
        transitionDuration: style.transitionDuration, transitionProperty: style.transitionProperty,
        animationDuration: style.animationDuration, animationName: style.animationName,
        opacity: style.opacity, transform: style.transform, outline: style.outline,
        hidden: element.getAttribute('aria-hidden'), box: rect(element),
      }
    }
    const active = document.activeElement
    return {
      motion: document.documentElement.dataset.motion,
      textScale: document.documentElement.dataset.textScale,
      width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scrollY,
      focus: active ? describe(active) : null,
      title: document.querySelector('main h1')?.textContent ?? '',
      transitions: [...document.querySelectorAll('[class*="enter-active"],[class*="leave-active"],.session-focus-ghost,.plan-focus-ghost,.plan-row--flash')].map(describe),
      buttons: [...document.querySelectorAll('.btn')].slice(0, 5).map(describe),
      dialogs: [...document.querySelectorAll('[role="dialog"]')].map(describe),
    }
  })
  const record = { label, sourceRevision: ctx.sourceRevision, frameOrFpsMeasured: false, ...result }
  await ctx.fs.appendFile(`${ctx.evidenceDir}/motion-focus-observations.jsonl`, JSON.stringify(record) + '\n')
  return record
}

export async function inspectFlatTextContrast(ctx, label) {
  // Numeric evidence is limited to flat, opaque backgrounds. Photo/sibling overlays
  // and unresolved color formats require screenshot judgement rather than a pass.
  await waitForSettledView(ctx)
  const result = await ctx.tab.playwright.evaluate(() => {
    const rgba = value => {
      const match = value.match(/^rgba?\(([^)]+)\)$/)
      if (!match) return null
      const values = match[1].split(',').map(Number)
      return values.length === 3 ? [...values, 1] : values
    }
    const luminance = rgb => rgb.slice(0, 3).map(value => {
      const s = value / 255
      return s <= .04045 ? s / 12.92 : ((s + .055) / 1.055) ** 2.4
    }).reduce((sum, value, index) => sum + value * [.2126, .7152, .0722][index], 0)
    const rows = []
    for (const element of document.querySelectorAll('p,h1,h2,h3,span,button,a,label,li,dt,dd,input,textarea,select')) {
      const bounds = element.getBoundingClientRect()
      const style = getComputedStyle(element)
      if (!bounds.width || !bounds.height || style.visibility === 'hidden') continue
      const text = ['INPUT', 'TEXTAREA', 'SELECT'].includes(element.tagName)
        ? element.value : [...element.childNodes].filter(node => node.nodeType === 3).map(node => node.textContent).join('').trim()
      if (!text) continue
      let parent = element
      let background = null
      let uncertain = false
      let opacity = 1
      while (parent) {
        const current = getComputedStyle(parent)
        opacity *= Number(current.opacity)
        if (current.backgroundImage !== 'none') uncertain = true
        const color = rgba(current.backgroundColor)
        if (color && color[3] > .001 && !background) {
          if (color[3] >= .999) background = color
          else uncertain = true
        }
        if (['plan-hero-text', 'hero-copy'].some(name => parent.classList.contains(name))) uncertain = true
        parent = parent.parentElement
      }
      const foreground = rgba(style.color)
      if (!background || !foreground || foreground[3] < .999 || opacity < .999) uncertain = true
      let ratio = null
      if (!uncertain) {
        const l1 = luminance(foreground), l2 = luminance(background)
        ratio = (Math.max(l1, l2) + .05) / (Math.min(l1, l2) + .05)
      }
      const fontSize = Number.parseFloat(style.fontSize)
      const weight = Number.parseFloat(style.fontWeight)
      const threshold = fontSize >= 24 || (fontSize >= 18.67 && weight >= 700) ? 3 : 4.5
      rows.push({
        tag: element.tagName, class: element.className, text: text.slice(0, 90),
        color: style.color, background, fontSize, weight, opacity,
        inViewport: bounds.bottom > 0 && bounds.top < innerHeight,
        ratio, threshold, numericCheck: ratio === null ? 'needs_visual_review' : ratio >= threshold ? 'passed' : 'below_threshold',
      })
    }
    return { width: innerWidth, height: innerHeight, motion: document.documentElement.dataset.motion, rows }
  })
  const record = { label, sourceRevision: ctx.sourceRevision, scope: 'flat text only; not a universal accessibility certificate', ...result }
  await ctx.fs.appendFile(`${ctx.evidenceDir}/text-contrast-observations.jsonl`, JSON.stringify(record) + '\n')
  return record
}
