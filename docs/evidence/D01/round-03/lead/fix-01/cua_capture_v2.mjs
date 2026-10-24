// Lead-only QA helper, called from cua_repl with its documented tab/viewport APIs.
// No app state injection. Dimension verification is not product acceptance.
export async function captureViewport(ctx, name, width, height) {
  const { tab, viewport, fs, evidenceDir, sourceRevision } = ctx
  await viewport.set({ width, height })
  await tab.playwright.domSnapshot()
  const before = await tab.playwright.evaluate(() => ({
    width: innerWidth, height: innerHeight, dpr: devicePixelRatio,
    scrollX, scrollY, scrollWidth: document.documentElement.scrollWidth,
    mainCount: document.querySelectorAll('main').length,
    skipTargetExists: Boolean(document.getElementById('main')),
  }))
  await fs.mkdir(`${evidenceDir}/screenshots`, { recursive: true })
  if (before.width !== width || before.height !== height || before.dpr <= 0) {
    await fs.appendFile(`${evidenceDir}/calibration-errors.jsonl`, JSON.stringify({ name, sourceRevision, before }) + '\n')
    throw new Error(`Capture requires the requested CSS viewport: ${JSON.stringify(before)}`)
  }
  const bytes = await tab.screenshot({ fullPage: false })
  const after = await tab.playwright.evaluate(() => ({ width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scrollX, scrollY }))
  const dimensionsVerified = after.width === width && after.height === height && after.dpr === before.dpr && after.scrollX === before.scrollX && after.scrollY === before.scrollY
  const extension = bytes[0] === 0x89 && bytes[1] === 0x50 ? 'png' : 'jpg'
  const path = `${evidenceDir}/screenshots/${name}.${extension}`
  await fs.writeFile(path, bytes, { flag: 'wx' })
  const record = { name, path, sourceRevision, before, after, dimensionsVerified, accepted: false }
  await fs.appendFile(`${evidenceDir}/viewport-captures.jsonl`, JSON.stringify(record) + '\n')
  if (!dimensionsVerified) throw new Error(`Viewport changed during capture; excluded: ${name}`)
  return { ...record, bytes }
}
