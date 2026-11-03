import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { AuthApiError } from '../../api/auth/emailAuthApi.ts'
import { EmailAuthApi } from '../../api/auth/emailAuthApi.ts'
import { EmailLoginFlow } from './emailLoginFlow.ts'
import { AuthEntryController } from './authEntryController.ts'
import { AuthSessionCoordinator } from './sessionCoordinator.ts'

const adultSession = (subjectId = '41', generation = 'session-1') => Object.freeze({
  status: 'authenticated', subjectId, generation,
  user: Object.freeze({ id: subjectId, nickname: '学员', roles: Object.freeze(['student']), ageVerified: true, ageDeclarationVersion: 2 }),
})
const underageUnconfirmedSession = (subjectId = '41', generation = 'session-1') => Object.freeze({
  status: 'authenticated', subjectId, generation,
  user: Object.freeze({ id: subjectId, nickname: '学员', roles: Object.freeze(['student']), ageVerified: false, ageDeclarationVersion: 1 }),
})

function deferred() {
  let resolve
  let reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function harness({ restore = { status: 'signed_out', reason: 'missing' }, challenge, login, approvalCheck, age } = {}) {
  let current = null
  let revision = 0
  let guardId = 0
  const watches = new Map()
  const events = []
  const calls = { restore: 0, requestCode: [], retryChallenge: [], submitCode: [], retryLogin: [], declare: [], reset: 0,
    openAttempt: 0, cancelAttempt: [], releaseAttempt: [], checkApproval: [] }
  const activeHandles = new Set()
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
    isInstallGuardCurrent(guard) { return guard?.revision === revision },
    async cancelInstallGuard(guard) {
      if (guard?.revision !== revision) return 'superseded'
      revision++
      const callbacks = [...(watches.get(guard) ?? [])]
      watches.delete(guard)
      for (const callback of callbacks) callback()
      events.push('cancel-install-guard')
      return 'cancelled'
    },
    async installAuthenticatedIfCurrent(result, guard) {
      if (guard?.revision !== revision) return { status: 'superseded' }
      const session = Object.freeze({ status: 'authenticated', subjectId: result.user.id,
        generation: `flow-session-${++guardId}`, user: result.user })
      current = session
      return { status: 'authenticated', session }
    },
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
    openAttempt() { calls.openAttempt++; const handle = Object.freeze({}); activeHandles.add(handle); return handle },
    async cancelAttempt(handle) {
      calls.cancelAttempt.push(handle)
      events.push('cancel-attempt')
      if (!activeHandles.has(handle)) return 'superseded'
      activeHandles.delete(handle)
      return 'cancelled'
    },
    releaseAttempt(handle) {
      calls.releaseAttempt.push(handle)
      events.push('release-attempt')
      if (!activeHandles.has(handle)) return false
      activeHandles.delete(handle)
      return true
    },
    async requestCode(email, handle) { calls.requestCode.push({ email, handle }); return challenge?.() ?? { status: 'waiting_code', expiresAt: '2026-10-31T12:00:00Z' } },
    async retryChallenge(handle) { calls.retryChallenge.push(handle); return challenge?.() ?? { status: 'waiting_code', expiresAt: '2026-10-31T12:00:00Z' } },
    async submitCode(code, handle) {
      calls.submitCode.push({ code, handle })
      const result = login?.() ?? { status: 'authenticated', session: adultSession() }
      if (result.status === 'authenticated') current = result.session
      return result
    },
    async retryLogin(handle) {
      calls.retryLogin.push(handle)
      const result = login?.() ?? { status: 'authenticated', session: adultSession() }
      if (result.status === 'authenticated') current = result.session
      return result
    },
    async checkApproval(handle) {
      calls.checkApproval.push(handle)
      const result = approvalCheck?.() ?? { status: 'approval_pending' }
      if (result.status === 'authenticated') current = result.session
      return result
    },
    reset() { calls.reset++; return true },
  }
  const ageApi = {
    async declare(confirmed) {
      calls.declare.push(confirmed)
      events.push('declare-age')
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
  return { controller, runtime, session, calls, events, emitted, watchCount: () => watches.size }
}

function realEmailHarness({ challengeReply, challengeStatus, loginReply, loginStatus, repository, ageDeclaration } = {}) {
  let stored = ''
  const vault = repository ?? {
    readAuthenticationState() { return { ok: true, errorCode: '', found: stored !== '', value: stored } },
    writeAuthenticationState(value) { stored = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState() { stored = ''; return { ok: true, errorCode: '', found: false, value: '' } },
    get value() { return stored },
  }
  const session = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  const requests = []
  const api = new EmailAuthApi('https://auth.example.test', async request => {
    requests.push(request)
    const isChallenge = request.url.endsWith('/auth/email/challenges')
    const status = isChallenge
      ? (typeof challengeStatus === 'function' ? challengeStatus() : challengeStatus ?? 202)
      : (typeof loginStatus === 'function' ? loginStatus() : loginStatus ?? 200)
    const requestId = 'a'.repeat(32)
    return {
      status,
      headers: { 'cache-control': 'no-store', 'content-type': 'application/json',
        ...(status === 401 ? { 'x-request-id': requestId } : {}) },
      body: status === 401 ? { code: 'UNAUTHENTICATED', message: '验证未通过', requestId } : isChallenge
        ? await (typeof challengeReply === 'function' ? challengeReply() : challengeReply) ??
          { challengeId: 'c'.repeat(24), nonce: 'N'.repeat(43), expiresAt: '2026-11-01T00:00:00Z' }
        : await (typeof loginReply === 'function' ? loginReply() : loginReply) ?? {
          status: 'authenticated', token: 'synthetic-token', expiresIn: 300,
          user: { id: '73', nickname: '学员', roles: ['student'], ageVerified: true, ageDeclarationVersion: 1 },
        },
    }
  })
  const key = { kty: 'EC', crv: 'P-256', x: 'A'.repeat(43), y: 'Q'.repeat(43) }
  const login = new EmailLoginFlow(api,
    { async initializeFirstForEmail() { return { status: 'ready', identity: { publicKeyForProtocol: key } } } },
    session, () => 'A'.repeat(43), () => ({ forSubmission: () => `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }))
  const emitted = []
  const controller = new AuthEntryController({ session, login,
    ageDeclaration: ageDeclaration ?? { async declare() { return { status: 'declared' } } } },
  value => emitted.push(value))
  return { controller, session, login, api, vault, emitted, requests }
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
  assert.equal(h.events.includes('release-attempt'), true)
  assert.equal(h.calls.declare.length, 0)
  await h.controller.confirmAdult()
  assert.deepEqual(h.calls.declare, [true])
  assert.equal(h.controller.state.status, 'authenticated')
  assert.equal(h.emitted.length, 1)
  assert.equal(h.emitted[0].user.ageVerified, true)
  assert.ok(h.events.indexOf('release-attempt') < h.events.indexOf('declare-age'), 'release the login scope before age declaration')
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
  assert.equal(h.calls.retryChallenge.length, 1)
  assert.equal(h.calls.requestCode[0].handle, h.calls.retryChallenge[0], 'challenge retries retain the same attempt handle')
  h.controller.setCode('654321')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'retry_login')
  h.controller.setCode('111111')
  assert.equal(h.controller.state.code, '', 'an uncertain login cannot be changed before exact retry')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'authenticated')
  assert.deepEqual(h.calls.submitCode.map(call => call.code), ['654321'])
  assert.equal(h.calls.retryLogin.length, 1)
  assert.equal(h.calls.submitCode[0].handle, h.calls.retryLogin[0], 'login retries retain the same attempt handle')
  assert.equal(h.calls.releaseAttempt.length, 1, 'release the attempt before publishing authenticated state')
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
  assert.equal(h.controller.state.comparisonCode, '4821')
  assert.match(h.controller.state.message, /已登录设备/)
  assert.match(h.controller.state.message, /回来检查/)
  assert.equal(h.emitted.length, 0)
})

test('approval check keeps the comparison code while pending and accepts only an authenticated result', async () => {
  let checks = 0
  const h = harness({ login: () => ({ status: 'approval_required', comparisonCode: '4821', expiresAt: '2026-10-31T12:00:00Z' }),
    approvalCheck: () => ++checks === 1 ? { status: 'approval_pending' } : { status: 'authenticated', session: adultSession() } })
  await h.controller.start()
  h.controller.setEmail('new-device@example.test')
  await h.controller.requestCode()
  h.controller.setCode('012345')
  await h.controller.submitCode()
  await h.controller.checkApproval()
  assert.equal(h.controller.state.status, 'approval_required')
  assert.equal(h.controller.state.comparisonCode, '4821')
  assert.match(h.controller.state.message, /尚未收到确认/)
  assert.equal(h.emitted.length, 0)
  await h.controller.checkApproval()
  assert.equal(h.controller.state.status, 'authenticated')
  assert.equal(h.emitted.length, 1)
  assert.equal(h.calls.checkApproval.length, 2)
  assert.equal(h.calls.releaseAttempt.length, 1)
})

test('real approval exchange restores the existing age-confirmation gate', async () => {
  let vaultValue = ''
  const session = new AuthSessionCoordinator({
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: !!vaultValue, value: vaultValue }),
    writeAuthenticationState: value => { vaultValue = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState: () => { vaultValue = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }, async () => ({ status: 'unavailable' }))
  const api = new EmailAuthApi('https://auth.example.test', async request => ({
    status: request.url.endsWith('/auth/email/challenges') ? 202 : 200,
    headers: { 'cache-control': 'no-store' },
    body: request.url.endsWith('/auth/email/challenges')
      ? { challengeId: 'c'.repeat(24), nonce: 'N'.repeat(43), expiresAt: '2026-11-01T00:00:00Z' }
      : request.url.endsWith('/auth/email-login')
        ? { status: 'approval_required', approvalId: 'a'.repeat(24), approvalSecret: 'S'.repeat(43), nonce: 'N'.repeat(43), comparisonCode: '123456', expiresAt: '2026-11-01T00:00:00Z' }
        : { status: 'authenticated', token: 'synthetic-token', expiresIn: 300,
            user: { id: '73', nickname: '', roles: ['student'], ageVerified: false, ageDeclarationVersion: 1 } },
  }))
  const publicKey = { kty: 'EC', crv: 'P-256', x: 'A'.repeat(43), y: 'Q'.repeat(43) }
  const proof = () => ({ forSubmission: () => `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` })
  const login = new EmailLoginFlow(api, { async initializeFirstForEmail() { return { status: 'ready', identity: { publicKeyForProtocol: publicKey } } } },
    session, () => 'A'.repeat(43), proof, proof)
  const emitted = []
  const controller = new AuthEntryController({ session, login, ageDeclaration: { async declare() { return { status: 'declared' } } } }, value => emitted.push(value))
  await controller.start()
  controller.setEmail('member@example.test')
  await controller.requestCode()
  controller.setCode('012345')
  await controller.submitCode()
  assert.equal(controller.state.status, 'approval_required')
  await controller.checkApproval()
  assert.equal(controller.state.status, 'confirm_age')
  assert.equal(session.snapshot().subjectId, '73')
  assert.equal(emitted.length, 0)
  assert.equal(JSON.parse(vaultValue).accessToken, 'synthetic-token')
})

test('actual EmailLoginFlow keeps rate-limited challenge and login retries on their original request', async () => {
  const h = harness()
  const challengeRequests = []
  const loginRequests = []
  let challengeCount = 0
  let loginCount = 0
  const api = {
    async requestChallenge(input, key) {
      challengeRequests.push({ input, key })
      if (++challengeCount === 1) throw new AuthApiError('rate_limited', 429)
      return { challengeId: 'challenge-73', expiresAt: '2026-10-31T12:00:00Z' }
    },
    async login(input, key) {
      loginRequests.push({ input, key })
      if (++loginCount === 1) throw new AuthApiError('rate_limited', 429)
      return { status: 'authenticated', expiresIn: 300,
        user: { id: '73', nickname: '学员', roles: ['student'], ageVerified: true, ageDeclarationVersion: 1 } }
    },
  }
  const identity = { publicKeyForProtocol: { kty: 'EC', crv: 'P-256', x: 'x'.repeat(43), y: 'y'.repeat(43) } }
  h.runtime.login = new EmailLoginFlow(api, {
    async initializeFirstForEmail() { return { status: 'ready', identity } },
  }, h.session, () => 'A'.repeat(43), () => ({ forSubmission: () => `${'A'.repeat(12)}.${'B'.repeat(12)}.${'C'.repeat(12)}` }))

  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  assert.equal(h.controller.state.status, 'retry_challenge')
  assert.match(h.controller.state.message, /避免重复发送/)
  h.controller.setEmail('changed@example.test')
  assert.equal(h.controller.state.email, 'person@example.test')
  await h.controller.requestCode()
  assert.equal(h.controller.state.status, 'waiting_code')
  assert.deepEqual(challengeRequests[1], challengeRequests[0], 'retry must preserve exact challenge body and key')

  h.controller.setCode('123456')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'retry_login')
  assert.match(h.controller.state.message, /不会再次发送验证码邮件/)
  h.controller.setCode('654321')
  assert.equal(h.controller.state.code, '', 'a rate-limited login cannot be changed to a new OTP')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'authenticated')
  assert.deepEqual(loginRequests[1], loginRequests[0], 'retry must preserve exact login body, OTP, proof, and key')
  assert.equal(h.emitted.length, 1)
})

test('dispose drops controller inputs and watcher and suppresses late completion without resetting shared login state', async () => {
  let finishChallenge
  const pending = new Promise(resolve => { finishChallenge = resolve })
  const h = harness({ challenge: () => pending })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  let notifications = 0
  h.controller.subscribe(() => { notifications++ })
  const request = h.controller.requestCode()
  assert.equal(h.watchCount(), 1)
  await h.controller.requestCode()
  assert.equal(h.calls.requestCode.length, 1, 'repeated clicks cannot start a second challenge while the first is in flight')
  const beforeDispose = notifications
  h.controller.dispose()
  assert.equal(h.watchCount(), 0)
  assert.equal(h.controller.state.email, '')
  assert.equal(h.controller.state.code, '')
  assert.equal(h.calls.reset, 0, 'do not destroy the flow while its exact request is unresolved')
  h.controller.setEmail('other@example.test')
  h.controller.setCode('999999')
  assert.equal(h.controller.state.email, '')
  assert.equal(h.controller.state.code, '')
  finishChallenge({ status: 'waiting_code', expiresAt: '2026-10-31T12:00:00Z' })
  await request
  assert.equal(notifications, beforeDispose, 'disposed UI listeners receive no late results')
  assert.equal(h.controller.state.email, '')
  assert.equal(h.controller.state.status, 'email')
  assert.equal(h.calls.reset, 0, 'disposing one controller must never reset a shared flow without its handle')
  await h.controller.start()
  assert.equal(h.calls.restore, 1, 'disposed controllers cannot restart')
})

test('real coordinator install failure preserves retry inputs and re-arms the UI session watcher', async () => {
  let vaultValue = ''
  let failFirstWrite = true
  let credentialWriteCalls = 0
  const success = { ok: true, errorCode: '', found: false, value: '' }
  const repository = {
    readAuthenticationState() { return { ok: true, errorCode: '', found: vaultValue !== '', value: vaultValue } },
    writeAuthenticationState(value) {
      if (value.includes('"accessToken"')) {
        credentialWriteCalls++
        if (failFirstWrite) { failFirstWrite = false; return { ok: false, errorCode: 'storage', found: false, value: '' } }
      }
      vaultValue = value
      return success
    },
    clearAuthenticationState() { vaultValue = ''; return success },
  }
  const session = new AuthSessionCoordinator(repository, async () => ({ status: 'unavailable' }))
  const api = new EmailAuthApi('https://auth.example.test', async request => ({
    status: request.url.endsWith('/auth/email/challenges') ? 202 : 200,
    headers: { 'cache-control': 'no-store', 'content-type': 'application/json' },
    body: request.url.endsWith('/auth/email/challenges')
      ? { challengeId: 'c'.repeat(24), nonce: 'N'.repeat(43), expiresAt: '2026-11-01T00:00:00Z' }
      : { status: 'authenticated', token: 'synthetic-token', expiresIn: 300,
          user: { id: '73', nickname: '学员', roles: ['student'], ageVerified: true, ageDeclarationVersion: 1 } },
  }))
  const key = { kty: 'EC', crv: 'P-256', x: 'A'.repeat(43), y: 'Q'.repeat(43) }
  const login = new EmailLoginFlow(api,
    { async initializeFirstForEmail() { return { status: 'ready', identity: { publicKeyForProtocol: key } } } },
    session, () => 'A'.repeat(43), () => ({ forSubmission: () => `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }))
  const controller = new AuthEntryController({ session, login, ageDeclaration: { async declare() { return { status: 'declared' } } } })
  await controller.start()
  controller.setEmail('person@example.test')
  await controller.requestCode()
  controller.setCode('123456')
  await controller.submitCode()
  assert.equal(credentialWriteCalls, 1)
  assert.equal(controller.state.status, 'retry_login')
  assert.equal(controller.state.email, 'person@example.test', 'the controller must not lose retry context during its own install guard invalidation')
  assert.equal(session.snapshot(), null)
  assert.equal(vaultValue, '')
  await session.logout()
  assert.equal(controller.state.status, 'session_changed', 'a fresh UI watcher observes logout after the failed guarded install')
  assert.equal(controller.state.email, '')
})

test('cancel invalidates this attempt before a delayed authenticated HTTP response can install', async () => {
  const loginStarted = deferred()
  const loginResponse = deferred()
  const h = realEmailHarness({ loginReply: () => { loginStarted.resolve(); return loginResponse.promise } })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  const submit = h.controller.submitCode()
  await loginStarted.promise
  const cancelled = await h.controller.cancel()
  assert.deepEqual(cancelled, { outcome: 'cancelled', leave: true })
  assert.equal(h.session.snapshot(), null)
  loginResponse.resolve({ status: 'authenticated', token: 'late-synthetic-token', expiresIn: 300,
    user: { id: '73', nickname: '学员', roles: ['student'], ageVerified: true, ageDeclarationVersion: 1 } })
  await submit
  await h.controller.dispose()
  assert.equal(h.session.snapshot(), null)
  assert.equal(h.vault.value, '')
  assert.deepEqual(h.emitted, [])
})

test('cancel during native vault install waits for cleanup and cannot emit the session', async () => {
  const writeStarted = deferred()
  const allowWrite = deferred()
  let value = ''
  const repository = {
    readAuthenticationState() { return { ok: true, errorCode: '', found: value !== '', value } },
    async writeAuthenticationState(next) {
      if (next.includes('accessToken')) {
        writeStarted.resolve()
        await allowWrite.promise
      }
      value = next
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    clearAuthenticationState() { value = ''; return { ok: true, errorCode: '', found: false, value: '' } },
    get value() { return value },
  }
  const h = realEmailHarness({ repository })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  const submit = h.controller.submitCode()
  await writeStarted.promise
  const cancelling = h.controller.cancel()
  allowWrite.resolve()
  assert.deepEqual(await cancelling, { outcome: 'cancelled', leave: true })
  await submit
  await h.controller.dispose()
  assert.equal(h.vault.value, '')
  assert.equal(h.session.snapshot(), null)
  assert.deepEqual(h.emitted, [])
})

test('double vault cleanup failure stays visible and retries cancellation with the same flow handle', async () => {
  let value = ''
  let failedClears = 2
  const repository = {
    readAuthenticationState() { return { ok: true, errorCode: '', found: value !== '', value } },
    writeAuthenticationState(next) {
      if (failedClears > 0 && next === '{"schema":1,"signedOut":true}') {
        return { ok: false, errorCode: 'unavailable', found: false, value: '' }
      }
      value = next
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    clearAuthenticationState() {
      if (failedClears > 0) { failedClears--; return { ok: false, errorCode: 'unavailable', found: false, value: '' } }
      value = ''
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    get value() { return value },
  }
  const h = realEmailHarness({ repository })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'retry_login')
  assert.equal(h.login.openAttempt(), null, 'the flow keeps the unresolved attempt reserved')
  const failed = await h.controller.cancel()
  assert.deepEqual(failed, { outcome: 'unavailable', leave: false })
  assert.equal(h.controller.state.status, 'cancel_failed')
  assert.match(h.controller.state.message, /尚未返回/)
  assert.equal(h.session.snapshot(), null)
  assert.equal(h.login.openAttempt(), null)
  failedClears = 0
  const retried = await h.controller.cancel()
  assert.deepEqual(retried, { outcome: 'cancelled', leave: true })
  const next = h.login.openAttempt()
  assert.ok(next, 'only the same-handle cleanup completion releases the single-flow slot')
  assert.equal(await h.login.cancelAttempt(next), 'cancelled')
  await h.controller.dispose()
  assert.deepEqual(h.emitted, [])
})

test('an older component attempt cannot cancel a later account installation', async () => {
  const h = realEmailHarness()
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  const otherApi = new EmailAuthApi('https://other.example.test', async () => ({
    status: 200, headers: { 'cache-control': 'no-store', 'content-type': 'application/json' },
    body: { status: 'authenticated', token: 'later-account-token', expiresIn: 300,
      user: { id: '88', nickname: '另一账号', roles: ['student'], ageVerified: true, ageDeclarationVersion: 1 } },
  }))
  const other = await otherApi.login({ challengeId: 'x'.repeat(24), code: '123456',
    deviceProof: `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }, 'A'.repeat(43))
  assert.equal((await h.session.installAuthenticated(other)).status, 'authenticated')
  const cancelled = await h.controller.cancel()
  assert.equal(cancelled.leave, true)
  assert.ok(['cancelled', 'superseded'].includes(cancelled.outcome))
  assert.equal(h.session.snapshot().subjectId, '88')
  assert.equal(JSON.parse(h.vault.value).accessToken, 'later-account-token')
  await h.controller.dispose()
})

test('unmount cancels the in-flight attempt before a late HTTP success can install', async () => {
  const loginStarted = deferred()
  const loginResponse = deferred()
  const h = realEmailHarness({ loginReply: () => { loginStarted.resolve(); return loginResponse.promise } })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  const submit = h.controller.submitCode()
  await loginStarted.promise
  await h.controller.dispose()
  loginResponse.resolve({ status: 'authenticated', token: 'unmount-late-token', expiresIn: 300,
    user: { id: '73', nickname: '学员', roles: ['student'], ageVerified: true, ageDeclarationVersion: 1 } })
  await submit
  assert.equal(h.session.snapshot(), null)
  assert.equal(h.vault.value, '')
  assert.deepEqual(h.emitted, [])
})

test('remount during a pending unmount cleanup recovers the same opaque attempt handle', async () => {
  const installStarted = deferred()
  const allowInstall = deferred()
  const cleanupStarted = deferred()
  const allowCleanup = deferred()
  let signedOutWrites = 0
  let value = ''
  const repository = {
    readAuthenticationState() { return { ok: true, errorCode: '', found: value !== '', value } },
    async writeAuthenticationState(next) {
      if (next.includes('accessToken')) {
        installStarted.resolve()
        await allowInstall.promise
      } else if (next.includes('signedOut')) {
        signedOutWrites++
        if (signedOutWrites === 2) {
          cleanupStarted.resolve()
          await allowCleanup.promise
        }
      }
      value = next
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    clearAuthenticationState() { value = ''; return { ok: true, errorCode: '', found: false, value: '' } },
    get value() { return value },
  }
  const h = realEmailHarness({ repository })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  const submit = h.controller.submitCode()
  await installStarted.promise
  const oldDispose = h.controller.dispose()
  allowInstall.resolve()
  await cleanupStarted.promise

  const remounted = new AuthEntryController({ session: h.session, login: h.login,
    ageDeclaration: { async declare() { return { status: 'declared' } } } })
  assert.equal(remounted.state.status, 'cancel_failed')
  assert.equal(h.login.openAttempt(), null, 'remount must not reserve a different attempt while old cleanup is pending')
  const retry = remounted.cancel()
  allowCleanup.resolve()
  assert.deepEqual(await retry, { outcome: 'cancelled', leave: true })
  await oldDispose
  await submit
  assert.equal(h.session.snapshot(), null)
  assert.equal(h.vault.value, '')
  const fresh = h.login.openAttempt()
  assert.ok(fresh, 'the flow becomes available only after the original cancellation barrier completes')
  await h.login.cancelAttempt(fresh)
  await remounted.dispose()
})

test('disposing an older idle controller cannot reset a newer controller login attempt', async () => {
  const h = realEmailHarness()
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'authenticated')
  await h.session.logout()

  const newer = new AuthEntryController({ session: h.session, login: h.login,
    ageDeclaration: { async declare() { return { status: 'declared' } } } })
  await newer.start()
  newer.setEmail('person@example.test')
  await newer.requestCode()
  await h.controller.dispose()
  newer.setCode('654321')
  await newer.submitCode()
  assert.equal(newer.state.status, 'authenticated')
  assert.equal(h.requests.filter(request => request.url.endsWith('/auth/email-login')).length, 2,
    'the active new controller must still send its login after the old component is disposed')
  await newer.dispose()
})

test('expired challenge is cancelled before the controller permits a fresh challenge', async () => {
  let calls = 0
  const h = realEmailHarness({ challengeStatus: () => ++calls === 1 ? 410 : 202 })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  assert.equal(h.controller.state.status, 'email')
  assert.match(h.controller.state.message, /本次验证已失效/)
  await h.controller.requestCode()
  assert.equal(h.controller.state.status, 'waiting_code')
  assert.equal(h.requests.filter(request => request.url.endsWith('/auth/email/challenges')).length, 2)
  await h.controller.cancel()
  await h.controller.dispose()
})

test('expired login cancels its stale attempt before a fresh challenge and login', async () => {
  let loginCalls = 0
  const h = realEmailHarness({ loginStatus: () => ++loginCalls === 1 ? 410 : 200,
    loginReply: () => {
    return { status: 'authenticated', token: 'fresh-synthetic-token', expiresIn: 300,
      user: { id: '73', nickname: '学员', roles: ['student'], ageVerified: true, ageDeclarationVersion: 1 } }
  } })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'email')
  assert.match(h.controller.state.message, /本次验证已失效/)
  await h.controller.requestCode()
  h.controller.setCode('654321')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'authenticated')
  assert.equal(h.requests.filter(request => request.url.endsWith('/auth/email/challenges')).length, 2)
  assert.equal(h.requests.filter(request => request.url.endsWith('/auth/email-login')).length, 2)
  await h.controller.dispose()
})

test('a rejected code keeps the active challenge available for corrected input', async () => {
  let loginCalls = 0
  const h = realEmailHarness({ loginStatus: () => ++loginCalls === 1 ? 401 : 200 })
  await h.controller.start()
  h.controller.setEmail('person@example.test')
  await h.controller.requestCode()
  h.controller.setCode('123456')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'waiting_code')
  assert.match(h.controller.state.message, /验证码不正确或已失效，请检查后重试/)
  h.controller.setCode('654321')
  await h.controller.submitCode()
  assert.equal(h.controller.state.status, 'authenticated')
  assert.equal(h.requests.filter(request => request.url.endsWith('/auth/email/challenges')).length, 1)
  assert.equal(h.requests.filter(request => request.url.endsWith('/auth/email-login')).length, 2)
  await h.controller.dispose()
})

test('uni-app surface exposes a parent-handled return, safe cancel retry, and server comparison code', () => {
  const source = readFileSync(new URL('./AuthEntry.vue', import.meta.url), 'utf8')
  const controllerSource = readFileSync(new URL('./authEntryController.ts', import.meta.url), 'utf8')
  assert.match(source, /cancel:\s*\[\]/)
  assert.match(source, /@click="cancel"/)
  assert.match(source, /{{\s*state\.comparisonCode\s*}}/)
  assert.match(controllerSource, /请在已登录设备核对并确认/)
  assert.match(source, /@click="controller\.checkApproval\(\)"/)
  assert.match(source, /重试安全取消/)
  assert.match(source, /重新验证/)
  assert.doesNotMatch(source, /请求编号|同一请求|同一登录请求/)
  assert.doesNotMatch(source, /浏览免费内容无需登录/)
  assert.match(source, /if \(!result\.leave \|\| result\.outcome === 'unavailable'\) return/)
  assert.match(source, /onBeforeUnmount\(\(\) => controller\.dispose\(\)\)/)
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
