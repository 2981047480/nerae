import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  AuthApiError, EmailAuthApi, challengeNonceForSigning, sessionTokenForSecureStorage,
  approvalSecretsForSecureStorage,
} from './emailAuthApi.ts'

// Synthetic values; entropy is supplied by the caller, not assessed by this adapter.
const requestKey = 'R'.repeat(42) + 'Q'
const challengeId = 'c'.repeat(24)
const approvalId = 'a'.repeat(24)
const nonce = 'N'.repeat(43)
const approvalSecret = 'S'.repeat(43)
const token = 'synthetic.token.value'
const proof = 'p'.repeat(15) + '.' + 'q'.repeat(15) + '.' + 'r'.repeat(15)
const publicKey = { kty: 'EC', crv: 'P-256', x: 'X'.repeat(43), y: 'Y'.repeat(43) }
const input = { email: 'Person+sample@EXAMPLE.ORG', devicePublicKey: publicKey, deviceLabel: '手机' }
const challenge = { challengeId, nonce, expiresAt: '2026-10-30T09:00:00Z', retryAfter: '2026-10-30T08:59:00Z' }
const user = { id: '101', nickname: '', roles: ['student'], ageVerified: false, ageDeclarationVersion: 3, status: 'active', avatarUrl: null }
const authenticated = { status: 'authenticated', token, expiresIn: 3600, user, onboarded: false }
const approval = { status: 'approval_required', approvalId, approvalSecret, nonce, comparisonCode: '012345', expiresAt: '2026-10-30T09:00:00Z' }
const response = (body, status = 200, headers = { 'Cache-Control': 'no-store' }) => ({ status, headers, body })
const err = code => error => error instanceof AuthApiError && error.code === code
const fixedApi = value => new EmailAuthApi('https://api.example.org/v1/', async () => value)

const login = { challengeId, code: '004216', deviceProof: proof }
const exchange = { approvalId, approvalSecret, deviceProof: proof }

test('challenge transmits exact caller values only in HTTPS body and returns nonce through explicit accessor', async () => {
  let sent
  const api = new EmailAuthApi('https://api.example.org/v1/', async request => { sent = request; return response(challenge, 202) })
  const result = await api.requestChallenge(input, requestKey)
  assert.equal(sent.url, 'https://api.example.org/v1/auth/email/challenges')
  assert.equal(sent.method, 'POST')
  assert.deepEqual(JSON.parse(sent.body), input)
  assert.equal(sent.headers['Idempotency-Key'], requestKey)
  assert.equal(sent.headers['Cache-Control'], 'no-store')
  assert.ok(!sent.url.includes(input.email) && !sent.url.includes(requestKey))
  assert.equal(result.challengeId, challengeId)
  assert.equal(challengeNonceForSigning(result), nonce)
  assert.ok(!Object.values(result).includes(nonce))
  assert.throws(() => JSON.stringify(result), /Sensitive authentication result/)
  assert.throws(() => challengeNonceForSigning({ ...result }), err('invalid_response'))
  const lowerCaseTime = await fixedApi(response({ ...challenge, expiresAt: '2026-10-30t09:00:00z' }, 202)).requestChallenge(input, requestKey)
  assert.equal(lowerCaseTime.expiresAt, '2026-10-30t09:00:00z')
})

test('authentication and approval are distinct; sensitive values are not ordinary properties or JSON', async () => {
  const success = await fixedApi(response(authenticated)).login(login, requestKey)
  assert.equal(success.status, 'authenticated')
  assert.equal(success.user.id, '101')
  assert.equal(success.user.ageVerified, false)
  assert.equal(success.user.ageDeclarationVersion, 3)
  assert.equal(sessionTokenForSecureStorage(success), token)
  assert.ok(!Object.values(success).includes(token))
  assert.throws(() => JSON.stringify(success), /Sensitive authentication result/)
  assert.throws(() => sessionTokenForSecureStorage({ ...success }), err('invalid_response'))
  assert.ok(Object.isFrozen(success.user.roles))
  const pending = await fixedApi(response(approval)).login(login, requestKey)
  assert.equal(pending.status, 'approval_required')
  assert.equal(pending.comparisonCode, '012345')
  assert.deepEqual(approvalSecretsForSecureStorage(pending), { approvalSecret, nonce })
  assert.ok(!Object.values(pending).includes(approvalSecret))
  assert.throws(() => JSON.stringify(pending), /Sensitive authentication result/)
  assert.throws(() => sessionTokenForSecureStorage(pending), err('invalid_response'))
})

test('login rejects an invalid adult declaration version', async () => {
  for (const ageDeclarationVersion of [0, 1.5, 9007199254740992, '3']) {
    await assert.rejects(fixedApi(response({ ...authenticated, user: { ...user, ageDeclarationVersion } })).login(login, requestKey), err('invalid_response'))
  }
})

test('approval exchange carries the secret only in its body; pending, denied and expired cannot become sessions', async () => {
  for (const status of ['pending', 'denied', 'expired']) {
    let sent
    const api = new EmailAuthApi('https://api.example.org', async request => { sent = request; return response({ status }) })
    const result = await api.exchange(exchange, requestKey)
    assert.deepEqual(result, { status })
    assert.equal(sent.url, `https://api.example.org/auth/login-approvals/${approvalId}/exchange`)
    assert.ok(!sent.url.includes(approvalSecret) && !sent.url.includes(proof))
    assert.deepEqual(JSON.parse(sent.body), { approvalSecret, deviceProof: proof })
    assert.throws(() => sessionTokenForSecureStorage(result), err('invalid_response'))
  }
  const success = await fixedApi(response(authenticated)).exchange(exchange, requestKey)
  assert.equal(sessionTokenForSecureStorage(success), token)
})

test('network failure is redacted, is not retried automatically, and explicit replay preserves key and exact body', async () => {
  const requests = []
  const api = new EmailAuthApi('https://api.example.org', async request => {
    requests.push(request)
    if (requests.length === 1) throw new Error(`private ${token} ${input.email}`)
    return response(authenticated)
  })
  await assert.rejects(api.login(login, requestKey), error => err('network')(error) && !String(error).includes(token))
  assert.equal(requests.length, 1)
  await api.login(login, requestKey)
  assert.equal(requests.length, 2)
  assert.equal(requests[0].body, requests[1].body)
  assert.equal(requests[0].headers['Idempotency-Key'], requests[1].headers['Idempotency-Key'])
})

test('invalid configuration and malformed inputs never call transport', async () => {
  for (const base of ['http://api.example.org', 'https://u:p@example.org', 'https://example.org/?x=token',
    'https://example.org/?', 'https://example.org/#', 'https://example.org/\n', 'https:example.org']) {
    assert.throws(() => new EmailAuthApi(base, async () => ({})), err('invalid_configuration'))
  }
  let calls = 0
  const api = new EmailAuthApi('https://api.example.org', async () => { calls++; return response(challenge, 202) })
  for (const invalid of [
    { ...input, email: 'bad email@example.org' }, { ...input, email: 'a@@example.org' },
    { ...input, devicePublicKey: { ...publicKey, d: 'private' } }, { ...input, deviceLabel: '' },
    { ...input, extra: 'sensitive' },
  ]) await assert.rejects(api.requestChallenge(invalid, requestKey), err('invalid_request'))
  await assert.rejects(api.requestChallenge(input, 'short'), err('invalid_request'))
  await assert.rejects(api.requestChallenge(input, 'R'.repeat(43)), err('invalid_request'))
  await assert.rejects(api.login({ ...login, code: 'not-code' }, requestKey), err('invalid_request'))
  await assert.rejects(api.login({ ...login, deviceProof: 'unsigned' }, requestKey), err('invalid_request'))
  await assert.rejects(api.exchange({ ...exchange, approvalId: '../private' }, requestKey), err('invalid_request'))
  assert.equal(calls, 0)
})

test('unexpected success status, missing or conflicting no-store, malformed fields and disabled user fail closed', async () => {
  const badChallenges = [
    response(challenge, 200), response(challenge, 202, {}), response(challenge, 202, { 'cache-control': 'public' }),
    response(challenge, 202, { 'Cache-Control': 'no-store', 'cache-control': 'public' }),
    response({ ...challenge, nonce: 'short' }, 202), response({ ...challenge, expiresAt: '2026-02-30T09:00:00Z' }, 202),
    response({ ...challenge, expiresAt: '2026-10-30T25:00:00Z' }, 202), response({ ...challenge, unexpected: 'private' }, 202),
  ]
  for (const invalid of badChallenges) await assert.rejects(fixedApi(invalid).requestChallenge(input, requestKey), err('invalid_response'))
  for (const invalid of [
    { ...authenticated, user: { ...user, status: 'disabled' } }, { ...authenticated, token: 'a\nb' },
    { ...authenticated, user: { ...user, roles: ['admin'] } }, { ...authenticated, expiresIn: 0 },
    { ...authenticated, extra: 'hidden' }, { ...approval, comparisonCode: '12xx' },
  ]) await assert.rejects(fixedApi(response(invalid)).login(login, requestKey), err('invalid_response'))
  await assert.rejects(fixedApi(response({ status: 'pending', token })).exchange(exchange, requestKey), err('invalid_response'))
  await assert.rejects(fixedApi(response({ status: 'approval_required', ...approval })).exchange(exchange, requestKey), err('invalid_response'))
})

test('failure statuses map to fixed errors without raw bodies or exceptions', async () => {
  const expected = new Map([[400, 'invalid_request'], [401, 'unauthorized'], [403, 'forbidden'], [404, 'not_found'],
    [409, 'conflict'], [410, 'expired'], [422, 'invalid_request'], [429, 'rate_limited'], [500, 'unavailable'],
    [503, 'unavailable'], [302, 'unavailable']])
  for (const [status, code] of expected) {
    const requestId = 'a'.repeat(32)
    const body = status === 401 ? { code: 'UNAUTHENTICATED', message: '验证未通过', requestId } : { message: token }
    const headers = status === 401 ? { 'Cache-Control': 'no-store', 'Content-Type': 'application/json', 'X-Request-ID': requestId }
      : { 'Cache-Control': 'no-store' }
    const api = fixedApi(response(body, status, headers))
    await assert.rejects(api.login(login, requestKey), error => err(code)(error) && error.status === status &&
      !String(error).includes(token) && !JSON.stringify(error).includes(token))
  }
  await assert.rejects(fixedApi({ status: 900, body: token, headers: {} }).login(login, requestKey), err('invalid_response'))
  for (const headers of [{}, { 'Cache-Control': 'public' }, { 'cache-control': 'no-store', 'Cache-Control': 'public' }]) {
    await assert.rejects(fixedApi(response({ message: token }, 401, headers)).login(login, requestKey), err('invalid_response'))
  }
  for (const malformed of [{}, { code: 'UNAUTHENTICATED' }, { code: 'UNAUTHENTICATED', message: 'bad', requestId: 'x' }]) {
    await assert.rejects(fixedApi(response(malformed, 401)).login(login, requestKey), err('invalid_response'))
  }
})
