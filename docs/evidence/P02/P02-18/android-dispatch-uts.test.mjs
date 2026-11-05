// Exercises the real UTS source as transpiled TypeScript with a synthetic
// bridge. It does not exercise DCloud's runtime, Android Keystore, or a device.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import vm from 'node:vm'
import test from 'node:test'

const require = createRequire(import.meta.url)
const ts = require(process.env.NERAE_TYPESCRIPT_MODULE || 'typescript')
const source = readFileSync(fileURLToPath(new URL('../../../../apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/index.uts', import.meta.url)), 'utf8')
const javascript = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText

function fixture(rawResult) {
  const calls = []
  const context = { label: 'synthetic Android context' }
  const android = { getAppContext: () => context }
  const bridge = { claimTrainingOperationDispatch: (...args) => {
    calls.push(args)
    if (rawResult instanceof Error) throw rawResult
    return rawResult
  } }
  const json = Object.create(JSON)
  json.parseObject = value => JSON.parse(value)
  const exports = {}
  vm.runInNewContext(javascript, { exports, JSON: json, UTSAndroid: android, NeraeAndroidSecureVaultBridge: bridge })
  return { claim: exports.claimTrainingOperationDispatch, calls, context }
}

const args = ['1', '{"entries":{}}', 'nerae.workout.operation.v1.12.event-0001', '{"entries":{"sent":true}}']
const encoded = (ok, errorCode, revision, found = false, value = '') => JSON.stringify({ ok, errorCode, found, value, revision })

test('Android first-dispatch forwards the complete native claim and returns its committed revision', () => {
  const f = fixture(encoded(true, '', '2'))
  const result = f.claim(...args)
  assert.equal(result.ok, true)
  assert.equal(result.revision, '2')
  assert.equal(result.found, false)
  assert.equal(result.value, '')
  assert.equal(f.calls.length, 1)
  assert.equal(f.calls[0][0], f.context)
  assert.deepEqual(f.calls[0].slice(1), args)
})

test('invalid inputs do not reach the native bridge', () => {
  const f = fixture(encoded(true, '', '2'))
  for (const invalid of [
    ['', ...args.slice(1)], ['0', ...args.slice(1)], ['-1', ...args.slice(1)],
    ['9223372036854775808', ...args.slice(1)], [args[0], '', ...args.slice(2)],
    [...args.slice(0, 2), '', args[3]], [...args.slice(0, 3), ''],
  ]) {
    const result = f.claim(...invalid)
    assert.equal(result.ok, false)
    assert.equal(result.errorCode, 'invalid_state')
    assert.equal(result.value, '')
    assert.equal(result.revision, '')
  }
  assert.equal(f.calls.length, 0)
})

test('native conflicts, invalid state and uncertain writes remain failures with no sensitive payload', () => {
  for (const code of ['invalid_state', 'conflict', 'unavailable']) {
    const f = fixture(encoded(false, code, '57', true, 'private source bytes'))
    const result = f.claim(...args)
    assert.equal(result.ok, false)
    assert.equal(result.errorCode, code)
    assert.equal(result.found, false)
    assert.equal(result.value, '')
    assert.equal(result.revision, '')
  }
})

test('unknown errors, malformed or misleading success replies and thrown bridge errors fail closed', () => {
  for (const raw of [
    encoded(false, 'unexpected', ''), 'not-json', '{}',
    encoded(true, '', '0'), encoded(true, '', '01'), encoded(true, '', '9223372036854775808'),
    encoded(true, '', '2', true), encoded(true, '', '2', false, 'private source bytes'),
    encoded(true, 'conflict', '2'), new Error('synthetic bridge error'),
  ]) {
    const result = fixture(raw).claim(...args)
    assert.equal(result.ok, false)
    assert.equal(result.errorCode, 'bridge_failure')
    assert.equal(result.found, false)
    assert.equal(result.value, '')
    assert.equal(result.revision, '')
  }
})
