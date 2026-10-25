// Actual CUA UI actions only. Run stages in order on a dedicated disposable preview.
import { resetSample, recordFlow, waitForSettledView } from './cua_final_review.mjs'

export const revisionNote = '先保持动作稳定，本周保留两次余力。'

async function role(ctx, label) {
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  await ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
    .getByRole('button', { name: label, exact: true }).click()
}

async function planVersions(ctx) {
  return ctx.tab.playwright.evaluate(() => Object.fromEntries(
    [...document.querySelectorAll('dt')].map(e => [e.textContent.trim(), e.nextElementSibling?.textContent.trim()]),
  ))
}

export async function publishLinEight(ctx) {
  await (ctx.qa?.resetSample ?? resetSample)(ctx)
  await role(ctx, '教练 顾言')
  await ctx.tab.playwright.getByRole('heading', { name: '待办', exact: true }).waitFor({ state: 'visible' })
  await ctx.tab.playwright.getByRole('button', { name: '查看学员', exact: true }).click()
  await ctx.tab.playwright.getByRole('heading', { name: '林澈', exact: true }).waitFor({ state: 'visible' })
  await ctx.tab.playwright.getByRole('button', { name: '继续计划草稿', exact: true }).click()
  await ctx.tab.playwright.getByRole('button', { name: '编辑', exact: true }).click()
  await ctx.tab.playwright.getByRole('textbox', { name: '动作备注', exact: true }).first()
    .waitFor({ state: 'visible' })
  await ctx.tab.playwright.getByRole('textbox', { name: '动作备注', exact: true }).first().fill(revisionNote)
  await ctx.tab.playwright.getByRole('button', { name: '保存', exact: true }).click()
  const saved = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-lin-eight-saved')
  if (!saved.snapshot.includes('草稿版本 8')) throw Error('Expected saved draft8 in actual UI')
  await ctx.tab.reload()
  await ctx.tab.playwright.getByRole('textbox', { name: '动作备注', exact: true }).first()
    .waitFor({ state: 'visible' })
  const restored = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-lin-eight-reloaded')
  if (!restored.snapshot.includes(revisionNote)) throw Error('Edited note did not survive reload')
  await ctx.tab.playwright.getByRole('button', { name: '审阅', exact: true }).click()
  await ctx.tab.playwright.getByRole('button', { name: '发布给林澈', exact: true }).click()
  const sheet = ctx.tab.playwright.getByRole('dialog', { name: '发布给 林澈', exact: true })
  await sheet.waitFor({ state: 'visible' })
  await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-lin-eight-publish-review')
  await sheet.getByRole('button', { name: '取消', exact: true }).click()
  const canceled = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-lin-eight-publish-canceled')
  const canceledVersions = await planVersions(ctx)
  if (!/版本\s*7\b/.test(canceledVersions['已交付']) || !/版本\s*8\b/.test(canceledVersions['草稿'])) {
    throw Error('Cancel did not preserve published7 and private draft8')
  }
  await ctx.tab.playwright.getByRole('button', { name: '发布给林澈', exact: true }).click()
  await sheet.getByRole('button', { name: '发布 版本 8', exact: true }).click()
  await sheet.waitFor({ state: 'hidden' })
  const published = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-lin-eight-published-current-six')
  const publishedVersions = await planVersions(ctx)
  if (!/版本\s*6\b/.test(publishedVersions['现用']) || !/版本\s*8\b/.test(publishedVersions['已交付'])) {
    throw Error('Actual publish result does not show new8/current6')
  }
  return { sourceRevision: ctx.sourceRevision, published8: true, current6: true, savedAndReloaded: true,
    queueToDraftNavigations: 2, scope: 'Local UI sample, no server or real professional review' }
}

export async function visitZhouThenOpenOldLin(ctx) {
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  await ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
    .getByRole('button', { name: /^学员 \d/ }).click()
  await ctx.tab.playwright.getByRole('button', { name: /^周 周岚 在读/ }).click()
  await ctx.tab.playwright.getByRole('heading', { name: '周岚', exact: true }).waitFor({ state: 'visible' })
  await ctx.tab.playwright.getByRole('button', { name: '回复', exact: true }).click()
  await ctx.tab.playwright.getByRole('heading', { name: '周岚', exact: true }).waitFor({ state: 'visible' })
  await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-last-coach-context-zhou')
  await role(ctx, '学员 林澈')
  await ctx.tab.playwright.getByRole('navigation', { name: '学员主导航', exact: true })
    .getByRole('button', { name: '消息', exact: true }).click()
  await ctx.tab.playwright.getByRole('button', { name: /^顾 顾言 / }).click()
  await ctx.tab.playwright.getByRole('button', { name: /^计划 版本 7/ }).click()
  await ctx.tab.playwright.getByRole('heading', { name: '收到新版计划', exact: true }).waitFor({ state: 'visible' })
  await (ctx.qa?.waitForSettledView ?? waitForSettledView)(ctx)
  const opened = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-student-old-seven-after-eight')
  if (opened.snapshot.includes(revisionNote)) throw Error('Old7 unexpectedly contains new8 note')
  await ctx.tab.reload()
  await ctx.tab.playwright.getByRole('heading', { name: '收到新版计划', exact: true }).waitFor({ state: 'visible' })
  const reloaded = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-student-old-seven-cold')
  if (reloaded.snapshot.includes(revisionNote) || !reloaded.snapshot.includes('版本 7 已被版本 8 替代')) {
    throw Error('Old7 cold reference lost its immutable snapshot')
  }
  await ctx.tab.playwright.getByRole('button', { name: '返回上一页', exact: true }).click()
  await ctx.tab.playwright.getByRole('textbox', { name: '写消息', exact: true }).waitFor({ state: 'visible' })
  await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-old-seven-back-to-gu')
  return { sourceRevision: ctx.sourceRevision, old7SnapshotRetained: true, lastCoachWasZhou: true,
    coldReloadRetained: true, returnedToMessageComposer: true }
}

export async function adoptEightWithCancelAndHistory(ctx) {
  await ctx.tab.playwright.getByRole('button', { name: /^计划 版本 8/ }).click()
  await ctx.tab.playwright.getByRole('heading', { name: '收到新版计划', exact: true }).waitFor({ state: 'visible' })
  const latest = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-student-new-eight-full-content')
  if (!latest.snapshot.includes(revisionNote)) throw Error('New8 does not contain the edited note')
  await ctx.tab.playwright.getByRole('button', { name: '采用这份安排', exact: true }).click()
  const sheet = ctx.tab.playwright.getByRole('dialog', { name: '采用这份安排', exact: true })
  await sheet.waitFor({ state: 'visible' })
  await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-adopt-eight-impact-review')
  await sheet.getByRole('button', { name: '取消', exact: true }).click()
  const canceled = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-adopt-eight-canceled')
  if (!canceled.snapshot.includes('版本 6')) throw Error('Cancel no longer shows prior current6')
  await ctx.tab.playwright.getByRole('button', { name: '采用这份安排', exact: true }).click()
  await sheet.getByRole('button', { name: '确认采用版本 8', exact: true }).click()
  await sheet.waitFor({ state: 'hidden' })
  const adopted = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-adopt-eight-confirmed')
  if (!adopted.snapshot.includes('已采用版本 8')) throw Error('Adoption did not reach its visible terminal state')
  await ctx.tab.reload()
  await ctx.tab.playwright.getByRole('heading', { name: '收到新版计划', exact: true }).waitFor({ state: 'visible' })
  await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-adopt-eight-reloaded')
  await ctx.tab.playwright.getByRole('button', { name: '返回上一页', exact: true }).click()
  await ctx.tab.playwright.getByRole('textbox', { name: '写消息', exact: true }).waitFor({ state: 'visible' })
  await ctx.tab.playwright.getByRole('navigation', { name: '学员主导航', exact: true })
    .getByRole('button', { name: '今天', exact: true }).click()
  await ctx.tab.playwright.getByRole('button', { name: '继续 哑铃卧推 · 第 4 组', exact: true })
    .waitFor({ state: 'visible' })
  const today = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-after-adoption-original-three-preserved')
  if (!today.snapshot.includes('已完成 3 / 12 组')) throw Error('Adoption changed today confirmed count')
  await ctx.tab.playwright.getByRole('navigation', { name: '学员主导航', exact: true })
    .getByRole('button', { name: '记录', exact: true }).click()
  const recordButton = ctx.tab.playwright.getByRole('button', { name: /^10月22日 周四 上肢力量/ })
  if (await recordButton.getAttribute('aria-expanded') !== 'true') await recordButton.click()
  const history = await (ctx.qa?.recordFlow ?? recordFlow)(ctx, 'f03-after-adoption-history-retained')
  if (!history.snapshot.includes('12.5 kg × 10 次')) throw Error('Historical confirmed values unavailable')
  return { sourceRevision: ctx.sourceRevision, canceledBeforeConfirmation: true, adopted8: true,
    reloadRetained: true, originalThreeSetsPreserved: true, historicalValuesRetained: true }
}
