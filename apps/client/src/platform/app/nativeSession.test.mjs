import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'

// Redirect only the native module in this Node test. The production binding
// itself remains the module under test; no device or Keychain is involved.
const nativeVault = {
  value: JSON.stringify({ schema: 1, subjectId: '42', accessToken: 'synthetic-token' }),
  calls: [],
  read() { this.calls.push('read'); return { ok: true, errorCode: '', found: !!this.value, value: this.value } },
  write(value) { this.calls.push('write'); this.value = value; return { ok: true, errorCode: '', found: false, value: '' } },
  clear() { this.calls.push('clear'); this.value = ''; return { ok: true, errorCode: '', found: false, value: '' } },
}
globalThis.__neraeNativeVaultTest = nativeVault
const moduleText = `
  export const readAuthenticationState = () => globalThis.__neraeNativeVaultTest.read()
  export const writeAuthenticationState = value => globalThis.__neraeNativeVaultTest.write(value)
  export const clearAuthenticationState = () => globalThis.__neraeNativeVaultTest.clear()
`
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === '@/uni_modules/nerae-secure-vault') {
      return { url: `data:text/javascript,${encodeURIComponent(moduleText)}`, shortCircuit: true }
    }
    return nextResolve(specifier, context)
  },
})

const { createAppSessionCoordinator } = await import('./nativeSession.ts')
let available = true
const readCurrentUser = async token => {
  assert.equal(token, 'synthetic-token')
  if (!available) throw new Error('synthetic network failure')
  return {
    status: 200,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: { id: '42', nickname: 'member', roles: ['student'] },
  }
}

test('App binding uses one coordinator and restores only after server verification', async () => {
  const session = createAppSessionCoordinator(readCurrentUser)
  assert.equal(createAppSessionCoordinator(readCurrentUser), session)
  assert.throws(() => createAppSessionCoordinator(async () => ({})), /already configured/)
  assert.equal((await session.restore()).status, 'authenticated')
  assert.equal(session.snapshot().subjectId, '42')
  assert.deepEqual(nativeVault.calls, ['read'])
  assert.equal(JSON.stringify(session.snapshot()).includes('synthetic-token'), false)
})

test('temporary verification failure preserves vault; logout clears authentication state', async () => {
  const session = createAppSessionCoordinator(readCurrentUser)
  available = false
  assert.deepEqual(await session.restore(), { status: 'unavailable', reason: 'verification' })
  assert.equal(JSON.parse(nativeVault.value).accessToken, 'synthetic-token')
  assert.equal(session.current(), null)
  assert.deepEqual(await session.logout(), { status: 'signed_out', reason: 'logged_out' })
  assert.equal(nativeVault.value, '')
  assert.deepEqual(nativeVault.calls.slice(-2), ['write', 'clear'])
})
