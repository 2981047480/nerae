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
  read(key) { return state.available ? state.values.has(key) ? ok(true, state.values.get(key)) : ok() : fail('unsupported_platform') },
  create(key, value) { if (!state.available) return fail('unsupported_platform'); if (state.values.has(key)) return fail('conflict'); state.values.set(key, value); return ok() },
  write(key, value) { if (!state.available) return fail('unsupported_platform'); if (!state.values.has(key)) return fail('conflict'); state.values.set(key, value); return ok() },
}
registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier !== '@/uni_modules/nerae-secure-vault') return nextResolve(specifier, context)
  const module = `export const discoverTrainingDrafts = (subject, cursor) => globalThis.__neraeDraftTest.discover(subject, cursor)
    export const readTrainingDraft = key => globalThis.__neraeDraftTest.read(key)
    export const createTrainingDraft = (key, value) => globalThis.__neraeDraftTest.create(key, value)
    export const writeTrainingDraft = (key, value) => globalThis.__neraeDraftTest.write(key, value)`
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
  assert.equal(await storage.read(key), value)
  assert.deepEqual(await storage.discover(), { eventIds: ['event-0001'], nextCursor: null, hasUnclassifiedItems: false })
  state.available = false
  await assert.rejects(storage.write(key, value), /未确认/)
  assert.equal(state.values.get(key), value)
})
