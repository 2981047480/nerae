import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'

const state = { values: new Map(), available: true, id: 0, generation: 'login-1', subject: '101' }
const ok = (found = false, value = '') => ({ ok: true, errorCode: '', found, value })
const fail = errorCode => ({ ok: false, errorCode, found: false, value: '' })
globalThis.__neraeEditorTest = {
  read(key) { return state.available ? state.values.has(key) ? ok(true, state.values.get(key)) : ok() : fail('unsupported_platform') },
  create(key, value) { if (!state.available) return fail('unsupported_platform'); if (state.values.has(key)) return fail('conflict'); state.values.set(key, value); return ok() },
  write(key, value) { if (!state.available) return fail('unsupported_platform'); if (!state.values.has(key)) return fail('conflict'); state.values.set(key, value); return ok() },
  random() { return { ok: true, errorCode: '', value: Buffer.alloc(32, ++state.id).toString('base64url') } },
}
registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier === '@/uni_modules/nerae-secure-vault') {
    const source = `export const readTrainingDraft = key => globalThis.__neraeEditorTest.read(key)
      export const createTrainingDraft = (key, value) => globalThis.__neraeEditorTest.create(key, value)
      export const writeTrainingDraft = (key, value) => globalThis.__neraeEditorTest.write(key, value)`
    return { url: `data:text/javascript,${encodeURIComponent(source)}`, shortCircuit: true }
  }
  if (specifier === '@/uni_modules/nerae-random') {
    return { url: 'data:text/javascript,export const generateSecureRandomRequestKey = () => globalThis.__neraeEditorTest.random()', shortCircuit: true }
  }
  return nextResolve(specifier, context)
} })
const { createAppTrainingEditor, attachAppTrainingEditor } = await import('./nativeTrainingEditor.ts')
const session = {
  snapshot() { return { subjectId: state.subject, generation: state.generation } },
  draftLease() { const generation = state.generation; return { generation, isCurrent: (id, gen) => id === state.subject && gen === generation && gen === state.generation } },
}

test('app editor factory writes and reopens one draft across both recording modes', async () => {
  state.values.clear(); state.available = true; state.id = 0; state.subject = '101'; state.generation = 'login-1'
  const today = new Date().toISOString().slice(0, 10)
  const made = await createAppTrainingEditor(session, today, 'UTC')
  assert.equal(made.ok, true, made.reason)
  const editor = made.controller
  assert.equal(editor.draft.mode, 'after_workout')
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  const { clientEventId, exercises } = editor.draft
  const entry = exercises[0].exerciseEntryKey
  const group = exercises[0].sets[0].recordKey
  assert.equal((await editor.editGroup(entry, group, 'weightKg', '42.5')).ok, true)
  assert.equal((await editor.editGroup(entry, group, 'reps', '8')).ok, true)
  assert.equal((await editor.setMode('per_set')).ok, true)
  assert.equal((await editor.confirmGroup(entry, group)).ok, true)
  await editor.close()
  const reopened = await attachAppTrainingEditor(session, clientEventId)
  assert.equal(reopened.ok, true, reopened.reason)
  assert.equal(reopened.controller.draft.mode, 'per_set')
  assert.equal(reopened.controller.draft.exercises[0].sets[0].weightKg, '42.5')
  assert.equal(reopened.controller.draft.exercises[0].sets[0].reps, '8')
  assert.equal(reopened.controller.draft.exercises[0].sets[0].confirmed, true)
  assert.equal(state.values.size, 1)
  await reopened.controller.close()
})

test('unsupported storage and account changes do not present an editable saved draft', async () => {
  state.values.clear(); state.available = false; state.subject = '101'; state.generation = 'login-1'
  const made = await createAppTrainingEditor(session, new Date().toISOString().slice(0, 10), 'UTC')
  assert.equal(made.ok, false)
  assert.equal(state.values.size, 0)
  state.available = true
  const first = await createAppTrainingEditor(session, new Date().toISOString().slice(0, 10), 'UTC')
  assert.equal(first.ok, true)
  const event = first.controller.draft.clientEventId
  state.subject = '202'; state.generation = 'login-2'
  assert.equal((await first.controller.setMode('per_set')).reason, 'session_changed')
  assert.equal((await attachAppTrainingEditor(session, event)).ok, false)
  await first.controller.close()
})
