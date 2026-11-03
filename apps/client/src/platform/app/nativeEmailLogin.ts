import { generateSecureRandomRequestKey } from '@/uni_modules/nerae-random'
import type { EmailAuthApi } from '../../api/auth/emailAuthApi'
import { EmailLoginFlow } from '../../features/auth/emailLoginFlow'
import type { AuthSessionCoordinator } from '../../features/auth/sessionCoordinator'
import { createAppDeviceIdentityCoordinator, signAppApprovalExchange, signAppEmailLoginChallenge } from './nativeDeviceIdentity'

let flow: EmailLoginFlow | undefined
let boundApi: EmailAuthApi | undefined
let boundSession: AuthSessionCoordinator | undefined

/** The caller must construct the API with the app's trusted service configuration. */
export function createAppEmailLoginFlow(api: EmailAuthApi, session: AuthSessionCoordinator): EmailLoginFlow {
  if (flow) {
    if (boundApi !== api || boundSession !== session) throw new Error('App login is already configured')
    return flow
  }
  const created = new EmailLoginFlow(api, createAppDeviceIdentityCoordinator(), session, () => {
    const result = generateSecureRandomRequestKey()
    if (!result || result.ok !== true || result.errorCode !== '') throw new Error('secure randomness unavailable')
    return result.value
  }, signAppEmailLoginChallenge, signAppApprovalExchange)
  boundApi = api
  boundSession = session
  flow = created
  return created
}
