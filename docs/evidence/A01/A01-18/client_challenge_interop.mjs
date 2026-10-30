// Test-only Node port against the private httptest TLS server; not an app transport.
import { randomBytes, createECDH } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

const base = process.env.NERAE_AUTH_INTEROP_URL
const source = process.env.NERAE_AUTH_INTEROP_CLIENT_DIR
if (!base || !source) throw new Error('isolated interop configuration missing')
const { EmailAuthApi, AuthApiError, challengeNonceForSigning } =
  await import(pathToFileURL(path.join(source, 'emailAuthApi.ts')).href)
const ec = createECDH('prime256v1')
ec.generateKeys()
const point = ec.getPublicKey()
const input = {
  email: 'client-interop@example.test',
  devicePublicKey: { kty: 'EC', crv: 'P-256', x: point.subarray(1, 33).toString('base64url'), y: point.subarray(33).toString('base64url') },
  deviceLabel: 'synthetic device',
}
const key = randomBytes(32).toString('base64url')
const port = async request => {
  const response = await fetch(request.url, {
    method: request.method, headers: request.headers, body: request.body, redirect: 'error', cache: 'no-store',
  })
  const body = await response.json()
  return { status: response.status, headers: Object.fromEntries(response.headers), body }
}
const api = new EmailAuthApi(base, port)
const first = await api.requestChallenge(input, key)
const again = await api.requestChallenge(input, key)
if (first.challengeId !== again.challengeId || first.expiresAt !== again.expiresAt ||
    challengeNonceForSigning(first) !== challengeNonceForSigning(again)) throw new Error('same-key replay differed')
let conflict = false
try { await api.requestChallenge({ ...input, deviceLabel: 'different synthetic device' }, key) }
catch (error) { conflict = error instanceof AuthApiError && error.code === 'conflict' }
if (!conflict) throw new Error('changed request was not rejected as a conflict')
process.stdout.write('PASS: actual email client adapter requested and replayed a challenge over local TLS; changed request conflicted.\n')
