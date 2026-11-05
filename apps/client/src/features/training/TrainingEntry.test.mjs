import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { createRenderer, defineComponent, h, nextTick, reactive } from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'
import ts from 'typescript'
import { TrainingEditorController } from './trainingEditorController.ts'
import { todayInTimezone } from './draftStorage.ts'

const current = { services: null, editorEmit: null, editorProps: null }
globalThis.__neraeTrainingEntryTest = current
const EditorStub = defineComponent({
  props: ['draft', 'latestAllowedDate', 'exerciseLabels', 'issues', 'statusMessage', 'saving', 'canSave'],
  emits: ['change-mode', 'prepare-date', 'change-date', 'add-exercise', 'add-set', 'edit-set', 'confirm-set', 'remove-set', 'save'],
  setup(props, { emit }) {
    current.editorEmit = emit
    return () => { current.editorProps = { ...props }; return h('recording-editor') }
  },
})
current.EditorStub = EditorStub
const vueURL = import.meta.resolve('vue')
const draftStorageURL = new URL('./draftStorage.ts', import.meta.url)
registerHooks({ resolve(specifier, context, nextResolve) {
  if (context.parentURL?.startsWith('data:text/javascript')) {
    if (specifier === 'vue') return { url: vueURL, shortCircuit: true }
    if (specifier.endsWith('/RecordingEditor.vue')) return { url: 'data:text/javascript,export default globalThis.__neraeTrainingEntryTest.EditorStub', shortCircuit: true }
    if (specifier.endsWith('/TrainingHistory.vue')) return { url: 'data:text/javascript,export default {render(){return null}}', shortCircuit: true }
    if (specifier === './draftStorage') return { url: draftStorageURL.href, shortCircuit: true }
    if (specifier.endsWith('/nativeTrainingEditor')) return { url: 'data:text/javascript,export const discoverAppTrainingDrafts=(...a)=>globalThis.__neraeTrainingEntryTest.services.discover(...a);export const createAppTrainingEditor=(...a)=>globalThis.__neraeTrainingEntryTest.services.create(...a);export const attachAppTrainingEditor=(...a)=>globalThis.__neraeTrainingEntryTest.services.attach(...a)', shortCircuit: true }
  }
  return nextResolve(specifier, context)
} })
const source = readFileSync(new URL('./TrainingEntry.vue', import.meta.url), 'utf8')
const descriptor = parse(source, { filename: 'TrainingEntry.vue' }).descriptor
const compiled = compileScript(descriptor, { id: 'training-entry-test', inlineTemplate: true }).content
const javascript = ts.transpileModule(compiled, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { default: Page } = await import(`data:text/javascript,${encodeURIComponent(javascript)}`)
const editorSource = readFileSync(new URL('./RecordingEditor.vue', import.meta.url), 'utf8')
const editorDescriptor = parse(editorSource, { filename: 'RecordingEditor.vue' }).descriptor
const editorCompiled = compileScript(editorDescriptor, { id: 'recording-editor-test', inlineTemplate: true,
  templateOptions: { compilerOptions: { isCustomElement: tag => tag === 'picker' } } }).content
const editorJavascript = ts.transpileModule(editorCompiled, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { default: ActualEditor } = await import(`data:text/javascript,${encodeURIComponent(editorJavascript)}`)
const renderer = createRenderer({
  createElement: type => ({ type, children: [], props: {}, text: '' }),
  createText: text => ({ type: '#text', text }),
  createComment: text => ({ type: '#comment', text }),
  setText: (node, text) => { node.text = text },
  setElementText: (node, text) => { node.text = text; node.children = [] },
  patchProp: (node, key, _old, value) => { node.props[key] = value },
  insert(node, parent, anchor) {
    if (node.parent) this.remove(node)
    node.parent = parent
    const index = anchor ? parent.children.indexOf(anchor) : -1
    if (index < 0) parent.children.push(node)
    else parent.children.splice(index, 0, node)
  },
  remove(node) {
    if (!node.parent) return
    const index = node.parent.children.indexOf(node)
    if (index >= 0) node.parent.children.splice(index, 1)
    node.parent = null
  },
  parentNode: node => node.parent,
  nextSibling(node) {
    if (!node.parent) return null
    return node.parent.children[node.parent.children.indexOf(node) + 1] || null
  },
})
const find = (node, predicate) => predicate(node) ? node : (node.children || []).map(child => find(child, predicate)).find(Boolean)
const findAll = (node, predicate) => [ ...(predicate(node) ? [node] : []), ...(node.children || []).flatMap(child => findAll(child, predicate)) ]
const flush = async () => { await nextTick(); await new Promise(resolve => setImmediate(resolve)); await nextTick() }
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done }); return { promise, resolve } }
function button(root, label) {
  const match = find(root, node => node.type === 'button' && node.text === label)
  assert.ok(match, `missing button ${label}`)
  return match
}
function fixture() {
  let subject = '101'
  let generation = 'session-one'
  const listeners = new Set()
  const session = {
    snapshot: () => ({ status: 'authenticated', subjectId: subject, generation, user: { ageVerified: true } }),
    isCurrent: (id, gen) => id === subject && gen === generation,
    captureInstallGuard: () => ({}),
    subscribeInstallGuardInvalidation: (_guard, listener) => { listeners.add(listener); return () => listeners.delete(listener) },
    invalidate() { subject = '202'; generation = 'session-two'; for (const listener of [...listeners]) listener() },
  }
  const lease = { generation: 'session-one', isCurrent: (id, gen) => session.isCurrent(id, gen) }
  const records = new Map()
  let fail = false
  let delayFirst = null
  const storage = {
    async read(key) { if (!records.has(key)) throw Error('missing'); return records.get(key) },
    async createIfAbsent(key, value) { if (records.has(key)) return 'exists'; records.set(key, value); return 'created' },
    async write(key, value) {
      if (delayFirst) { const wait = delayFirst; delayFirst = null; await wait.promise }
      if (fail) throw Error('private storage error')
      records.set(key, value)
    },
  }
  let id = 0
  let idFailure = false
  const ids = { nextId: () => { if (idFailure) throw Error('random unavailable'); return `identifier-${String(++id).padStart(4, '0')}` } }
  const services = {
    async discover(_session, cursor) {
      const eventIds = [...records.keys()].map(key => key.split('.').at(-1))
      return { ok: true, eventIds: cursor ? [] : eventIds, nextCursor: null, hasUnclassifiedItems: false }
    },
    create: (_session, date, timeZone) => TrainingEditorController.create({ subjectId: '101', date, timeZone, storage, now: () => new Date(), auth: lease, ids }),
    attach: (_session, eventId) => TrainingEditorController.attach('101', eventId, storage, () => new Date(), lease, ids),
  }
  return { session, records, services, fail(value) { fail = value }, failIds(value) { idFailure = value }, delay(writeGate) { delayFirst = writeGate } }
}
function mount(context, timeZone = 'Asia/Shanghai') {
  current.services = context.services; current.editorEmit = null; current.editorProps = null
  const root = { type: 'root', children: [] }
  const app = renderer.createApp(Page, { session: context.session, timeZone })
  const instance = app.mount(root)
  return { root, app, instance }
}

test('both recording modes preserve rapid edits and failed device saves can be retried', async () => {
  const context = fixture()
  const { root, app, instance } = mount(context)
  await flush()
  button(root, '新建记录').props.onTap()
  await flush()
  assert.equal(current.editorProps.draft.mode, 'after_workout')
  assert.equal(current.editorProps.canSave, false)
  current.editorEmit('add-exercise')
  await flush()
  const input = find(root, node => node.type === 'input')
  assert.ok(input)
  input.props.onInput({ detail: { value: '深蹲' } })
  await flush()
  button(root, '添加').props.onTap()
  await flush()
  let exercise = current.editorProps.draft.exercises[0]
  assert.equal(exercise.identity.exerciseName, '深蹲')
  const record = exercise.sets[0].recordKey
  current.editorEmit('change-mode', 'per_set')
  await flush()
  const gate = deferred()
  context.delay(gate)
  current.editorEmit('edit-set', exercise.exerciseEntryKey, record, 'weightKg', '4')
  current.editorEmit('edit-set', exercise.exerciseEntryKey, record, 'weightKg', '42')
  await nextTick()
  assert.equal(current.editorProps.draft.exercises[0].sets[0].weightKg, '42')
  gate.resolve()
  await flush()
  current.editorEmit('edit-set', exercise.exerciseEntryKey, record, 'reps', '8')
  await flush()
  current.editorEmit('confirm-set', exercise.exerciseEntryKey, record)
  await flush()
  assert.equal(current.editorProps.draft.exercises[0].sets[0].confirmed, true)
  current.editorEmit('change-mode', 'after_workout')
  await flush()
  exercise = current.editorProps.draft.exercises[0]
  assert.equal(exercise.sets[0].weightKg, '42')
  assert.equal(exercise.sets[0].reps, '8')
  context.fail(true)
  current.editorEmit('edit-set', exercise.exerciseEntryKey, record, 'reps', '9')
  await flush()
  assert.match(current.editorProps.statusMessage, /尚未确认/)
  assert.match(current.editorProps.statusMessage, /关闭应用可能丢失/)
  assert.equal(instance.canLeave(), false)
  button(root, '‹').props.onTap()
  await flush()
  assert.ok(find(root, node => node.type === 'recording-editor'))
  context.fail(false)
  button(root, '重试本机保存').props.onTap()
  await flush()
  assert.match(current.editorProps.statusMessage, /尚未同步/)
  assert.equal(instance.canLeave(), true)
  assert.equal(JSON.parse([...context.records.values()][0]).draft.exercises[0].sets[0].reps, '9')
  button(root, '‹').props.onTap()
  await flush()
  assert.ok(find(root, node => node.type === 'button' && node.text === '继续'))
  app.unmount()
})

test('an editor returned after account change is closed and never shown', async () => {
  const context = fixture()
  const wait = deferred()
  let closed = 0
  context.services.create = () => wait.promise
  const { root, app } = mount(context)
  await flush()
  button(root, '新建记录').props.onTap()
  context.session.invalidate()
  wait.resolve({ ok: true, controller: { close: async () => { closed++ } } })
  await flush()
  assert.equal(closed, 1)
  assert.equal(find(root, node => node.type === 'recording-editor'), undefined)
  app.unmount()
})

test('a failed attempt to add a repeated exercise keeps its input and cancelling clears it', async () => {
  const context = fixture()
  const { root, app } = mount(context)
  await flush()
  button(root, '新建记录').props.onTap()
  await flush()
  current.editorEmit('add-exercise')
  await flush()
  let input = find(root, node => node.type === 'input')
  input.props.onInput({ detail: { value: '深蹲' } })
  await flush()
  button(root, '添加').props.onTap()
  await flush()
  current.editorEmit('add-exercise')
  await flush()
  input = find(root, node => node.type === 'input')
  input.props.onInput({ detail: { value: '深蹲' } })
  await flush()
  context.failIds(true)
  button(root, '添加').props.onTap()
  await flush()
  assert.equal(current.editorProps.draft.exercises.length, 1)
  assert.equal(find(root, node => node.type === 'input').props.value, '深蹲')
  button(root, '取消').props.onTap()
  await flush()
  assert.doesNotMatch(current.editorProps.statusMessage, /暂时无法完成/)
  current.editorEmit('add-exercise')
  await flush()
  assert.equal(find(root, node => node.type === 'input').props.value, '')
  app.unmount()
})

test('an add operation cannot be cancelled while its local write is pending', async () => {
  const context = fixture()
  const { root, app } = mount(context)
  await flush()
  button(root, '新建记录').props.onTap()
  await flush()
  current.editorEmit('add-exercise')
  await flush()
  find(root, node => node.type === 'input').props.onInput({ detail: { value: '深蹲' } })
  await flush()
  const gate = deferred()
  context.delay(gate)
  button(root, '添加').props.onTap()
  await nextTick()
  button(root, '取消').props.onTap()
  await nextTick()
  assert.ok(find(root, node => node.type === 'input'))
  gate.resolve()
  await flush()
  assert.equal(find(root, node => node.type === 'input'), undefined)
  assert.equal(current.editorProps.draft.exercises.length, 1)
  app.unmount()
})

test('an editor created after unmount is released instead of retained', async () => {
  const context = fixture()
  const wait = deferred()
  let closed = 0
  context.services.create = () => wait.promise
  const { root, app } = mount(context)
  await flush()
  button(root, '新建记录').props.onTap()
  app.unmount()
  wait.resolve({ ok: true, controller: { close: async () => { closed++ } } })
  await flush()
  assert.equal(closed, 1)
})

test('a failed discovery is not described as an empty list and can be retried', async () => {
  const context = fixture()
  let attempts = 0
  context.services.discover = async () => ++attempts === 1
    ? { ok: false, reason: 'storage_unavailable' }
    : { ok: true, eventIds: [], nextCursor: null, hasUnclassifiedItems: false }
  const { root, app } = mount(context)
  await flush()
  assert.ok(find(root, node => node.text === '暂时无法读取本机草稿，请重试。'))
  assert.equal(find(root, node => node.text === '还没有本机草稿。'), undefined)
  button(root, '重新查找').props.onTap()
  await flush()
  assert.ok(find(root, node => node.text === '还没有本机草稿。'))
  assert.equal(attempts, 2)
  app.unmount()
})

test('draft discovery loads the next page and keeps unclassified items visible as a warning', async () => {
  const context = fixture()
  const cursors = []
  let closed = 0
  context.services.discover = async (_session, cursor) => {
    cursors.push(cursor)
    return cursor === ''
      ? { ok: true, eventIds: ['event-first'], nextCursor: 'next-page', hasUnclassifiedItems: false }
      : { ok: true, eventIds: ['event-next'], nextCursor: null, hasUnclassifiedItems: true }
  }
  context.services.attach = async (_session, eventId) => ({ ok: true, controller: {
    draft: { date: eventId === 'event-first' ? '2026-11-01' : '2026-11-02', exercises: [] },
    localStatus: 'saved_locally', close: async () => { closed++ },
  } })
  const { root, app } = mount(context)
  await flush()
  assert.ok(find(root, node => node.text === '2026-11-01'))
  button(root, '加载更多').props.onTap()
  await flush()
  assert.ok(find(root, node => node.text === '2026-11-01'))
  assert.ok(find(root, node => node.text === '2026-11-02'))
  assert.ok(find(root, node => node.text === '有部分本机草稿暂时无法识别，原数据仍保留。'))
  assert.deepEqual(cursors, ['', 'next-page'])
  assert.equal(closed, 2)
  app.unmount()
})

test('an existing draft uses its own frozen time zone for the date picker', async () => {
  const context = fixture()
  context.services.discover = async () => ({ ok: true, eventIds: ['event-existing'], nextCursor: null, hasUnclassifiedItems: false })
  context.services.attach = async () => ({ ok: true, controller: {
    draft: { date: '2026-11-01', timeZone: 'Pacific/Kiritimati', mode: 'after_workout', exercises: [] },
    localStatus: 'saved_locally', close: async () => {},
  } })
  const { root, app } = mount(context, 'Etc/GMT+12')
  await flush()
  button(root, '继续').props.onTap()
  await flush()
  assert.equal(current.editorProps.latestAllowedDate, todayInTimezone('Pacific/Kiritimati', new Date()))
  assert.notEqual(current.editorProps.latestAllowedDate, todayInTimezone('Etc/GMT+12', new Date()))
  app.unmount()
})

test('opening the date picker refreshes the allowed day after midnight', async () => {
  const OriginalDate = globalThis.Date
  let now = OriginalDate.parse('2026-10-28T15:59:00Z')
  class ClockDate extends OriginalDate {
    constructor(...args) { super(...(args.length ? args : [now])) }
    static now() { return now }
  }
  globalThis.Date = ClockDate
  try {
    const context = fixture()
    const { root, app } = mount(context)
    await flush()
    button(root, '新建记录').props.onTap()
    await flush()
    assert.equal(current.editorProps.latestAllowedDate, '2026-10-28')
    now += 2 * 60 * 1000
    current.editorEmit('prepare-date')
    await nextTick()
    assert.equal(current.editorProps.latestAllowedDate, '2026-10-29')
    app.unmount()
  } finally { globalThis.Date = OriginalDate }
})

test('the actual recording editor requests a date refresh on picker touch', async () => {
  let prepared = 0
  const root = { type: 'root', children: [] }
  const app = renderer.createApp(ActualEditor, {
    draft: { date: '2026-10-28', mode: 'after_workout', exercises: [] },
    latestAllowedDate: '2026-10-28', exerciseLabels: {}, issues: [], statusMessage: '', saving: false, canSave: false,
    onPrepareDate: () => { prepared++ },
  })
  app.component('picker', defineComponent({
    emits: ['touchstart'],
    setup(_props, { emit, slots }) { return () => h('picker', { onTouchstart: () => emit('touchstart') }, slots.default?.()) },
  }))
  app.component('scroll-view', defineComponent({ setup: (_props, { slots }) => () => h('scroll-view', {}, slots.default?.()) }))
  app.mount(root)
  const picker = find(root, node => node.type === 'picker')
  assert.ok(picker)
  picker.props.onTouchstart()
  assert.equal(prepared, 1)
  app.unmount()
})

test('per-set mode focuses one exercise at a time while after-workout mode shows all', async () => {
  const exercise = (entry, name, record) => ({ exerciseEntryKey: entry,
    identity: { source: 'custom', exerciseName: name }, kind: 'weight',
    sets: [{ recordKey: record, setIndex: 1, weightKg: '42', reps: '8', seconds: '', rpe: '',
      confirmed: false, persisted: false, dirty: true }] })
  const state = reactive({ draft: { date: '2026-10-28', mode: 'per_set',
    exercises: [exercise('entry-0001', '深蹲', 'record-001'), exercise('entry-0002', '划船', 'record-002')] } })
  const actions = []
  const root = { type: 'root', children: [] }
  const host = defineComponent({ setup: () => () => h(ActualEditor, { draft: state.draft,
    latestAllowedDate: '2026-10-28', exerciseLabels: {}, issues: [], statusMessage: '', saving: false, canSave: false,
    onEditSet: (...args) => actions.push(['edit', ...args]), onConfirmSet: (...args) => actions.push(['confirm', ...args]),
    onRemoveSet: (...args) => actions.push(['remove', ...args]) }) })
  const app = renderer.createApp(host)
  app.component('picker', defineComponent({ setup: (_props, { slots }) => () => h('picker', {}, slots.default?.()) }))
  app.component('scroll-view', defineComponent({ setup: (_props, { slots }) => () => h('scroll-view', {}, slots.default?.()) }))
  app.mount(root)
  const cardNames = () => findAll(root, node => node.props?.class === 'exercise-card__name').map(node => node.text)
  const tabs = () => findAll(root, node => node.type === 'button' && String(node.props?.class || '').includes('exercise-tabs__button'))
  assert.deepEqual(cardNames(), ['深蹲'])
  assert.equal(tabs().length, 2)
  tabs()[1].props.onTap()
  await nextTick()
  assert.deepEqual(cardNames(), ['划船'])
  const inputs = findAll(root, node => node.type === 'input')
  assert.equal(inputs.length, 2)
  inputs[0].props.onInput({ detail: { value: '50' } })
  button(root, '记下').props.onTap()
  button(root, '×').props.onTap()
  assert.deepEqual(actions, [ ['edit', 'entry-0002', 'record-002', 'weightKg', '50'],
    ['confirm', 'entry-0002', 'record-002'], ['remove', 'entry-0002', 'record-002'] ])
  state.draft.mode = 'after_workout'
  await nextTick()
  assert.deepEqual(cardNames(), ['深蹲', '划船'])
  assert.equal(state.draft.exercises[1].sets[0].weightKg, '42')
  state.draft.mode = 'per_set'
  state.draft.exercises.push(exercise('entry-0003', '推举', 'record-003'))
  await nextTick()
  assert.deepEqual(cardNames(), ['推举'])
  assert.equal(state.draft.exercises.length, 3)
  app.unmount()
})
