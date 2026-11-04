import Foundation

private enum Failure: Error { case assertion }
private func check(_ value: @autoclosure () throws -> Bool) throws {
    guard try value() else { throw Failure.assertion }
}

private final class FakeDraftBackend: NeraeDraftBackend {
    var values: [String: Data] = [:]
    var failAfterUpdate = false
    var corruptRead = false
    var failEnumeration = false
    var accountOverride: [String]?
    func accounts() throws -> [String] {
        if failEnumeration { throw NeraeVaultError.unavailable }
        return accountOverride ?? values.keys.sorted()
    }
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
        let composedEvent = "event-\u{00e9}001"
        let decomposedEvent = "event-e\u{0301}001"
        try check(composedEvent == decomposedEvent && Data(composedEvent.utf8) != Data(decomposedEvent.utf8))
        try expect(.invalidState) {
            _ = try store.create(key: "nerae.workout.draft.v1.101.\(composedEvent)",
                                 value: try payload("101", decomposedEvent))
        }
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

        let conditional = try payload(note: "条件保存")
        try check(try store.compareAndSet(key: key, expected: afterUnknown, value: conditional))
        try check(!(try store.compareAndSet(key: key, expected: afterUnknown, value: first)))
        try check(try store.read(key: key).value == conditional)
        try expect(.invalidState) { _ = try store.compareAndSet(key: key, expected: conditional, value: try payload("202")) }
        try check(try store.read(key: key).value == conditional)
        try check(!(try store.compareAndSet(key: "nerae.workout.draft.v1.101.event-0002",
                                            expected: try payload("101", "event-0002"),
                                            value: try payload("101", "event-0002", note: "new"))))
        backend.failAfterUpdate = true
        try expect(.unavailable) { _ = try store.compareAndSet(key: key, expected: conditional, value: afterUnknown) }
        backend.failAfterUpdate = false
        try check(try store.read(key: key).value == afterUnknown)

        // Swift String equality treats canonically equivalent Unicode strings
        // as equal; a draft comparison must compare the original UTF-8 bytes.
        let composed = try payload(note: "\u{00e9}")
        let decomposed = try payload(note: "e\u{0301}")
        try check(composed == decomposed && Data(composed.utf8) != Data(decomposed.utf8))
        try store.write(key: key, value: composed)
        try check(!(try store.compareAndSet(key: key, expected: decomposed, value: first)))
        try check(Data((try store.read(key: key)).value.utf8) == Data(composed.utf8))
        try store.write(key: key, value: afterUnknown)
        backend.corruptRead = true
        try expect(.unavailable) { _ = try store.read(key: key) }
        let beforeCorruptWrite = backend.values
        try expect(.unavailable) { try store.write(key: key, value: first) }
        try expect(.unavailable) { _ = try store.compareAndSet(key: key, expected: afterUnknown, value: first) }
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

        var casWinners = 0
        var casFailures = 0
        DispatchQueue.concurrentPerform(iterations: 64) { index in
            do {
                let next = try payload("101", "event-race01", note: "writer-\(index)")
                if try stores[index % stores.count].compareAndSet(key: raceKey, expected: raceValue, value: next) {
                    resultLock.lock(); casWinners += 1; resultLock.unlock()
                }
            } catch { resultLock.lock(); casFailures += 1; resultLock.unlock() }
        }
        try check(casWinners == 1 && casFailures == 0)

        let discoveryBackend = FakeDraftBackend()
        let discovery = NeraeProtectedDraftStore(backend: discoveryBackend)
        try check(try discovery.create(key: "nerae.workout.draft.v1.101.event-first1", value: try payload("101", "event-first1")))
        try check(try discovery.create(key: "nerae.workout.draft.v1.101.event-next01", value: try payload("101", "event-next01")))
        try check(try discovery.create(key: "nerae.workout.draft.v1.202.event-other1", value: try payload("202", "event-other1")))
        func listing(_ subject: String, _ cursor: String = "") throws -> [String: Any] {
            let json = try discovery.discover(subject: subject, after: cursor)
            guard let data = json.data(using: .utf8), let result = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw Failure.assertion }
            return result
        }
        let forFirst = try listing("101")
        try check(forFirst["eventIds"] as? [String] == ["event-first1", "event-next01"])
        try check(forFirst["hasUnclassifiedItems"] as? Bool == false)
        try check(forFirst["nextCursor"] is NSNull)
        try check(try listing("202")["eventIds"] as? [String] == ["event-other1"])
        try check(try listing("303")["eventIds"] as? [String] == [])
        try expect(.invalidState) { _ = try discovery.discover(subject: "0101", after: "") }
        try expect(.invalidState) { _ = try discovery.discover(subject: "101", after: "short") }
        let corruptAccount = String(repeating: "f", count: 64)
        discoveryBackend.values[corruptAccount] = Data("broken".utf8)
        let withCorruption = try listing("101")
        try check(withCorruption["eventIds"] as? [String] == ["event-first1", "event-next01"])
        try check(withCorruption["hasUnclassifiedItems"] as? Bool == true)
        try check(try listing("202")["hasUnclassifiedItems"] as? Bool == true)
        try check(discoveryBackend.values[corruptAccount] == Data("broken".utf8))
        discoveryBackend.values[corruptAccount] = try payload("101", "event-unmatched").data(using: .utf8)
        try check(try listing("101")["hasUnclassifiedItems"] as? Bool == true)
        discoveryBackend.failEnumeration = true
        try expect(.unavailable) { _ = try discovery.discover(subject: "101", after: "") }
        discoveryBackend.failEnumeration = false
        discoveryBackend.accountOverride = Array(repeating: corruptAccount, count: 257)
        try check(try listing("101")["hasUnclassifiedItems"] as? Bool == true)
        let oversizedBackend = FakeDraftBackend()
        let large = Data(repeating: 0xff, count: 64 * 1024)
        for index in 0..<129 { oversizedBackend.values[String(format: "%064x", index)] = large }
        let largeResult = try NeraeProtectedDraftStore(backend: oversizedBackend).discover(subject: "101", after: "")
        let largeObject = try JSONSerialization.jsonObject(with: Data(largeResult.utf8)) as? [String: Any]
        try check(largeObject?["hasUnclassifiedItems"] as? Bool == true)

        let pagesBackend = FakeDraftBackend()
        let pagesStore = NeraeProtectedDraftStore(backend: pagesBackend)
        for index in 0..<280 {
            let event = String(format: "event-page-%04d", index)
            try check(try pagesStore.create(key: "nerae.workout.draft.v1.101.\(event)", value: try payload("101", event)))
        }
        func page(_ cursor: String) throws -> [String: Any] {
            let json = try pagesStore.discover(subject: "101", after: cursor)
            guard let value = try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any] else { throw Failure.assertion }
            return value
        }
        let page1 = try page("")
        guard let ids1 = page1["eventIds"] as? [String], let cursor1 = page1["nextCursor"] as? String else { throw Failure.assertion }
        let page2 = try page(cursor1)
        guard let ids2 = page2["eventIds"] as? [String], let cursor2 = page2["nextCursor"] as? String else { throw Failure.assertion }
        let page3 = try page(cursor2)
        guard let ids3 = page3["eventIds"] as? [String] else { throw Failure.assertion }
        try check(ids1.count == 100 && ids2.count == 100 && ids3.count == 80 && page3["nextCursor"] is NSNull)
        try check(Set(ids1 + ids2 + ids3).count == 280)
        print("PASS: synthetic draft isolation, in-process races, paged discovery and uncertain write preservation")
    }
}
