// Test-only Node device/vault against private local TLS services; not a native App.
import { createHmac, generateKeyPairSync, randomBytes, sign } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

async function main() {
  const { NERAE_AUTH_INTEROP_URL: base, NERAE_AUTH_INTEROP_ROOT: root,
    NERAE_AUTH_INTEROP_SOURCE: sourceToken, NERAE_AUTH_INTEROP_SUBJECT: subject,
    NERAE_AUTH_INTEROP_WORKOUT: workout } = process.env
  if (!base || !root || !sourceToken || !subject || !workout) throw new Error('fixture configuration missing')
  const fromRoot = file => pathToFileURL(path.join(root, file)).href
  const { EmailAuthApi, challengeNonceForSigning, approvalSecretsForSecureStorage } =
    await import(fromRoot('apps/client/src/api/auth/emailAuthApi.ts'))
  const { EmailLoginFlow } = await import(fromRoot('apps/client/src/features/auth/emailLoginFlow.ts'))
  const { ApprovalOwnerApi } = await import(fromRoot('apps/client/src/features/auth/approvalOwnerApi.ts'))
  const { AuthSessionCoordinator } = await import(fromRoot('apps/client/src/features/auth/sessionCoordinator.ts'))

  const { privateKey, publicKey } = generateKeyPairSync('ec', { namedCurve: 'prime256v1' })
  const jwk = publicKey.export({ format: 'jwk' })
  const identity = { publicKeyForProtocol: { kty: 'EC', crv: 'P-256', x: jwk.x, y: jwk.y } }
  const random = () => randomBytes(32).toString('base64url')
  const deviceProof = (purpose, resourceId, nonce) => {
    const now = Math.floor(Date.now() / 1000)
    const header = Buffer.from(JSON.stringify({ alg: 'ES256', typ: 'JWT' })).toString('base64url')
    const claims = Buffer.from(JSON.stringify({ aud: 'nerae-auth-device', purpose, resourceId, nonce,
      iat: now, exp: now + 60, jti: random() })).toString('base64url')
    const unsigned = `${header}.${claims}`
    const signature = sign('sha256', Buffer.from(unsigned), { key: privateKey, dsaEncoding: 'ieee-p1363' }).toString('base64url')
    return { forSubmission: () => `${unsigned}.${signature}` }
  }
  // This key is fixed inside the isolated Go fixture; it is not a mail credential.
  const deriveSyntheticCode = id => {
    const fields = [Buffer.from(id), Buffer.from('email_login'), Buffer.from([6]), Buffer.alloc(8)]
    const hmac = createHmac('sha256', Buffer.alloc(32, 0x45)).update('nerae.auth.email-code-derive.v1\0')
    for (const field of fields) {
      const size = Buffer.alloc(4); size.writeUInt32BE(field.length)
      hmac.update(size).update(field)
    }
    const value = hmac.digest().readBigUInt64BE()
    // A rejected sample is astronomically unlikely; fail rather than silently diverge.
    if (value < ((1n << 64n) - 1000000n) % 1000000n) throw new Error('synthetic sample rejected')
    return (value % 1000000n).toString().padStart(6, '0')
  }

  let lastChallenge = ''
  let dropApprovedResponse = true
  const exchanges = []
  const api = new EmailAuthApi(base, async request => {
    const response = await fetch(request.url, { method: request.method, headers: request.headers,
      body: request.body, redirect: 'error', cache: 'no-store' })
    const body = await response.json()
    if (request.url.endsWith('/auth/email/challenges') && response.status === 202) lastChallenge = body.challengeId
    if (request.url.includes('/auth/login-approvals/')) {
      exchanges.push({ body: request.body, key: request.headers['Idempotency-Key'] })
      if (body.status === 'authenticated' && dropApprovedResponse) {
        dropApprovedResponse = false
        throw new Error('synthetic response lost after server commit')
      }
    }
    return { status: response.status, headers: Object.fromEntries(response.headers), body }
  })
  let stored = ''
  const vault = {
    readAuthenticationState: () => ({ ok: true, errorCode: '', found: !!stored, value: stored }),
    writeAuthenticationState: value => { stored = value; return { ok: true, errorCode: '', found: false, value: '' } },
    clearAuthenticationState: () => { stored = ''; return { ok: true, errorCode: '', found: false, value: '' } },
  }
  const session = new AuthSessionCoordinator(vault, async () => ({ status: 'unavailable' }))
  const flow = new EmailLoginFlow(api, { async initializeFirstForEmail() { return { status: 'ready', identity } } }, session, random,
    (_identity, challenge) => deviceProof('email_login', challenge.challengeId, challengeNonceForSigning(challenge)),
    (_identity, approval) => deviceProof('approval_exchange', approval.approvalId, approvalSecretsForSecureStorage(approval).nonce))

  if ((await flow.requestCode('a01-24-e2e@example.test')).status !== 'waiting_code' || !lastChallenge) throw new Error('challenge failed')
  const pending = await flow.submitCode(deriveSyntheticCode(lastChallenge))
  if (pending.status !== 'approval_required' || (await flow.checkApproval()).status !== 'approval_pending') throw new Error('pending flow failed')
  let dropDecisionResponse = true
  const ownerRequests = []
  const ownerSession = {
    current: () => ({ subjectId: subject, generation: 'synthetic-owner-generation', accessToken: sourceToken }),
    isCurrent: (id, generation) => id === subject && generation === 'synthetic-owner-generation',
    captureInstallGuard: () => ({}), subscribeInstallGuardInvalidation: () => () => {},
  }
  const owner = new ApprovalOwnerApi(base, async request => {
    const response = await fetch(request.url, { method: request.method, headers: request.header, body: request.data, redirect: 'error' })
    const body = await response.json()
    if (request.method === 'POST') {
      ownerRequests.push({ url: request.url, body: request.data, key: request.header['Idempotency-Key'] })
      if (body.status === 'approved' && dropDecisionResponse) {
        dropDecisionResponse = false
        throw new Error('synthetic owner response lost after commit')
      }
    }
    return { statusCode: response.status, headers: Object.fromEntries(response.headers), data: body }
  }, ownerSession, random)
  const listed = await owner.list()
  const item = listed.status === 'ready' ? listed.items.find(value => value.comparisonCode === pending.comparisonCode) : undefined
  if (!item || (await owner.decide(item, 'approve', true)).status !== 'retry' ||
    (await owner.retryDecision()).status !== 'approved' || ownerRequests.length !== 2 ||
    JSON.stringify(ownerRequests[0]) !== JSON.stringify(ownerRequests[1])) throw new Error('owner decision replay failed')
  if ((await flow.checkApproval()).status !== 'retry_approval') throw new Error('lost response was not retained')
  const result = await flow.checkApproval()
  if (result.status !== 'authenticated' || result.session.subjectId !== subject || exchanges.length !== 3 ||
    exchanges[0].key === exchanges[1].key || JSON.stringify(exchanges[1]) !== JSON.stringify(exchanges[2])) {
    throw new Error('approval replay failed')
  }
  const accessToken = JSON.parse(stored).accessToken
  const record = await fetch(`${base}/me/sessions/${workout}`, { headers: { Authorization: `Bearer ${accessToken}` }, redirect: 'error' })
  if (!record.ok || (await record.json()).id !== workout) throw new Error('shared workout unavailable')
  process.stdout.write('PASS: actual owner client replayed a lost approval decision over isolated TLS\n')
  process.stdout.write('PASS: actual client flow approved and replayed a lost response over isolated TLS, then read the shared workout\n')
}

main().catch(() => { process.stderr.write('isolated client approval interop failed\n'); process.exitCode = 1 })
