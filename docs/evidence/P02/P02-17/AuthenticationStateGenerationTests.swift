import Foundation

private enum BackendFailure: Error, Equatable { case before, after }

private final class SyntheticCredentialBackend: NeraeVaultBackend {
    private let lock = NSLock()
    private var stored: Data?
    private var nextWriteFailure: BackendFailure?
    private var nextClearFailure: BackendFailure?
    private var pauseNextRead = false
    let readEntered = DispatchSemaphore(value: 0)
    let releaseRead = DispatchSemaphore(value: 0)

    init(_ initial: String?) { stored = initial?.data(using: .utf8) }

    func read() throws -> Data? {
        lock.lock()
        let value = stored
        let shouldPause = pauseNextRead
        pauseNextRead = false
        lock.unlock()
        if shouldPause {
            readEntered.signal()
            _ = releaseRead.wait(timeout: .now() + 3)
        }
        return value
    }

    func write(_ value: Data) throws {
        lock.lock()
        let failure = nextWriteFailure
        nextWriteFailure = nil
        if failure == .before { lock.unlock(); throw BackendFailure.before }
        stored = value
        lock.unlock()
        if failure == .after { throw BackendFailure.after }
    }

    func clear() throws {
        lock.lock()
        let failure = nextClearFailure
        nextClearFailure = nil
        if failure == .before { lock.unlock(); throw BackendFailure.before }
        stored = nil
        lock.unlock()
        if failure == .after { throw BackendFailure.after }
    }

    func failNextWrite(_ failure: BackendFailure) { lock.lock(); nextWriteFailure = failure; lock.unlock() }
    func failNextClear(_ failure: BackendFailure) { lock.lock(); nextClearFailure = failure; lock.unlock() }
    func pauseNextBackendRead() { lock.lock(); pauseNextRead = true; lock.unlock() }
    func replaceOutsideGate(_ string: String?) { lock.lock(); stored = string?.data(using: .utf8); lock.unlock() }
}

private final class ResultBox<Value> {
    private let lock = NSLock()
    private var value: Value?
    func put(_ value: Value) { lock.lock(); self.value = value; lock.unlock() }
    func get() -> Value? { lock.lock(); defer { lock.unlock() }; return value }
}

private func check(_ name: String, _ condition: @autoclosure () -> Bool) {
    guard condition() else { fputs("FAIL \(name)\n", stderr); exit(1) }
    print("PASS \(name)")
}

@main struct AuthenticationStateGenerationTests {
    static func main() throws {
        let accountA = #"{"schema":1,"subjectId":"100","accessToken":"token-A"}"#
        let accountB = #"{"schema":1,"subjectId":"200","accessToken":"token-B"}"#
        let backend = SyntheticCredentialBackend(accountA)
        let gate1 = NeraeAuthenticationStateGenerationGate(vault: NeraeCredentialVault(backend: backend))
        let gate2 = NeraeAuthenticationStateGenerationGate(vault: NeraeCredentialVault(backend: backend))

        let firstA = try gate1.snapshot()
        try gate1.write(accountB)
        try gate2.write(accountA)
        let afterABA = try gate1.snapshot()
        check("A to B to A invalidates old replay snapshot despite equal bytes", !gate1.isCurrent(firstA) && firstA.encodedState.utf8.elementsEqual(afterABA.encodedState.utf8))
        check("fresh reread is current in its gate instance", gate1.isCurrent(afterABA))
        let gate2Snapshot = try gate2.snapshot()
        check("each gate instance can validate its own fresh snapshot", gate2.isCurrent(gate2Snapshot))
        let duplicateValueBackend = SyntheticCredentialBackend(accountA)
        let unrelatedGate = NeraeAuthenticationStateGenerationGate(vault: NeraeCredentialVault(backend: duplicateValueBackend))
        check("equal bytes in a different vault do not validate another gate snapshot", !unrelatedGate.isCurrent(gate2Snapshot))

        let beforeCAS = try gate1.snapshot()
        try gate2.compareAndSet(expectedFound: true, expectedValue: accountA, value: accountB)
        check("successful CAS invalidates old snapshot", !gate1.isCurrent(beforeCAS))
        let beforeCASConflict = try gate1.snapshot()
        do { try gate2.compareAndSet(expectedFound: true, expectedValue: accountA, value: accountA); check("CAS conflict reported", false) } catch { }
        check("failed CAS conservatively invalidates old snapshot", !gate1.isCurrent(beforeCASConflict))

        try gate1.write(accountA)
        let beforeFailedWrite = try gate1.snapshot()
        backend.failNextWrite(.before)
        do { try gate2.write(accountB); check("prewrite failure reported", false) } catch { }
        check("write failure before backend apply invalidates old snapshot", !gate1.isCurrent(beforeFailedWrite))
        let afterPrewriteFailure = try gate1.snapshot()
        check("prewrite failure leaves stored bytes unchanged", afterPrewriteFailure.encodedState.utf8.elementsEqual(accountA.utf8))

        let beforeUncertainWrite = try gate1.snapshot()
        backend.failNextWrite(.after)
        do { try gate2.write(accountB); check("postwrite failure reported", false) } catch { }
        check("write-then-throw invalidates old snapshot", !gate1.isCurrent(beforeUncertainWrite))
        let afterUncertainWrite = try gate1.snapshot()
        check("write-then-throw can have persisted new account", afterUncertainWrite.encodedState.utf8.elementsEqual(accountB.utf8))

        try gate1.write(accountA)
        let beforeFailedClear = try gate1.snapshot()
        backend.failNextClear(.before)
        do { try gate2.clear(); check("preclear failure reported", false) } catch { }
        check("clear failure before backend apply invalidates old snapshot", !gate1.isCurrent(beforeFailedClear))
        let afterPreclearFailure = try gate1.snapshot()
        check("preclear failure leaves stored bytes unchanged", afterPreclearFailure.encodedState.utf8.elementsEqual(accountA.utf8))

        let beforeUncertainClear = try gate1.snapshot()
        backend.failNextClear(.after)
        do { try gate2.clear(); check("clear-after-apply failure reported", false) } catch { }
        check("clear-then-throw invalidates old snapshot", !gate1.isCurrent(beforeUncertainClear))
        let afterUncertainClear = try gate1.snapshot()
        check("clear-then-throw may leave vault empty", !afterUncertainClear.found)

        try gate1.write(accountA)
        let beforeConcurrentRead = try gate1.snapshot()
        backend.pauseNextBackendRead()
        let snapshotBox = ResultBox<Result<NeraeAuthenticationStateSnapshot, Error>>()
        let snapshotDone = DispatchSemaphore(value: 0)
        DispatchQueue.global().async {
            snapshotBox.put(Result { try gate1.snapshot() })
            snapshotDone.signal()
        }
        guard backend.readEntered.wait(timeout: .now() + 2) == .success else { fatalError("snapshot read did not enter backend") }
        let writeDone = DispatchSemaphore(value: 0)
        DispatchQueue.global().async {
            try? gate2.write(accountB)
            writeDone.signal()
        }
        check("concurrent write waits while snapshot read and generation capture are locked", writeDone.wait(timeout: .now() + 0.1) == .timedOut)
        backend.releaseRead.signal()
        guard snapshotDone.wait(timeout: .now() + 2) == .success,
              writeDone.wait(timeout: .now() + 2) == .success,
              let captured = try? snapshotBox.get()?.get() else { fatalError("concurrent checks timed out") }
        check("read result and generation form one stale-detecting snapshot", captured.encodedState.utf8.elementsEqual(accountA.utf8) && !gate1.isCurrent(captured))
        let afterConcurrentRead = try gate1.snapshot()
        check("two gate instances share one process generation", afterConcurrentRead.generation >= beforeConcurrentRead.generation + 1)

        let beforeConcurrentWrites = try gate1.snapshot()
        let writeGroup = DispatchGroup()
        writeGroup.enter(); writeGroup.enter()
        DispatchQueue.global().async { try? gate1.write(accountA); writeGroup.leave() }
        DispatchQueue.global().async { try? gate2.write(accountB); writeGroup.leave() }
        check("concurrent writes complete", writeGroup.wait(timeout: .now() + 2) == .success)
        let afterConcurrentWrites = try gate1.snapshot()
        check("each serialized concurrent write advances generation", afterConcurrentWrites.generation == beforeConcurrentWrites.generation + 2 && !gate1.isCurrent(beforeConcurrentWrites))

        try gate1.write(accountA)
        let externallyObservedA = try gate1.snapshot()
        backend.replaceOutsideGate(accountA)
        backend.replaceOutsideGate(accountB)
        backend.replaceOutsideGate(accountA)
        check("demonstrated boundary: uninstrumented external A-B-A is not observable", gate1.isCurrent(externallyObservedA))
        let externallyDifferent = try gate1.snapshot()
        backend.replaceOutsideGate(accountB)
        check("postflight byte reread detects a currently different external value", !gate1.isCurrent(externallyDifferent))

        let restartOldSnapshot = try gate1.snapshot()
        let foreignProcessSnapshot = NeraeAuthenticationStateSnapshot(processNonce: UUID(), gateNonce: restartOldSnapshot.gateNonce,
                                                                       generation: restartOldSnapshot.generation,
                                                                       found: restartOldSnapshot.found,
                                                                       encodedState: restartOldSnapshot.encodedState)
        check("snapshot from another process nonce is rejected", !gate1.isCurrent(foreignProcessSnapshot))
        print("PASS generation result is only an in-process stale-flight signal; no journal settlement")
    }
}
