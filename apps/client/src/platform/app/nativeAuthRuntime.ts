import { EmailAuthApi } from '../../api/auth/emailAuthApi'
import { AgeAttestationApi } from '../../features/auth/ageAttestationApi'
import type { EmailLoginFlow } from '../../features/auth/emailLoginFlow'
import type { AuthSessionCoordinator } from '../../features/auth/sessionCoordinator'
import { createConfiguredAppHttpsPorts } from './index'
import { createAppEmailLoginFlow } from './nativeEmailLogin'
import { createAppSessionCoordinator } from './nativeSession'

export interface AppAuthRuntime {
  readonly session: AuthSessionCoordinator
  readonly login: EmailLoginFlow
  readonly ageDeclaration: AgeAttestationApi
}

let runtime: AppAuthRuntime | undefined
let configuredBase: string | undefined

/**
 * Bind the App's existing native HTTPS, device key and secure session ports to
 * one explicitly supplied HTTPS service. No development URL or mock fallback
 * is provided. The page is responsible for presenting the resulting states.
 */
export function createAppAuthRuntime(baseUrl: string): AppAuthRuntime {
  // The transport validates the origin; normalize trailing slashes once so all
  // assembled clients construct paths against exactly the same service base.
  const ports = createConfiguredAppHttpsPorts(baseUrl)
  const url = new URL(baseUrl)
  const base = `${url.origin}${url.pathname.replace(/\/+$/, '')}`
  if (runtime) {
    if (base !== configuredBase) throw new Error('App authentication is already configured')
    return runtime
  }
  // Validate both base and API before creating any process-wide coordinator.
  const api = new EmailAuthApi(base, ports.authTransport)
  const session = createAppSessionCoordinator(ports.readCurrentUser)
  const ageDeclaration = new AgeAttestationApi(base, ports.authTransport, session)
  const login = createAppEmailLoginFlow(api, session)
  const result = Object.freeze({ session, login, ageDeclaration })
  configuredBase = base
  runtime = result
  return result
}
