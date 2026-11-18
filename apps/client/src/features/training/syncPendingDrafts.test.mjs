import test from 'node:test'
import assert from 'node:assert/strict'
import { syncPendingDrafts } from './syncPendingDrafts.ts'

const credential={subjectId:'101',generation:'g-1',accessToken:'synthetic-token'}
function fixture(){
  let active=true;const closed=[];const sent=[]
  const auth={current:()=>active?credential:null,isCurrent:(id,g)=>active&&id==='101'&&g==='g-1'}
  const controller=id=>({draft:{recordFormat:2,server:{id:'42'},exercises:[]},close:async()=>closed.push(id)})
  return {auth,closed,sent,controller,invalidate(){active=false}}
}

test('foreground recovery skips an open editor, syncs other drafts and closes its own handles',async()=>{
  const f=fixture()
  const result=await syncPendingDrafts({auth:f.auth,discover:async()=>({ok:true,eventIds:['open','saved'],nextCursor:null,hasUnclassifiedItems:false}),
    attach:async id=>id==='open'?{ok:false,reason:'already_open'}:{ok:true,controller:f.controller(id)},
    synchronize:async c=>{f.sent.push(c);return{state:'synced'}}})
  assert.equal(result.state,'pending');assert.equal(f.sent.length,1);assert.deepEqual(f.closed,['saved'])
})

test('account change aborts a foreground pass before visiting another draft',async()=>{
  const f=fixture();const attached=[]
  const result=await syncPendingDrafts({auth:f.auth,discover:async()=>({ok:true,eventIds:['first','second'],nextCursor:null,hasUnclassifiedItems:false}),
    attach:async id=>{attached.push(id);return{ok:true,controller:f.controller(id)}},
    synchronize:async()=>{f.invalidate();return{state:'synced'}}})
  assert.equal(result.state,'session_changed');assert.deepEqual(attached,['first']);assert.deepEqual(f.closed,['first'])
})

test('conflicts are reported without clearing or replacing the protected local draft',async()=>{
  const f=fixture(), original=f.controller('conflict')
  original.draft.exercises=[{sets:[{weightKg:'45'}]}]
  const result=await syncPendingDrafts({auth:f.auth,discover:async()=>({ok:true,eventIds:['conflict'],nextCursor:null,hasUnclassifiedItems:false}),
    attach:async()=>({ok:true,controller:original}),synchronize:async()=>({state:'conflict'})})
  assert.deepEqual(result,{state:'attention',conflicts:1});assert.equal(original.draft.exercises[0].sets[0].weightKg,'45')
  assert.deepEqual(f.closed,['conflict'])
})

test('invalid pagination cannot loop or falsely report a complete sync',async()=>{
  const f=fixture();let reads=0
  const result=await syncPendingDrafts({auth:f.auth,discover:async()=>{reads++;return{ok:true,eventIds:[],nextCursor:'same-cursor',hasUnclassifiedItems:false}},
    attach:async()=>{throw Error('not called')},synchronize:async()=>({state:'synced'})})
  assert.equal(result.state,'attention');assert.equal(reads,2)
})

test('a legacy draft with content is reported for attention rather than silently counted as synced',async()=>{
  const f=fixture();const original=f.controller('legacy');delete original.draft.recordFormat
  const result=await syncPendingDrafts({auth:f.auth,discover:async()=>({ok:true,eventIds:['legacy'],nextCursor:null,hasUnclassifiedItems:false}),
    attach:async()=>({ok:true,controller:original}),synchronize:async()=>{throw Error('legacy writes must not be sent')}})
  assert.equal(result.state,'attention');assert.deepEqual(f.closed,['legacy'])
})
