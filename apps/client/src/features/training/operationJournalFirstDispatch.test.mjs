import test from 'node:test'
import assert from 'node:assert/strict'
import { OperationJournalError, TrainingOperationJournal } from './operationJournal.ts'

const eventId = 'event-first-send-01'
const request = Object.freeze({ method: 'POST', path: '/me/sessions', idempotencyKey: 'first-send-key-01',
  body: JSON.stringify({ source: 'free', date: '2026-11-03', timeZone: 'Asia/Shanghai', clientEventId: eventId }) })
const code = expected => error => error instanceof OperationJournalError && error.code === expected

function fixture() {
  const rows = new Map()
  const claimed = new Set()
  let generation = 'session-1'
  let failTransition = false
  const auth = { current: () => ({ subjectId: '101', generation }),
    isCurrent: (subject, value) => subject === '101' && value === generation }
  const storage = {
    async read(key) { return rows.get(key) },
    async createIfAbsent(key, value) { if (rows.has(key)) return false; rows.set(key, value); return true },
    async compareAndSwap(key, expected, next) {
      if (failTransition) throw new Error('private native error')
      if (rows.get(key) !== expected) return false
      if (next === null) rows.delete(key); else rows.set(key, next)
      return true
    },
    async claimFirstSend(key, expected, next) {
      if (rows.get(key) !== expected || claimed.has(key)) return false
      claimed.add(key); rows.set(key, next); return true
    },
  }
  const journal = new TrainingOperationJournal(storage, auth, { retentionMs: 60_000, now: () => 1_000 })
  return { journal, rows, storage, auth, changeSession() { generation = 'session-2' },
    failTransition() { failTransition = true } }
}

test('the claimed original request is dispatched once and stays uncertain after a response', async () => {
  const { journal } = fixture()
  await journal.stage(eventId, request)
  const permit = await journal.beginSend(eventId)
  let calls = 0
  const result = await journal.dispatchFirst(permit, request, async exact => {
    calls++
    assert.deepEqual(exact, request)
    assert.equal(Object.isFrozen(exact), true)
    assert.equal(permit.isCurrent(), false)
    await assert.rejects(journal.dispatchFirst(permit, request, async () => 'duplicate'), code('conflict'))
    return 'candidate'
  })
  assert.equal(result, 'candidate')
  assert.equal(calls, 1)
  assert.equal((await journal.readPending(eventId)).state, 'uncertain')
  await assert.rejects(journal.dispatchFirst(permit, request, async () => 'duplicate'), code('conflict'))
  await assert.rejects(journal.beginSend(eventId), code('reconciliation_required'))
})

test('a forged, changed, or different-owner permit never invokes the transport', async () => {
  const first = fixture()
  const other = new TrainingOperationJournal(first.storage, first.auth, { retentionMs: 60_000, now: () => 1_000 })
  await first.journal.stage(eventId, request)
  const permit = await first.journal.beginSend(eventId)
  let calls = 0
  const send = async () => { calls++; return 'unexpected' }
  await assert.rejects(first.journal.dispatchFirst({ ...permit }, request, send), code('conflict'))
  await assert.rejects(other.dispatchFirst(permit, request, send), code('conflict'))
  await assert.rejects(first.journal.dispatchFirst(permit, { ...request, idempotencyKey: 'another-key-01' }, send), code('conflict'))
  assert.equal(calls, 0)
  await first.journal.markUncertain(permit)
})

test('an altered persisted operation or changed login blocks the callback', async () => {
  for (const mode of ['stored', 'session']) {
    const f = fixture()
    await f.journal.stage(eventId, request)
    const permit = await f.journal.beginSend(eventId)
    if (mode === 'stored') f.rows.set([...f.rows.keys()][0], '{}')
    else f.changeSession()
    let calls = 0
    await assert.rejects(f.journal.dispatchFirst(permit, request, async () => { calls++; return 'unexpected' }))
    assert.equal(calls, 0)
  }
})

test('a lost response keeps the original request for read-only reconciliation', async () => {
  const { journal } = fixture()
  await journal.stage(eventId, request)
  const permit = await journal.beginSend(eventId)
  await assert.rejects(journal.dispatchFirst(permit, request, async () => { throw new Error('response lost') }), /response lost/)
  const pending = await journal.readPending(eventId)
  assert.equal(pending.state, 'uncertain')
  assert.deepEqual(pending.request, request)
  await assert.rejects(journal.beginSend(eventId), code('reconciliation_required'))
})

test('failure to record uncertainty does not release a transport response as settled', async () => {
  const f = fixture()
  await f.journal.stage(eventId, request)
  const permit = await f.journal.beginSend(eventId)
  f.failTransition()
  await assert.rejects(f.journal.dispatchFirst(permit, request, async () => 'candidate'), code('unavailable'))
  assert.equal((await f.journal.readPending(eventId)).state, 'in_flight')
  assert.equal(permit.isCurrent(), false)
})
