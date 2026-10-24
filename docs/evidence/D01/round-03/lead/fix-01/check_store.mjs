// Independent Lead diagnostic of the actual store module in Node.
// Storage/window are test doubles; this is not an actual-browser result.
import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

const timers = new Set(), intervals = new Set(), writes = [], data = new Map()
let failWrites = false, accepting = true
globalThis.window = {
  scrollY: 0,
  localStorage: {
    getItem: key => data.get(key) ?? null,
    setItem(key, value) {
      if (failWrites) throw new Error('Lead test: storage unavailable')
      data.set(key, value); writes.push({ key, value, at: Date.now() })
    },
    removeItem: key => data.delete(key),
  },
  setTimeout(fn, ms) {
    if (!accepting) return 0
    const id = setTimeout(() => { timers.delete(id); fn() }, ms)
    timers.add(id); return id
  },
  clearTimeout(id) { clearTimeout(id); timers.delete(id) },
  setInterval(fn, ms) { const id = setInterval(fn, ms); intervals.add(id); return id },
  clearInterval(id) { clearInterval(id); intervals.delete(id) },
  addEventListener() {}, scrollTo() {},
  matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
}
const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
const { createServer } = await import(pathToFileURL(resolve('node_modules/vite/dist/node/index.js')).href)
const server = await createServer({ root: process.cwd(), configFile: false,
  cacheDir: '/private/tmp/nerae-r3-lead-fix01-cache',
  server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
const result = { scope: 'actual store module; fake window/storage; not browser evidence', checks: {} }
try {
  const s = await server.ssrLoadModule('/src/state/store.ts')
  const training = await server.ssrLoadModule('/src/utils/training.ts')
  s.jumpToScenario('explore')
  await delay(700)
  const afterDebounce = writes.length
  await delay(500)
  assert.equal(afterDebounce, 1)
  assert.equal(writes.length, afterDebounce)
  result.checks.idlePersistence = { afterDebounce, afterIdle: writes.length }

  const validate = weight => training.validateSetInput('weight', { weightKg: weight, reps: '10', seconds: '' })
  const valid = ['1.1', '2.3', '8.2', '12.5'].map(weight => ({ weight, errors: validate(weight) }))
  const invalid = ['-1', 'abc', '1.234'].map(weight => ({ weight, errors: validate(weight) }))
  assert.ok(valid.every(x => Object.values(x.errors).every(v => !v)))
  assert.ok(invalid.every(x => Object.values(x.errors).some(Boolean)))
  result.checks.decimals = { valid, invalid }

  s.updatePlanField('day-a', 'a1', 'sets', '5')
  assert.equal(s.saveDraft(), true)
  assert.equal(s.state.coach.draftVersion, 8)
  assert.equal(s.saveDraft(), false)
  assert.equal(s.state.coach.draftVersion, 8)
  s.requestPublish(); s.confirmPublish()
  const firstDelivery = JSON.stringify(s.state.coach.deliveries.find(x => x.version === 8))
  assert.equal(Number(s.state.coach.deliveries[0].content.days[0].exercises[0].sets), 5)
  s.updatePlanField('day-a', 'a1', 'sets', '6')
  assert.equal(s.saveDraft(), true)
  assert.equal(s.state.coach.draftVersion, 9)
  s.requestPublish(); s.confirmPublish()
  assert.equal(s.state.coach.deliveries.length, 2)
  assert.equal(JSON.stringify(s.state.coach.deliveries.find(x => x.version === 8)), firstDelivery)
  assert.equal(s.state.coach.activeStudentVersion, 6)
  assert.equal(Number(s.state.coach.published.days[0].exercises[0].sets), 4)
  result.checks.versions = { draft: s.state.coach.draftVersion, student: s.state.coach.activeStudentVersion,
    deliveries: s.state.coach.deliveries.map(x => ({ version: x.version, sets: x.content.days[0].exercises[0].sets })) }

  failWrites = true
  s.updatePlanField('day-a', 'a1', 'sets', '7')
  const savedBeforeFailure = s.state.coach.savedContent
  assert.equal(s.saveDraft(), false)
  assert.equal(s.state.coach.draftVersion, 9)
  assert.equal(Number(s.state.coach.draft.days[0].exercises[0].sets), 7)
  assert.equal(s.state.coach.savedContent, savedBeforeFailure)
  assert.equal(s.storageState.ok, false)
  assert.match(s.ui.saveNote, /未能保存/)
  const failureNote = s.ui.saveNote
  failWrites = false
  assert.equal(s.retryPersist(), true)
  assert.equal(s.storageState.ok, true)
  assert.equal(s.saveDraft(), true)
  assert.equal(s.state.coach.draftVersion, 10)
  assert.equal(JSON.stringify(s.state.coach.deliveries.find(x => x.version === 8)), firstDelivery)
  result.checks.storageFailure = { failureNote, memoryRetained: true, savedMarkersRolledBack: true,
    retrySucceeded: true, nextSavedVersion: s.state.coach.draftVersion }

  s.jumpToScenario('explore'); s.resetPreview()
  await delay(500)
  const saved = JSON.parse(writes.at(-1).value)
  assert.equal(saved.screen, 'today'); assert.equal(saved.actor, 'student')
  assert.equal(saved.coach.draftVersion, 7); assert.equal(saved.coach.deliveries.length, 0)
  const done = saved.session.exercises.flatMap(x => x.sets).filter(x => x.status === 'done').length
  assert.equal(done, 3)
  result.checks.reset = { screen: saved.screen, actor: saved.actor, draft: saved.coach.draftVersion, done }
  result.status = 'passed'
  console.log(JSON.stringify(result, null, 2))
} catch (error) {
  result.status = 'failed'; result.error = String(error?.stack ?? error)
  console.log(JSON.stringify(result, null, 2)); process.exitCode = 1
} finally {
  accepting = false
  for (const id of timers) clearTimeout(id)
  for (const id of intervals) clearInterval(id)
  await server.close()
}
