// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { h, nextTick } from 'vue'
import BaseSheet from '../components/BaseSheet.vue'

beforeEach(() => {
  document.body.innerHTML = ''
  document.body.style.overflow = ''
  Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
      return document.body
    },
  })
})

afterEach(() => {
  document.body.innerHTML = ''
  vi.unstubAllGlobals()
})

describe('弹层焦点', () => {
  it('打开后焦点在弹层内，初始 Shift+Tab 不逃出，关闭后回到触发按钮', async () => {
    const outside = document.createElement('button')
    outside.textContent = '外部预览'
    document.body.appendChild(outside)
    outside.focus()

    const wrapper = mount(BaseSheet, {
      props: { open: false, title: '测试弹层' },
      slots: { default: () => h('button', { class: 'inside-action' }, '弹层内操作') },
      attachTo: document.body,
    })

    await wrapper.setProps({ open: true })
    await nextTick()
    await nextTick()

    const dialog = document.querySelector('[role="dialog"]') as HTMLElement
    expect(dialog).toBeTruthy()
    expect(dialog.contains(document.activeElement)).toBe(true)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }))
    expect(dialog.contains(document.activeElement)).toBe(true)
    console.info(
      '[fix-evidence] modal-focus',
      JSON.stringify({
        initialInside: dialog.contains(document.activeElement),
        activeLabel: (document.activeElement as HTMLElement | null)?.textContent?.trim(),
      }),
    )

    const focusables = Array.from(
      dialog.querySelectorAll<HTMLElement>('button:not([disabled])'),
    )
    const last = focusables[focusables.length - 1]!
    last.focus()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }))
    expect(document.activeElement).toBe(focusables[0])

    await wrapper.setProps({ open: false })
    await nextTick()
    expect(document.activeElement).toBe(outside)
    wrapper.unmount()
  })
})

describe('应用结构与未知过渡根节点', () => {
  async function mountApp() {
    vi.resetModules()
    window.localStorage.clear()
    window.matchMedia = vi.fn().mockImplementation(() => ({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }))
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => setTimeout(() => cb(0), 0))
    window.scrollTo = vi.fn()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const { default: App } = await import('../App.vue')
    const store = await import('../state/store')
    const wrapper = mount(App, { attachTo: document.body })
    await flushPromises()
    await nextTick()
    return { wrapper, store, warn }
  }

  it('四种情境各自只有一个 #main，跳转目标有效', async () => {
    const { wrapper, store } = await mountApp()
    const screens = ['today', 'explore', 'coach-review', 'training'] as const
    for (const screen of screens) {
      store.jumpToScenario(screen)
      await flushPromises()
      await nextTick()
      expect(document.querySelectorAll('main')).toHaveLength(1)
      expect(document.querySelectorAll('#main')).toHaveLength(1)
      const skip = document.querySelector<HTMLAnchorElement>('a[href="#main"]')
      expect(skip).toBeTruthy()
      expect(document.querySelector<HTMLElement>(skip!.getAttribute('href')!)).toBeTruthy()
    }
    wrapper.unmount()
  })

  it('今天、发现、教练审阅切换不产生 Transition 非元素根警告', async () => {
    const { wrapper, store, warn } = await mountApp()
    const transitions = ['explore', 'coach-review', 'today'] as const
    for (const screen of transitions) {
      store.jumpToScenario(screen)
      await flushPromises()
      await nextTick()
    }
    const messages = warn.mock.calls.map((call) => String(call[0])).join('\n')
    expect(messages).not.toContain('non-element root')
    wrapper.unmount()
  })

  it('记录第 4 组后反馈指向该组，页面不显示休息计时器', async () => {
    const { wrapper, store } = await mountApp()
    store.jumpToScenario('training')
    await flushPromises()
    await nextTick()
    store.setInputValue('s-a1-4', 'weightKg', '12.5')
    store.setInputValue('s-a1-4', 'reps', '10')
    expect(store.completeCurrentSet()).toBe(true)
    await flushPromises()
    await nextTick()
    const text = document.body.textContent ?? ''
    expect(text).toContain('第 4 组已记录')
    expect(document.querySelector<HTMLInputElement>('[aria-label="哑铃卧推 第 4 组重量 kg"]')?.value).toBe('12.5')
    expect(text).not.toContain('倒计时')
    expect(store.state.session.exercises[0]!.sets[3]!.completedAt).not.toBeNull()
    expect(
      Date.parse(store.state.session.exercises[0]!.sets[3]!.completedAt!),
    ).toBeGreaterThan(Date.parse('2026-10-24T19:12:00+08:00'))
    console.info(
      '[r04-evidence] logging-feedback',
      JSON.stringify({
        text: document.querySelector('.log-result')?.textContent?.replace(/\s+/g, ' ').trim(),
        completedAt: store.state.session.exercises[0]!.sets[3]!.completedAt,
      }),
    )
    store.resetPreview()
    wrapper.unmount()
  })

  it('首页以记录为主操作，不进入训练播放器', async () => {
    const { wrapper } = await mountApp()
    const cta = document.querySelector('.journal-primary')
    expect(cta?.textContent).toContain('继续填写')
    expect(document.querySelector('.hero-media')).toBeNull()
    expect(document.body.textContent).not.toContain('开始这次训练')
    wrapper.unmount()
  })

  it('发布弹层跨断点取消时，焦点回到同任务的可见触发', async () => {
    const { wrapper, store } = await mountApp()
    store.jumpToScenario('coach-edit')
    await flushPromises()
    await nextTick()
    store.updatePlanField('day-a', 'a1', 'sets', '5')
    expect(store.saveDraft()).toBe(true)
    await nextTick()
    const triggers = Array.from(
      document.querySelectorAll<HTMLButtonElement>('[data-publish-trigger]'),
    )
    expect(triggers.length).toBe(2)
    const desktopTrigger = triggers[0]!
    const mobileTrigger = triggers[1]!
    desktopTrigger.focus()
    expect(document.activeElement).toBe(desktopTrigger)
    store.requestPublish()
    await flushPromises()
    await nextTick()
    expect(document.querySelector('[role="dialog"]')).toBeTruthy()

    // 模拟 1440→390 的断点变化：桌面触发被隐藏，移动触发可见
    desktopTrigger.style.display = 'none'
    mobileTrigger.style.display = 'inline-flex'

    store.closeSheet()
    await flushPromises()
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 10))
    expect(document.activeElement).toBe(mobileTrigger)
    expect(document.activeElement).not.toBe(document.body)
    wrapper.unmount()
  })

  it('200% 预览下发布确认的“版本 N”词组保持完整', async () => {
    const { wrapper, store } = await mountApp()
    store.jumpToScenario('coach-edit')
    await flushPromises()
    await nextTick()
    store.updatePlanField('day-a', 'a1', 'sets', '5')
    store.saveDraft()
    store.requestPublish()
    await flushPromises()
    await nextTick()
    store.setTextScale(2)
    await nextTick()
    const versionPhrases = Array.from(document.querySelectorAll('.keep-phrase')).map((node) =>
      node.textContent?.replace(/\s+/g, ' ').trim(),
    )
    expect(versionPhrases).toContain('草稿版本 8')
    expect(versionPhrases).toContain('版本 6')
    const publishButton = Array.from(document.querySelectorAll<HTMLButtonElement>('.publish-actions .btn')).find(
      (button) => button.textContent?.includes('发布版本'),
    )
    expect(publishButton?.querySelector('.keep-phrase')?.textContent).toContain('版本 8')
    wrapper.unmount()
  })

  it('文字放大预览入口可在 100%/200% 切换并随重置回到 100%', async () => {
    const { wrapper, store } = await mountApp()
    store.setTextScale(2)
    await nextTick()
    expect(document.documentElement.dataset.textScale).toBe('200')
    store.openSheet('preview')
    await flushPromises()
    const scaleButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('.text-scale-btn'))
    expect(scaleButtons).toHaveLength(2)
    expect(scaleButtons[0]?.getAttribute('aria-pressed')).toBe('false')
    expect(scaleButtons[1]?.getAttribute('aria-pressed')).toBe('true')
    store.resetPreview()
    await flushPromises()
    await nextTick()
    expect(document.documentElement.dataset.textScale).toBe('100')
    expect(store.state.textScale).toBe(1)
    wrapper.unmount()
  })

  it('发现可直接搜索，清除条件可恢复列表', async () => {
    const { wrapper, store } = await mountApp()
    store.jumpToScenario('explore')
    await flushPromises()
    const input = document.querySelector<HTMLInputElement>('.library-search input')!
    expect(input).toBeTruthy()
    input.value = '平板'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(document.querySelector('.library-caption')?.textContent).not.toContain('平板')
    expect(store.ui.exploreQuery).toBe('平板')
    const clear = Array.from(document.querySelectorAll('button')).find(b => b.textContent?.trim() === '清除条件')!
    clear.click()
    await nextTick()
    expect(input.value).toBe('')
    store.resetPreview()
    wrapper.unmount()
  })

  it('教练编辑的名称与训练日输入有真实关联 label', async () => {
    const { wrapper, store } = await mountApp()
    store.jumpToScenario('coach-edit')
    await flushPromises()
    await nextTick()
    const nameInput = document.querySelector<HTMLInputElement>('.name-input')
    const dayInput = document.querySelector<HTMLInputElement>('.day-title-input')
    expect(nameInput?.id).toBeTruthy()
    expect(dayInput?.id).toBeTruthy()
    expect(document.querySelector(`label[for="${nameInput!.id}"]`)?.textContent).toContain('动作名称')
    expect(document.querySelector(`label[for="${dayInput!.id}"]`)?.textContent).toContain('训练日名称')
    wrapper.unmount()
  })

  it('发布成功后焦点落到状态区，不留在已禁用触发点或 BODY', async () => {
    const { wrapper, store } = await mountApp()
    store.jumpToScenario('coach-edit')
    await flushPromises()
    await nextTick()
    store.updatePlanField('day-a', 'a1', 'sets', '5')
    expect(store.saveDraft()).toBe(true)
    store.requestPublish()
    await flushPromises()
    await nextTick()
    expect(document.querySelector('[role="dialog"]')).toBeTruthy()
    store.confirmPublish()
    await flushPromises()
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 10))
    expect(document.activeElement?.classList.contains('status-row')).toBe(true)
    wrapper.unmount()
  })

  it('记录页直接显示填写界面，不生成播放器标题飞入层', async () => {
    const { wrapper, store } = await mountApp()
    store.enterTraining()
    await flushPromises()
    await nextTick()
    expect(document.querySelector('.log-mode')).toBeTruthy()
    expect(document.querySelector('.session-focus-ghost')).toBeNull()
    expect(document.querySelector('.training-rest')).toBeNull()
    store.setReduceMotion(true)
    await nextTick()
    expect(document.documentElement.dataset.motion).toBe('reduced')
    wrapper.unmount()
  })

  it('预览减少动态效果同步到 documentElement，覆盖 Teleport 弹层', async () => {
    const { wrapper, store } = await mountApp()
    store.setReduceMotion(true)
    await nextTick()
    expect(document.documentElement.dataset.motion).toBe('reduced')
    store.openSheet('preview')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')).toBeTruthy()
    expect(document.documentElement.dataset.motion).toBe('reduced')
    store.setReduceMotion(false)
    await nextTick()
    expect(document.documentElement.dataset.motion).toBe('full')
    wrapper.unmount()
  })
})
