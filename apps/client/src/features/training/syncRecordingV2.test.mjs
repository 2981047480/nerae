import test from 'node:test'
import assert from 'node:assert/strict'
import { SecureDraftStorage } from './secureDraftStorage.ts'
import { TrainingEditorController } from './trainingEditorController.ts'
import { RecordingApi } from './recordingApi.ts'
import { syncRecordingV2 } from './syncRecordingV2.ts'
import { pendingRecordingV2 } from './recordingV2Intent.ts'
import { batchSetSnapshot } from './recording.ts'
import { createAppNativeHttpsPorts } from '../../platform/app/nativeHttpsTransport.ts'

const event='event-sync-01', lease={subjectId:'101',generation:'login-1'}
const auth={generation:'login-1',current:()=>({...lease,accessToken:'test-token'}),isCurrent:(id,g)=>id==='101'&&g==='login-1'}
const now=()=>new Date('2026-11-05T00:00:00Z')
function nativePort(){
  const rows=new Map();let loseAcknowledgement=false;return{rows,set loseAcknowledgement(value){loseAcknowledgement=value},discoverTrainingDrafts(){return{ok:true,errorCode:'',found:true,value:JSON.stringify({eventIds:[],nextCursor:null,hasUnclassifiedItems:false})}},
    readVersionedTrainingDraft(key){const row=rows.get(key);return row?{ok:true,errorCode:'',found:true,value:row.value,revision:row.rev}:{ok:true,errorCode:'',found:false,value:'',revision:'0'}},
    compareAndSetVersionedTrainingDraft(key,rev,found,before,after){const old=rows.get(key);if((old?.rev||'0')!==rev||Boolean(old)!==found||(old?.value||'')!==before)return{ok:false,errorCode:'conflict',found:false,value:'',revision:''};const next=String(Number(rev)+1);rows.set(key,{rev:next,value:after});
      if(loseAcknowledgement&&before){const prior=JSON.parse(before).draft;const nextDraft=JSON.parse(after).draft
        if(prior.syncPending&&!nextDraft.syncPending&&JSON.parse(prior.syncPending).request.path.endsWith('/sets')){loseAcknowledgement=false;throw Error('native write completed but result was lost')}}
      return{ok:true,errorCode:'',found:false,value:'',revision:next}}}
}
const replayHeaders={'Idempotency-Key-Expires-At':'2027-01-01T00:00:00Z','Idempotency-Replayed':'true','Content-Type':'application/json','Cache-Control':'no-store','X-Request-Id':'0123456789abcdef0123456789abcdef'}
const writeHeaders={'Idempotency-Key-Expires-At':'2027-01-01T00:00:00Z'}
function server() {
  let state=null;const receipts=new Map();const calls=[];let loseSets=false;let offline=false
  const request=async req=>{
    const path=new URL(req.url).pathname;calls.push({method:req.method,path,key:req.header['Idempotency-Key'],body:req.data})
    if(offline)throw Error('offline')
    if(req.method==='GET')return state?{statusCode:200,data:structuredClone(state)}:{statusCode:404,data:{}}
    const replay=path.endsWith('/replay');const base=replay?path.slice(0,-7):path
    const key=req.header['Idempotency-Key'];const old=receipts.get(key)
    if(replay)return old&&old.path===base&&old.body===req.data?{statusCode:old.status,data:old.receipt,headers:replayHeaders}:{statusCode:503,data:{}}
    if(old)return old.path===base&&old.body===req.data?{statusCode:old.status,data:old.receipt,headers:replayHeaders}:{statusCode:409,data:{}}
    const input=JSON.parse(req.data);let status=200
    if(base==='/me/recordings'){
      if(state)return{statusCode:409,data:{}}
      state={id:'42',version:1,source:'free',date:input.date,timeZone:input.timeZone,clientEventId:input.clientEventId,status:'in_progress',mode:input.mode,createdAt:'2026-11-04T00:00:00Z',updatedAt:'2026-11-04T00:00:00Z',entries:[],sets:[],removedSets:[]};status=201
    }else if(!state||input.expectedVersion!==state.version)return{statusCode:412,data:{}}
    else if(base==='/me/recordings/42/sets'){
      for(const entry of input.addEntries||[])state.entries.push({...entry,source:'custom'})
      for(const set of input.sets||[]){const index=state.sets.findIndex(x=>x.recordKey===set.recordKey);if(index<0)state.sets.push(set);else state.sets[index]=set}
      for(const removal of input.removeRecords||[]){const index=state.sets.findIndex(x=>x.recordKey===removal.recordKey);if(index>=0){const [set]=state.sets.splice(index,1);state.removedSets.push({set,removedPosition:removal.removedPosition})}}
      if(input.date)state.date=input.date
      if(input.mode)state.mode=input.mode
      state.version++
    }else if(base==='/me/recordings/42/finish'){
      state.status='completed';state.recordedAt='2026-11-04T01:00:00Z';state.version++
    }else return{statusCode:404,data:{}}
    const receipt={id:'42',version:state.version};receipts.set(key,{path:base,body:req.data,status,receipt})
    if(loseSets&&base.endsWith('/sets')){loseSets=false;throw Error('response lost after commit')}
    return{statusCode:status,data:receipt,headers:writeHeaders}
  }
  return {request,calls,get state(){return state},set loseSets(value){loseSets=value},set offline(value){offline=value},advance(){state.version++},expire(){receipts.clear()}}
}
async function setup({completed=true}={}){
  const port=nativePort(),storage=new SecureDraftStorage(port,'101',auth)
  const ids={nextId:(()=>{const values=[event,'entry-0001','record-001','record-002'];return()=>values.shift()})()}
  const created=await TrainingEditorController.create({subjectId:'101',date:'2026-11-04',timeZone:'Asia/Shanghai',storage,now,auth,ids})
  assert.equal(created.ok,true);const editor=created.controller
  await editor.addCustomExercise('深蹲','weight');await editor.editGroup('entry-0001','record-001','weightKg','45');await editor.editGroup('entry-0001','record-001','reps','8');await editor.addGroup('entry-0001');if(completed)await editor.requestV2Completion();else await editor.enableV2Recording()
  const service=server(),api=new RecordingApi('https://api.example.org',auth,service.request,lease)
  let counter=0;const nextKey=()=>`request-key-${String(++counter).padStart(3,'0')}`
  return{editor,port,storage,ids,service,api,nextKey}
}

test('create, save all retained groups including a blank one, then finish',async()=>{
  const f=await setup();const result=await syncRecordingV2(f.editor,f.api,auth,f.nextKey)
  assert.equal(result.state,'synced');assert.equal(f.service.state.status,'completed');assert.equal(f.service.state.sets.length,2)
  assert.deepEqual(f.service.state.sets[1].measurement,{method:'total'})
  assert.equal(f.editor.draft.server.status,'completed');assert.equal(f.editor.draft.syncPending,undefined)
  assert.equal(f.editor.draft.exercises[0].sets[1].persisted,true)
  await f.editor.close()
})

test('lost response survives restart and replay does not duplicate groups',async()=>{
  const f=await setup();f.service.loseSets=true
  const first=await syncRecordingV2(f.editor,f.api,auth,f.nextKey);assert.equal(first.state,'pending')
  const marker=f.editor.draft.syncPending;const old=pendingRecordingV2(f.editor.draft,'101').request
  assert.equal(f.service.state.sets.length,2);await f.editor.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  assert.equal(reopened.controller.draft.syncPending,marker)
  const second=await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)
  assert.equal(second.state,'synced');assert.equal(f.service.state.sets.length,2)
  const replays=f.service.calls.filter(call=>call.path.endsWith('/sets/replay'))
  assert.equal(replays.length,2);assert.equal(replays[1].key,old.idempotencyKey);assert.equal(replays[1].body,old.body)
  await reopened.controller.close()
})

test('expired receipt and a newer cloud version keep the original local changes for review',async()=>{
  const f=await setup();f.service.loseSets=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'pending')
  const marker=f.editor.draft.syncPending;const before=f.service.calls.filter(call=>call.path==='/me/recordings/42/sets').length
  f.service.expire();f.service.advance()
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(f.editor.draft.syncPending,marker)
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'45')
  assert.equal(f.service.calls.filter(call=>call.path==='/me/recordings/42/sets').length,before)
  await f.editor.close()
})

test('editing after a response is lost survives replay and is sent as a new version',async()=>{
  const f=await setup();f.service.loseSets=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'pending')
  const pending=pendingRecordingV2(f.editor.draft,'101').request
  assert.equal((await f.editor.editGroup('entry-0001','record-001','weightKg','50')).ok,true)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.sets[0].measurement.weightKg,'50')
  const sends=f.service.calls.filter(call=>call.path==='/me/recordings/42/sets')
  assert.equal(sends.length,2);assert.equal(sends[0].body,pending.body);assert.notEqual(sends[1].key,pending.idempotencyKey)
  await f.editor.close()
})

test('offline completion remains on device and later syncs',async()=>{
  const f=await setup();f.service.offline=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'pending')
  assert.equal(f.editor.draft.completionRequested,true);assert.ok(f.editor.draft.syncPending)
  f.service.offline=false
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.status,'completed');await f.editor.close()
})

test('the native HTTPS adapter carries exact v2 requests and receipts end to end',async()=>{
  const f=await setup();let requestNumber=0
  const ports=createAppNativeHttpsPorts('https://api.example.org',(request,callbacks)=>{
    const requestId=`native-${++requestNumber}`
    const headers=JSON.parse(request.headersJson)
    Promise.resolve().then(()=>f.service.request({url:request.baseUrl+request.path,method:request.method,header:headers,data:request.body}))
      .then(response=>callbacks.success({requestId,status:response.statusCode,body:JSON.stringify(response.data),
        headersJson:JSON.stringify({'content-type':'application/json','cache-control':'no-store',...(response.headers||{})})}))
      .catch(()=>callbacks.fail({requestId,code:'network'}))
    return requestId
  },()=>{},1000)
  const api=new RecordingApi('https://api.example.org',auth,ports.recordingRequest,lease)
  assert.equal((await syncRecordingV2(f.editor,api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.status,'completed');assert.equal(f.service.state.sets.length,2)
  assert.ok(f.service.calls.some(call=>call.path.endsWith('/sets/replay')))
  await f.editor.close()
})

test('two independent device stores preserve both versions when the same group changes',async()=>{
  const { restoreCloudRecordingV2ToLocalDraft } = await import('./cloudRecordingV2Restore.ts')
  const f=await setup();assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  await f.editor.editGroup('entry-0001','record-001','weightKg','48')
  await f.editor.close()
  const port2=nativePort(),storage2=new SecureDraftStorage(port2,'101',auth)
  const other=await restoreCloudRecordingV2ToLocalDraft({sessionId:'42',lease,auth,draftLease:auth,api:f.api,storage:storage2,ids:f.ids,now})
  assert.equal(other.ok,true)
  await other.controller.editGroup('entry-0001','record-001','weightKg','52')
  let count=0;const nextOtherKey=()=>`other-device-key-${String(++count).padStart(3,'0')}`
  assert.equal((await syncRecordingV2(other.controller,f.api,auth,nextOtherKey)).state,'synced')
  assert.equal(f.service.state.sets[0].measurement.weightKg,'52')
  assert.equal(other.controller.draft.exercises[0].sets[0].weightKg,'52')
  await other.controller.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  assert.equal((await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(reopened.controller.draft.exercises[0].sets[0].weightKg,'48')
  await reopened.controller.close()
})

test('an acknowledged server write survives an uncertain local commit without resending it',async()=>{
  const f=await setup();f.port.loseAcknowledgement=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'local_error')
  assert.equal(f.service.state.sets.length,2)
  assert.equal((await f.editor.retryLocalSave()).ok,true)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.calls.filter(call=>call.path==='/me/recordings/42/sets').length,1)
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'45')
  await f.editor.close()
})


test('offline create retains the original intent while a new training date syncs later',async t=>{
  const f=await setup();t.after(async()=>{await f.editor.close()});f.service.offline=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'pending')
  const original=f.editor.draft.syncPending
  assert.equal((await f.editor.setDate('2026-11-03')).ok,true)
  assert.equal(f.editor.draft.syncPending,original)
  f.service.offline=false
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.date,'2026-11-03');assert.equal(f.service.state.sets.length,2)
  assert.equal(f.editor.draft.date,'2026-11-03');await f.editor.close()
})

test('a completed synced recording can change date without losing its groups',async t=>{
  const f=await setup();t.after(async()=>{await f.editor.close()});assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal((await f.editor.setDate('2026-11-02')).ok,true)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.date,'2026-11-02');assert.equal(f.service.state.sets.length,2)
  assert.equal(f.service.state.status,'completed');await f.editor.close()
})

test('a date edit made after a lost set response is sent after replay',async t=>{
  const f=await setup();t.after(async()=>{await f.editor.close()});f.service.loseSets=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'pending')
  const original=f.editor.draft.syncPending
  assert.equal((await f.editor.setDate('2026-11-02')).ok,true)
  assert.equal(f.editor.draft.syncPending,original)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.date,'2026-11-02');assert.equal(f.service.state.sets.length,2)
  await f.editor.close()
})


test('date changed while creation is in flight survives the original response',async t=>{
  const f=await setup();t.after(async()=>{await f.editor.close()})
  let started, release;const reached=new Promise(resolve=>{started=resolve}), gate=new Promise(resolve=>{release=resolve})
  const request=async input=>{
    if(input.method==='POST'&&new URL(input.url).pathname==='/me/recordings'){started();await gate}
    return f.service.request(input)
  }
  const api=new RecordingApi('https://api.example.org',auth,request,lease)
  const syncing=syncRecordingV2(f.editor,api,auth,f.nextKey);await reached
  const marker=f.editor.draft.syncPending
  assert.equal((await f.editor.setDate('2026-11-03')).ok,true)
  assert.equal(f.editor.draft.syncPending,marker);release()
  assert.equal((await syncing).state,'synced')
  assert.equal(f.service.state.date,'2026-11-03');assert.equal(f.service.state.sets.length,2)
})


test('conflicting date changes on two devices keep both values for review',async t=>{
  const { restoreCloudRecordingV2ToLocalDraft }=await import('./cloudRecordingV2Restore.ts')
  const f=await setup();t.after(async()=>{try{await f.editor.close()}catch{}})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal((await f.editor.setDate('2026-11-02')).ok,true);await f.editor.close()
  const storage2=new SecureDraftStorage(nativePort(),'101',auth)
  const other=await restoreCloudRecordingV2ToLocalDraft({sessionId:'42',lease,auth,draftLease:auth,api:f.api,storage:storage2,ids:f.ids,now})
  assert.equal(other.ok,true);t.after(async()=>{try{await other.controller.close()}catch{}})
  assert.equal((await other.controller.setDate('2026-11-03')).ok,true)
  let n=0;assert.equal((await syncRecordingV2(other.controller,f.api,auth,()=>`other-date-key-${++n}`)).state,'synced')
  await other.controller.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids)
  assert.equal(reopened.ok,true);t.after(async()=>{try{await reopened.controller.close()}catch{}})
  assert.equal((await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(reopened.controller.draft.date,'2026-11-02');assert.equal(f.service.state.date,'2026-11-03')
})

test('a create receipt cannot clear the stored request when its snapshot date differs',async t=>{
  const f=await setup();t.after(async()=>{await f.editor.close()})
  const request=async input=>{
    const result=await f.service.request(input)
    if(input.method==='GET'&&result.statusCode===200)result.data.date='2026-11-03'
    return result
  }
  const api=new RecordingApi('https://api.example.org',auth,request,lease)
  assert.equal((await syncRecordingV2(f.editor,api,auth,f.nextKey)).state,'conflict')
  assert.ok(f.editor.draft.syncPending);assert.equal(f.editor.draft.date,'2026-11-04')
  assert.equal(f.service.state.sets.length,0)
})

async function conflictingDevices(t) {
  const { restoreCloudRecordingV2ToLocalDraft } = await import('./cloudRecordingV2Restore.ts')
  const f = await setup()
  t.after(async()=>{ try { await f.editor.close() } catch {} })
  assert.equal((await syncRecordingV2(f.editor, f.api, auth, f.nextKey)).state, 'synced')
  assert.equal((await f.editor.editGroup('entry-0001','record-001','weightKg','48')).ok, true)
  await f.editor.close()
  const other = await restoreCloudRecordingV2ToLocalDraft({ sessionId:'42', lease, auth, draftLease:auth, api:f.api,
    storage:new SecureDraftStorage(nativePort(),'101',auth), ids:f.ids, now })
  assert.equal(other.ok, true)
  assert.equal((await other.controller.editGroup('entry-0001','record-001','weightKg','52')).ok, true)
  let n=0
  assert.equal((await syncRecordingV2(other.controller,f.api,auth,()=>`other-choice-key-${++n}`)).state, 'synced')
  await other.controller.close()
  const attached=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids)
  assert.equal(attached.ok,true)
  t.after(async()=>{ try { await attached.controller.close() } catch {} })
  assert.equal((await syncRecordingV2(attached.controller,f.api,auth,f.nextKey)).state,'conflict')
  return {...f,editor:attached.controller}
}

test('conflicting group values can be reviewed and choosing local saves only the selected value',async t=>{
  const { reviewRecordingV2Conflict, resolveRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t)
  const result=await reviewRecordingV2Conflict(f.editor,f.api,auth)
  assert.equal(result.ok,true)
  assert.equal(result.review.rows.length,1)
  const row=result.review.rows[0]
  assert.match(row.local,/48 kg/);assert.match(row.cloud,/52 kg/)
  assert.equal((await resolveRecordingV2Conflict(f.editor,f.api,auth,result.review,{})).ok,false)
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48')
  assert.equal(f.service.state.sets[0].measurement.weightKg,'52')
  const chosen=await resolveRecordingV2Conflict(f.editor,f.api,auth,result.review,{[row.key]:'local'})
  assert.equal(chosen.ok,true)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.sets[0].measurement.weightKg,'48')
  assert.equal(f.service.state.sets.length,2)
})

test('choosing the cloud value retains the other groups and does not send a replacement',async t=>{
  const { reviewRecordingV2Conflict, resolveRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t)
  const result=await reviewRecordingV2Conflict(f.editor,f.api,auth);assert.equal(result.ok,true)
  const row=result.review.rows[0], count=f.service.calls.filter(call=>call.method==='POST'&&call.path.endsWith('/sets')).length
  assert.equal((await resolveRecordingV2Conflict(f.editor,f.api,auth,result.review,{[row.key]:'cloud'})).ok,true)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'52');assert.equal(f.editor.draft.exercises[0].sets.length,2)
  assert.equal(f.service.calls.filter(call=>call.method==='POST'&&call.path.endsWith('/sets')).length,count)
})

test('conflict choices are rejected if local or cloud changes during review',async t=>{
  const { reviewRecordingV2Conflict, resolveRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t)
  const a=await reviewRecordingV2Conflict(f.editor,f.api,auth);assert.equal(a.ok,true)
  f.service.advance()
  assert.equal((await resolveRecordingV2Conflict(f.editor,f.api,auth,a.review,{[a.review.rows[0].key]:'local'})).reason,'stale')
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48');assert.equal(f.service.state.sets[0].measurement.weightKg,'52')
  const b=await reviewRecordingV2Conflict(f.editor,f.api,auth);assert.equal(b.ok,true)
  await f.editor.editGroup('entry-0001','record-001','reps','9')
  assert.equal((await resolveRecordingV2Conflict(f.editor,f.api,auth,b.review,{[b.review.rows[0].key]:'cloud'})).reason,'stale')
  assert.equal(f.editor.draft.exercises[0].sets[0].reps,'9');assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48')
})

test('an unresolved dispatched intent is never cleared by conflict review',async t=>{
  const { reviewRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await setup();t.after(async()=>{ await f.editor.close() })
  f.service.loseSets=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'pending')
  const marker=f.editor.draft.syncPending;f.service.expire();f.service.advance()
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal((await reviewRecordingV2Conflict(f.editor,f.api,auth)).ok,false)
  assert.equal(f.editor.draft.syncPending,marker)
})

test('date, mode and group choices are independent and all must be selected',async t=>{
  const { reviewRecordingV2Conflict, resolveRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t)
  assert.equal((await f.editor.setDate('2026-11-02')).ok,true)
  assert.equal((await f.editor.setMode('per_set')).ok,true)
  const result=await reviewRecordingV2Conflict(f.editor,f.api,auth);assert.equal(result.ok,true)
  const keys=result.review.rows.map(row=>row.key)
  assert.deepEqual(keys,['date','mode','set:record-001'])
  assert.equal((await resolveRecordingV2Conflict(f.editor,f.api,auth,result.review,{date:'local',mode:'cloud'})).reason,'invalid_choice')
  assert.equal((await resolveRecordingV2Conflict(f.editor,f.api,auth,result.review,{date:'local',mode:'cloud','set:record-001':'cloud'})).ok,true)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.date,'2026-11-02');assert.equal(f.service.state.mode,'after_workout')
  assert.equal(f.service.state.sets[0].measurement.weightKg,'52')
})

test('a moved existing group remains untouched for a separate recovery path',async t=>{
  const { reviewRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t)
  f.service.state.entries.push({exerciseEntryKey:'entry-0002',source:'custom',exerciseName:'卧推'})
  f.service.state.sets.push({recordKey:'record-003',exerciseEntryKey:'entry-0002',setIndex:1,confirmed:false,measurement:{method:'total',weightKg:'30',reps:10}})
  f.service.state.sets[0].setIndex=3
  f.service.advance()
  assert.deepEqual(await reviewRecordingV2Conflict(f.editor,f.api,auth),{ok:false,reason:'unsupported'})
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48')
  assert.equal(f.service.state.sets[0].measurement.weightKg,'52');assert.equal(f.service.state.entries.length,2)
})

test('a copied or replaced conflict review cannot authorize a choice',async t=>{
  const { reviewRecordingV2Conflict, resolveRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t)
  const result=await reviewRecordingV2Conflict(f.editor,f.api,auth);assert.equal(result.ok,true)
  const fake={rows:result.review.rows}
  assert.deepEqual(await resolveRecordingV2Conflict(f.editor,f.api,auth,fake,{[fake.rows[0].key]:'cloud'}),{ok:false,reason:'stale'})
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48')
})

test('review freezes submitted choices while the fresh server read is in flight',async t=>{
  const { reviewRecordingV2Conflict, resolveRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t)
  let delay=false, start, release
  const started=new Promise(resolve=>{start=resolve}), gate=new Promise(resolve=>{release=resolve})
  const api=new RecordingApi('https://api.example.org',auth,async request=>{
    if(delay&&request.method==='GET'){start();await gate}
    return f.service.request(request)
  },lease)
  const result=await reviewRecordingV2Conflict(f.editor,api,auth);assert.equal(result.ok,true)
  const key=result.review.rows[0].key, choices={[key]:'local'};delay=true
  const pending=resolveRecordingV2Conflict(f.editor,api,auth,result.review,choices)
  await started;choices[key]='cloud';release()
  assert.equal((await pending).ok,true)
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48')
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.sets[0].measurement.weightKg,'48')
})

test('conflict review is invalidated when the account session changes',async t=>{
  const { reviewRecordingV2Conflict, resolveRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t);let active=true
  const changing={current:()=>active?{...lease,accessToken:'test-token'}:null,isCurrent:(id,g)=>active&&id==='101'&&g==='login-1'}
  const api=new RecordingApi('https://api.example.org',changing,f.service.request,lease)
  const result=await reviewRecordingV2Conflict(f.editor,api,changing);assert.equal(result.ok,true)
  active=false
  assert.deepEqual(await resolveRecordingV2Conflict(f.editor,api,changing,result.review,{[result.review.rows[0].key]:'cloud'}),
    {ok:false,reason:'session_changed'})
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48');assert.equal(f.service.state.sets[0].measurement.weightKg,'52')
})

test('repeated action segments have distinct labels when their groups differ',async t=>{
  const { recordingV2ConflictRows }=await import('./recordingV2ConflictPlan.ts')
  const f=await conflictingDevices(t),local=structuredClone(f.editor.draft),remote=structuredClone(f.service.state)
  const next=structuredClone(local.exercises[0]);next.exerciseEntryKey='entry-0002';next.sets=[{...next.sets[0],recordKey:'record-003',weightKg:'30'}];next.removedSets=[]
  local.exercises.push(next);remote.entries.push({exerciseEntryKey:'entry-0002',source:'custom',exerciseName:'深蹲'})
  remote.sets.push({recordKey:'record-003',exerciseEntryKey:'entry-0002',setIndex:1,confirmed:false,measurement:{method:'total',weightKg:'32',reps:8}})
  const rows=recordingV2ConflictRows(local,remote)
  assert.equal(rows.length,2);assert.match(rows[0].label,/第 1 段/);assert.match(rows[1].label,/第 2 段/)
})

test('an account change while loading conflict details returns session_changed',async t=>{
  const { reviewRecordingV2Conflict }=await import('./recordingV2Conflict.ts')
  const f=await conflictingDevices(t);let active=true,start,release
  const started=new Promise(resolve=>{start=resolve}),gate=new Promise(resolve=>{release=resolve})
  const changing={current:()=>active?{...lease,accessToken:'test-token'}:null,isCurrent:(id,g)=>active&&id==='101'&&g==='login-1'}
  const api=new RecordingApi('https://api.example.org',changing,async request=>{start();await gate;return f.service.request(request)},lease)
  const pending=reviewRecordingV2Conflict(f.editor,api,changing);await started;active=false;release()
  assert.deepEqual(await pending,{ok:false,reason:'session_changed'})
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48');assert.equal(f.service.state.sets[0].measurement.weightKg,'52')
})

test('conflict choices distinguish a measured zero from a missing weight',async t=>{
  const {recordingV2ConflictRows,applyRecordingV2ConflictChoices}=await import('./recordingV2ConflictPlan.ts')
  const f=await conflictingDevices(t),local=structuredClone(f.editor.draft),remote=structuredClone(f.service.state)
  local.exercises[0].sets[0].weightKg='';remote.sets[0].measurement.weightKg='0'
  const rows=recordingV2ConflictRows(local,remote);assert.equal(rows.length,1)
  assert.match(rows[0].local,/重量未填/);assert.match(rows[0].cloud,/0 kg/)
  const chosen=applyRecordingV2ConflictChoices(local,remote,{[rows[0].key]:'cloud'})
  assert.equal(chosen.exercises[0].sets[0].weightKg,'0')
})

test('a newer cloud version with identical content advances without asking the user',async t=>{
  const f=await setup();t.after(async()=>{await f.editor.close()})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  const prior=f.editor.draft.server.version, sends=f.service.calls.filter(call=>call.method==='POST'&&!call.path.endsWith('/replay')).length
  f.service.advance()
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.editor.draft.server.version,prior+1)
  assert.equal(f.service.calls.filter(call=>call.method==='POST'&&!call.path.endsWith('/replay')).length,sends)
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'45')
})

test('an unchanged local device automatically receives another device group edit',async t=>{
  const {restoreCloudRecordingV2ToLocalDraft}=await import('./cloudRecordingV2Restore.ts')
  const f=await setup();assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced');await f.editor.close()
  const other=await restoreCloudRecordingV2ToLocalDraft({sessionId:'42',lease,auth,draftLease:auth,api:f.api,
    storage:new SecureDraftStorage(nativePort(),'101',auth),ids:f.ids,now});assert.equal(other.ok,true)
  await other.controller.editGroup('entry-0001','record-001','weightKg','52')
  let n=0;assert.equal((await syncRecordingV2(other.controller,f.api,auth,()=>`other-pull-key-${++n}`)).state,'synced')
  await other.controller.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  t.after(async()=>{try{await reopened.controller.close()}catch{}})
  const writes=f.service.calls.filter(call=>call.method==='POST'&&!call.path.endsWith('/replay')).length
  assert.equal((await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(reopened.controller.draft.exercises[0].sets[0].weightKg,'52')
  assert.equal(f.service.calls.filter(call=>call.method==='POST'&&!call.path.endsWith('/replay')).length,writes)
})

test('automatic cloud refresh never discards an unsynced per-set confirmation',async t=>{
  const {restoreCloudRecordingV2ToLocalDraft}=await import('./cloudRecordingV2Restore.ts')
  const f=await setup();assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal((await f.editor.setMode('per_set')).ok,true)
  assert.equal((await f.editor.confirmGroup('entry-0001','record-001')).ok,true)
  assert.equal((await f.editor.setMode('after_workout')).ok,true);await f.editor.close()
  const other=await restoreCloudRecordingV2ToLocalDraft({sessionId:'42',lease,auth,draftLease:auth,api:f.api,
    storage:new SecureDraftStorage(nativePort(),'101',auth),ids:f.ids,now});assert.equal(other.ok,true)
  await other.controller.editGroup('entry-0001','record-001','weightKg','52')
  let n=0;assert.equal((await syncRecordingV2(other.controller,f.api,auth,()=>`other-confirm-key-${++n}`)).state,'synced')
  await other.controller.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  t.after(async()=>{try{await reopened.controller.close()}catch{}})
  assert.equal((await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(reopened.controller.draft.exercises[0].sets[0].confirmed,true)
  assert.equal(reopened.controller.draft.exercises[0].sets[0].weightKg,'45')
  assert.equal(f.service.state.sets[0].measurement.weightKg,'52')
})

test('another device completion is pulled when the local draft has not changed',async t=>{
  const {restoreCloudRecordingV2ToLocalDraft}=await import('./cloudRecordingV2Restore.ts')
  const f=await setup({completed:false});t.after(async()=>{try{await f.editor.close()}catch{}})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced');await f.editor.close()
  const other=await restoreCloudRecordingV2ToLocalDraft({sessionId:'42',lease,auth,draftLease:auth,api:f.api,
    storage:new SecureDraftStorage(nativePort(),'101',auth),ids:f.ids,now});assert.equal(other.ok,true)
  assert.equal((await other.controller.requestV2Completion()).ok,true)
  let n=0;assert.equal((await syncRecordingV2(other.controller,f.api,auth,()=>`other-finish-key-${++n}`)).state,'synced')
  await other.controller.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  t.after(async()=>{try{await reopened.controller.close()}catch{}})
  assert.equal((await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(reopened.controller.draft.completionRequested,true)
  assert.equal(reopened.controller.draft.server.status,'completed')
})

test('another device completion never discards local unsynced group edits',async t=>{
  const {restoreCloudRecordingV2ToLocalDraft}=await import('./cloudRecordingV2Restore.ts')
  const f=await setup({completed:false});t.after(async()=>{try{await f.editor.close()}catch{}})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  await f.editor.editGroup('entry-0001','record-001','weightKg','48');await f.editor.close()
  const other=await restoreCloudRecordingV2ToLocalDraft({sessionId:'42',lease,auth,draftLease:auth,api:f.api,
    storage:new SecureDraftStorage(nativePort(),'101',auth),ids:f.ids,now});assert.equal(other.ok,true)
  assert.equal((await other.controller.requestV2Completion()).ok,true)
  let n=0;assert.equal((await syncRecordingV2(other.controller,f.api,auth,()=>`other-finish-edit-key-${++n}`)).state,'synced')
  await other.controller.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  t.after(async()=>{try{await reopened.controller.close()}catch{}})
  assert.equal((await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(reopened.controller.draft.exercises[0].sets[0].weightKg,'48')
  assert.equal(reopened.controller.draft.completionRequested,false)
})

test('another device can change a group and finish before the untouched device returns',async t=>{
  const {restoreCloudRecordingV2ToLocalDraft}=await import('./cloudRecordingV2Restore.ts')
  const f=await setup({completed:false});t.after(async()=>{try{await f.editor.close()}catch{}})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced');await f.editor.close()
  const other=await restoreCloudRecordingV2ToLocalDraft({sessionId:'42',lease,auth,draftLease:auth,api:f.api,
    storage:new SecureDraftStorage(nativePort(),'101',auth),ids:f.ids,now});assert.equal(other.ok,true)
  await other.controller.editGroup('entry-0001','record-001','weightKg','52');await other.controller.requestV2Completion()
  let n=0;assert.equal((await syncRecordingV2(other.controller,f.api,auth,()=>`other-edit-finish-key-${++n}`)).state,'synced')
  await other.controller.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  t.after(async()=>{try{await reopened.controller.close()}catch{}})
  assert.equal((await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(reopened.controller.draft.exercises[0].sets[0].weightKg,'52')
  assert.equal(reopened.controller.draft.completionRequested,true)
})

async function peerAddsContent(t, editLocal, editPeer) {
  const {restoreCloudRecordingV2ToLocalDraft}=await import('./cloudRecordingV2Restore.ts')
  const f=await setup({completed:false});t.after(async()=>{try{await f.editor.close()}catch{}})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  if(editLocal)await editLocal(f.editor)
  await f.editor.close()
  const values=['peer-entry-001','peer-record-001','peer-record-002'];const peerIds={nextId:()=>values.shift()}
  const other=await restoreCloudRecordingV2ToLocalDraft({sessionId:'42',lease,auth,draftLease:auth,api:f.api,
    storage:new SecureDraftStorage(nativePort(),'101',auth),ids:peerIds,now});assert.equal(other.ok,true)
  t.after(async()=>{try{await other.controller.close()}catch{}})
  await editPeer(other.controller)
  let n=0;assert.equal((await syncRecordingV2(other.controller,f.api,auth,()=>`peer-add-key-${++n}`)).state,'synced')
  await other.controller.close()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  t.after(async()=>{try{await reopened.controller.close()}catch{}})
  return {...f,editor:reopened.controller}
}

test('an untouched local draft receives a group added on another device',async t=>{
  const f=await peerAddsContent(t,null,async peer=>{assert.equal((await peer.addGroup('entry-0001')).ok,true)})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.editor.draft.exercises[0].sets.length,3)
  assert.equal(f.editor.draft.exercises[0].sets[2].recordKey,'peer-entry-001')
})

test('an untouched local draft receives a new action and completion from another device',async t=>{
  const f=await peerAddsContent(t,null,async peer=>{
    assert.equal((await peer.addCustomExercise('卧推','weight')).ok,true)
    assert.equal((await peer.requestV2Completion()).ok,true)
  })
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.editor.draft.exercises.length,2)
  assert.equal(f.editor.draft.exercises[1].identity.exerciseName,'卧推')
  assert.equal(f.editor.draft.completionRequested,true)
})

test('a locally removed group is never overwritten by another device adding a group',async t=>{
  const f=await peerAddsContent(t,async local=>{assert.equal((await local.removeGroup('entry-0001','record-001')).ok,true)},
    async peer=>{assert.equal((await peer.addGroup('entry-0001')).ok,true)})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(f.editor.draft.exercises[0].removedSets[0].set.recordKey,'record-001')
  assert.equal(f.service.state.sets.length,3)
})

async function conflictingRestoredGroup(t,legacy) {
  const f=await setup({completed:false});t.after(async()=>{try{await f.editor.close()}catch{}})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.editor.draft.syncTracking,1)
  assert.equal((await f.editor.removeGroup('entry-0001','record-001')).ok,true)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal((await f.editor.restoreGroup('entry-0001','record-001')).ok,true)
  if(!legacy)assert.equal(f.editor.draft.exercises[0].sets[0].dirty,true)
  await f.editor.close()
  if(legacy){
    const row=[...f.port.rows.values()][0],data=JSON.parse(row.value)
    data.schema=3;delete data.draft.syncTracking;data.draft.exercises[0].sets[0].dirty=false
    row.value=JSON.stringify(data);row.rev=String(Number(row.rev)+1)
  }
  const index=f.service.state.removedSets.findIndex(item=>item.set.recordKey==='record-001')
  assert.ok(index>=0);const [item]=f.service.state.removedSets.splice(index,1)
  f.service.state.sets.push({...item.set,measurement:{...item.set.measurement,weightKg:'52'}});f.service.advance()
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  t.after(async()=>{try{await reopened.controller.close()}catch{}})
  return {...f,editor:reopened.controller}
}

test('restoring a previously removed group is treated as a local edit',async t=>{
  const f=await conflictingRestoredGroup(t,false)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'45')
  assert.equal(f.service.state.sets.find(set=>set.recordKey==='record-001').measurement.weightKg,'52')
})

test('legacy schema 3 never assumes a restored group was untouched',async t=>{
  const f=await conflictingRestoredGroup(t,true)
  assert.equal(f.editor.draft.syncTracking,undefined)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'45')
  assert.equal(f.service.state.sets.find(set=>set.recordKey==='record-001').measurement.weightKg,'52')
})

test('a clean legacy schema 3 draft upgrades only after matching its current cloud version',async t=>{
  const f=await setup();t.after(async()=>{try{await f.editor.close()}catch{}})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced');await f.editor.close()
  const row=[...f.port.rows.values()][0],old=JSON.parse(row.value);old.schema=3;delete old.draft.syncTracking
  row.value=JSON.stringify(old);row.rev=String(Number(row.rev)+1)
  const reopened=await TrainingEditorController.attach('101',event,f.storage,now,auth,f.ids);assert.equal(reopened.ok,true)
  t.after(async()=>{try{await reopened.controller.close()}catch{}})
  assert.equal(reopened.controller.draft.syncTracking,undefined)
  assert.equal((await syncRecordingV2(reopened.controller,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(reopened.controller.draft.syncTracking,1)
  assert.equal(JSON.parse([...f.port.rows.values()][0].value).schema,4)
})

test('cloud additions cannot overwrite a local edit made while the snapshot is rechecked',async t=>{
  const f=await peerAddsContent(t,null,async peer=>{assert.equal((await peer.addGroup('entry-0001')).ok,true)})
  let reads=0,start,release;const started=new Promise(resolve=>{start=resolve}),gate=new Promise(resolve=>{release=resolve})
  const api=new RecordingApi('https://api.example.org',auth,async request=>{
    if(request.method==='GET'&&++reads===2){start();await gate}
    return f.service.request(request)
  },lease)
  const syncing=syncRecordingV2(f.editor,api,auth,f.nextKey);await started
  assert.equal((await f.editor.editGroup('entry-0001','record-001','weightKg','48')).ok,true);release()
  assert.equal((await syncing).state,'conflict')
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48')
  assert.equal(f.editor.draft.exercises[0].sets.length,2);assert.equal(f.service.state.sets.length,3)
})

test('a local group edit is preserved when another device adds a new action',async t=>{
  const f=await peerAddsContent(t,async local=>{assert.equal((await local.editGroup('entry-0001','record-001','weightKg','48')).ok,true)},
    async peer=>{assert.equal((await peer.addCustomExercise('卧推','weight')).ok,true)})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'conflict')
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48');assert.equal(f.editor.draft.exercises.length,1)
  assert.equal(f.service.state.entries.length,2)
})

test('conflict review keeps a cloud-added action and lets the user choose the edited group',async t=>{
  const {reviewRecordingV2Conflict,resolveRecordingV2Conflict}=await import('./recordingV2Conflict.ts')
  const f=await peerAddsContent(t,async local=>{assert.equal((await local.editGroup('entry-0001','record-001','weightKg','48')).ok,true)},
    async peer=>{assert.equal((await peer.editGroup('entry-0001','record-001','weightKg','52')).ok,true)
      assert.equal((await peer.addCustomExercise('卧推','weight')).ok,true)})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'conflict')
  const review=await reviewRecordingV2Conflict(f.editor,f.api,auth)
  assert.equal(review.ok,true);assert.equal(review.review.rows.length,1)
  assert.equal(review.review.hasCloudAdditions,true)
  assert.match(review.review.rows[0].local,/48 kg/);assert.match(review.review.rows[0].cloud,/52 kg/)
  assert.equal((await resolveRecordingV2Conflict(f.editor,f.api,auth,review.review,{[review.review.rows[0].key]:'local'})).ok,true)
  assert.equal(f.editor.draft.exercises.length,2);assert.equal(f.editor.draft.exercises[1].identity.exerciseName,'卧推')
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.entries.length,2);assert.equal(f.service.state.sets.find(set=>set.recordKey==='record-001').measurement.weightKg,'48')
})

test('conflict review retains an added cloud group when choosing the cloud value',async t=>{
  const {reviewRecordingV2Conflict,resolveRecordingV2Conflict}=await import('./recordingV2Conflict.ts')
  const f=await peerAddsContent(t,async local=>{assert.equal((await local.editGroup('entry-0001','record-001','weightKg','48')).ok,true)},
    async peer=>{assert.equal((await peer.editGroup('entry-0001','record-001','weightKg','52')).ok,true)
      assert.equal((await peer.addGroup('entry-0001')).ok,true)})
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'conflict')
  const review=await reviewRecordingV2Conflict(f.editor,f.api,auth);assert.equal(review.ok,true)
  assert.equal(review.review.hasCloudAdditions,true)
  assert.equal((await resolveRecordingV2Conflict(f.editor,f.api,auth,review.review,{[review.review.rows[0].key]:'cloud'})).ok,true)
  assert.equal(f.editor.draft.exercises[0].sets.length,3)
  assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'52')
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.sets.length,3)
})

test('cloud additions do not hide local structural edits or removed groups',async t=>{
  const {recordingV2ConflictRows}=await import('./recordingV2ConflictPlan.ts')
  const f=await peerAddsContent(t,null,async peer=>{assert.equal((await peer.addGroup('entry-0001')).ok,true)})
  const local=structuredClone(f.editor.draft),remote=structuredClone(f.service.state)
  local.exercises[0].sets[0].persisted=false
  assert.equal(recordingV2ConflictRows(local,remote),null)
  local.exercises[0].sets[0].persisted=true
  delete local.syncTracking
  assert.equal(recordingV2ConflictRows(local,remote),null)
  local.syncTracking=1
  const removed=local.exercises[0].sets.shift()
  local.exercises[0].removedSets=[{set:{...removed,dirty:true},removedPosition:1}]
  assert.equal(recordingV2ConflictRows(local,remote),null)
})

test('cloud additions are rechecked before a conflict choice is saved',async t=>{
  const {reviewRecordingV2Conflict,resolveRecordingV2Conflict}=await import('./recordingV2Conflict.ts')
  const f=await peerAddsContent(t,async local=>{assert.equal((await local.editGroup('entry-0001','record-001','weightKg','48')).ok,true)},
    async peer=>{assert.equal((await peer.addCustomExercise('卧推','weight')).ok,true)})
  const review=await reviewRecordingV2Conflict(f.editor,f.api,auth);assert.equal(review.ok,true)
  f.service.advance()
  const result=await resolveRecordingV2Conflict(f.editor,f.api,auth,review.review,{[review.review.rows[0].key]:'local'})
  assert.deepEqual(result,{ok:false,reason:'stale'})
  assert.equal(f.editor.draft.exercises.length,1);assert.equal(f.editor.draft.exercises[0].sets[0].weightKg,'48')
  assert.equal(f.service.state.entries.length,2)
})

test('batch groups survive offline completion and a lost response without duplicates',async t=>{
  const f=await setup({completed:false})
  t.after(async()=>{try{await f.editor.close()}catch{}})
  let number=2;f.ids.nextId=()=>`batch-record-${++number}`
  const snapshot=batchSetSnapshot(f.editor.draft.exercises[0])
  const applied=await f.editor.applyBatchGroups('entry-0001','record-001',['record-002'],['weightKg'],2,snapshot)
  assert.equal(applied.ok,true)
  assert.deepEqual(f.editor.draft.exercises[0].sets.map(set=>[set.weightKg,set.reps]),[['45','8'],['45',''],['45',''],['45','']])
  assert.equal((await f.editor.requestV2Completion()).ok,true)
  f.service.offline=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'pending')
  f.service.offline=false;f.service.loseSets=true
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'pending')
  assert.equal(f.service.state.sets.length,4)
  assert.equal((await syncRecordingV2(f.editor,f.api,auth,f.nextKey)).state,'synced')
  assert.equal(f.service.state.status,'completed')
  assert.deepEqual(f.service.state.sets.map(set=>[set.measurement.weightKg,set.measurement.reps]),[
    ['45',8],['45',undefined],['45',undefined],['45',undefined]])
})
