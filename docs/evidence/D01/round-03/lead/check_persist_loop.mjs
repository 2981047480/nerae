// Lead diagnostic: actual store module in a Node test harness, not a browser claim.
import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

const timers = new Set()
const intervals = new Set()
const writes = []
let acceptingTimers = true
globalThis.window = {
  scrollY: 0,
  localStorage: {
    getItem: () => null,
    setItem: (key, value) => writes.push({ key, value, at: Date.now() }),
    removeItem: () => {},
  },
  setTimeout(fn, ms) {
    if (!acceptingTimers) return 0
    const id = setTimeout(() => { timers.delete(id); fn() }, ms)
    timers.add(id)
    return id
  },
  clearTimeout: (id) => { clearTimeout(id); timers.delete(id) },
  setInterval(fn, ms) { const id = setInterval(fn, ms); intervals.add(id); return id },
  clearInterval: (id) => { clearInterval(id); intervals.delete(id) },
  addEventListener() {},
  scrollTo() {},
  matchMedia: () => ({ matches: false, addEventListener() {} }),
}

const { createServer } = await import(pathToFileURL(resolve('node_modules/vite/dist/node/index.js')).href)
const server = await createServer({
  root: process.cwd(), configFile: false,
  cacheDir: '/private/tmp/nerae-round3-lead-vite-cache',
  server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom',
})
try {
  const store = await server.ssrLoadModule('/src/state/store.ts')
  store.enterTraining()
  await new Promise((resolve) => setTimeout(resolve, 700))
  const idleWrites = writes.slice()
  let contracts
  if (process.argv.includes('--contracts')) {
    const training = await server.ssrLoadModule('/src/utils/training.ts')
    const decimalInputs = [1.1, 2.3, 8.2, 12.5].map((weight) => ({
      weight,
      errors: training.validateSetInput('weight', { weightKg: String(weight), reps: '10', seconds: '' }),
    }))
    const initialVersion = store.state.coach.draftVersion
    store.updatePlanField('day-a', 'a1', 'sets', '5')
    const changedSave = store.saveDraft()
    const changedVersion = store.state.coach.draftVersion
    const unchangedSave = store.saveDraft()
    const unchangedVersion = store.state.coach.draftVersion
    store.requestPublish()
    store.confirmPublish()
    store.updatePlanField('day-a', 'a1', 'sets', '6')
    store.saveDraft()
    contracts = {
      decimalInputs,
      saveVersion: {
        initialVersion, changedSave, changedVersion, unchangedSave, unchangedVersion,
        changedContentCreatedNewVersion: changedSave && changedVersion > initialVersion,
        unchangedContentAvoidedNewVersion: !unchangedSave && unchangedVersion === changedVersion,
      },
      afterDeliveryAndFurtherSave: {
        deliveredVersion: store.state.coach.deliveredVersion,
        currentStudentVersion: store.state.coach.activeStudentVersion,
        publishedVersion: store.state.coach.publishedVersion,
        publishedFirstExerciseSets: store.state.coach.published.days[0].exercises[0].sets,
        draftVersion: store.state.coach.draftVersion,
        draftFirstExerciseSets: store.state.coach.draft.days[0].exercises[0].sets,
        savedFirstExerciseSets: JSON.parse(store.state.coach.savedContent).days[0].exercises[0].sets,
        coachStateKeys: Object.keys(store.state.coach),
        expected: 'The delivered content with first exercise = 5 sets remains recoverable as its own immutable version.',
      },
    }
  }
  console.log(JSON.stringify({
    scope: 'actual store module; fake browser storage/timers; not an actual-browser result',
    action: 'one navigation then no user actions for 700ms',
    writes: idleWrites.length,
    updatedAtValues: idleWrites.map((w) => JSON.parse(w.value).updatedAt),
    expectedIdleBehavior: 'one initial debounced write, then no periodic self-triggered writes',
    selfTriggerObserved: idleWrites.length > 2,
    contracts,
  }, null, 2))
} finally {
  acceptingTimers = false
  for (const id of timers) clearTimeout(id)
  for (const id of intervals) clearInterval(id)
  await server.close()
}
