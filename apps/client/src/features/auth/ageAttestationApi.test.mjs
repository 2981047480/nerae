import assert from 'node:assert/strict'
import { test } from 'node:test'
import { EmailAuthApi } from '../../api/auth/emailAuthApi.ts'
import { AgeAttestationApi } from './ageAttestationApi.ts'
import { AuthSessionCoordinator } from './sessionCoordinator.ts'

const token = 'synthetic.age.token'
const user = (ageVerified, ageDeclarationVersion) => ({ id: '101', nickname: '', roles: ['student'], ageVerified, ageDeclarationVersion })
const reply = (body, status = 200) => ({ status, headers: { 'content-type': 'application/json', 'cache-control': 'no-store' }, body })
const requestId = 'a'.repeat(32)
const conflict = () => ({ status: 412,
  headers: { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store', 'x-request-id': requestId },
  body: { code: 'VERSION_CONFLICT', message: '记录已更新，请先查看最新内容', requestId } })

async function setup(transport) {
  let persisted = ''
  let currentUser = user(false, 1)
  const session = new AuthSessionCoordinator({
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: !!persisted, value: persisted }),
    writeAuthenticationState: value => { persisted = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState: () => { persisted = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }, async () => ({ status: 'valid', user: currentUser }))
  const auth = new EmailAuthApi('https://api.example.test', async () => ({ status: 200, headers: { 'cache-control': 'no-store' },
    body: { status: 'authenticated', token, expiresIn: 3600, user: currentUser } }))
  const login = await auth.login({ challengeId: 'c'.repeat(24), code: '012345',
    deviceProof: `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }, 'A'.repeat(43))
  await session.installAuthenticated(login)
  const calls = []
  const api = new AgeAttestationApi('https://api.example.test/v1', async request => {
    calls.push(request)
    return transport(request, value => { currentUser = value })
  }, session)
  return { api, session, calls, setUser: value => { currentUser = value } }
}

test('explicit declaration uses the authenticated version and refreshes the current session', async () => {
  const context = await setup(async (_request, setUser) => { setUser(user(true, 2)); return reply(user(true, 2)) })
  assert.equal((await context.api.declare(false)).status, 'invalid_confirmation')
  assert.equal(context.calls.length, 0)
  assert.equal((await context.api.declare(true)).status, 'declared')
  assert.equal(context.calls[0].url, 'https://api.example.test/v1/me/age-verify')
  assert.deepEqual(JSON.parse(context.calls[0].body), { isAdult: true, expectedVersion: 1 })
  assert.equal(context.calls[0].headers.Authorization, `Bearer ${token}`)
  assert.equal(context.session.snapshot().user.ageDeclarationVersion, 2)
  assert.equal(context.session.snapshot().user.ageVerified, true)
  assert.equal((await context.api.declare(true)).status, 'already_declared')
  assert.equal(context.calls.length, 1)
})

test('unknown commit result refreshes state but never automatically repeats a declaration', async () => {
  const context = await setup(async (_request, setUser) => { setUser(user(true, 2)); throw new Error('synthetic lost response') })
  assert.equal((await context.api.declare(true)).status, 'reconcile_required')
  assert.equal(context.calls.length, 1)
  assert.equal(context.session.snapshot().user.ageVerified, true)
  assert.equal((await context.api.declare(true)).status, 'already_declared')
  assert.equal(context.calls.length, 1)
})

test('version conflict refreshes state and requires another explicit confirmation', async () => {
  let requests = 0
  const context = await setup(async (_request, setUser) => {
    if (++requests === 1) { setUser(user(false, 2)); return conflict() }
    setUser(user(true, 3))
    return reply(user(true, 3))
  })
  assert.equal((await context.api.declare(true)).status, 'version_conflict')
  assert.equal(context.calls.length, 1)
  assert.equal(context.session.snapshot().user.ageDeclarationVersion, 2)
  assert.equal((await context.api.declare(true)).status, 'declared')
  assert.equal(JSON.parse(context.calls[1].body).expectedVersion, 2)
})

test('malformed 412 responses refresh state without claiming an authoritative conflict', async () => {
  const cases = [
    () => reply({ code: 'VERSION_CONFLICT' }, 412),
    () => ({ ...conflict(), body: { ...conflict().body, code: 'OTHER' } }),
    () => ({ ...conflict(), body: { ...conflict().body, message: '' } }),
    () => ({ ...conflict(), headers: { ...conflict().headers, 'x-request-id': 'b'.repeat(32) } }),
    () => ({ ...conflict(), headers: { ...conflict().headers, 'Cache-Control': 'no-store' } }),
    () => ({ ...conflict(), headers: { ...conflict().headers, 'content-type': 'text/plain' } }),
  ]
  for (const response of cases) {
    const context = await setup(async (_request, setUser) => { setUser(user(false, 2)); return response() })
    assert.equal((await context.api.declare(true)).status, 'reconcile_required')
    assert.equal(context.calls.length, 1)
    assert.equal(context.session.snapshot().user.ageDeclarationVersion, 2)
  }
})

test('missing or invalid profile versions cannot be guessed or sent', async () => {
  const context = await setup(async () => { throw new Error('must not send') })
  context.setUser({ id: '101', nickname: '', roles: ['student'], ageVerified: false })
  await context.session.restore()
  assert.equal((await context.api.declare(true)).status, 'unavailable')
  assert.equal(context.calls.length, 0)
})

test('session change during a declaration cannot publish its response', async () => {
  let finish
  const response = new Promise(resolve => { finish = resolve })
  const context = await setup(async () => response)
  const pending = context.api.declare(true)
  await new Promise(resolve => setImmediate(resolve))
  await context.session.logout()
  finish(reply(user(true, 2)))
  assert.equal((await pending).status, 'session_changed')
  assert.equal(context.session.current(), null)
})
