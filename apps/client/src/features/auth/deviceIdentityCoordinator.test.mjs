import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { inspect } from 'node:util'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import {
  DeviceIdentityCoordinator,
  DeviceIdentityRegistrySnapshot,
  EmailRegistryKey,
  ReservedDeviceAlias,
  aliasFromRandomBytes,
  normalizeEmailForDeviceRegistry,
} from './deviceIdentityCoordinator.ts'

const publicKey = Object.freeze({ kty: 'EC', crv: 'P-256', x: 'A'.repeat(43), y: 'Q'.repeat(43) })
const emailVectors = JSON.parse(readFileSync(fileURLToPath(new URL(
  '../../../../../docs/evidence/P02/P02-12/email-normalize-vectors.json', import.meta.url,
)), 'utf8'))
const deferred = () => {
  let resolve
  let reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function canonicalAlias(byte) {
  return aliasFromRandomBytes(new Uint8Array(32).fill(byte))
}

function aliasValue(alias) { return alias?.forDeviceKeyBridge() }

function memoryRegistry() {
  const rows = new Map()
  let revision = 0
  let nextActivationConflict = false
  const calls = { reads: 0, writes: 0, transitions: [] }
  return {
    rows,
    calls,
    conflictNextActivation() { nextActivationConflict = true },
    async read(key) {
      calls.reads++
      const normalized = key.forProtectedRegistry()
      const row = rows.get(normalized)
      if (!row) return DeviceIdentityRegistrySnapshot.missing(String(revision))
      return row.status === 'active'
        ? DeviceIdentityRegistrySnapshot.active(String(revision), row.alias)
        : DeviceIdentityRegistrySnapshot.reserved(String(revision), row.alias)
    },
    async compareAndSet(key, expected, nextStatus, alias) {
      calls.writes++
      calls.transitions.push(nextStatus)
      const normalized = key.forProtectedRegistry()
      if (nextStatus === 'active' && nextActivationConflict) {
        nextActivationConflict = false
        rows.set('other@example.com', { status: 'active', alias: aliasValue(canonicalAlias(45)) })
        revision++
        return 'conflict'
      }
      if (expected.revisionForCompareAndSet() !== String(revision)) return 'conflict'
      const current = rows.get(normalized)
      if (nextStatus === 'reserved') {
        if (expected.status !== 'missing' || current || [...rows.values()].some(row => row.alias === aliasValue(alias))) {
          return 'conflict'
        }
        rows.set(normalized, { status: 'reserved', alias: aliasValue(alias) })
      } else {
        if (expected.status !== 'reserved' || current?.status !== 'reserved' ||
          current.alias !== aliasValue(alias) || expected.aliasForCoordinator()?.forDeviceKeyBridge() !== aliasValue(alias)) {
          return 'conflict'
        }
        rows.set(normalized, { status: 'active', alias: aliasValue(alias) })
      }
      revision++
      return 'stored'
    },
  }
}

function memoryDeviceKeys() {
  const keys = new Map()
  const calls = { creates: [], reads: [] }
  let createOverride
  return {
    keys,
    calls,
    setCreateOverride(value) { createOverride = value },
    async createAtReservedAlias(alias) {
      const value = aliasValue(alias)
      calls.creates.push(value)
      const answer = await createOverride?.(value)
      if (answer) return answer
      keys.set(value, publicKey)
      return { status: 'ready', alias: new ReservedDeviceAlias(value), publicKey }
    },
    async readExisting(alias) {
      const value = aliasValue(alias)
      calls.reads.push(value)
      if (!keys.has(value)) return { status: 'missing' }
      return { status: 'ready', alias: new ReservedDeviceAlias(value), publicKey: keys.get(value) }
    },
  }
}

function sequenceRandom(...aliases) {
  let next = 0
  return {
    get calls() { return next },
    async generateAlias() { return aliases[next++] ?? canonicalAlias(250) },
  }
}

test('shared email normalization vectors match service behavior and reject quoted locals', () => {
  const expand = (value, parts) => parts
    ? `${parts.local}@${parts.domainChar.repeat(parts.domainLength)}`
    : value
  for (const vector of emailVectors) {
    const result = normalizeEmailForDeviceRegistry(expand(vector.input, vector.inputParts))
    const clientAccept = vector.clientAccept ?? vector.accept
    assert.equal(result !== null, clientAccept, vector.name)
    if (clientAccept) {
      const normalized = result.forProtectedRegistry()
      assert.equal(normalized, expand(vector.expected, vector.expectedParts), vector.name)
      const stable = normalizeEmailForDeviceRegistry(normalized)
      assert.notEqual(stable, null, `${vector.name} normalized output must be accepted again`)
      assert.equal(stable.forProtectedRegistry(), normalized, `${vector.name} normalization must be idempotent`)
    }
  }
  assert.equal(JSON.stringify(normalizeEmailForDeviceRegistry('secret@example.com')), '"<redacted>"')
  assert.equal(aliasFromRandomBytes(new Uint8Array(31)), null)
  assert.equal(aliasValue(aliasFromRandomBytes(new Uint8Array(32).fill(0))).length, 43)
  assert.match(aliasValue(aliasFromRandomBytes(new Uint8Array(32).fill(1))), /E$/)
})

test('explicit first use reserves before create and serializes same-account initialization', async () => {
  const registry = memoryRegistry()
  const keys = memoryDeviceKeys()
  const random = sequenceRandom(canonicalAlias(1))
  keys.setCreateOverride(async alias => {
    const row = registry.rows.get('Name+tag@example.com')
    assert.equal(row?.status, 'reserved')
    assert.equal(row?.alias, alias)
    return undefined
  })
  const coordinator = new DeviceIdentityCoordinator(registry, random, keys)
  const [first, second] = await Promise.all([
    coordinator.initializeFirstForEmail('Name+tag@EXAMPLE.COM'),
    coordinator.initializeFirstForEmail('Name+tag@example.com'),
  ])

  assert.equal(first.status, 'ready')
  assert.equal(second.status, 'ready')
  assert.equal(first.identity.aliasForProtocol, second.identity.aliasForProtocol)
  assert.equal(random.calls, 1)
  assert.equal(keys.calls.creates.length, 1)
  assert.equal(keys.calls.reads.length, 1)
  assert.equal(registry.rows.size, 1)
  assert.equal(registry.rows.get('Name+tag@example.com').status, 'active')
  assert.deepEqual(JSON.parse(JSON.stringify(first)), { status: 'ready', identity: { status: 'ready' } })
  assert.equal(String(first.identity), '<redacted device identity>')
  assert.equal(inspect(new EmailRegistryKey('Name+tag@example.com')), 'EmailRegistryKey {}')
  assert.equal(String(canonicalAlias(1)), '<redacted device alias>')
  assert.equal(JSON.stringify(canonicalAlias(1)), '"<redacted>"')
  assert.deepEqual(JSON.parse(JSON.stringify(DeviceIdentityRegistrySnapshot.active('1', canonicalAlias(1)))), { status: 'active' })
  assert.deepEqual(JSON.parse(JSON.stringify(DeviceIdentityRegistrySnapshot.reserved('1', canonicalAlias(1)))), { status: 'reserved' })
})

test('registry write failure or indeterminate reservation never creates a key', async () => {
  for (const writeResult of ['unavailable', 'indeterminate']) {
    const registry = memoryRegistry()
    registry.compareAndSet = async () => writeResult
    const keys = memoryDeviceKeys()
    const coordinator = new DeviceIdentityCoordinator(registry, sequenceRandom(canonicalAlias(2)), keys)
    assert.deepEqual(await coordinator.initializeFirstForEmail('a@example.com'), { status: 'storage_unavailable' })
    assert.equal(keys.calls.creates.length, 0)
  }
  assert.deepEqual(await new DeviceIdentityCoordinator({
    async read() { return DeviceIdentityRegistrySnapshot.missing('0') },
    async compareAndSet() { throw new Error('private storage detail') },
  }, sequenceRandom(canonicalAlias(3)), memoryDeviceKeys()).initializeFirstForEmail('a@example.com'), {
    status: 'storage_unavailable',
  })
})

test('a candidate alias already owned by another email is rejected without key creation', async () => {
  const registry = memoryRegistry()
  const alias = canonicalAlias(12)
  registry.rows.set('first@example.com', { status: 'active', alias: aliasValue(alias) })
  const keys = memoryDeviceKeys()
  const coordinator = new DeviceIdentityCoordinator(registry, sequenceRandom(alias), keys)
  assert.deepEqual(await coordinator.initializeFirstForEmail('second@example.com'), { status: 'reservation_conflict' })
  assert.equal(keys.calls.creates.length, 0)
})

test('uncertain native create remains reserved and restart never adopts an existing key', async () => {
  const registry = memoryRegistry()
  const keys = memoryDeviceKeys()
  const alias = canonicalAlias(4)
  keys.setCreateOverride(async value => {
    keys.keys.set(value, publicKey) // Native operation may have succeeded before its result became unknown.
    return { status: 'unavailable' }
  })
  const firstCoordinator = new DeviceIdentityCoordinator(registry, sequenceRandom(alias), keys)
  assert.deepEqual(await firstCoordinator.initializeFirstForEmail('a@example.com'), { status: 'creation_uncertain' })
  assert.deepEqual(keys.calls.creates, [aliasValue(alias)])
  assert.equal(registry.rows.get('a@example.com').status, 'reserved')

  const restartRandom = sequenceRandom(canonicalAlias(5))
  const restarted = new DeviceIdentityCoordinator(registry, restartRandom, keys)
  assert.deepEqual(await restarted.readForEmail('a@example.com'), { status: 'reservation_incomplete' })
  assert.deepEqual(await restarted.initializeFirstForEmail('a@example.com'), { status: 'reservation_incomplete' })
  assert.equal(restartRandom.calls, 0)
  assert.equal(keys.calls.reads.length, 0)
  assert.deepEqual(keys.calls.creates, [aliasValue(alias)])
})

test('native alias conflict persists a reservation that restart cannot upgrade by reading the key', async () => {
  const registry = memoryRegistry()
  const keys = memoryDeviceKeys()
  const alias = canonicalAlias(13)
  keys.keys.set(aliasValue(alias), publicKey) // The conflicting key may belong to a different identity.
  keys.setCreateOverride(async () => ({ status: 'alias_conflict' }))
  const coordinator = new DeviceIdentityCoordinator(registry, sequenceRandom(alias), keys)
  assert.deepEqual(await coordinator.initializeFirstForEmail('conflict@example.com'), { status: 'creation_conflict' })
  assert.equal(registry.rows.get('conflict@example.com').status, 'reserved')
  assert.deepEqual(keys.calls.creates, [aliasValue(alias)])

  const restarted = new DeviceIdentityCoordinator(registry, sequenceRandom(canonicalAlias(14)), keys)
  assert.deepEqual(await restarted.readForEmail('conflict@example.com'), { status: 'reservation_incomplete' })
  assert.deepEqual(await restarted.initializeFirstForEmail('conflict@example.com'), { status: 'reservation_incomplete' })
  assert.equal(keys.calls.reads.length, 0)
  assert.equal(keys.calls.creates.length, 1)
})

test('crash after reservation with a missing key remains reserved and never creates on restart', async () => {
  const registry = memoryRegistry()
  const keys = memoryDeviceKeys()
  const alias = canonicalAlias(6)
  const reservation = DeviceIdentityRegistrySnapshot.missing('0')
  assert.equal(await registry.compareAndSet(new EmailRegistryKey('a@example.com'), reservation,
    'reserved', alias), 'stored')
  const random = sequenceRandom(canonicalAlias(7))
  const coordinator = new DeviceIdentityCoordinator(registry, random, keys)
  assert.deepEqual(await coordinator.initializeFirstForEmail('a@example.com'), { status: 'reservation_incomplete' })
  assert.equal(random.calls, 0)
  assert.equal(keys.calls.creates.length, 0)
  assert.equal(keys.calls.reads.length, 0)
})

test('a successful create is not ready until active state CAS is confirmed', async () => {
  const registry = memoryRegistry()
  const keys = memoryDeviceKeys()
  const baseCompare = registry.compareAndSet.bind(registry)
  registry.compareAndSet = async (key, expected, nextStatus, alias) => {
    if (nextStatus === 'active') return 'indeterminate'
    return baseCompare(key, expected, nextStatus, alias)
  }
  const alias = canonicalAlias(15)
  const coordinator = new DeviceIdentityCoordinator(registry, sequenceRandom(alias), keys)
  assert.deepEqual(await coordinator.initializeFirstForEmail('activation@example.com'), { status: 'activation_uncertain' })
  assert.equal(registry.rows.get('activation@example.com').status, 'reserved')
  assert.deepEqual(await new DeviceIdentityCoordinator(registry, sequenceRandom(canonicalAlias(16)), keys)
    .readForEmail('activation@example.com'), { status: 'reservation_incomplete' })
  assert.equal(keys.calls.reads.length, 0)
})

test('activation CAS retries only within the create-success execution after another account updates registry', async () => {
  const registry = memoryRegistry()
  const keys = memoryDeviceKeys()
  registry.conflictNextActivation()
  const alias = canonicalAlias(17)
  const coordinator = new DeviceIdentityCoordinator(registry, sequenceRandom(alias), keys)
  const result = await coordinator.initializeFirstForEmail('activation-race@example.com')
  assert.equal(result.status, 'ready')
  assert.equal(registry.rows.get('activation-race@example.com').status, 'active')
  assert.deepEqual(registry.calls.transitions, ['reserved', 'active', 'active'])
  assert.equal(keys.calls.creates.length, 1)
})

test('distinct local-part identities stay separate while domain case shares an identity', async () => {
  const registry = memoryRegistry()
  const keys = memoryDeviceKeys()
  const coordinator = new DeviceIdentityCoordinator(registry,
    sequenceRandom(canonicalAlias(8), canonicalAlias(9)), keys)
  const upper = await coordinator.initializeFirstForEmail('Name@example.com')
  const lower = await coordinator.initializeFirstForEmail('name@EXAMPLE.COM')
  assert.equal(upper.status, 'ready')
  assert.equal(lower.status, 'ready')
  assert.notEqual(upper.identity.aliasForProtocol, lower.identity.aliasForProtocol)
  assert.equal(registry.rows.size, 2)
  assert.equal((await coordinator.readForEmail('Name@EXAMPLE.com')).identity.aliasForProtocol,
    upper.identity.aliasForProtocol)
})

test('independent coordinators use CAS to prevent duplicate keys and retry by reading the winner', async () => {
  const registry = memoryRegistry()
  const keys = memoryDeviceKeys()
  const gate = deferred()
  const started = deferred()
  keys.setCreateOverride(async value => {
    started.resolve()
    await gate.promise
    keys.keys.set(value, publicKey)
    return { status: 'ready', alias: new ReservedDeviceAlias(value), publicKey }
  })

  const initialReads = deferred()
  let readCount = 0
  const baseRead = registry.read.bind(registry)
  registry.read = async key => {
    if (readCount < 2) {
      readCount++
      if (readCount === 2) initialReads.resolve()
      await initialReads.promise
    }
    return baseRead(key)
  }

  const firstRandom = sequenceRandom(canonicalAlias(10))
  const secondRandom = sequenceRandom(canonicalAlias(11))
  const first = new DeviceIdentityCoordinator(registry, firstRandom, keys)
  const second = new DeviceIdentityCoordinator(registry, secondRandom, keys)
  const firstAttempt = first.initializeFirstForEmail('same@example.com')
  const secondAttempt = second.initializeFirstForEmail('same@example.com')
  await started.promise
  const loser = await secondAttempt
  assert.deepEqual(loser, { status: 'reservation_incomplete' })
  gate.resolve()
  const winner = await firstAttempt
  assert.equal(winner.status, 'ready')
  assert.equal(keys.calls.creates.length, 1)
  assert.equal(firstRandom.calls + secondRandom.calls, 2) // A losing reservation never creates its candidate.

  keys.setCreateOverride(undefined)
  const retried = await second.initializeFirstForEmail('same@example.com')
  assert.equal(retried.status, 'ready')
  assert.equal(retried.identity.aliasForProtocol, winner.identity.aliasForProtocol)
  assert.equal(keys.calls.creates.length, 1)
})
