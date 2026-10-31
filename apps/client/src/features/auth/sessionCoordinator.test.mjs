import assert from 'node:assert/strict'
import { inspect } from 'node:util'
import { test } from 'node:test'
import { EmailAuthApi } from '../../api/auth/emailAuthApi.ts'
import { AuthSessionCoordinator } from './sessionCoordinator.ts'

const user = id => ({ id, nickname: `user-${id}`, roles: ['student'], ageVerified: false, status: 'active', avatarUrl: null })
const reply = (id, token, expiresIn = 3600) => ({
  status: 'authenticated', token, expiresIn, user: user(id),
})
const authenticated = (id, token, expiresIn) => new EmailAuthApi('https://api.example.test', async () => ({
  status: 200, headers: { 'Cache-Control': 'no-store' }, body: reply(id, token, expiresIn),
})).login({ challengeId: 'c'.repeat(24), code: '123456', deviceProof: `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }, 'A'.repeat(43))

function memoryVault(initial = '') {
  let value = initial
  const calls = []
  return {
    calls,
    get value() { return value },
    readAuthenticationState() {
      calls.push('read')
      return { ok: true, errorCode: '', found: value !== '', value }
    },
    writeAuthenticationState(next) {
      calls.push('write')
      value = next
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    clearAuthenticationState() {
      calls.push('clear')
      value = ''
      return { ok: true, errorCode: '', found: false, value: '' }
    },
  }
}

function deferred() {
  let resolve
  let reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

const liveVerifier = (id, token) => async (candidate, subject) =>
  candidate === token && subject === id ? { status: 'valid', user: user(id) } : { status: 'revoked' }

test('branded authenticated result persists only in the injected vault and token is non-enumerable', async () => {
  const token = 'synthetic.bearer.secret'
  const vault = memoryVault()
  const coordinator = new AuthSessionCoordinator(vault, liveVerifier('101', token))
  const result = await coordinator.installAuthenticated(await authenticated('101', token))
  assert.equal(result.status, 'authenticated')
  assert.equal(coordinator.snapshot().subjectId, '101')
  const credential = coordinator.current()
  assert.equal(credential.accessToken, token)
  assert.deepEqual(Object.keys(credential), ['subjectId', 'generation'])
  assert.ok(!JSON.stringify(credential).includes(token))
  assert.ok(!inspect(credential).includes(token))
  assert.ok(!JSON.stringify(coordinator).includes(token))
  assert.ok(!inspect(coordinator).includes(token))
  assert.equal(coordinator.repository, undefined)
  assert.equal(coordinator.verifyPersisted, undefined)
  assert.ok(!JSON.stringify(coordinator.snapshot()).includes(token))
  assert.equal(JSON.parse(vault.value).accessToken, token)
  assert.deepEqual(JSON.parse(vault.value), { schema: 1, subjectId: '101', accessToken: token })
  assert.ok(!Object.hasOwn(JSON.parse(vault.value), 'expiresIn'))
})

test('a login install guard rejects logout and renews only after its own failed vault write', async () => {
  const credential = await authenticated('101', 'synthetic.guard.token')
  const store = memoryVault()
  let failWrite = true
  const originalWrite = store.writeAuthenticationState.bind(store)
  store.writeAuthenticationState = value => {
    if (failWrite && value.includes('accessToken')) return { ok: false, errorCode: 'unavailable', found: false, value: '' }
    return originalWrite(value)
  }
  const coordinator = new AuthSessionCoordinator(store, liveVerifier('101', 'synthetic.guard.token'))
  const guard = coordinator.captureInstallGuard()
  assert.ok(guard)
  let invalidations = 0
  coordinator.subscribeInstallGuardInvalidation(guard, () => { invalidations++ })
  assert.equal((await coordinator.installAuthenticatedIfCurrent(credential, guard)).status, 'unavailable')
  assert.equal(invalidations, 0)
  assert.equal(coordinator.isInstallGuardCurrent(guard), true)
  failWrite = false
  assert.equal((await coordinator.installAuthenticatedIfCurrent(credential, guard)).status, 'authenticated')
  assert.equal(invalidations, 1)
  assert.equal(coordinator.isInstallGuardCurrent(guard), false)
  const later = coordinator.captureInstallGuard()
  await coordinator.logout()
  assert.equal((await coordinator.installAuthenticatedIfCurrent(credential, later)).status, 'superseded')
  assert.equal(coordinator.current(), null)
  assert.equal((await coordinator.installAuthenticatedIfCurrent(credential, {})).status, 'superseded')
})

test('guard invalidation listeners run synchronously once and cannot break logout', async () => {
  const coordinator = new AuthSessionCoordinator(memoryVault(), async () => ({ status: 'unavailable' }))
  const guard = coordinator.captureInstallGuard()
  let called = 0
  const unsubscribe = coordinator.subscribeInstallGuardInvalidation(guard, () => { called++ })
  coordinator.subscribeInstallGuardInvalidation(guard, () => { throw new Error('synthetic listener failure') })
  let removed = 0
  const removeOther = coordinator.subscribeInstallGuardInvalidation(guard, () => { removed++ })
  removeOther()
  const pending = coordinator.logout()
  assert.equal(called, 1)
  assert.equal(removed, 0)
  assert.equal((await pending).status, 'signed_out')
  unsubscribe()
  await coordinator.logout()
  assert.equal(called, 1)
  assert.throws(() => coordinator.subscribeInstallGuardInvalidation(guard, () => {}), /session guard unavailable/)
  assert.throws(() => coordinator.subscribeInstallGuardInvalidation({}, () => {}), /session guard unavailable/)
})

test('a reentrant logout from guard invalidation supersedes the earlier install', async () => {
  const coordinator = new AuthSessionCoordinator(memoryVault(), async () => ({ status: 'unavailable' }))
  const guard = coordinator.captureInstallGuard()
  let logout
  coordinator.subscribeInstallGuardInvalidation(guard, () => { logout = coordinator.logout() })
  const credential = await authenticated('101', 'synthetic.guard.token')
  assert.equal((await coordinator.installAuthenticated(credential)).status, 'superseded')
  assert.equal((await logout).status, 'signed_out')
  assert.equal(coordinator.current(), null)
})

test('a reentrant session change and unsubscribe do not notify the same watcher twice', async () => {
  const coordinator = new AuthSessionCoordinator(memoryVault(), async () => ({ status: 'unavailable' }))
  const firstGuard = coordinator.captureInstallGuard()
  const secondGuard = coordinator.captureInstallGuard()
  let secondCalls = 0
  let removedCalls = 0
  let nested
  let removeThird
  coordinator.subscribeInstallGuardInvalidation(firstGuard, () => { removeThird(); nested = coordinator.logout() })
  coordinator.subscribeInstallGuardInvalidation(secondGuard, () => { secondCalls++ })
  removeThird = coordinator.subscribeInstallGuardInvalidation(secondGuard, () => { removedCalls++ })
  const outer = coordinator.logout()
  assert.equal((await outer).status, 'superseded')
  assert.equal((await nested).status, 'signed_out')
  assert.equal(secondCalls, 1)
  assert.equal(removedCalls, 0)
})

test('a reentrant logout on guarded install completion cannot publish stale success', async () => {
  const coordinator = new AuthSessionCoordinator(memoryVault(), async () => ({ status: 'unavailable' }))
  const guard = coordinator.captureInstallGuard()
  let logout
  coordinator.subscribeInstallGuardInvalidation(guard, () => { logout = coordinator.logout() })
  const credential = await authenticated('101', 'synthetic.guard.token')
  assert.equal((await coordinator.installAuthenticatedIfCurrent(credential, guard)).status, 'superseded')
  assert.equal((await logout).status, 'signed_out')
  assert.equal(coordinator.current(), null)
})

test('logout while a guarded install writes cannot renew its old guard', async () => {
  const store = memoryVault()
  const writing = deferred()
  const originalWrite = store.writeAuthenticationState.bind(store)
  store.writeAuthenticationState = async value => {
    if (value.includes('accessToken')) await writing.promise
    return originalWrite(value)
  }
  const coordinator = new AuthSessionCoordinator(store, liveVerifier('101', 'synthetic.guard.token'))
  const guard = coordinator.captureInstallGuard()
  const installation = coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'synthetic.guard.token'), guard)
  await new Promise(resolve => setImmediate(resolve))
  const logout = coordinator.logout()
  writing.resolve()
  assert.equal((await installation).status, 'superseded')
  assert.equal((await logout).status, 'signed_out')
  assert.equal(coordinator.isInstallGuardCurrent(guard), false)
  assert.equal(coordinator.current(), null)
  assert.equal(store.value, '')
})

test('cancelling an unused guard leaves an already authenticated session untouched', async () => {
  const vault = memoryVault()
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  await coordinator.installAuthenticated(await authenticated('101', 'existing.synthetic'))
  const original = coordinator.snapshot()
  const guard = coordinator.captureInstallGuard()
  let invalidated = 0
  coordinator.subscribeInstallGuardInvalidation(guard, () => { invalidated++ })
  assert.equal(await coordinator.cancelInstallGuard(guard), 'cancelled')
  assert.equal(invalidated, 1)
  assert.equal(coordinator.isCurrent(original.subjectId, original.generation), true)
  assert.equal(JSON.parse(vault.value).accessToken, 'existing.synthetic')
  assert.equal((await coordinator.installAuthenticatedIfCurrent(await authenticated('202', 'late.synthetic'), guard)).status, 'superseded')
})

test('cancelling a guarded install during a vault write removes its late credential', async () => {
  const vault = memoryVault()
  const writeStarted = deferred()
  const releaseWrite = deferred()
  const write = vault.writeAuthenticationState.bind(vault)
  vault.writeAuthenticationState = async value => {
    if (value.includes('accessToken')) { writeStarted.resolve(); await releaseWrite.promise }
    return write(value)
  }
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  const guard = coordinator.captureInstallGuard()
  const installing = coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'late.synthetic'), guard)
  await writeStarted.promise
  const cancelling = coordinator.cancelInstallGuard(guard)
  assert.equal(coordinator.current(), null)
  releaseWrite.resolve()
  assert.equal((await installing).status, 'superseded')
  assert.equal(await cancelling, 'cancelled')
  assert.equal(coordinator.current(), null)
  assert.equal(vault.value, '')
})

test('cancelling an older guard cannot clear a newer account installation', async () => {
  const vault = memoryVault()
  const writeStarted = deferred()
  const releaseWrite = deferred()
  const write = vault.writeAuthenticationState.bind(vault)
  vault.writeAuthenticationState = async value => {
    if (value.includes('old.synthetic')) { writeStarted.resolve(); await releaseWrite.promise }
    return write(value)
  }
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  const guard = coordinator.captureInstallGuard()
  const old = coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'old.synthetic'), guard)
  await writeStarted.promise
  const newer = coordinator.installAuthenticated(await authenticated('202', 'new.synthetic'))
  assert.equal(await coordinator.cancelInstallGuard(guard), 'cancelled')
  releaseWrite.resolve()
  assert.equal((await old).status, 'superseded')
  assert.equal((await newer).status, 'authenticated')
  assert.equal(coordinator.snapshot().subjectId, '202')
  assert.equal(JSON.parse(vault.value).accessToken, 'new.synthetic')
})

test('a new account started during cancellation survives old vault cleanup', async () => {
  const vault = memoryVault()
  const writeStarted = deferred()
  const releaseWrite = deferred()
  const write = vault.writeAuthenticationState.bind(vault)
  vault.writeAuthenticationState = async value => {
    if (value.includes('old.synthetic')) { writeStarted.resolve(); await releaseWrite.promise }
    return write(value)
  }
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  const guard = coordinator.captureInstallGuard()
  const oldCredential = await authenticated('101', 'old.synthetic')
  const newCredential = await authenticated('202', 'new.synthetic')
  const old = coordinator.installAuthenticatedIfCurrent(oldCredential, guard)
  await writeStarted.promise
  const cancelling = coordinator.cancelInstallGuard(guard)
  const newer = coordinator.installAuthenticated(newCredential)
  releaseWrite.resolve()
  assert.equal((await old).status, 'superseded')
  assert.equal(await cancelling, 'cancelled')
  assert.equal((await newer).status, 'authenticated')
  assert.equal(coordinator.snapshot().subjectId, '202')
  assert.equal(JSON.parse(vault.value).accessToken, 'new.synthetic')
})

test('a restore queued after cancellation cannot revive the cancelled credential', async () => {
  const vault = memoryVault()
  const writeStarted = deferred()
  const releaseWrite = deferred()
  const write = vault.writeAuthenticationState.bind(vault)
  vault.writeAuthenticationState = async value => {
    if (value.includes('cancelled.synthetic')) { writeStarted.resolve(); await releaseWrite.promise }
    return write(value)
  }
  let verifications = 0
  const coordinator = new AuthSessionCoordinator(vault, async () => { verifications++; return { status: 'valid', user: user('101') } })
  const guard = coordinator.captureInstallGuard()
  const installing = coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'cancelled.synthetic'), guard)
  await writeStarted.promise
  const cancelling = coordinator.cancelInstallGuard(guard)
  const restoring = coordinator.restore()
  releaseWrite.resolve()
  assert.equal((await installing).status, 'superseded')
  assert.equal(await cancelling, 'cancelled')
  assert.deepEqual(await restoring, { status: 'signed_out', reason: 'missing' })
  assert.equal(verifications, 0)
  assert.equal(vault.value, '')
})

test('cancellation also supersedes a restore already queued behind that install', async () => {
  const vault = memoryVault()
  const writeStarted = deferred()
  const releaseWrite = deferred()
  const write = vault.writeAuthenticationState.bind(vault)
  vault.writeAuthenticationState = async value => {
    if (value.includes('cancelled.synthetic')) { writeStarted.resolve(); await releaseWrite.promise }
    return write(value)
  }
  let verifications = 0
  const coordinator = new AuthSessionCoordinator(vault, async () => { verifications++; return { status: 'valid', user: user('101') } })
  const guard = coordinator.captureInstallGuard()
  const installing = coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'cancelled.synthetic'), guard)
  await writeStarted.promise
  const firstRestore = coordinator.restore()
  const secondRestore = coordinator.restore()
  const cancelling = coordinator.cancelInstallGuard(guard)
  releaseWrite.resolve()
  assert.equal((await installing).status, 'superseded')
  assert.equal((await firstRestore).status, 'superseded')
  assert.equal((await secondRestore).status, 'superseded')
  assert.equal(await cancelling, 'cancelled')
  assert.equal(coordinator.current(), null)
  assert.equal(verifications, 0)
  assert.equal(vault.value, '')
})

test('logout retirement is ordered before a subsequent restore', async () => {
  const token = 'existing.synthetic.token'
  const vault = memoryVault(JSON.stringify({ schema: 1, subjectId: '101', accessToken: token }))
  let verifications = 0
  const coordinator = new AuthSessionCoordinator(vault, async () => { verifications++; return { status: 'valid', user: user('101') } })
  const loggingOut = coordinator.logout()
  const restoring = coordinator.restore()
  assert.equal((await loggingOut).status, 'superseded')
  assert.deepEqual(await restoring, { status: 'signed_out', reason: 'missing' })
  assert.equal(verifications, 0)
  assert.equal(vault.value, '')
})

test('a reentrant restore from cancellation notification waits behind cleanup', async () => {
  const vault = memoryVault()
  const writeStarted = deferred()
  const releaseWrite = deferred()
  const write = vault.writeAuthenticationState.bind(vault)
  vault.writeAuthenticationState = async value => {
    if (value.includes('cancelled.synthetic')) { writeStarted.resolve(); await releaseWrite.promise }
    return write(value)
  }
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'valid', user: user('101') }))
  const guard = coordinator.captureInstallGuard()
  let restoring
  coordinator.subscribeInstallGuardInvalidation(guard, () => { restoring = coordinator.restore() })
  const installing = coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'cancelled.synthetic'), guard)
  await writeStarted.promise
  const cancelling = coordinator.cancelInstallGuard(guard)
  releaseWrite.resolve()
  assert.equal((await installing).status, 'superseded')
  assert.equal(await cancelling, 'cancelled')
  assert.deepEqual(await restoring, { status: 'signed_out', reason: 'missing' })
  assert.equal(vault.value, '')
})

test('failed cancellation cleanup blocks queued restore from activating the cancelled token', async () => {
  const vault = memoryVault()
  const writeStarted = deferred()
  const releaseWrite = deferred()
  const write = vault.writeAuthenticationState.bind(vault)
  let failCleanup = false
  vault.writeAuthenticationState = async value => {
    if (value.includes('cancelled.synthetic')) { writeStarted.resolve(); await releaseWrite.promise; return write(value) }
    if (failCleanup) return { ok: false, errorCode: 'unavailable', found: false, value: '' }
    return write(value)
  }
  const clear = vault.clearAuthenticationState.bind(vault)
  vault.clearAuthenticationState = () => failCleanup
    ? { ok: false, errorCode: 'unavailable', found: false, value: '' } : clear()
  let verifications = 0
  const coordinator = new AuthSessionCoordinator(vault, async () => { verifications++; return { status: 'valid', user: user('101') } })
  const guard = coordinator.captureInstallGuard()
  const installing = coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'cancelled.synthetic'), guard)
  await writeStarted.promise
  failCleanup = true
  const cancelling = coordinator.cancelInstallGuard(guard)
  const restoring = coordinator.restore()
  releaseWrite.resolve()
  assert.equal((await installing).status, 'superseded')
  assert.equal(await cancelling, 'unavailable')
  assert.deepEqual(await restoring, { status: 'unavailable', reason: 'storage' })
  assert.equal(verifications, 0)
  assert.equal(coordinator.current(), null)
  assert.equal(JSON.parse(vault.value).accessToken, 'cancelled.synthetic')
  failCleanup = false
  assert.equal(await coordinator.cancelInstallGuard(guard), 'cancelled')
  assert.equal(vault.value, '')
  assert.deepEqual(await coordinator.restore(), { status: 'signed_out', reason: 'missing' })
})

test('cancellation retries a partially written credential when install cleanup failed', async () => {
  const vault = memoryVault()
  const write = vault.writeAuthenticationState.bind(vault)
  const clear = vault.clearAuthenticationState.bind(vault)
  let failCleanup = false
  vault.writeAuthenticationState = value => {
    if (value.includes('partial.synthetic')) {
      write(value) // platform wrote the value but reported failure
      failCleanup = true
      return { ok: false, errorCode: 'unavailable', found: false, value: '' }
    }
    return failCleanup ? { ok: false, errorCode: 'unavailable', found: false, value: '' } : write(value)
  }
  vault.clearAuthenticationState = () => failCleanup
    ? { ok: false, errorCode: 'unavailable', found: false, value: '' } : clear()
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'valid', user: user('101') }))
  const guard = coordinator.captureInstallGuard()
  assert.equal((await coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'partial.synthetic'), guard)).status, 'unavailable')
  assert.equal(await coordinator.cancelInstallGuard(guard), 'unavailable')
  assert.equal(JSON.parse(vault.value).accessToken, 'partial.synthetic')
  failCleanup = false
  assert.equal(await coordinator.cancelInstallGuard(guard), 'cancelled')
  assert.equal(vault.value, '')
  assert.deepEqual(await coordinator.restore(), { status: 'signed_out', reason: 'missing' })
})

test('retrying failed cancellation never clears an intervening successful login', async () => {
  const vault = memoryVault()
  const write = vault.writeAuthenticationState.bind(vault)
  const clear = vault.clearAuthenticationState.bind(vault)
  let failCleanup = false
  vault.writeAuthenticationState = value => failCleanup && !value.includes('accessToken')
    ? { ok: false, errorCode: 'unavailable', found: false, value: '' } : write(value)
  vault.clearAuthenticationState = () => failCleanup
    ? { ok: false, errorCode: 'unavailable', found: false, value: '' } : clear()
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  const guard = coordinator.captureInstallGuard()
  assert.equal((await coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'old.synthetic'), guard)).status, 'authenticated')
  failCleanup = true
  assert.equal(await coordinator.cancelInstallGuard(guard), 'unavailable')
  failCleanup = false
  assert.equal((await coordinator.installAuthenticated(await authenticated('202', 'new.synthetic'))).status, 'authenticated')
  assert.equal(await coordinator.cancelInstallGuard(guard), 'superseded')
  assert.equal(coordinator.snapshot().subjectId, '202')
  assert.equal(JSON.parse(vault.value).accessToken, 'new.synthetic')
})

test('cancelling a just-completed guarded install revokes only that installed session', async () => {
  const vault = memoryVault()
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  const guard = coordinator.captureInstallGuard()
  assert.equal((await coordinator.installAuthenticatedIfCurrent(await authenticated('101', 'installed.synthetic'), guard)).status, 'authenticated')
  const cancellation = coordinator.cancelInstallGuard(guard)
  assert.equal(coordinator.current(), null)
  assert.equal(await cancellation, 'cancelled')
  assert.equal(vault.value, '')
})

test('approval result, copied result, and malformed authenticated data cannot be installed', async () => {
  const vault = memoryVault()
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  const approval = await new EmailAuthApi('https://api.example.test', async () => ({
    status: 200, headers: { 'Cache-Control': 'no-store' },
    body: { status: 'approval_required', approvalId: 'a'.repeat(24), approvalSecret: 's'.repeat(43),
      nonce: 'n'.repeat(43), comparisonCode: '1234', expiresAt: '2026-10-31T12:00:00Z' },
  })).login({ challengeId: 'c'.repeat(24), code: '123456', deviceProof: `${'a'.repeat(15)}.${'b'.repeat(15)}.${'c'.repeat(15)}` }, 'A'.repeat(43))
  assert.equal((await coordinator.installAuthenticated(approval)).status, 'rejected')
  const success = await authenticated('101', 'synthetic.token')
  assert.equal((await coordinator.installAuthenticated({ ...success })).status, 'rejected')
  assert.equal((await coordinator.installAuthenticated({ ...success, expiresIn: 0 })).status, 'rejected')
  assert.deepEqual(vault.calls, [])
  assert.equal(coordinator.current(), null)
})

test('restore activates only after server verification and does not derive expiry from expiresIn', async () => {
  const token = 'restored.synthetic.token'
  const vault = memoryVault(JSON.stringify({ schema: 1, subjectId: '202', accessToken: token }))
  const seen = []
  const coordinator = new AuthSessionCoordinator(vault, async (candidate, subject) => {
    seen.push([candidate, subject])
    return { status: 'valid', user: user('202') }
  })
  const result = await coordinator.restore()
  assert.equal(result.status, 'authenticated')
  assert.deepEqual(seen, [[token, '202']])
  assert.equal(coordinator.current().accessToken, token)
  assert.ok(!Object.hasOwn(JSON.parse(vault.value), 'expiresIn'))
})

test('missing, expired, revoked, malformed, and temporarily unverifiable vault entries fail closed', async t => {
  const absent = new AuthSessionCoordinator(memoryVault(), async () => ({ status: 'unavailable' }))
  assert.deepEqual(await absent.restore(), { status: 'signed_out', reason: 'missing' })

  for (const status of ['expired', 'revoked']) {
    const vault = memoryVault(JSON.stringify({ schema: 1, subjectId: '303', accessToken: 'expired.synthetic' }))
    const coordinator = new AuthSessionCoordinator(vault, async () => ({ status }))
    assert.deepEqual(await coordinator.restore(), { status: 'signed_out', reason: 'expired' })
    assert.equal(vault.value, '')
    assert.equal(coordinator.current(), null)
  }

  const malformedVault = memoryVault('{"schema":1,"subjectId":"303","accessToken":"a","accessToken":"b"}')
  const malformed = new AuthSessionCoordinator(malformedVault, async () => ({ status: 'valid', user: user('303') }))
  assert.deepEqual(await malformed.restore(), { status: 'signed_out', reason: 'invalid' })
  assert.equal(malformedVault.value, '')

  const token = 'not-confirmed.synthetic'
  const temporaryVault = memoryVault(JSON.stringify({ schema: 1, subjectId: '303', accessToken: token }))
  const temporary = new AuthSessionCoordinator(temporaryVault, async () => ({ status: 'unavailable' }))
  assert.deepEqual(await temporary.restore(), { status: 'unavailable', reason: 'verification' })
  assert.equal(temporary.current(), null)
  assert.notEqual(temporaryVault.value, '')
  t.diagnostic('Unavailable verifier preserves the OS-vault record but exposes no credential.')
})

test('vault failures and verifier exceptions are redacted and leave no active credential', async () => {
  const secret = 'synthetic.private.token'
  const brokenVault = {
    readAuthenticationState: () => { throw new Error(secret) },
    writeAuthenticationState: () => ({ ok: false, errorCode: secret, found: false, value: '' }),
    clearAuthenticationState: () => ({ ok: false, errorCode: secret, found: false, value: '' }),
  }
  const coordinator = new AuthSessionCoordinator(brokenVault, async () => { throw new Error(secret) })
  const installed = await coordinator.installAuthenticated(await authenticated('404', secret))
  assert.deepEqual(installed, { status: 'unavailable', reason: 'storage' })
  assert.ok(!JSON.stringify(installed).includes(secret))
  assert.equal(coordinator.current(), null)
  assert.deepEqual(await coordinator.restore(), { status: 'unavailable', reason: 'storage' })
  assert.deepEqual(await coordinator.logout(), { status: 'unavailable', reason: 'storage' })
})

test('a persisted signed-out tombstone prevents restoration when vault deletion fails', async () => {
  let value = JSON.stringify({ schema: 1, subjectId: '455', accessToken: 'old.synthetic.token' })
  const repository = {
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: true, value }),
    writeAuthenticationState: next => {
      value = next
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    clearAuthenticationState: () => ({ ok: false, errorCode: 'unavailable', found: false, value: '' }),
  }
  const original = new AuthSessionCoordinator(repository, async () => ({ status: 'valid', user: user('455') }))
  await original.restore()
  assert.deepEqual(await original.logout(), { status: 'signed_out', reason: 'logged_out' })
  assert.deepEqual(JSON.parse(value), { schema: 1, signedOut: true })
  let verified = false
  const afterRestart = new AuthSessionCoordinator(repository, async () => {
    verified = true
    return { status: 'valid', user: user('455') }
  })
  assert.deepEqual(await afterRestart.restore(), { status: 'signed_out', reason: 'logged_out' })
  assert.equal(verified, false)
  assert.equal(afterRestart.current(), null)
})

test('switch and logout invalidate old leases immediately and stale expiry cannot clear a newer account', async () => {
  const vault = memoryVault()
  const coordinator = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  await coordinator.installAuthenticated(await authenticated('501', 'first.synthetic'))
  const old = coordinator.current()
  const oldLease = coordinator.draftLease()
  const switching = coordinator.installAuthenticated(await authenticated('502', 'second.synthetic'))
  assert.equal(coordinator.current(), null)
  assert.equal(oldLease.isCurrent('501', old.generation), false)
  assert.equal((await switching).status, 'authenticated')
  const current = coordinator.current()
  assert.equal((await coordinator.invalidateIfCurrent(old.subjectId, old.generation)).status, 'superseded')
  assert.equal(coordinator.current().accessToken, 'second.synthetic')
  const loggingOut = coordinator.logout()
  assert.equal(coordinator.current(), null)
  assert.equal(coordinator.isCurrent(current.subjectId, current.generation), false)
  assert.deepEqual(await loggingOut, { status: 'signed_out', reason: 'logged_out' })
})

test('a superseded login write cannot publish or overwrite a later account', async () => {
  const gate = deferred()
  const vault = memoryVault()
  let block = false
  const repository = {
    readAuthenticationState: () => vault.readAuthenticationState(),
    clearAuthenticationState: () => vault.clearAuthenticationState(),
    writeAuthenticationState: async value => {
      if (block) await gate.promise
      return vault.writeAuthenticationState(value)
    },
  }
  const coordinator = new AuthSessionCoordinator(repository, async () => ({ status: 'unavailable' }))
  block = true
  const first = coordinator.installAuthenticated(await authenticated('601', 'first.pending'))
  await Promise.resolve()
  const second = coordinator.installAuthenticated(await authenticated('602', 'second.wins'))
  gate.resolve()
  assert.equal((await first).status, 'superseded')
  assert.equal((await second).status, 'authenticated')
  assert.equal(coordinator.snapshot().subjectId, '602')
  assert.equal(JSON.parse(vault.value).accessToken, 'second.wins')
})

test('logout during a pending restore prevents stale verification from resurrecting the old session', async () => {
  const token = 'restore.pending.synthetic'
  const vault = memoryVault(JSON.stringify({ schema: 1, subjectId: '701', accessToken: token }))
  const gate = deferred()
  const coordinator = new AuthSessionCoordinator(vault, async () => gate.promise)
  const restoring = coordinator.restore()
  await Promise.resolve()
  const logout = coordinator.logout()
  gate.resolve({ status: 'valid', user: user('701') })
  assert.equal((await restoring).status, 'superseded')
  assert.deepEqual(await logout, { status: 'signed_out', reason: 'logged_out' })
  assert.equal(coordinator.current(), null)
  assert.equal(vault.value, '')
})
