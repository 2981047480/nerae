import type { ApprovalDecisionResult, ApprovalOwnerApi, PendingApprovalDecision, PendingDeviceApproval } from './approvalOwnerApi'
import type { AuthSessionCoordinator } from './sessionCoordinator'

export interface DeviceApprovalState {
  readonly status: 'loading' | 'ready' | 'working' | 'retry' | 'done' | 'unavailable' | 'session_changed'
  readonly items: readonly PendingDeviceApproval[]
  readonly message: string
  readonly pending?: PendingApprovalDecision
}
type SessionPort = Pick<AuthSessionCoordinator, 'current' | 'isCurrent' | 'captureInstallGuard' | 'subscribeInstallGuardInvalidation'>
type ApprovalPort = Pick<ApprovalOwnerApi, 'list' | 'decide' | 'retryDecision' | 'pendingDecision' | 'stopWaiting'>
type Listener = (state: DeviceApprovalState) => void

function state(status: DeviceApprovalState['status'], message: string, items: readonly PendingDeviceApproval[] = [],
  pending?: PendingApprovalDecision): DeviceApprovalState {
  return Object.freeze({ status, message, items: Object.freeze([...items]), ...(pending ? { pending } : {}) })
}

/** Presentation coordinator only; it cannot approve without an explicit action. */
export class DeviceApprovalController {
  #session: SessionPort
  #api: ApprovalPort
  #state: DeviceApprovalState = state('loading', '正在检查待确认的登录')
  #listeners = new Set<Listener>()
  #unwatch: (() => void) | null = null
  #busy = false
  #disposed = false
  #epoch = 0

  constructor(session: SessionPort, api: ApprovalPort) {
    if (!session || typeof session.current !== 'function' || typeof session.isCurrent !== 'function' ||
      typeof session.captureInstallGuard !== 'function' || typeof session.subscribeInstallGuardInvalidation !== 'function' ||
      !api || typeof api.list !== 'function' || typeof api.decide !== 'function' ||
      typeof api.retryDecision !== 'function' || typeof api.pendingDecision !== 'function' ||
      typeof api.stopWaiting !== 'function') throw new Error('device confirmation unavailable')
    this.#session = session
    this.#api = api
  }

  get state(): DeviceApprovalState { return this.#state }
  subscribe(listener: Listener): () => void {
    if (this.#disposed) return () => undefined
    this.#listeners.add(listener)
    listener(this.#state)
    return () => { this.#listeners.delete(listener) }
  }

  async start(): Promise<void> {
    if (this.#disposed || this.#unwatch || this.#busy) return
    const current = this.#session.current()
    const guard = this.#session.captureInstallGuard()
    if (!current || !guard || !this.#session.isCurrent(current.subjectId, current.generation)) {
      this.#set(state('session_changed', '登录状态已变化，请重新登录。'))
      return
    }
    const epoch = this.#epoch
    try {
      const unwatch = this.#session.subscribeInstallGuardInvalidation(guard, () => {
        if (this.#disposed) return
        this.#epoch++
        this.#set(state('session_changed', '登录状态已变化，请重新登录。'))
      })
      if (!this.#active(epoch)) { unwatch(); return }
      this.#unwatch = unwatch
    } catch { this.#set(state('session_changed', '登录状态已变化，请重新登录。')); return }
    await this.refresh()
  }

  async refresh(): Promise<void> {
    if (this.#disposed || !this.#unwatch || this.#busy || this.#state.status === 'session_changed') return
    const epoch = this.#epoch
    this.#busy = true
    this.#set(state('loading', '正在检查待确认的登录'))
    try {
      const result = await this.#api.list()
      if (!this.#active(epoch)) return
      if (result.status === 'ready') {
        this.#set(state('ready', result.items.length ? '请与新设备显示的匹配码核对。' : '目前没有待确认的登录。', result.items))
      } else if (result.status === 'decision_pending') {
        this.#showRetry('上次确认结果尚未确定，请重试检查。')
      } else if (result.status === 'session_changed' || result.status === 'not_authenticated') {
        this.#set(state('session_changed', '登录状态已变化，请重新登录。'))
      } else this.#set(state('unavailable', '暂时无法获取待确认登录，请稍后重试。'))
    } catch { if (this.#active(epoch)) this.#set(state('unavailable', '暂时无法获取待确认登录，请稍后重试。')) }
    finally { this.#busy = false }
  }

  async decide(item: PendingDeviceApproval, decision: 'approve' | 'deny', confirmed: unknown = false): Promise<void> {
    if (this.#disposed || this.#busy || this.#state.status !== 'ready' || !this.#state.items.includes(item) ||
      (decision === 'approve' && confirmed !== true)) return
    const epoch = this.#epoch
    this.#busy = true
    this.#set(state('working', '正在提交确认'))
    try { this.#handle(await this.#api.decide(item, decision, confirmed), epoch) }
    catch { if (this.#active(epoch)) this.#showRetry('确认结果尚未确定，请重试检查。') }
    finally { this.#busy = false }
  }

  async retry(): Promise<void> {
    if (this.#disposed || this.#busy || this.#state.status !== 'retry') return
    const epoch = this.#epoch
    this.#busy = true
    this.#set(state('working', '正在检查确认结果'))
    try { this.#handle(await this.#api.retryDecision(), epoch) }
    catch { if (this.#active(epoch)) this.#showRetry('确认结果尚未确定，请重试检查。') }
    finally { this.#busy = false }
  }

  stopWaiting(acknowledged: unknown): void {
    if (this.#disposed || this.#busy || this.#state.status !== 'retry') return
    if (this.#api.stopWaiting(acknowledged) === 'unresolved') {
      this.#set(state('unavailable', '之前的操作结果仍未确认，且可能稍后完成。请刷新列表并在新设备检查登录状态。'))
    }
  }

  dispose(): void {
    if (this.#disposed) return
    this.#disposed = true
    this.#epoch++
    try { this.#unwatch?.() } catch { /* best-effort cleanup */ }
    this.#unwatch = null
    this.#listeners.clear()
    this.#state = state('session_changed', '')
  }

  #active(epoch: number): boolean { return !this.#disposed && this.#epoch === epoch }
  #handle(result: ApprovalDecisionResult, epoch: number): void {
    if (!this.#active(epoch)) return
    if (result.status === 'approved' || result.status === 'denied') {
      this.#set(state('done', result.status === 'approved' ? '已允许新设备登录。' : '已拒绝新设备登录。'))
    } else if (result.status === 'session_changed' || result.status === 'not_authenticated') {
      this.#set(state('session_changed', '登录状态已变化，请重新登录。'))
    } else if (result.status === 'retry' || result.status === 'rate_limited' || result.status === 'decision_pending' || result.status === 'needs_review') {
      this.#showRetry(result.status === 'rate_limited' ? '请求过于频繁，请稍后重试。'
        : result.status === 'needs_review' ? '当前无法确认结果，请检查登录状态后重试，或停止等待并重新查看。'
          : '确认结果尚未确定，请重试检查。')
    } else this.#set(state('unavailable', '当前无法完成确认，请重新检查。'))
  }
  #showRetry(message: string): void {
    const pending = this.#api.pendingDecision()
    this.#set(pending ? state('retry', message, [], pending) : state('unavailable', '当前无法继续确认，请重新检查。'))
  }
  #set(next: DeviceApprovalState): void {
    if (this.#disposed) return
    this.#state = next
    for (const listener of [...this.#listeners]) { try { listener(next) } catch { /* isolate UI observers */ } }
  }
}
