import fs from 'node:fs'
import vm from 'node:vm'

const artifact = process.env.NERAE_APP_SERVICE
if (!artifact) throw new Error('NERAE_APP_SERVICE is required')
const source = fs.readFileSync(artifact, 'utf8')

const genericFunction = () => () => ({ mount() {} })
const makeApp = () => ({ _component: {}, provide() {}, mount() {} })
const vue = new Proxy({}, { get: (_target, property) => {
  if (property === 'createApp' || property === 'createVueApp') return makeApp
  return genericFunction()
} })
const uni = {
  restoreGlobal() {},
  requireGlobal: () => ({}),
}
const context = {
  console,
  uni,
  Vue: vue,
  Vuex: {},
  Pinia: {},
  weex: {},
  plus: {},
  URL,
  setTimeout,
  clearTimeout,
  setInterval,
  clearInterval,
  __definePage() {},
  __uniConfig: { styles: [] },
  document: { querySelector: () => ({}) },
}
vm.runInNewContext(source, context, { timeout: 5_000, filename: artifact })

const probe = context.__p02_11_probe
if (!probe || typeof probe !== 'object') throw new Error('App entry did not publish its temporary transport probe')
for (const key of ['factory', 'authTransport', 'recordingRequest', 'nativeRequest', 'nativeCancel']) {
  if (typeof probe[key] !== 'function') throw new Error(`App artifact did not retain callable ${key}`)
}
if (probe.factory('https://api.example.test/v1')?.authTransport === undefined) {
  throw new Error('Retained factory did not construct the adapter ports')
}
for (const [name, promise] of [
  ['authTransport', probe.authTransport({
    url: 'https://api.example.test/v1/auth/email-challenges', method: 'POST', headers: {}, body: '{}',
  })],
  ['recordingRequest', probe.recordingRequest({
    url: 'https://api.example.test/v1/me/sessions?page=1&pageSize=20', method: 'GET', header: {},
  })],
]) {
  try {
    await promise
    throw new Error(`${name} unexpectedly reported transport success`)
  } catch (error) {
    if (!['AuthApiError', 'RecordingApiError'].includes(error?.name) || error?.code !== 'unavailable') {
      throw new Error(`${name} did not preserve the emitted unsupported-platform result`)
    }
  }
}
console.log('PASS: executed App JS resource called both adapter ports and observed the emitted unsupported-platform bridge result; injected VM globals and this fallback do not prove iOS/Android UTS linking or device networking.')
