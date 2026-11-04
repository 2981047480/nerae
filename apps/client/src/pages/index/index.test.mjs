import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { createRenderer, defineComponent, h, nextTick, onBeforeUnmount, onMounted } from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'
import ts from 'typescript'
import { AuthSessionCoordinator } from '../../features/auth/sessionCoordinator.ts'

const pageURL = new URL('./index.vue', import.meta.url)
const gateURL = new URL('../../features/auth/appEntrySession.ts', import.meta.url)
const vueURL = import.meta.resolve('vue')
const current = { runtime: null, authMounts: 0, panelMounts: 0, authEmit: null, panelEmit: null,
  backHandler: null, trainingEmit: null, trainingCanLeave: true, recordingApi: null }
globalThis.__neraeEntryTest = current

const AuthStub = defineComponent({
  props: ['runtime', 'allowCancel'], emits: ['authenticated', 'cancel'],
  setup(props, { emit }) {
    current.authMounts++
    current.authEmit = emit
    let active = true
    onMounted(async () => {
      const result = await props.runtime.session.restore()
      if (active && result.status === 'authenticated' && result.session.user.ageVerified) emit('authenticated', result.session)
    })
    onBeforeUnmount(() => { active = false })
    return () => h('auth-entry', { allowCancel: props.allowCancel })
  },
})
const PanelStub = defineComponent({
  emits: ['session-changed'],
  setup(_props, { emit }) {
    current.panelMounts++
    current.panelEmit = emit
    return () => h('approval-panel')
  },
})
const TrainingStub = defineComponent({
  props: ['session', 'timeZone', 'recordingApi'], emits: ['exit', 'session-changed'],
  setup(props, { emit, expose }) {
    current.trainingEmit = emit
    current.recordingApi = props.recordingApi
    expose({ canLeave: () => current.trainingCanLeave })
    return () => h('training-entry')
  },
})
current.AuthStub = AuthStub
current.PanelStub = PanelStub
current.TrainingStub = TrainingStub

registerHooks({ resolve(specifier, context, nextResolve) {
  if (context.parentURL?.startsWith('data:text/javascript')) {
    if (specifier === 'vue') return { url: vueURL, shortCircuit: true }
    if (specifier === '@dcloudio/uni-app') return { url: 'data:text/javascript,export const onBackPress = callback => { globalThis.__neraeEntryTest.backHandler = callback }', shortCircuit: true }
    if (specifier.endsWith('/AuthEntry.vue')) return { url: 'data:text/javascript,export default globalThis.__neraeEntryTest.AuthStub', shortCircuit: true }
    if (specifier.endsWith('/DeviceApprovalPanel.vue')) return { url: 'data:text/javascript,export default globalThis.__neraeEntryTest.PanelStub', shortCircuit: true }
    if (specifier.endsWith('/TrainingEntry.vue')) return { url: 'data:text/javascript,export default globalThis.__neraeEntryTest.TrainingStub', shortCircuit: true }
    if (specifier.endsWith('/appEntrySession')) return { url: gateURL.href, shortCircuit: true }
    if (specifier.endsWith('/nativeAuthRuntime')) return { url: 'data:text/javascript,export const createAppAuthRuntime = () => { if (!globalThis.__neraeEntryTest.runtime) throw Error(); return globalThis.__neraeEntryTest.runtime }', shortCircuit: true }
  }
  return nextResolve(specifier, context)
} })

// Compile the actual page with stubs for native and child-component boundaries.
// The test supplies a non-secret URL; it does not exercise uni-app preprocessing.
const source = readFileSync(pageURL, 'utf8').replace('import.meta.env.VITE_NERAE_API_BASE', "'https://api.example.test/v1'")
const descriptor = parse(source, { filename: 'index.vue' }).descriptor
const compiled = compileScript(descriptor, { id: 'nerae-entry-test', inlineTemplate: true }).content
const javascript = ts.transpileModule(compiled, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { default: Page } = await import(`data:text/javascript,${encodeURIComponent(javascript)}`)

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
const flush = async () => { await nextTick(); await new Promise(resolve => setImmediate(resolve)); await nextTick() }
function mount() {
  const root = { type: 'root', children: [] }
  const app = renderer.createApp(Page)
  app.mount(root)
  return { root, app }
}

test('an unavailable service configuration does not mount login or approval', async () => {
  current.runtime = null; current.authMounts = 0; current.panelMounts = 0
  const { root, app } = mount()
  await flush()
  assert.ok(find(root, node => node.text === '暂时无法登录'))
  assert.equal(current.authMounts, 0)
  assert.equal(current.panelMounts, 0)
  app.unmount()
})

test('an explicit session recheck keeps a valid account or returns a revoked one to login', async () => {
  let stored = JSON.stringify({ schema: 1, subjectId: '101', accessToken: 'synthetic-token' })
  let authorized = true
  const user = { id: '101', nickname: '', roles: ['student'], ageVerified: true }
  const session = new AuthSessionCoordinator({
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: !!stored, value: stored }),
    writeAuthenticationState: value => { stored = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState: () => { stored = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }, async () => authorized ? { status: 'valid', user } : { status: 'revoked' })
  current.runtime = { session, approvals: {} }; current.authMounts = 0; current.panelMounts = 0; current.panelEmit = null
  const { root, app } = mount()
  await flush()
  assert.ok(find(root, node => node.type === 'approval-panel'))
  assert.equal(current.authMounts, 1)
  assert.equal(current.panelMounts, 1)
  current.panelEmit('session-changed')
  await flush()
  assert.equal(find(root, node => node.type === 'approval-panel'), undefined)
  let button = find(root, node => node.type === 'button' && node.text === '重新检查')
  assert.ok(button); button.props.onClick()
  await flush()
  assert.ok(find(root, node => node.type === 'approval-panel'))
  assert.equal(current.authMounts, 2)
  assert.equal(current.panelMounts, 2)
  authorized = false
  current.panelEmit('session-changed')
  await flush()
  button = find(root, node => node.type === 'button' && node.text === '重新检查')
  assert.ok(button); button.props.onClick()
  await flush()
  assert.ok(find(root, node => node.type === 'auth-entry'))
  assert.equal(current.authMounts, 3)
  assert.equal(current.panelMounts, 2)
  assert.equal(session.snapshot(), null)
  assert.equal(stored, '')
  current.authEmit('cancel')
  await flush()
  assert.ok(find(root, node => node.type === 'auth-entry'))
  assert.equal(current.authMounts, 4, 'safe cancellation remounts the root login entry')
  app.unmount()
})

test('the signed-in recording entry blocks back navigation until its draft can be left', async () => {
  let stored = JSON.stringify({ schema: 1, subjectId: '101', accessToken: 'synthetic-token' })
  const user = { id: '101', nickname: '', roles: ['student'], ageVerified: true }
  const session = new AuthSessionCoordinator({
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: !!stored, value: stored }),
    writeAuthenticationState: value => { stored = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState: () => { stored = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }, async () => ({ status: 'valid', user }))
  const recordingApi = {}
  current.runtime = { session, approvals: {}, createRecordingApi: () => recordingApi }; current.trainingCanLeave = false; current.backHandler = null
  const { root, app } = mount()
  await flush()
  const start = find(root, node => node.type === 'button' && node.text === '开始记录')
  assert.ok(start)
  start.props.onClick()
  await flush()
  assert.ok(find(root, node => node.type === 'training-entry'))
  assert.equal(current.recordingApi, recordingApi)
  assert.equal(current.backHandler({ from: 'backbutton' }), true)
  await flush()
  assert.ok(find(root, node => node.type === 'training-entry'))
  current.trainingEmit('exit')
  await flush()
  assert.ok(find(root, node => node.type === 'training-entry'))
  current.trainingCanLeave = true
  assert.equal(current.backHandler({ from: 'backbutton' }), true)
  await flush()
  assert.equal(find(root, node => node.type === 'training-entry'), undefined)
  assert.ok(find(root, node => node.type === 'approval-panel'))
  find(root, node => node.type === 'button' && node.text === '开始记录').props.onClick()
  await flush()
  assert.ok(find(root, node => node.type === 'training-entry'))
  await session.logout()
  await flush()
  assert.equal(find(root, node => node.type === 'training-entry'), undefined)
  assert.ok(find(root, node => node.text === '登录状态已变化'))
  app.unmount()
})
