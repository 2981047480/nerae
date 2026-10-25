import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

interface StorageStub {
  fail: boolean
  writes: number
  store: Map<string, string>
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

function createStorage(fail = false): StorageStub {
  const stub: StorageStub = {
    fail,
    writes: 0,
    store: new Map<string, string>(),
    getItem(key) {
      return stub.store.get(key) ?? null
    },
    setItem(key, value) {
      if (stub.fail) throw new DOMException('QuotaExceededError')
      stub.writes += 1
      stub.store.set(key, value)
    },
    removeItem(key) {
      stub.store.delete(key)
    },
  }
  return stub
}

function installWindow(storage: StorageStub): void {
  const win: any = {
    localStorage: storage,
    setTimeout: (...args: any[]) => setTimeout(...(args as [any, any])),
    clearTimeout: (id: any) => clearTimeout(id),
    setInterval: (...args: any[]) => setInterval(...(args as [any, any])),
    clearInterval: (id: any) => clearInterval(id),
    addEventListener: vi.fn(),
    matchMedia: () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }),
    scrollTo: vi.fn(),
    scrollY: 0,
  }
  vi.stubGlobal('window', win)
}

async function loadStore(storage = createStorage()) {
  installWindow(storage)
  const store = await import('../state/store')
  return { store, storage }
}

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('持久化与错误', () => {
  it('一次导航后防抖保存一次，空闲不持续自触发', async () => {
    const { store, storage } = await loadStore()
    store.jumpToScenario('explore')
    await vi.advanceTimersByTimeAsync(250)
    const writes = storage.writes
    expect(writes).toBeGreaterThan(0)
    await vi.advanceTimersByTimeAsync(1500)
    expect(storage.writes).toBe(writes)
    console.info('[fix-evidence] idle-persist', JSON.stringify({ writesAfterDebounce: writes, writesAfterIdle: storage.writes }))
  })

  it('重置后旧防抖任务不把旧页面写回', async () => {
    const { store, storage } = await loadStore()
    store.jumpToScenario('explore')
    store.resetPreview()
    await vi.advanceTimersByTimeAsync(600)
    const raw = storage.getItem('nerae.round3.prototype.v1')
    expect(raw).not.toBeNull()
    const saved = JSON.parse(raw!)
    expect(saved.screen).toBe('today')
    expect(saved.actor).toBe('student')
    expect(store.state.screen).toBe('today')
  })

  it('存储失败保留内存输入并明确提示，恢复后可重试保存', async () => {
    const storage = createStorage(true)
    const { store } = await loadStore(storage)
    store.jumpToScenario('coach-review')
    store.updatePlanField('day-a', 'a1', 'name', '哑铃卧推（调整）')
    const first = store.saveDraft()
    expect(first).toBe(false)
    expect(store.state.coach.draftVersion).toBe(7)
    expect(store.state.coach.draft.days[0]!.exercises[0]!.name).toBe('哑铃卧推（调整）')
    const failureNote = store.ui.saveNote
    expect(failureNote).toContain('未能保存')
    expect(store.storageState.ok).toBe(false)
    expect(store.state.coach.savedContent).not.toContain('哑铃卧推（调整）')

    storage.fail = false
    const second = store.saveDraft()
    expect(second).toBe(true)
    expect(store.state.coach.draftVersion).toBe(8)
    expect(store.storageState.ok).toBe(true)
    expect(store.state.coach.savedContent).toContain('哑铃卧推（调整）')
    console.info(
      '[fix-evidence] storage-failure',
      JSON.stringify({ firstSave: first, failureNote, recoveredNote: store.ui.saveNote, finalVersion: store.state.coach.draftVersion, recovered: store.storageState.ok }),
    )
  })
})

describe('草稿版本与不可变交付快照', () => {
  it('内容变化保存产生新版本，无改动保存不增版', async () => {
    const { store } = await loadStore()
    store.updatePlanField('day-a', 'a1', 'sets', '5')
    expect(store.saveDraft()).toBe(true)
    expect(store.state.coach.draftVersion).toBe(8)
    expect(store.saveDraft()).toBe(false)
    expect(store.state.coach.draftVersion).toBe(8)
  })

  it('交付后保留不可变快照，后续编辑不改变已交付与学员现用内容', async () => {
    const { store } = await loadStore()
    store.updatePlanField('day-a', 'a1', 'sets', '5')
    expect(store.saveDraft()).toBe(true)
    store.requestPublish()
    store.confirmPublish()

    expect(store.state.coach.deliveries.some((item) => item.version === 8)).toBe(true)
    expect(store.state.coach.activeStudentVersion).toBe(6)
    expect(store.state.coach.published.days[0]!.exercises[0]!.sets).toBe('4')
    expect(store.state.coach.deliveries).toHaveLength(2)
    const delivered = store.state.coach.deliveries.find((item) => item.version === 8)!
    expect(delivered).toBeTruthy()
    expect(delivered.content.days[0]!.exercises[0]!.sets).toBe('5')

    store.updatePlanField('day-a', 'a1', 'sets', '6')
    expect(store.saveDraft()).toBe(true)
    expect(store.state.coach.draftVersion).toBe(9)
    expect(store.state.coach.deliveries.find((item) => item.version === 8)!.content.days[0]!.exercises[0]!.sets).toBe('5')
    expect(store.state.coach.published.days[0]!.exercises[0]!.sets).toBe('4')
    expect(store.state.coach.activeStudentVersion).toBe(6)
    console.info(
      '[fix-evidence] versions',
      JSON.stringify({
        deliveredVersion: store.state.coach.deliveries[store.state.coach.deliveries.length - 1]?.version,
        draftVersion: store.state.coach.draftVersion,
        activeStudentVersion: store.state.coach.activeStudentVersion,
        publishedFirstSets: store.state.coach.published.days[0]!.exercises[0]!.sets,
        deliveredFirstSets: store.state.coach.deliveries[0]!.content.days[0]!.exercises[0]!.sets,
        draftFirstSets: store.state.coach.draft.days[0]!.exercises[0]!.sets,
      }),
    )
  })
})

describe('文字放大预览状态', () => {
  it('200% 持久化，重置预览恢复 100% 且不改动效偏好', async () => {
    const { store, storage } = await loadStore()
    store.setReduceMotion(true)
    store.setTextScale(2)
    expect(store.state.reduceMotion).toBe(true)
    expect(store.state.textScale).toBe(2)
    store.setReduceMotion(false)
    expect(store.state.textScale).toBe(2)
    store.setReduceMotion(true)
    await vi.advanceTimersByTimeAsync(300)
    const saved = JSON.parse(storage.getItem('nerae.round3.prototype.v1')!)
    expect(saved.textScale).toBe(2)
    expect(saved.reduceMotion).toBe(true)
    store.resetPreview()
    await vi.advanceTimersByTimeAsync(300)
    const reset = JSON.parse(storage.getItem('nerae.round3.prototype.v1')!)
    expect(reset.textScale).toBe(1)
    expect(reset.reduceMotion).toBe(false)
  })
})

describe('发现筛选偏好按内容类型保留', () => {
  it('训练器械筛选不丢失，也不套到饮食条件上', async () => {
    const { store } = await loadStore()
    store.jumpToScenario('explore')
    store.ui.filterDraft = { goal: 'all', equipment: 'machine', duration: 'all' }
    store.applyFilterDraft()
    expect(store.filteredExplore.value).toHaveLength(2)

    store.setExploreMode('diet')
    expect(store.filteredExplore.value).toHaveLength(3)
    expect(store.state.explore.trainingFilters.equipment).toBe('machine')
    expect(store.state.explore.dietFilters.equipment).toBe('all')

    store.setExploreMode('training')
    expect(store.filteredExplore.value).toHaveLength(2)
    expect(store.currentFilters.value.equipment).toBe('machine')
  })
})
