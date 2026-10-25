// Read-only DOM evidence after actual normal UI triggers; no animation clock manipulation.
export async function probe(ctx, label) {
  const dom = await ctx.tab.playwright.evaluate(() => {
    const selectors = '[class*="enter-active"],[class*="leave-active"],.session-focus-ghost,.plan-focus-ghost,.rest-recorded,.set-row--just,.plan-row--flash,.student-detail,.sync-block,.rest-track span,.training-bar span'
    const describe = e => {
      const s = getComputedStyle(e), r = e.getBoundingClientRect()
      return { tag: e.tagName, class: e.getAttribute('class'), text: e.textContent.trim().slice(0, 180), animationName: s.animationName,
        animationDuration: s.animationDuration, transitionProperty: s.transitionProperty, transitionDuration: s.transitionDuration,
        opacity: s.opacity, transform: s.transform, position: s.position, zIndex: s.zIndex, pointerEvents: s.pointerEvents,
        backgroundImage: s.backgroundImage, backgroundSize: s.backgroundSize, hidden: e.getAttribute('aria-hidden'),
        box: {x:r.x,y:r.y,width:r.width,height:r.height} }
    }
    return { mode: document.documentElement.dataset.motion, title: document.querySelector('main h1')?.textContent,
      elements: [...document.querySelectorAll(selectors)].map(describe),
      focus: document.activeElement ? {tag:document.activeElement.tagName,text:document.activeElement.textContent.trim().slice(0,100)} : null }
  })
  const result = {label,sourceRevision:ctx.sourceRevision,frameOrFpsMeasured:false,...dom}
  await ctx.fs.appendFile(ctx.evidenceDir+'/motion-probes.jsonl', JSON.stringify(result)+'\n')
  return result
}
