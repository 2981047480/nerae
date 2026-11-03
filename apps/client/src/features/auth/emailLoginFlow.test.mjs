import assert from 'node:assert/strict'
import { inspect } from 'node:util'
import { test } from 'node:test'
import { EmailAuthApi } from '../../api/auth/emailAuthApi.ts'
import { EmailLoginFlow } from './emailLoginFlow.ts'
import { AuthSessionCoordinator } from './sessionCoordinator.ts'

const email = 'member@example.test'
const challengeId = 'c'.repeat(24)
const nonce = 'N'.repeat(43)
const token = 'synthetic.token.value'
const deviceProof = `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}`
const publicKey = { kty: 'EC', crv: 'P-256', x: 'A'.repeat(43), y: 'Q'.repeat(43) }
const reply = (status, body, headers = { 'cache-control': 'no-store' }) => ({ status, headers, body })
const requestId = 'a'.repeat(32)
const rejected = () => reply(401, { code: 'UNAUTHENTICATED', message: '验证未通过', requestId },
  { 'cache-control': 'no-store', 'content-type': 'application/json', 'x-request-id': requestId })
const challenge = { challengeId, nonce, expiresAt: '2026-11-01T00:00:00Z' }
const authenticated = { status: 'authenticated', token, expiresIn: 3600,
  user: { id: '101', nickname: '', roles: ['student'], ageVerified: false, ageDeclarationVersion: 1 } }

function vault() {
  let value = ''
  return {
    get value() { return value },
    readAuthenticationState() { return { ok: true, errorCode: '', found: !!value, value } },
    writeAuthenticationState(next) { value = next; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState() { value = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }
}

function setup(transport, options = {}) {
  const requests = []
  let serial = 0
  let signs = 0
  let approvalSigns = 0
  const storage = vault()
  const api = new EmailAuthApi('https://api.example.test', async request => {
    requests.push({ ...request, headers: { ...request.headers } })
    return transport(request, requests.length)
  })
  const session = new AuthSessionCoordinator(storage, async () => ({ status: 'unavailable' }))
  const device = { publicKeyForProtocol: publicKey }
  const flow = new EmailLoginFlow(api,
    { async initializeFirstForEmail() { return { status: 'ready', identity: device } } },
    options.session ?? session,
    options.random ?? (() => Buffer.alloc(32, ++serial).toString('base64url')),
    options.sign ?? (() => { signs++; return { forSubmission: () => deviceProof } }),
    options.signApproval ?? (() => { approvalSigns++; return { forSubmission: () => `${deviceProof}${approvalSigns}` } }))
  return { flow, requests, storage, session, get signs() { return signs }, get approvalSigns() { return approvalSigns } }
}

test('challenge and login retries reuse the exact key and body, then install the verified session', async () => {
  let challenges = 0
  let logins = 0
  const context = setup(async request => {
    if (request.url.endsWith('/auth/email/challenges')) {
      if (++challenges === 1) throw new Error('synthetic unknown outcome')
      return reply(202, challenge)
    }
    if (++logins === 1) throw new Error('synthetic unknown outcome')
    return reply(200, authenticated)
  })
  assert.equal((await context.flow.requestCode(email)).status, 'retry_challenge')
  assert.equal((await context.flow.requestCode(email)).status, 'restart_required')
  assert.equal((await context.flow.retryChallenge()).status, 'waiting_code')
  assert.deepEqual(context.requests[0], context.requests[1])
  assert.equal((await context.flow.submitCode('012345')).status, 'retry_login')
  assert.equal((await context.flow.submitCode('999999')).status, 'retry_login')
  const result = await context.flow.retryLogin()
  assert.equal(result.status, 'authenticated')
  assert.equal(result.session.subjectId, '101')
  assert.deepEqual(context.requests[2], context.requests[3])
  assert.equal(context.signs, 1)
  assert.equal(JSON.parse(context.storage.value).accessToken, token)
  for (const value of [JSON.stringify(context.flow), inspect(context.flow), JSON.stringify(result)]) {
    assert.equal(value.includes(email), false)
    assert.equal(value.includes('012345'), false)
    assert.equal(value.includes(deviceProof), false)
    assert.equal(value.includes(token), false)
  }
})

test('a definitive rejected code permits an explicit new attempt with a new proof and key', async () => {
  let attempts = 0
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge)
    : ++attempts === 1 ? rejected() : reply(200, authenticated))
  assert.equal((await context.flow.requestCode(email)).status, 'waiting_code')
  assert.equal((await context.flow.submitCode('012345')).status, 'verification_rejected')
  assert.equal((await context.flow.submitCode('123456')).status, 'authenticated')
  assert.notEqual(context.requests[1].headers['Idempotency-Key'], context.requests[2].headers['Idempotency-Key'])
  assert.equal(context.signs, 2)
})

test('rate limits preserve the original login attempt and invalid input never calls the API', async () => {
  let attempts = 0
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge)
    : ++attempts === 1 ? reply(429, { code: 'RATE_LIMITED' }) : reply(200, authenticated))
  assert.equal((await context.flow.requestCode(email)).status, 'waiting_code')
  assert.equal((await context.flow.submitCode('12')).status, 'invalid_code')
  assert.equal(context.requests.length, 1)
  assert.equal((await context.flow.submitCode('012345')).status, 'rate_limited')
  assert.equal((await context.flow.retryLogin()).status, 'authenticated')
  assert.deepEqual(context.requests[1], context.requests[2])
})

test('approval result never installs a session or exposes its secret', async () => {
  const approval = { status: 'approval_required', approvalId: 'a'.repeat(24), approvalSecret: 'S'.repeat(43),
    nonce: 'N'.repeat(43), comparisonCode: '123456', expiresAt: '2026-11-01T00:00:00Z' }
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : reply(200, approval))
  assert.equal((await context.flow.requestCode(email)).status, 'waiting_code')
  const result = await context.flow.submitCode('012345')
  assert.deepEqual(result, { status: 'approval_required', comparisonCode: '123456', expiresAt: approval.expiresAt })
  assert.equal(context.session.current(), null)
  assert.equal(context.storage.value, '')
  assert.equal((await context.flow.retryLogin()).status, 'not_ready')
  assert.equal(JSON.stringify(result).includes(approval.approvalSecret), false)
})

test('approval polling signs fresh requests after pending and exactly retries an uncertain exchange', async () => {
  const approval = { status: 'approval_required', approvalId: 'a'.repeat(24), approvalSecret: 'S'.repeat(43),
    nonce: 'N'.repeat(43), comparisonCode: '123456', expiresAt: '2026-11-01T00:00:00Z' }
  let polls = 0
  const context = setup(async request => {
    if (request.url.endsWith('/auth/email/challenges')) return reply(202, challenge)
    if (request.url.endsWith('/auth/email-login')) return reply(200, approval)
    if (++polls === 1) return reply(200, { status: 'pending' })
    if (polls === 2) throw new Error('synthetic unknown exchange outcome')
    return reply(200, authenticated)
  })
  assert.equal((await context.flow.requestCode(email)).status, 'waiting_code')
  assert.equal((await context.flow.submitCode('012345')).status, 'approval_required')
  assert.equal((await context.flow.checkApproval()).status, 'approval_pending')
  assert.equal(context.storage.value, '')
  assert.equal((await context.flow.checkApproval()).status, 'retry_approval')
  const result = await context.flow.checkApproval()
  assert.equal(result.status, 'authenticated')
  assert.equal(result.session.subjectId, '101')
  assert.equal(JSON.parse(context.storage.value).accessToken, token)
  assert.equal(context.approvalSigns, 2)
  assert.notEqual(context.requests[2].headers['Idempotency-Key'], context.requests[3].headers['Idempotency-Key'])
  assert.notEqual(JSON.parse(context.requests[2].body).deviceProof, JSON.parse(context.requests[3].body).deviceProof)
  assert.deepEqual(context.requests[3], context.requests[4])
  for (const value of [JSON.stringify(context.flow), inspect(context.flow), JSON.stringify(result)]) {
    assert.equal(value.includes(approval.approvalSecret), false)
    assert.equal(value.includes(approval.nonce), false)
    assert.equal(value.includes(token), false)
  }
  assert.equal((await context.flow.checkApproval()).status, 'not_ready')
})

test('rate limits and vault failures retain the exact approval exchange for safe retry', async () => {
  const approval = { status: 'approval_required', approvalId: 'a'.repeat(24), approvalSecret: 'S'.repeat(43),
    nonce: 'N'.repeat(43), comparisonCode: '123456', expiresAt: '2026-11-01T00:00:00Z' }
  let polls = 0
  const storage = vault()
  const write = storage.writeAuthenticationState.bind(storage)
  let fail = true
  storage.writeAuthenticationState = value => {
    if (fail && value.includes('accessToken')) { fail = false; return { ok: false, errorCode: 'unavailable', found: false, value: '' } }
    return write(value)
  }
  const session = new AuthSessionCoordinator(storage, async () => ({ status: 'unavailable' }))
  const context = setup(async request => {
    if (request.url.endsWith('/auth/email/challenges')) return reply(202, challenge)
    if (request.url.endsWith('/auth/email-login')) return reply(200, approval)
    return ++polls === 1 ? reply(429, { code: 'RATE_LIMITED' }) : reply(200, authenticated)
  }, { session })
  await context.flow.requestCode(email)
  await context.flow.submitCode('012345')
  assert.equal((await context.flow.checkApproval()).status, 'rate_limited')
  assert.equal((await context.flow.checkApproval()).status, 'retry_approval')
  assert.equal((await context.flow.checkApproval()).status, 'authenticated')
  assert.equal(context.approvalSigns, 1)
  assert.deepEqual(context.requests[2], context.requests[3])
  assert.deepEqual(context.requests[3], context.requests[4])
})

test('denied or expired approval never installs a session and permits a new login', async () => {
  for (const status of ['denied', 'expired']) {
    const approval = { status: 'approval_required', approvalId: 'a'.repeat(24), approvalSecret: 'S'.repeat(43),
      nonce: 'N'.repeat(43), comparisonCode: '123456', expiresAt: '2026-11-01T00:00:00Z' }
    const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge)
      : request.url.endsWith('/auth/email-login') ? reply(200, approval) : reply(200, { status }))
    await context.flow.requestCode(email)
    await context.flow.submitCode('012345')
    assert.equal((await context.flow.checkApproval()).status, `approval_${status}`)
    assert.equal(context.session.current(), null)
    assert.equal(context.storage.value, '')
    assert.equal((await context.flow.checkApproval()).status, 'not_ready')
    assert.equal((await context.flow.requestCode('new@example.test')).status, 'waiting_code')
  }
})

test('logout during approval polling prevents a late successful exchange from installing a session', async () => {
  const approval = { status: 'approval_required', approvalId: 'a'.repeat(24), approvalSecret: 'S'.repeat(43),
    nonce: 'N'.repeat(43), comparisonCode: '123456', expiresAt: '2026-11-01T00:00:00Z' }
  let finish
  const response = new Promise(resolve => { finish = resolve })
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge)
    : request.url.endsWith('/auth/email-login') ? reply(200, approval) : response)
  await context.flow.requestCode(email)
  await context.flow.submitCode('012345')
  const pending = context.flow.checkApproval()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal((await context.session.logout()).status, 'signed_out')
  finish(reply(200, authenticated))
  assert.equal((await pending).status, 'session_changed')
  assert.equal(context.session.current(), null)
  assert.equal(context.storage.value, '')
  assert.equal((await context.flow.checkApproval()).status, 'not_ready')
})

test('a failed vault install keeps the exact login attempt available for explicit retry', async () => {
  let installs = 0
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : reply(200, authenticated), {
    session: { captureInstallGuard: () => ({}), isInstallGuardCurrent: () => true, cancelInstallGuard: async () => 'cancelled',
      subscribeInstallGuardInvalidation: () => () => {}, isCurrent: () => false,
      async installAuthenticatedIfCurrent() { installs++; return { status: 'unavailable', reason: 'storage' } } },
  })
  await context.flow.requestCode(email)
  assert.equal((await context.flow.submitCode('012345')).status, 'retry_login')
  assert.equal((await context.flow.retryLogin()).status, 'retry_login')
  assert.equal(installs, 2)
  assert.deepEqual(context.requests[1], context.requests[2])
  assert.equal(context.signs, 1)
})

test('a guarded native vault failure retains the original login until a later successful retry', async () => {
  const storage = vault()
  const originalWrite = storage.writeAuthenticationState.bind(storage)
  let rejectOnce = true
  storage.writeAuthenticationState = value => {
    if (rejectOnce && value.includes('accessToken')) {
      rejectOnce = false
      return { ok: false, errorCode: 'unavailable', found: false, value: '' }
    }
    return originalWrite(value)
  }
  const session = new AuthSessionCoordinator(storage, async () => ({ status: 'unavailable' }))
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : reply(200, authenticated), { session })
  assert.equal((await context.flow.requestCode(email)).status, 'waiting_code')
  assert.equal((await context.flow.submitCode('012345')).status, 'retry_login')
  assert.equal((await context.flow.retryLogin()).status, 'authenticated')
  assert.deepEqual(context.requests[1], context.requests[2])
  assert.equal(context.signs, 1)
})

test('an untrusted 401 preserves the original login attempt for exact replay', async () => {
  let attempts = 0
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge)
    : ++attempts === 1 ? reply(401, { code: 'UNAUTHENTICATED' }) : reply(200, authenticated))
  await context.flow.requestCode(email)
  assert.equal((await context.flow.submitCode('012345')).status, 'retry_login')
  assert.equal((await context.flow.retryLogin()).status, 'authenticated')
  assert.deepEqual(context.requests[1], context.requests[2])
  assert.equal(context.signs, 1)
})

test('logout during an in-flight login prevents a late response from installing a session', async () => {
  let finish
  const response = new Promise(resolve => { finish = resolve })
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : response)
  await context.flow.requestCode(email)
  const pending = context.flow.submitCode('012345')
  await new Promise(resolve => setImmediate(resolve))
  assert.equal((await context.session.logout()).status, 'signed_out')
  finish(reply(200, authenticated))
  assert.equal((await pending).status, 'session_changed')
  assert.equal(context.session.current(), null)
  assert.equal(context.storage.value, '')
  assert.equal((await context.flow.retryLogin()).status, 'not_ready')
  assert.equal((await context.flow.requestCode('new@example.test')).status, 'waiting_code')
  assert.equal(JSON.parse(context.requests.at(-1).body).email, 'new@example.test')
})

test('logout while awaiting code allows a fresh challenge without a manual reset', async () => {
  const context = setup(async () => reply(202, challenge))
  assert.equal((await context.flow.requestCode(email)).status, 'waiting_code')
  assert.equal((await context.session.logout()).status, 'signed_out')
  assert.equal((await context.flow.requestCode('new@example.test')).status, 'waiting_code')
  assert.equal(context.requests.length, 2)
  assert.equal(JSON.parse(context.requests[1].body).email, 'new@example.test')
})

test('cancelling a scoped login before a late success cannot install a session', async () => {
  let finish
  const response = new Promise(resolve => { finish = resolve })
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : response)
  const handle = context.flow.openAttempt()
  assert.ok(handle)
  assert.equal((await context.flow.requestCode(email)).status, 'not_ready')
  assert.equal(await context.flow.cancelAttempt({}), 'superseded')
  assert.equal((await context.flow.requestCode(email, handle)).status, 'waiting_code')
  const pending = context.flow.submitCode('012345', handle)
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(await context.flow.cancelAttempt(handle), 'cancelled')
  finish(reply(200, authenticated))
  assert.equal((await pending).status, 'session_changed')
  assert.equal(context.session.current(), null)
  assert.equal(context.storage.value, '')
  const next = context.flow.openAttempt()
  assert.ok(next)
  assert.equal(await context.flow.cancelAttempt(handle), 'superseded')
  assert.equal((await context.flow.requestCode('new@example.test', next)).status, 'waiting_code')
})

test('cancelling a scoped login during vault write cleans its credential without publishing it', async () => {
  const storage = vault()
  const write = storage.writeAuthenticationState.bind(storage)
  let writingStarted
  const started = new Promise(resolve => { writingStarted = resolve })
  let release
  const gate = new Promise(resolve => { release = resolve })
  storage.writeAuthenticationState = async value => {
    if (value.includes('accessToken')) { writingStarted(); await gate }
    return write(value)
  }
  const session = new AuthSessionCoordinator(storage, async () => ({ status: 'unavailable' }))
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : reply(200, authenticated), { session })
  const handle = context.flow.openAttempt()
  await context.flow.requestCode(email, handle)
  const pending = context.flow.submitCode('012345', handle)
  await started
  const cancelling = context.flow.cancelAttempt(handle)
  assert.equal(session.current(), null)
  release()
  assert.equal((await pending).status, 'session_changed')
  assert.equal(await cancelling, 'cancelled')
  assert.equal(session.current(), null)
  assert.equal(storage.value, '')
})

test('a finished attempt can be released without signing out and its old handle cannot cancel the session', async () => {
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : reply(200, authenticated))
  const handle = context.flow.openAttempt()
  await context.flow.requestCode(email, handle)
  assert.equal((await context.flow.submitCode('012345', handle)).status, 'authenticated')
  assert.equal(context.flow.releaseAttempt(handle), true)
  assert.equal(await context.flow.cancelAttempt(handle), 'superseded')
  assert.equal(context.session.snapshot().subjectId, '101')
})

test('failed scoped cancellation stays unavailable until the same handle safely retries cleanup', async () => {
  const storage = vault()
  const write = storage.writeAuthenticationState.bind(storage)
  const clear = storage.clearAuthenticationState.bind(storage)
  let cleanupFails = false
  storage.writeAuthenticationState = value => cleanupFails && !value.includes('accessToken')
    ? { ok: false, errorCode: 'unavailable', found: false, value: '' } : write(value)
  storage.clearAuthenticationState = () => cleanupFails
    ? { ok: false, errorCode: 'unavailable', found: false, value: '' } : clear()
  const session = new AuthSessionCoordinator(storage, async () => ({ status: 'unavailable' }))
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : reply(200, authenticated), { session })
  const handle = context.flow.openAttempt()
  await context.flow.requestCode(email, handle)
  assert.equal((await context.flow.submitCode('012345', handle)).status, 'authenticated')
  cleanupFails = true
  assert.equal(await context.flow.cancelAttempt(handle), 'unavailable')
  assert.equal(context.flow.openAttempt(), null)
  assert.equal(context.flow.releaseAttempt(handle), false)
  assert.equal((await context.flow.requestCode(email, handle)).status, 'not_ready')
  assert.equal(session.current(), null)
  assert.notEqual(storage.value, '')
  cleanupFails = false
  assert.equal(await context.flow.cancelAttempt(handle), 'cancelled')
  assert.equal(storage.value, '')
  assert.ok(context.flow.openAttempt())
})

test('cancelling an old scoped attempt cannot sign out a later account', async () => {
  let finish
  const response = new Promise(resolve => { finish = resolve })
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : response)
  const handle = context.flow.openAttempt()
  await context.flow.requestCode(email, handle)
  const pending = context.flow.submitCode('012345', handle)
  await new Promise(resolve => setImmediate(resolve))
  const otherApi = new EmailAuthApi('https://api.example.test', async () => reply(200, {
    status: 'authenticated', token: 'other.synthetic.token', expiresIn: 3600,
    user: { id: '202', nickname: '', roles: ['student'], ageVerified: false },
  }))
  const other = await otherApi.login({ challengeId, code: '123456', deviceProof }, 'A'.repeat(43))
  assert.equal((await context.session.installAuthenticated(other)).status, 'authenticated')
  assert.equal(await context.flow.cancelAttempt(handle), 'cancelled')
  finish(reply(200, authenticated))
  assert.equal((await pending).status, 'session_changed')
  assert.equal(context.session.snapshot().subjectId, '202')
  assert.equal(JSON.parse(context.storage.value).accessToken, 'other.synthetic.token')
})

test('logout immediately discards an idle uncertain login attempt', async () => {
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge)
    : Promise.reject(new Error('synthetic unknown result')))
  assert.equal((await context.flow.requestCode(email)).status, 'waiting_code')
  assert.equal((await context.flow.submitCode('012345')).status, 'retry_login')
  assert.equal((await context.session.logout()).status, 'signed_out')
  // If cleanup were lazy, retryLogin would first see the stale attempt and
  // report session_changed instead of not_ready.
  assert.equal((await context.flow.retryLogin()).status, 'not_ready')
  assert.equal((await context.flow.requestCode('new@example.test')).status, 'waiting_code')
})

test('logout immediately discards a pending approval and permits a new challenge', async () => {
  const approval = { status: 'approval_required', approvalId: 'a'.repeat(24), approvalSecret: 'S'.repeat(43),
    nonce: 'N'.repeat(43), comparisonCode: '123456', expiresAt: '2026-11-01T00:00:00Z' }
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : reply(200, approval))
  await context.flow.requestCode(email)
  assert.equal((await context.flow.submitCode('012345')).status, 'approval_required')
  await context.session.logout()
  assert.equal((await context.flow.requestCode('new@example.test')).status, 'waiting_code')
})

test('an account switch during an in-flight login cannot be overwritten by its old response', async () => {
  let finish
  const response = new Promise(resolve => { finish = resolve })
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : response)
  await context.flow.requestCode(email)
  const pending = context.flow.submitCode('012345')
  await new Promise(resolve => setImmediate(resolve))
  const otherApi = new EmailAuthApi('https://api.example.test', async () => reply(200, {
    status: 'authenticated', token: 'other.synthetic.token', expiresIn: 3600,
    user: { id: '202', nickname: '', roles: ['student'], ageVerified: false },
  }))
  const other = await otherApi.login({ challengeId, code: '123456', deviceProof }, 'A'.repeat(43))
  assert.equal((await context.session.installAuthenticated(other)).status, 'authenticated')
  finish(reply(200, authenticated))
  assert.equal((await pending).status, 'session_changed')
  assert.equal(context.session.snapshot().subjectId, '202')
  assert.equal(JSON.parse(context.storage.value).accessToken, 'other.synthetic.token')
})

test('in-flight requests cannot be reset or duplicated', async () => {
  let finish
  const response = new Promise(resolve => { finish = resolve })
  const context = setup(async () => response)
  const inFlight = context.flow.requestCode(email)
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(context.flow.reset(), false)
  assert.equal((await context.flow.requestCode(email)).status, 'busy')
  finish(reply(202, challenge))
  assert.equal((await inFlight).status, 'waiting_code')
  assert.equal(context.requests.length, 1)
  assert.equal(context.flow.reset(), true)
})
