import assert from 'node:assert/strict'
import { test } from 'node:test'
import { RecordingApi, RecordingApiError, createUniRequestPort } from './recordingApi.ts'

const credential = { subjectId: '101', generation: 'login-1', accessToken: 'real-session-token' }
const auth = { current: () => credential, isCurrent: (id, generation) => id === '101' && generation === 'login-1' }
const set = {
  recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲',
  resolvedExerciseName: '深蹲', setIndex: 1, kind: 'weight', weightKg: 50, reps: 8,
}
const detail = overrides => ({
  id: '42', clientEventId: 'event-0001', date: '2026-10-29', timeZone: 'Asia/Shanghai', status: 'in_progress',
  source: 'free', version: 3, createdAt: '2026-10-29T08:00:00Z',
  updatedAt: '2026-10-29T08:00:00Z', recordedAt: null, sets: [], exerciseEntryKeys: [], ...overrides,
})
const listedSession = overrides => ({
  id: '42', date: '2026-10-29', timeZone: 'Asia/Shanghai', status: 'in_progress', source: 'free', version: 3,
  createdAt: '2026-10-29T08:00:00Z', updatedAt: '2026-10-29T08:00:00Z', recordedAt: null,
  ...overrides,
})
const sessionPage = (items, overrides) => ({ page: 1, pageSize: 20, total: items.length, items, ...overrides })
const apiError = code => error => error instanceof RecordingApiError && error.code === code
const defaultWriteExpiry = '2001-01-01T00:00:00.123456789Z'
const makeApi = (base, leaseAuth, request) => new RecordingApi(base, leaseAuth, async input => {
  const response = await request(input)
  return input.method === 'POST' && response && response.statusCode >= 200 && response.statusCode < 300 &&
    response.headers === undefined
    ? { ...response, headers: { 'Idempotency-Key-Expires-At': defaultWriteExpiry } }
    : response
}, { subjectId: '101', generation: 'login-1' })

test('free-session creation sends the actual input with authentication and a caller-owned idempotency key', async () => {
  const requests = []
  const api = makeApi('https://api.example.org/v1/', auth, async request => {
    requests.push(request)
    return { statusCode: 201, data: detail() }
  })
  const input = { date: '2026-10-29', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' }
  const created = await api.createFreeSession(input, 'create-0001')
  assert.equal(created.detail.id, '42')
  assert.equal(created.idempotencyKeyExpiresAt, defaultWriteExpiry)
  assert.equal(created.detail.clientEventId, input.clientEventId)
  assert.equal(requests[0].url, 'https://api.example.org/v1/me/sessions')
  assert.equal(requests[0].header.Authorization, 'Bearer real-session-token')
  assert.equal(requests[0].header['Idempotency-Key'], 'create-0001')
  assert.deepEqual(JSON.parse(requests[0].data), { source: 'free', ...input })
  assert.ok(!('subjectId' in JSON.parse(requests[0].data)))
})

test('saving and rereading use the same resource, version, keys and caller-owned replay token', async () => {
  const requests = []
  const api = makeApi('https://api.example.org/v1', auth, async request => {
    requests.push(request)
    return { statusCode: 200, data: request.method === 'GET' ? detail() : detail({ version: 4, sets: [set], exerciseEntryKeys: ['entry-0001'] }) }
  })
  const baseline = await api.readSession('42')
  const { resolvedExerciseName, ...fields } = set
  const write = { ...fields, completed: true }
  await api.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', baseline)
  await api.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', baseline)
  await api.readSession('42')
  assert.equal(requests[1].url, 'https://api.example.org/v1/me/sessions/42/sets')
  assert.equal(requests[3].url, 'https://api.example.org/v1/me/sessions/42')
  assert.equal(requests[1].data, requests[2].data)
  assert.equal(requests[1].header['Idempotency-Key'], requests[2].header['Idempotency-Key'])
  assert.deepEqual(JSON.parse(requests[1].data), { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'], sets: [write] })
  assert.equal(requests[3].header['Idempotency-Key'], undefined)
})

test('version conflict and network errors are explicit and do not trigger a blind retry', async () => {
  let writes = 0
  const conflict = makeApi('https://api.example.org', auth, async request => {
    if (request.method === 'GET') return { statusCode: 200, data: detail() }
    writes++
    return { statusCode: 412, data: { message: 'server-private-details' } }
  })
  const baseline = await conflict.readSession('42')
  await assert.rejects(conflict.saveSets('42', 3, [{ ...set, completed: true }], ['entry-0001'], 'update-0001', baseline), apiError('version_conflict'))
  assert.equal(writes, 1)
  const offline = makeApi('https://api.example.org', auth, async () => { throw new Error('token in low-level error') })
  await assert.rejects(offline.readSession('42'), error => apiError('network')(error) && !error.message.includes('token'))
})

test('finish preserves explicit nulls but omits unrelated fields and verifies its full result', async () => {
  let captured
  const initial = detail({ sets: [set], exerciseEntryKeys: ['entry-0001'], durationMin: 30 })
  const completed = detail({ version: 4, status: 'completed', recordedAt: '2026-10-29T08:00:01Z',
    sets: [set], exerciseEntryKeys: ['entry-0001'], durationMin: null, note: '完成' })
  const api = makeApi('https://api.example.org/v1', auth, async request => {
    if (request.method === 'GET') return { statusCode: 200, data: initial }
    captured = request
    return { statusCode: 200, data: completed }
  })
  const baseline = await api.readSession('42')
  assert.equal((await api.finishSession('42', { expectedVersion: 3, durationMin: null, note: '完成', ignored: 'do not send' }, 'finish-0001', baseline)).detail.status, 'completed')
  assert.deepEqual(JSON.parse(captured.data), { expectedVersion: 3, durationMin: null, note: '完成' })
})

test('invalid or duplicate sets are rejected before network access', async () => {
  let calls = 0
  const api = makeApi('https://api.example.org', auth, async () => { calls++; return { statusCode: 200, data: detail() } })
  const baseline = await api.readSession('42')
  const write = { ...set, completed: true }
  assert.throws(() => api.saveSets('42', 3, [{ ...write, reps: 0 }], ['entry-0001'], 'update-0001', baseline), apiError('invalid_request'))
  assert.throws(() => api.saveSets('42', 3, [write, write], ['entry-0001'], 'update-0001', baseline), apiError('invalid_request'))
  assert.equal(calls, 1)
})

test('a response from the old session cannot be used after switching account or logging in again', async () => {
  let generation = 'login-1'
  let release
  const lease = {
    current: () => ({ ...credential, generation }),
    isCurrent: (id, captured) => id === '101' && captured === generation,
  }
  const api = makeApi('https://api.example.org', lease, () => new Promise(resolve => { release = resolve }))
  const pending = api.readSession('42')
  generation = 'login-2'
  release({ statusCode: 200, data: detail() })
  await assert.rejects(pending, apiError('session_changed'))
})

test('mutating the credential object during a request does not make the old response current', async () => {
  let release
  const mutable = { ...credential }
  const lease = {
    current: () => mutable,
    isCurrent: (id, generation) => id === mutable.subjectId && generation === mutable.generation,
  }
  const api = makeApi('https://api.example.org', lease, () => new Promise(resolve => { release = resolve }))
  const pending = api.readSession('42')
  mutable.generation = 'login-2'
  release({ statusCode: 200, data: detail() })
  await assert.rejects(pending, apiError('session_changed'))
})

test('missing authentication and insecure or malformed configuration fail closed', async () => {
  for (const base of ['http://api.example.org', 'https://user:pass@example.org', 'https://example.org/?token=secret']) {
    assert.throws(() => makeApi(base, auth, async () => ({})), /服务地址/)
  }
  let called = false
  const api = makeApi('https://api.example.org', { current: () => null, isCurrent: () => false }, async () => { called = true })
  await assert.rejects(api.readSession('42'), apiError('unauthenticated'))
  assert.equal(called, false)
  assert.throws(() => api.readSession('../42'), apiError('invalid_request'))
})

test('session list sends validated date filters and only returns a vetted summary before reading by ID', async () => {
  const requests = []
  const api = makeApi('https://api.example.org/v1', auth, async request => {
    requests.push(request)
    if (requests.length === 1) return { statusCode: 200, data: sessionPage([
      listedSession({ extraSecret: 'must not escape', planId: 'opaque-plan' }),
    ], { total: 5 }) }
    return { statusCode: 200, data: detail() }
  })
  const page = await api.listSessions({ page: 1, pageSize: 20, from: '2026-10-01', to: '2026-10-31' })
  assert.equal(requests[0].url, 'https://api.example.org/v1/me/sessions?page=1&pageSize=20&from=2026-10-01&to=2026-10-31')
  assert.equal(requests[0].method, 'GET')
  assert.equal(requests[0].header.Authorization, 'Bearer real-session-token')
  assert.deepEqual(page.lease, { subjectId: '101', generation: 'login-1' })
  assert.ok(!('accessToken' in page.lease))
  assert.deepEqual(Object.keys(page.items[0]).sort(), [
    'createdAt', 'date', 'id', 'recordedAt', 'source', 'status', 'timeZone', 'updatedAt', 'version',
  ])
  assert.equal(page.total, 5)
  assert.equal(page.items[0].source, 'free')
  const loaded = await api.readSession(page.items[0].id)
  assert.equal(requests[1].url, 'https://api.example.org/v1/me/sessions/42')
  assert.ok(!('accessToken' in loaded.lease))
})

test('session list accepts an empty page and completed service records only with recordedAt', async () => {
  const valid = makeApi('https://api.example.org', auth, async () => ({
    statusCode: 200,
    data: sessionPage([listedSession({ status: 'completed', source: 'plan', recordedAt: '2026-10-29T08:05:00Z' })]),
  }))
  assert.equal((await valid.listSessions({ page: 1, pageSize: 20 })).items[0].status, 'completed')
  const empty = makeApi('https://api.example.org', auth, async () => ({ statusCode: 200, data: sessionPage([], { total: 0 }) }))
  assert.deepEqual((await empty.listSessions({ page: 1, pageSize: 20 })).items, [])
})

test('session list rejects invalid input, duplicate IDs, malformed records and mismatched pagination', async () => {
  let calls = 0
  const api = makeApi('https://api.example.org', auth, async () => { calls++; return { statusCode: 200, data: sessionPage([]) } })
  for (const input of [
    { page: 0, pageSize: 20 }, { page: Number.MAX_SAFE_INTEGER + 1, pageSize: 20 },
    { page: 1, pageSize: 0 }, { page: 1, pageSize: 51 }, { page: 1, pageSize: 20, from: '2026-02-30' },
    { page: 1, pageSize: 20, from: '2026-11-01', to: '2026-10-01' },
  ]) await assert.rejects(api.listSessions(input), apiError('invalid_request'))
  assert.equal(calls, 0)

  const malformedPages = [
    sessionPage([listedSession(), listedSession()]),
    sessionPage([], { page: 2 }), sessionPage([], { pageSize: 19 }), sessionPage([], { total: -1 }),
    sessionPage([listedSession(), listedSession({ id: '43' })], { page: 2, total: 21 }),
    sessionPage([], { total: Number.MAX_SAFE_INTEGER + 1 }), sessionPage([listedSession()], { pageSize: 0 }),
    sessionPage([listedSession({ date: '2026-02-30' })]),
    sessionPage([listedSession({ timeZone: 'No/Such_Zone' })]),
    sessionPage([listedSession({ status: 'running' })]), sessionPage([listedSession({ source: 'template' })]),
    sessionPage([listedSession({ version: 0 })]), sessionPage([listedSession({ createdAt: 'yesterday' })]),
    sessionPage([listedSession({ updatedAt: '2026-10-29T24:00:00Z' })]),
    sessionPage([listedSession({ recordedAt: 'not-time' })]),
    sessionPage([listedSession({ status: 'completed', recordedAt: null })]),
  ]
  for (const data of malformedPages) {
    const candidate = makeApi('https://api.example.org', auth, async () => ({ statusCode: 200, data }))
    await assert.rejects(candidate.listSessions({ page: 1, pageSize: 20 }), apiError('invalid_response'))
  }
})

test('session list snapshots caller parameters before the network await and caps items to the page remainder', async () => {
  let release
  let requestSeen
  const api = makeApi('https://api.example.org', auth, request => {
    requestSeen = request
    return new Promise(resolve => { release = resolve })
  })
  const input = { page: 2, pageSize: 20, from: '2026-10-01' }
  const pending = api.listSessions(input)
  input.page = 1
  input.pageSize = 50
  input.from = '2026-01-01'
  release({ statusCode: 200, data: sessionPage([listedSession()], { page: 2, pageSize: 20, total: 21 }) })
  const result = await pending
  assert.equal(requestSeen.url, 'https://api.example.org/me/sessions?page=2&pageSize=20&from=2026-10-01')
  assert.equal(result.page, 2)
  assert.equal(result.items.length, 1)

  const hugePage = makeApi('https://api.example.org', auth, async () => ({
    statusCode: 200,
    data: sessionPage([], { page: Number.MAX_SAFE_INTEGER, pageSize: 50, total: Number.MAX_SAFE_INTEGER }),
  }))
  assert.deepEqual((await hugePage.listSessions({ page: Number.MAX_SAFE_INTEGER, pageSize: 50 })).items, [])
  const impossibleItem = makeApi('https://api.example.org', auth, async () => ({
    statusCode: 200,
    data: sessionPage([listedSession()], { page: Number.MAX_SAFE_INTEGER, pageSize: 50, total: Number.MAX_SAFE_INTEGER }),
  }))
  await assert.rejects(impossibleItem.listSessions({ page: Number.MAX_SAFE_INTEGER, pageSize: 50 }), apiError('invalid_response'))
})

test('session list rejects an account or generation change while the request is in flight', async () => {
  let generation = 'login-1'
  let release
  const leaseAuth = {
    current: () => ({ ...credential, generation }),
    isCurrent: (id, captured) => id === '101' && captured === generation,
  }
  const api = makeApi('https://api.example.org', leaseAuth, () => new Promise(resolve => { release = resolve }))
  const pending = api.listSessions({ page: 1, pageSize: 20 })
  generation = 'login-2'
  release({ statusCode: 200, data: sessionPage([listedSession()]) })
  await assert.rejects(pending, apiError('session_changed'))

  let activeSubject = '101'
  let finish
  const switchingAccount = {
    current: () => ({ subjectId: activeSubject, generation: 'login-1', accessToken: 'real-session-token' }),
    isCurrent: (id, generation) => id === activeSubject && generation === 'login-1',
  }
  const otherAccountApi = makeApi('https://api.example.org', switchingAccount, () => new Promise(resolve => { finish = resolve }))
  const accountPending = otherAccountApi.listSessions({ page: 1, pageSize: 20 })
  activeSubject = '202'
  finish({ statusCode: 200, data: sessionPage([listedSession()]) })
  await assert.rejects(accountPending, apiError('session_changed'))
})

test('session list does not release a response after its token disappears', async () => {
  let token = 'real-session-token'
  let finish
  const expiringAuth = {
    current: () => token ? { ...credential, accessToken: token } : null,
    isCurrent: (id, generation) => id === '101' && generation === 'login-1',
  }
  const api = makeApi('https://api.example.org', expiringAuth, () => new Promise(resolve => { finish = resolve }))
  const pending = api.listSessions({ page: 1, pageSize: 20 })
  token = ''
  finish({ statusCode: 200, data: sessionPage([listedSession()]) })
  await assert.rejects(pending, apiError('session_changed'))
})

test('detail and list reject responses after a token is replaced within the same subject and generation', async () => {
  for (const operation of ['list', 'detail']) {
    let token = 'original-session-token'
    let release
    const stableIdentity = {
      current: () => ({ ...credential, accessToken: token }),
      isCurrent: (id, generation) => id === '101' && generation === 'login-1',
    }
    const api = makeApi('https://api.example.org', stableIdentity, () => new Promise(resolve => { release = resolve }))
    const pending = operation === 'list'
      ? api.listSessions({ page: 1, pageSize: 20 })
      : api.readSession('42')
    token = 'replacement-session-token'
    release({ statusCode: 200, data: operation === 'list' ? sessionPage([listedSession()]) : detail() })
    await assert.rejects(pending, apiError('session_changed'))
  }
})

test('detail and list recheck the captured token after parsing the response', async () => {
  for (const operation of ['list', 'detail']) {
    let token = 'original-session-token'
    const stableIdentity = {
      current: () => ({ ...credential, accessToken: token }),
      isCurrent: (id, generation) => id === '101' && generation === 'login-1',
    }
    const parsedRecord = new Proxy(listedSession(), {
      get(target, property, receiver) {
        if (property === 'id') token = 'replacement-session-token'
        return Reflect.get(target, property, receiver)
      },
    })
    const parsedDetail = new Proxy(detail(), {
      get(target, property, receiver) {
        if (property === 'id') token = 'replacement-session-token'
        return Reflect.get(target, property, receiver)
      },
    })
    const api = makeApi('https://api.example.org', stableIdentity, async () => ({
      statusCode: 200,
      data: operation === 'list' ? sessionPage([parsedRecord]) : parsedDetail,
    }))
    const pending = operation === 'list'
      ? api.listSessions({ page: 1, pageSize: 20 })
      : api.readSession('42')
    await assert.rejects(pending, apiError('session_changed'))
  }
})

test('session list maps unauthenticated and transport failures without exposing credentials', async () => {
  const unauthorized = makeApi('https://api.example.org', auth, async () => ({ statusCode: 401, data: { token: 'private' } }))
  await assert.rejects(unauthorized.listSessions({ page: 1, pageSize: 20 }), error => apiError('unauthenticated')(error) && !error.message.includes('private'))
  const offline = makeApi('https://api.example.org', auth, async () => { throw new Error('real-session-token') })
  await assert.rejects(offline.listSessions({ page: 1, pageSize: 20 }), error => apiError('network')(error) && !error.message.includes('token'))
})

test('malformed, incomplete or mismatched server records never become an acknowledgement', async () => {
  const invalid = [
    detail({ id: '43' }),
    detail({ version: 0 }),
    detail({ date: '2026-02-30' }),
    detail({ timeZone: 'No/Such_Zone' }),
    detail({ createdAt: '2026-02-30T08:00:00Z' }),
    detail({ updatedAt: '2026-10-29T24:00:00Z' }),
    detail({ status: 'completed', sets: [] }),
    detail({ sets: [{ ...set, resolvedExerciseName: '' }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [{ ...set, resolvedExerciseName: '不同名称' }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [{ ...set, reps: 0 }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [{ ...set, weightKg: '50' }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [set, set], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [{ ...set, exerciseId: '5' }], exerciseEntryKeys: ['entry-0001'] }),
  ]
  for (const data of invalid) {
    const api = makeApi('https://api.example.org', auth, async () => ({ statusCode: 200, data }))
    await assert.rejects(api.readSession('42'), apiError('invalid_response'))
  }
})

test('the uni-app adapter forwards request completion and rejects a transport failure', async () => {
  const previous = globalThis.uni
  try {
    globalThis.uni = { request(options) { options.success({ statusCode: 200, data: { ok: true } }) } }
    const request = createUniRequestPort()
    assert.deepEqual(await request({ url: 'https://example.org', method: 'GET', header: {} }), { statusCode: 200, data: { ok: true } })
    globalThis.uni = { request(options) { options.fail({ errMsg: 'offline' }) } }
    await assert.rejects(request({ url: 'https://example.org', method: 'GET', header: {} }), /network unavailable/)
  } finally { globalThis.uni = previous }
})

test('the uni-app recording adapter preserves only unambiguous allowed response headers', async () => {
  const previous = globalThis.uni
  try {
    const request = createUniRequestPort()
    globalThis.uni = { request(options) { options.success({ statusCode: 201, data: { ok: true }, header: {
      'Cache-Control': 'no-store', 'Idempotency-Key-Expires-At': '2026-11-01T00:00:00Z',
      'X-Request-Id': 'abc', 'Set-Cookie': 'not-for-the-app',
    } }) } }
    const response = await request({ url: 'https://example.org', method: 'POST', header: {} })
    assert.deepEqual(response.headers, { 'cache-control': 'no-store',
      'idempotency-key-expires-at': '2026-11-01T00:00:00Z', 'x-request-id': 'abc' })
    assert.equal(Object.isFrozen(response.headers), true)
    for (const header of [
      { 'X-Request-ID': 'abc', 'x-request-id': 'def' },
      { 'Cache-Control': ['no-store'] },
      { 'Idempotency-Key-Expires-At': 'bad\r\nvalue' },
    ]) {
      globalThis.uni = { request(options) { options.success({ statusCode: 200, data: {}, header }) } }
      await assert.rejects(request({ url: 'https://example.org', method: 'GET', header: {} }), apiError('invalid_response'))
    }
  } finally { globalThis.uni = previous }
})

test('write expiry is preserved exactly for create, set and finish, while GET never exposes a write receipt', async () => {
  const expiry = '2001-02-28T23:59:59.120034500+00:00'
  const initial = detail()
  const api = makeApi('https://api.example.org/v1', auth, async request => {
    if (request.method === 'GET') return { statusCode: 200, data: initial,
      headers: { 'Idempotency-Key-Expires-At': '2001-03-01T00:00:00Z', 'Set-Cookie': 'secret=not-exposed' } }
    if (request.url.endsWith('/me/sessions')) return { statusCode: 201, data: detail(), headers: { 'idempotency-key-expires-at': expiry } }
    if (request.url.endsWith('/sets')) return { statusCode: 200,
      data: detail({ version: 4, sets: [set], exerciseEntryKeys: ['entry-0001'] }), headers: { 'idempotency-key-expires-at': expiry } }
    return { statusCode: 200,
      data: detail({ version: 5, status: 'completed', recordedAt: '2026-10-29T08:00:01Z', sets: [set], exerciseEntryKeys: ['entry-0001'] }),
      headers: { 'idempotency-key-expires-at': expiry } }
  })

  const created = await api.createFreeSession({ date: '2026-10-29', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' }, 'create-0001')
  assert.equal(created.idempotencyKeyExpiresAt, expiry)
  const read = await api.readSession('42')
  assert.equal(Object.hasOwn(read, 'idempotencyKeyExpiresAt'), false)
  assert.deepEqual(Object.keys(read).sort(), ['detail', 'lease'])
  const saved = await api.saveBatch('42', { expectedVersion: 3, sets: [{ ...set, completed: true }], exerciseEntryKeys: ['entry-0001'] },
    'sets-0001', read)
  assert.equal(saved.idempotencyKeyExpiresAt, expiry)
  const completed = await api.finishSession('42', { expectedVersion: saved.detail.version }, 'finish-0001', saved)
  assert.equal(completed.idempotencyKeyExpiresAt, expiry)
  for (const result of [created, saved, completed]) {
    assert.deepEqual(Object.keys(result).sort(), ['detail', 'idempotencyKeyExpiresAt', 'lease'])
    assert.equal(JSON.stringify(result).includes('Set-Cookie'), false)
    assert.equal(JSON.stringify(result).includes('Authorization'), false)
  }
})

test('a successful write without one valid unambiguous expiry requires reconciliation without leaking headers', async () => {
  const malformed = [
    undefined,
    {},
    { 'Idempotency-Key-Expires-At': 'not-a-date', 'X-Request-ID': 'private-request-id' },
    { 'Idempotency-Key-Expires-At': '2026-02-30T00:00:00Z' },
    { 'Idempotency-Key-Expires-At': '2026-10-31T00:00:00+08:00' },
    { 'Idempotency-Key-Expires-At': '2026-10-31T00:00:00.1234567890Z' },
    { 'Idempotency-Key-Expires-At': '2026-10-31T00:00:00Z', 'idempotency-key-expires-at': '2026-11-01T00:00:00Z' },
  ]
  for (const headers of malformed) {
    const api = new RecordingApi('https://api.example.org', auth, async () => ({ statusCode: 201, data: detail(), headers }),
      { subjectId: '101', generation: 'login-1' })
    await assert.rejects(api.createFreeSession({ date: '2026-10-29', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' },
      'create-0001'), error => apiError('reconcile_required')(error) && error.message === 'reconcile_required' &&
        !JSON.stringify(error).includes('private-request-id'))
  }
})

test('the real uni.request adapter turns a duplicate successful-write expiry header into reconciliation', async () => {
  const previous = globalThis.uni
  try {
    globalThis.uni = { request(options) { options.success({ statusCode: 201, data: detail(), header: {
      'Idempotency-Key-Expires-At': '2026-11-01T00:00:00Z',
      'idempotency-key-expires-at': '2026-11-02T00:00:00Z',
      'Set-Cookie': 'secret=hidden',
    } }) } }
    const api = new RecordingApi('https://api.example.org', auth, createUniRequestPort(),
      { subjectId: '101', generation: 'login-1' })
    await assert.rejects(api.createFreeSession({ date: '2026-10-29', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' },
      'create-0001'), apiError('reconcile_required'))
  } finally { globalThis.uni = previous }
})

test('a native-style invalid response after a write is conservatively reconciled', async () => {
  const api = new RecordingApi('https://api.example.org', auth, async () => {
    throw new RecordingApiError('invalid_response')
  }, { subjectId: '101', generation: 'login-1' })
  await assert.rejects(api.createFreeSession({ date: '2026-10-29', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' },
    'create-0001'), apiError('reconcile_required'))
})

test('write response after an account or session change cannot release expiry metadata', async () => {
  let generation = 'login-1'
  let release
  const switchingAuth = {
    current: () => ({ ...credential, generation }),
    isCurrent: (id, capturedGeneration) => id === '101' && capturedGeneration === generation,
  }
  const api = makeApi('https://api.example.org', switchingAuth, () => new Promise(resolve => { release = resolve }))
  const pending = api.createFreeSession({ date: '2026-10-29', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' }, 'create-0001')
  generation = 'login-2'
  release({ statusCode: 201, data: detail(), headers: { 'Idempotency-Key-Expires-At': defaultWriteExpiry, 'Set-Cookie': 'secret=hidden' } })
  await assert.rejects(pending, apiError('session_changed'))
})


test('ordered details accept gaps but reject missing, duplicate, mismatched or unsorted snapshots', async () => {
  const secondEntry = { ...set, recordKey: 'record-003', exerciseEntryKey: 'entry-0002', exerciseName: '划船', resolvedExerciseName: '划船' }
  const laterSet = { ...set, recordKey: 'record-002', setIndex: 4 }
  const valid = detail({ sets: [set, laterSet, secondEntry], exerciseEntryKeys: ['entry-0001', 'entry-0002'] })
  const apiFor = data => makeApi('https://api.example.org', auth, async () => ({ statusCode: 200, data }))
  assert.deepEqual((await apiFor(valid).readSession('42')).detail.exerciseEntryKeys, ['entry-0001', 'entry-0002'])
  const invalid = [
    { ...valid, exerciseEntryKeys: undefined },
    { ...valid, exerciseEntryKeys: null },
    { ...valid, exerciseEntryKeys: ['entry-0001', 'entry-0001'] },
    { ...valid, exerciseEntryKeys: ['entry-0001'] },
    { ...valid, exerciseEntryKeys: ['entry-0001', 'entry-0002', 'entry-0003'] },
    { ...valid, exerciseEntryKeys: ['Entry-0001', 'entry-0002'] },
    { ...valid, exerciseEntryKeys: ['entry-0002', 'entry-0001'] },
    { ...valid, sets: [laterSet, set, secondEntry] },
    { ...valid, sets: [set, { ...laterSet, setIndex: 1 }, secondEntry] },
    { ...valid, sets: [set, { ...laterSet, exerciseName: '其他动作' }, secondEntry] },
    detail({ sets: [], exerciseEntryKeys: ['entry-0001'] }),
  ]
  for (const data of invalid) await assert.rejects(apiFor(data).readSession('42'), apiError('invalid_response'))
})

test('batch sends an explicit projected order for a reorder, delete and restore', async () => {
  const requests = []
  const secondEntry = { ...set, recordKey: 'record-002', exerciseEntryKey: 'entry-0002', exerciseName: '划船', resolvedExerciseName: '划船' }
  const api = makeApi('https://api.example.org', auth, async request => {
    requests.push(request)
    if (request.method === 'GET') return { statusCode: 200, data: detail({ sets: [set, secondEntry], exerciseEntryKeys: ['entry-0001', 'entry-0002'] }) }
    const body = JSON.parse(request.data)
    if (body.deleteRecordKeys) return { statusCode: 200, data: detail({ version: 5, sets: [secondEntry], exerciseEntryKeys: ['entry-0002'] }) }
    if (body.restoreRecordKeys) return { statusCode: 200, data: detail({ version: 6, sets: [secondEntry, set], exerciseEntryKeys: ['entry-0002', 'entry-0001'] }) }
    return { statusCode: 200, data: detail({ version: 4, sets: [secondEntry, set], exerciseEntryKeys: ['entry-0002', 'entry-0001'] }) }
  })
  const { resolvedExerciseName, ...fields } = set
  const write = { ...fields, completed: true }
  const baseline = await api.readSession('42')
  const reordered = await api.saveBatch('42', { expectedVersion: 3, exerciseEntryKeys: ['entry-0002', 'entry-0001'] }, 'reorder-001', baseline)
  const removed = await api.saveBatch('42', { expectedVersion: 4, exerciseEntryKeys: ['entry-0002'], deleteRecordKeys: ['record-001'] }, 'delete-0001', reordered)
  await api.saveBatch('42', { expectedVersion: 5, exerciseEntryKeys: ['entry-0002', 'entry-0001'], sets: [write], restoreRecordKeys: ['record-001'] }, 'restore-001', removed)
  assert.deepEqual(JSON.parse(requests[1].data), { expectedVersion: 3, exerciseEntryKeys: ['entry-0002', 'entry-0001'] })
  assert.deepEqual(JSON.parse(requests[2].data), { expectedVersion: 4, exerciseEntryKeys: ['entry-0002'], deleteRecordKeys: ['record-001'] })
  assert.deepEqual(JSON.parse(requests[3].data), { expectedVersion: 5, exerciseEntryKeys: ['entry-0002', 'entry-0001'], sets: [write], restoreRecordKeys: ['record-001'] })
})

test('invalid order and overlapping batches fail before a network request', async () => {
  let requests = 0
  const api = makeApi('https://api.example.org', auth, async () => { requests++; return { statusCode: 200, data: detail() } })
  const baseline = await api.readSession('42')
  const write = { ...set, completed: true }
  const base = { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'] }
  const bad = [
    { ...base, exerciseEntryKeys: undefined },
    { ...base, exerciseEntryKeys: ['entry-0001', 'entry-0001'] },
    { ...base, exerciseEntryKeys: ['entry-0002'], sets: [write] },
    { ...base, sets: [] },
    { ...base, deleteRecordKeys: [] },
    { ...base, restoreRecordKeys: ['record-001'] },
    { ...base, sets: [write], deleteRecordKeys: ['record-001'] },
    { ...base, sets: [write], restoreRecordKeys: ['record-002'] },
    { ...base, sets: [write, { ...write, recordKey: 'record-002' }] },
    { ...base, sets: [write, { ...write, recordKey: 'record-002', setIndex: 2, exerciseName: '不同动作' }] },
    { ...base, deleteRecordKeys: ['record-001', 'record-001'] },
    { ...base, sets: Array.from({ length: 200 }, (_, i) => ({ ...write, recordKey: `record-${String(i).padStart(3, '0')}`, setIndex: i + 1 })), deleteRecordKeys: ['record-999'] },
    { ...base, exerciseEntryKeys: Array.from({ length: 2001 }, (_, i) => `entry-${String(i).padStart(4, '0')}`) },
  ]
  for (const input of bad) assert.throws(() => api.saveBatch('42', input, 'update-0001', baseline), apiError('invalid_request'))
  assert.equal(requests, 1)
})


test('a 200 response that does not acknowledge the requested order or group is not treated as saved', async () => {
  const write = { ...set, completed: true }
  const initial = detail({ sets: [set], exerciseEntryKeys: ['entry-0001'] })
  const wrong = [
    detail(),
    detail({ sets: [{ ...set, reps: 7 }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [set], exerciseEntryKeys: ['entry-0001'], version: 2 }),
  ]
  for (const data of wrong) {
    const api = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? initial : data }))
    const baseline = await api.readSession('42')
    await assert.rejects(api.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', baseline), apiError('invalid_response'))
  }
  const undeleted = makeApi('https://api.example.org', auth, async () => ({ statusCode: 200, data: initial }))
  const baseline = await undeleted.readSession('42')
  await assert.rejects(undeleted.saveBatch('42', { expectedVersion: 3, exerciseEntryKeys: [], deleteRecordKeys: ['record-001'] }, 'delete-0001', baseline), apiError('invalid_response'))
})

test('in-flight batch acknowledgement uses the original immutable request values', async () => {
  let release
  let sent
  const api = makeApi('https://api.example.org', auth, request => {
    if (request.method === 'GET') return Promise.resolve({ statusCode: 200, data: detail() })
    sent = JSON.parse(request.data)
    return new Promise(resolve => { release = resolve })
  })
  const baseline = await api.readSession('42')
  const input = { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'], sets: [{ ...set, completed: true }] }
  const pending = api.saveBatch('42', input, 'update-0001', baseline)
  input.expectedVersion = 99
  input.exerciseEntryKeys[0] = 'entry-0002'
  input.sets[0].reps = 1
  release({ statusCode: 200, data: detail({ version: 4, sets: [set], exerciseEntryKeys: ['entry-0001'] }) })
  assert.equal((await pending).detail.version, 4)
  assert.equal(sent.expectedVersion, 3)
  assert.deepEqual(sent.exerciseEntryKeys, ['entry-0001'])
  assert.equal(sent.sets[0].reps, 8)
})

test('a recording API bound to one login refuses a different login before network access', async () => {
  let generation = 'login-1'
  let requests = 0
  const lease = { current: () => ({ ...credential, generation }), isCurrent: (id, captured) => id === '101' && captured === generation }
  const api = makeApi('https://api.example.org', lease, async () => { requests++; return { statusCode: 200, data: detail() } })
  generation = 'login-2'
  await assert.rejects(api.readSession('42'), apiError('session_changed'))
  assert.equal(requests, 0)
})


test('a write response must preserve every unrelated saved group from its baseline', async () => {
  const second = { ...set, recordKey: 'record-002', setIndex: 2, reps: 6 }
  const original = detail({ sets: [set, second], exerciseEntryKeys: ['entry-0001'] })
  const write = { ...second, reps: 7, completed: true }
  const changed = { ...second, reps: 7 }
  const wrong = [
    detail({ version: 4, sets: [changed], exerciseEntryKeys: ['entry-0001'] }),
    detail({ version: 4, sets: [{ ...set, reps: 9 }, changed], exerciseEntryKeys: ['entry-0001'] }),
    detail({ version: 4, sets: [{ ...set, resolvedExerciseName: '其他名称' }, changed], exerciseEntryKeys: ['entry-0001'] }),
  ]
  for (const response of wrong) {
    const api = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? original : response }))
    const baseline = await api.readSession('42')
    await assert.rejects(api.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', baseline), apiError('invalid_response'))
  }
})

test('write baseline must be issued by the same adapter, session and version', async () => {
  let calls = 0
  const transport = async () => { calls++; return { statusCode: 200, data: detail() } }
  const first = makeApi('https://api.example.org', auth, transport)
  const second = makeApi('https://api.example.org', auth, transport)
  const baseline = await first.readSession('42')
  const write = { ...set, completed: true }
  assert.throws(() => second.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', baseline), apiError('invalid_request'))
  assert.throws(() => first.saveSets('42', 4, [write], ['entry-0001'], 'update-0001', baseline), apiError('invalid_request'))
  assert.throws(() => first.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', { ...baseline }), apiError('invalid_request'))
  assert.equal(calls, 1)
})


test('existing groups can be changed without losing untouched groups; versions reflect actual changes', async () => {
  const second = { ...set, recordKey: 'record-002', setIndex: 2, reps: 6 }
  const original = detail({ sets: [set, second], exerciseEntryKeys: ['entry-0001'] })
  const write = { ...second, reps: 7, completed: true }
  const changed = detail({ version: 4, sets: [set, { ...second, reps: 7 }], exerciseEntryKeys: ['entry-0001'] })
  const success = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? original : changed }))
  const baseline = await success.readSession('42')
  const updated = await success.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', baseline)
  assert.equal(updated.detail.version, 4)
  assert.deepEqual(updated.detail.sets.map(group => group.reps), [8, 7])
  const invalid = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? original : { ...changed, version: 3 } }))
  const otherBaseline = await invalid.readSession('42')
  await assert.rejects(invalid.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', otherBaseline), apiError('invalid_response'))
  const noop = makeApi('https://api.example.org', auth, async () => ({ statusCode: 200, data: original }))
  const noopBaseline = await noop.readSession('42')
  assert.equal((await noop.saveSets('42', 3, [{ ...second, completed: true }], ['entry-0001'], 'update-0002', noopBaseline)).detail.version, 3)
})


test('a write preserves creation and recording instants even when offsets or fractional spellings differ', async () => {
  const original = detail({ createdAt: '2026-10-29T08:00:00.123400Z', recordedAt: '2026-10-29T08:01:00.000001Z' })
  const write = { ...set, completed: true }
  const result = detail({ version: 4, sets: [set], exerciseEntryKeys: ['entry-0001'],
    createdAt: '2026-10-29T16:00:00.1234+08:00', recordedAt: '2026-10-29T16:01:00.0000010+08:00' })
  const good = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? original : result }))
  const baseline = await good.readSession('42')
  await good.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', baseline)
  for (const response of [
    { ...result, createdAt: '2026-10-29T16:00:01.1234+08:00' },
    { ...result, recordedAt: '2026-10-29T16:01:00.000002+08:00' },
  ]) {
    const bad = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? original : response }))
    const badBaseline = await bad.readSession('42')
    await assert.rejects(bad.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', badBaseline), apiError('invalid_response'))
  }
})

test('restoring a custom group cannot acknowledge a different resolved name', async () => {
  const write = { ...set, completed: true }
  const api = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200,
    data: request.method === 'GET' ? detail() : detail({ version: 4, sets: [{ ...set, resolvedExerciseName: '不同名称' }], exerciseEntryKeys: ['entry-0001'] }) }))
  const baseline = await api.readSession('42')
  await assert.rejects(api.saveBatch('42', { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'], sets: [write], restoreRecordKeys: ['record-001'] }, 'restore-001', baseline), apiError('invalid_response'))
})


test('finish rejects a 200 response unless the saved status, version, fields and all groups are confirmed', async () => {
  const initial = detail({ sets: [set], exerciseEntryKeys: ['entry-0001'], durationMin: 30, note: '原备注' })
  const finished = detail({ version: 4, status: 'completed', recordedAt: '2026-10-29T08:00:01Z',
    sets: [set], exerciseEntryKeys: ['entry-0001'], durationMin: null, note: '新备注' })
  const wrong = [
    initial,
    { ...finished, version: 3 },
    { ...finished, recordedAt: null },
    { ...finished, sets: [{ ...set, reps: 7 }] },
    { ...finished, createdAt: '2026-10-28T08:00:00Z' },
    { ...finished, durationMin: 30 },
    { ...finished, note: '原备注' },
    { ...finished, date: '2026-10-28' },
  ]
  for (const response of wrong) {
    const api = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? initial : response }))
    const baseline = await api.readSession('42')
    await assert.rejects(api.finishSession('42', { expectedVersion: 3, durationMin: null, note: '新备注' }, 'finish-0001', baseline), apiError('invalid_response'))
  }
})

test('no-op set batch cannot advance its update timestamp', async () => {
  const original = detail({ sets: [set], exerciseEntryKeys: ['entry-0001'] })
  const api = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200,
    data: request.method === 'GET' ? original : { ...original, updatedAt: '2026-10-29T08:00:01Z' } }))
  const baseline = await api.readSession('42')
  await assert.rejects(api.saveSets('42', 3, [{ ...set, completed: true }], ['entry-0001'], 'update-0001', baseline), apiError('invalid_response'))
})


test('finish asks for reconciliation when a contract-optional field needed for confirmation is absent', async () => {
  const initial = detail({ sets: [set], exerciseEntryKeys: ['entry-0001'], durationMin: 30 })
  const withoutDuration = detail({ version: 4, status: 'completed', recordedAt: '2026-10-29T08:00:01Z', sets: [set], exerciseEntryKeys: ['entry-0001'] })
  for (const input of [{ expectedVersion: 3, durationMin: null }, { expectedVersion: 3 }]) {
    const api = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? initial : withoutDuration }))
    const baseline = await api.readSession('42')
    await assert.rejects(api.finishSession('42', input, 'finish-0001', baseline), apiError('reconcile_required'))
  }
})

test('finish rejects a backwards update instant but allows equal instants and offsets', async () => {
  const initial = detail({ updatedAt: '2026-10-29T08:00:00.000001Z', sets: [set], exerciseEntryKeys: ['entry-0001'] })
  const finished = detail({ version: 4, status: 'completed', recordedAt: '2026-10-29T08:00:01Z',
    updatedAt: '2026-10-29T16:00:00.0000010+08:00', sets: [set], exerciseEntryKeys: ['entry-0001'] })
  const good = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? initial : finished }))
  const baseline = await good.readSession('42')
  assert.equal((await good.finishSession('42', { expectedVersion: 3 }, 'finish-0001', baseline)).detail.version, 4)
  const bad = makeApi('https://api.example.org', auth, async request => ({ statusCode: 200, data: request.method === 'GET' ? initial :
    { ...finished, updatedAt: '2026-10-29T16:00:00.0000009+08:00' } }))
  const badBaseline = await bad.readSession('42')
  await assert.rejects(bad.finishSession('42', { expectedVersion: 3 }, 'finish-0001', badBaseline), apiError('invalid_response'))
})

test('the original client event identity is required and must survive create, update and finish responses', async () => {
  for (const invalid of [undefined, '', 'short', 'x'.repeat(65)]) {
    const raw = { ...detail(), clientEventId: invalid }
    const api = makeApi('https://api.example.org', auth, async () => ({ statusCode: 200, data: raw }))
    await assert.rejects(api.readSession('42'), apiError('invalid_response'))
  }

  const createApi = makeApi('https://api.example.org', auth, async () => ({
    statusCode: 201, data: detail({ clientEventId: 'event-0002' }),
  }))
  await assert.rejects(createApi.createFreeSession({ date: '2026-10-29', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' }, 'create-0001'), apiError('invalid_response'))

  const initial = detail({ sets: [set], exerciseEntryKeys: ['entry-0001'] })
  const write = { ...set, completed: true }
  const updateApi = makeApi('https://api.example.org', auth, async request => ({
    statusCode: 200,
    data: request.method === 'GET' ? initial : detail({ clientEventId: 'event-0002', version: 4,
      sets: [set], exerciseEntryKeys: ['entry-0001'] }),
  }))
  const baseline = await updateApi.readSession('42')
  await assert.rejects(updateApi.saveSets('42', 3, [write], ['entry-0001'], 'update-0001', baseline), apiError('invalid_response'))

  const finishApi = makeApi('https://api.example.org', auth, async request => ({
    statusCode: 200,
    data: request.method === 'GET' ? initial : detail({ clientEventId: 'event-0002', status: 'completed', version: 4,
      recordedAt: '2026-10-29T08:01:00Z', sets: [set], exerciseEntryKeys: ['entry-0001'] }),
  }))
  const finishBaseline = await finishApi.readSession('42')
  await assert.rejects(finishApi.finishSession('42', { expectedVersion: 3 }, 'finish-0001', finishBaseline), apiError('invalid_response'))
})
