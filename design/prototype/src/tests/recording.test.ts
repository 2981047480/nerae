// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
  window.localStorage.clear()
  window.scrollTo = vi.fn()
  window.matchMedia = vi.fn().mockReturnValue({ matches:false, addEventListener:vi.fn() })
})
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })

async function blankLog() {
  const store = await import('../state/store')
  store.state.session.exercises = []
  store.state.inputs = {}
  store.state.session.status = 'in_progress'
  store.state.session.conflicts = []
  const exerciseId = store.addLogExercise('测试卧推')!
  const setId = store.state.session.exercises[0]!.sets[0]!.id
  return {store,exerciseId,setId}
}

function fill(store:Awaited<ReturnType<typeof blankLog>>['store'],setId:string,weight='15',reps='8') {
  store.setInputValue(setId,'weightKg',weight)
  store.setInputValue(setId,'reps',reps)
}

describe('R04 两种记录方式', () => {
  it('默认训练后记录；历史仅作参考，空白实际数据不会被完成', async () => {
    const store = await import('../state/store')
    expect(store.state.recordingMode).toBe('after')
    const entry = store.currentEntry.value!
    expect(entry.exercise.lastRef.weightKg).toBe(12.5)
    expect(store.logInput(entry.exercise,entry.set).weightKg).toBe('')
    expect(store.recordLogSet(entry.set.id)).toBe(false)
    expect(entry.set.status).toBe('pending')
    store.usePreviousLogSet(entry.set.id)
    expect(store.logInput(entry.exercise,entry.set).reps).toBe('10')
    expect(entry.set.status).toBe('pending')
  })

  it('模式切换与刷新保留相同组 ID 和未确认输入', async () => {
    const {store,setId} = await blankLog()
    fill(store,setId,'17.5','11')
    const sessionId=store.state.session.id
    store.state.session.selectedExerciseId=store.state.session.exercises[0]!.id
    store.setRecordingMode('set')
    store.setRecordingMode('after')
    store.setRecordingMode('set')
    vi.resetModules()
    const restored=await import('../state/store')
    expect(restored.state.session.id).toBe(sessionId)
    expect(restored.state.recordingMode).toBe('set')
    expect(restored.state.session.selectedExerciseId).toBe(store.state.session.selectedExerciseId)
    expect(restored.state.inputs[setId]).toEqual({weightKg:'17.5',reps:'11',seconds:''})
    expect(restored.state.session.exercises[0]!.sets[0]!.status).toBe('pending')
  })

  it('批量保存只收实际填写组，重复保存/归档使用同一记录 ID', async () => {
    const {store,setId,exerciseId}=await blankLog()
    fill(store,setId)
    store.addLogSet(exerciseId)
    expect(store.saveTrainingLog()).toBe(true)
    const id=store.state.session.id
    expect(store.recordRows.value.find(r=>r.id===id)?.sets).toHaveLength(1)
    expect(store.recordRows.value.find(r=>r.id===id)?.partial).toBe(true)
    expect(store.saveTrainingLog()).toBe(true)
    expect(store.state.records.filter(r=>r.id===id)).toHaveLength(1)
    store.goToPlanDetail('plan-core')
    store.requestStartPlan('plan-core')
    store.confirmReplaceAndStart()
    expect(store.recordRows.value.filter(r=>r.id===id)).toHaveLength(1)
    expect(store.recordRows.value.find(r=>r.id===id)?.partial).toBe(true)
    expect(store.recordRows.value.find(r=>r.id===id)?.sets).toHaveLength(1)
  })

  it('先校验所有填写行；有半行时不把另一行偷偷记为完成', async () => {
    const {store,setId,exerciseId}=await blankLog()
    fill(store,setId)
    const second=store.addLogSet(exerciseId)!
    store.setInputValue(second,'weightKg','20')
    expect(store.saveTrainingLog()).toBe(false)
    expect(store.doneSets.value).toBe(0)
    expect(store.logErrors[second]?.reps).toBeTruthy()
    expect(store.state.inputs[setId]?.weightKg).toBe('15')
  })

  it('换计划时保留已保存事实，不让未确认编辑覆盖它，并保留新增已记下组', async () => {
    const {store,setId,exerciseId}=await blankLog()
    fill(store,setId,'20','10')
    store.saveTrainingLog()
    const id=store.state.session.id
    store.setInputValue(setId,'weightKg','25')
    const second=store.addLogSet(exerciseId)!
    fill(store,second,'22.5','8')
    expect(store.recordLogSet(second)).toBe(true)
    store.goToPlanDetail('plan-core')
    store.requestStartPlan('plan-core')
    store.confirmReplaceAndStart()
    const record=store.recordRows.value.find(r=>r.id===id)!
    expect(record.sets).toHaveLength(2)
    expect(record.sets.find(s=>s.id===setId)?.weightKg).toBe(20)
    expect(record.sets.find(s=>s.id===second)?.weightKg).toBe(22.5)
  })

  it('逐组可任选动作，重复确认不重复计数、不伪造时间间隔', async () => {
    const {store,setId}=await blankLog()
    const another=store.addLogExercise('坐姿划船')!
    const next=store.state.session.exercises.find(e=>e.id===another)!.sets[0]!
    store.setRecordingMode('set')
    fill(store,next.id,'30','12')
    expect(store.recordLogSet(next.id)).toBe(true)
    const timestamp=next.completedAt
    expect(Date.parse(timestamp!)).toBe(Date.now())
    await vi.advanceTimersByTimeAsync(10000)
    expect(store.recordLogSet(next.id)).toBe(true)
    expect(next.completedAt).toBe(timestamp)
    expect(store.doneSets.value).toBe(1)
    expect(store.state.session.exercises[0]!.sets.find(s=>s.id===setId)?.status).toBe('pending')
    expect(store.state.session).not.toHaveProperty('rest')
  })

  it('删除后编号连续，再加组不复用组 ID，已删输入不泄漏', async () => {
    const {store,setId,exerciseId}=await blankLog()
    fill(store,setId)
    const second=store.addLogSet(exerciseId)!
    store.removeLogSet(setId)
    const third=store.addLogSet(exerciseId)!
    expect(third).not.toBe(setId)
    expect(store.state.session.exercises[0]!.sets.map(s=>[s.id,s.index])).toEqual([[second,1],[third,2]])
    expect(store.state.inputs[setId]).toBeUndefined()
    store.removeLogExercise(exerciseId)
    expect(store.state.inputs[second]).toBeUndefined()
    expect(store.state.session.exercises).toHaveLength(0)
  })

  it('补记按训练日期归档，拒绝不存在和未来日期', async () => {
    const {store,setId}=await blankLog()
    fill(store,setId)
    expect(store.setTrainingDate('2026-02-30')).toBe(false)
    expect(store.setTrainingDate('2999-12-31')).toBe(false)
    expect(store.setTrainingDate('2026-10-20')).toBe(true)
    expect(store.saveTrainingLog()).toBe(true)
    const record=store.state.records.find(r=>r.id===store.state.session.id)!
    expect(record.date).toBe('2026-10-20')
    expect(record.lateRecorded).toBe(true)
    expect(Date.parse(record.sets[0]!.completedAt)).toBe(Date.now())
  })

  it('实际秒数可手填，自重按次数，不生成计时器', async () => {
    const {store}=await blankLog()
    store.state.session.exercises=[]
    const duration=store.addLogExercise('侧桥','durationPerSide')!
    const seconds=store.state.session.exercises.find(e=>e.id===duration)!.sets[0]!
    store.setInputValue(seconds.id,'seconds','40')
    const bodyweight=store.addLogExercise('俯卧撑','bodyweight')!
    const reps=store.state.session.exercises.find(e=>e.id===bodyweight)!.sets[0]!
    store.setInputValue(reps.id,'reps','12')
    expect(store.saveTrainingLog()).toBe(true)
    expect(seconds.seconds).toBe(40)
    expect(seconds.reps).toBeNull()
    expect(reps.weightKg).toBeNull()
    expect(reps.reps).toBe(12)
  })

  it('存储失败明确失败且保留输入，恢复后同记录重试', async () => {
    const {store,setId}=await blankLog()
    fill(store,setId)
    const storage=vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{throw new Error('quota')})
    expect(store.saveTrainingLog()).toBe(false)
    expect(store.logMessage.error).toBe(true)
    expect(store.logMessage.text).toContain('未能保存')
    expect(store.state.inputs[setId]?.reps).toBe('8')
    storage.mockRestore()
    expect(store.saveTrainingLog()).toBe(true)
    expect(store.state.records.filter(r=>r.id===store.state.session.id)).toHaveLength(1)
  })

  it('冲突选择不推测本机事实，采用另一份后输入与保存值一致', async () => {
    const {store,setId}=await blankLog()
    store.state.session.conflicts=[{setId,exerciseName:'测试卧推',setIndex:1,localText:'—',remoteText:'12.5 kg × 8 次',remoteAt:'另一份样例'}]
    expect(store.resolveSetConflict('local')).toBe(false)
    expect(store.doneSets.value).toBe(0)
    fill(store,setId,'20','10')
    expect(store.resolveSetConflict('remote')).toBe(true)
    expect(store.state.inputs[setId]).toEqual({weightKg:'12.5',reps:'8',seconds:''})
    expect(store.saveTrainingLog()).toBe(true)
    expect(store.state.records[0]!.sets[0]!.weightKg).toBe(12.5)
    expect(store.state.records[0]!.sets[0]!.reps).toBe(8)
  })

  it('旧持久化数据恢复时丢弃休息倒计时，保留组事实', async () => {
    const {store,setId}=await blankLog()
    fill(store,setId)
    store.setRecordingMode('set')
    const raw=JSON.parse(window.localStorage.getItem(store.storageKey)!)
    raw.session.rest={paused:false,endsAt:Date.now()+60000}
    delete raw.recordingMode
    window.localStorage.setItem(store.storageKey,JSON.stringify(raw))
    vi.resetModules()
    const restored=await import('../state/store')
    expect(restored.state.session).not.toHaveProperty('rest')
    expect(restored.state.recordingMode).toBe('after')
    expect(restored.state.inputs[setId]?.weightKg).toBe('15')
  })

  it('界面可填写和切换模式，并逐组确认实际输入', async () => {
    const {store,setId}=await blankLog()
    const {default:TrainingView}=await import('../views/TrainingView.vue')
    const wrapper=mount(TrainingView)
    await wrapper.get(`input[aria-label="测试卧推 第 1 组重量 kg"]`).setValue('22.5')
    await wrapper.get(`input[aria-label="测试卧推 第 1 组次数"]`).setValue('9')
    const modeButton=wrapper.findAll('button').find(b=>b.text()==='每组完成后记录')!
    await modeButton.trigger('click')
    expect(store.state.inputs[setId]?.weightKg).toBe('22.5')
    expect(wrapper.find('.log-record-set').exists()).toBe(true)
    await wrapper.get('.log-record-set').trigger('click')
    await flushPromises()
    expect(store.doneSets.value).toBe(1)
    expect(wrapper.text()).not.toContain('倒计时')
    wrapper.unmount()
  })

  it('空记录有添加动作入口，空保存显示错误且不生成记录', async () => {
    const {store}=await blankLog()
    store.state.session.exercises=[]
    const existing=store.state.records.length
    const {default:TrainingView}=await import('../views/TrainingView.vue')
    const wrapper=mount(TrainingView)
    expect(wrapper.text()).toContain('这次练了什么？')
    expect(wrapper.findAll('button').some(b=>b.text()==='添加动作')).toBe(true)
    await wrapper.findAll('button').find(b=>b.text().startsWith('保存记录'))!.trigger('click')
    expect(wrapper.get('.log-result[role="alert"]').text()).toContain('至少填写一组')
    expect(store.state.records).toHaveLength(existing)
    wrapper.unmount()
  })

  it('撤销只恢复删除项，不覆盖删除后对其他组的编辑', async () => {
    const {store,setId,exerciseId}=await blankLog()
    fill(store,setId,'20','10')
    const second=store.addLogSet(exerciseId)!
    fill(store,second,'25','8')
    const {default:TrainingView}=await import('../views/TrainingView.vue')
    const wrapper=mount(TrainingView,{global:{stubs:{teleport:true}}})
    await wrapper.get('button[aria-label="移除 测试卧推 第 1 组"]').trigger('click')
    await wrapper.findAll('button').find(b=>b.text()==='确认移除')!.trigger('click')
    expect(store.state.inputs[setId]).toBeUndefined()
    await wrapper.get('input[aria-label="测试卧推 第 1 组重量 kg"]').setValue('27.5')
    await wrapper.findAll('button').find(b=>b.text()==='撤销')!.trigger('click')
    expect(store.state.inputs[setId]?.weightKg).toBe('20')
    expect(store.state.inputs[second]?.weightKg).toBe('27.5')
    expect(store.state.session.exercises[0]!.sets.map(s=>[s.id,s.index])).toEqual([[setId,1],[second,2]])
    wrapper.unmount()
  })

  it('拒绝日期后界面恢复原值；改已保存日期需要重新保存记录', async () => {
    const {store,setId}=await blankLog()
    fill(store,setId)
    expect(store.saveTrainingLog()).toBe(true)
    const original=store.state.session.trainingDate
    const {default:TrainingView}=await import('../views/TrainingView.vue')
    const wrapper=mount(TrainingView)
    await wrapper.get('input[aria-label="训练日期"]').setValue('2999-12-31')
    expect((wrapper.get('input[aria-label="训练日期"]').element as HTMLInputElement).value).toBe(original)
    expect(wrapper.get('input[aria-label="训练日期"]').attributes('aria-invalid')).toBe('true')
    await wrapper.get('input[aria-label="训练日期"]').setValue('2026-10-20')
    expect(store.state.session.status).toBe('in_progress')
    expect(store.state.records.find(r=>r.id===store.state.session.id)?.date).toBe(original)
    expect(store.saveTrainingLog()).toBe(true)
    expect(store.state.records.find(r=>r.id===store.state.session.id)?.date).toBe('2026-10-20')
    wrapper.unmount()
  })
})
