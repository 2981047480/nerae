// Lead-only normal UI helpers. Never reads or writes application internals.
export async function openRoleThread(ctx, role) {
  const coach = role === 'coach'
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
  await dialog.getByRole('button', { name: coach ? '教练 顾言' : '学员 林澈', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await ctx.qa.waitForSettledView(ctx)
  await ctx.tab.playwright.getByRole('navigation', { name: coach ? '教练主导航' : '学员主导航', exact: true })
    .getByRole('button', { name: '消息', exact: true }).click()
  await ctx.tab.playwright.getByRole('button', { name: coach ? /^林 林澈 / : /^顾 顾言 / }).click()
  await ctx.qa.waitForSettledView(ctx)
  return ctx.tab.playwright.domSnapshot()
}

export async function chooseMessageMode(ctx, label) {
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
  await dialog.getByRole('button', { name: label, exact: true }).click()
  if (await dialog.isVisible()) await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await ctx.qa.waitForSettledView(ctx)
  return ctx.tab.playwright.domSnapshot()
}
