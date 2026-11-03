import assert from 'node:assert/strict'
import { test } from 'node:test'
import { AuthSessionCoordinator } from './sessionCoordinator.ts'
import { AppEntrySession } from './appEntrySession.ts'

function deferred() {
  let resolve
  const promise = new Promise(done => { resolve = done })
  return { promise, resolve }
}

async function setup({ adult = true, clear = async () => {} } = {}) {
  let stored = JSON.stringify({ schema: 1, subjectId: '101', accessToken: 'synthetic-token' })
  const user = { id: '101', nickname: '', roles: ['student'], ageVerified: adult }
  const session = new AuthSessionCoordinator({
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: !!stored, value: stored }),
    writeAuthenticationState: value => { stored = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState: async () => { await clear(); stored = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }, async () => ({ status: 'valid', user }))
  const restored = await session.restore()
  assert.equal(restored.status, 'authenticated')
  return { session, restored: restored.session }
}

test('the entry hides the previous account as soon as logout starts, before secure storage finishes', async () => {
  const clear = deferred()
  const { session, restored } = await setup({ clear: () => clear.promise })
  let invalidated = 0
  const entry = new AppEntrySession(session, () => { invalidated++ })
  assert.equal(entry.accept(restored), true)
  const logout = session.logout()
  assert.equal(invalidated, 1)
  assert.equal(session.snapshot(), null)
  assert.equal(entry.accept(restored), false)
  clear.resolve()
  assert.equal((await logout).status, 'signed_out')
  entry.dispose()
})

test('a stale session cannot open the entry after the same account is restored again', async () => {
  const { session, restored } = await setup()
  let invalidated = 0
  const entry = new AppEntrySession(session, () => { invalidated++ })
  assert.equal(entry.accept(restored), true)
  const next = await session.restore()
  assert.equal(next.status, 'authenticated')
  assert.equal(invalidated, 1)
  assert.notEqual(next.session.generation, restored.generation)
  assert.equal(entry.accept(restored), false)
  assert.equal(entry.accept(next.session), true)
  await session.logout()
  assert.equal(invalidated, 2)
  entry.dispose()
})

test('the entry rejects missing adult confirmation and releases its subscription on dispose', async () => {
  const first = await setup({ adult: false })
  const underReview = new AppEntrySession(first.session, () => assert.fail('not accepted'))
  assert.equal(underReview.accept(first.restored), false)
  underReview.dispose()

  const second = await setup()
  let invalidated = 0
  const entry = new AppEntrySession(second.session, () => { invalidated++ })
  assert.equal(entry.accept(second.restored), true)
  entry.dispose()
  assert.equal(entry.accept(second.restored), false)
  await second.session.logout()
  assert.equal(invalidated, 0)
})

test('clearing the entry watch permits an authoritative recheck without interrupting it', async () => {
  const { session, restored } = await setup()
  let invalidated = 0
  const entry = new AppEntrySession(session, () => { invalidated++ })
  assert.equal(entry.accept(restored), true)
  entry.clear()
  const checked = await session.restore()
  assert.equal(checked.status, 'authenticated')
  assert.equal(invalidated, 0)
  assert.equal(entry.accept(checked.session), true)
  entry.dispose()
})
