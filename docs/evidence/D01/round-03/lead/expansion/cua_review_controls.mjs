// Normal preview controls only; does not change operating-system preferences.
export async function setMotion(ctx, reduced) {
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
  const control = dialog.getByRole('switch', { name: '减少动态效果', exact: true })
  if (await control.getAttribute('aria-checked') !== String(reduced)) await control.click()
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await ctx.qa.waitForSettledView(ctx)
  return ctx.tab.playwright.domSnapshot()
}

export async function setTextScale(ctx, scale) {
  await ctx.tab.playwright.getByRole('button', { name: '打开预览控制', exact: true }).click()
  const dialog = ctx.tab.playwright.getByRole('dialog', { name: '预览', exact: true })
  await dialog.getByRole('button', { name: `${scale}%`, exact: true }).click()
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await ctx.qa.waitForSettledView(ctx)
  return ctx.tab.playwright.domSnapshot()
}
