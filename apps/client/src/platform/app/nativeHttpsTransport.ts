import { AuthApiError, type AuthHttpRequest, type AuthHttpResponse, type SecureAuthTransport } from '../../api/auth/emailAuthApi'
import { RecordingApiError, type HttpRequest, type HttpResponse, type RequestPort } from '../../features/training/recordingApi'

export type HttpsMethod = 'GET' | 'POST' | 'DELETE'
export type HttpsRequest = {
  baseUrl: string
  path: string
  method: HttpsMethod
  headersJson: string
  body: string
  timeoutMs: number
}
export type HttpsResponse = { requestId: string; status: number; headersJson: string; body: string }
export type HttpsErrorCode = 'unsupported_platform' | 'invalid_configuration' | 'invalid_request' |
  'network' | 'timeout' | 'cancelled' | 'response_too_large' | 'invalid_response' | 'unavailable'
export type HttpsFailure = { requestId: string; code: HttpsErrorCode }
export type HttpsCallbacks = { success: (response: HttpsResponse) => void; fail: (error: HttpsFailure) => void }
export type NativeHttpsRequestPort = (request: HttpsRequest, callbacks: HttpsCallbacks) => string
export type NativeHttpsCancelPort = (requestId: string) => void

export interface AuthenticatedMeWireResponse {
  readonly status: number
  readonly headers: Readonly<Record<string, string>>
  readonly body: Readonly<Record<string, unknown>>
}

export type SessionRevocationOutcome =
  | { readonly state: 'revoked' }
  | { readonly state: 'rejected'; readonly reason: 'unauthorized' | 'forbidden' | 'not_found' | 'conflict' | 'expired' | 'rate_limited' }
  | { readonly state: 'unknown' }

export type AppNativeHttpsPorts = {
  authTransport: SecureAuthTransport
  recordingRequest: RequestPort
  readCurrentUser(accessToken: string): Promise<AuthenticatedMeWireResponse>
  revokeCurrentSession(accessToken: string): Promise<SessionRevocationOutcome>
}

const maxBodyBytes = 4 * 1024 * 1024
const maxHeadersBytes = 20 * 1024
const responseHeaderNames = new Set(['content-type', 'cache-control', 'x-request-id', 'idempotency-key-expires-at'])
const requestHeaderNames = new Set(['accept', 'authorization', 'cache-control', 'content-type', 'idempotency-key', 'x-request-id'])
const failureCodes = new Set<HttpsErrorCode>([
  'unsupported_platform', 'invalid_configuration', 'invalid_request', 'network', 'timeout',
  'cancelled', 'response_too_large', 'invalid_response', 'unavailable',
])

/** Count UTF-8 bytes without relying on TextEncoder, which is not universal in JSCore. */
function utf8ByteLength(value: string): number {
  let bytes = 0
  for (let i = 0; i < value.length; i++) {
    const unit = value.charCodeAt(i)
    if (unit <= 0x7f) bytes++
    else if (unit <= 0x7ff) bytes += 2
    else if (unit >= 0xd800 && unit <= 0xdbff) { bytes += 4; i++ }
    else bytes += 3
  }
  return bytes
}

class TransportFailure extends Error {
  readonly code: HttpsErrorCode
  constructor(code: HttpsErrorCode) {
    super('native transport failure')
    this.name = 'TransportFailure'
    this.code = code
  }
}

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function wellFormed(value: string): boolean {
  for (let i = 0; i < value.length; i++) {
    const unit = value.charCodeAt(i)
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const next = value.charCodeAt(i + 1)
      if (!(next >= 0xdc00 && next <= 0xdfff)) return false
      i++
    } else if (unit >= 0xdc00 && unit <= 0xdfff) return false
  }
  return true
}

/** Strict JSON parser: rejects duplicate decoded keys at every object depth. */
function parseStrictJson(text: unknown, maxBytes: number): unknown {
  if (typeof text !== 'string' || !wellFormed(text)) throw new TransportFailure('invalid_response')
  if (utf8ByteLength(text) > maxBytes) {
    throw new TransportFailure('response_too_large')
  }
  let cursor = 0
  let nodes = 0
  const fail = (): never => { throw new TransportFailure('invalid_response') }
  const whitespace = (): void => { while (/[\u0020\u0009\u000a\u000d]/.test(text[cursor] || '\u0000')) cursor++ }
  const parseString = (): string => {
    if (text[cursor] !== '"') return fail()
    const start = cursor++
    while (cursor < text.length) {
      const code = text.charCodeAt(cursor)
      if (code === 0x22) {
        cursor++
        let decoded: unknown
        try { decoded = JSON.parse(text.slice(start, cursor)) } catch { return fail() }
        if (typeof decoded !== 'string' || !wellFormed(decoded)) return fail()
        return decoded
      }
      if (code < 0x20) return fail()
      if (code === 0x5c) {
        cursor++
        const escape = text[cursor]
        if (escape === 'u') {
          if (!/^[0-9a-fA-F]{4}$/.test(text.slice(cursor + 1, cursor + 5))) return fail()
          cursor += 5
          continue
        }
        if (!escape || !'"\\/bfnrt'.includes(escape)) return fail()
      }
      cursor++
    }
    return fail()
  }
  const parseNumber = (): number => {
    const match = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/.exec(text.slice(cursor))
    if (!match) return fail()
    cursor += match[0].length
    const value = Number(match[0])
    if (!Number.isFinite(value)) return fail()
    return value
  }
  const parseValue = (depth: number): unknown => {
    if (depth > 64 || ++nodes > 100_000) return fail()
    whitespace()
    const current = text[cursor]
    if (current === '"') return parseString()
    if (current === '{') {
      cursor++
      whitespace()
      const result: Record<string, unknown> = {}
      const seen = new Set<string>()
      if (text[cursor] === '}') { cursor++; return result }
      while (cursor < text.length) {
        whitespace()
        const key = parseString()
        if (seen.has(key)) return fail()
        seen.add(key)
        whitespace()
        if (text[cursor++] !== ':') return fail()
        Object.defineProperty(result, key, { value: parseValue(depth + 1), enumerable: true, configurable: true, writable: true })
        whitespace()
        if (text[cursor] === '}') { cursor++; return result }
        if (text[cursor++] !== ',') return fail()
      }
      return fail()
    }
    if (current === '[') {
      cursor++
      whitespace()
      const result: unknown[] = []
      if (text[cursor] === ']') { cursor++; return result }
      while (cursor < text.length) {
        result.push(parseValue(depth + 1))
        whitespace()
        if (text[cursor] === ']') { cursor++; return result }
        if (text[cursor++] !== ',') return fail()
      }
      return fail()
    }
    if (text.startsWith('true', cursor)) { cursor += 4; return true }
    if (text.startsWith('false', cursor)) { cursor += 5; return false }
    if (text.startsWith('null', cursor)) { cursor += 4; return null }
    if (current === '-' || (current >= '0' && current <= '9')) return parseNumber()
    return fail()
  }
  const result = parseValue(0)
  whitespace()
  if (cursor !== text.length) return fail()
  return result
}

function parsedResponseBody(raw: unknown): Record<string, unknown> {
  const value = parseStrictJson(raw, maxBodyBytes)
  if (!object(value)) throw new TransportFailure('invalid_response')
  return value
}

function responseHeaders(raw: unknown): Readonly<Record<string, string>> {
  const value = parseStrictJson(raw, maxHeadersBytes)
  if (!object(value)) throw new TransportFailure('invalid_response')
  const names = new Set<string>()
  const result: Record<string, string> = {}
  for (const [name, headerValue] of Object.entries(value)) {
    const lower = name.toLowerCase()
    if (!responseHeaderNames.has(lower) || names.has(lower) || name.length < 1 || name.length > 128 ||
      !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) || typeof headerValue !== 'string' ||
      !wellFormed(headerValue) || utf8ByteLength(headerValue) > 8192 || /[\u0000-\u001f\u007f]/.test(headerValue)) {
      throw new TransportFailure('invalid_response')
    }
    names.add(lower)
    Object.defineProperty(result, name, { value: headerValue, enumerable: true, configurable: true, writable: true })
  }
  return Object.freeze(result)
}

type ConfiguredBase = { baseUrl: string; origin: string; path: string }
function configuredBase(value: unknown): ConfiguredBase {
  if (typeof value !== 'string' || value.length > 2048 || !/^https:\/\//i.test(value) || /[\u0000-\u0020\u007f\\?#]/.test(value)) {
    throw new TransportFailure('invalid_configuration')
  }
  let url: URL
  try { url = new URL(value) } catch { throw new TransportFailure('invalid_configuration') }
  if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.search || url.hash ||
    url.port && (!/^\d+$/.test(url.port) || Number(url.port) < 1 || Number(url.port) > 65535)) {
    throw new TransportFailure('invalid_configuration')
  }
  const path = url.pathname.replace(/\/+$/, '')
  const baseUrl = `${url.origin}${path}`
  return { baseUrl, origin: url.origin, path }
}

function routeFor(base: ConfiguredBase, value: unknown): string {
  if (typeof value !== 'string' || value.length > 8192) throw new TransportFailure('invalid_request')
  let url: URL
  try { url = new URL(value) } catch { throw new TransportFailure('invalid_request') }
  if (url.protocol !== 'https:' || url.origin !== base.origin || url.username || url.password || url.hash) {
    throw new TransportFailure('invalid_request')
  }
  const path = url.pathname
  if (base.path ? !path.startsWith(`${base.path}/`) : !path.startsWith('/')) throw new TransportFailure('invalid_request')
  const routePath = path.slice(base.path.length)
  for (const part of routePath.split('/')) {
    let decoded: string
    try { decoded = decodeURIComponent(part) } catch { throw new TransportFailure('invalid_request') }
    if (decoded === '.' || decoded === '..' || decoded.includes('/') || decoded.includes('\\')) {
      throw new TransportFailure('invalid_request')
    }
  }
  const route = `${routePath}${url.search}`
  if (!route.startsWith('/') || route.startsWith('//') || route.includes('\\') || /[\u0000-\u0020\u007f]/.test(route)) {
    throw new TransportFailure('invalid_request')
  }
  return route
}

function headersForNative(value: unknown): string {
  if (!object(value)) throw new TransportFailure('invalid_request')
  const names = new Set<string>()
  let total = 0
  for (const [name, headerValue] of Object.entries(value)) {
    const lower = name.toLowerCase()
    if (!requestHeaderNames.has(lower) || names.has(lower) || name.length < 1 || name.length > 128 ||
      !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) || typeof headerValue !== 'string' ||
      !wellFormed(headerValue) || headerValue.length > 8192 || /[\u0000-\u001f\u007f]/.test(headerValue)) {
      throw new TransportFailure('invalid_request')
    }
    names.add(lower)
    total += utf8ByteLength(name) + utf8ByteLength(headerValue)
    if (total > 16 * 1024) throw new TransportFailure('invalid_request')
  }
  const encoded = JSON.stringify(value)
  if (utf8ByteLength(encoded) > maxHeadersBytes) throw new TransportFailure('invalid_request')
  return encoded
}

function requestBody(value: unknown, method: HttpsMethod): string {
  if (value === undefined) return ''
  if (typeof value !== 'string' || !wellFormed(value) || utf8ByteLength(value) > maxBodyBytes ||
    ((method === 'GET' || method === 'DELETE') && value !== '')) {
    throw new TransportFailure('invalid_request')
  }
  return value
}

function validRequestId(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 128 && /^[\x21-\x7e]*$/.test(value)
}

type NativeParsedResponse = {
  status: number
  headers: Readonly<Record<string, string>>
  body: Record<string, unknown>
  emptyBody: boolean
}

function validateNativeResponse(value: unknown, expectedRequestId: string, allowEmpty204 = false): NativeParsedResponse {
  if (!object(value) || Object.keys(value).length !== 4 || !['requestId', 'status', 'headersJson', 'body'].every(key => Object.prototype.hasOwnProperty.call(value, key)) ||
    !validRequestId(value.requestId) || !expectedRequestId || value.requestId !== expectedRequestId ||
    typeof value.headersJson !== 'string' || typeof value.body !== 'string' ||
    !Number.isInteger(value.status) || Number(value.status) < 100 || Number(value.status) > 599) {
    throw new TransportFailure('invalid_response')
  }
  const status = Number(value.status)
  const headers = responseHeaders(value.headersJson)
  if (allowEmpty204 && status === 204 && value.body === '') {
    return { status, headers, body: Object.freeze({}), emptyBody: true }
  }
  return { status, headers, body: parsedResponseBody(value.body), emptyBody: value.body === '' }
}

function mapFailure(code: HttpsErrorCode, surface: 'auth' | 'recording'): Error {
  if (surface === 'auth') {
    if (code === 'invalid_configuration') return new AuthApiError('invalid_configuration')
    if (code === 'invalid_request') return new AuthApiError('invalid_request')
    if (code === 'network' || code === 'timeout' || code === 'cancelled') return new AuthApiError('network')
    if (code === 'invalid_response' || code === 'response_too_large') return new AuthApiError('invalid_response')
    return new AuthApiError('unavailable')
  }
  if (code === 'invalid_request' || code === 'invalid_configuration') return new RecordingApiError('invalid_request')
  if (code === 'network' || code === 'timeout' || code === 'cancelled') return new RecordingApiError('network')
  if (code === 'invalid_response' || code === 'response_too_large') return new RecordingApiError('invalid_response')
  return new RecordingApiError('unavailable')
}

function nativeFailure(value: unknown, returnedId: string): HttpsErrorCode {
  if (!object(value) || Object.keys(value).length !== 2 || !['requestId', 'code'].every(key => Object.prototype.hasOwnProperty.call(value, key)) ||
    !validRequestId(value.requestId) || (value.requestId !== '' && value.requestId !== returnedId) ||
    typeof value.code !== 'string' || !failureCodes.has(value.code as HttpsErrorCode)) return 'invalid_response'
  return value.code as HttpsErrorCode
}

function makeNativeRequest(base: ConfiguredBase, port: NativeHttpsRequestPort, cancel: NativeHttpsCancelPort,
  timeoutMs: number, method: HttpsMethod, url: string, headers: unknown, data: unknown,
  allowEmpty204 = false): Promise<NativeParsedResponse> {
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 120_000) throw new TransportFailure('invalid_configuration')
  const path = routeFor(base, url)
  const headersJson = headersForNative(headers)
  const body = requestBody(data, method)
  const request: HttpsRequest = { baseUrl: base.baseUrl, path, method, headersJson, body, timeoutMs }
  return new Promise((resolve, reject) => {
    let returnedId = ''
    let returned = false
    let settled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let callback: { ok: true; value: HttpsResponse } | { ok: false; value: HttpsFailure } | undefined
    const finish = (): void => {
      if (!returned || !callback || settled) return
      settled = true
      if (timer !== undefined) clearTimeout(timer)
      if (!callback.ok) {
        reject(new TransportFailure(nativeFailure(callback.value, returnedId)))
        return
      }
      try { resolve(validateNativeResponse(callback.value, returnedId, allowEmpty204)) }
      catch (error) { reject(error instanceof TransportFailure ? error : new TransportFailure('invalid_response')) }
    }
    const callbacks: HttpsCallbacks = {
      success: value => { if (settled || callback) return; callback = { ok: true, value }; finish() },
      fail: value => { if (settled || callback) return; callback = { ok: false, value }; finish() },
    }
    try {
      returnedId = port(request, callbacks)
      if (!validRequestId(returnedId) || (returnedId.length === 0 && !(callback && !callback.ok && callback.value.requestId === ''))) {
        returned = true
        settled = true
        reject(new TransportFailure('unavailable'))
        return
      }
      returned = true
      if (returnedId.length > 0) {
        timer = setTimeout(() => {
          if (settled) return
          settled = true
          try { cancel(returnedId) } catch { /* cancellation errors carry no response information */ }
          reject(new TransportFailure('timeout'))
        }, timeoutMs + 1000)
      }
      finish()
    } catch {
      if (!settled) {
        settled = true
        if (timer !== undefined) clearTimeout(timer)
        reject(new TransportFailure('network'))
      }
    }
  })
}

/** App-only ports over the callback bridge. Construction requires an explicit HTTPS base. */
export function createAppNativeHttpsPorts(baseUrl: string, nativeRequest: NativeHttpsRequestPort,
  nativeCancel: NativeHttpsCancelPort, timeoutMs = 30_000): AppNativeHttpsPorts {
  let base: ConfiguredBase
  try {
    base = configuredBase(baseUrl)
    if (typeof nativeRequest !== 'function' || typeof nativeCancel !== 'function' ||
      !Number.isSafeInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 120_000) throw new TransportFailure('invalid_configuration')
  } catch { throw new AuthApiError('invalid_configuration') }

  const run = (method: HttpsMethod, url: string, headers: unknown, body: unknown, surface: 'auth' | 'recording', allowEmpty204 = false) => {
    try {
      return makeNativeRequest(base, nativeRequest, nativeCancel, timeoutMs, method, url, headers, body, allowEmpty204).catch(error => {
        throw mapFailure(error instanceof TransportFailure ? error.code : 'unavailable', surface)
      })
    } catch (error) {
      return Promise.reject(mapFailure(error instanceof TransportFailure ? error.code : 'unavailable', surface))
    }
  }

  const authTransport: SecureAuthTransport = async (request: AuthHttpRequest): Promise<AuthHttpResponse> => {
    if (!request || request.method !== 'POST' || typeof request.body !== 'string') throw new AuthApiError('invalid_request')
    const result = await run('POST', request.url, request.headers, request.body, 'auth')
    return { status: result.status, headers: result.headers, body: result.body }
  }
  const recordingRequest: RequestPort = async (request: HttpRequest): Promise<HttpResponse> => {
    if (!request || (request.method !== 'GET' && request.method !== 'POST')) throw new RecordingApiError('invalid_request')
    const result = await run(request.method, request.url, request.header, request.data, 'recording')
    return { statusCode: result.status, data: result.body, headers: result.headers }
  }
  const readCurrentUser = async (accessToken: string): Promise<AuthenticatedMeWireResponse> => {
    if (typeof accessToken !== 'string' || accessToken.length < 1 || accessToken.length > 8192 ||
      !/^[\x21-\x7e]+$/.test(accessToken)) throw new AuthApiError('invalid_request')
    const result = await run('GET', `${base.baseUrl}/me`, {
      Accept: 'application/json', Authorization: `Bearer ${accessToken}`, 'Cache-Control': 'no-store',
    }, '', 'auth')
    return Object.freeze({ status: result.status, headers: result.headers, body: result.body })
  }
  const revokeCurrentSession = async (accessToken: string): Promise<SessionRevocationOutcome> => {
    if (typeof accessToken !== 'string' || accessToken.length < 1 || accessToken.length > 8192 ||
      !/^[\x21-\x7e]+$/.test(accessToken)) throw new AuthApiError('invalid_request')
    let result: NativeParsedResponse
    try {
      result = await run('DELETE', `${base.baseUrl}/auth/sessions/current`, {
        Accept: 'application/json', Authorization: `Bearer ${accessToken}`, 'Cache-Control': 'no-store',
      }, '', 'auth', true)
    } catch {
      // The request may have reached the server and committed before its
      // response was lost. Callers must not present this as a definite failure
      // or blindly retry with a new operation.
      return { state: 'unknown' }
    }
    const cacheControl = Object.entries(result.headers).filter(([name]) => name.toLowerCase() === 'cache-control')
    if (cacheControl.length !== 1 || typeof cacheControl[0][1] !== 'string' ||
      cacheControl[0][1].trim().toLowerCase() !== 'no-store') return { state: 'unknown' }
    if (result.status === 204) {
      return result.emptyBody ? { state: 'revoked' } : { state: 'unknown' }
    }
    if (result.status === 401) return { state: 'rejected', reason: 'unauthorized' }
    if (result.status === 403) return { state: 'rejected', reason: 'forbidden' }
    if (result.status === 404) return { state: 'rejected', reason: 'not_found' }
    if (result.status === 409) return { state: 'rejected', reason: 'conflict' }
    if (result.status === 410) return { state: 'rejected', reason: 'expired' }
    if (result.status === 429) return { state: 'rejected', reason: 'rate_limited' }
    return { state: 'unknown' }
  }
  return { authTransport, recordingRequest, readCurrentUser, revokeCurrentSession }
}
