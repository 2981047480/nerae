import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { createRenderer, defineComponent, h, nextTick, reactive } from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'
import ts from 'typescript'
import { TrainingEditorController } from './trainingEditorController.ts'
import { SecureDraftStorage } from './secureDraftStorage.ts'
import { RecordingApi } from './recordingApi.ts'
import { todayInTimezone } from './draftStorage.ts'
import { batchSetSnapshot } from './recording.ts'

const current = { services: null, editorEmit: null, editorProps: null, historyEmit: null, completionEmit: null, completionProps: null, conflictEmit: null, conflictProps: null }
globalThis.__neraeTrainingEntryTest = current
const EditorStub = defineComponent({
  props: ['draft', 'latestAllowedDate', 'exerciseLabels', 'issues', 'statusMessage', 'saving', 'canSave'],
  emits: ['change-mode', 'prepare-date', 'change-date', 'add-exercise', 'add-set', 'copy-set', 'edit-set', 'apply-batch', 'confirm-set', 'remove-set', 'restore-set', 'save'],
  setup(props, { emit }) {
    current.editorEmit = emit
    return () => { current.editorProps = { ...props }; return h('recording-editor') }
  },
})
current.EditorStub = EditorStub
current.CompletionStub = defineComponent({ props: ['draft', 'statusMessage', 'busy', 'cloud'],
  emits: ['edit', 'edit-set', 'copy-set', 'remove-set', 'restore-set'], setup(props, { emit }) {
    current.completionEmit = emit
    return () => { current.completionProps = { ...props }; return h('recording-completed') }
  } })
current.HistoryStub = defineComponent({ emits: ['session-changed', 'edit-v2'], setup(_props, { emit }) {
  current.historyEmit = emit
  return () => h('training-history')
} })
current.ConflictStub = defineComponent({ props: ['review', 'choices', 'busy', 'error'], emits: ['choose', 'cancel', 'confirm'], setup(props, {emit}) {
  current.conflictEmit = emit
  return () => { current.conflictProps = {...props}; return h('recording-conflict-review') }
} })
// The editor tests exercise the parent/editor contract; the ruler has its own
// numeric tests and is separately mounted in the platform component probe.
current.RulerStub = defineComponent({
  props: ['label', 'unit', 'value', 'kind'],
  emits: ['change'],
  setup(props, { emit }) {
    return () => h('input', { value: props.value, 'aria-label': props.label,
      onInput: event => emit('change', event.detail.value) })
  },
})
const vueURL = import.meta.resolve('vue')
const draftStorageURL = new URL('./draftStorage.ts', import.meta.url)
const recordingURL = new URL('./recording.ts', import.meta.url)
const measureURL = new URL('./recordingV2Measure.ts', import.meta.url)
const syncURL = new URL('./syncRecordingV2.ts', import.meta.url)
const conflictURL = new URL('./recordingV2Conflict.ts', import.meta.url)
registerHooks({ resolve(specifier, context, nextResolve) {
  if (context.parentURL?.startsWith('data:text/javascript')) {
    if (specifier === 'vue') return { url: vueURL, shortCircuit: true }
    if (specifier.endsWith('/RecordingEditor.vue')) return { url: 'data:text/javascript,export default globalThis.__neraeTrainingEntryTest.EditorStub', shortCircuit: true }
    if (specifier.endsWith('/RecordingCompleted.vue')) return { url: 'data:text/javascript,export default globalThis.__neraeTrainingEntryTest.CompletionStub', shortCircuit: true }
    if (specifier.endsWith('/RecordingConflictReview.vue')) return { url: 'data:text/javascript,export default globalThis.__neraeTrainingEntryTest.ConflictStub', shortCircuit: true }
    if (specifier === './RulerInput.vue') return { url: 'data:text/javascript,export default globalThis.__neraeTrainingEntryTest.RulerStub', shortCircuit: true }
    if (specifier === './SwipeableSetCard.vue') return { url: 'data:text/javascript,export default globalThis.__neraeTrainingEntryTest.Swipeable', shortCircuit: true }
    if (specifier.endsWith('/TrainingHistory.vue')) return { url: 'data:text/javascript,export default globalThis.__neraeTrainingEntryTest.HistoryStub', shortCircuit: true }
    if (specifier === './draftStorage') return { url: draftStorageURL.href, shortCircuit: true }
    if (specifier === './recording') return { url: recordingURL.href, shortCircuit: true }
    if (specifier === './recordingV2Measure') return { url: measureURL.href, shortCircuit: true }
    if (specifier === './syncRecordingV2') return { url: syncURL.href, shortCircuit: true }
    if (specifier === './recordingV2Conflict') return { url: conflictURL.href, shortCircuit: true }
    if (specifier.endsWith('/nativeTrainingEditor')) return { url: 'data:text/javascript,export const discoverAppTrainingDrafts=(...a)=>globalThis.__neraeTrainingEntryTest.services.discover(...a);export const createAppTrainingEditor=(...a)=>globalThis.__neraeTrainingEntryTest.services.create(...a);export const attachAppTrainingEditor=(...a)=>globalThis.__neraeTrainingEntryTest.services.attach(...a);export const restoreAppCloudRecordingV2=(...a)=>globalThis.__neraeTrainingEntryTest.services.restore(...a);export const nextAppTrainingRequestKey=()=>globalThis.__neraeTrainingEntryTest.services.nextKey()', shortCircuit: true }
  }
  return nextResolve(specifier, context)
} })
const source = readFileSync(new URL('./TrainingEntry.vue', import.meta.url), 'utf8')
const descriptor = parse(source, { filename: 'TrainingEntry.vue' }).descriptor
const compiled = compileScript(descriptor, { id: 'training-entry-test', inlineTemplate: true }).content
const javascript = ts.transpileModule(compiled, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { default: Page } = await import(`data:text/javascript,${encodeURIComponent(javascript)}`)
const swipeSource = readFileSync(new URL('./SwipeableSetCard.vue', import.meta.url), 'utf8')
const swipeDescriptor = parse(swipeSource, { filename: 'SwipeableSetCard.vue' }).descriptor
const swipeCompiled = compileScript(swipeDescriptor, { id: 'swipeable-test', inlineTemplate: true }).content
const swipeJavascript = ts.transpileModule(swipeCompiled, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
current.Swipeable = (await import(`data:text/javascript,${encodeURIComponent(swipeJavascript)}`)).default
const editorSource = readFileSync(new URL('./RecordingEditor.vue', import.meta.url), 'utf8')
const editorDescriptor = parse(editorSource, { filename: 'RecordingEditor.vue' }).descriptor
const editorCompiled = compileScript(editorDescriptor, { id: 'recording-editor-test', inlineTemplate: true,
  templateOptions: { compilerOptions: { isCustomElement: tag => tag === 'picker' } } }).content
const editorJavascript = ts.transpileModule(editorCompiled, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { default: ActualEditor } = await import(`data:text/javascript,${encodeURIComponent(editorJavascript)}`)
const completionSource = readFileSync(new URL('./RecordingCompleted.vue', import.meta.url), 'utf8')
const completionDescriptor = parse(completionSource, { filename: 'RecordingCompleted.vue' }).descriptor
const completionCompiled = compileScript(completionDescriptor, { id: 'recording-completed-test', inlineTemplate: true }).content
const completionJavascript = ts.transpileModule(completionCompiled, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { default: ActualCompleted } = await import(`data:text/javascript,${encodeURIComponent(completionJavascript)}`)
const conflictSource = readFileSync(new URL('./RecordingConflictReview.vue', import.meta.url), 'utf8')
const conflictDescriptor = parse(conflictSource, { filename: 'RecordingConflictReview.vue' }).descriptor
const conflictCompiled = compileScript(conflictDescriptor, { id:'recording-conflict-test', inlineTemplate:true }).content
const conflictJavascript = ts.transpileModule(conflictCompiled, {compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText
const { default: ActualConflict } = await import(`data:text/javascript,${encodeURIComponent(conflictJavascript)}`)
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
function fixture(secure = false) {
  let subject = '101'
  let generation = 'session-one'
  const listeners = new Set()
  const session = {
    snapshot: () => ({ status: 'authenticated', subjectId: subject, generation, user: { ageVerified: true } }),
    current: () => ({ subjectId: subject, generation, accessToken: 'test-token' }),
    isCurrent: (id, gen) => id === subject && gen === generation,
    captureInstallGuard: () => ({}),
    subscribeInstallGuardInvalidation: (_guard, listener) => { listeners.add(listener); return () => listeners.delete(listener) },
    invalidate() { subject = '202'; generation = 'session-two'; for (const listener of [...listeners]) listener() },
  }
  const lease = { generation: 'session-one', isCurrent: (id, gen) => session.isCurrent(id, gen) }
  const records = new Map()
  let fail = false
  let delayFirst = null
  const legacyStorage = {
    async read(key) { if (!records.has(key)) throw Error('missing'); return records.get(key) },
    async createIfAbsent(key, value) { if (records.has(key)) return 'exists'; records.set(key, value); return 'created' },
    async write(key, value) {
      if (delayFirst) { const wait = delayFirst; delayFirst = null; await wait.promise }
      if (fail) throw Error('private storage error')
      records.set(key, value)
    },
  }
  const storage = secure ? new SecureDraftStorage({
    discoverTrainingDrafts() { return { ok: true, errorCode: '', found: true, value: JSON.stringify({ eventIds: [], nextCursor: null, hasUnclassifiedItems: false }) } },
    readVersionedTrainingDraft(key) { const row=records.get(key);return row ? { ok:true,errorCode:'',found:true,value:row.value,revision:row.rev } : { ok:true,errorCode:'',found:false,value:'',revision:'0' } },
    compareAndSetVersionedTrainingDraft(key,revision,found,before,after) {
      const old=records.get(key);if((old?.rev||'0')!==revision||Boolean(old)!==found||(old?.value||'')!==before)return {ok:false,errorCode:'conflict',found:false,value:'',revision:''}
      if(fail)throw Error('private storage error')
      const rev=String(Number(revision)+1);records.set(key,{value:after,rev});return {ok:true,errorCode:'',found:false,value:'',revision:rev}
    },
  },'101',lease) : legacyStorage
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
    nextKey: () => `request-key-${String(++id).padStart(4, '0')}`,
  }
  return { session, records, services, storage, ids, lease, fail(value) { fail = value }, failIds(value) { idFailure = value }, delay(writeGate) { delayFirst = writeGate } }
}
function mount(context, timeZone = 'Asia/Shanghai', recordingApi = null) {
  current.services = context.services; current.editorEmit = null; current.editorProps = null; current.historyEmit = null
  current.completionEmit = null; current.completionProps = null
  current.conflictEmit = null; current.conflictProps = null
  const root = { type: 'root', children: [] }
  const app = renderer.createApp(Page, { session: context.session, timeZone, recordingApi })
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
  current.editorEmit('copy-set', exercise.exerciseEntryKey, record)
  await flush()
  assert.equal(current.editorProps.draft.exercises[0].sets[1].weightKg, '42')
  assert.equal(current.editorProps.draft.exercises[0].sets[1].confirmed, false)
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

test('the page completes retained groups through protected local storage and the recording API', async () => {
  const context = fixture(true)
  const lease = { subjectId: '101', generation: 'session-one' }
  let cloud = null
  const calls = []
  const api = new RecordingApi('https://api.example.org', context.session, async request => {
    const path = new URL(request.url).pathname
    calls.push({ method: request.method, path })
    if (path.endsWith('/replay')) return { statusCode: 503, data: {} }
    if (request.method === 'GET') return cloud ? { statusCode: 200, data: structuredClone(cloud) } : { statusCode: 404, data: {} }
    const input = JSON.parse(request.data)
    const headers = { 'Idempotency-Key-Expires-At': '2027-01-01T00:00:00Z' }
    if (path === '/me/recordings') {
      cloud = { id: '42', version: 1, source: 'free', date: input.date, timeZone: input.timeZone,
        clientEventId: input.clientEventId, mode: input.mode, status: 'in_progress',
        createdAt: '2026-11-04T00:00:00Z', updatedAt: '2026-11-04T00:00:00Z', entries: [], sets: [], removedSets: [] }
      return { statusCode: 201, data: { id: '42', version: 1 }, headers }
    }
    if (path === '/me/recordings/42/sets' && input.expectedVersion === cloud.version) {
      cloud.entries.push(...(input.addEntries || []).map(item => ({ ...item, source: 'custom' })))
      cloud.sets.push(...(input.sets || []))
      for (const removal of input.removeRecords || []) {
        const index=cloud.sets.findIndex(set=>set.recordKey===removal.recordKey)
        if(index>=0){const [set]=cloud.sets.splice(index,1);cloud.removedSets.push({set,removedPosition:removal.removedPosition})}
      }
      if(!cloud.sets.length && cloud.status==='completed'){cloud.status='in_progress';delete cloud.recordedAt}
      cloud.mode = input.mode || cloud.mode;cloud.version++
      return { statusCode: 200, data: { id: '42', version: cloud.version }, headers }
    }
    if (path === '/me/recordings/42/finish' && input.expectedVersion === cloud.version) {
      cloud.status = 'completed';cloud.recordedAt = '2026-11-04T01:00:00Z';cloud.version++
      return { statusCode: 200, data: { id: '42', version: cloud.version }, headers }
    }
    return { statusCode: 412, data: {} }
  }, lease)
  const { root, app } = mount(context, 'Asia/Shanghai', api)
  await flush();button(root, '新建记录').props.onTap();await flush()
  assert.ok(current.editorProps, `${find(root, node => node.type === 'text' && node.props.role === 'alert')?.text || 'editor did not open'} (local rows: ${context.records.size})`)
  assert.equal(current.editorProps.draft.recordFormat, 2)
  current.editorEmit('add-exercise');await flush()
  find(root, node => node.type === 'input').props.onInput({ detail: { value: '深蹲' } });await flush()
  button(root, '添加').props.onTap();await flush()
  const exercise = current.editorProps.draft.exercises[0]
  current.editorEmit('change-mode', 'per_set');await flush()
  current.editorEmit('edit-set', exercise.exerciseEntryKey, exercise.sets[0].recordKey, 'weightKg', '45');await flush()
  current.editorEmit('confirm-set', exercise.exerciseEntryKey, exercise.sets[0].recordKey);await flush()
  current.editorEmit('add-set', exercise.exerciseEntryKey);await flush()
  assert.equal(current.editorProps.canSave, true)
  current.editorEmit('save');await flush();await flush()
  assert.equal(cloud.status, 'completed')
  assert.equal(cloud.sets.length, 2)
  assert.equal(cloud.sets[0].measurement.weightKg, '45')
  assert.deepEqual(cloud.sets[1].measurement, { method: 'total' })
  assert.match(current.completionProps.statusMessage, /已同步/)
  assert.equal(current.completionProps.draft.exercises[0].sets.length,2)
  assert.ok(calls.some(call => call.path === '/me/recordings/42/finish'))
  const keys=current.completionProps.draft.exercises[0].sets.map(set=>set.recordKey)
  current.completionEmit('remove-set',exercise.exerciseEntryKey,keys[0]);await flush()
  await new Promise(resolve=>setTimeout(resolve,850));await flush();await flush()
  assert.equal(cloud.sets.length,1)
  current.completionEmit('remove-set',exercise.exerciseEntryKey,keys[1]);await flush()
  await new Promise(resolve=>setTimeout(resolve,850));await flush();await flush()
  assert.equal(cloud.status,'in_progress');assert.equal(cloud.sets.length,0)
  assert.equal(cloud.removedSets.length,2)
  app.unmount()
})

test('a cloud record opens for editing only after restoring it into an empty protected slot', async () => {
  const { restoreCloudRecordingV2ToLocalDraft } = await import('./cloudRecordingV2Restore.ts')
  const context = fixture(true)
  const lease = { subjectId: '101', generation: 'session-one' }
  const detail = { id:'42',version:2,source:'free',date:'2026-11-04',timeZone:'Asia/Shanghai',clientEventId:'cloud-event-01',
    status:'completed',mode:'after_workout',createdAt:'2026-11-04T00:00:00Z',updatedAt:'2026-11-04T01:00:00Z',
    recordedAt:'2026-11-04T01:00:00Z',entries:[{exerciseEntryKey:'cloud-entry-01',source:'custom',exerciseName:'深蹲'}],
    sets:[{recordKey:'cloud-set-001',exerciseEntryKey:'cloud-entry-01',setIndex:1,confirmed:false,measurement:{method:'total',weightKg:'45'}}],removedSets:[] }
  const api = new RecordingApi('https://api.example.org',context.session,async()=>({statusCode:200,data:structuredClone(detail)}),lease)
  context.services.restore = (_session, adapter, sessionId) => restoreCloudRecordingV2ToLocalDraft({ sessionId, api:adapter,
    auth:context.session,draftLease:context.lease,lease,storage:context.storage,ids:context.ids,now:()=>new Date() })
  const { app } = mount(context,'Asia/Shanghai',api)
  await flush();current.historyEmit('edit-v2','42','cloud-event-01');await flush();await flush()
  assert.equal(current.editorProps.draft.exercises[0].sets[0].weightKg,'45')
  assert.equal(current.editorProps.draft.exercises[0].sets[0].reps,'')
  assert.equal(context.records.size,1)
  app.unmount()
})

test('completed details show missing inputs and server volume, and allow undoing a removed group', async () => {
  const set={recordKey:'record-001',setIndex:1,weightKg:'12',reps:'12',seconds:'',rpe:'',confirmed:true,persisted:true,dirty:false}
  const blank={recordKey:'record-002',setIndex:2,weightKg:'',reps:'',seconds:'',rpe:'',confirmed:false,persisted:true,dirty:false}
  const state=reactive({draft:{recordFormat:2,completionRequested:true,exercises:[{exerciseEntryKey:'entry-0001',
    identity:{source:'custom',exerciseName:'深蹲'},kind:'weight',sets:[set,blank],removedSets:[]}]}})
  const cloud={entries:[{exerciseEntryKey:'entry-0001',volume:{calculatedKg:'144.00',fullSets:1,partialSets:0,excludedSets:1}}]}
  const root={type:'root',children:[]}
  const app=renderer.createApp(defineComponent({setup:()=>()=>h(ActualCompleted,{draft:state.draft,cloud,statusMessage:'已同步',busy:false,
    onRemoveSet:()=>{const e=state.draft.exercises[0];state.draft={...state.draft,exercises:[{...e,sets:[e.sets[1]],removedSets:[{set:e.sets[0],removedPosition:1}]}]}},
    onRestoreSet:()=>{const e=state.draft.exercises[0];state.draft={...state.draft,exercises:[{...e,sets:[e.removedSets[0].set,...e.sets],removedSets:[]}]}},
  })}))
  app.component('scroll-view',defineComponent({setup:(_p,{slots})=>()=>h('scroll-view',{},slots.default?.())}))
  app.mount(root);await nextTick()
  assert.ok(find(root,node=>node.text==='已计算 144 kg，1 组未计入'))
  const values=findAll(root,node=>node.props?.class==='completed__set-value')
  const flatten=node=>`${node.text||''}${(node.children||[]).map(flatten).join('')}`
  assert.match(flatten(values[0]),/12/);assert.match(flatten(values[1]),/重量未填.*次数未填/)
  const face=find(root,node=>node.type==='button'&&node.props?.class==='swipe-card__face')
  face.props.onLongpress();await nextTick()
  find(root,node=>node.type==='button'&&node.props?.['aria-label']==='移除第 1 组').props.onTap({stopPropagation(){}})
  await nextTick();assert.equal(state.draft.exercises[0].sets.length,1)
  button(root,'撤销').props.onTap();await nextTick()
  assert.equal(state.draft.exercises[0].sets[0].weightKg,'12')
  app.unmount()
})

test('completion groups repeated action segments for the action count and total', async()=>{
  const group=(entry,record,weight,reps)=>({exerciseEntryKey:entry,identity:{source:'custom',exerciseName:'深蹲'},kind:'weight',
    sets:[{recordKey:record,setIndex:1,weightKg:weight,reps,seconds:'',rpe:'',confirmed:false,persisted:true,dirty:false}],removedSets:[]})
  const draft={exercises:[group('entry-0001','record-001','40','8'),group('entry-0002','record-002','50','6')]}
  const cloud={entries:[{exerciseEntryKey:'entry-0001',volume:{calculatedKg:'320.00',fullSets:1,partialSets:0,excludedSets:0}},
    {exerciseEntryKey:'entry-0002',volume:{calculatedKg:'300.00',fullSets:1,partialSets:0,excludedSets:0}}],
    actionVolumes:[{exerciseEntryKeys:['entry-0001','entry-0002'],volume:{calculatedKg:'620.00',fullSets:2,partialSets:0,excludedSets:0}}]}
  const root={type:'root',children:[]};const app=renderer.createApp(ActualCompleted,{draft,cloud,statusMessage:'已同步',busy:false})
  app.component('scroll-view',defineComponent({setup:(_p,{slots})=>()=>h('scroll-view',{},slots.default?.())}))
  try {
    app.mount(root);await nextTick()
    const numbers=findAll(root,node=>node.props?.class==='completed__summary-number')
    assert.equal(numbers[0].text,'1');assert.equal(numbers[1].text,'2')
    assert.ok(find(root,node=>node.text==='本动作累计 620 kg'))
    assert.ok(find(root,node=>node.text==='本段：已计算 320 kg'))
    assert.ok(find(root,node=>node.text==='本段：已计算 300 kg'))
  } finally { app.unmount() }
})

test('completed navigation offers only actions outside the visible list', async () => {
  const previous=globalThis.uni
  globalThis.uni={createSelectorQuery(){const query={select(){return query},selectAll(){return query},boundingClientRect(){return query},
    exec(callback){callback([{top:0,bottom:100},[{top:0,bottom:80},{top:110,bottom:180}]])}};return query}}
  const exercise=(key,name)=>({exerciseEntryKey:key,identity:{source:'custom',exerciseName:name},kind:'weight',sets:[],removedSets:[]})
  const root={type:'root',children:[]}
  const app=renderer.createApp(ActualCompleted,{draft:{exercises:[exercise('entry-0001','深蹲'),exercise('entry-0002','卧推')]},statusMessage:'待同步',busy:false,cloud:null})
  app.component('scroll-view',defineComponent({inheritAttrs:false,setup:(_p,{slots,attrs})=>()=>h('scroll-view',attrs,slots.default?.())}))
  try {
    app.mount(root);await new Promise(resolve=>setTimeout(resolve,120));await nextTick()
    const nav=find(root,node=>node.props?.class==='completed__navigation-inner')
    assert.ok(nav);assert.equal(find(nav,node=>node.type==='button'&&node.text==='深蹲'),undefined)
    button(nav,'卧推').props.onTap();await nextTick()
    const list=find(root,node=>node.type==='scroll-view'&&node.props?.class==='completed__list')
    assert.equal(list.props['scroll-into-view'],'completed-action-1')
  } finally { app.unmount();globalThis.uni=previous }
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
    draft: { date: '2026-11-01', timeZone: 'Pacific/Kiritimati', mode: 'after_workout', exercises: [], recordFormat: 2 },
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
    onCopySet: (...args) => actions.push(['copy', ...args]),
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
  const copy = find(root, node => node.type === 'button' && node.props?.['aria-label'] === '复制第 1 组')
  assert.ok(copy)
  copy.props.onTap({ stopPropagation() {} })
  const setCard = find(root, node => node.type === 'button' && node.props?.class === 'swipe-card__face')
  assert.ok(setCard)
  setCard.props.onTap()
  await nextTick()
  const inputs = findAll(root, node => node.type === 'input')
  assert.equal(inputs.length, 2)
  inputs[0].props.onInput({ detail: { value: '50' } })
  button(root, '记下').props.onTap()
  button(root, '×').props.onTap()
  setCard.props.onLongpress()
  await nextTick()
  const remove = find(root, node => node.type === 'button' && node.props?.['aria-label'] === '移除第 1 组')
  assert.ok(remove)
  assert.equal(remove.props.disabled, false)
  remove.props.onTap({ stopPropagation() {} })
  assert.deepEqual(actions, [ ['copy', 'entry-0002', 'record-002'], ['edit', 'entry-0002', 'record-002', 'weightKg', '50'],
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

test('the group sheet keeps edits after closing and waits for successful per-set confirmation', async () => {
  const state = reactive({ draft: { date: '2026-10-28', mode: 'after_workout', exercises: [{
    exerciseEntryKey: 'entry-0001', identity: { source: 'custom', exerciseName: '深蹲' }, kind: 'weight',
    sets: [{ recordKey: 'record-001', setIndex: 1, weightKg: '40', reps: '8', seconds: '', rpe: '', confirmed: false, persisted: false, dirty: false }],
  }] } })
  const root = { type: 'root', children: [] }
  let attempts = 0
  const app = renderer.createApp(defineComponent({ setup: () => () => h(ActualEditor, {
    draft: state.draft, latestAllowedDate: '2026-10-28', exerciseLabels: {}, issues: [], statusMessage: '', saving: false, canSave: false,
    onEditSet: (_exercise, _record, field, value) => { state.draft.exercises[0].sets[0][field] = value; state.draft.exercises[0].sets[0].confirmed = false },
    onConfirmSet: () => { attempts++; const set = state.draft.exercises[0].sets[0]; if (set.weightKg && set.reps) set.confirmed = true },
  }) }))
  app.component('picker', defineComponent({ setup: (_props, { slots }) => () => h('picker', {}, slots.default?.()) }))
  app.component('scroll-view', defineComponent({ setup: (_props, { slots }) => () => h('scroll-view', {}, slots.default?.()) }))
  app.mount(root)
  const open = async () => { find(root, node => node.type === 'button' && node.props?.class === 'swipe-card__face').props.onTap(); await nextTick() }
  await open()
  let inputs = findAll(root, node => node.type === 'input')
  inputs[0].props.onInput({ detail: { value: '45' } })
  await nextTick()
  button(root, '完成输入').props.onTap()
  await nextTick()
  await open()
  inputs = findAll(root, node => node.type === 'input')
  assert.equal(inputs[0].props.value, '45')
  assert.equal(inputs[1].props.value, '8')
  const dragArea = find(root, node => node.props?.class === 'set-sheet__drag-area')
  assert.ok(dragArea)
  dragArea.props.onTouchstart({ touches: [{ clientX: 100, clientY: 100 }] })
  dragArea.props.onTouchend({ changedTouches: [{ clientX: 200, clientY: 145 }] })
  await nextTick()
  assert.ok(find(root, node => node.props?.class === 'set-sheet'))
  dragArea.props.onTouchstart({ touches: [{ clientX: 100, clientY: 100 }] })
  dragArea.props.onTouchend({ changedTouches: [{ clientX: 105, clientY: 180 }] })
  await nextTick()
  assert.equal(find(root, node => node.props?.class === 'set-sheet'), undefined)
  await open()
  inputs = findAll(root, node => node.type === 'input')
  assert.equal(inputs[0].props.value, '45')
  state.draft.mode = 'per_set'
  await nextTick()
  assert.equal(find(root, node => node.props?.class === 'set-sheet'), undefined)
  await open()
  inputs = findAll(root, node => node.type === 'input')
  inputs[1].props.onInput({ detail: { value: '' } })
  await nextTick()
  button(root, '记下').props.onTap()
  await nextTick()
  assert.equal(attempts, 1)
  assert.ok(find(root, node => node.props?.class === 'set-sheet'))
  inputs[1].props.onInput({ detail: { value: '10' } })
  await nextTick()
  button(root, '记下').props.onTap()
  await nextTick()
  assert.equal(attempts, 2)
  assert.equal(find(root, node => node.props?.class === 'set-sheet'), undefined)
  assert.equal(state.draft.exercises[0].sets[0].confirmed, true)
  assert.equal(state.draft.exercises[0].sets[0].weightKg, '45')
  app.unmount()
})

test('group swipe reveals removal, undo restores input, and removed list remains recoverable', async () => {
  const original = { recordKey: 'record-001', setIndex: 1, weightKg: '45', reps: '8', seconds: '', rpe: '', confirmed: false, persisted: false, dirty: true }
  const state = reactive({ draft: { date: '2026-10-28', mode: 'after_workout', exercises: [{
    exerciseEntryKey: 'entry-0001', identity: { source: 'custom', exerciseName: '深蹲' }, kind: 'weight', sets: [original],
  }] } })
  const root = { type: 'root', children: [] }
  const app = renderer.createApp(defineComponent({ setup: () => () => h(ActualEditor, {
    draft: state.draft, latestAllowedDate: '2026-10-28', exerciseLabels: {}, issues: [], statusMessage: '', saving: false, canSave: false,
    onRemoveSet: () => { const e = state.draft.exercises[0]; state.draft = { ...state.draft, exercises: [{ ...e, sets: [], removedSets: [{ set: e.sets[0], removedPosition: 1 }] }] } },
    onRestoreSet: () => { const e = state.draft.exercises[0]; state.draft = { ...state.draft, exercises: [{ ...e, sets: [e.removedSets[0].set], removedSets: [] }] } },
  }) }))
  app.component('picker', defineComponent({ setup: (_props, { slots }) => () => h('picker', {}, slots.default?.()) }))
  app.component('scroll-view', defineComponent({ setup: (_props, { slots }) => () => h('scroll-view', {}, slots.default?.()) }))
  app.mount(root)
  const reveal = async () => {
    const card = find(root, node => node.props?.class === 'swipe-card')
    card.props.onTouchstart({ touches: [{ clientX: 200, clientY: 200 }] })
    card.props.onTouchmove({ touches: [{ clientX: 110, clientY: 203 }] })
    card.props.onTouchend()
    await nextTick()
    const remove = find(root, node => node.props?.class === 'swipe-card__remove')
    assert.equal(remove.props.disabled, false)
    remove.props.onTap({ stopPropagation() {} })
    await nextTick()
  }
  await reveal()
  assert.ok(find(root, node => node.props?.class === 'undo-notice'))
  button(root, '撤销').props.onTap()
  await nextTick()
  assert.equal(state.draft.exercises[0].sets[0].weightKg, '45')
  assert.equal(state.draft.exercises[0].sets[0].reps, '8')
  await reveal()
  const toggle = find(root, node => node.props?.class === 'removed-groups__toggle')
  toggle.props.onTap()
  await nextTick()
  assert.ok(find(root, node => node.text?.includes('移除时为第 1 组')))
  assert.equal(find(root, node => node.props?.class === 'removed-groups__values').text, '45 kg × 8 次')
  button(root, '恢复').props.onTap()
  await nextTick()
  assert.equal(state.draft.exercises[0].sets.length, 1)
  app.unmount()
})

test('conflict review requires a choice for every displayed difference',async()=>{
  const state=reactive({choices:{}});let confirmed=0,cancelled=0
  const review={hasCloudAdditions:true,rows:[{key:'set:record-001',label:'深蹲 · 第 1 组',local:'48 kg × 8 次',cloud:'52 kg × 8 次'},
    {key:'date',label:'训练日期',local:'2026-11-02',cloud:'2026-11-03'}]}
  const root={type:'root',children:[]}
  const app=renderer.createApp(defineComponent({setup:()=>()=>h(ActualConflict,{review,choices:state.choices,busy:false,error:'',
    onChoose:(key,value)=>{state.choices={...state.choices,[key]:value}},onConfirm:()=>confirmed++,onCancel:()=>cancelled++})}))
  app.component('scroll-view',defineComponent({setup:(_p,{slots})=>()=>h('scroll-view',{},slots.default?.())}))
  try {
    app.mount(root);await nextTick()
    assert.ok(find(root,node=>node.text?.includes('另一台设备新增的动作和组会一并保留')))
    const confirm=button(root,'保存选择');assert.equal(confirm.props.disabled,true)
    let options=findAll(root,node=>node.type==='button'&&String(node.props?.class||'').includes('conflict-review__option'))
    assert.equal(options.length,4);options[0].props.onTap();await nextTick();assert.equal(button(root,'保存选择').props.disabled,true)
    options=findAll(root,node=>node.type==='button'&&String(node.props?.class||'').includes('conflict-review__option'))
    options[3].props.onTap();await nextTick();assert.equal(button(root,'保存选择').props.disabled,false)
    button(root,'保存选择').props.onTap();button(root,'稍后处理').props.onTap()
    assert.equal(confirmed,1);assert.equal(cancelled,1);assert.deepEqual({...state.choices},{'set:record-001':'local',date:'cloud'})
  } finally { app.unmount() }
})

test('training page opens conflict review, preserves a cancelled choice, and applies the reviewed cloud group',async()=>{
  const { restoreCloudRecordingV2ToLocalDraft }=await import('./cloudRecordingV2Restore.ts')
  const context=fixture(true);const lease={subjectId:'101',generation:'session-one'}
  const detail={id:'42',version:2,source:'free',date:'2026-11-04',timeZone:'Asia/Shanghai',clientEventId:'cloud-event-02',
    status:'in_progress',mode:'after_workout',createdAt:'2026-11-04T00:00:00Z',updatedAt:'2026-11-04T01:00:00Z',
    entries:[{exerciseEntryKey:'cloud-entry-02',source:'custom',exerciseName:'深蹲'}],
    sets:[{recordKey:'cloud-set-002',exerciseEntryKey:'cloud-entry-02',setIndex:1,confirmed:false,measurement:{method:'total',weightKg:'45',reps:8}}],removedSets:[]}
  const api=new RecordingApi('https://api.example.org',context.session,async request=>{
    if(request.method==='GET')return {statusCode:200,data:structuredClone(detail)}
    return {statusCode:503,data:{}}
  },lease)
  context.services.restore=(_session,adapter,sessionId)=>restoreCloudRecordingV2ToLocalDraft({sessionId,api:adapter,
    auth:context.session,draftLease:context.lease,lease,storage:context.storage,ids:context.ids,now:()=>new Date()})
  const {root,app}=mount(context,'Asia/Shanghai',api)
  try {
    await flush();current.historyEmit('edit-v2','42','cloud-event-02');await flush();await flush()
    current.editorEmit('edit-set','cloud-entry-02','cloud-set-002','weightKg','48');await flush()
    detail.version++;detail.sets[0].measurement.weightKg='52'
    await new Promise(resolve=>setTimeout(resolve,850));await flush();await flush()
    button(root,'查看差异').props.onTap();await flush();await flush()
    assert.match(current.conflictProps.review.rows[0].local,/48 kg/);assert.match(current.conflictProps.review.rows[0].cloud,/52 kg/)
    current.conflictEmit('cancel');await flush();assert.equal(current.editorProps.draft.exercises[0].sets[0].weightKg,'48')
    button(root,'查看差异').props.onTap();await flush();await flush()
    const key=current.conflictProps.review.rows[0].key
    current.conflictEmit('choose',key,'cloud');await flush();context.fail(true)
    current.conflictEmit('confirm');await flush();await flush()
    assert.match(current.conflictProps.error,/本机保存尚未确认/);assert.equal(current.conflictProps.busy,true)
    assert.equal(detail.sets[0].measurement.weightKg,'52')
    context.fail(false);button(root,'重试本机保存').props.onTap();await flush();await flush()
    assert.equal(current.editorProps.draft.exercises[0].sets[0].weightKg,'52')
    assert.equal(detail.sets[0].measurement.weightKg,'52');assert.match(current.editorProps.statusMessage,/已同步/)
  } finally { app.unmount() }
})

test('an unchanged open draft shows completed details after another device finishes it',async()=>{
  const {restoreCloudRecordingV2ToLocalDraft}=await import('./cloudRecordingV2Restore.ts')
  const prior=globalThis.uni;let networkListener
  globalThis.uni={onNetworkStatusChange(callback){networkListener=callback},offNetworkStatusChange(){}}
  const context=fixture(true),lease={subjectId:'101',generation:'session-one'}
  const detail={id:'42',version:2,source:'free',date:'2026-11-04',timeZone:'Asia/Shanghai',clientEventId:'cloud-event-03',
    status:'in_progress',mode:'after_workout',createdAt:'2026-11-04T00:00:00Z',updatedAt:'2026-11-04T01:00:00Z',
    entries:[{exerciseEntryKey:'cloud-entry-03',source:'custom',exerciseName:'深蹲'}],
    sets:[{recordKey:'cloud-set-003',exerciseEntryKey:'cloud-entry-03',setIndex:1,confirmed:false,measurement:{method:'total',weightKg:'45',reps:8}}],removedSets:[]}
  const api=new RecordingApi('https://api.example.org',context.session,async()=>({statusCode:200,data:structuredClone(detail)}),lease)
  context.services.restore=(_session,adapter,sessionId)=>restoreCloudRecordingV2ToLocalDraft({sessionId,api:adapter,
    auth:context.session,draftLease:context.lease,lease,storage:context.storage,ids:context.ids,now:()=>new Date()})
  const {app}=mount(context,'Asia/Shanghai',api)
  try {
    await flush();current.historyEmit('edit-v2','42','cloud-event-03');await flush();await flush()
    assert.equal(current.editorProps.draft.completionRequested,false)
    detail.version++;detail.status='completed';detail.recordedAt='2026-11-04T02:00:00Z'
    networkListener({isConnected:true});await flush();await flush();await flush()
    assert.equal(current.completionProps.draft.completionRequested,true)
    assert.match(current.completionProps.statusMessage,/已同步/)
  } finally {app.unmount();globalThis.uni=prior}
})

test('batch selection applies only chosen fields and groups and can add groups in both modes', async () => {
  for (const mode of ['after_workout','per_set']) {
    const group = (key,index,weight,reps) => ({recordKey:key,setIndex:index,weightKg:weight,reps,seconds:'',rpe:'',confirmed:false,persisted:false,dirty:true})
    const state = reactive({draft:{recordFormat:2,date:'2026-11-16',mode,exercises:[{exerciseEntryKey:'entry-0001',
      identity:{source:'custom',exerciseName:'深蹲'},kind:'weight',sets:[
        group('record-001',1,'45','8'),group('record-002',2,'50','6'),group('record-003',3,'30','10')]}]}})
    const events=[]
    const root={type:'root',children:[]}
    const app=renderer.createApp(defineComponent({setup:()=>()=>h(ActualEditor,{
      draft:state.draft,latestAllowedDate:'2026-11-16',exerciseLabels:{},issues:[],statusMessage:'',saving:false,canSave:true,
      onApplyBatch:(...args)=>events.push(args),
    })}))
    app.component('picker', defineComponent({setup:(_p,{slots})=>()=>h('picker',{},slots.default?.())}))
    app.component('scroll-view', defineComponent({setup:(_p,{slots})=>()=>h('scroll-view',{},slots.default?.())}))
    app.mount(root)
    try {
      find(root,node=>node.type==='button' && node.props?.class==='swipe-card__face').props.onTap()
      await nextTick()
      find(root,node=>node.type==='button' && node.props?.class==='batch-entry').props.onTap()
      await nextTick()
      const fields=findAll(root,node=>node.type==='button' && String(node.props?.class||'').includes('batch-field'))
      assert.equal(fields.length,2)
      fields[1].props.onTap()
      const groups=findAll(root,node=>node.type==='button' && String(node.props?.class||'').includes('batch-group'))
      assert.equal(groups.length,3)
      groups[2].props.onTap()
      const plus=find(root,node=>node.type==='button' && node.props?.['aria-label']==='增加新增组数')
      plus.props.onTap();plus.props.onTap()
      await nextTick()
      const apply=find(root,node=>node.type==='button' && node.props?.class==='set-sheet__done')
      assert.equal(apply.props.disabled,false)
      apply.props.onTap()
      await nextTick()
      assert.equal(events.length,1)
      assert.deepEqual(events[0].slice(0,5),['entry-0001','record-001',['record-001','record-003'],['weightKg'],2])
      assert.equal(events[0][5],batchSetSnapshot(state.draft.exercises[0]))
      assert.equal(find(root,node=>node.props?.class==='set-sheet'),undefined)
    } finally { app.unmount() }
  }
})

test('batch selection blocks stale groups until the user returns to the input', async () => {
  const set={recordKey:'record-001',setIndex:1,weightKg:'45',reps:'8',seconds:'',rpe:'',confirmed:false,persisted:false,dirty:true}
  const state=reactive({draft:{recordFormat:2,date:'2026-11-16',mode:'after_workout',exercises:[{
    exerciseEntryKey:'entry-0001',identity:{source:'custom',exerciseName:'深蹲'},kind:'weight',sets:[set]}]}})
  const root={type:'root',children:[]};const events=[]
  const app=renderer.createApp(defineComponent({setup:()=>()=>h(ActualEditor,{draft:state.draft,
    latestAllowedDate:'2026-11-16',exerciseLabels:{},issues:[],statusMessage:'',saving:false,canSave:true,
    onApplyBatch:(...args)=>events.push(args)})}))
  app.component('picker', defineComponent({setup:(_p,{slots})=>()=>h('picker',{},slots.default?.())}))
  app.component('scroll-view', defineComponent({setup:(_p,{slots})=>()=>h('scroll-view',{},slots.default?.())}))
  app.mount(root)
  try {
    find(root,node=>node.type==='button' && node.props?.class==='swipe-card__face').props.onTap();await nextTick()
    find(root,node=>node.type==='button' && node.props?.class==='batch-entry').props.onTap();await nextTick()
    state.draft.exercises[0].sets[0].weightKg='50';await nextTick()
    assert.match(find(root,node=>node.props?.class==='batch-warning').text,/重新选择/)
    assert.equal(find(root,node=>node.type==='button' && node.props?.class==='set-sheet__done').props.disabled,true)
    assert.equal(events.length,0)
  } finally {app.unmount()}
})

test('batch changes are atomically persisted through the protected editor and can be retried', async () => {
  const context=fixture(true);const {root,app}=mount(context)
  try {
    await flush();button(root,'新建记录').props.onTap();await flush()
    current.editorEmit('add-exercise');await flush()
    find(root,node=>node.type==='input').props.onInput({detail:{value:'深蹲'}});await flush()
    button(root,'添加').props.onTap();await flush()
    let exercise=current.editorProps.draft.exercises[0];const entry=exercise.exerciseEntryKey;const source=exercise.sets[0].recordKey
    current.editorEmit('edit-set',entry,source,'weightKg','45');await flush()
    current.editorEmit('edit-set',entry,source,'reps','8');await flush()
    current.editorEmit('add-set',entry);await flush()
    exercise=current.editorProps.draft.exercises[0]
    const selected=exercise.sets[1].recordKey;const snapshot=batchSetSnapshot(exercise)
    context.fail(true)
    current.editorEmit('apply-batch',entry,source,[selected],['weightKg'],1,snapshot);await flush()
    assert.match(current.editorProps.statusMessage,/尚未确认/)
    assert.deepEqual(current.editorProps.draft.exercises[0].sets.map(s=>[s.weightKg,s.reps]),[['45','8'],['45',''],['45','']])
    const storedBefore=JSON.parse([...context.records.values()][0].value).draft
    assert.equal(storedBefore.exercises[0].sets.length,2)
    context.fail(false);button(root,'重试本机保存').props.onTap();await flush()
    const storedAfter=JSON.parse([...context.records.values()][0].value).draft
    assert.deepEqual(storedAfter.exercises[0].sets.map(s=>[s.weightKg,s.reps]),[['45','8'],['45',''],['45','']])
  } finally {app.unmount()}
})

test('batch selection rejects an invalid selected value while allowing a valid field alone', async () => {
  const set={recordKey:'record-001',setIndex:1,weightKg:'45',reps:'101',seconds:'',rpe:'',confirmed:false,persisted:false,dirty:true}
  const draft={recordFormat:2,date:'2026-11-16',mode:'after_workout',exercises:[{exerciseEntryKey:'entry-0001',
    identity:{source:'custom',exerciseName:'深蹲'},kind:'weight',sets:[set]}]}
  const root={type:'root',children:[]}
  const app=renderer.createApp(ActualEditor,{draft,latestAllowedDate:'2026-11-16',exerciseLabels:{},issues:[],statusMessage:'',saving:false,canSave:true})
  app.component('picker', defineComponent({setup:(_p,{slots})=>()=>h('picker',{},slots.default?.())}))
  app.component('scroll-view', defineComponent({setup:(_p,{slots})=>()=>h('scroll-view',{},slots.default?.())}))
  app.mount(root)
  try {
    find(root,node=>node.type==='button' && node.props?.class==='swipe-card__face').props.onTap();await nextTick()
    find(root,node=>node.type==='button' && node.props?.class==='batch-entry').props.onTap();await nextTick()
    assert.equal(find(root,node=>node.type==='button' && node.props?.class==='set-sheet__done').props.disabled,true)
    assert.match(find(root,node=>node.props?.class==='batch-warning').text,/格式不正确/)
    const fields=findAll(root,node=>node.type==='button' && String(node.props?.class||'').includes('batch-field'))
    fields[1].props.onTap();await nextTick()
    assert.equal(find(root,node=>node.type==='button' && node.props?.class==='set-sheet__done').props.disabled,false)
  } finally {app.unmount()}
})
