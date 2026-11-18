// @vitest-environment jsdom
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'

beforeEach(() => {
  vi.resetModules()
  window.localStorage.clear()
  window.scrollTo = vi.fn()
  window.matchMedia = vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })
})
afterEach(() => { document.body.innerHTML = ''; vi.restoreAllMocks() })

describe('R05 内容与服务接缝', () => {
  it('首页主操作进入同一记录，保留模式、组 ID 和未确认输入', async () => {
    const store = await import('../state/store')
    const { default: Today } = await import('../views/TodayView.vue')
    store.setInputValue('s-a1-4', 'weightKg', '15')
    store.setInputValue('s-a1-4', 'reps', '8')
    store.state.recordingMode = 'set'
    const before = JSON.stringify({ session: store.state.session, inputs: store.state.inputs, mode: store.state.recordingMode })
    const wrapper = mount(Today)
    await wrapper.get('.studio-record-cta').trigger('click')
    expect(store.state.screen).toBe('training')
    expect(JSON.stringify({ session: store.state.session, inputs: store.state.inputs, mode: store.state.recordingMode })).toBe(before)
    wrapper.unmount()
  })

  it('教练跟进区域不把别人的点评归给当前教练', async () => {
    const store = await import('../state/store')
    const { default: Today } = await import('../views/TodayView.vue')
    for (const record of store.state.records) if (record.feedback) record.feedback.author = '另一位教练'
    const wrapper = mount(Today)
    expect(wrapper.get('.studio-coach-panel').text()).toContain('顾言')
    expect(wrapper.find('.studio-coach-panel blockquote').exists()).toBe(false)
    expect(wrapper.get('.studio-coach-panel').text()).toContain('当前服务')
    wrapper.unmount()
  })

  it('切换服务包同步交付清单、报价，并将该服务带入咨询', async () => {
    const store = await import('../state/store')
    const { default: Coach } = await import('../views/CoachDetailView.vue')
    store.goToCoachDetail('gu-yan')
    const wrapper = mount(Coach)
    const single = wrapper.findAll('[role="radio"]').find(item => item.text().includes('动作调整'))!
    await single.trigger('click')
    expect(wrapper.get('[aria-label="当前服务交付内容"]').text()).toContain('一个动作的完整核对')
    expect(wrapper.get('.studio-cover-offer').text()).toContain('¥199 / 次')
    expect(wrapper.get('[aria-label="当前服务交付内容"]').text()).not.toContain('每周 3 次')
    await wrapper.get('.coach-actions button').trigger('click')
    await flushPromises()
    expect(store.state.screen).toBe('messages')
    expect(store.ui.activeConversationId).not.toBeNull()
    expect(store.state.conversations.find(item => item.id === store.ui.activeConversationId)?.packageId).toBe('pkg-check')
    wrapper.unmount()
  })

  it('内容卡收藏只改变收藏，打开卡片进入对应计划', async () => {
    const store = await import('../state/store')
    const { EXPLORE_ITEMS } = await import('../data/sample')
    const { default: Card } = await import('../components/ContentCard.vue')
    const item = EXPLORE_ITEMS[1]!
    const wrapper = mount(Card, { props: { item } })
    const saved = JSON.stringify(store.state.session)
    const wasSaved = store.state.favorites.includes(item.id)
    await wrapper.get('.content-card-save').trigger('click')
    expect(store.state.favorites.includes(item.id)).toBe(!wasSaved)
    expect(JSON.stringify(store.state.session)).toBe(saved)
    await wrapper.get('.content-card-open').trigger('click')
    expect(store.state.screen).toBe('planDetail')
    expect(store.ui.selectedPlanId).toBe(item.id)
    wrapper.unmount()
  })

  it('教练列表的报价与展示的交付服务一致，不用其他低价服务作起价', async () => {
    const store = await import('../state/store')
    const { default: Explore } = await import('../views/ExploreView.vue')
    store.setExploreMode('coach')
    const wrapper = mount(Explore)
    const coach = wrapper.get('[aria-label="查看教练 顾言"]')
    expect(coach.text()).toContain('每周 3 次训练安排')
    expect(coach.text()).toContain('¥699 / 月')
    expect(coach.text()).not.toContain('¥199')
    wrapper.unmount()
  })

  it('图片失败可读，换到另一份内容时重新尝试加载新图', async () => {
    const { default: Photo } = await import('../components/PhotoFrame.vue')
    const wrapper = mount(Photo, { props: { src: '/missing.webp', alt: '训练内容示意' } })
    await wrapper.get('img').trigger('error')
    expect(wrapper.get('[role="img"]').attributes('aria-label')).toContain('图片暂不可用')
    await wrapper.setProps({ src: '/assets/explore-plank.webp' })
    expect(wrapper.get('img').attributes('src')).toBe('/assets/explore-plank.webp')
    wrapper.unmount()
  })
})
