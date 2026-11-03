import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'

const state = { raw: '', keys: new Map(), creates: 0, random: 0, mode: '', writes: 0, signs: [] }
const empty = { kty: 'EC', crv: 'P-256', x: '', y: '' }
const publicKey = { kty: 'EC', crv: 'P-256', x: 'A'.repeat(43), y: 'Q'.repeat(43) }
const failure = errorCode => ({ ok: false, errorCode, alias: '', publicKey: empty, proof: '' })
globalThis.__neraeIdentityTest = {
  readRegistry() { return { ok: true, errorCode: '', found: !!state.raw, value: state.raw } },
  cas(found, oldValue, next) {
    state.writes++
    if (found !== !!state.raw || oldValue !== state.raw) return { ok: false, errorCode: 'conflict', found: false, value: '' }
    if (state.mode === 'reject' || (state.mode === 'reject-activation' && JSON.parse(next).entries.some(entry => entry.status === 'active'))) {
      return { ok: false, errorCode: 'unavailable', found: false, value: '' }
    }
    state.raw = next
    if (state.mode === 'uncertain') throw new Error('synthetic unknown outcome')
    return { ok: true, errorCode: '', found: false, value: '' }
  },
  random() {
    state.random++
    return { ok: true, errorCode: '', value: Buffer.alloc(32, state.random).toString('base64url') }
  },
  create(alias) {
    state.creates++
    assert.ok(JSON.parse(state.raw).entries.some(entry => entry.alias === alias), 'alias must be persisted before key creation')
    if (state.keys.has(alias)) return failure('alias_conflict')
    state.keys.set(alias, publicKey)
    return { ok: true, errorCode: '', alias, publicKey, proof: '' }
  },
  readKey(alias) {
    if (!state.keys.has(alias)) return failure('missing_key')
    return { ok: true, errorCode: '', alias, publicKey: state.keys.get(alias), proof: '' }
  },
  sign(alias, purpose, resourceId, nonce) {
    state.signs.push({ alias, purpose, resourceId, nonce })
    if (!state.keys.has(alias) || state.mode === 'sign-failure') return failure('missing_key')
    return { ok: true, errorCode: '', alias, publicKey: empty, proof: `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }
  },
}

const modules = {
  '@/uni_modules/nerae-secure-vault': `
    export const readDeviceIdentityRegistry = () => globalThis.__neraeIdentityTest.readRegistry()
    export const compareAndSetDeviceIdentityRegistry = (found, oldValue, next) => globalThis.__neraeIdentityTest.cas(found, oldValue, next)
  `,
  '@/uni_modules/nerae-random': `export const generateSecureRandomRequestKey = () => globalThis.__neraeIdentityTest.random()`,
  '@/uni_modules/nerae-device-key': `
    export const createDeviceKeyAtReservedAlias = alias => globalThis.__neraeIdentityTest.create(alias)
    export const readExistingDevicePublicKey = alias => globalThis.__neraeIdentityTest.readKey(alias)
    export const signExistingDeviceProof = (alias, purpose, resourceId, nonce) => globalThis.__neraeIdentityTest.sign(alias, purpose, resourceId, nonce)
  `,
}
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (Object.hasOwn(modules, specifier)) return { url: `data:text/javascript,${encodeURIComponent(modules[specifier])}`, shortCircuit: true }
    return nextResolve(specifier, context)
  },
})
const { createAppDeviceIdentityCoordinator, signAppApprovalExchange, signAppEmailLoginChallenge } = await import('./nativeDeviceIdentity.ts')
const { EmailAuthApi } = await import('../../api/auth/emailAuthApi.ts')
const { ProtectedDeviceIdentity, ReservedDeviceAlias } = await import('../../features/auth/deviceIdentityCoordinator.ts')
const { AuthSessionCoordinator } = await import('../../features/auth/sessionCoordinator.ts')
const { createAppEmailLoginFlow } = await import('./nativeEmailLogin.ts')
const coordinator = createAppDeviceIdentityCoordinator()

function reset() {
  state.raw = ''
  state.keys.clear()
  state.creates = 0
  state.random = 0
  state.mode = ''
  state.writes = 0
  state.signs = []
}

test('App binding reserves in native registry before creating and reuses the same identity', async () => {
  reset()
  assert.equal(createAppDeviceIdentityCoordinator(), coordinator)
  const first = await coordinator.initializeFirstForEmail('Name@EXAMPLE.COM')
  const second = await coordinator.readForEmail('Name@example.com')
  assert.equal(first.status, 'ready')
  assert.equal(second.status, 'ready')
  assert.equal(first.identity.aliasForProtocol, second.identity.aliasForProtocol)
  assert.equal(state.creates, 1)
  assert.equal(state.random, 1)
  assert.deepEqual(JSON.parse(state.raw).entries.map(entry => entry.email), ['Name@example.com'])
  assert.equal(JSON.parse(state.raw).entries[0].status, 'active')
  assert.equal(JSON.stringify(first).includes('Name@example.com'), false)
})

test('native CAS conflict cannot overwrite another account or create an unreserved key', async () => {
  reset()
  const results = await Promise.all([
    coordinator.initializeFirstForEmail('first@example.com'),
    coordinator.initializeFirstForEmail('second@example.com'),
  ])
  assert.deepEqual(results.map(result => result.status).sort(), ['ready', 'reservation_conflict'])
  assert.equal(state.creates, 1)
  assert.equal(JSON.parse(state.raw).entries.length, 1)
  const loser = results[0].status === 'ready' ? 'second@example.com' : 'first@example.com'
  assert.equal((await coordinator.initializeFirstForEmail(loser)).status, 'ready')
  assert.equal(JSON.parse(state.raw).entries.length, 2)
  assert.equal(state.creates, 2)
})

test('unknown native CAS outcome preserves reservation and never creates a replacement', async () => {
  reset()
  state.mode = 'uncertain'
  assert.equal((await coordinator.initializeFirstForEmail('member@example.com')).status, 'storage_unavailable')
  assert.equal(state.creates, 0)
  const reserved = JSON.parse(state.raw).entries[0].alias
  state.mode = ''
  assert.equal((await coordinator.initializeFirstForEmail('member@example.com')).status, 'reservation_incomplete')
  assert.equal(JSON.parse(state.raw).entries[0].alias, reserved)
  assert.equal(state.creates, 0)
})

test('the same random alias cannot be assigned to two different emails', async () => {
  reset()
  assert.equal((await coordinator.initializeFirstForEmail('first@example.com')).status, 'ready')
  const raw = state.raw
  state.random = 0 // Deliberately force a collision in the synthetic random port.
  assert.equal((await coordinator.initializeFirstForEmail('second@example.com')).status, 'reservation_conflict')
  assert.equal(state.raw, raw)
  assert.equal(state.creates, 1)
})

test('corrupt or duplicate protected registry is preserved and cannot authorize a key creation', async () => {
  reset()
  const alias = Buffer.alloc(32, 1).toString('base64url')
  for (const raw of [
    '{"schema":1,"entries":[],"entries":[]}',
    JSON.stringify({ schema: 1, entries: [{ email: 'a@example.com', alias, status: 'active' }, { email: 'b@example.com', alias, status: 'active' }] }),
    JSON.stringify({ schema: 1, entries: [{ email: 'A@EXAMPLE.COM', alias, status: 'active' }] }),
  ]) {
    state.raw = raw
    assert.equal((await coordinator.initializeFirstForEmail('member@example.com')).status, 'storage_unavailable')
    assert.equal(state.raw, raw)
  }
  assert.equal(state.writes, 0)
  assert.equal(state.creates, 0)
})

test('native CAS failure fails closed before key creation', async () => {
  reset()
  state.mode = 'reject'
  assert.equal((await coordinator.initializeFirstForEmail('member@example.com')).status, 'storage_unavailable')
  assert.equal(state.raw, '')
  assert.equal(state.creates, 0)
})

test('an alias already present outside the registry is never treated as this account identity', async () => {
  reset()
  const existing = Buffer.alloc(32, 1).toString('base64url')
  state.keys.set(existing, publicKey)
  assert.equal((await coordinator.initializeFirstForEmail('member@example.com')).status, 'creation_conflict')
  assert.equal(JSON.parse(state.raw).entries[0].status, 'reserved')
  assert.equal((await coordinator.readForEmail('member@example.com')).status, 'reservation_incomplete')
  assert.equal((await coordinator.initializeFirstForEmail('member@example.com')).status, 'reservation_incomplete')
  assert.equal(state.creates, 1)
})

test('a created key without a durably activated reservation remains unusable', async () => {
  reset()
  state.mode = 'reject-activation'
  assert.equal((await coordinator.initializeFirstForEmail('member@example.com')).status, 'activation_uncertain')
  assert.equal(JSON.parse(state.raw).entries[0].status, 'reserved')
  assert.equal(state.keys.size, 1)
  state.mode = ''
  assert.equal((await coordinator.readForEmail('member@example.com')).status, 'reservation_incomplete')
  assert.equal(state.creates, 1)
})

test('email proof signs only an issued challenge using the existing device key and hides the proof', async () => {
  reset()
  const ready = await coordinator.initializeFirstForEmail('member@example.com')
  assert.equal(ready.status, 'ready')
  const challengeId = 'c'.repeat(24)
  const nonce = 'N'.repeat(43)
  const api = new EmailAuthApi('https://api.example.test', async () => ({
    status: 202, headers: { 'cache-control': 'no-store' },
    body: { challengeId, nonce, expiresAt: '2026-11-01T00:00:00Z' },
  }))
  const challenge = await api.requestChallenge({ email: 'member@example.com', devicePublicKey: publicKey }, 'A'.repeat(43))
  const forged = new ProtectedDeviceIdentity(new ReservedDeviceAlias(ready.identity.aliasForProtocol), publicKey)
  assert.equal(signAppEmailLoginChallenge(forged, challenge), null)
  assert.equal(signAppEmailLoginChallenge(ready.identity, { ...challenge }), null)
  assert.equal(state.signs.length, 0)
  const proof = signAppEmailLoginChallenge(ready.identity, challenge)
  assert.ok(proof)
  assert.equal(state.signs.length, 1)
  assert.deepEqual(state.signs[0], { alias: ready.identity.aliasForProtocol, purpose: 'email_login', resourceId: challengeId, nonce })
  assert.equal(proof.forSubmission(), `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}`)
  assert.equal(JSON.stringify(proof), '"<redacted>"')
  state.mode = 'sign-failure'
  assert.equal(signAppEmailLoginChallenge(ready.identity, challenge), null)
  assert.equal(state.creates, 1)
})

test('approval proof signs only an issued approval with the existing device key', async () => {
  reset()
  const ready = await coordinator.initializeFirstForEmail('member@example.com')
  assert.equal(ready.status, 'ready')
  const approvalId = 'a'.repeat(24)
  const nonce = 'N'.repeat(43)
  const api = new EmailAuthApi('https://api.example.test', async () => ({
    status: 200, headers: { 'cache-control': 'no-store' },
    body: { status: 'approval_required', approvalId, approvalSecret: 'S'.repeat(43), nonce,
      comparisonCode: '123456', expiresAt: '2026-11-01T00:00:00Z' },
  }))
  const approval = await api.login({ challengeId: 'c'.repeat(24), code: '012345',
    deviceProof: `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }, 'A'.repeat(43))
  const forged = new ProtectedDeviceIdentity(new ReservedDeviceAlias(ready.identity.aliasForProtocol), publicKey)
  assert.equal(signAppApprovalExchange(forged, approval), null)
  assert.equal(signAppApprovalExchange(ready.identity, { ...approval }), null)
  assert.equal(state.signs.length, 0)
  const proof = signAppApprovalExchange(ready.identity, approval)
  assert.ok(proof)
  assert.deepEqual(state.signs[0], { alias: ready.identity.aliasForProtocol, purpose: 'approval_exchange', resourceId: approvalId, nonce })
  assert.equal(JSON.stringify(proof), '"<redacted>"')
  state.mode = 'sign-failure'
  assert.equal(signAppApprovalExchange(ready.identity, approval), null)
  assert.equal(state.creates, 1)
})

test('App email flow composes the native device ports, proof and secure session coordinator', async () => {
  reset()
  let sessionValue = ''
  const session = new AuthSessionCoordinator({
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: !!sessionValue, value: sessionValue }),
    writeAuthenticationState: value => { sessionValue = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState: () => { sessionValue = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }, async () => ({ status: 'unavailable' }))
  const requests = []
  const api = new EmailAuthApi('https://api.example.test', async request => {
    requests.push(request)
    if (request.url.endsWith('/auth/email/challenges')) return {
      status: 202, headers: { 'cache-control': 'no-store' },
      body: { challengeId: 'c'.repeat(24), nonce: 'N'.repeat(43), expiresAt: '2026-11-01T00:00:00Z' },
    }
    return { status: 200, headers: { 'cache-control': 'no-store' }, body: {
      status: 'authenticated', token: 'synthetic-session-token', expiresIn: 3600,
      user: { id: '42', nickname: '', roles: ['student'], ageVerified: false, ageDeclarationVersion: 1 },
    } }
  })
  const flow = createAppEmailLoginFlow(api, session)
  assert.equal(createAppEmailLoginFlow(api, session), flow)
  assert.throws(() => createAppEmailLoginFlow(new EmailAuthApi('https://api.example.test', async () => ({})), session), /already configured/)
  assert.equal((await flow.requestCode('member@example.com')).status, 'waiting_code')
  assert.equal(JSON.parse(state.raw).entries[0].status, 'active')
  assert.equal((await flow.submitCode('012345')).status, 'authenticated')
  assert.equal(state.creates, 1)
  assert.equal(state.signs.length, 1)
  assert.equal(requests.length, 2)
  assert.equal(session.snapshot().subjectId, '42')
  assert.equal(JSON.parse(sessionValue).accessToken, 'synthetic-session-token')
})
