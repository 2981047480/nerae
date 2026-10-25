// Lead QA inventory. These actions use only observed, normal preview/navigation UI.
import { gotoScenario } from '../fix-02/cua_review_v3.mjs'
import { resetSample, waitForSettledView } from './cua_final_review.mjs'

export const cases = [
  ['s01-today', /^今天 /, '上肢力量', 'student'],
  ['s02-explore', /^发现 /, '发现', 'student'],
  ['s03-plan', /^计划详情 /, '上肢拉力与划船', 'student'],
  ['s04-training', /^训练 \d/, '哑铃卧推', 'student'],
  ['s05-records', /^记录 /, '记录', 'student'],
  ['s06-coaches', /^教练 \d/, '发现', 'student'],
  ['s07-coach-detail', /^教练详情 /, '顾言', 'student'],
  ['s08-student-list', null, '消息', 'student'],
  ['s08-student-thread', null, '顾言', 'student'],
  ['s08-coach-list', null, '消息', 'coach'],
  ['s08-coach-thread', null, '林澈', 'coach'],
  ['s09-incoming', /^收到计划 /, '收到新版计划', 'student'],
  ['s10-own-plan', /^我的计划 /, '我的计划', 'student'],
  ['s11-ai', /^AI 整理 /, '整理这份计划', 'student'],
  ['s12-diet', /^饮食自评 /, '训练日三餐 · 分配原则', 'student'],
  ['s13-todo', /^待办 /, '待办', 'coach'],
  ['s14-students', /^学员 \d/, '学员', 'coach'],
  ['s14-student-detail', null, '林澈', 'coach'],
  ['s15-review', /^计划审阅 /, '力量基础 · 第 4 周', 'coach'],
  ['s15-edit', /^计划编辑 /, '力量基础 · 第 4 周', 'coach'],
  ['s16-services', /^服务 /, '服务内容', 'coach'],
  ['s01-current-plan', null, '力量基础', 'student'],
].map(([family, scenario, heading, actor]) => ({ family, scenario, heading, actor }))

export async function prepareCase(ctx, family) {
  const item = cases.find(item => item.family === family)
  if (!item) throw Error(`Unknown QA case ${family}`)
  await resetSample(ctx)
  if (item.scenario) await gotoScenario(ctx, item.scenario)
  else if (family.startsWith('s08-')) {
    if (item.actor === 'coach') {
      await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
      await ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
        .getByRole('button', { name: '教练 顾言', exact: true }).click()
    }
    await ctx.tab.playwright.getByRole('navigation', {
      name: item.actor === 'coach' ? '教练主导航' : '学员主导航', exact: true,
    }).getByRole('button', { name: '消息', exact: true }).click()
    if (family.endsWith('thread')) {
      await ctx.tab.playwright.getByRole('button', {
        name: item.actor === 'coach' ? /^林 林澈 / : /^顾 顾言 /,
      }).click()
    }
  } else if (family === 's14-student-detail') {
    await gotoScenario(ctx, /^学员 \d/)
    await ctx.tab.playwright.getByRole('button', { name: /^林 林澈 在读/ }).click()
  } else if (family === 's01-current-plan') {
    await ctx.tab.playwright.getByRole('button', { name: /^现用安排 · 版本 6/ }).click()
  }
  await ctx.tab.playwright.getByRole('heading', { name: item.heading, exact: true }).first()
    .waitFor({ state: 'visible' })
  await waitForSettledView(ctx)
  const header = await ctx.tab.playwright.evaluate(() => document.querySelector('header')?.textContent ?? '')
  const visibleCoach = header.replace(/\s/g, '').includes('顾言·教练')
  if (visibleCoach !== (item.actor === 'coach')) {
    throw Error(`Rendered actor mismatch for ${family}: ${header}`)
  }
  if (family === 's06-coaches' && await ctx.tab.playwright.getByRole('tab', { name: '教练', exact: true })
    .getAttribute('aria-selected') !== 'true') throw Error('Coach discovery tab not selected')
  const result = { family, expectedActor: item.actor, renderedHeader: header, expectedHeading: item.heading,
    sourceRevision: ctx.sourceRevision, verified: true }
  await ctx.fs.appendFile(`${ctx.evidenceDir}/normal-case-preparation.jsonl`, JSON.stringify(result) + '\n')
  return result
}
