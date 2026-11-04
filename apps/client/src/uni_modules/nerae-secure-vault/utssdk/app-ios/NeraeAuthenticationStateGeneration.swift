import Foundation

/// Immutable in-process authentication-state observation for a replay flight.
/// Its value may contain a bearer credential and is deliberately redacted.
struct NeraeAuthenticationStateSnapshot: CustomStringConvertible, CustomDebugStringConvertible {
    let processNonce: UUID
    let gateNonce: UUID
    let generation: UInt64
    let found: Bool
    let encodedState: String

    var description: String { "NeraeAuthenticationStateSnapshot{redacted}" }
    var debugDescription: String { description }
}

/// Candidate serialization and generation boundary for the app's one shared
/// authentication Keychain item. All app-owned reads, writes, and clears must
/// use one of these gates; direct calls to the wrapped vault bypass the clock.
/// This is a stale-flight check only, not authentication proof or settlement
/// authorization.
final class NeraeAuthenticationStateGenerationGate {
    private enum ProcessState {
        static let lock = NSRecursiveLock()
        static let nonce = UUID()
        static var generation: UInt64 = 0
        static var exhausted = false

        static func bump() throws {
            guard !exhausted, generation < UInt64.max else {
                exhausted = true
                throw NeraeVaultError.unavailable
            }
            generation += 1
        }
    }

    private let vault: NeraeCredentialVault
    private let gateNonce = UUID()

    init(vault: NeraeCredentialVault) {
        self.vault = vault
    }

    /// Captures both Keychain contents and the process generation under the
    /// same lock used by every gate mutation.
    func snapshot() throws -> NeraeAuthenticationStateSnapshot {
        ProcessState.lock.lock()
        defer { ProcessState.lock.unlock() }
        guard !ProcessState.exhausted else { throw NeraeVaultError.unavailable }
        let read = try vault.read()
        return NeraeAuthenticationStateSnapshot(processNonce: ProcessState.nonce,
                                                gateNonce: gateNonce,
                                                generation: ProcessState.generation,
                                                found: read.found,
                                                encodedState: read.value)
    }

    /// Re-reads and checks under the shared lock. The generation detects app
    /// writes including write-then-throw; the bytes detect a currently changed
    /// value written by an uninstrumented path.
    func isCurrent(_ snapshot: NeraeAuthenticationStateSnapshot) -> Bool {
        ProcessState.lock.lock()
        defer { ProcessState.lock.unlock() }
        guard !ProcessState.exhausted,
              snapshot.processNonce == ProcessState.nonce,
              snapshot.gateNonce == gateNonce,
              snapshot.generation == ProcessState.generation,
              let latest = try? vault.read() else { return false }
        return latest.found == snapshot.found && latest.value.utf8.elementsEqual(snapshot.encodedState.utf8)
    }

    /// Advance before entering Keychain. Even if the backend persists and then
    /// reports an error, no older snapshot can pass its postflight check.
    func write(_ value: String) throws {
        ProcessState.lock.lock()
        defer { ProcessState.lock.unlock() }
        try ProcessState.bump()
        try vault.write(value)
    }

    /// CAS is also a possible vault mutation path. A failed comparison may
    /// conservatively invalidate a snapshot, which is safe for replay recovery.
    func compareAndSet(expectedFound: Bool, expectedValue: String, value: String) throws {
        ProcessState.lock.lock()
        defer { ProcessState.lock.unlock() }
        try ProcessState.bump()
        try vault.compareAndSet(expectedFound: expectedFound, expectedValue: expectedValue, value: value)
    }

    /// Clear attempts also invalidate old snapshots, including uncertain clears.
    func clear() throws {
        ProcessState.lock.lock()
        defer { ProcessState.lock.unlock() }
        try ProcessState.bump()
        try vault.clear()
    }
}
