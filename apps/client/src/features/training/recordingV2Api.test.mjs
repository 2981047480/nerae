import test from 'node:test'
import assert from 'node:assert/strict'
import { RecordingApi, RecordingApiError, isIssuedRecordingV2Snapshot } from './recordingApi.ts'
import { TrainingOperationJournal } from './operationJournal.ts'

const event = 'event-0001'
const set = { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', setIndex: 1, confirmed: false, measurement: { method: 'total' } }
const detail = overrides => ({ id: '42', version: 1, source: 'free', date: '2026-11-04', timeZone: 'Asia/Shanghai', clientEventId: event,
  status: 'in_progress', mode: 'after_workout', createdAt: '2026-11-04T00:00:00Z', updatedAt: '2026-11-04T00:00:00Z',
  entries: [{ exerciseEntryKey: 'entry-0001', source: 'custom', exerciseName: '深蹲' }], sets: [structuredClone(set)], removedSets: [], ...overrides })
const credential = { subjectId: '101', generation: 'login-1', accessToken: 'token-not-logged' }
const auth = { current: () => credential, isCurrent: (id, generation) => id === '101' && generation === 'login-1' }
const lease = { subjectId: '101', generation: 'login-1' }
const makeApi = port => new RecordingApi('https://api.example.org', auth, port, lease)
const expiry = { 'Idempotency-Key-Expires-At': '2027-01-01T00:00:00Z' }
const code = expected => error => error instanceof RecordingApiError && error.code === expected
function journal() {
  const values = new Map(); const marks = new Set()
  const storage = { async read(key) { return values.get(key) }, async createIfAbsent(key, value) { if (values.has(key)) return false; values.set(key,value); return true },
    async compareAndSwap(key,before,after) { if (values.get(key) !== before) return false; if (after === null) values.delete(key); else values.set(key,after); return true },
    async claimFirstSend(key,before,after) { if (marks.has(key) || values.get(key) !== before) return false; marks.add(key); values.set(key,after); return true } }
  const journalAuth = { current: () => lease, isCurrent: auth.isCurrent }
  return new TrainingOperationJournal(storage, journalAuth, { now: () => 1000, retentionMs: 60000 })
}

test('v2 reads preserve blank groups and issue an immutable owner-scoped snapshot', async () => {
  let sent
  const source = detail()
  const api = makeApi(async request => { sent = request; return { statusCode: 200, data: source } })
  const got = await api.readRecordingV2('42')
  assert.equal(sent.url, 'https://api.example.org/me/recordings/42')
  assert.equal(sent.header.Authorization, 'Bearer token-not-logged')
  assert.deepEqual(got.detail.sets[0].measurement, { method: 'total' })
  assert.equal(isIssuedRecordingV2Snapshot(got), true)
  assert.equal(isIssuedRecordingV2Snapshot({ ...got }), false)
  assert.ok(Object.isFrozen(got.detail.sets[0].measurement))
  source.sets[0].measurement.weightKg = '999'
  assert.equal(got.detail.sets[0].measurement.weightKg, undefined)
})

test('v2 read rejects unsafe or internally inconsistent server snapshots', async () => {
  for (const raw of [detail({ id: '43' }), detail({ version: 9007199254740992 }), detail({ entries: [] }),
    detail({ sets: [{ ...set, confirmed: 1 }] }), detail({ sets: [set, set] }),
    detail({ status: 'completed' }), detail({ unknown: 1 }), detail({ date: '2026-02-30' }),
    detail({ removedSets: [{ set, removedPosition: 1 }] })]) {
    await assert.rejects(makeApi(async () => ({ statusCode: 200, data: raw })).readRecordingV2('42'), code('invalid_response'))
  }
})

test('v2 direct write uses an exact native-claimed journal request and remains a candidate', async () => {
  const j = journal()
  const req = { method: 'POST', path: '/me/recordings', idempotencyKey: 'idempotency-1', body: JSON.stringify({ source: 'free', date: '2026-11-04', timeZone: 'Asia/Shanghai', clientEventId: event, mode: 'after_workout' }) }
  await j.stage(event,req); const permit = await j.beginSend(event)
  let calls = 0
  const api = makeApi(async actual => { calls++; assert.equal(actual.data, req.body); assert.equal(actual.header['Idempotency-Key'], req.idempotencyKey); return { statusCode: 201, data: { id: '42', version: 1 }, headers: expiry } })
  const result = await api.dispatchFirstRecordingV2(j,permit,() => true)
  assert.deepEqual(result.receipt,{ id: '42',version: 1 }); assert.equal(result.lease.subjectId, '101')
  assert.equal(calls,1); assert.equal((await j.readPending(event)).state,'uncertain')
  await assert.rejects(api.dispatchFirstRecordingV2(j,permit,() => true))
  assert.equal(calls,1)
})

test('v2 blocks changed drafts before transport and retains ambiguous writes', async () => {
  for (const draftCurrent of [false,true]) {
    const j=journal(); const req={ method:'POST',path:'/me/recordings/42/sets',idempotencyKey:'idempotency-1',body:JSON.stringify({expectedVersion:1,exerciseEntryKeys:[]}) }
    await j.stage(event,req); const permit=await j.beginSend(event); let calls=0
    const api=makeApi(async () => { calls++; throw new Error('response lost') })
    await assert.rejects(api.dispatchFirstRecordingV2(j,permit,() => draftCurrent),code('reconcile_required'))
    assert.equal(calls,draftCurrent ? 1 : 0); assert.equal((await j.readPending(event)).state,'uncertain')
  }
})

test('v2 listing discovers new and legacy records for the current account', async () => {
  const items = [
    { id:'42', version:2, recordFormat:2, date:'2026-11-04', timeZone:'Asia/Shanghai', status:'completed' },
    { id:'41', version:5, recordFormat:1, date:'2026-11-03', timeZone:'Asia/Shanghai', status:'skipped' },
  ]
  let sent
  const api = makeApi(async request => { sent=request; return { statusCode:200, data:{ items, page:1, pageSize:20, total:2 } } })
  const page = await api.listRecordingsV2({ page:1, pageSize:20 })
  assert.equal(sent.url,'https://api.example.org/me/recordings?page=1&pageSize=20')
  assert.deepEqual(page.items,items); assert.equal(page.lease.subjectId,'101'); assert.ok(Object.isFrozen(page.items[0]))
})

test('v2 listing rejects bad pages and does not expose another login after an in-flight change', async () => {
  const badPages = [ {items:[],page:2,pageSize:20,total:0}, {items:[{id:'1',version:1,recordFormat:2,date:'2026-11-04',timeZone:'UTC',status:'skipped'}],page:1,pageSize:20,total:1}, {items:[],page:1,pageSize:20,total:-1} ]
  for (const value of badPages) await assert.rejects(makeApi(async () => ({ statusCode:200,data:value })).listRecordingsV2({page:1,pageSize:20}),code('invalid_response'))
  let generation='login-1'
  const changingAuth={current:()=>({...credential,generation}),isCurrent:(id,g)=>id==='101' && g===generation}
  const api=new RecordingApi('https://api.example.org',changingAuth,async()=>{generation='login-2';return {statusCode:200,data:{items:[],page:1,pageSize:20,total:0}}},lease)
  await assert.rejects(api.listRecordingsV2({page:1,pageSize:20}),code('session_changed'))
})

test('v2 replay uses only the retained request and leaves its journal for safe reconciliation', async () => {
  const j=journal(); const req={method:'POST',path:'/me/recordings/42/sets',idempotencyKey:'idempotency-1',body:JSON.stringify({expectedVersion:1,exerciseEntryKeys:[]})}
  await j.stage(event,req); const permit=await j.beginSend(event)
  await assert.rejects(j.dispatchFirst(permit,req,async()=>{throw new Error('lost')}))
  const headers={...expiry,'Idempotency-Replayed':'true','Content-Type':'application/json','Cache-Control':'no-store','X-Request-Id':'0123456789abcdef0123456789abcdef'}
  const api=makeApi(async request=>{assert.equal(request.url,'https://api.example.org/me/recordings/42/sets/replay');assert.equal(request.data,req.body);return {statusCode:200,data:{id:'42',version:2},headers}})
  const result=await api.replayPendingRecordingV2(j,event)
  assert.equal(result.state,'candidate'); assert.deepEqual(result.receipt,{id:'42',version:2})
  assert.equal((await j.readPending(event)).state,'uncertain')
})

test('v2 replay does not accept another record, impossible version, or absent proof headers', async () => {
  for (const response of [
    {statusCode:200,data:{id:'43',version:2},headers:{...expiry,'Idempotency-Replayed':'true','Content-Type':'application/json','Cache-Control':'no-store','X-Request-Id':'0123456789abcdef0123456789abcdef'}},
    {statusCode:200,data:{id:'42',version:3},headers:{...expiry,'Idempotency-Replayed':'true','Content-Type':'application/json','Cache-Control':'no-store','X-Request-Id':'0123456789abcdef0123456789abcdef'}},
    {statusCode:200,data:{id:'42',version:2},headers:expiry},
  ]) {
    const j=journal(); const req={method:'POST',path:'/me/recordings/42/sets',idempotencyKey:'idempotency-1',body:JSON.stringify({expectedVersion:1,exerciseEntryKeys:[]})}
    await j.stage(event,req); const permit=await j.beginSend(event); await assert.rejects(j.dispatchFirst(permit,req,async()=>{throw new Error('lost')}))
    assert.deepEqual(await makeApi(async()=>response).replayPendingRecordingV2(j,event),{state:'unresolved'})
    assert.equal((await j.readPending(event)).state,'uncertain')
  }
})

test('v2 read validates server-calculated volume counts against the visible groups', async()=>{
  const valid=detail(); valid.entries[0].volume={fullSets:0,partialSets:0,excludedSets:1}
  assert.equal((await makeApi(async()=>({statusCode:200,data:valid})).readRecordingV2('42')).detail.entries[0].volume.excludedSets,1)
  const badCount=detail(); badCount.entries[0].volume={fullSets:0,partialSets:0,excludedSets:2}
  const badValue=detail(); badValue.entries[0].volume={calculatedKg:'x',fullSets:1,partialSets:0,excludedSets:0}
  const missingValue=detail(); missingValue.entries[0].volume={fullSets:1,partialSets:0,excludedSets:0}
  for(const raw of [badCount,badValue,missingValue]) await assert.rejects(makeApi(async()=>({statusCode:200,data:raw})).readRecordingV2('42'),code('invalid_response'))
})


test('v2 reads one action total for repeated segments and rejects inconsistent group membership', async()=>{
  const first={...set,measurement:{method:'total',weightKg:'40',reps:8}}
  const second={...set,recordKey:'record-002',exerciseEntryKey:'entry-0002',measurement:{method:'total',weightKg:'50',reps:6}}
  const value=detail({entries:[{exerciseEntryKey:'entry-0001',source:'custom',exerciseName:'深蹲'},
    {exerciseEntryKey:'entry-0002',source:'custom',exerciseName:'深蹲'}],sets:[first,second],
    actionVolumes:[{exerciseEntryKeys:['entry-0001','entry-0002'],volume:{calculatedKg:'620.00',fullSets:2,partialSets:0,excludedSets:0}}]})
  const result=await makeApi(async()=>({statusCode:200,data:value})).readRecordingV2('42')
  assert.equal(result.detail.actionVolumes[0].volume.calculatedKg,'620.00')
  for(const broken of [
    {...value,actionVolumes:[{...value.actionVolumes[0],exerciseEntryKeys:['entry-0001','entry-0001']}]},
    {...value,actionVolumes:[{...value.actionVolumes[0],exerciseEntryKeys:['entry-0001']}]},
    {...value,actionVolumes:[{...value.actionVolumes[0],volume:{calculatedKg:'620.00',fullSets:3,partialSets:0,excludedSets:0}}]},
    {...value,entries:[value.entries[0],{...value.entries[1],exerciseName:'卧推'}]},
    {...value,actionVolumes:[{exerciseEntryKeys:['entry-0001'],volume:{calculatedKg:'320.00',fullSets:1,partialSets:0,excludedSets:0}},
      {exerciseEntryKeys:['entry-0002'],volume:{calculatedKg:'300.00',fullSets:1,partialSets:0,excludedSets:0}}]},
  ]) await assert.rejects(makeApi(async()=>({statusCode:200,data:broken})).readRecordingV2('42'),code('invalid_response'))
})
