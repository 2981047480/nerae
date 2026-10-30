import type { AuthUser } from '../../api/auth/emailAuthApi'
import type { PersistedCredentialCheck, PersistedCredentialVerifier } from './sessionCoordinator'

/** Narrow port returning the native transport's parsed status, headers and JSON object. */
export interface CurrentUserResponse {
  readonly status: number
  readonly headers: Readonly<Record<string, string>>
  readonly body: unknown
}

export type CurrentUserReadPort = (accessToken: string) => Promise<CurrentUserResponse>

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function validSubjectId(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 20 && /^[1-9][0-9]*$/.test(value)
}

function noStore(headers: unknown): boolean {
  if (!isObject(headers)) return false
  const cacheControl = Object.entries(headers).filter(([name]) => name.toLowerCase() === 'cache-control')
  const contentType = Object.entries(headers).filter(([name]) => name.toLowerCase() === 'content-type')
  return cacheControl.length === 1 && typeof cacheControl[0][1] === 'string' &&
    cacheControl[0][1].trim().toLowerCase() === 'no-store' &&
    contentType.length === 1 && typeof contentType[0][1] === 'string' &&
    /^application\/json(?:\s*;\s*charset=utf-8)?$/i.test(contentType[0][1].trim())
}

function isServerUnauthenticated(body: unknown, headers: unknown): boolean {
  if (!isObject(body) || !isObject(headers) || body.code !== 'UNAUTHENTICATED' ||
    typeof body.message !== 'string' || body.message.length > 2048 ||
    typeof body.requestId !== 'string' || !/^[a-f0-9]{32}$/.test(body.requestId) ||
    !['code', 'message', 'requestId'].every(key => Object.prototype.hasOwnProperty.call(body, key))) return false
  if ('details' in body && !isObject(body.details)) return false
  const requestIds = Object.entries(headers).filter(([name]) => name.toLowerCase() === 'x-request-id')
  return requestIds.length === 1 && requestIds[0][1] === body.requestId
}

function parseCurrentUser(value: unknown): AuthUser | undefined {
  if (!isObject(value)) return undefined
  if (!validSubjectId(value.id) ||
    typeof value.nickname !== 'string' || !Array.isArray(value.roles) ||
    !value.roles.every(role => role === 'student' || role === 'coach') ||
    ('avatarUrl' in value && value.avatarUrl !== null && typeof value.avatarUrl !== 'string') ||
    ('ageVerified' in value && typeof value.ageVerified !== 'boolean') ||
    ('status' in value && value.status !== 'active' && value.status !== 'disabled')) return undefined
  if (value.status === 'disabled') return undefined
  return Object.freeze({
    id: value.id,
    nickname: value.nickname,
    roles: Object.freeze([...value.roles]) as readonly ('student' | 'coach')[],
    ...('avatarUrl' in value ? { avatarUrl: value.avatarUrl as string | null } : {}),
    ...('ageVerified' in value ? { ageVerified: value.ageVerified as boolean } : {}),
    ...('status' in value ? { status: 'active' as const } : {}),
  })
}

/**
 * Builds the authenticated `/me` verifier used by AuthSessionCoordinator.
 * Transport errors and every non-401 response fail closed as unavailable; only
 * a no-store server 401 marks the saved credential revoked.
 */
export function createCurrentUserVerifier(readCurrentUser: CurrentUserReadPort): PersistedCredentialVerifier {
  if (typeof readCurrentUser !== 'function') throw new Error('current-user transport unavailable')
  return async (accessToken: string, expectedSubjectId: string): Promise<PersistedCredentialCheck> => {
    if (typeof accessToken !== 'string' || accessToken.length < 1 || accessToken.length > 8192 ||
      !/^[\x21-\x7e]+$/.test(accessToken) || !validSubjectId(expectedSubjectId)) return { status: 'unavailable' }
    try {
      const response = await readCurrentUser(accessToken)
      if (!isObject(response) || !Number.isInteger(response.status) || response.status < 100 || response.status > 599 ||
        !noStore(response.headers) || !isObject(response.body)) return { status: 'unavailable' }
      if (response.status === 401) {
        return isServerUnauthenticated(response.body, response.headers) ? { status: 'revoked' } : { status: 'unavailable' }
      }
      if (response.status !== 200) return { status: 'unavailable' }
      const user = parseCurrentUser(response.body)
      if (!user || user.id !== expectedSubjectId) return { status: 'unavailable' }
      return { status: 'valid', user }
    } catch {
      return { status: 'unavailable' }
    }
  }
}
