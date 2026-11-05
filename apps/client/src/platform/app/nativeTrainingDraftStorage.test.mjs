import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'

const state = { values: new Map(), available: true }
const ok = (found = false, value = '') => ({ ok: true, errorCode: '', found, value })
const fail = code => ({ ok: false, errorCode: code, found: false, value: '' })
globalThis.__neraeDraftTest = {
  discover(subject, cursor) {
    const prefix = `nerae.workout.draft.v1.${subject}.`
    const all = [...state.values.keys()].filter(key => key.startsWith(prefix)).map(key => key.slice(prefix.length)).sort().filter(id => !cursor || id > cursor)
    const eventIds = all.slice(0, 100)
    return state.available ? ok(true, JSON.stringify({ eventIds, nextCursor: all.length > 100 ? eventIds.at(-1) : null, hasUnclassifiedItems: false })) : fail('unsupported_platform')
  },
  read(key) {
    if (!state.available) return { ...fail('unsupported_platform'), revision: '' }
    const row = state.values.get(key)
    return row ? { ok: true, errorCode: '', found: true, value: row.value, revision: row.revision }
      : { ok: true, errorCode: '', found: false, value: '', revision: '0' }
  },
  compareAndSet(key, revision, found, expected, value) {
    if (!state.available) return { ...fail('unsupported_platform'), revision: '' }
    const row = state.values.get(key)
    const matches = row ? found && revision === row.revision && expected === row.value
      : !found && revision === '0' && expected === ''
    if (!matches) return { ...fail('conflict'), revision: '' }
    const next = (BigInt(revision) + 1n).toString()
    state.values.set(key, { value, revision: next })
    return { ok: true, errorCode: '', found: false, value: '', revision: next }
  },
}
registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier !== '@/uni_modules/nerae-secure-vault') {
    try { return nextResolve(specifier, context) }
    catch (error) {
      if (specifier.startsWith('.') && !specifier.endsWith('.ts')) return nextResolve(`${specifier}.ts`, context)
      throw error
    }
  }
  const module = `export const discoverTrainingDrafts = (subject, cursor) => globalThis.__neraeDraftTest.discover(subject, cursor)
    export const readVersionedTrainingDraft = key => globalThis.__neraeDraftTest.read(key)
    export const compareAndSetVersionedTrainingDraft = (key, revision, found, expected, value) => globalThis.__neraeDraftTest.compareAndSet(key, revision, found, expected, value)`
  return { url: `data:text/javascript,${encodeURIComponent(module)}`, shortCircuit: true }
} })
const { createNativeTrainingDraftStorage } = await import('./nativeTrainingDraftStorage.ts')

test('native draft factory binds the current subject and fails closed on unsupported platforms', async () => {
  state.values.clear()
  state.available = true
  const storage = createNativeTrainingDraftStorage('101', { generation: 'one', isCurrent: (id, gen) => id === '101' && gen === 'one' })
  const key = 'nerae.workout.draft.v1.101.event-0001'
  const value = JSON.stringify({ schema: 1, subjectId: '101', draft: { clientEventId: 'event-0001' } })
  assert.equal(await storage.createIfAbsent(key, value), 'created')
  assert.deepEqual(await storage.readVersioned(key), { found: true, value, revision: '1' })
  assert.deepEqual(await storage.discover(), { eventIds: ['event-0001'], nextCursor: null, hasUnclassifiedItems: false })
  state.available = false
  await assert.rejects(storage.readVersioned(key), /失败/)
  await assert.rejects(storage.write(key, value), /需要版本化写入/)
  assert.equal(state.values.get(key).value, value)
})
