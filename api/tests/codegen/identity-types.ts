// Compile generated identity shapes; actual authorization and crypto need runtime tests.
import type { components } from './generated-types';

type Outcome = components['schemas']['PhoneLoginOutcome'];
type Identity = components['schemas']['IdentityItem'];
type Link = components['schemas']['IdentityLinkStart'];
type Recovery = components['schemas']['RecoveryCodeUseRequest'];
type Setup = components['schemas']['RecoveryCodeIssueRequest'];
type EmailChallenge = components['schemas']['EmailLoginChallengeRequest'];
type EmailLogin = components['schemas']['EmailLoginRequest'];
type AgeAttestation = components['schemas']['AgeAttestation'];

declare const outcome: Outcome;
if (outcome.status === 'approval_required') {
  const secret: string = outcome.approvalSecret;
  // @ts-expect-error Pending approval must not expose a session token.
  const token: string = outcome.token;
  void [secret, token];
} else {
  const token: string = outcome.token;
  void token;
}
const phone: Identity = { id: 'opaque', kind: 'phone', phoneLast4: '8000' };
const mini: Identity = { id: 'opaque', kind: 'wechat_mini' };
const email: Identity = { id: 'opaque', kind: 'email', emailMasked: 'p***@example.com' };
const emailLink: Link = { kind: 'email', email: 'person@example.com', expectedVersion: 1 };
const emailChallenge: EmailChallenge = { email: 'person@example.com', devicePublicKey: { kty: 'EC', crv: 'P-256', x: 'x', y: 'y' } };
const replace: Link = { kind: 'phone', intent: 'replace', phone: '+8613800138000', expectedVersion: 1 };
const recovery: Recovery = { approvalSecret: 'secret', recoveryCode: 'code', deviceProof: 'proof' };
const setup: Setup = { expectedGeneration: 0, confirmed: true };
const age: AgeAttestation = { isAdult: true, expectedVersion: 1 };
// @ts-expect-error A phone link must distinguish adding from replacing.
const missingIntent: Link = { kind: 'phone', phone: '+8613800138000', expectedVersion: 1 };
// @ts-expect-error User confirmation is required before issuing a recovery code.
const rejectedSetup: Setup = { expectedGeneration: 0, confirmed: false };
// @ts-expect-error Recovery requires proof of the same device key.
const missingProof: Recovery = { approvalSecret: 'secret', recoveryCode: 'code' };
// @ts-expect-error Email login also requires proof of the target device key.
const noEmailProof: EmailLogin = { challengeId: 'challenge', code: '123456' };
// @ts-expect-error An adult declaration must use the current version.
const ageWithoutVersion: AgeAttestation = { isAdult: true };
void [phone, mini, email, emailLink, emailChallenge, replace, recovery, setup, age, missingIntent, rejectedSetup, missingProof, noEmailProof, ageWithoutVersion];
