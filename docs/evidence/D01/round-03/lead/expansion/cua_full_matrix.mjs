// Lead-only browser QA. Invoke through cua_repl with current documented bindings.
// No source edits, app-state injection, clipping, or automatic product acceptance.
import { captureViewport } from '../fix-01/cua_capture_v2.mjs'
import { prepareViewport, auditDom, auditBounds } from '../fix-02/cua_review_v3.mjs'

export async function captureCurrentViewMatrix(ctx, {
  family,
  widths = [320, 390, 430, 768, 1440],
  scales = [100, 200],
  height = 900,
}) {
  if (!/^[a-f0-9]{40}$/.test(ctx.sourceRevision ?? '')) throw Error('Matrix requires an explicit frozen source revision')
  if (!/^[a-z0-9-]+$/.test(family)) throw Error('Invalid evidence label')
  const result = { family, sourceRevision: ctx.sourceRevision, accepted: false, captures: [] }
  await ctx.fs.mkdir(`${ctx.evidenceDir}/dom-text`, { recursive: true })
  for (const scale of scales) {
    await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
    const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
    await dialog.getByRole('button', { name: `${scale}%`, exact: true }).click()
    await ctx.tab.playwright.domSnapshot()
    await dialog.getByRole('button', { name: '关闭', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    for (const width of widths) {
      const name = `${family}-${width}-text${scale}`
      await prepareViewport(ctx, width, height)
      await ctx.tab.playwright.locator('main .view-enter-active').waitFor({ state: 'hidden' })
      const snapshot = await ctx.tab.playwright.domSnapshot()
      await ctx.fs.writeFile(`${ctx.evidenceDir}/dom-text/${name}.txt`, snapshot, { flag: 'wx' })
      const dom = await auditDom(ctx, name)
      const bounds = await auditBounds(ctx, name)
      let capture
      try { capture = await captureViewport(ctx, name, width, height) }
      catch (error) {
        // Preserve excluded capture and log. A settled fresh observation allows one retry.
        if (!String(error).includes('Viewport changed during capture; excluded:')) throw error
        await ctx.tab.playwright.domSnapshot()
        capture = await captureViewport(ctx, `${name}-retry`, width, height)
      }
      result.captures.push({
        name,
        path: capture.path,
        dimensionsVerified: capture.dimensionsVerified,
        observedTextScale: bounds.scale,
        mainCount: dom.mainCount,
        skipTargetExists: dom.skipTargetExists,
        duplicateIds: dom.duplicateIds,
        overflow: dom.scrollWidth > width,
        clippedControls: bounds.clippedControls,
        smallButtons: bounds.smallButtons.filter(x => x.box.width < 43.8 || x.box.height < 43.8),
      })
    }
  }
  await ctx.fs.appendFile(`${ctx.evidenceDir}/matrix-summary.jsonl`, JSON.stringify(result) + '\n')
  return result
}
