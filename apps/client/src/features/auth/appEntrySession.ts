import type { AuthSessionCoordinator, PublicSession, SessionInstallGuard } from './sessionCoordinator'

/** Keeps the root screen bound to the same verified adult session as login. */
export class AppEntrySession {
  readonly #session: AuthSessionCoordinator
  readonly #onInvalidated: () => void
  #stop: (() => void) | null = null
  #disposed = false

  constructor(session: AuthSessionCoordinator, onInvalidated: () => void) {
    this.#session = session
    this.#onInvalidated = onInvalidated
  }

  accept(candidate: PublicSession): boolean {
    if (this.#disposed || !candidate || candidate.status !== 'authenticated') return false
    let guard: SessionInstallGuard | null = null
    let stop: (() => void) | null = null
    try {
      const current = this.#session.snapshot()
      if (!current || current.subjectId !== candidate.subjectId || current.generation !== candidate.generation ||
        current.user.ageVerified !== true || !this.#session.isCurrent(current.subjectId, current.generation)) return false
      guard = this.#session.captureInstallGuard()
      if (!guard) return false
      stop = this.#session.subscribeInstallGuardInvalidation(guard, () => {
        this.#stop = null
        if (!this.#disposed) this.#onInvalidated()
      })
      if (!this.#session.isInstallGuardCurrent(guard) || !this.#session.isCurrent(current.subjectId, current.generation)) {
        stop()
        return false
      }
    } catch {
      stop?.()
      return false
    }
    this.clear()
    this.#stop = stop
    return true
  }

  /** Stop observing before an explicit fresh session restore begins. */
  clear(): void {
    const stop = this.#stop
    this.#stop = null
    try { stop?.() } catch { /* best-effort unsubscribe */ }
  }

  dispose(): void {
    this.#disposed = true
    this.clear()
  }
}
