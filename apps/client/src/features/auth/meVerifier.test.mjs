import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createCurrentUserVerifier } from './meVerifier.ts'
import { AuthSessionCoordinator } from './sessionCoordinator.ts'

const token = 'synthetic.persisted.bearer'
const expectedSubjectId = '812'
const user = { id: expectedSubjectId, nickname: '', roles: ['student'], ageVerified: false, ageDeclarationVersion: 3, status: 'active', avatarUrl: null }
const headers = { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' }
const response = (status = 200, body = user, responseHeaders = headers) => ({ status, headers: responseHeaders, body })
const requestId = 'a'.repeat(32)
const unauthenticatedBody = { code: 'UNAUTHENTICATED', message: '请先登录', requestId }
const unauthenticatedHeaders = { ...headers, 'x-request-id': requestId }

test('200 with no-store and a contract User for the expected subject restores a safe copy', async () => {
  const verifier = createCurrentUserVerifier(async candidate => {
    assert.equal(candidate, token)
    return response()
  })
  const result = await verifier(token, expectedSubjectId)
  assert.deepEqual(result, { status: 'valid', user })
  assert.ok(Object.isFrozen(result.user))
  assert.ok(Object.isFrozen(result.user.roles))
  assert.ok(!JSON.stringify(result).includes(token))
})

test('the verifier composes with coordinator restore without exposing the saved bearer', async () => {
  const vault = {
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: true,
      value: JSON.stringify({ schema: 1, subjectId: expectedSubjectId, accessToken: token }) }),
    writeAuthenticationState: () => ({ ok: true, errorCode: '', found: false, value: '' }),
    clearAuthenticationState: () => ({ ok: true, errorCode: '', found: false, value: '' }),
  }
  const verifier = createCurrentUserVerifier(async () => response())
  const coordinator = new AuthSessionCoordinator(vault, verifier)
  const result = await coordinator.restore()
  assert.equal(result.status, 'authenticated')
  assert.equal(coordinator.current().accessToken, token)
  assert.ok(!JSON.stringify(result).includes(token))
})

test('mismatch and non-401 failures preserve the vault entry but never activate it', async () => {
  const original = JSON.stringify({ schema: 1, subjectId: expectedSubjectId, accessToken: token })
  let stored = original
  const vault = {
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: true, value: stored }),
    writeAuthenticationState: value => {
      stored = value
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    clearAuthenticationState: () => {
      stored = ''
      return { ok: true, errorCode: '', found: false, value: '' }
    },
  }
  for (const failedResponse of [
    response(200, { ...user, id: '813' }), response(403, { code: 'FORBIDDEN' }),
    response(401, {}, unauthenticatedHeaders),
  ]) {
    const coordinator = new AuthSessionCoordinator(vault, createCurrentUserVerifier(async () => failedResponse))
    assert.deepEqual(await coordinator.restore(), { status: 'unavailable', reason: 'verification' })
    assert.equal(stored, original)
    assert.equal(coordinator.current(), null)
  }
})

test('only contract-valid UNAUTHENTICATED 401 revokes the saved credential', async () => {
  const verifier = createCurrentUserVerifier(async () => response(401, unauthenticatedBody, unauthenticatedHeaders))
  assert.deepEqual(await verifier(token, expectedSubjectId), { status: 'revoked' })
  const invalid401s = [
    response(401, {}, unauthenticatedHeaders),
    response(401, { ...unauthenticatedBody, code: 'UNAUTHORIZED' }, unauthenticatedHeaders),
    response(401, { code: 'UNAUTHENTICATED', requestId }, unauthenticatedHeaders),
    response(401, { ...unauthenticatedBody, requestId: 'not-an-id' }, unauthenticatedHeaders),
    response(401, { ...unauthenticatedBody, details: [] }, unauthenticatedHeaders),
    response(401, unauthenticatedBody, { ...headers, 'x-request-id': 'b'.repeat(32) }),
    response(401, unauthenticatedBody, headers),
    response(401, unauthenticatedBody, { ...unauthenticatedHeaders, 'cache-control': 'public' }),
    response(401, null, unauthenticatedHeaders),
  ]
  for (const invalid of invalid401s) {
    const badVerifier = createCurrentUserVerifier(async () => invalid)
    assert.deepEqual(await badVerifier(token, expectedSubjectId), { status: 'unavailable' })
  }
  const malformedBody = createCurrentUserVerifier(async () => response(401, null))
  assert.deepEqual(await malformedBody(token, expectedSubjectId), { status: 'unavailable' })
})

test('403, rate limits, server failures, redirects, network failures and unknown statuses are unavailable', async () => {
  for (const status of [302, 400, 403, 404, 422, 429, 500, 503, 599]) {
    const verifier = createCurrentUserVerifier(async () => response(status, { code: 'FAILURE' }))
    const result = await verifier(token, expectedSubjectId)
    assert.deepEqual(result, { status: 'unavailable' }, `HTTP ${status}`)
    assert.ok(!JSON.stringify(result).includes(token))
  }
  const network = createCurrentUserVerifier(async () => { throw new Error(`private ${token}`) })
  assert.deepEqual(await network(token, expectedSubjectId), { status: 'unavailable' })
})

test('missing or invalid no-store/content-type and subject mismatch cannot activate or revoke', async () => {
  for (const badHeaders of [
    {}, { 'content-type': 'application/json' }, { 'cache-control': 'no-store' },
    { 'content-type': 'text/plain', 'cache-control': 'no-store' },
    { 'content-type': 'application/json', 'Cache-Control': 'public', 'cache-control': 'no-store' },
  ]) {
    const verifier = createCurrentUserVerifier(async () => response(200, user, badHeaders))
    assert.deepEqual(await verifier(token, expectedSubjectId), { status: 'unavailable' })
  }
  const mismatch = createCurrentUserVerifier(async () => response(200, { ...user, id: '813' }))
  assert.deepEqual(await mismatch(token, expectedSubjectId), { status: 'unavailable' })
})

test('malformed, disabled and unknown User values fail closed; unrecognized fields are not projected', async () => {
  const badUsers = [
    null, 'not-an-object', { ...user, id: '0' }, { ...user, roles: ['admin'] },
    { ...user, status: 'locked' }, { ...user, status: 'disabled' }, { ...user, ageVerified: 'true' },
    { ...user, ageDeclarationVersion: 0 }, { ...user, ageDeclarationVersion: 9007199254740992 },
    { ...user, ageDeclarationVersion: '3' },
    { ...user, avatarUrl: 42 },
  ]
  for (const bad of badUsers) {
    const verifier = createCurrentUserVerifier(async () => response(200, bad))
    const result = await verifier(token, expectedSubjectId)
    assert.deepEqual(result, { status: 'unavailable' })
    assert.ok(!JSON.stringify(result).includes(token))
  }
  const malformedResponse = createCurrentUserVerifier(async () => null)
  assert.deepEqual(await malformedResponse(token, expectedSubjectId), { status: 'unavailable' })

  const extended = createCurrentUserVerifier(async () => response(200, { ...user, privateField: token }))
  const projected = await extended(token, expectedSubjectId)
  assert.equal(projected.status, 'valid')
  assert.ok(!Object.hasOwn(projected.user, 'privateField'))
  assert.ok(!JSON.stringify(projected).includes(token))
})

test('invalid inputs do not call the transport and transport details never appear in results', async () => {
  let calls = 0
  const verifier = createCurrentUserVerifier(async () => { calls++; throw new Error(token) })
  assert.deepEqual(await verifier('bad\r\nheader', expectedSubjectId), { status: 'unavailable' })
  assert.deepEqual(await verifier(token, 'not-an-id'), { status: 'unavailable' })
  assert.equal(calls, 0)
  const failed = await verifier(token, expectedSubjectId)
  assert.deepEqual(failed, { status: 'unavailable' })
  assert.ok(!JSON.stringify(failed).includes(token))
})
