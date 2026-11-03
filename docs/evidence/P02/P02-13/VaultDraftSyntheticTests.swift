import Foundation

private enum Failure: Error { case assertion }
private func check(_ value: @autoclosure () throws -> Bool) throws {
    guard try value() else { throw Failure.assertion }
}

private final class FakeDraftBackend: NeraeDraftBackend {
    var values: [String: Data] = [:]
    var failAfterUpdate = false
    var corruptRead = false
    func read(account: String) throws -> Data? {
        if corruptRead { return Data("broken".utf8) }
        return values[account]
    }
    func create(account: String, value: Data) throws -> Bool {
        if values[account] != nil { return false }
        values[account] = value
        return true
    }
    func update(account: String, value: Data) throws {
        guard values[account] != nil else { throw NeraeVaultError.conflict }
        values[account] = value
        if failAfterUpdate { throw NeraeVaultError.unavailable }
    }
}

private func payload(_ subject: String = "101", _ event: String = "event-0001", note: String = "") throws -> String {
    let object: [String: Any] = ["schema": 1, "subjectId": subject, "draft": ["clientEventId": event, "note": note]]
    let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    guard let value = String(data: data, encoding: .utf8) else { throw Failure.assertion }
    return value
}

private func expect(_ expected: NeraeVaultError, _ body: () throws -> Void) throws {
    do { try body(); throw Failure.assertion }
    catch let actual as NeraeVaultError {
        switch (expected, actual) {
        case (.conflict, .conflict), (.invalidState, .invalidState), (.unavailable, .unavailable): return
        default: throw Failure.assertion
        }
    }
}

@main struct Main {
    static func main() throws {
        let backend = FakeDraftBackend()
        let store = NeraeProtectedDraftStore(backend: backend)
        let key = "nerae.workout.draft.v1.101.event-0001"
        let otherKey = "nerae.workout.draft.v1.202.event-0001"
        let first = try payload()
        try check(!(try store.read(key: key)).found)
        try check(try store.create(key: key, value: first))
        try check(!(try store.create(key: key, value: try payload(note: "other"))))
        try check(try store.read(key: key).value == first)
        try check(try store.create(key: otherKey, value: try payload("202")))
        try expect(.invalidState) { _ = try store.create(key: key, value: try payload("202")) }
        try expect(.invalidState) { _ = try store.create(key: key, value: try payload("101", "event-0002")) }
        try expect(.conflict) { try store.write(key: "nerae.workout.draft.v1.101.event-0002", value: try payload("101", "event-0002")) }
        try check(backend.values.count == 2)

        let baseline = try payload(note: "")
        let exact = try payload(note: String(repeating: "a", count: 64 * 1024 - baseline.utf8.count))
        try check(exact.utf8.count == 64 * 1024)
        try store.write(key: key, value: exact)
        try check(try store.read(key: key).value == exact)
        let tooLarge = try payload(note: String(repeating: "a", count: 64 * 1024 - baseline.utf8.count + 1))
        try expect(.invalidState) { try store.write(key: key, value: tooLarge) }
        try check(try store.read(key: key).value == exact)

        let afterUnknown = try payload(note: "最新输入")
        backend.failAfterUpdate = true
        try expect(.unavailable) { try store.write(key: key, value: afterUnknown) }
        backend.failAfterUpdate = false
        try check(try store.read(key: key).value == afterUnknown)
        backend.corruptRead = true
        try expect(.unavailable) { _ = try store.read(key: key) }
        let beforeCorruptWrite = backend.values
        try expect(.unavailable) { try store.write(key: key, value: first) }
        try check(backend.values == beforeCorruptWrite)
        backend.corruptRead = false
        try check(try store.read(key: key).value == afterUnknown)

        // Different store instances share one process lock even when callers
        // race. This says nothing about Keychain writers in other processes.
        let racingBackend = FakeDraftBackend()
        let stores = [NeraeProtectedDraftStore(backend: racingBackend), NeraeProtectedDraftStore(backend: racingBackend)]
        let raceKey = "nerae.workout.draft.v1.101.event-race01"
        let raceValue = try payload("101", "event-race01")
        let resultLock = NSLock()
        var winners = 0
        var failures = 0
        DispatchQueue.concurrentPerform(iterations: 64) { index in
            do {
                let created = try stores[index % stores.count].create(key: raceKey, value: raceValue)
                if created { resultLock.lock(); winners += 1; resultLock.unlock() }
            } catch { resultLock.lock(); failures += 1; resultLock.unlock() }
        }
        try check(winners == 1 && failures == 0 && racingBackend.values.count == 1)
        try check(try stores[1].read(key: raceKey).value == raceValue)
        print("PASS: synthetic per-draft isolation, in-process create races, bounds and uncertain write preservation")
    }
}
