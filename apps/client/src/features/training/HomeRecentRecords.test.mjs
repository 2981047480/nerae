import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { createRenderer, nextTick } from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'
import ts from 'typescript'

const source = readFileSync(new URL('./HomeRecentRecords.vue', import.meta.url), 'utf8')
const style = source.slice(source.lastIndexOf('<style'))
assert.equal(source.includes('<image'), false)
assert.equal(source.includes('已计算'), false)
assert.equal(style.includes('border-left'), false)
assert.match(style, /\.recent-records__values \{[^}]*align-items:\s*flex-end/)
assert.match(style, /\.recent-records__value \{[^}]*font-variant-numeric:\s*tabular-nums/)
assert.match(style, /\.recent-records__value \{[^}]*text-align:\s*right/)

const vueURL = import.meta.resolve('vue')
const apiURL = new URL('./recordingApi.ts', import.meta.url)
const { RecordingApiError } = await import('./recordingApi.ts')
registerHooks({ resolve(specifier, context, nextResolve) {
  if (context.parentURL?.startsWith('data:text/javascript')) {
    if (specifier === 'vue') return { url: vueURL, shortCircuit: true }
    if (specifier === './recordingApi') return { url: apiURL.href, shortCircuit: true }
    if (specifier === './recordingV2Projection') return { url: new URL('./recordingV2Projection.ts', import.meta.url).href, shortCircuit: true }
    if (specifier === './recordingV2Measure') return { url: new URL('./recordingV2Measure.ts', import.meta.url).href, shortCircuit: true }
  }
  return nextResolve(specifier, context)
} })
const descriptor = parse(source, { filename: 'HomeRecentRecords.vue' }).descriptor
const compiled = compileScript(descriptor, { id: 'home-recent-records-test', inlineTemplate: true }).content
const js = ts.transpileModule(compiled, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { default: Page } = await import(`data:text/javascript,${encodeURIComponent(js)}`)

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
    if (index < 0) parent.children.push(node); else parent.children.splice(index, 0, node)
  },
  remove(node) {
    if (!node.parent) return
    const index = node.parent.children.indexOf(node)
    if (index >= 0) node.parent.children.splice(index, 1)
    node.parent = null
  },
  parentNode: node => node.parent,
  nextSibling(node) { return node.parent?.children[node.parent.children.indexOf(node) + 1] || null },
})
const find = (node, predicate) => predicate(node) ? node : (node.children || []).map(child => find(child, predicate)).find(Boolean)
const texts = node => {
  const found = []
  const visit = item => { if (item.text) found.push(item.text); for (const child of item.children || []) visit(child) }
  visit(node)
  return found
}
const inventedZero = text => /(?:^|[^\d.])0(?:\.0+)? kg/.test(text) || /(?:^|[^\d])0 次/.test(text)
const flush = async () => { await nextTick(); await new Promise(resolve => setImmediate(resolve)); await nextTick() }
const lease = { subjectId: '101', generation: 'session-one' }
const summary = { id: '42', version: 3, recordFormat: 2, date: '2026-11-02', timeZone: 'Asia/Shanghai', status: 'completed' }

function fixture(api, owner = lease) {
  let active = true
  const listeners = new Set()
  const session = {
    snapshot: () => ({ ...owner }),
    isCurrent: (subjectId, generation) => active && subjectId === owner.subjectId && generation === owner.generation,
    captureInstallGuard: () => ({}),
    subscribeInstallGuardInvalidation: (_guard, listener) => { listeners.add(listener); return () => listeners.delete(listener) },
    invalidate() { active = false; for (const listener of [...listeners]) listener() },
  }
  const root = { type: 'root', children: [] }
  let changes = 0
  const app = renderer.createApp(Page, { session, api, onSessionChanged: () => changes++ })
  app.mount(root)
  return { root, app, session, changes: () => changes }
}

function button(root, label) {
  const result = find(root, node => node.type === 'button' && node.text === label)
  assert.ok(result, `missing button ${label}`)
  return result
}

const v2Detail = {
  id: '42', clientEventId: 'event-0001', date: '2026-11-02', status: 'completed',
  entries: [
    { exerciseEntryKey: 'entry-0001', source: 'custom', exerciseName: '深蹲', volume: { calculatedKg: '284.00', fullSets: 2, partialSets: 0, excludedSets: 1 } },
    { exerciseEntryKey: 'entry-0002', source: 'custom', exerciseName: '卧推', removedPosition: 1 },
    { exerciseEntryKey: 'entry-0003', source: 'custom', exerciseName: '深蹲' },
  ],
  sets: [
    { recordKey: 'record-003', exerciseEntryKey: 'entry-0001', setIndex: 3, measurement: { method: 'total' } },
    { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', setIndex: 1, measurement: { method: 'total', weightKg: '40', reps: 8 } },
    { recordKey: 'record-002', exerciseEntryKey: 'entry-0001', setIndex: 2, measurement: { method: 'total', weightKg: '42' } },
    { recordKey: 'record-004', exerciseEntryKey: 'entry-0003', setIndex: 1, measurement: { method: 'sides', left: { state: 'performed', weightKg: '12', reps: 10 }, right: { state: 'not_performed' } } },
  ],
  removedSets: [],
}

test('the latest saved record shows each retained set, with missing values left blank', async () => {
  const calls = []
  const api = {
    async listRecordingsV2(input) {
      calls.push(input)
      return { lease, page: 1, pageSize: 1, total: 2, items: [summary, { ...summary, id: '7', date: '2026-10-01' }] }
    },
    async readRecordingV2(id) {
      calls.push(id)
      assert.equal(id, '42')
      return { lease, detail: v2Detail }
    },
  }
  const { root, app } = fixture(api)
  await flush()
  const shown = texts(root)
  assert.deepEqual(calls, [{ page: 1, pageSize: 1 }, '42'])
  assert.equal(shown.filter(item => item === '深蹲').length, 2)
  assert.equal(shown.includes('卧推'), false)
  assert.ok(shown.includes('40 kg × 8 次'))
  assert.ok(shown.includes('42 kg × 次数未填'))
  assert.ok(shown.includes('重量未填 × 次数未填'))
  assert.ok(shown.includes('左 12 kg × 10 次 · 右 未做'))
  assert.equal(shown.some(item => inventedZero(item) || item.includes('284') || item.includes('已计算')), false)
  assert.ok(shown.includes('2026-11-02'))
  assert.equal(shown.includes('2026-10-01'), false)
  assert.ok(shown.includes('已完成'))
  const row = find(root, node => node.props?.class === 'recent-records__row')
  assert.ok(row)
  assert.equal(row.children.some(child => child.props?.class === 'recent-records__name'), true)
  assert.equal(row.children.some(child => child.props?.class === 'recent-records__values'), true)
  app.unmount()
})

test('an empty cloud history is distinct from a failed read, and the read can be retried', async () => {
  let attempts = 0
  const api = { async listRecordingsV2() {
    if (++attempts === 1) throw new Error('network')
    return { lease, page: 1, pageSize: 1, total: 0, items: [] }
  } }
  const { root, app } = fixture(api)
  await flush()
  assert.ok(find(root, node => node.text === '暂时无法读取云端记录，请稍后重试。'))
  assert.equal(find(root, node => node.text === '还没有已保存到云端的训练。'), undefined)
  button(root, '重试云端记录').props.onTap()
  await flush()
  assert.ok(find(root, node => node.text === '还没有已保存到云端的训练。'))
  assert.equal(find(root, node => node.text === '暂时无法读取云端记录，请稍后重试。'), undefined)
  app.unmount()
})

test('a mismatched lease is not shown as an empty history', async () => {
  const api = { async listRecordingsV2() { return { lease: { subjectId: '202', generation: 'session-one' }, page: 1, pageSize: 1, total: 1, items: [summary] } } }
  const { root, app, changes } = fixture(api)
  await flush()
  assert.equal(changes(), 0)
  assert.ok(find(root, node => node.text === '暂时无法读取云端记录，请稍后重试。'))
  assert.equal(find(root, node => node.text === '还没有已保存到云端的训练。'), undefined)
  assert.equal(find(root, node => node.text === '2026-11-02'), undefined)
  app.unmount()
})

test('account changes discard an in-flight recent record', async () => {
  let release
  const waiting = new Promise(resolve => { release = resolve })
  const { root, app, session, changes } = fixture({ listRecordingsV2: () => waiting })
  await flush()
  session.invalidate()
  release({ lease, page: 1, pageSize: 1, total: 1, items: [summary] })
  await flush()
  assert.equal(changes(), 1)
  assert.equal(find(root, node => node.text === '2026-11-02'), undefined)
  assert.equal(find(root, node => node.text === '还没有已保存到云端的训练。'), undefined)
  app.unmount()
})

test('a denied detail read clears the account instead of showing an empty record', async () => {
  const api = {
    async listRecordingsV2() { return { lease, page: 1, pageSize: 1, total: 1, items: [summary] } },
    async readRecordingV2() { throw new RecordingApiError('forbidden', 403) },
  }
  const { root, app, changes } = fixture(api)
  await flush()
  assert.equal(changes(), 1)
  assert.equal(find(root, node => node.text === '2026-11-02'), undefined)
  assert.equal(find(root, node => node.text === '还没有已保存到云端的训练。'), undefined)
  app.unmount()
})

test('an in-flight detail from the previous account is not painted', async () => {
  let release
  const waiting = new Promise(resolve => { release = resolve })
  const api = {
    async listRecordingsV2() { return { lease, page: 1, pageSize: 1, total: 1, items: [summary] } },
    readRecordingV2: () => waiting,
  }
  const { root, app, session, changes } = fixture(api)
  await flush()
  session.invalidate()
  release({ lease, detail: v2Detail })
  await flush()
  assert.equal(changes(), 1)
  assert.equal(texts(root).includes('深蹲'), false)
  app.unmount()
})

test('a legacy record keeps separate segments and does not turn a missing weight into zero', async () => {
  const api = {
    async listRecordingsV2() { return { lease, page: 1, pageSize: 1, total: 1, items: [{ ...summary, recordFormat: 1, status: 'in_progress' }] } },
    async readSession() {
      return { lease, detail: {
        date: '2026-11-01', status: 'in_progress', exerciseEntryKeys: ['entry-0001', 'entry-0002'],
        sets: [
          { recordKey: 'record-b', exerciseEntryKey: 'entry-0001', setIndex: 2, kind: 'weight', resolvedExerciseName: '深蹲', exerciseName: '深蹲', reps: 8 },
          { recordKey: 'record-a', exerciseEntryKey: 'entry-0001', setIndex: 1, kind: 'weight', resolvedExerciseName: '深蹲', exerciseName: '深蹲', weightKg: 40, reps: 8 },
          { recordKey: 'record-c', exerciseEntryKey: 'entry-0002', setIndex: 1, kind: 'weight', resolvedExerciseName: '深蹲', exerciseName: '旧名', weightKg: 60, reps: 5 },
        ],
      } }
    },
  }
  const { root, app } = fixture(api)
  await flush()
  const shown = texts(root)
  assert.equal(shown.filter(item => item === '深蹲').length, 2)
  assert.equal(shown.includes('旧名'), false)
  assert.ok(shown.includes('40 kg × 8 次'))
  assert.ok(shown.includes('8 次'))
  assert.ok(shown.includes('60 kg × 5 次'))
  assert.equal(shown.some(inventedZero), false)
  assert.ok(shown.includes('未完成'))
  const firstValues = find(root, node => node.props?.class === 'recent-records__values')
  assert.deepEqual(firstValues.children.map(child => child.text).filter(Boolean), ['40 kg × 8 次', '8 次'])
  app.unmount()
})
