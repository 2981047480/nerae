import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { createRenderer, nextTick } from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'
import ts from 'typescript'

const vueURL = import.meta.resolve('vue')
const apiURL = new URL('./recordingApi.ts', import.meta.url)
const { RecordingApiError } = await import('./recordingApi.ts')
registerHooks({ resolve(specifier, context, nextResolve) {
  if (context.parentURL?.startsWith('data:text/javascript')) {
    if (specifier === 'vue') return { url: vueURL, shortCircuit: true }
    if (specifier === './recordingApi') return { url: apiURL.href, shortCircuit: true }
  }
  return nextResolve(specifier, context)
} })
const source = readFileSync(new URL('./TrainingHistory.vue', import.meta.url), 'utf8')
const descriptor = parse(source, { filename: 'TrainingHistory.vue' }).descriptor
const compiled = compileScript(descriptor, { id: 'training-history-test', inlineTemplate: true }).content
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
const flush = async () => { await nextTick(); await new Promise(resolve => setImmediate(resolve)); await nextTick() }
const lease = { subjectId: '101', generation: 'session-one' }
const summary = { id: '42', date: '2026-10-29', status: 'completed' }
function fixture(api) {
  let active = true
  const listeners = new Set()
  const session = {
    snapshot: () => ({ ...lease }),
    isCurrent: (subjectId, generation) => active && subjectId === lease.subjectId && generation === lease.generation,
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

test('authenticated history lists and reads saved training without touching local drafts', async () => {
  const calls = []
  const api = {
    async listSessions(input) { calls.push(input); return { lease, page: 1, pageSize: 20, total: 1, items: [summary] } },
    async readSession(id) {
      calls.push(id)
      return { lease, detail: { date: summary.date, sets: [{ recordKey: 'record-001', exerciseName: '深蹲', setIndex: 1, weightKg: 50, reps: 8 }] } }
    },
  }
  const { root, app } = fixture(api)
  await flush()
  assert.ok(find(root, node => node.text === '2026-10-29'))
  button(root, '查看').props.onTap()
  await flush()
  assert.ok(find(root, node => node.text === '50 kg · 8 次'))
  button(root, '返回云端列表').props.onTap()
  await flush()
  assert.deepEqual(calls, [{ page: 1, pageSize: 20 }, '42'])
  app.unmount()
})

test('a failed cloud read can be retried and an empty result is shown honestly', async () => {
  let attempts = 0
  const api = { async listSessions() {
    if (++attempts === 1) throw Error('network')
    return { lease, page: 1, pageSize: 20, total: 0, items: [] }
  } }
  const { root, app } = fixture(api)
  await flush()
  assert.ok(find(root, node => node.text === '暂时无法读取云端记录，请稍后重试。'))
  button(root, '重试云端记录').props.onTap()
  await flush()
  assert.ok(find(root, node => node.text === '还没有已保存到云端的训练。'))
  app.unmount()
})

test('account changes discard an in-flight cloud response', async () => {
  let release
  const waiting = new Promise(resolve => { release = resolve })
  const { root, app, session, changes } = fixture({ listSessions: () => waiting })
  session.invalidate()
  release({ lease, page: 1, pageSize: 20, total: 1, items: [summary] })
  await flush()
  assert.equal(changes(), 1)
  assert.equal(find(root, node => node.text === '2026-10-29'), undefined)
  app.unmount()
})

test('more cloud records load by page without repeating records', async () => {
  const calls = []
  const first = Array.from({ length: 20 }, (_, index) => ({ ...summary, id: String(index + 1), date: `2026-10-${String(index + 1).padStart(2, '0')}` }))
  const api = { async listSessions(input) {
    calls.push(input.page)
    return input.page === 1
      ? { lease, page: 1, pageSize: 20, total: 21, items: first }
      : { lease, page: 2, pageSize: 20, total: 21, items: [{ ...summary, id: '21' }] }
  } }
  const { root, app } = fixture(api)
  await flush()
  button(root, '加载更多云端记录').props.onTap()
  await flush()
  assert.deepEqual(calls, [1, 2])
  assert.equal(find(root, node => node.type === 'button' && node.text === '加载更多云端记录'), undefined)
  assert.ok(find(root, node => node.text === '2026-10-29'))
  app.unmount()
})

test('a denied record read clears cached account data and asks for a session recheck', async () => {
  const api = {
    async listSessions() { return { lease, page: 1, pageSize: 20, total: 1, items: [summary] } },
    async readSession() { throw new RecordingApiError('forbidden', 403) },
  }
  const { root, app, changes } = fixture(api)
  await flush()
  assert.ok(find(root, node => node.text === '2026-10-29'))
  button(root, '查看').props.onTap()
  await flush()
  assert.equal(changes(), 1)
  assert.equal(find(root, node => node.text === '2026-10-29'), undefined)
  app.unmount()
})
