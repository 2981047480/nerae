import test from 'node:test'
import assert from 'node:assert/strict'
import { RecordingApi } from './recordingApi.ts'
import { projectRecordingV2ToDraft } from './recordingV2Restore.ts'
import { prepareRecordingV2Batch } from './recordingV2Projection.ts'
const event='event-0001'
const auth={current:()=>({subjectId:'101',generation:'g-1',accessToken:'token'}),isCurrent:(id,g)=>id==='101'&&g==='g-1'}
const lease={subjectId:'101',generation:'g-1'}
const item=(key,index,measurement,confirmed=false)=>({recordKey:key,exerciseEntryKey:'entry-0001',setIndex:index,confirmed,measurement})
const detail=()=>({id:'42',version:5,source:'free',date:'2026-11-04',timeZone:'Asia/Shanghai',clientEventId:event,status:'in_progress',mode:'per_set',createdAt:'2026-11-04T00:00:00Z',updatedAt:'2026-11-04T01:00:00Z',entries:[{exerciseEntryKey:'entry-0001',source:'custom',exerciseName:'深蹲'}],sets:[item('record-001',1,{method:'total',weightKg:'0',reps:8}),item('record-003',3,{method:'total'})],removedSets:[{set:item('record-002',2,{method:'total',weightKg:'45'}),removedPosition:2}]})
const issued=async raw=>new RecordingApi('https://api.example.org',auth,async()=>({statusCode:200,data:raw}),lease).readRecordingV2('42')

test('restore preserves blank and partial groups, actual zero, mode, removed position and identities', async()=>{
  const snapshot=await issued(detail()); const restored=projectRecordingV2ToDraft(snapshot,auth,'2026-11-05')
  assert.equal(restored.ok,true); const draft=restored.draft
  assert.equal(draft.mode,'per_set'); assert.deepEqual(draft.server,{id:'42',version:5,status:'in_progress'})
  assert.deepEqual(draft.exercises[0].sets.map(s=>[s.recordKey,s.weightKg,s.reps,s.confirmed]),[['record-001','0','8',false],['record-003','','',false]])
  assert.equal(draft.exercises[0].removedSets[0].removedPosition,2)
  assert.equal(draft.exercises[0].removedSets[0].set.weightKg,'45')
  const batch=prepareRecordingV2Batch(draft,snapshot.detail)
  assert.equal(batch.ok,true); assert.equal(batch.changed,false)
})

test('restore rejects unissued, unsupported or wrong-account snapshots without changing data', async()=>{
  const snapshot=await issued(detail())
  assert.equal(projectRecordingV2ToDraft({...snapshot},auth,'2026-11-05').reason,'invalid_snapshot')
  assert.equal(projectRecordingV2ToDraft(snapshot,{...auth,isCurrent:()=>false},'2026-11-05').reason,'session_changed')
  const side=detail(); side.sets[0].measurement={method:'pair_each',weightKg:'12',reps:10}
  assert.equal(projectRecordingV2ToDraft(await issued(side),auth,'2026-11-05').reason,'unsupported')
  const removedEntry=detail(); removedEntry.entries[0].removedPosition=1
  assert.equal(projectRecordingV2ToDraft(await issued(removedEntry),auth,'2026-11-05').reason,'unsupported')
})

test('cloud restore writes only to an empty versioned slot and preserves an existing local draft', async()=>{
  const { SecureDraftStorage } = await import('./secureDraftStorage.ts')
  const { restoreCloudRecordingV2ToLocalDraft } = await import('./cloudRecordingV2Restore.ts')
  const values=new Map(), revisions=new Map()
  const native={readVersionedTrainingDraft(key){return values.has(key)?{ok:true,errorCode:'',found:true,value:values.get(key),revision:revisions.get(key)}:{ok:true,errorCode:'',found:false,value:'',revision:'0'}},
    compareAndSetVersionedTrainingDraft(key,rev,found,before,next){if(values.has(key)!==found||(revisions.get(key)||'0')!==rev||(values.get(key)||'')!==before)return{ok:false,errorCode:'conflict',found:false,value:'',revision:''};values.set(key,next);revisions.set(key,String(Number(rev)+1));return{ok:true,errorCode:'',found:false,value:'',revision:String(Number(rev)+1)}},
    discoverTrainingDrafts(){return {ok:true,errorCode:'',found:true,value:JSON.stringify({eventIds:[],nextCursor:null,hasUnclassifiedItems:false})}}}
  const draftLease={generation:'g-1',isCurrent:auth.isCurrent}
  const storage=new SecureDraftStorage(native,'101',draftLease)
  const api=new RecordingApi('https://api.example.org',auth,async()=>({statusCode:200,data:detail()}),lease)
  const options={sessionId:'42',lease,auth,draftLease,api,storage,ids:{nextId:()=> 'unused-id-01'},now:()=>new Date('2026-11-05T00:00:00Z')}
  const first=await restoreCloudRecordingV2ToLocalDraft(options)
  assert.equal(first.ok,true); assert.equal(first.controller.draft.exercises[0].sets[0].weightKg,'0')
  await first.controller.close()
  const key='nerae.workout.draft.v1.101.event-0001'; const before=values.get(key)
  const second=await restoreCloudRecordingV2ToLocalDraft(options)
  assert.equal(second.ok,false); assert.equal(second.reason,'local_exists'); assert.equal(values.get(key),before)
})
