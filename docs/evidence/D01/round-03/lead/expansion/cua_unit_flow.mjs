// Lead QA through normal UI only. Use a disposable preview of a frozen build.
import { resetSample, recordFlow, waitForSettledView } from './cua_final_review.mjs'

export async function startPublishedPlan(ctx, title) {
  await ctx.tab.playwright.getByRole('navigation', { name: '学员主导航', exact: true })
    .getByRole('button', { name: '发现', exact: true }).click()
  const row = ctx.tab.playwright.locator('li.item').filter({ hasText: title })
  await row.getByRole('button', { name: '查看完整计划', exact: true }).click()
  await ctx.tab.playwright.getByRole('heading', { name: title, exact: true }).waitFor({ state: 'visible' })
  await ctx.tab.playwright.getByRole('button', { name: '开始这次训练', exact: true }).click()
  const replace = ctx.tab.playwright.getByRole('dialog')
  await replace.waitFor({ state: 'visible' })
  return ctx.tab.playwright.domSnapshot()
}

export async function recordActualDose(ctx, { name, field, value, label }) {
  await ctx.tab.playwright.locator('h1#exercise-title').filter({ hasText: name })
    .waitFor({ state: 'visible' })
  await waitForSettledView(ctx)
  const snapshot = await ctx.tab.playwright.domSnapshot()
  if (snapshot.includes('textbox "重量，千克"')) throw Error(`Unweighted dose exposes a kg field: ${name}`)
  await ctx.tab.playwright.getByRole('textbox', { name: field, exact: true }).fill(String(value))
  const complete = ctx.tab.playwright.getByRole('button', { name: /^完成第 \d+ 组$/ })
  if (!await complete.isEnabled()) throw Error(`Actual valid dose is blocked: ${name}`)
  await complete.click()
  const result = await recordFlow(ctx, label)
  return result
}

export async function skipActualRest(ctx) {
  const skip = ctx.tab.playwright.getByRole('button', { name: '提前结束休息', exact: true })
  if (await skip.isVisible()) await skip.click()
  await waitForSettledView(ctx)
  return ctx.tab.playwright.domSnapshot()
}

export async function prepareCore(ctx) {
  await resetSample(ctx)
  return startPublishedPlan(ctx, '核心稳定 · 20 分钟')
}

export async function prepareMobility(ctx) {
  await resetSample(ctx)
  return startPublishedPlan(ctx, '训练后舒展 · 髋与肩')
}
