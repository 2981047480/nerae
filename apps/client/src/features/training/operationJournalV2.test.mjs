import assert from 'node:assert/strict'
import { test } from 'node:test'
import { TrainingOperationJournal, normalizeRecordingV2Request } from './operationJournal.ts'

class Storage {
  values = new Map()
  marks = new Set()
  async read(key) { return this.values.get(key) }
  async createIfAbsent(key, value) { if (this.values.has(key)) return false; this.values.set(key, value); return true }
  async compareAndSwap(key, before, after) { if (this.values.get(key) !== before) return false; if (after === null) this.values.delete(key); else this.values.set(key, after); return true }
  async claimFirstSend(key, before, after) { if (this.marks.has(key)) return false; this.marks.add(key); return this.compareAndSwap(key, before, after) }
}
const auth = { current: () => ({ subjectId: '101', generation: 'session-1' }), isCurrent: (id, generation) => id === '101' && generation === 'session-1' }
const request = (path, body) => ({ method: 'POST', path, body: JSON.stringify(body), idempotencyKey: 'v2-key-0001' })
const journal = () => new TrainingOperationJournal(new Storage(), auth, { retentionMs: 86400000, now: () => 1798000000000 })
const create = { source: 'free', date: '2026-10-28', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001', mode: 'after_workout' }
const set = { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', setIndex: 1, confirmed: false, measurement: { method: 'total' } }
const batch = { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], addEntries: [{ exerciseEntryKey: 'entry-0001', exerciseName: '深蹲' }], sets: [set] }

test('v2 creation, partial/blank groups, remove and finish can be retained before first dispatch', async () => {
  for (const req of [request('/me/recordings', create), request('/me/recordings/42/sets', batch),
    request('/me/recordings/42/sets', { ...batch, removeRecords: [{ recordKey: 'record-001', removedPosition: 1 }] }),
    request('/me/recordings/42/finish', { expectedVersion: 2 })]) {
    const j = journal()
    const staged = await j.stage('event-0001', req)
    assert.equal(staged.state, 'ready')
    const permit = await j.beginSend('event-0001')
    let sent = 0
    const output = await j.dispatchFirst(permit, req, async (exact, lease, isCurrent) => {
      assert.deepEqual(exact, req); assert.equal(lease.subjectId, '101'); assert.equal(isCurrent(), true); sent++; return 'ok'
    })
    assert.equal(output, 'ok'); assert.equal(sent, 1)
    assert.equal((await j.readPending('event-0001')).state, 'uncertain')
  }
})

test('v2 journal rejects wrong event, unknown fields, invalid group, and unsafe paths', async () => {
  const bad = [request('/me/recordings', { ...create, clientEventId: 'another-001' }),
    request('/me/recordings', { ...create, extra: true }), request('/me/recordings/42/sets', { ...batch, sets: [{ ...set, measurement: { method: 'total', weightKg: -1 } }] }),
    request('/me/recordings/42/sets', { ...batch, exerciseEntryKeys: ['entry-0001', 'entry-0001'] }),
    request('/me/recordings/42/sets/replay', batch), request('/me/recordings/42/finish', { expectedVersion: 0 })]
  for (const req of bad) await assert.rejects(journal().stage('event-0001', req), error => error.code === 'invalid_operation')
})


test('versioned batch date accepts a real date and rejects malformed dates',()=>{
  for(const date of ['2026-02-28','2024-02-29']) {
    assert.doesNotThrow(()=>normalizeRecordingV2Request({method:'POST',path:'/me/recordings/42/sets',idempotencyKey:'request-0001',body:JSON.stringify({expectedVersion:1,date,exerciseEntryKeys:[]})},'event-0001'))
  }
  for(const date of ['2026-02-30','0999-12-31',null,42]) {
    assert.throws(()=>normalizeRecordingV2Request({method:'POST',path:'/me/recordings/42/sets',idempotencyKey:'request-0001',body:JSON.stringify({expectedVersion:1,date,exerciseEntryKeys:[]})},'event-0001'))
  }
})
