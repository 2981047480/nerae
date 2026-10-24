// Lead-only browser QA. Invoke through cua_repl with current documented bindings.
// No source edits, app-state injection, clipping, or automatic product acceptance.
import { captureViewport } from '../fix-01/cua_capture_v2.mjs'
import { prepareViewport, auditDom, auditBounds } from '../fix-02/cua_review_v3.mjs'

// Flow evidence must preserve the scroll/focus produced by the real action.
// Unlike the width matrix, this function never calls viewport.set().
export async function captureCurrentViewport(ctx, name) {
  const inspect=()=>({
    width:innerWidth,height:innerHeight,dpr:devicePixelRatio,scrollX,scrollY,
    mainCount:document.querySelectorAll('main').length,
    skipTargetExists:Boolean(document.getElementById('main')),
    textScale:document.documentElement.dataset.textScale,
  })
  const before=await ctx.tab.playwright.evaluate(inspect)
  const bytes=await ctx.tab.screenshot({fullPage:false})
  const after=await ctx.tab.playwright.evaluate(inspect)
  const dimensionsVerified=['width','height','dpr','scrollX','scrollY'].every(k=>before[k]===after[k])
  const extension=bytes[0]===0x89&&bytes[1]===0x50?'png':'jpg'
  await ctx.fs.mkdir(`${ctx.evidenceDir}/screenshots`,{recursive:true})
  const path=`${ctx.evidenceDir}/screenshots/${name}.${extension}`
  await ctx.fs.writeFile(path,bytes,{flag:'wx'})
  const record={name,path,mode:'preserve-current-viewport',sourceRevision:ctx.sourceRevision,before,after,dimensionsVerified,accepted:false}
  await ctx.fs.appendFile(`${ctx.evidenceDir}/flow-captures.jsonl`,JSON.stringify(record)+'\n')
  if(!dimensionsVerified)throw Error(`Current viewport changed during capture; excluded: ${name}`)
  return {...record,bytes}
}

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
      const squeezedHeadings = await ctx.tab.playwright.evaluate(() =>
        [...document.querySelectorAll('main h1,main h2,main h3')].map(element => {
          const box = element.getBoundingClientRect()
          return { text: element.textContent.trim(), width: box.width, height: box.height, fontSize: Number.parseFloat(getComputedStyle(element).fontSize) }
        }).filter(row => row.width > 0 && (row.text.match(/[\u4e00-\u9fff]/g) ?? []).length >= 5 && row.width < row.fontSize * 2.05))
      if (dom.mainCount !== 1 || !dom.skipTargetExists) throw Error(`Rendered main/skip target unavailable: ${name}`)
      if (bounds.scale !== String(scale)) throw Error(`Text scale was not applied: ${name}, observed ${bounds.scale}`)
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
        squeezedHeadings,
        smallButtons: bounds.smallButtons.filter(x => x.box.width < 43.8 || x.box.height < 43.8),
        smallFields: dom.fields.filter(x =>
          (x.element.width < 43.8 && x.hit.width < 43.8) ||
          (x.element.height < 43.8 && x.hit.height < 43.8)),
      })
    }
  }
  await ctx.fs.appendFile(`${ctx.evidenceDir}/matrix-summary.jsonl`, JSON.stringify(result) + '\n')
  return result
}
