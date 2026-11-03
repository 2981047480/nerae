import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { DeviceApprovalController } from './deviceApprovalController.ts'

const item = Object.freeze({ id: 'a'.repeat(24), comparisonCode: '123456', createdAt: '2026-11-03T00:00:00Z', expiresAt: '2026-11-03T00:05:00Z' })
const pending = Object.freeze({ decision: 'approve', comparisonCode: '123456' })
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

function fixture({ list, decide, retry, pendingDecision, stop } = {}) {
  let current = true
  const listeners = new Set()
  const calls = { list: 0, decide: [], retry: 0, stop: [] }
  const session = {
    current: () => current ? { subjectId: '101', generation: 'one', accessToken: 'synthetic-token' } : null,
    isCurrent: (subject, generation) => current && subject === '101' && generation === 'one',
    captureInstallGuard: () => ({}),
    subscribeInstallGuardInvalidation: (_guard, callback) => { listeners.add(callback); return () => listeners.delete(callback) },
    invalidate: () => { current = false; for (const callback of [...listeners]) callback(); listeners.clear() },
  }
  const api = {
    async list() { calls.list++; return list ? list() : { status: 'ready', items: [item] } },
    async decide(...args) { calls.decide.push(args); return decide ? decide(...args) : { status: 'approved' } },
    async retryDecision() { calls.retry++; return retry ? retry() : { status: 'approved' } },
    pendingDecision: () => pendingDecision ? pendingDecision() : pending,
    stopWaiting: value => { calls.stop.push(value); return stop ? stop(value) : value === true ? 'unresolved' : 'invalid_confirmation' },
  }
  return { controller: new DeviceApprovalController(session, api), session, calls }
}

test('user must explicitly confirm a listed comparison code before approval', async () => {
  const { controller, calls } = fixture()
  await controller.start()
  assert.equal(controller.state.status, 'ready')
  assert.equal(controller.state.items[0].comparisonCode, '123456')
  await controller.decide(item, 'approve', false)
  assert.equal(calls.decide.length, 0)
  await controller.decide(item, 'approve', true)
  assert.deepEqual(calls.decide[0], [item, 'approve', true])
  assert.equal(controller.state.status, 'done')
  assert.match(controller.state.message, /已允许/)
  assert.equal(controller.state.items.length, 0)
})

test('an unknown decision stays retryable and stopping explicitly reports an unresolved outcome', async () => {
  const { controller, calls } = fixture({ decide: () => ({ status: 'needs_review' }) })
  await controller.start()
  await controller.decide(item, 'approve', true)
  assert.equal(controller.state.status, 'retry')
  assert.deepEqual(controller.state.pending, pending)
  controller.stopWaiting(false)
  assert.equal(controller.state.status, 'retry')
  controller.stopWaiting(true)
  assert.equal(controller.state.status, 'unavailable')
  assert.match(controller.state.message, /仍未确认/)
  assert.match(controller.state.message, /可能稍后完成/)
  await controller.refresh()
  assert.equal(controller.state.status, 'ready')
  assert.deepEqual(calls.stop, [false, true])
})

test('reopening a panel preserves the original pending decision for explicit retry', async () => {
  const { controller, calls } = fixture({ list: () => ({ status: 'decision_pending' }) })
  await controller.start()
  assert.equal(controller.state.status, 'retry')
  assert.deepEqual(controller.state.pending, pending)
  await controller.retry()
  assert.equal(controller.state.status, 'done')
  assert.equal(calls.retry, 1)
})

test('logout or disposal clears visible codes and ignores a late response', async () => {
  const gate = deferred()
  const first = fixture({ list: () => gate.promise })
  const starting = first.controller.start()
  await new Promise(resolve => setImmediate(resolve))
  first.session.invalidate()
  gate.resolve({ status: 'ready', items: [item] })
  await starting
  assert.equal(first.controller.state.status, 'session_changed')
  assert.deepEqual(first.controller.state.items, [])

  const second = fixture()
  await second.controller.start()
  assert.equal(second.controller.state.items.length, 1)
  second.controller.dispose()
  assert.equal(second.controller.state.items.length, 0)
})

test('a list requiring reauthentication clears visible approval codes', async () => {
  const { controller } = fixture({ list: () => ({ status: 'not_authenticated' }) })
  await controller.start()
  assert.equal(controller.state.status, 'session_changed')
  assert.deepEqual(controller.state.items, [])
})

test('panel exposes explicit compare, allow, reject and unresolved recovery actions', () => {
  const source = readFileSync(new URL('./DeviceApprovalPanel.vue', import.meta.url), 'utf8')
  assert.match(source, /匹配一致，允许登录/)
  assert.match(source, /不是我，拒绝登录/)
  assert.match(source, /之前的操作仍可能完成/)
  assert.match(source, /controller\.stopWaiting\(true\)/)
})
