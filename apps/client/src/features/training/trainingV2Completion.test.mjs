import test from 'node:test'
import assert from 'node:assert/strict'
import { SecureDraftStorage } from './secureDraftStorage.ts'
import { TrainingEditorController } from './trainingEditorController.ts'

function fixture() {
  const rows=new Map()
  const native={rows,discoverTrainingDrafts(){return{ok:true,errorCode:'',found:true,value:JSON.stringify({eventIds:[],nextCursor:null,hasUnclassifiedItems:false})}},
    readVersionedTrainingDraft(key){const row=rows.get(key);return row?{ok:true,errorCode:'',found:true,value:row.value,revision:row.rev}:{ok:true,errorCode:'',found:false,value:'',revision:'0'}},
    compareAndSetVersionedTrainingDraft(key,revision,found,before,after){const old=rows.get(key);if((old?.rev||'0')!==revision||Boolean(old)!==found||(old?.value||'')!==before)return{ok:false,errorCode:'conflict',found:false,value:'',revision:''};const rev=String(Number(revision)+1);rows.set(key,{value:after,rev});return{ok:true,errorCode:'',found:false,value:'',revision:rev}}}
  const auth={generation:'generation-1',isCurrent:(s,g)=>s==='101'&&g==='generation-1'}
  const storage=new SecureDraftStorage(native,'101',auth)
  const ids={nextId:(()=>{const values=['event-finish-1','entry-0001','record-001'];return()=>values.shift()})()}
  return {native,auth,storage,ids,now:()=>new Date('2026-11-05T00:00:00Z')}
}

test('new protected drafts can use v2 before completion and per-set input may be incomplete',async()=>{
  const f=fixture();const created=await TrainingEditorController.create({subjectId:'101',date:'2026-11-04',timeZone:'Asia/Shanghai',...f})
  assert.equal(created.ok,true);const editor=created.controller
  assert.equal((await editor.enableV2Recording()).ok,true)
  assert.equal(editor.draft.recordFormat,2);assert.equal(editor.draft.completionRequested,false)
  assert.equal(JSON.parse([...f.native.rows.values()][0].value).schema,3)
  await editor.addCustomExercise('深蹲','weight');await editor.setMode('per_set')
  await editor.editGroup('entry-0001','record-001','weightKg','45')
  assert.equal((await editor.confirmGroup('entry-0001','record-001')).ok,true)
  assert.equal(editor.draft.exercises[0].sets[0].confirmed,true)
  await editor.editGroup('entry-0001','record-001','weightKg','45..')
  assert.equal((await editor.confirmGroup('entry-0001','record-001')).ok,false)
  assert.equal((await editor.requestV2Completion()).ok,false)
  assert.equal(editor.draft.completionRequested,false)
  await editor.close()
})

test('offline completion persists every remaining card including a blank group',async()=>{
  const f=fixture();const created=await TrainingEditorController.create({subjectId:'101',date:'2026-11-04',timeZone:'Asia/Shanghai',...f})
  assert.equal(created.ok,true);const editor=created.controller
  assert.equal((await editor.requestV2Completion()).ok,false)
  assert.equal((await editor.addCustomExercise('深蹲','weight')).ok,true)
  assert.equal((await editor.requestV2Completion()).ok,true)
  const row=[...f.native.rows.values()][0];const envelope=JSON.parse(row.value)
  assert.equal(envelope.schema,3);assert.equal(envelope.draft.completionRequested,true)
  assert.equal(envelope.draft.exercises[0].sets[0].weightKg,'')
  await editor.close()
  const reopened=await TrainingEditorController.attach('101','event-finish-1',f.storage,f.now,f.auth,f.ids)
  assert.equal(reopened.ok,true);assert.equal(reopened.controller.draft.completionRequested,true)
  assert.equal(reopened.controller.draft.exercises[0].sets.length,1)
  await reopened.controller.close()
})

test('the v2 create request is persisted before sending and survives subsequent edits and restart',async()=>{
  const { RecordingApi }=await import('./recordingApi.ts')
  const { pendingRecordingV2 }=await import('./recordingV2Intent.ts')
  const f=fixture();const created=await TrainingEditorController.create({subjectId:'101',date:'2026-11-04',timeZone:'Asia/Shanghai',...f})
  assert.equal(created.ok,true);const editor=created.controller
  assert.equal((await editor.addCustomExercise('深蹲','weight')).ok,true);assert.equal((await editor.requestV2Completion()).ok,true)
  const auth={current:()=>({subjectId:'101',generation:'generation-1',accessToken:'token'}),isCurrent:f.auth.isCurrent}
  const api=new RecordingApi('https://api.example.org',auth,async()=>{throw new Error('must not send')},{subjectId:'101',generation:'generation-1'})
  const body=JSON.stringify({source:'free',date:'2026-11-04',timeZone:'Asia/Shanghai',clientEventId:'event-finish-1',mode:'after_workout'})
  const request={method:'POST',path:'/me/recordings',idempotencyKey:'request-key-01',body}
  assert.equal((await editor.stageRecordingV2(request,null,api,auth)).ok,true)
  assert.equal((await editor.setDate('2026-11-03')).ok,true)
  assert.equal((await editor.editGroup('entry-0001','record-001','reps','9')).ok,true)
  const saved=JSON.parse([...f.native.rows.values()][0].value)
  assert.equal(saved.schema,3); assert.deepEqual(pendingRecordingV2(saved.draft,'101').request,request)
  assert.equal(saved.draft.date,'2026-11-03')
  assert.equal(saved.draft.exercises[0].sets[0].reps,'9')
  await editor.close()
  const reopened=await TrainingEditorController.attach('101','event-finish-1',f.storage,f.now,f.auth,f.ids)
  assert.equal(reopened.ok,true); assert.deepEqual(pendingRecordingV2(reopened.controller.draft,'101').request,request)
  assert.equal(reopened.controller.draft.date,'2026-11-03')
  await reopened.controller.close()
})

test('a verified create receipt advances only server metadata and keeps edits made while sending',async()=>{
  const { RecordingApi }=await import('./recordingApi.ts')
  const f=fixture();const created=await TrainingEditorController.create({subjectId:'101',date:'2026-11-04',timeZone:'Asia/Shanghai',...f})
  const editor=created.controller;await editor.addCustomExercise('深蹲','weight');await editor.requestV2Completion()
  const auth={current:()=>({subjectId:'101',generation:'generation-1',accessToken:'token'}),isCurrent:f.auth.isCurrent}
  const detail={id:'42',version:1,source:'free',date:'2026-11-04',timeZone:'Asia/Shanghai',clientEventId:'event-finish-1',status:'in_progress',mode:'after_workout',createdAt:'2026-11-04T00:00:00Z',updatedAt:'2026-11-04T00:00:00Z',entries:[],sets:[],removedSets:[]}
  let release,start;const started=new Promise(r=>{start=r});const wait=new Promise(r=>{release=r})
  const api=new RecordingApi('https://api.example.org',auth,async req=>{
    if(req.method==='GET')return{statusCode:200,data:detail}
    start();await wait;return{statusCode:201,data:{id:'42',version:1},headers:{'Idempotency-Key-Expires-At':'2027-01-01T00:00:00Z'}}
  },{subjectId:'101',generation:'generation-1'})
  const request={method:'POST',path:'/me/recordings',idempotencyKey:'request-key-01',body:JSON.stringify({source:'free',date:'2026-11-04',timeZone:'Asia/Shanghai',clientEventId:'event-finish-1',mode:'after_workout'})}
  assert.equal((await editor.stageRecordingV2(request,null,api,auth)).ok,true)
  const marker=editor.draft.syncPending;const guard=()=>editor.localStatus==='saved_locally'&&editor.draft.syncPending===marker
  const sending=api.sendStoredRecordingV2(editor.draft,guard);await started
  assert.equal((await editor.editGroup('entry-0001','record-001','reps','9')).ok,true);release()
  const receipt=await sending;const snapshot=await api.readRecordingV2('42')
  assert.equal((await editor.acknowledgeRecordingV2({...receipt},snapshot,api,auth)).ok,false)
  assert.equal((await editor.acknowledgeRecordingV2(receipt,snapshot,api,auth)).ok,true)
  assert.deepEqual(editor.draft.server,{id:'42',version:1,status:'in_progress'})
  assert.equal(editor.draft.exercises[0].sets[0].reps,'9');assert.equal(editor.draft.completionRequested,true)
  assert.equal(editor.draft.syncPending,undefined)
  await editor.close()
})
