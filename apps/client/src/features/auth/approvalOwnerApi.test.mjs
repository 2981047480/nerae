import assert from 'node:assert/strict'
import { inspect } from 'node:util'
import { test } from 'node:test'
import { EmailAuthApi } from '../../api/auth/emailAuthApi.ts'
import { ApprovalOwnerApi } from './approvalOwnerApi.ts'
import { AuthSessionCoordinator } from './sessionCoordinator.ts'

const token = 'synthetic.owner.token'
const item = { id: 'a'.repeat(24), comparisonCode: '123456', createdAt: '2026-11-03T00:00:00Z', expiresAt: '2026-11-03T00:05:00Z' }
const headers = { 'cache-control': 'no-store', 'content-type': 'application/json; charset=utf-8' }
const response = (statusCode, data, fields = headers) => ({ statusCode, data, headers: fields })
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

async function setup(transport) {
  let stored = ''
  const session = new AuthSessionCoordinator({
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: !!stored, value: stored }),
    writeAuthenticationState: value => { stored = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState: () => { stored = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }, async () => ({ status: 'unavailable' }))
  const login = new EmailAuthApi('https://api.example.test', async () => ({ status: 200, headers: { 'cache-control': 'no-store' },
    body: { status: 'authenticated', token, expiresIn: 3600, user: { id: '101', nickname: '', roles: ['student'], ageVerified: true } } }))
  const auth = await login.login({ challengeId: 'c'.repeat(24), code: '012345',
    deviceProof: `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }, 'A'.repeat(43))
  assert.equal((await session.installAuthenticated(auth)).status, 'authenticated')
  const calls = []
  let serial = 0
  const api = new ApprovalOwnerApi('https://api.example.test/v1', async request => {
    calls.push({ ...request, header: { ...request.header } })
    return transport(request, calls.length)
  }, session, () => Buffer.alloc(32, ++serial).toString('base64url'))
  return { api, session, calls }
}

test('approval requires an issued item and explicit user confirmation, then retries an unknown result exactly', async () => {
  let decisions = 0
  const context = await setup(async request => {
    if (request.method === 'GET') return response(200, { items: [item] })
    if (++decisions === 1) throw new Error('synthetic response lost after commit')
    return response(200, { status: 'approved' })
  })
  const listed = await context.api.list()
  assert.equal(listed.status, 'ready')
  const issued = listed.items[0]
  assert.equal((await context.api.decide(issued, 'approve')).status, 'invalid_confirmation')
  assert.equal((await context.api.decide({ ...issued }, 'approve', true)).status, 'invalid_item')
  assert.equal(context.calls.length, 1)
  assert.equal((await context.api.decide(issued, 'approve', true)).status, 'retry')
  assert.equal((await context.api.list()).status, 'decision_pending')
  assert.equal((await context.api.decide(issued, 'deny')).status, 'decision_pending')
  assert.equal((await context.api.retryDecision()).status, 'approved')
  assert.deepEqual(context.calls[1], context.calls[2])
  assert.equal(context.calls[1].url, `https://api.example.test/v1/me/login-approvals/${item.id}/decision`)
  assert.deepEqual(JSON.parse(context.calls[1].data), { decision: 'approve', comparisonCode: item.comparisonCode, confirmed: true })
  assert.equal(context.calls[1].header.Authorization, `Bearer ${token}`)
  assert.equal(context.calls[1].url.includes(token), false)
  assert.equal((await context.api.decide(issued, 'deny')).status, 'invalid_item')
  for (const value of [JSON.stringify(context.api), inspect(context.api), JSON.stringify(listed)]) assert.equal(value.includes(token), false)
})

test('denial sends only the denial decision and a rate limit preserves the original attempt', async () => {
  let decisions = 0
  const context = await setup(async request => request.method === 'GET' ? response(200, { items: [item] })
    : ++decisions === 1 ? response(429, { code: 'RATE_LIMITED' }) : response(200, { status: 'denied' }))
  const listed = await context.api.list()
  assert.equal((await context.api.decide(listed.items[0], 'deny')).status, 'rate_limited')
  assert.equal((await context.api.retryDecision()).status, 'denied')
  assert.deepEqual(context.calls[1], context.calls[2])
  assert.deepEqual(JSON.parse(context.calls[1].data), { decision: 'deny' })
})

test('malformed or cacheable lists fail closed and invalidate prior choices', async () => {
  for (const bad of [
    response(200, { items: [item] }, { 'cache-control': 'public', 'content-type': 'application/json' }),
    response(200, { items: [item] }, { ...headers, 'Cache-Control': 'no-store' }),
    response(200, { items: [item, item] }),
    response(200, { items: [{ ...item, comparisonCode: 'x' }] }),
    response(200, { items: [{ ...item, expiresAt: item.createdAt }] }),
    response(200, { items: [{ ...item, extra: 'unexpected' }] }),
    response(200, { items: [], extra: 'unexpected' }),
  ]) {
    let reads = 0
    const context = await setup(async () => ++reads === 1 ? response(200, { items: [item] }) : bad)
    const listed = await context.api.list()
    assert.equal((await context.api.list()).status, 'unavailable')
    assert.equal((await context.api.decide(listed.items[0], 'approve', true)).status, 'invalid_item')
    assert.equal(context.calls.length, 2)
  }
})

test('a wrong or cacheable decision response cannot settle the pending request', async () => {
  let decisions = 0
  const context = await setup(async request => request.method === 'GET' ? response(200, { items: [item] })
    : ++decisions === 1 ? response(200, { status: 'denied' })
      : decisions === 2 ? response(200, { status: 'approved' }, { 'cache-control': 'public', 'content-type': 'application/json' })
        : response(200, { status: 'approved' }))
  const listed = await context.api.list()
  assert.equal((await context.api.decide(listed.items[0], 'approve', true)).status, 'retry')
  assert.equal((await context.api.retryDecision()).status, 'retry')
  assert.equal((await context.api.retryDecision()).status, 'approved')
  assert.deepEqual(context.calls[1], context.calls[2])
  assert.deepEqual(context.calls[2], context.calls[3])
})

test('a rejected or expired request can be explicitly left unresolved and the list refreshed', async () => {
  let reads = 0
  const context = await setup(async request => request.method === 'GET'
    ? response(200, { items: ++reads === 1 ? [item] : [] })
    : response(403, { code: 'FORBIDDEN' }))
  const listed = await context.api.list()
  assert.equal((await context.api.decide(listed.items[0], 'approve', true)).status, 'needs_review')
  assert.equal((await context.api.list()).status, 'decision_pending')
  assert.equal(context.api.stopWaiting(false), 'invalid_confirmation')
  assert.equal(context.api.stopWaiting(true), 'unresolved')
  assert.deepEqual(await context.api.list(), { status: 'ready', items: [] })
  assert.equal((await context.api.retryDecision()).status, 'unavailable')
  assert.equal((await context.api.decide(listed.items[0], 'approve', true)).status, 'invalid_item')
})

test('logout during list or decision discards the late result and pending credential', async () => {
  const listGate = deferred()
  const first = await setup(async () => listGate.promise)
  const listing = first.api.list()
  await new Promise(resolve => setImmediate(resolve))
  await first.session.logout()
  listGate.resolve(response(200, { items: [item] }))
  assert.equal((await listing).status, 'session_changed')
  assert.equal((await first.api.list()).status, 'not_authenticated')

  const decisionGate = deferred()
  const second = await setup(async request => request.method === 'GET' ? response(200, { items: [item] }) : decisionGate.promise)
  const listed = await second.api.list()
  const deciding = second.api.decide(listed.items[0], 'approve', true)
  await new Promise(resolve => setImmediate(resolve))
  await second.session.logout()
  decisionGate.resolve(response(200, { status: 'approved' }))
  assert.equal((await deciding).status, 'session_changed')
  assert.equal((await second.api.retryDecision()).status, 'unavailable')
})

test('an in-flight decision is not duplicated or replaced by another action', async () => {
  const gate = deferred()
  const context = await setup(async request => request.method === 'GET' ? response(200, { items: [item] }) : gate.promise)
  const listed = await context.api.list()
  const deciding = context.api.decide(listed.items[0], 'approve', true)
  await new Promise(resolve => setImmediate(resolve))
  assert.equal((await context.api.decide(listed.items[0], 'deny')).status, 'busy')
  assert.equal((await context.api.retryDecision()).status, 'busy')
  assert.equal((await context.api.list()).status, 'busy')
  gate.resolve(response(200, { status: 'approved' }))
  assert.equal((await deciding).status, 'approved')
  assert.equal(context.calls.length, 2)
})
