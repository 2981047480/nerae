import {
  clearAuthenticationState,
  readAuthenticationState,
  writeAuthenticationState,
} from '@/uni_modules/nerae-secure-vault'
import { createCurrentUserVerifier, type CurrentUserReadPort } from '../../features/auth/meVerifier'
import { AuthSessionCoordinator } from '../../features/auth/sessionCoordinator'

let coordinator: AuthSessionCoordinator | undefined
let boundReadCurrentUser: CurrentUserReadPort | undefined

/** Bind one coordinator per App process to the OS-backed vault. The caller
 * supplies the /me port from the app's trusted service configuration. */
export function createAppSessionCoordinator(readCurrentUser: CurrentUserReadPort): AuthSessionCoordinator {
  if (coordinator) {
    if (boundReadCurrentUser !== readCurrentUser) throw new Error('App session is already configured')
    return coordinator
  }
  const created = new AuthSessionCoordinator(
    { readAuthenticationState, writeAuthenticationState, clearAuthenticationState },
    createCurrentUserVerifier(readCurrentUser),
  )
  boundReadCurrentUser = readCurrentUser
  coordinator = created
  return created
}
