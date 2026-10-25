// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { lastReferenceLabel, summarizeRecordSets } from '../utils/format'

async function loadStore() {
  vi.resetModules()
  window.localStorage.clear()
  const store = await import('../state/store')
  return store
}

type Store = Awaited<ReturnType<typeof loadStore>>

async function savedStore(): Promise<Store> {
  await vi.advanceTimersByTimeAsync(300)
  vi.resetModules()
  const reloaded = await import('../state/store')
  return reloaded
}

function completeSessionWithInputs(store: Store): void {
  while (!store.isComplete.value) {
    const entry = store.currentEntry.value
    if (!entry) throw new Error('no current entry')
    const input = store.state.inputs[entry.set.id] ?? { weightKg: '', reps: '', seconds: '' }
    if (entry.exercise.kind === 'duration') {
      if (!input.seconds) store.setInputValue(entry.set.id, 'seconds', '30')
    } else {
      if (!input.weightKg) store.setInputValue(entry.set.id, 'weightKg', '10')
      if (!input.reps) store.setInputValue(entry.set.id, 'reps', '10')
    }
    expect(store.completeCurrentSet()).toBe(true)
    store.skipRest()
  }
}

beforeEach(() => {
  vi.useFakeTimers()
  document.body.innerHTML = ''
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('交付与采用同源', () => {
  it('种子交付在 coach.deliveries 与 student.incoming 是同一不可变快照', async () => {
    const store = await loadStore()
    const latest = store.coachLatestDelivery.value!
    const incoming = store.state.student.incoming!
    expect(latest.version).toBe(7)
    expect(incoming.version).toBe(7)
    expect(incoming.content).toEqual(latest.content)
    expect(incoming.hash).toBe(store.coachLatestDelivery.value!.content ? incoming.hash : '')
    expect(store.state.student.planVersion).toBe(6)
  })

  it('同一版本重复发布不新增交付或消息', async () => {
    const store = await loadStore()
    const deliveries = store.state.coach.deliveries.length
    const messages = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!.messages.length
    expect(store.confirmPublish()).toBe(false)
    expect(store.state.coach.deliveries).toHaveLength(deliveries)
    expect(store.state.conversations.find((item) => item.id === 'conv-gu-yan')!.messages).toHaveLength(messages)
    store.resetPreview()
  })

  it('发布新版本只更新交付与消息，不改变学员现用版本或今天会话', async () => {
    const store = await loadStore()
    const sessionId = store.state.session.id
    const done = store.doneSets.value
    store.updatePlanField('day-a', 'a1', 'sets', '5')
    store.saveDraft()
    store.requestPublish()
    expect(store.confirmPublish()).toBe(true)
    expect(store.state.coach.deliveries.some((item) => item.version === 8)).toBe(true)
    expect(store.state.student.incoming?.version).toBe(8)
    expect(store.state.student.planVersion).toBe(6)
    expect(store.state.session.id).toBe(sessionId)
    expect(store.doneSets.value).toBe(done)
    expect(store.state.conversations.find((item) => item.id === 'conv-gu-yan')!.messages.filter((item) => item.id === 'm-plan-8')).toHaveLength(1)
    store.resetPreview()
  })

  it('采用只改未来安排，今天会话与历史记录不变', async () => {
    const store = await loadStore()
    const sessionId = store.state.session.id
    const startedAt = store.state.session.startedAt
    const history = JSON.stringify(store.state.records)
    store.openAdoptSheet()
    expect(store.confirmAdoption()).toBe(true)
    expect(store.state.student.planVersion).toBe(7)
    expect(store.state.student.adoptedVersion).toBe(7)
    expect(store.state.session.id).toBe(sessionId)
    expect(store.state.session.startedAt).toBe(startedAt)
    expect(JSON.stringify(store.state.records)).toBe(history)
    expect(store.state.student.schedule.every((slot) => slot.planVersion === 7)).toBe(true)
    store.resetPreview()
  })

  it('旧确认 hash 不对应新内容时失效，冲突需要再一次核对', async () => {
    const store = await loadStore()
    store.openAdoptSheet()
    store.state.student.incoming!.hash = 'different-hash'
    expect(store.confirmAdoption()).toBe(false)
    expect(store.state.student.planVersion).toBe(6)
    store.resetPreview()

    const second = await loadStore()
    second.applyPreviewState('incomingPlan', 'conflict')
    second.openAdoptSheet()
    expect(second.confirmAdoption()).toBe(false)
    expect(second.state.student.planVersion).toBe(6)
    expect(second.confirmAdoption()).toBe(true)
    expect(second.state.student.planVersion).toBe(7)
    second.resetPreview()
  })
})

describe('持久化当前对象与替换历史', () => {
  it('选择非默认计划后刷新仍回到同一计划', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-dumbbell')
    expect(store.ui.selectedPlanId).toBe('plan-dumbbell')
    const reloaded = await savedStore()
    expect(reloaded.state.screen).toBe('planDetail')
    expect(reloaded.ui.selectedPlanId).toBe('plan-dumbbell')
    expect(reloaded.selectedPlanItem.value?.id).toBe('plan-dumbbell')
    expect(reloaded.state.selection.selectedPlanId).toBe('plan-dumbbell')
    reloaded.resetPreview()
  })

  it('替换安排把进行中会话的已确认组归档为未完成记录，不丢事实', async () => {
    const store = await loadStore()
    expect(store.requestStartPlan('plan-dumbbell')).toBe('replace')
    store.confirmReplaceAndStart()
    const archived = store.state.records.find((record) => record.id.startsWith('archived-session-20261024-strength'))
    expect(archived).toBeTruthy()
    expect(archived!.partial).toBe(true)
    expect(archived!.sets).toHaveLength(3)
    expect(archived!.partialNote).toContain('历史保留')
    expect(store.doneSets.value).toBe(0)
    expect(store.state.session.status).toBe('in_progress')
    expect(store.state.session.id).not.toBe('session-20261024-strength')
    store.resetPreview()
  })

  it('所选训练日、替换摘要与实际会话同一对象，刷新后仍保留', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-weekly-base')
    store.selectPlanDay('plan-weekly-base-day-b')
    expect(store.activePlanDay.value?.title).toContain('训练日 B')
    expect(store.requestStartPlan('plan-weekly-base')).toBe('replace')
    store.confirmReplaceAndStart()
    expect(store.state.session.dayId).toBe('plan-weekly-base-day-b')
    expect(store.state.session.dayTitle).toContain('训练日 B')
    expect(store.state.session.exercises.map((exercise) => exercise.name)).toContain('高位下拉')
    const sessionId = store.state.session.id
    const sequence = store.state.sessionSequence
    expect(store.requestStartPlan('plan-weekly-base')).toBe('duplicate')
    expect(store.state.session.id).toBe(sessionId)
    expect(store.state.sessionSequence).toBe(sequence)

    const reloaded = await savedStore()
    expect(reloaded.ui.selectedPlanId).toBe('plan-weekly-base')
    expect(reloaded.ui.planDayId).toBe('plan-weekly-base-day-b')
    expect(reloaded.activePlanDay.value?.title).toContain('训练日 B')
    reloaded.resetPreview()
  })

  it('替换后再次开始同一计划产生唯一会话，前后已确认事实都保留', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-dumbbell')
    expect(store.requestStartPlan('plan-dumbbell')).toBe('replace')
    store.confirmReplaceAndStart()
    const firstId = store.state.session.id
    const firstSetId = store.currentEntry.value!.set.id
    store.setInputValue(firstSetId, 'weightKg', '12')
    store.setInputValue(firstSetId, 'reps', '8')
    completeSessionWithInputs(store)

    store.goToPlanDetail('plan-core')
    expect(store.requestStartPlan('plan-core')).toBe('replace')
    store.confirmReplaceAndStart()

    store.goToPlanDetail('plan-dumbbell')
    expect(store.requestStartPlan('plan-dumbbell')).toBe('replace')
    store.confirmReplaceAndStart()
    const secondId = store.state.session.id
    expect(secondId).not.toBe(firstId)
    expect(store.state.session.dayId).toBe('plan-dumbbell-day-1')
    const firstPending = store.currentEntry.value!.set.id
    store.setInputValue(firstPending, 'weightKg', '17')
    store.setInputValue(firstPending, 'reps', '9')
    completeSessionWithInputs(store)

    store.goToPlanDetail('plan-core')
    expect(store.requestStartPlan('plan-core')).toBe('replace')
    store.confirmReplaceAndStart()

    const archived = store.state.records.filter((record) => record.id.startsWith('archived-session-plan-dumbbell'))
    expect(archived).toHaveLength(2)
    expect(new Set(archived.map((record) => record.id)).size).toBe(2)
    for (const record of archived) {
      expect(record.sets).toHaveLength(12)
      expect(record.partial).toBe(false)
    }
    expect(archived.some((record) => record.sets[0]!.weightKg === 12 && record.sets[0]!.reps === 8)).toBe(true)
    expect(archived.some((record) => record.sets[0]!.weightKg === 17 && record.sets[0]!.reps === 9)).toBe(true)
    store.resetPreview()
  })

  it('今天完成后更换内容先说明未来影响，取消不改安排，确认才建立新未来', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-dumbbell')
    expect(store.requestStartPlan('plan-dumbbell')).toBe('replace')
    store.confirmReplaceAndStart()
    completeSessionWithInputs(store)
    expect(store.isComplete.value).toBe(true)
    const versionBefore = store.state.student.planVersion
    const scheduleBefore = JSON.stringify(store.state.student.schedule)
    const sessionBefore = store.state.session.id
    const recordsBefore = store.state.records.length

    store.goToPlanDetail('plan-core')
    expect(store.requestStartPlan('plan-core')).toBe('replace')
    expect(store.ui.sheet).toBe('replace')
    expect(store.state.student.planVersion).toBe(versionBefore)
    expect(JSON.stringify(store.state.student.schedule)).toBe(scheduleBefore)
    expect(store.state.session.id).toBe(sessionBefore)

    store.closeSheet()
    expect(store.state.student.planVersion).toBe(versionBefore)
    expect(JSON.stringify(store.state.student.schedule)).toBe(scheduleBefore)
    expect(store.state.records.length).toBe(recordsBefore)

    expect(store.requestStartPlan('plan-core')).toBe('replace')
    store.confirmReplaceAndStart()
    expect(store.state.student.planVersion).toBe(versionBefore + 1)
    expect(JSON.stringify(store.state.student.schedule)).not.toBe(scheduleBefore)
    const archived = store.state.records.filter((record) => record.id.startsWith('archived-session-plan-dumbbell'))
    expect(archived).toHaveLength(1)
    expect(archived[0]!.partial).toBe(false)
    expect(archived[0]!.sets).toHaveLength(12)
    expect(store.recordRows.value.filter((record) => record.id.startsWith('archived-session-plan-dumbbell'))).toHaveLength(1)
    expect(store.doneSets.value).toBe(0)
    store.resetPreview()
  })

  it('取消替换不改原会话与记录', async () => {
    const store = await loadStore()
    const session = JSON.stringify(store.state.session)
    const records = JSON.stringify(store.state.records)
    expect(store.requestStartPlan('plan-dumbbell')).toBe('replace')
    store.closeSheet()
    expect(JSON.stringify(store.state.session)).toBe(session)
    expect(JSON.stringify(store.state.records)).toBe(records)
    store.resetPreview()
  })
})

describe('消息、撤权与重试', () => {
  it('发送失败保留原文，同 ID 重试不新增消息', async () => {
    const store = await loadStore()
    store.goToConversation('conv-gu-yan')
    store.applyPreviewState('messages', 'failed')
    store.setMessageDraft('conv-gu-yan', '周六上午训练可以吗')
    const before = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!.messages.length
    expect(store.sendMessage('conv-gu-yan')).toBe(true)
    const conversation = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    const message = conversation.messages[conversation.messages.length - 1]!
    expect(message.status).toBe('failed')
    expect(message.text).toBe('周六上午训练可以吗')
    expect(store.retryMessage(message.id, 'conv-gu-yan')).toBe(true)
    expect(conversation.messages).toHaveLength(before + 1)
    expect(conversation.messages.filter((item) => item.id === message.id)).toHaveLength(1)
    expect(conversation.messages.find((item) => item.id === message.id)!.status).toBe('sent')
    store.resetPreview()
  })

  it('只读关系阻止发送但保留草稿', async () => {
    const store = await loadStore()
    store.goToConversation('conv-gu-yan')
    store.applyPreviewState('messages', 'readonly')
    store.setMessageDraft('conv-gu-yan', '还能继续吗')
    expect(store.sendMessage('conv-gu-yan')).toBe(false)
    expect(store.state.conversations.find((item) => item.id === 'conv-gu-yan')!.drafts.student).toBe('还能继续吗')
    expect(store.messagesReadOnly.value).toBe(true)
    store.resetPreview()
  })

  it('撤权不删除本人消息，教练读取面隐藏健康原文与引用，普通会话仍可发送', async () => {
    const store = await loadStore()
    store.openConversation('conv-gu-yan')
    const conversation = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    const studentHealth = conversation.messages.find((item) => item.id === 'm-health-1')!
    const feedback = conversation.messages.find((item) => item.id === 'm-feedback-1')!
    expect(store.revokeHealthAuthorization()).toBe(true)
    expect(store.state.relations.find((item) => item.id === 'rel-gu-yan')!.healthAuthorized).toBe(false)
    expect(studentHealth.text).toContain('右肩')
    expect(studentHealth.redacted).not.toBe(true)
    expect(store.messageTextForActor(conversation, studentHealth)).toContain('右肩')

    store.state.actor = 'coach'
    expect(store.messageTextForActor(conversation, studentHealth)).toBe('健康内容已不可见')
    expect(store.messageRefsForActor(conversation, feedback)).toHaveLength(0)
    expect(store.conversationHealthVisible(conversation)).toBe(false)
    store.goToCoachStudentDetail('lin-che')
    expect(store.healthAuthorizedFor(store.state.students.find((item) => item.id === 'lin-che')!)).toBe(false)
    expect(store.selectedStudentEvidence.value.map((item) => item.id)).toEqual(['lin-4'])

    store.state.actor = 'student'
    store.goToConversation('conv-gu-yan')
    store.setMessageDraft('conv-gu-yan', '普通安排还可以聊')
    expect(store.sendMessage('conv-gu-yan')).toBe(true)
    store.resetPreview()
  })

  it('咨询带入当前服务包，查看服务返回同一包，既有约定单独保留', async () => {
    const store = await loadStore()
    const agreed = store.serviceAgreement.value!.agreedPriceYuan
    store.prefillConsultation('gu-yan', 'pkg-check')
    const conversation = store.currentConversation.value!
    expect(conversation.packageId).toBe('pkg-check')
    expect(conversation.serviceLabel).toContain('动作调整')
    const check = store.state.coaches
      .find((coach) => coach.id === 'gu-yan')!
      .packages.find((pack) => pack.id === 'pkg-check')!
    expect(check.priceYuan).toBe(199)
    expect(check.priceUnit).toBe('/ 次')
    store.goToCoachDetail('gu-yan', conversation.packageId!)
    expect(store.ui.selectedPackageId).toBe('pkg-check')
    expect(store.serviceAgreement.value!.agreedPriceYuan).toBe(agreed)
    expect(store.serviceAgreement.value!.packageId).toBe('pkg-strength')
    expect(store.state.relations.find((item) => item.id === 'rel-gu-yan')!.healthAuthorized).toBe(true)
    store.resetPreview()
  })

  it('消息标题随当前会话人物变化，而不是固定顾言', async () => {
    const store = await loadStore()
    store.goToConversation('conv-he-an')
    expect(store.currentConversation.value?.coachName).toBe('何岸')
    store.goToConversation('conv-jiang-yao')
    expect(store.currentConversation.value?.coachName).toBe('江遥')
    expect(store.currentConversation.value?.serviceLabel).toContain('耐力基础')
    store.resetPreview()
  })
})

describe('教练待办、学员与服务', () => {
  it('待办计数来自集合，处理与恢复不删除事实', async () => {
    const store = await loadStore()
    expect(store.todoCounts.value.reply).toBe(1)
    expect(store.openTodo.value).toHaveLength(5)
    store.completeTodo('todo-reply')
    expect(store.todoCounts.value.reply).toBe(0)
    expect(store.openTodo.value).toHaveLength(4)
    expect(store.doneTodo.value).toHaveLength(1)
    store.restoreTodo('todo-reply')
    expect(store.openTodo.value).toHaveLength(5)
    store.resetPreview()
  })

  it('待办与详情进入周岚自己的计划，切回林澈不串交付与现用版本', async () => {
    const store = await loadStore()
    store.goToCoachStudentDetail('zhou-lan')
    store.goToCoachPlanForStudent('zhou-lan')
    expect(store.state.activeCoachStudentId).toBe('zhou-lan')
    expect(store.state.coach.draft.title).toBe('耐力基础')
    expect(store.state.coach.draft.days[1]!.title).toContain('走跑间歇')
    expect(store.state.coach.draftVersion).toBe(4)
    expect(store.state.coach.activeStudentVersion).toBe(3)
    expect(store.state.student.incoming?.version).toBe(7)

    const reloaded = await savedStore()
    expect(reloaded.state.screen).toBe('coachPlan')
    expect(reloaded.state.activeCoachStudentId).toBe('zhou-lan')
    expect(reloaded.state.coach.draft.title).toBe('耐力基础')
    expect(reloaded.ui.selectedStudentId).toBe('zhou-lan')

    reloaded.goToCoachPlanForStudent('lin-che')
    expect(reloaded.state.activeCoachStudentId).toBe('lin-che')
    expect(reloaded.state.coach.draft.title).toBe('力量基础')
    expect(reloaded.state.coach.deliveries[0]!.version).toBe(7)
    expect(reloaded.state.coach.activeStudentVersion).toBe(6)
    reloaded.resetPreview()
  })

  it('周岚发布只更新周岚交付，不改林澈收到版本或现用版本', async () => {
    const store = await loadStore()
    store.openTodoItem('todo-draft')
    expect(store.state.activeCoachStudentId).toBe('zhou-lan')
    store.updatePlanField('zhou-day-a', 'z-a1', 'target', '5 分钟')
    expect(store.saveDraft()).toBe(true)
    store.requestPublish()
    expect(store.confirmPublish()).toBe(true)
    expect(store.state.coach.deliveries.some((item) => item.version === 5)).toBe(true)
    expect(store.state.student.incoming?.version).toBe(7)
    expect(store.state.student.planVersion).toBe(6)
    expect(store.state.students.find((item) => item.id === 'zhou-lan')!.deliveredVersion).toBe(5)
    expect(store.state.students.find((item) => item.id === 'zhou-lan')!.planVersion).toBe(3)
    expect(store.state.coachSnapshots['lin-che']!.deliveries[0]!.version).toBe(7)
    store.resetPreview()
  })

  it('周岚回复按同一 studentId 打开/创建对话，不借用林澈会话', async () => {
    const store = await loadStore()
    store.goToCoachStudentDetail('zhou-lan')
    store.replyToStudent('zhou-lan')
    const conversation = store.currentConversation.value!
    expect(store.state.actor).toBe('coach')
    expect(store.state.screen).toBe('messages')
    expect(conversation.studentId).toBe('zhou-lan')
    expect(conversation.id).not.toBe('conv-gu-yan')
    expect(conversation.relationId).toBe('rel-zhou-lan')
    expect(conversation.messages.every((message) => message.author !== 'student' || message.authorName === '周岚' || message.authorName === '林澈')).toBe(true)
    store.setMessageDraft(conversation.id, '周四的耐力安排可以晚半小时开始吗？')
    expect(store.sendMessage(conversation.id)).toBe(true)
    expect(conversation.messages.at(-1)!.author).toBe('coach')
    expect(store.conversationList.value.some((item) => item.studentId === 'zhou-lan')).toBe(true)
    store.resetPreview()
  })

  it('健康授权按关系派生，仅本人可见的观察即使授权也不进教练证据', async () => {
    const store = await loadStore()
    const lin = store.state.students.find((item) => item.id === 'lin-che')!
    expect(store.healthAuthorizedFor(lin)).toBe(true)
    expect(store.selectedStudentEvidence.value.map((item) => item.id)).not.toContain('lin-3')
    store.goToCoachStudentDetail('chen-mo')
    expect(store.healthAuthorizedFor(store.state.students.find((item) => item.id === 'chen-mo')!)).toBe(false)
    expect(store.selectedStudentEvidence.value.every((item) => item.kind === 'plan')).toBe(true)
    store.resetPreview()
  })

  it('未读按双方读取游标独立计算，失败/重试不重复计数', async () => {
    const store = await loadStore()
    store.openConversation('conv-gu-yan')
    const conversation = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    store.setMessageDraft('conv-gu-yan', '周六的训练可以晚半小时开始吗')
    expect(store.sendMessage('conv-gu-yan')).toBe(true)
    expect(store.conversationUnread(conversation, 'student')).toBe(0)
    expect(store.conversationUnread(conversation, 'coach')).toBe(1)

    store.state.actor = 'coach'
    expect(store.unreadTotal.value).toBeGreaterThanOrEqual(1)
    store.openConversation('conv-gu-yan')
    expect(store.conversationUnread(conversation, 'coach')).toBe(0)
    expect(store.conversationUnread(conversation, 'student')).toBe(0)
    store.setMessageDraft('conv-gu-yan', '可以，周六上午 10 点开始')
    expect(store.sendMessage('conv-gu-yan')).toBe(true)
    expect(store.conversationUnread(conversation, 'student')).toBe(1)
    expect(store.conversationUnread(conversation, 'coach')).toBe(0)

    store.state.actor = 'student'
    store.openConversation('conv-gu-yan')
    expect(store.conversationUnread(conversation, 'student')).toBe(0)
    store.applyPreviewState('messages', 'failed')
    store.setMessageDraft('conv-gu-yan', '那周四呢')
    expect(store.sendMessage('conv-gu-yan')).toBe(true)
    expect(store.conversationUnread(conversation, 'coach')).toBe(0)
    const failed = conversation.messages.at(-1)!
    expect(store.retryMessage(failed.id, 'conv-gu-yan')).toBe(true)
    expect(store.conversationUnread(conversation, 'coach')).toBe(1)
    expect(store.retryMessage(failed.id, 'conv-gu-yan')).toBe(true)
    expect(store.conversationUnread(conversation, 'coach')).toBe(1)
    store.resetPreview()
  })

  it('接收方看不到发送方的失败/发送中消息，也不能替对方重试或删除', async () => {
    const store = await loadStore()
    store.goToConversation('conv-gu-yan')
    const conversation = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    store.applyPreviewState('messages', 'failed')
    store.setMessageDraft('conv-gu-yan', '未发送的安排：周六晚半小时')
    expect(store.sendMessage('conv-gu-yan')).toBe(true)
    const failed = conversation.messages.at(-1)!
    expect(failed.status).toBe('failed')
    store.state.actor = 'coach'
    expect(store.lastVisibleMessage(conversation)?.id).not.toBe(failed.id)
    expect(store.visibleMessagesForActor(conversation).every((item) => item.status === 'sent')).toBe(true)
    expect(store.messageTextForActor(conversation, failed)).toBe('健康内容已不可见')
    expect(store.retryMessage(failed.id, 'conv-gu-yan')).toBe(false)
    store.discardFailedMessage(failed.id, 'conv-gu-yan')
    expect(conversation.messages.some((item) => item.id === failed.id)).toBe(true)

    store.state.actor = 'student'
    expect(store.visibleMessagesForActor(conversation).some((item) => item.id === failed.id)).toBe(true)
    expect(store.messagesReadOnly.value).toBe(false)
    store.resetPreview()
  })

  it('同会话草稿按参与者隔离，发送只清当前一方', async () => {
    const store = await loadStore()
    store.goToConversation('conv-gu-yan')
    store.setMessageDraft('conv-gu-yan', '学员自己的草稿')
    store.state.actor = 'coach'
    const conversation = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    expect(conversation.drafts.student).toBe('学员自己的草稿')
    expect(conversation.drafts.coach).toBe('')
    store.setMessageDraft('conv-gu-yan', '教练自己的草稿')
    expect(store.sendMessage('conv-gu-yan')).toBe(true)
    expect(conversation.drafts.coach).toBe('')
    expect(conversation.drafts.student).toBe('学员自己的草稿')

    store.state.actor = 'student'
    expect(conversation.drafts.student).toBe('学员自己的草稿')
    expect(store.sendMessage('conv-gu-yan')).toBe(true)
    expect(conversation.drafts.student).toBe('')
    store.resetPreview()
  })

  it('未授权学员的可见证据不包含健康观察', async () => {
    const store = await loadStore()
    store.goToCoachStudentDetail('zhou-lan')
    const evidence = store.selectedStudentEvidence.value
    expect(evidence.every((item) => item.id !== 'zhou-2')).toBe(true)
    expect(evidence.every((item) => item.id !== 'zhou-1')).toBe(true)
    expect(evidence.map((item) => item.id)).toContain('zhou-3')
    expect(store.selectedStudentRestriction.value).toContain('未授权')
    store.resetPreview()
  })

  it('服务字段保存后实际学员详情同源，既有约定快照不变，未改包不增版', async () => {
    const store = await loadStore()
    const agreed = JSON.parse(JSON.stringify(store.serviceAgreement.value!))
    const untouchedVersion = store.state.services.drafts['pkg-check']!.version
    store.updateServiceField('pkg-strength', 'priceInput', '759')
    store.updateServiceField('pkg-strength', 'priceUnit', '/ 月')
    store.updateServiceField('pkg-strength', 'cycle', '8 周')
    store.updateServiceField('pkg-strength', 'scope', '每周 2 次训练安排；每次训练后的文字反馈')
    store.updateServiceField('pkg-strength', 'goalsText', '基础力量与节奏恢复')
    store.updateServiceField('pkg-strength', 'response', '工作日 12 小时内回复')
    store.updateServiceField('pkg-strength', 'description', '按每周两次安排基础力量与节奏恢复')
    expect(store.serviceDirty.value).toBe(true)
    expect(store.saveServices()).toBe(true)
    expect(store.state.services.drafts['pkg-strength']!.version).toBe(3)
    expect(store.state.services.drafts['pkg-check']!.version).toBe(untouchedVersion)
    expect(store.serviceDirty.value).toBe(false)
    const coachPackage = store.state.coaches
      .find((coach) => coach.id === 'gu-yan')!
      .packages.find((pack) => pack.id === 'pkg-strength')!
    expect(coachPackage.priceYuan).toBe(759)
    expect(coachPackage.priceUnit).toBe('/ 月')
    expect(coachPackage.cycle).toBe('8 周')
    expect(coachPackage.includes).toEqual(['每周 2 次训练安排', '每次训练后的文字反馈'])
    expect(coachPackage.goals).toEqual(['基础力量与节奏恢复'])
    expect(coachPackage.response).toBe('工作日 12 小时内回复')
    expect(coachPackage.summary).toContain('每周两次')
    expect(store.serviceAgreement.value!.agreedPriceYuan).toBe(agreed.agreedPriceYuan)
    expect(store.serviceAgreement.value!.agreedName).toBe(agreed.agreedName)
    expect(store.serviceAgreement.value!.agreedPriceUnit).toBe(agreed.agreedPriceUnit)
    expect(store.serviceAgreement.value!.agreedCycle).toBe(agreed.agreedCycle)
    store.resetPreview()
  })

  it('无效价格保留输入并阻止保存，0 元可以使用', async () => {
    const store = await loadStore()
    const initial = store.state.services.saved['pkg-strength']!.priceYuan
    store.updateServiceField('pkg-strength', 'priceInput', '-12')
    expect(store.servicePriceIssues.value['pkg-strength']).toContain('整数')
    expect(store.saveServices()).toBe(false)
    expect(store.state.services.drafts['pkg-strength']!.priceInput).toBe('-12')
    expect(store.state.services.saved['pkg-strength']!.priceYuan).toBe(initial)
    expect(store.state.coaches.find((coach) => coach.id === 'gu-yan')!.packages.find((pack) => pack.id === 'pkg-strength')!.priceYuan).toBe(initial)

    store.updateServiceField('pkg-strength', 'priceInput', '0')
    expect(store.servicePriceIssues.value['pkg-strength']).toBeNull()
    expect(store.saveServices()).toBe(true)
    expect(store.state.services.saved['pkg-strength']!.priceYuan).toBe(0)
    store.resetPreview()
  })
})

describe('我的计划与 AI', () => {
  it('保存产生新版本，无改动不增版；审阅提交不等于公开', async () => {
    const store = await loadStore()
    const version = store.state.ownPlan.version
    store.updateOwnField('own-day-a', 'oa1', 'sets', '5')
    expect(store.saveOwnPlan()).toBe(true)
    expect(store.state.ownPlan.version).toBe(version + 1)
    expect(store.saveOwnPlan()).toBe(false)
    expect(store.openShareReview()).toBeUndefined()
    expect(store.ui.sheet).toBe('share')
    expect(store.confirmShareReview()).toBe(true)
    expect(store.state.ownPlan.status).toBe('pending_review')
    expect(store.state.ownPlan.visibility).toBe('private')
    expect(store.state.ownPlan.publishedContent).toBeNull()
    store.updateOwnField('own-day-a', 'oa1', 'sets', '3')
    expect(store.state.ownPlan.status).toBe('draft')
    expect(store.state.ownPlan.confirmedReviewHash).toBeNull()
    store.resetPreview()
  })

  it('公开预览包含组间休息和公开备注，不包含私人健康备注', async () => {
    const store = await loadStore()
    const preview = store.ownPlanPublicPreview.value
    expect(preview.days[0]!.exercises[0]!.restSeconds).toBe('90')
    expect(preview.days[0]!.exercises[0]!.note).toContain('保留 2 次余力')
    expect(JSON.stringify(preview)).not.toContain(store.state.ownPlan.healthNote)
    store.resetPreview()
  })

  it('版本冲突时保留本地修改，解决后才可保存', async () => {
    const store = await loadStore()
    store.applyPreviewState('myPlan', 'conflict')
    store.updateOwnField('own-day-a', 'oa1', 'sets', '3')
    expect(store.saveOwnPlan()).toBe(false)
    expect(store.state.ownPlan.days[0]!.exercises[0]!.sets).toBe('3')
    expect(store.resolveOwnConflict('local')).toBe(true)
    expect(store.state.ownPlan.remoteVersion).toBeNull()
    expect(store.state.ownPlan.days[0]!.exercises[0]!.sets).toBe('3')
    store.resetPreview()
  })

  it('AI 候选只应用一次，未保存前不改变现用安排', async () => {
    const store = await loadStore()
    const version = store.state.ownPlan.version
    expect(store.applyAiCandidate()).toBe(true)
    expect(store.state.ownPlan.days[0]!.exercises[0]!.target).toBe('8 次')
    expect(store.state.ownPlan.version).toBe(version)
    expect(store.applyAiCandidate()).toBe(false)
    expect(store.state.ai.appliedIds.filter((id) => id === 'ai-stable-reps')).toHaveLength(1)
    expect(store.aiStatus.value).toBe('duplicate')
    store.resetPreview()
  })

  it('教练交付的计划不能由学员 AI 自助改写', async () => {
    const store = await loadStore()
    store.goToAiAssist('ai-coach-plan', 'coach')
    const before = JSON.stringify(store.state.ownPlan.days)
    expect(store.applyAiCandidate()).toBe(false)
    expect(JSON.stringify(store.state.ownPlan.days)).toBe(before)
    expect(store.aiStatusCopy.value.title).toContain('不能自助完成')
    store.resetPreview()
  })
})

describe('对象一致性与动作口径', () => {
  it('今天现用安排入口落到教练计划，不落到自有内容编辑器', async () => {
    const store = await loadStore()
    const studentPlanTitle = store.state.student.plan.title
    const ownTitle = store.state.ownPlan.title
    store.goToStudentPlan()
    expect(store.state.screen).toBe('studentPlan')
    expect(store.state.student.plan.title).toBe(studentPlanTitle)
    expect(store.state.ownPlan.title).toBe(ownTitle)
    expect(studentPlanTitle).not.toBe(ownTitle)
    expect(store.state.student.planVersion).toBe(6)
    store.resetPreview()
  })

  it('记录动作按业务名称归一，不因计划内临时 ID 重复', async () => {
    const store = await loadStore()
    const names = store.recordExerciseOptions.value.map((option) => option.name)
    expect(new Set(names).size).toBe(names.length)
    expect(names.filter((name) => name === '平板支撑')).toHaveLength(1)
    expect(names.filter((name) => name === '死虫式')).toHaveLength(1)
    store.resetPreview()
  })
})

describe('匿名、年龄与服务准入', () => {
  it('匿名开始先登录再确认年龄，取消不建训练，继续后同一次开始只建一次', async () => {
    const store = await loadStore()
    store.applyPreviewState('planDetail', 'anonymous')
    store.goToPlanDetail('plan-dumbbell')
    const sessionId = store.state.session.id
    expect(store.requestStartPlan('plan-dumbbell')).toBe('auth')
    expect(store.ui.sheet).toBe('access')
    expect(store.ui.accessStep).toBe('signin')
    expect(store.state.session.id).toBe(sessionId)
    expect(store.ui.pendingAccess?.kind).toBe('startPlan')

    store.completeLocalSignIn()
    expect(store.ui.accessStep).toBe('age')
    expect(store.requestStartPlan('plan-dumbbell')).toBe('auth')
    store.confirmAdultAccess()
    expect(store.ui.sheet).toBe('replace')
    expect(store.state.session.id).toBe(sessionId)

    store.confirmReplaceAndStart()
    expect(store.state.session.planId).toBe('plan-dumbbell')
    expect(store.state.auth).toEqual({ signedIn: true, age: 'adult' })
    store.resetPreview()
  })

  it('未成年停止服务但不阻止浏览，未知年龄不能换内容绕过', async () => {
    const minor = await loadStore()
    minor.applyPreviewState('planDetail', 'minor')
    minor.goToPlanDetail('plan-core')
    expect(minor.requestStartPlan('plan-core')).toBe('minor')
    expect(minor.ui.accessStep).toBe('minor')
    expect(minor.state.session.planId).toBe('plan-upper-pull')
    minor.cancelAccess()
    expect(minor.state.screen).toBe('planDetail')
    expect(minor.state.session.planId).toBe('plan-upper-pull')
    minor.resetPreview()

    const unknown = await loadStore()
    unknown.applyPreviewState('planDetail', 'unknown-age')
    unknown.goToPlanDetail('plan-dumbbell')
    expect(unknown.requestStartPlan('plan-dumbbell')).toBe('auth')
    expect(unknown.ui.accessStep).toBe('age')
    unknown.goToPlanDetail('plan-core')
    expect(unknown.requestStartPlan('plan-core')).toBe('auth')
    expect(unknown.ui.accessStep).toBe('age')
    expect(unknown.state.session.id).toBe('session-20261024-strength')
    unknown.cancelAccess()
    unknown.resetPreview()
  })
})

describe('记录与饮食自评', () => {
  it('范围变化改变同一动作的起点与差值，不造数', async () => {
    const store = await loadStore()
    store.setRecordsRange('4w')
    const first4 = store.recordChart.value[0]!
    store.setRecordsRange('12w')
    const first12 = store.recordChart.value[0]!
    expect(first4.value).toBe(9.5)
    expect(first12.value).toBe(8.75)
    expect(store.recordSummary.value.delta).not.toBe(first4.value)
    store.resetPreview()
  })

  it('延迟完成保留补记说明，不伪装按时', async () => {
    const store = await loadStore()
    store.setRecordsRange('4w')
    const late = store.recordsInRange.value.find((record) => record.lateRecorded)
    expect(late).toBeTruthy()
    expect(late!.lateNote).toContain('补记')
    store.resetPreview()
  })

  it('饮食自评保存回读，且不生成摄入数字', async () => {
    const store = await loadStore()
    store.setDietStatus('partial')
    store.setDietObservation('晚餐蔬菜偏少')
    expect(store.saveDietReport()).toBe(true)
    expect(store.state.diet.report.status).toBe('partial')
    expect(store.state.diet.report.observation).toBe('晚餐蔬菜偏少')
    expect(store.state.diet.report.savedAt).not.toBeNull()
    expect(store.dietInsufficient.value).toContain('无法给出热量')
    store.resetPreview()
  })

  it('撤回健康授权后教练看不到饮食观察，自己的记录仍保留', async () => {
    const store = await loadStore()
    store.setDietStatus('on_plan')
    store.saveDietReport()
    store.revokeHealthAuthorization()
    expect(store.state.diet.report.coachVisible).toBe(false)
    expect(store.state.diet.report.status).toBe('on_plan')
    store.resetPreview()
  })
})

describe('fix02 历史引用与记录投影', () => {
  it('旧v7引用按Lin快照打开，不因最后教练上下文是周岚而回v8', async () => {
    const store = await loadStore()
    store.activateCoachStudentPlan('lin-che')
    store.updatePlanField('day-a', 'a1', 'note', '第8版新增说明：先热身，再完成当前重量。')
    expect(store.saveDraft()).toBe(true)
    store.requestPublish()
    expect(store.confirmPublish()).toBe(true)
    expect(store.state.coach.deliveries.some((item) => item.version === 8)).toBe(true)
    store.activateCoachStudentPlan('zhou-lan')
    expect(store.state.coach.deliveries.some((item) => item.version === 8)).toBe(false)

    store.state.actor = 'student'
    store.openMessageRef({ kind: 'plan', id: 'delivery-7', label: 'v7', meta: '' }, 'conv-gu-yan')
    expect(store.state.screen).toBe('incomingPlan')
    expect(store.ui.incomingHistoryVersion).toBe(7)
    expect(store.incomingDelivery.value?.version).toBe(7)
    expect(store.incomingDelivery.value?.content.days[0]?.exercises[0]?.note).toContain('保留 2 次余力')
    expect(store.incomingAvailable.value).toBe(false)
    expect(store.incomingStatusLabel.value).toContain('替代')
    store.resetPreview()
  })

  it('教练从周岚会话打开周岚交付只读，不混入林澈现用版', async () => {
    const store = await loadStore()
    store.activateCoachStudentPlan('zhou-lan')
    store.updatePlanField('zhou-day-a', 'z-a1', 'target', '5 分钟')
    expect(store.saveDraft()).toBe(true)
    store.requestPublish()
    expect(store.confirmPublish()).toBe(true)
    store.state.actor = 'coach'
    store.openMessageRef({ kind: 'plan', id: 'delivery-5', label: 'v5', meta: '' }, 'conv-zhou-lan')
    expect(store.ui.incomingReadOnly).toBe(true)
    expect(store.ui.incomingHistoryStudentId).toBe('zhou-lan')
    expect(store.incomingDelivery.value?.version).toBe(5)
    expect(store.incomingDelivery.value?.content.days[0]?.title).toContain('基础耐力')
    expect(store.incomingAvailable.value).toBe(false)
    store.resetPreview()
  })

  it('缺失计划引用不 fallback 最新，训练引用定位原记录', async () => {
    const store = await loadStore()
    store.state.actor = 'student'
    store.openMessageRef({ kind: 'plan', id: 'delivery-99', label: 'v99', meta: '' }, 'conv-gu-yan')
    expect(store.ui.incomingRefMissing).toBe(true)
    expect(store.incomingDelivery.value).toBeNull()
    expect(store.incomingStatusLabel.value).toBe('引用不可用')
    expect(store.incomingAvailable.value).toBe(false)

    store.openMessageRef({ kind: 'training', id: 'record-20261022', label: '训练', meta: '' }, 'conv-gu-yan')
    expect(store.state.screen).toBe('records')
    expect(store.ui.recordsFocusExplicit).toBe(true)
    expect(store.ui.recordsFocusId).toBe('record-20261022')
    expect(store.ui.recordsExerciseId).toBe('哑铃卧推')

    store.openMessageRef({ kind: 'training', id: 'record-missing', label: '缺失', meta: '' }, 'conv-gu-yan')
    expect(store.ui.recordsFocusId).toBeNull()
    expect(store.ui.recordsFocusExplicit).toBe(true)
    store.resetPreview()
  })

  it('记录空/单条时图表摘要点评同一投影', async () => {
    const store = await loadStore()
    store.applyPreviewState('records', 'empty')
    expect(store.recordViewRows.value).toHaveLength(0)
    expect(store.recordChart.value).toHaveLength(0)
    expect(store.recordSummary.value.count).toBe(0)
    expect(store.recordsFeedback.value).toBeNull()

    store.applyPreviewState('records', 'single')
    expect(store.recordViewRows.value).toHaveLength(1)
    expect(store.recordChart.value).toHaveLength(1)
    expect(store.recordSummary.value.count).toBe(1)
    expect(store.recordSummary.value.delta).toBeNull()
    store.resetPreview()
  })

  it('记录信息不足时图表摘要明细与原因同源', async () => {
    const store = await loadStore()
    store.applyPreviewState('records', 'insufficient')
    expect(store.recordViewRows.value).toHaveLength(0)
    expect(store.recordChart.value).toHaveLength(0)
    expect(store.recordSummary.value.count).toBe(0)
    expect(store.recordsFeedback.value).toBeNull()
    expect(store.state.preview.records).toBe('insufficient')
    store.resetPreview()
  })

  it('教练空结果与结果计数一致', async () => {
    const store = await loadStore()
    store.state.explore.mode = 'coach'
    store.state.explore.coachFilters.goal = 'strength'
    store.applyPreviewState('explore', 'empty')
    expect(store.filteredCoaches.value).toHaveLength(0)
    expect(store.filteredExplore.value).toHaveLength(0)
    expect(store.state.explore.coachFilters.goal).toBe('strength')
    store.resetPreview()
  })

  it('B训练日沿用所选动作器械与内容时长，不沿用旧哑铃平凳', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-weekly-base')
    store.ui.planDayId = 'plan-weekly-base-day-b'
    expect(store.requestStartPlan('plan-weekly-base')).toBe('replace')
    store.confirmReplaceAndStart()
    expect(store.state.session.dayTitle).toContain('拉与下肢')
    expect(store.state.session.durationMin).toBe(40)
    expect(store.state.session.exercises.length).toBe(4)
    expect(store.state.session.exercises[0]!.name).toBe('高位下拉')
    expect(store.state.session.exercises[0]!.equipment).toContain('高位下拉器')
    expect(store.state.session.exercises[1]!.equipment).toContain('哑铃')
    expect(store.todayFacts.value.exerciseCount).toBe(4)
    store.resetPreview()
  })

  it('周岚发布后完成草稿待办，再次编辑重新打开', async () => {
    const store = await loadStore()
    store.activateCoachStudentPlan('zhou-lan')
    store.updatePlanField('zhou-day-a', 'z-a1', 'target', '5 分钟')
    expect(store.saveDraft()).toBe(true)
    const zhou = () => store.state.students.find((item) => item.id === 'zhou-lan')!
    const draftTodo = () => store.state.todoItems.find((item) => item.id === 'todo-draft')!
    expect(zhou().planDraftStatus).toBe('计划草稿待发布')
    expect(draftTodo().done).toBe(false)
    store.requestPublish()
    expect(store.confirmPublish()).toBe(true)
    expect(zhou().planDraftStatus).toContain('已交付版本 5')
    expect(draftTodo().done).toBe(true)
    store.removeExercise('zhou-day-a', 'z-a2')
    expect(zhou().planDraftStatus).toBe('计划草稿有未保存改动')
    expect(draftTodo().done).toBe(false)
    store.undoRemoveExercise()
    expect(zhou().planDraftStatus).toContain('已交付版本 5')
    expect(draftTodo().done).toBe(true)
    store.updatePlanField('zhou-day-a', 'z-a1', 'target', '6 分钟')
    expect(zhou().planDraftStatus).toBe('计划草稿有未保存改动')
    expect(draftTodo().done).toBe(false)

    store.addExercise('zhou-day-a')
    expect(zhou().planDraftStatus).toBe('计划草稿有未保存改动')
    expect(draftTodo().done).toBe(false)
    store.removeExercise('zhou-day-a', 'z-a2')
    expect(zhou().planDraftStatus).toBe('计划草稿有未保存改动')
    store.undoRemoveExercise()
    expect(zhou().planDraftStatus).toBe('计划草稿有未保存改动')
    store.resetPreview()
  })
})

describe('fix03 引用完整生命周期', () => {
  it('学员林澈旧v7刷新后仍为v7，返回仍为学员消息', async () => {
    const store = await loadStore()
    store.activateCoachStudentPlan('lin-che')
    store.updatePlanField('day-a', 'a1', 'note', '第8版新增说明：先热身，再完成当前重量。')
    store.saveDraft()
    store.requestPublish()
    store.confirmPublish()
    store.activateCoachStudentPlan('zhou-lan')
    store.state.actor = 'student'
    store.goToConversation('conv-gu-yan')
    store.openMessageRef({ kind: 'plan', id: 'delivery-7', label: 'v7', meta: '' }, 'conv-gu-yan')
    expect(store.incomingDelivery.value?.version).toBe(7)
    expect(store.ui.incomingReadOnly).toBe(false)

    const reloaded = await savedStore()
    expect(reloaded.state.screen).toBe('incomingPlan')
    expect(reloaded.state.actor).toBe('student')
    expect(reloaded.ui.incomingHistoryVersion).toBe(7)
    expect(reloaded.incomingDelivery.value?.version).toBe(7)
    expect(reloaded.incomingDelivery.value?.content.days[0]?.exercises[0]?.note).toContain('保留 2 次余力')
    expect(reloaded.incomingAvailable.value).toBe(false)
    reloaded.goBack()
    expect(reloaded.state.screen).toBe('messages')
    expect(reloaded.state.actor).toBe('student')
    reloaded.resetPreview()
  })

  it('教练周岚旧交付刷新后仍为周岚只读，返回仍为教练消息', async () => {
    const store = await loadStore()
    store.activateCoachStudentPlan('zhou-lan')
    store.updatePlanField('zhou-day-a', 'z-a1', 'target', '4 分钟')
    store.saveDraft()
    store.requestPublish()
    store.confirmPublish()
    store.updatePlanField('zhou-day-a', 'z-a1', 'target', '5 分钟')
    store.saveDraft()
    store.requestPublish()
    store.confirmPublish()
    store.state.actor = 'coach'
    store.goToConversation('conv-zhou-lan')
    expect(store.state.coach.deliveries.map((item) => item.version)).toContain(5)
    store.openMessageRef({ kind: 'plan', id: 'delivery-5', label: 'v5', meta: '' }, 'conv-zhou-lan')
    expect(store.ui.incomingReadOnly).toBe(true)
    expect(store.incomingDelivery.value?.version).toBe(5)
    expect(store.confirmAdoption()).toBe(false)

    const reloaded = await savedStore()
    expect(reloaded.state.screen).toBe('incomingPlan')
    expect(reloaded.state.actor).toBe('coach')
    expect(reloaded.ui.incomingHistoryStudentId).toBe('zhou-lan')
    expect(reloaded.ui.incomingReadOnly).toBe(true)
    expect(reloaded.incomingDelivery.value?.version).toBe(5)
    expect(reloaded.incomingDelivery.value?.content.days[0]?.title).toContain('基础耐力')
    expect(reloaded.incomingAvailable.value).toBe(false)
    expect(reloaded.confirmAdoption()).toBe(false)
    reloaded.goBack()
    expect(reloaded.state.screen).toBe('messages')
    expect(reloaded.state.actor).toBe('coach')
    expect(reloaded.currentConversation.value?.studentId).toBe('zhou-lan')
    reloaded.resetPreview()
  })

  it('缺失引用刷新仍不可用，训练原记录定位刷新保持', async () => {
    const store = await loadStore()
    store.state.actor = 'student'
    store.goToConversation('conv-gu-yan')
    store.openMessageRef({ kind: 'plan', id: 'delivery-99', label: 'v99', meta: '' }, 'conv-gu-yan')
    const missing = await savedStore()
    expect(missing.state.screen).toBe('incomingPlan')
    expect(missing.ui.incomingRefMissing).toBe(true)
    expect(missing.incomingDelivery.value).toBeNull()
    expect(missing.incomingStatusLabel.value).toBe('引用不可用')
    expect(missing.incomingAvailable.value).toBe(false)

    missing.goBack()
    expect(missing.state.screen).toBe('messages')
    missing.openMessageRef({ kind: 'training', id: 'record-20261022', label: '训练', meta: '' }, 'conv-gu-yan')
    const training = await savedStore()
    expect(training.state.screen).toBe('records')
    expect(training.ui.recordsFocusExplicit).toBe(true)
    expect(training.ui.recordsFocusId).toBe('record-20261022')
    training.resetPreview()
  })

  it('教练消息预览入口保持教练角色', async () => {
    const store = await loadStore()
    store.jumpToScenario('coach-messages')
    expect(store.state.actor).toBe('coach')
    expect(store.state.screen).toBe('messages')
    expect(store.conversationList.value.every((item) => item.coachId === 'gu-yan')).toBe(true)
    store.resetPreview()
  })
})

describe('fix03 动效合同', () => {
  it('休息暂停后继续首帧不先增大再跳回，随后按真实时间递减', async () => {
    const store = await loadStore()
    const entry = store.currentEntry.value!
    store.setInputValue(entry.set.id, 'weightKg', '12.5')
    store.setInputValue(entry.set.id, 'reps', '10')
    expect(store.completeCurrentSet()).toBe(true)
    expect(store.state.session.rest).not.toBeNull()
    store.pauseRest()
    const paused = store.restRemainingMs.value
    expect(paused).toBeGreaterThan(0)
    await vi.advanceTimersByTimeAsync(5500)
    store.resumeRest()
    const firstFrame = store.restRemainingMs.value
    expect(firstFrame).toBeLessThanOrEqual(paused + 1000)
    expect(firstFrame).toBeGreaterThan(0)
    await vi.advanceTimersByTimeAsync(1300)
    expect(store.restRemainingMs.value).toBeLessThan(firstFrame)
    store.resetPreview()
  })

  it('完成组后即时状态保留确认标记，进入休息分支仍可见', async () => {
    const store = await loadStore()
    const entry = store.currentEntry.value!
    store.setInputValue(entry.set.id, 'weightKg', '42.5')
    store.setInputValue(entry.set.id, 'reps', '11')
    expect(store.completeCurrentSet()).toBe(true)
    expect(store.ui.justCompletedSetId).toBe(entry.set.id)
    expect(store.state.session.rest).not.toBeNull()
    await vi.advanceTimersByTimeAsync(100)
    expect(store.ui.justCompletedSetId).toBe(entry.set.id)
    store.resetPreview()
  })
})

describe('fix05 M12 同步重试状态', () => {
  it('保存成功才清除预览失败/待保存状态，真实失败保留', async () => {
    const store = await loadStore()
    const before = store.state.session.exercises.flatMap((exercise) => exercise.sets).filter((set) => set.status === 'done').length
    store.state.preview.training = 'sync-failed'
    expect(store.retryPersist()).toBe(true)
    expect(store.state.preview.training).not.toBe('sync-failed')
    expect(store.storageState.ok).toBe(true)

    store.state.preview.training = 'sync-failed'
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota')
    })
    expect(store.retryPersist()).toBe(false)
    expect(store.state.preview.training).toBe('sync-failed')
    expect(store.storageState.ok).toBe(false)
    const after = store.state.session.exercises.flatMap((exercise) => exercise.sets).filter((set) => set.status === 'done').length
    expect(after).toBe(before)
    spy.mockRestore()
    expect(store.retryPersist()).toBe(true)
    expect(store.state.preview.training).not.toBe('sync-failed')
    expect(store.storageState.ok).toBe(true)
    store.resetPreview()
  })
})

describe('fix04 引用返回与旧缓存迁移', () => {
  it('非默认训练引用冷恢复后 goBack 回到原 messages', async () => {
    const store = await loadStore()
    const record = store.state.records.find((item) => item.id === 'record-20260929')!
    store.goToConversation('conv-gu-yan')
    store.openMessageRef({ kind: 'training', id: record.id, label: '9月29日训练', meta: '原记录' })
    expect(store.ui.recordsFocusExplicit).toBe(true)
    expect(store.ui.recordsFocusId).toBe(record.id)
    const reloaded = await savedStore()
    expect(reloaded.state.screen).toBe('records')
    expect(reloaded.ui.recordsFocusId).toBe(record.id)
    reloaded.goBack()
    expect(reloaded.state.screen).toBe('messages')
    expect(reloaded.state.actor).toBe('student')
    expect(reloaded.currentConversation.value?.id).toBe('conv-gu-yan')
    reloaded.resetPreview()
  })

  it('旧 coach messages 缓存缺 selection.actor 时保留 parsed.actor', async () => {
    vi.useRealTimers()
    vi.resetModules(); window.localStorage.clear()
    const first = await import('../state/store')
    first.state.actor = 'coach'
    first.goToConversation('conv-zhou-lan')
    await new Promise((resolve) => setTimeout(resolve, 350))
    const raw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    delete raw.selection.actor
    raw.screen = 'messages'
    raw.actor = 'coach'
    window.localStorage.setItem(first.storageKey, JSON.stringify(raw))
    vi.resetModules()
    const reloaded = await import('../state/store')
    expect(reloaded.state.actor).toBe('coach')
    expect(reloaded.ui.activeConversationId).toBe('conv-zhou-lan')
  })

  it('旧 incomingPlan 缺引用字段时保留读者并明确不可用，不落最新版', async () => {
    vi.useRealTimers()
    vi.resetModules(); window.localStorage.clear()
    const first = await import('../state/store')
    first.state.actor = 'coach'
    first.goToConversation('conv-zhou-lan')
    await new Promise((resolve) => setTimeout(resolve, 350))
    const raw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    raw.screen = 'incomingPlan'
    raw.stack = ['messages']
    raw.actor = 'coach'
    for (const key of ['incomingHistoryVersion', 'incomingHistoryStudentId', 'incomingReadOnly', 'incomingRefMissing']) {
      delete raw.selection[key]
    }
    window.localStorage.setItem(first.storageKey, JSON.stringify(raw))
    vi.resetModules()
    const reloaded = await import('../state/store')
    expect(reloaded.state.actor).toBe('coach')
    expect(reloaded.ui.incomingRefMissing).toBe(true)
    expect(reloaded.incomingDelivery.value).toBeNull()
    expect(reloaded.incomingAvailable.value).toBe(false)
    expect(reloaded.ui.incomingHistoryStudentId).toBe('zhou-lan')
    reloaded.goBack()
    expect(reloaded.state.screen).toBe('messages')
    expect(reloaded.state.actor).toBe('coach')
  })

  it('旧 records 引用缺 focus 字段时不落默认 10/22，正常入口仍可默认', async () => {
    vi.useRealTimers()
    vi.resetModules(); window.localStorage.clear()
    const first = await import('../state/store')
    first.goToConversation('conv-gu-yan')
    await new Promise((resolve) => setTimeout(resolve, 350))
    const raw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    raw.screen = 'records'
    raw.stack = ['messages']
    delete raw.selection.recordsFocusId
    delete raw.selection.recordsFocusExplicit
    window.localStorage.setItem(first.storageKey, JSON.stringify(raw))
    vi.resetModules()
    const unknown = await import('../state/store')
    expect(unknown.ui.recordsFocusExplicit).toBe(true)
    expect(unknown.ui.recordsFocusId).toBeNull()

    const directRaw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    directRaw.screen = 'records'
    directRaw.stack = []
    delete directRaw.selection.recordsFocusId
    delete directRaw.selection.recordsFocusExplicit
    window.localStorage.setItem(first.storageKey, JSON.stringify(directRaw))
    vi.resetModules()
    const direct = await import('../state/store')
    expect(direct.ui.recordsFocusExplicit).toBe(false)
    expect(direct.ui.recordsFocusId).toBeNull()
  })
})

describe('fix06 最新引用采用', () => {
  it('学员点击本人最新消息引用可明确确认采用，取消保留6，确认后才更新', async () => {
    const store = await loadStore()
    const beforeSession = JSON.parse(JSON.stringify(store.state.session))
    store.jumpToScenario('today')
    store.goToConversation('conv-gu-yan')
    const ref = store.state.conversations
      .find((item) => item.id === 'conv-gu-yan')!
      .messages.flatMap((item) => item.refs ?? [])
      .find((item) => item.kind === 'plan' && item.id === 'delivery-7')!
    expect(ref).toBeTruthy()
    store.openMessageRef(ref)
    expect(store.incomingDelivery.value?.version).toBe(7)
    expect(store.incomingAvailable.value).toBe(true)
    store.openAdoptSheet()
    expect(store.ui.sheet).toBe('adopt')
    store.closeSheet()
    expect(store.state.student.planVersion).toBe(6)
    store.openAdoptSheet()
    expect(store.confirmAdoption()).toBe(true)
    expect(store.state.student.planVersion).toBe(7)
    expect(store.state.student.incoming?.status).toBe('adopted')
    expect(JSON.parse(JSON.stringify(store.state.session))).toEqual(beforeSession)
    store.resetPreview()
  })

  it('已采用的最新引用保持 adopted，不重复更新未来安排', async () => {
    const store = await loadStore()
    store.jumpToScenario('today')
    store.goToIncomingPlan()
    store.openAdoptSheet()
    expect(store.confirmAdoption()).toBe(true)
    const schedule = JSON.parse(JSON.stringify(store.state.student.schedule))
    store.goToConversation('conv-gu-yan')
    const ref = store.state.conversations
      .find((item) => item.id === 'conv-gu-yan')!
      .messages.flatMap((item) => item.refs ?? [])
      .find((item) => item.kind === 'plan' && item.id === 'delivery-7')!
    store.openMessageRef(ref)
    expect(store.incomingDelivery.value?.status).toBe('adopted')
    expect(store.incomingAvailable.value).toBe(false)
    expect(store.confirmAdoption()).toBe(false)
    expect(JSON.parse(JSON.stringify(store.state.student.schedule))).toEqual(schedule)
    store.resetPreview()
  })
})

describe('fix06 单位与换场恢复', () => {
  it('自重鸟狗式按次/侧完成，不要求kg；侧桥按秒/侧，不转成次数', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-core')
    expect(store.requestStartPlan('plan-core')).toBe('replace')
    store.confirmReplaceAndStart()
    const kinds = store.state.session.exercises.map((item) => ({ name: item.name, kind: item.kind, target: item.targetText }))
    expect(kinds).toEqual([
      { name: '死虫式', kind: 'duration', target: '40 秒' },
      { name: '鸟狗式', kind: 'bodyweightPerSide', target: '10 次/侧' },
      { name: '侧桥', kind: 'durationPerSide', target: '30 秒/侧' },
      { name: '平板支撑', kind: 'duration', target: '45 秒' },
    ])
    // 死虫式 2 组 40 秒
    for (let i = 0; i < 2; i += 1) {
      const entry = store.currentEntry.value!
      expect(entry.exercise.kind).toBe('duration')
      store.setInputValue(entry.set.id, 'seconds', '40')
      expect(store.completeCurrentSet()).toBe(true)
      store.skipRest()
    }
    // 鸟狗式 2 组 10 次/侧，无 kg 输入
    for (let i = 0; i < 2; i += 1) {
      const entry = store.currentEntry.value!
      expect(entry.exercise.kind).toBe('bodyweightPerSide')
      expect(store.currentInput.value?.weightKg).toBe('')
      store.setInputValue(entry.set.id, 'reps', '10')
      expect(store.completeCurrentSet()).toBe(true)
      expect(entry.set.weightKg).toBeNull()
      expect(entry.set.reps).toBe(10)
      store.skipRest()
    }
    const bird = store.state.session.exercises.find((item) => item.name === '鸟狗式')!
    expect(bird.lastRef.dateLabel).toBe('暂无可比较参考')
    expect(bird.lastRef.weightKg).toBeUndefined()
    // 侧桥 2 组 30 秒/侧
    for (let i = 0; i < 2; i += 1) {
      const entry = store.currentEntry.value!
      expect(entry.exercise.kind).toBe('durationPerSide')
      store.setInputValue(entry.set.id, 'seconds', '30')
      expect(store.completeCurrentSet()).toBe(true)
      expect(entry.set.seconds).toBe(30)
      expect(entry.set.reps).toBeNull()
      store.skipRest()
    }
    store.resetPreview()
  })

  it('猫牛式8次呼吸按计数保留单位；腿后侧30秒/侧按秒', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-mobility')
    expect(store.requestStartPlan('plan-mobility')).toBe('replace')
    store.confirmReplaceAndStart()
    const cat = store.state.session.exercises.find((item) => item.name === '猫牛式')!
    expect(cat.kind).toBe('breath')
    expect(cat.targetText).toBe('8 次呼吸')
    const stretch = store.state.session.exercises.find((item) => item.name === '腿后侧伸展')!
    expect(stretch.kind).toBe('durationPerSide')
    expect(stretch.targetText).toBe('30 秒/侧')
    const first = store.currentEntry.value!
    store.setInputValue(first.set.id, 'reps', '8')
    expect(store.completeCurrentSet()).toBe(true)
    expect(first.set.weightKg).toBeNull()
    expect(first.set.reps).toBe(8)
    store.skipRest()
    store.resetPreview()
  })

  it('新秒数会话不继承旧重量错误，开始新安排时清空', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-dumbbell')
    store.requestStartPlan('plan-dumbbell')
    store.confirmReplaceAndStart()
    const entry = store.currentEntry.value!
    store.setInputValue(entry.set.id, 'weightKg', '0')
    store.setInputValue(entry.set.id, 'reps', '10')
    expect(store.completeCurrentSet()).toBe(false)
    expect(store.ui.trainingErrors.weightKg).toBeTruthy()
    store.leaveTraining()
    store.goToPlanDetail('plan-core')
    store.requestStartPlan('plan-core')
    store.confirmReplaceAndStart()
    expect(store.ui.trainingErrors).toEqual({})
    store.resetPreview()
  })
})

describe('fix06 单位文字出口', () => {
  it('上次参考保留呼吸次数和秒/侧', () => {
    expect(lastReferenceLabel({ dateLabel: '10月21日', reps: 8 }, 'breath')).toBe('8 次呼吸')
    expect(lastReferenceLabel({ dateLabel: '10月21日', seconds: 30, perSide: true }, 'durationPerSide')).toBe('30 秒/侧')
    expect(lastReferenceLabel({ dateLabel: '10月21日', reps: 10, perSide: true }, 'bodyweightPerSide')).toBe('10 次/侧')
    expect(lastReferenceLabel({ dateLabel: '10月21日', weightKg: 12.5, reps: 8 }, 'weight')).toBe('12.5 kg × 8 次')
  })

  it('冲突预览使用当前呼吸/秒每侧单位，不伪造重量', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-core')
    store.requestStartPlan('plan-core')
    store.confirmReplaceAndStart()
    for (let i = 0; i < 4; i += 1) {
      const entry = store.currentEntry.value!
      if (entry.exercise.kind === 'duration') {
        store.setInputValue(entry.set.id, 'seconds', '40')
      } else {
        store.setInputValue(entry.set.id, 'reps', '10')
      }
      expect(store.completeCurrentSet()).toBe(true)
      store.skipRest()
    }
    const side = store.currentEntry.value!
    expect(side.exercise.name).toBe('侧桥')
    store.setInputValue(side.set.id, 'seconds', '30')
    store.applyPreviewState('training', 'conflict')
    expect(store.state.session.conflicts[0]?.localText).toBe('30 秒/侧')
    expect(store.state.session.conflicts[0]?.remoteText).toBe('42 秒/侧')

    store.goToPlanDetail('plan-mobility')
    store.requestStartPlan('plan-mobility')
    store.confirmReplaceAndStart()
    const cat = store.currentEntry.value!
    expect(cat.exercise.name).toBe('猫牛式')
    store.setInputValue(cat.set.id, 'reps', '8')
    store.applyPreviewState('training', 'conflict')
    expect(store.state.session.conflicts[0]?.localText).toBe('8 次呼吸')
    expect(store.state.session.conflicts[0]?.remoteText).toBe('8 次呼吸')
    store.resetPreview()
  })
})

describe('fix07 可比较记录序列', () => {
  it('鸟狗式旧perSide 0kg与新bodyweight不混图，单点不造增量', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-core')
    store.requestStartPlan('plan-core')
    store.confirmReplaceAndStart()
    const birdAtStart = store.state.session.exercises.find((item) => item.name === '鸟狗式')!
    expect(birdAtStart.kind).toBe('bodyweightPerSide')
    expect(birdAtStart.lastRef.dateLabel).toBe('暂无可比较参考')
    while (!store.isComplete.value) {
      const entry = store.currentEntry.value!
      if (entry.exercise.kind === 'duration' || entry.exercise.kind === 'durationPerSide') {
        store.setInputValue(entry.set.id, 'seconds', entry.exercise.name === '平板支撑' ? '45' : entry.exercise.name === '侧桥' ? '30' : '40')
      } else {
        store.setInputValue(entry.set.id, 'reps', '10')
      }
      expect(store.completeCurrentSet()).toBe(true)
      store.skipRest()
    }
    store.leaveTraining()
    store.setRecordsExercise('鸟狗式')
    const chart = store.recordChart.value
    expect(chart).toHaveLength(1)
    expect(chart[0]?.unit).toBe('次/侧')
    expect(chart[0]?.value).toBe(10)
    expect(store.recordSummary.value.delta).toBeNull()
    expect(store.recordViewRows.value.some((record) => record.sets.some((set) => set.exerciseName === '鸟狗式' && set.kind === 'perSide'))).toBe(true)
    store.goToPlanDetail('plan-core')
    store.requestStartPlan('plan-core')
    store.confirmReplaceAndStart()
    const birdAgain = store.state.session.exercises.find((item) => item.name === '鸟狗式')!
    expect(birdAgain.lastRef.dateLabel).not.toBe('暂无可比较参考')
    expect(birdAgain.lastRef.reps).toBe(10)
    expect(birdAgain.lastRef.perSide).toBe(true)
    store.resetPreview()
  })

  it('新自重参考只取相同测量语义，不把旧0kg当同单位', async () => {
    const store = await loadStore()
    store.goToPlanDetail('plan-core')
    store.requestStartPlan('plan-core')
    store.confirmReplaceAndStart()
    const bird = store.state.session.exercises.find((item) => item.name === '鸟狗式')!
    expect(bird.lastRef.weightKg).toBeUndefined()
    expect(bird.lastRef.dateLabel).toBe('暂无可比较参考')
    store.resetPreview()
  })
})

describe('fix08 消息预览占位清理', () => {
  it('失败/发送中回正常只清占位，保留真实会话、消息和双方草稿', async () => {
    const store = await loadStore()
    store.goToConversation('conv-gu-yan')
    const gu = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    const jiang = store.state.conversations.find((item) => item.id === 'conv-jiang-yao')!
    gu.drafts.student = '学员真实私稿'
    gu.drafts.coach = '教练真实私稿'
    const messagesBefore = JSON.parse(JSON.stringify(gu.messages))
    const jiangMessages = JSON.parse(JSON.stringify(jiang.messages))
    store.applyPreviewState('messages', 'failed')
    expect(gu.messages.some((message) => message.id === 'preview-pending')).toBe(true)
    store.applyPreviewState('messages', 'normal')
    const after = store.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    expect(after).toBeTruthy()
    expect(after.messages).toEqual(messagesBefore)
    expect(after.drafts).toEqual({ student: '学员真实私稿', coach: '教练真实私稿' })
    expect(store.state.conversations.find((item) => item.id === 'conv-jiang-yao')!.messages).toEqual(jiangMessages)
    expect(store.state.conversations.some((item) => item.id === 'conv-gu-yan')).toBe(true)
    store.resetPreview()
  })

  it('无占位时正常恢复不清真实会话或草稿', async () => {
    const store = await loadStore()
    const before = JSON.parse(JSON.stringify(store.state.conversations))
    store.applyPreviewState('messages', 'normal')
    expect(store.state.conversations).toHaveLength(before.length)
    expect(store.state.conversations.map((item) => item.id)).toEqual(before.map((item: any) => item.id))
    expect(JSON.parse(JSON.stringify(store.state.conversations))).toEqual(before)
    store.resetPreview()
  })
})

describe('fix09 折叠记录摘要', () => {
  it('20kg×5与12.5kg×10不拼接，只描述真实一组', () => {
    const sets = [
      { kind: 'weight' as const, weightKg: 12.5, reps: 10, seconds: null },
      { kind: 'weight' as const, weightKg: 12.5, reps: 9, seconds: null },
      { kind: 'weight' as const, weightKg: 20, reps: 5, seconds: null },
    ]
    expect(summarizeRecordSets(sets)).toBe('20 kg × 5 次 · 3 组')
    expect(summarizeRecordSets(sets)).not.toContain('20 kg × 10')
  })

  it('每侧次数/秒、呼吸、自重和旧0kg保留单位', () => {
    expect(summarizeRecordSets([{ kind: 'perSide', weightKg: 20, reps: 5, seconds: null }])).toBe('20 kg × 5 次/侧 · 1 组')
    expect(summarizeRecordSets([{ kind: 'durationPerSide', weightKg: null, reps: null, seconds: 30 }])).toBe('30 秒/侧 · 1 组')
    expect(summarizeRecordSets([{ kind: 'bodyweightPerSide', weightKg: null, reps: 10, seconds: null }, { kind: 'bodyweightPerSide', weightKg: null, reps: 10, seconds: null }])).toBe('自重 × 10 次/侧 · 2 组')
    expect(summarizeRecordSets([{ kind: 'breath', weightKg: null, reps: 8, seconds: null }])).toBe('8 次呼吸 · 1 组')
    expect(summarizeRecordSets([{ kind: 'perSide', weightKg: 0, reps: 10, seconds: null }])).toBe('0 kg × 10 次/侧 · 1 组')
  })

  it('不同记录方式不合并，引导看明细', () => {
    const mixed = [
      { kind: 'perSide' as const, weightKg: 0, reps: 10, seconds: null },
      { kind: 'bodyweightPerSide' as const, weightKg: null, reps: 10, seconds: null },
    ]
    expect(summarizeRecordSets(mixed)).toBe('多种记录方式 · 查看明细')
  })
})

describe('旧本地状态的兼容加载', () => {
  it('旧状态缺周岚关系与会话时按 active 学员恢复，不借用别人的包或价格', async () => {
    vi.useRealTimers()
    vi.resetModules()
    window.localStorage.clear()
    const first = await import('../state/store')
    first.resetPreview()
    await new Promise((resolve) => setTimeout(resolve, 300))
    const raw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    raw.relations = raw.relations.filter((item: any) => item.id !== 'rel-zhou-lan')
    raw.conversations = raw.conversations.filter((item: any) => item.id !== 'conv-zhou-lan')
    raw.actor = 'coach'
    raw.screen = 'coachStudentDetail'
    raw.selection.selectedStudentId = 'zhou-lan'
    window.localStorage.setItem(first.storageKey, JSON.stringify(raw))

    vi.resetModules()
    const reloaded = await import('../state/store')
    const relation = reloaded.state.relations.find((item) => item.id === 'rel-zhou-lan')!
    expect(relation.status).toBe('active')
    expect(relation.agreedNeedsReview).toBe(true)
    expect(relation.agreedPriceKnown).toBe(false)
    expect(relation.agreedPriceYuan).toBe(0)
    expect(relation.packageId).toBe('')
    const conversation = reloaded.state.conversations.find(
      (item) => item.studentId === 'zhou-lan',
    )!
    expect(conversation.relationId).toBe('rel-zhou-lan')
    expect(conversation.drafts).toEqual({ student: '', coach: '' })
    reloaded.goToCoachStudentDetail('zhou-lan')
    reloaded.replyToStudent('zhou-lan')
    expect(reloaded.state.screen).toBe('messages')
    expect(reloaded.currentConversation.value?.studentId).toBe('zhou-lan')
    expect(reloaded.state.students.find((item) => item.id === 'zhou-lan')!.planVersion).toBe(3)
    reloaded.resetPreview()
  })

  it('旧未归属草稿迁移给单一参与者，不复制给双方', async () => {
    vi.useRealTimers()
    vi.resetModules()
    window.localStorage.clear()
    const first = await import('../state/store')
    first.resetPreview()
    await new Promise((resolve) => setTimeout(resolve, 300))
    const raw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    const conversation = raw.conversations.find((item: any) => item.id === 'conv-gu-yan')
    delete conversation.drafts
    conversation.draft = '切换到教练前的旧草稿'
    raw.actor = 'coach'
    window.localStorage.setItem(first.storageKey, JSON.stringify(raw))

    vi.resetModules()
    const reloaded = await import('../state/store')
    const migrated = reloaded.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    expect(migrated.drafts.coach).toBe('')
    expect(migrated.drafts.student).toBe('')
    expect(migrated.unassignedLegacyDraft).toBe('切换到教练前的旧草稿')
    reloaded.resetPreview()
  })

  it('已生成过 draftOwnerInferred 的旧草稿也退回未归属，不继续显示', async () => {
    vi.useRealTimers()
    vi.resetModules()
    window.localStorage.clear()
    const first = await import('../state/store')
    first.resetPreview()
    await new Promise((resolve) => setTimeout(resolve, 300))
    const raw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    const conversation = raw.conversations.find((item: any) => item.id === 'conv-gu-yan')
    conversation.drafts = { student: '', coach: '旧推断归属的草稿' }
    conversation.draftOwnerInferred = 'coach'
    window.localStorage.setItem(first.storageKey, JSON.stringify(raw))

    vi.resetModules()
    const reloaded = await import('../state/store')
    const migrated = reloaded.state.conversations.find((item) => item.id === 'conv-gu-yan')!
    expect(migrated.drafts.coach).toBe('')
    expect(migrated.drafts.student).toBe('')
    expect(migrated.unassignedLegacyDraft).toBe('旧推断归属的草稿')
    reloaded.resetPreview()
  })

  it('缺少 priceInput/priceUnit 的旧服务草稿冷启动仍保留 759/8周，并可渲染入口', async () => {
    vi.useRealTimers()
    vi.resetModules()
    window.localStorage.clear()
    const first = await import('../state/store')
    first.updateServiceField('pkg-strength', 'priceInput', '759')
    first.updateServiceField('pkg-strength', 'cycle', '8 周')
    first.updateServiceField('pkg-strength', 'scope', '每周 2 次训练安排；每次训练后的文字反馈')
    first.updateServiceField('pkg-strength', 'goalsText', '基础力量与节奏恢复')
    first.updateServiceField('pkg-strength', 'response', '工作日 12 小时内回复')
    expect(first.saveServices()).toBe(true)
    await new Promise((resolve) => setTimeout(resolve, 300))
    const raw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    delete raw.services.drafts['pkg-strength'].priceInput
    delete raw.services.drafts['pkg-strength'].priceUnit
    delete raw.services.saved['pkg-strength'].priceInput
    delete raw.services.saved['pkg-strength'].priceUnit
    raw.screen = 'coachServices'
    raw.actor = 'coach'
    window.localStorage.setItem(first.storageKey, JSON.stringify(raw))

    vi.resetModules()
    const reloaded = await import('../state/store')
    expect(reloaded.state.screen).toBe('coachServices')
    const draft = reloaded.state.services.drafts['pkg-strength']!
    expect(draft.priceInput).toBe('759')
    expect(draft.priceYuan).toBe(759)
    expect(draft.priceUnit).toBe('/ 月')
    expect(draft.cycle).toBe('8 周')
    expect(draft.scope).toContain('每周 2 次')
    expect(draft.response).toContain('12 小时')
    expect(reloaded.servicePriceIssues.value['pkg-strength']).toBeNull()

    const { default: App } = await import('../App.vue')
    const wrapper = mount(App, { attachTo: document.body })
    await flushPromises()
    await nextTick()
    expect(document.querySelectorAll('#main')).toHaveLength(1)
    expect(document.querySelector('.preview-trigger')).toBeTruthy()
    wrapper.unmount()
    reloaded.resetPreview()
  })

  it('旧缓存里的公开投影按已保存服务刷新，旧约定快照按服务版本恢复且不跟随新报价', async () => {
    vi.useRealTimers()
    vi.resetModules()
    window.localStorage.clear()
    const first = await import('../state/store')
    first.updateServiceField('pkg-strength', 'priceInput', '759')
    first.updateServiceField('pkg-strength', 'cycle', '8 周')
    first.updateServiceField('pkg-strength', 'scope', '每周 2 次训练安排；每次训练后的文字反馈')
    first.updateServiceField('pkg-strength', 'goalsText', '基础力量与节奏恢复')
    first.updateServiceField('pkg-strength', 'response', '工作日 12 小时内回复')
    expect(first.saveServices()).toBe(true)
    await new Promise((resolve) => setTimeout(resolve, 300))
    const raw = JSON.parse(window.localStorage.getItem(first.storageKey)!)
    const guYan = raw.coaches.find((coach: any) => coach.id === 'gu-yan')
    const pack = guYan.packages.find((item: any) => item.id === 'pkg-strength')
    pack.summary = '每周 3 次完整训练安排'
    pack.priceYuan = 699
    pack.priceUnit = '/ 月'
    pack.cycle = '4 周'
    pack.includes = ['每周 3 次训练安排']
    pack.goals = ['力量基础']
    pack.response = '工作日 24 小时内回复'
    pack.version = 2
    const relation = raw.relations.find((item: any) => item.id === 'rel-gu-yan')
    delete relation.agreedName
    delete relation.agreedPriceUnit
    delete relation.agreedCycle
    delete relation.agreedNeedsReview
    raw.screen = 'coachDetail'
    raw.actor = 'student'
    raw.selection.selectedCoachId = 'gu-yan'
    raw.selection.selectedPackageId = 'pkg-strength'
    window.localStorage.setItem(first.storageKey, JSON.stringify(raw))

    vi.resetModules()
    const reloaded = await import('../state/store')
    const projected = reloaded.state.coaches
      .find((coach) => coach.id === 'gu-yan')!
      .packages.find((item) => item.id === 'pkg-strength')!
    expect(projected.priceYuan).toBe(759)
    expect(projected.priceUnit).toBe('/ 月')
    expect(projected.cycle).toBe('8 周')
    expect(projected.includes).toEqual(['每周 2 次训练安排', '每次训练后的文字反馈'])
    expect(projected.goals).toEqual(['基础力量与节奏恢复'])
    expect(projected.response).toBe('工作日 12 小时内回复')
    const agreement = reloaded.state.relations.find((item) => item.id === 'rel-gu-yan')!
    expect(agreement.agreedPriceYuan).toBe(699)
    expect(agreement.agreedName).toBe('力量基础 · 月度计划')
    expect(agreement.agreedPriceUnit).toBe('/ 月')
    expect(agreement.agreedCycle).toBe('4 周')
    expect(agreement.serviceVersion).toBe(2)
    expect(agreement.agreedNeedsReview).toBe(false)
    reloaded.resetPreview()
  })
})

describe('预览状态与界面结构', () => {
  it('16 族的每个预览状态都可应用，不抛错', async () => {
    const store = await loadStore()
    const screens = Object.keys(store.PREVIEW_STATES) as Array<keyof typeof store.PREVIEW_STATES>
    const families = screens.filter((screen) => screen !== 'studentPlan')
    expect(families).toHaveLength(16)
    for (const screen of families) {
      for (const option of store.PREVIEW_STATES[screen]) {
        expect(() => store.applyPreviewState(screen, option.key)).not.toThrow()
      }
      store.applyPreviewState(screen, store.PREVIEW_STATES[screen][0]!.key)
    }
    expect(store.state.screen).toBeTruthy()
    store.resetPreview()
  })

  it('17 个视图（16 族 + 现用计划详情）都渲染且只有一个 #main，没有 Transition 非元素根警告', async () => {
    vi.useRealTimers()
    vi.resetModules()
    window.localStorage.clear()
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => setTimeout(() => cb(0), 0))
    window.scrollTo = vi.fn()
    window.matchMedia = vi.fn().mockImplementation(() => ({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const { default: App } = await import('../App.vue')
    const store = await import('../state/store')
    const wrapper = mount(App, { attachTo: document.body })
    await flushPromises()

    const screens = [
      'today',
      'explore',
      'planDetail',
      'training',
      'records',
      'messages',
      'incomingPlan',
      'myPlan',
      'studentPlan',
      'aiAssist',
      'diet',
      'coachDetail',
      'coachTodo',
      'coachStudents',
      'coachStudentDetail',
      'coachServices',
      'coachPlan',
    ] as const
    for (const screen of screens) {
      store.state.screen = screen
      store.state.actor = ['coachTodo', 'coachStudents', 'coachStudentDetail', 'coachServices', 'coachPlan'].includes(screen)
        ? 'coach'
        : 'student'
      if (screen === 'messages') store.state.actor = 'student'
      if (screen === 'coachStudentDetail') store.ui.selectedStudentId = 'lin-che'
      await flushPromises()
      await nextTick()
      expect(document.querySelectorAll('main'), `${screen} main`).toHaveLength(1)
      expect(document.querySelectorAll('#main'), `${screen} #main`).toHaveLength(1)
      const skip = document.querySelector<HTMLAnchorElement>('a[href="#main"]')
      expect(document.querySelector(skip!.getAttribute('href')!)).toBeTruthy()
    }
    const messages = warn.mock.calls.map((call) => String(call[0])).join('\n')
    expect(messages).not.toContain('non-element root')
    wrapper.unmount()
    store.resetPreview()
    warn.mockRestore()
  })
})
