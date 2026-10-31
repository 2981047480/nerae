import assert from 'node:assert/strict'
import { test } from 'node:test'
import { AuthEntryController } from './authEntryController.ts'

const adultSession = (subjectId = '41', generation = 'session-1') => Object.freeze({
  status: 'authenticated', subjectId, generation,
  user: Object.freeze({ id: subjectId, nickname: '学员', roles: Object.freeze(['student']), ageVerified: true, ageDeclarationVersion: 2 }),
})
const underageUnconfirmedSession = (subjectId = '41', generation = 'session-1') => Object.freeze({
  status: 'authenticated', subjectId, generation,
  user: Object.freeze({ id: subjectId, nickname: '学员', roles: Object.freeze(['student']), ageVerified: false, ageDeclarationVersion: 1 }),
})

function harness({ restore = { status: 'signed_out', reason: 'missing' }, challenge, login, age } = {}) {
  let current = null
  let revision = 0
  let guardId = 0
  const watches = new Map()
  const calls = { restore: 0, requestCode: [], retryChallenge: 0, submitCode: [], retryLogin: 0, declare: [], reset: 0 }
  const session = {
    async restore() {
      calls.restore++
      revision++
      for (const callbacks of watches.values()) for (const callback of callbacks) callback()
      watches.clear()
      if (restore.status === 'authenticated') current = restore.session
      else if (restore.status === 'signed_out') current = null
      return restore
    },
    snapshot: () => current,
    isCurrent: (subject, generation) => current?.subjectId === subject && current?.generation === generation,
    captureInstallGuard() { return Object.freeze({ revision, id: ++guardId }) },
    subscribeInstallGuardInvalidation(guard, callback) {
      if (guard.revision !== revision) throw new Error('stale guard')
      const list = watches.get(guard) ?? new Set()
      list.add(callback)
      watches.set(guard, list)
      return () => { list.delete(callback); if (!list.size) watches.delete(guard) }
    },
    invalidate() {
      revision++
      const callbacks = [...watches.values()].flatMap(list => [...list])
      watches.clear()
      for (const callback of callbacks) callback()
    },
    setCurrent(value) { current = value },
  }
  const api = {
    async requestCode(email) { calls.requestCode.push(email); return challenge?.() ?? { status: 'waiting_code', expiresAt: '2026-10-31T12:00:00Z' } },
    async retryChallenge() { calls.retryChallenge++; return challenge?.() ?? { status: 'waiting_code', expiresAt: '2026-10-31T12:00:00Z' } },
    async submitCode(code) {
      calls.submitCode.push(code)
      const result = login?.() ?? { status: 'authenticated', session: adultSession() }
      if (result.status === 'authenticated') current = result.session
      return result
    },
    async retryLogin() {
      calls.retryLogin++
      const result = login?.() ?? { status: 'authenticated', session: adultSession() }
      if (result.status === 'authenticated') current = result.session
      return result
    },
    reset() { calls.reset++; return true },
  }
  const ageApi = {
    async declare(confirmed) {
      calls.declare.push(confirmed)
      const result = age?.() ?? { status: 'declared' }
      if (result.status === 'declared' || result.status === 'already_declared') {
        if (current) current = Object.freeze({ ...current, user: Object.freeze({ ...current.user, ageVerified: true, ageDeclarationVersion: 2 }) })
      }
      return result
    },
  }
  const runtime = { session, login: api, ageDeclaration: ageApi }
  const emitted = []
  const controller = new AuthEntryController(runtime, session => emitted.push(session))
  return { controller, runtime, session, calls, emitted }
}

test('restores and emits only the actual currently verified adult session', async () => {
  const existing = adultSession('51', 'restored-generation')
  const h = harness({ restore: { status: 'authenticated', session: existing } })
  await h.controller.start()
  assert.equal(h.controller.state.status, 'authenticated')
  assert.equal(h.emitted.length, 1)
  assert.equal(h.emitted[0], existing)
  assert.equal(JSON.stringify(h.emitted[0]).includes('token'), false)
  h.session.invalidate()
  assert.equal(h.controller.state.status, 'session_changed', 'logout must clear the success state immediately')
})

test('login alone does not emit; explicit adult confirmation emits only after authoritative state changes', async () => {
  const signedIn = underageUnconfirmedSession()
  const h = harness({ login: () => ({ status: 'authenticated', session: signedIn }) })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  assert.equal(h.controller.state.status, 'waiting_code')
  h.controller.setCode('123456')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'confirm_age')
  assert.equal(h.emitted.length, 0)
  assert.equal(h.calls.declare.length, 0)
  await h.controller.confirmAdult()
  assert.deepEqual(h.calls.declare, [true])
  assert.equal(h.controller.state.status, 'authenticated')
  assert.equal(h.emitted.length, 1)
  assert.equal(h.emitted[0].user.ageVerified, true)
  h.session.invalidate()
  assert.equal(h.controller.state.status, 'session_changed', 'the watcher must be renewed after the age API refreshes the lease')
})

test('uncertain challenge and login retries use the existing retry ports, not new requests', async () => {
  let challengeCalls = 0
  let loginCalls = 0
  const h = harness({
    challenge: () => ++challengeCalls === 1 ? { status: 'retry_challenge' } : { status: 'waiting_code', expiresAt: '2026-10-31T12:00:00Z' },
    login: () => ++loginCalls === 1 ? { status: 'retry_login' } : { status: 'authenticated', session: adultSession('62', 'new-generation') },
  })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  assert.equal(h.controller.state.status, 'retry_challenge')
  h.controller.setEmail('changed@example.test')
  assert.equal(h.controller.state.email, 'person@example.test', 'an uncertain challenge cannot be edited before exact retry')
  await h.controller.retryChallenge()
  assert.equal(h.controller.state.status, 'waiting_code')
  assert.equal(h.calls.requestCode.length, 1)
  assert.equal(h.calls.retryChallenge, 1)
  h.controller.setCode('654321')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'retry_login')
  h.controller.setCode('111111')
  assert.equal(h.controller.state.code, '', 'an uncertain login cannot be changed before exact retry')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'authenticated')
  assert.deepEqual(h.calls.submitCode, ['654321'])
  assert.equal(h.calls.retryLogin, 1)
  assert.equal(h.emitted.length, 1)
})

test('new-device approval remains pending and never emits an authenticated session', async () => {
  const h = harness({ login: () => ({ status: 'approval_required', comparisonCode: '4821', expiresAt: '2026-10-31T12:00:00Z' }) })
  await h.controller.start()
  h.controller.setEmail('new-device@example.test')
  await h.controller.requestCode()
  h.controller.setCode('654321')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'approval_required')
  assert.match(h.controller.state.message, /已登录设备/)
  assert.match(h.controller.state.message, /不会创建登录会话/)
  assert.equal(h.emitted.length, 0)
})

test('session invalidation clears email and code and cannot publish a late stale result', async () => {
  let finishLogin
  const pendingLogin = new Promise(resolve => { finishLogin = resolve })
  const h = harness({ login: () => pendingLogin })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  const submitting = h.controller.submitCode()
  h.session.invalidate()
  assert.equal(h.controller.state.email, '')
  assert.equal(h.controller.state.code, '')
  const stale = adultSession('41', 'stale-generation')
  h.session.setCurrent(null)
  finishLogin({ status: 'authenticated', session: stale })
  await submitting
  assert.equal(h.controller.state.status, 'session_changed')
  assert.equal(h.emitted.length, 0)
})

test('non-authoritative age outcomes never emit; adult status must be visible on current session', async () => {
  const h = harness({ login: () => ({ status: 'authenticated', session: underageUnconfirmedSession() }), age: () => ({ status: 'reconcile_required' }) })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  await h.controller.submitCode()
  await h.controller.confirmAdult()
  assert.equal(h.controller.state.status, 'confirm_age')
  assert.equal(h.controller.state.session.user.ageVerified, false)
  assert.equal(h.emitted.length, 0)
})
