import test from 'node:test'
import assert from 'node:assert/strict'
import { createRecordingDraft } from './recording.ts'
import { recordingV2Marker, pendingRecordingV2 } from './recordingV2Intent.ts'

const draft=()=>({...createRecordingDraft('event-0001','2026-11-04','Asia/Shanghai','2026-11-05'),recordFormat:2})
const request=(path,body)=>({method:'POST',path,idempotencyKey:'request-0001',body:JSON.stringify(body)})
const creation=request('/me/recordings',{source:'free',date:'2026-11-04',timeZone:'Asia/Shanghai',clientEventId:'event-0001',mode:'after_workout'})

test('create intent survives mode edits and retains exact original request',()=>{
  const local=draft();local.syncPending=recordingV2Marker('101',local.clientEventId,creation)
  local.mode='per_set'
  assert.deepEqual(pendingRecordingV2(local,'101').request,creation)
  assert.equal(pendingRecordingV2(local,'202'),null)
  assert.deepEqual(pendingRecordingV2({...local,date:'2026-11-03'},'101').request,creation)
})

test('sets and finish intents require the same owner, session and baseline version',()=>{
  const local={...draft(),server:{id:'42',version:5,status:'in_progress'}}
  for(const req of [request('/me/recordings/42/sets',{expectedVersion:5,exerciseEntryKeys:[]}),request('/me/recordings/42/finish',{expectedVersion:5})]) {
    const staged={...local,syncPending:recordingV2Marker('101',local.clientEventId,req)}
    assert.deepEqual(pendingRecordingV2(staged,'101').request,req)
    assert.equal(pendingRecordingV2({...staged,server:{...staged.server,version:6}},'101'),null)
    assert.equal(pendingRecordingV2({...staged,server:{...staged.server,id:'43'}},'101'),null)
  }
})

test('markers reject altered bodies, noncanonical JSON and old record formats',()=>{
  const local=draft(); const marker=recordingV2Marker('101',local.clientEventId,creation)
  assert.equal(pendingRecordingV2({...local,recordFormat:undefined,syncPending:marker},'101'),null)
  assert.equal(pendingRecordingV2({...local,syncPending:marker.replace('request-0001','bad key')},'101'),null)
  assert.throws(()=>recordingV2Marker('101',local.clientEventId,{...creation,body:'{ "source": "free" }'}))
  assert.throws(()=>recordingV2Marker('101',local.clientEventId,{...creation,path:'/me/sessions'}))
})
