// Run only against the isolated TLS server and synthetic sessions supplied by
// TestWorkoutHTTPRead. This checks the real client adapter, not a native app.
import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

const required = ['NERAE_INTEROP_URL', 'NERAE_INTEROP_CLIENT_DIR', 'NERAE_INTEROP_SUBJECT_A', 'NERAE_INTEROP_SUBJECT_B',
  'NERAE_INTEROP_TOKEN_A', 'NERAE_INTEROP_TOKEN_B', 'NERAE_INTEROP_TODAY']
if (required.some(name => !process.env[name])) throw new Error('isolated interop setup incomplete')
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (/^\.\/(recording|recordingApi|recordingProjection|operationJournal|setsIntent)$/.test(specifier) &&
      context.parentURL?.includes('/features/training/')) return nextResolve(`${specifier}.ts`, context)
    if (specifier === './recording' && /\/(recordingApi|recordingProjection|draftStorage)\.ts$/.test(context.parentURL || '')) {
      return nextResolve('./recording.ts', context)
    }
    if (specifier === './recordingApi' && context.parentURL?.endsWith('/recordingProjection.ts')) {
      return nextResolve('./recordingApi.ts', context)
    }
    return nextResolve(specifier, context)
  },
})
const featureDir = process.env.NERAE_INTEROP_CLIENT_DIR
const { RecordingApi } = await import(pathToFileURL(path.join(featureDir, 'recordingApi.ts')).href)
const { restoreConfirmedSession } = await import(pathToFileURL(path.join(featureDir, 'recordingProjection.ts')).href)
hooks.deregister()
const request = async options => {
  const response = await fetch(options.url, { method: options.method, headers: options.header, body: options.data })
  return { statusCode: response.status, data: await response.json() }
}
const make = (subjectId, accessToken) => {
  const lease = { subjectId, generation: 'isolated-test-session' }
  const credential = { ...lease, accessToken }
  const auth = { current: () => credential, isCurrent: (id, generation) => id === subjectId && generation === lease.generation }
  return { api: new RecordingApi(process.env.NERAE_INTEROP_URL, auth, request, lease), auth }
}
const first = make(process.env.NERAE_INTEROP_SUBJECT_A, process.env.NERAE_INTEROP_TOKEN_A)
const second = make(process.env.NERAE_INTEROP_SUBJECT_B, process.env.NERAE_INTEROP_TOKEN_B)
const page = await first.api.listSessions({ page: 1, pageSize: 20 })
assert.equal(page.total, 1)
assert.equal(page.items.length, 1)
const snapshot = await first.api.readSession(page.items[0].id)
const restored = restoreConfirmedSession(snapshot, first.auth, process.env.NERAE_INTEROP_TODAY)
assert.equal(restored.ok, true)
assert.equal(restored.draft.clientEventId, 'http-event-001')
assert.equal(restored.draft.server.status, 'completed')
assert.deepEqual(restored.draft.exercises.map(item => item.exerciseEntryKey), ['http-entry-002', 'http-entry-001'])
assert.deepEqual(restored.draft.exercises[1].sets.map(item => [item.setIndex, item.weightKg]), [[1, '42.5'], [3, '45.25']])
assert.equal((await second.api.listSessions({ page: 1, pageSize: 20 })).total, 0)
await assert.rejects(second.api.readSession(page.items[0].id), error => error.code === 'not_found')
console.log('PASS: real client adapter read and restored isolated TLS HTTP data')
