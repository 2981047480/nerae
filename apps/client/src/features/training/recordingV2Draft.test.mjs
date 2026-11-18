import test from 'node:test'
import assert from 'node:assert/strict'
import { addExercise, createRecordingDraft, removeRecordedSetV2, restoreDraft, restoreUnsubmittedSet } from './recording.ts'
import { encodeDraftEnvelope, supportsDraftEnvelope } from './draftEnvelope.ts'

const make = () => {
  const fresh = createRecordingDraft('event-0001','2026-11-04','Asia/Shanghai','2026-11-05')
  const added = addExercise(fresh,'entry-0001',{source:'custom',exerciseName:'深蹲'},'weight','record-001')
  added.exercises[0].sets[0] = {...added.exercises[0].sets[0],weightKg:'45',reps:'8',persisted:true}
  return {...added,recordFormat:2,completionRequested:true,server:{id:'42',version:2,status:'completed'}}
}

test('v2 drafts use a protected new envelope and cannot be interpreted as an old one',()=>{
  const draft=make(); const encoded=JSON.parse(encodeDraftEnvelope('101',draft))
  assert.equal(encoded.schema,3); assert.equal(supportsDraftEnvelope(3,draft),true)
  assert.equal(supportsDraftEnvelope(2,draft),false); assert.equal(supportsDraftEnvelope(3,{...draft,recordFormat:undefined}),false)
  assert.equal(restoreDraft(draft,'2026-11-05').completionRequested,true)
})

test('a tracked baseline uses schema 4 and cannot be read as schema 3',()=>{
  const draft={...make(),syncTracking:1};const encoded=JSON.parse(encodeDraftEnvelope('101',draft))
  assert.equal(encoded.schema,4);assert.equal(supportsDraftEnvelope(4,draft),true)
  assert.equal(supportsDraftEnvelope(3,draft),false);assert.equal(supportsDraftEnvelope(4,{...draft,syncTracking:undefined}),false)
  assert.throws(()=>restoreDraft({...draft,server:undefined},'2026-11-05'))
})

test('removing a synced last group keeps its identity and input and returns the local record to a draft',()=>{
  const initial=make(); const removed=removeRecordedSetV2(initial,'entry-0001','record-001')
  assert.equal(removed.completionRequested,false); assert.equal(removed.exercises[0].sets.length,0)
  assert.deepEqual(removed.exercises[0].removedSets[0],{set:{...initial.exercises[0].sets[0],dirty:true},removedPosition:1})
  assert.equal(restoreDraft(removed,'2026-11-05').exercises[0].removedSets[0].set.persisted,true)
  const restored=restoreUnsubmittedSet(removed,'entry-0001','record-001')
  assert.equal(restored.exercises[0].sets[0].weightKg,'45'); assert.equal(restored.exercises[0].sets[0].persisted,true)
  assert.equal(restored.exercises[0].sets[0].dirty,true)
})

test('legacy records cannot use the v2 synced-removal path',()=>{
  const draft=make(); delete draft.recordFormat
  assert.throws(()=>removeRecordedSetV2(draft,'entry-0001','record-001'))
})
