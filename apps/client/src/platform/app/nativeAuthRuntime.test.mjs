import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'

// These ports simulate the UTS boundary only; they do not execute native code
// or contact an actual server. The real App assembly is the module under test.
const state = { registry: '', session: '', keys: new Map(), random: 0, requests: [], adult: false }
const publicKey = { kty: 'EC', crv: 'P-256', x: 'A'.repeat(43), y: 'Q'.repeat(43) }
const emptyKey = { kty: 'EC', crv: 'P-256', x: '', y: '' }
const read = value => ({ ok: true, errorCode: '', found: !!value, value })
const done = () => ({ ok: true, errorCode: '', found: false, value: '' })
const user = () => ({ id: '101', nickname: '', roles: ['student'], ageVerified: state.adult, ageDeclarationVersion: state.adult ? 2 : 1 })
globalThis.__neraeRuntimeTest = {
  readRegistry: () => read(state.registry),
  casRegistry(found, expected, next) {
    if (found !== !!state.registry || expected !== state.registry) return { ok: false, errorCode: 'conflict', found: false, value: '' }
    state.registry = next
    return done()
  },
  readSession: () => read(state.session),
  writeSession(value) { state.session = value; return done() },
  clearSession() { state.session = ''; return done() },
  random: () => ({ ok: true, errorCode: '', value: Buffer.alloc(32, ++state.random).toString('base64url') }),
  create(alias) { state.keys.set(alias, publicKey); return { ok: true, errorCode: '', alias, publicKey, proof: '' } },
  readKey(alias) {
    return state.keys.has(alias) ? { ok: true, errorCode: '', alias, publicKey, proof: '' }
      : { ok: false, errorCode: 'missing_key', alias: '', publicKey: emptyKey, proof: '' }
  },
  sign(alias) {
    if (!state.keys.has(alias)) return { ok: false, errorCode: 'missing_key', alias: '', publicKey: emptyKey, proof: '' }
    return { ok: true, errorCode: '', alias, publicKey: emptyKey, proof: `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }
  },
  request(request, callbacks) {
    state.requests.push(request)
    const headers = JSON.parse(request.headersJson)
    let status = 200
    let body
    if (request.path === '/auth/email/challenges') {
      status = 202
      body = { challengeId: 'c'.repeat(24), nonce: 'N'.repeat(43), expiresAt: '2026-11-01T00:00:00Z' }
    } else if (request.path === '/auth/email-login') {
      body = { status: 'authenticated', token: 'synthetic.runtime.token', expiresIn: 3600, user: user() }
    } else if (request.path === '/me/age-verify') {
      assert.equal(headers.Authorization, 'Bearer synthetic.runtime.token')
      assert.deepEqual(JSON.parse(request.body), { isAdult: true, expectedVersion: 1 })
      state.adult = true
      body = user()
    } else if (request.path === '/me') {
      assert.equal(headers.Authorization, 'Bearer synthetic.runtime.token')
      body = user()
    } else if (request.path === '/me/login-approvals' && request.method === 'GET') {
      assert.equal(headers.Authorization, 'Bearer synthetic.runtime.token')
      body = { items: [{ id: 'a'.repeat(24), comparisonCode: '123456', createdAt: '2026-11-03T00:00:00Z', expiresAt: '2026-11-03T00:05:00Z' }] }
    } else if (request.path === `/me/login-approvals/${'a'.repeat(24)}/decision` && request.method === 'POST') {
      assert.equal(headers.Authorization, 'Bearer synthetic.runtime.token')
      assert.deepEqual(JSON.parse(request.body), { decision: 'approve', comparisonCode: '123456', confirmed: true })
      body = { status: 'approved' }
    } else throw new Error('unexpected synthetic route')
    const requestId = `native-${state.requests.length}`
    callbacks.success({ requestId, status,
      headersJson: JSON.stringify({ 'content-type': 'application/json', 'cache-control': 'no-store' }), body: JSON.stringify(body) })
    return requestId
  },
}

const modules = {
  '@/uni_modules/nerae-secure-vault': `
    export const readAuthenticationState = () => globalThis.__neraeRuntimeTest.readSession()
    export const writeAuthenticationState = value => globalThis.__neraeRuntimeTest.writeSession(value)
    export const clearAuthenticationState = () => globalThis.__neraeRuntimeTest.clearSession()
    export const readDeviceIdentityRegistry = () => globalThis.__neraeRuntimeTest.readRegistry()
    export const compareAndSetDeviceIdentityRegistry = (found, expected, next) => globalThis.__neraeRuntimeTest.casRegistry(found, expected, next)
  `,
  '@/uni_modules/nerae-random': `export const generateSecureRandomRequestKey = () => globalThis.__neraeRuntimeTest.random()`,
  '@/uni_modules/nerae-device-key': `
    export const createDeviceKeyAtReservedAlias = alias => globalThis.__neraeRuntimeTest.create(alias)
    export const readExistingDevicePublicKey = alias => globalThis.__neraeRuntimeTest.readKey(alias)
    export const signExistingDeviceProof = alias => globalThis.__neraeRuntimeTest.sign(alias)
  `,
  '@/uni_modules/nerae-https': `
    export const request = (input, callbacks) => globalThis.__neraeRuntimeTest.request(input, callbacks)
    export const cancel = () => {}
  `,
}
registerHooks({ resolve(specifier, context, nextResolve) {
  if (Object.hasOwn(modules, specifier)) return { url: `data:text/javascript,${encodeURIComponent(modules[specifier])}`, shortCircuit: true }
  return nextResolve(specifier, context)
} })
const { createAppAuthRuntime } = await import('./nativeAuthRuntime.ts')

test('App authentication assembly requires one explicit HTTPS origin', async () => {
  for (const bad of ['', ' ', 'not-a-url', 'http://api.example.test/v1', 'https://user:pass@api.example.test/v1']) {
    assert.throws(() => createAppAuthRuntime(bad))
  }
  assert.equal(state.requests.length, 0)
  const runtime = createAppAuthRuntime('https://api.example.test/v1///')
  assert.equal(createAppAuthRuntime('https://api.example.test/v1'), runtime)
  assert.equal(createAppAuthRuntime('https://api.example.test/v1///'), runtime)
  assert.throws(() => createAppAuthRuntime('https://other.example.test/v1'), /already configured/)
  assert.equal((await runtime.session.restore()).status, 'signed_out')
  assert.equal((await runtime.login.requestCode('member@example.test')).status, 'waiting_code')
  assert.equal((await runtime.login.submitCode('012345')).status, 'authenticated')
  assert.equal(runtime.session.snapshot().user.ageVerified, false)
  assert.equal((await runtime.ageDeclaration.declare(true)).status, 'declared')
  assert.equal(runtime.session.snapshot().user.ageVerified, true)
  const pending = await runtime.approvals.list()
  assert.equal(pending.status, 'ready')
  assert.equal((await runtime.approvals.decide(pending.items[0], 'approve', true)).status, 'approved')
  assert.deepEqual(state.requests.map(item => item.path), [
    '/auth/email/challenges', '/auth/email-login', '/me/age-verify', '/me',
    '/me/login-approvals', `/me/login-approvals/${'a'.repeat(24)}/decision`,
  ])
  assert.ok(state.requests.every(item => item.baseUrl === 'https://api.example.test/v1'))
  assert.equal((await runtime.session.logout()).status, 'signed_out')
  assert.equal(state.session, '')
})
