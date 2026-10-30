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
    options.sign ?? (() => { signs++; return { forSubmission: () => deviceProof } }))
  return { flow, requests, storage, session, get signs() { return signs } }
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

test('a failed vault install keeps the exact login attempt available for explicit retry', async () => {
  let installs = 0
  const context = setup(async request => request.url.endsWith('/auth/email/challenges') ? reply(202, challenge) : reply(200, authenticated), {
    session: { captureInstallGuard: () => ({}), isInstallGuardCurrent: () => true,
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
