import Foundation
import CryptoKit

private enum Failure: Error { case assertion }
private func check(_ value: @autoclosure () throws -> Bool) throws {
    guard try value() else { throw Failure.assertion }
}

private final class FakeDraftBackend: NeraeDraftBackend {
    var values: [String: Data] = [:]
    var failAfterUpdate = false
    var failBeforeUpdate = false
    var failAfterCreate = false
    var failReadAfterUpdate = false
    var failNextRead = false
    var corruptRead = false
    var failEnumeration = false
    var accountOverride: [String]?
    func accounts() throws -> [String] {
        if failEnumeration { throw NeraeVaultError.unavailable }
        return accountOverride ?? values.keys.sorted()
    }
    func read(account: String) throws -> Data? {
        if failNextRead { failNextRead = false; throw NeraeVaultError.unavailable }
        if corruptRead { return Data("broken".utf8) }
        return values[account]
    }
    func create(account: String, value: Data) throws -> Bool {
        if values[account] != nil { return false }
        values[account] = value
        if failAfterCreate { throw NeraeVaultError.unavailable }
        return true
    }
    func update(account: String, value: Data) throws {
        guard values[account] != nil else { throw NeraeVaultError.conflict }
        if failBeforeUpdate { throw NeraeVaultError.unavailable }
        values[account] = value
        if failReadAfterUpdate { failNextRead = true }
        if failAfterUpdate { throw NeraeVaultError.unavailable }
    }
}

private func payload(_ subject: String = "101", _ event: String = "event-0001", note: String = "") throws -> String {
    let object: [String: Any] = ["schema": 1, "subjectId": subject, "draft": ["clientEventId": event, "note": note]]
    let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    guard let value = String(data: data, encoding: .utf8) else { throw Failure.assertion }
    return value
}

private func payloadWithSyncPending(_ subject: String = "101", _ event: String = "event-0001",
                                   note: String = "", syncPending: Any?) throws -> String {
    var draft: [String: Any] = ["clientEventId": event, "note": note]
    if let syncPending { draft["syncPending"] = syncPending }
    let object: [String: Any] = ["schema": 1, "subjectId": subject, "draft": draft]
    let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    guard let value = String(data: data, encoding: .utf8) else { throw Failure.assertion }
    return value
}

private func syncPendingBytes(_ json: String) throws -> Data? {
    guard let object = try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any],
          let draft = object["draft"] as? [String: Any] else { throw Failure.assertion }
    guard let value = draft["syncPending"] else { return nil }
    guard let string = value as? String else { throw Failure.assertion }
    return Data(string.utf8)
}

private func account(for key: String) -> String {
    SHA256.hash(data: Data(key.utf8)).map { String(format: "%02x", $0) }.joined()
}

private func versionedEnvelope(_ value: Data, revision: String) throws -> Data {
    try PropertyListSerialization.data(fromPropertyList: [
        "format": "com.nerae.training-draft.versioned.v2",
        "payload": value,
        "revision": revision
    ], format: .binary, options: 0)
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

        // Legacy records remain byte-for-byte untouched by reads, then migrate
        // in place on the first successful versioned mutation.
        let legacyBackend = FakeDraftBackend()
        let legacyStore = NeraeProtectedDraftStore(backend: legacyBackend)
        let legacyKey = "nerae.workout.draft.v1.101.event-legacy1"
        let legacyPayload = try payload("101", "event-legacy1", note: "e\u{0301}")
        let legacyBytes = Data(legacyPayload.utf8)
        legacyBackend.values[account(for: legacyKey)] = legacyBytes
        let legacyRead = try legacyStore.readVersioned(key: legacyKey)
        try check(legacyRead.found && legacyRead.revision == "0" && Data(legacyRead.value.utf8) == legacyBytes)
        try check(legacyBackend.values[account(for: legacyKey)] == legacyBytes)
        let upgraded = try payload("101", "event-legacy1", note: "升级")
        try check(try legacyStore.compareAndSetVersioned(key: legacyKey, expectedRevision: "0",
                                                         expectedFound: true, expectedValue: legacyPayload,
                                                         value: upgraded))
        let upgradedRead = try legacyStore.readVersioned(key: legacyKey)
        try check(upgradedRead.revision == "1" && Data(upgradedRead.value.utf8) == Data(upgraded.utf8))
        try check(legacyBackend.values[account(for: legacyKey)]?.starts(with: Data("bplist00".utf8)) == true)

        let compatibilityLegacyKey = "nerae.workout.draft.v1.101.event-legacy3"
        let compatibilityOld = try payload("101", "event-legacy3", note: "旧调用")
        let compatibilityNew = try payload("101", "event-legacy3", note: "升级写入")
        legacyBackend.values[account(for: compatibilityLegacyKey)] = Data(compatibilityOld.utf8)
        try check(try legacyStore.compareAndSet(key: compatibilityLegacyKey,
                                                expected: compatibilityOld, value: compatibilityNew))
        let compatibilityRead = try legacyStore.readVersioned(key: compatibilityLegacyKey)
        try check(compatibilityRead.revision == "1" && Data(compatibilityRead.value.utf8) == Data(compatibilityNew.utf8))

        // Older drafts may contain unrelated duplicate keys and deep unknown
        // fields. The marker check must not make those drafts uneditable.
        let deepKey = "nerae.workout.draft.v1.101.event-deep-old"
        let deepValue = #"{"schema":1,"subjectId":"101","draft":{"clientEventId":"event-deep-old","note":"old","note":"new","extra":"# +
            String(repeating: "[", count: 40) + "1" + String(repeating: "]", count: 40) + "}}"
        legacyBackend.values[account(for: deepKey)] = Data(deepValue.utf8)
        try check(try legacyStore.readVersioned(key: deepKey).value == deepValue)
        try legacyStore.write(key: deepKey, value: deepValue)
        try check(try legacyStore.readVersioned(key: deepKey).value == deepValue)
        let manyKey = "nerae.workout.draft.v1.101.event-many-old"
        let manyValue = #"{"schema":1,"subjectId":"101","draft":{"clientEventId":"event-many-old","extra":["# +
            Array(repeating: "0", count: 13_000).joined(separator: ",") + "]}}"
        legacyBackend.values[account(for: manyKey)] = Data(manyValue.utf8)
        try legacyStore.write(key: manyKey, value: manyValue)
        try check(try legacyStore.readVersioned(key: manyKey).value == manyValue)

        // A versioned write must not introduce an invalid marker that a
        // later recovery cannot distinguish from a valid pending operation.
        let invalidMarkerKey = "nerae.workout.draft.v1.101.event-invalid1"
        let markerFree = try payload("101", "event-invalid1")
        try check(try legacyStore.create(key: invalidMarkerKey, value: markerFree))
        let markerFreeRead = try legacyStore.readVersioned(key: invalidMarkerKey)
        let invalidMarker = try payloadWithSyncPending("101", "event-invalid1", syncPending: NSNull())
        try expect(.invalidState) {
            _ = try legacyStore.compareAndSetVersioned(key: invalidMarkerKey, expectedRevision: markerFreeRead.revision,
                                                       expectedFound: true, expectedValue: markerFree, value: invalidMarker)
        }
        try check(try legacyStore.readVersioned(key: invalidMarkerKey).value == markerFree)
        try expect(.invalidState) { _ = try legacyStore.create(key: "nerae.workout.draft.v1.101.event-invalid2",
            value: try payloadWithSyncPending("101", "event-invalid2", syncPending: NSNull())) }

        // Legacy write APIs may edit ordinary fields, but cannot create,
        // remove, or change the protected syncPending marker. Compare marker
        // values by UTF-8 bytes, since Swift String equality normalizes Unicode.
        let pendingKey = "nerae.workout.draft.v1.101.event-pending1"
        let composedMarker = "receipt-\u{00e9}-ready"
        let decomposedMarker = "receipt-e\u{0301}-ready"
        try check(composedMarker == decomposedMarker && Data(composedMarker.utf8) != Data(decomposedMarker.utf8))
        let pendingInitial = try payloadWithSyncPending("101", "event-pending1", note: "before", syncPending: composedMarker)
        let pendingChangedNote = try payloadWithSyncPending("101", "event-pending1", note: "ordinary edit", syncPending: composedMarker)
        legacyBackend.values[account(for: pendingKey)] = Data(pendingInitial.utf8)
        try legacyStore.write(key: pendingKey, value: pendingChangedNote)
        try check(syncPendingBytes(try legacyStore.readVersioned(key: pendingKey).value) == Data(composedMarker.utf8))
        let pendingAfterWrite = try legacyStore.readVersioned(key: pendingKey).value
        let pendingCas = try payloadWithSyncPending("101", "event-pending1", note: "ordinary CAS edit", syncPending: composedMarker)
        try check(try legacyStore.compareAndSet(key: pendingKey, expected: pendingAfterWrite, value: pendingCas))
        try check(syncPendingBytes(try legacyStore.readVersioned(key: pendingKey).value) == Data(composedMarker.utf8))

        let pendingBytesBeforeRejectedChanges = legacyBackend.values[account(for: pendingKey)]!
        let pendingAfterFailure = try payloadWithSyncPending("101", "event-pending1", note: "after unknown write", syncPending: composedMarker)
        legacyBackend.failBeforeUpdate = true
        try expect(.unavailable) { try legacyStore.write(key: pendingKey, value: pendingAfterFailure) }
        legacyBackend.failBeforeUpdate = false
        try check(legacyBackend.values[account(for: pendingKey)] == pendingBytesBeforeRejectedChanges)
        legacyBackend.failAfterUpdate = true
        try expect(.unavailable) { try legacyStore.write(key: pendingKey, value: pendingAfterFailure) }
        legacyBackend.failAfterUpdate = false
        try check(Data(try legacyStore.readVersioned(key: pendingKey).value.utf8) == Data(pendingAfterFailure.utf8))
        try check(syncPendingBytes(try legacyStore.readVersioned(key: pendingKey).value) == Data(composedMarker.utf8))
        let pendingBytesAfterUnknownWrite = legacyBackend.values[account(for: pendingKey)]!
        let pendingWithoutField = try payload("101", "event-pending1", note: "delete marker")
        try expect(.invalidState) { try legacyStore.write(key: pendingKey, value: pendingWithoutField) }
        try expect(.invalidState) { _ = try legacyStore.compareAndSet(key: pendingKey, expected: pendingAfterFailure, value: pendingWithoutField) }
        let pendingChangedUnicode = try payloadWithSyncPending("101", "event-pending1", note: "replace marker", syncPending: decomposedMarker)
        try expect(.invalidState) { try legacyStore.write(key: pendingKey, value: pendingChangedUnicode) }
        try expect(.invalidState) { _ = try legacyStore.compareAndSet(key: pendingKey, expected: pendingAfterFailure, value: pendingChangedUnicode) }
        let nonStringPending = try payloadWithSyncPending("101", "event-pending1", syncPending: 7)
        try expect(.invalidState) { try legacyStore.write(key: pendingKey, value: nonStringPending) }
        try expect(.invalidState) { _ = try legacyStore.compareAndSet(key: pendingKey, expected: pendingAfterFailure, value: nonStringPending) }
        let duplicateNew = #"{"schema":1,"subjectId":"101","draft":{"clientEventId":"event-pending1","syncPending":"receipt-é-ready","syncPending":"receipt-x-ready"}}"#
        try expect(.invalidState) { try legacyStore.write(key: pendingKey, value: duplicateNew) }
        try expect(.invalidState) { _ = try legacyStore.compareAndSet(key: pendingKey, expected: pendingAfterFailure, value: duplicateNew) }
        try check(legacyBackend.values[account(for: pendingKey)] == pendingBytesAfterUnknownWrite)

        // A matching versioned CAS retains authority to settle the marker.
        let versionedPendingBeforeSettlement = try legacyStore.readVersioned(key: pendingKey)
        let settled = try payload("101", "event-pending1", note: "settled by versioned owner")
        try check(try legacyStore.compareAndSetVersioned(key: pendingKey,
                                                         expectedRevision: versionedPendingBeforeSettlement.revision,
                                                         expectedFound: true,
                                                         expectedValue: versionedPendingBeforeSettlement.value,
                                                         value: settled))
        try check(syncPendingBytes(try legacyStore.readVersioned(key: pendingKey).value) == nil)

        // An old draft with no marker remains editable through old APIs, but
        // those APIs cannot introduce a new protected marker either.
        let noMarkerKey = "nerae.workout.draft.v1.101.event-nopend1"
        let noMarkerOld = try payload("101", "event-nopend1", note: "legacy")
        legacyBackend.values[account(for: noMarkerKey)] = Data(noMarkerOld.utf8)
        let noMarkerEdit = try payload("101", "event-nopend1", note: "legacy edit")
        try legacyStore.write(key: noMarkerKey, value: noMarkerEdit)
        try check(try legacyStore.compareAndSet(key: noMarkerKey, expected: noMarkerEdit,
                                                value: try payload("101", "event-nopend1", note: "legacy CAS")))
        let noMarkerCurrent = try legacyStore.readVersioned(key: noMarkerKey).value
        let introducedMarker = try payloadWithSyncPending("101", "event-nopend1", note: "forged", syncPending: "forged")
        try expect(.invalidState) { try legacyStore.write(key: noMarkerKey, value: introducedMarker) }
        try expect(.invalidState) { _ = try legacyStore.compareAndSet(key: noMarkerKey, expected: noMarkerCurrent, value: introducedMarker) }
        try check(Data(try legacyStore.readVersioned(key: noMarkerKey).value.utf8) == Data(noMarkerCurrent.utf8))
        let markerFreeVersion = try legacyStore.readVersioned(key: noMarkerKey)
        try check(try legacyStore.compareAndSetVersioned(key: noMarkerKey, expectedRevision: markerFreeVersion.revision,
                                                         expectedFound: true, expectedValue: noMarkerCurrent, value: introducedMarker))
        let stagedVersion = try legacyStore.readVersioned(key: noMarkerKey)
        try check(try legacyStore.compareAndSetVersioned(key: noMarkerKey, expectedRevision: stagedVersion.revision,
                                                         expectedFound: true, expectedValue: introducedMarker, value: noMarkerCurrent))
        try expect(.invalidState) { try legacyStore.write(key: noMarkerKey, value: introducedMarker) }
        try check(try legacyStore.readVersioned(key: noMarkerKey).value == noMarkerCurrent)

        let emojiKey = "nerae.workout.draft.v1.101.event-emoji01"
        let emojiInitial = try payloadWithSyncPending("101", "event-emoji01", syncPending: "🚴")
        try check(try legacyStore.create(key: emojiKey, value: emojiInitial))
        let emojiEscaped = #"{"schema":1,"subjectId":"101","draft":{"clientEventId":"event-emoji01","syncPending":"\ud83d\udeb4"}}"#
        try legacyStore.write(key: emojiKey, value: emojiEscaped)
        try check(try legacyStore.readVersioned(key: emojiKey).value == emojiEscaped)
        let invalidSurrogate = #"{"schema":1,"subjectId":"101","draft":{"clientEventId":"event-emoji01","syncPending":"\ud83d"}}"#
        try expect(.invalidState) { try legacyStore.write(key: emojiKey, value: invalidSurrogate) }
        try check(try legacyStore.readVersioned(key: emojiKey).value == emojiEscaped)

        // Malformed old marker values and duplicate JSON keys are not treated
        // as an absent marker; failed writes preserve the exact old bytes.
        let malformedPendingKey = "nerae.workout.draft.v1.101.event-badpend"
        let malformedOld = try payloadWithSyncPending("101", "event-badpend", syncPending: NSNull())
        let malformedOldBytes = Data(malformedOld.utf8)
        legacyBackend.values[account(for: malformedPendingKey)] = malformedOldBytes
        try expect(.unavailable) { try legacyStore.write(key: malformedPendingKey, value: try payload("101", "event-badpend", note: "must retain")) }
        try expect(.unavailable) { _ = try legacyStore.readVersioned(key: malformedPendingKey) }
        try check(legacyBackend.values[account(for: malformedPendingKey)] == malformedOldBytes)
        let duplicatePendingKey = "nerae.workout.draft.v1.101.event-dup-pend"
        let duplicateOld = #"{"schema":1,"subjectId":"101","draft":{"clientEventId":"event-dup-pend","syncPending":"first","syncPending":"second"}}"#
        let duplicateOldBytes = Data(duplicateOld.utf8)
        legacyBackend.values[account(for: duplicatePendingKey)] = duplicateOldBytes
        try expect(.unavailable) { try legacyStore.write(key: duplicatePendingKey, value: try payload("101", "event-dup-pend", note: "must retain")) }
        try expect(.unavailable) { _ = try legacyStore.readVersioned(key: duplicatePendingKey) }
        try check(legacyBackend.values[account(for: duplicatePendingKey)] == duplicateOldBytes)
        let escapedDuplicateKey = "nerae.workout.draft.v1.101.event-escaped1"
        let escapedDuplicate = #"{"schema":1,"subjectId":"101","draft":{"clientEventId":"event-escaped1","syncPending":"first","sync\u0050ending":"second"}}"#
        legacyBackend.values[account(for: escapedDuplicateKey)] = Data(escapedDuplicate.utf8)
        try expect(.unavailable) { _ = try legacyStore.readVersioned(key: escapedDuplicateKey) }
        try check(legacyBackend.values[account(for: escapedDuplicateKey)] == Data(escapedDuplicate.utf8))
        let duplicateDraftKey = "nerae.workout.draft.v1.101.event-dupdraft"
        let duplicateDraft = #"{"schema":1,"subjectId":"101","draft":{"clientEventId":"event-dupdraft"},"dr\u0061ft":{"clientEventId":"event-dupdraft","syncPending":"hidden"}}"#
        legacyBackend.values[account(for: duplicateDraftKey)] = Data(duplicateDraft.utf8)
        try expect(.unavailable) { _ = try legacyStore.readVersioned(key: duplicateDraftKey) }
        try check(legacyBackend.values[account(for: duplicateDraftKey)] == Data(duplicateDraft.utf8))
        let inventory = try legacyStore.discover(subject: "101", after: "")
        let inventoryData = Data(inventory.utf8)
        guard let listing = try JSONSerialization.jsonObject(with: inventoryData) as? [String: Any] else { throw Failure.assertion }
        try check(listing["hasUnclassifiedItems"] as? Bool == true)
        // Competing ordinary edits serialize, and whichever wins must retain
        // the original marker byte sequence.
        let racePendingKey = "nerae.workout.draft.v1.101.event-pendrace"
        let racePendingValue = try payloadWithSyncPending("101", "event-pendrace", note: "baseline", syncPending: composedMarker)
        legacyBackend.values[account(for: racePendingKey)] = Data(racePendingValue.utf8)
        let racingLegacyStores = [NeraeProtectedDraftStore(backend: legacyBackend), NeraeProtectedDraftStore(backend: legacyBackend)]
        let legacyResultLock = NSLock()
        var legacyWins = 0
        var legacyFailures = 0
        DispatchQueue.concurrentPerform(iterations: 32) { index in
            do {
                let current = try racingLegacyStores[index % 2].readVersioned(key: racePendingKey).value
                let update = try payloadWithSyncPending("101", "event-pendrace", note: "edit-\(index)", syncPending: composedMarker)
                if try racingLegacyStores[index % 2].compareAndSet(key: racePendingKey, expected: current, value: update) {
                    legacyResultLock.lock(); legacyWins += 1; legacyResultLock.unlock()
                }
            } catch { legacyResultLock.lock(); legacyFailures += 1; legacyResultLock.unlock() }
        }
        try check(legacyWins >= 1 && legacyFailures == 0)
        try check(syncPendingBytes(try legacyStore.readVersioned(key: racePendingKey).value) == Data(composedMarker.utf8))

        let versionedBackend = FakeDraftBackend()
        let versionedStores = [NeraeProtectedDraftStore(backend: versionedBackend),
                               NeraeProtectedDraftStore(backend: versionedBackend)]
        let versionedKey = "nerae.workout.draft.v1.101.event-version"
        let a = try payload("101", "event-version", note: "é")
        let b = try payload("101", "event-version", note: "B")
        let c = try payload("101", "event-version", note: "C")
        let uncertainCreateBackend = FakeDraftBackend()
        let uncertainCreate = NeraeProtectedDraftStore(backend: uncertainCreateBackend)
        uncertainCreateBackend.failAfterCreate = true
        try expect(.unavailable) { _ = try uncertainCreate.create(key: versionedKey, value: a) }
        uncertainCreateBackend.failAfterCreate = false
        let recoveredCreate = try uncertainCreate.readVersioned(key: versionedKey)
        try check(recoveredCreate.found && recoveredCreate.revision == "1" && Data(recoveredCreate.value.utf8) == Data(a.utf8))
        try check(!(try uncertainCreate.create(key: versionedKey, value: b)))
        try check(!(try versionedStores[0].readVersioned(key: versionedKey)).found)
        try check(try versionedStores[0].compareAndSetVersioned(key: versionedKey, expectedRevision: "0",
                                                                 expectedFound: false, expectedValue: "", value: a))
        try check(try versionedStores[0].readVersioned(key: versionedKey).revision == "1")
        try check(try versionedStores[1].compareAndSetVersioned(key: versionedKey, expectedRevision: "1",
                                                                 expectedFound: true, expectedValue: a, value: b))
        try check(try versionedStores[0].compareAndSetVersioned(key: versionedKey, expectedRevision: "2",
                                                                 expectedFound: true, expectedValue: b, value: a))
        try check(!(try versionedStores[1].compareAndSetVersioned(key: versionedKey, expectedRevision: "1",
                                                                   expectedFound: true, expectedValue: a, value: c)))
        try expect(.invalidState) {
            _ = try versionedStores[0].compareAndSetVersioned(key: versionedKey, expectedRevision: "3",
                                                                expectedFound: true, expectedValue: try payload("202", "event-version"),
                                                                value: c)
        }
        try expect(.invalidState) {
            _ = try versionedStores[0].compareAndSetVersioned(key: versionedKey, expectedRevision: "01",
                                                                expectedFound: true, expectedValue: a, value: c)
        }
        let afterABA = try versionedStores[0].readVersioned(key: versionedKey)
        try check(afterABA.revision == "3" && Data(afterABA.value.utf8) == Data(a.utf8))
        func decodeBridge(_ value: String) throws -> [String: Any] {
            guard let parsed = try JSONSerialization.jsonObject(with: Data(value.utf8)) as? [String: Any] else { throw Failure.assertion }
            return parsed
        }
        let bridgeRead = try decodeBridge(NeraeSecureVaultBridge.readVersionedTrainingDraft(in: versionedStores[0], key: versionedKey))
        try check(bridgeRead["ok"] as? Bool == true && bridgeRead["revision"] as? String == "3" &&
                  bridgeRead["value"] as? String == a)
        let bridgeConflict = try decodeBridge(NeraeSecureVaultBridge.compareAndSetVersionedTrainingDraft(
            in: versionedStores[1], key: versionedKey, expectedRevision: "1", expectedFound: true,
            expectedValue: a, value: c))
        try check(bridgeConflict["ok"] as? Bool == false && bridgeConflict["errorCode"] as? String == "conflict" &&
                  bridgeConflict["value"] as? String == "" && bridgeConflict["revision"] as? String == "")

        // Exactly one concurrent writer with the same revision can advance.
        var versionedWins = 0
        var versionedFailures = 0
        DispatchQueue.concurrentPerform(iterations: 64) { index in
            do {
                let next = try payload("101", "event-version", note: "race-\(index)")
                if try versionedStores[index % 2].compareAndSetVersioned(key: versionedKey,
                                                                           expectedRevision: "3", expectedFound: true,
                                                                           expectedValue: a, value: next) {
                    resultLock.lock(); versionedWins += 1; resultLock.unlock()
                }
            } catch { resultLock.lock(); versionedFailures += 1; resultLock.unlock() }
        }
        try check(versionedWins == 1 && versionedFailures == 0)
        let revision4 = try versionedStores[0].readVersioned(key: versionedKey)
        try check(revision4.revision == "4")

        // A failure before mutation preserves the complete old envelope. A
        // failure after mutation is unknown to the caller but remains readable.
        let beforeFailure = versionedBackend.values[account(for: versionedKey)]!
        versionedBackend.failBeforeUpdate = true
        try expect(.unavailable) {
            _ = try versionedStores[0].compareAndSetVersioned(key: versionedKey, expectedRevision: "4",
                                                               expectedFound: true, expectedValue: revision4.value,
                                                               value: c)
        }
        versionedBackend.failBeforeUpdate = false
        try check(versionedBackend.values[account(for: versionedKey)] == beforeFailure)
        versionedBackend.failAfterUpdate = true
        try expect(.unavailable) {
            _ = try versionedStores[0].compareAndSetVersioned(key: versionedKey, expectedRevision: "4",
                                                               expectedFound: true, expectedValue: revision4.value,
                                                               value: c)
        }
        versionedBackend.failAfterUpdate = false
        let afterUnknownVersionedWrite = try versionedStores[0].readVersioned(key: versionedKey)
        try check(afterUnknownVersionedWrite.revision == "5" && Data(afterUnknownVersionedWrite.value.utf8) == Data(c.utf8))
        versionedBackend.failReadAfterUpdate = true
        try expect(.unavailable) {
            _ = try versionedStores[0].compareAndSetVersioned(key: versionedKey, expectedRevision: "5",
                                                               expectedFound: true, expectedValue: c, value: b)
        }
        versionedBackend.failReadAfterUpdate = false
        let recoveredReadback = try versionedStores[0].readVersioned(key: versionedKey)
        try check(recoveredReadback.revision == "6" && Data(recoveredReadback.value.utf8) == Data(b.utf8))
        let d = try payload("101", "event-version", note: "compatibility write")
        try versionedStores[0].write(key: versionedKey, value: d)
        let revision7 = try versionedStores[0].readVersioned(key: versionedKey)
        try check(revision7.revision == "7" && Data(revision7.value.utf8) == Data(d.utf8))
        let e = try payload("101", "event-version", note: "compatibility CAS")
        try check(try versionedStores[1].compareAndSet(key: versionedKey, expected: d, value: e))
        let revision8 = try versionedStores[0].readVersioned(key: versionedKey)
        try check(revision8.revision == "8" && Data(revision8.value.utf8) == Data(e.utf8))
        let bridgeWrite = try decodeBridge(NeraeSecureVaultBridge.compareAndSetVersionedTrainingDraft(
            in: versionedStores[0], key: versionedKey, expectedRevision: "8", expectedFound: true,
            expectedValue: e, value: c))
        try check(bridgeWrite["ok"] as? Bool == true && bridgeWrite["revision"] as? String == "9" &&
                  bridgeWrite["value"] as? String == "")
        let afterBridgeWrite = try versionedStores[0].readVersioned(key: versionedKey)
        try check(afterBridgeWrite.revision == "9" && Data(afterBridgeWrite.value.utf8) == Data(c.utf8))

        // A compatibility caller holding pre-sync bytes must not overwrite
        // a newer marker-free draft after a request has been settled.
        let staleBridgeWrite = try decodeBridge(NeraeSecureVaultBridge.writeTrainingDraft(
            in: versionedStores[0], key: versionedKey, value: a))
        try check(staleBridgeWrite["ok"] as? Bool == false && staleBridgeWrite["errorCode"] as? String == "unavailable")
        let afterRejectedLegacyWrite = try versionedStores[0].readVersioned(key: versionedKey)
        try check(afterRejectedLegacyWrite.revision == "9" && Data(afterRejectedLegacyWrite.value.utf8) == Data(c.utf8))

        // Corrupt, unknown-version, invalid UTF-8, and exhausted revisions are
        // retained and fail closed instead of being reset or overwritten.
        let corruptBackend = FakeDraftBackend()
        let corruptStore = NeraeProtectedDraftStore(backend: corruptBackend)
        let corruptKey = "nerae.workout.draft.v1.101.event-corrupt"
        let corruptDraftAccount = account(for: corruptKey)
        let unknown = try versionedEnvelope(Data(try payload("101", "event-corrupt").utf8), revision: "1")
        let unknownObject = try PropertyListSerialization.propertyList(from: unknown, options: [], format: nil) as! [String: Any]
        var badFormat = unknownObject
        badFormat["format"] = "com.nerae.training-draft.versioned.v999"
        corruptBackend.values[corruptDraftAccount] = try PropertyListSerialization.data(fromPropertyList: badFormat, format: .binary, options: 0)
        let unknownBytes = corruptBackend.values[corruptDraftAccount]!
        try expect(.unavailable) { _ = try corruptStore.readVersioned(key: corruptKey) }
        try expect(.unavailable) { try corruptStore.write(key: corruptKey, value: try payload("101", "event-corrupt", note: "overwrite")) }
        try check(corruptBackend.values[corruptDraftAccount] == unknownBytes)
        corruptBackend.values[corruptDraftAccount] = Data([0xff, 0xfe])
        let invalidUTF8 = corruptBackend.values[corruptDraftAccount]!
        try expect(.unavailable) { _ = try corruptStore.readVersioned(key: corruptKey) }
        try check(corruptBackend.values[corruptDraftAccount] == invalidUTF8)

        let maxBackend = FakeDraftBackend()
        let maxStore = NeraeProtectedDraftStore(backend: maxBackend)
        let maxKey = "nerae.workout.draft.v1.101.event-maxrev1"
        let maxPayload = Data(try payload("101", "event-maxrev1").utf8)
        maxBackend.values[account(for: maxKey)] = try versionedEnvelope(maxPayload, revision: "9223372036854775807")
        let maxBytes = maxBackend.values[account(for: maxKey)]!
        try check(try maxStore.readVersioned(key: maxKey).revision == "9223372036854775807")
        try expect(.unavailable) { try maxStore.write(key: maxKey, value: try payload("101", "event-maxrev1", note: "overflow")) }
        try expect(.unavailable) {
            _ = try maxStore.compareAndSetVersioned(key: maxKey, expectedRevision: "9223372036854775807",
                                                     expectedFound: true, expectedValue: String(decoding: maxPayload, as: UTF8.self),
                                                     value: try payload("101", "event-maxrev1", note: "overflow"))
        }
        try check(maxBackend.values[account(for: maxKey)] == maxBytes)

        // A tracked schema-4 draft can only be changed through revision-bound CAS.
        let trackedBackend = FakeDraftBackend()
        let trackedStore = NeraeProtectedDraftStore(backend: trackedBackend)
        let trackedKey = "nerae.workout.draft.v1.101.event-track01"
        let oldSchema = #"{"schema":3,"subjectId":"101","draft":{"clientEventId":"event-track01","recordFormat":2}}"#
        let tracked = #"{"schema":4,"subjectId":"101","draft":{"clientEventId":"event-track01","recordFormat":2,"syncTracking":1}}"#
        let trackedEdit = #"{"schema":4,"subjectId":"101","draft":{"clientEventId":"event-track01","recordFormat":2,"syncTracking":1,"note":"new"}}"#
        try check(try trackedStore.create(key: trackedKey, value: oldSchema))
        let beforeTracking = try trackedStore.readVersioned(key: trackedKey)
        try check(try trackedStore.compareAndSetVersioned(key: trackedKey, expectedRevision: beforeTracking.revision,
          expectedFound: true, expectedValue: beforeTracking.value, value: tracked))
        let trackedState = try trackedStore.readVersioned(key: trackedKey)
        try expect(.invalidState) { try trackedStore.write(key: trackedKey, value: trackedEdit) }
        try expect(.invalidState) { _ = try trackedStore.compareAndSet(key: trackedKey, expected: tracked, value: trackedEdit) }
        try expect(.invalidState) { _ = try trackedStore.compareAndSetVersioned(key: trackedKey, expectedRevision: trackedState.revision,
          expectedFound: true, expectedValue: tracked, value: oldSchema) }
        try check(try trackedStore.compareAndSetVersioned(key: trackedKey, expectedRevision: trackedState.revision,
          expectedFound: true, expectedValue: tracked, value: trackedEdit))
        try check(try trackedStore.readVersioned(key: trackedKey).value == trackedEdit)

        // Discovery accepts legacy and versioned items without rewriting either.
        let mixedBackend = FakeDraftBackend()
        let mixedStore = NeraeProtectedDraftStore(backend: mixedBackend)
        let mixedLegacyKey = "nerae.workout.draft.v1.101.event-legacy2"
        let mixedV2Key = "nerae.workout.draft.v1.101.event-version2"
        let mixedLegacyBytes = Data(try payload("101", "event-legacy2").utf8)
        mixedBackend.values[account(for: mixedLegacyKey)] = mixedLegacyBytes
        try check(try mixedStore.create(key: mixedV2Key, value: try payload("101", "event-version2")))
        let mixedList = try mixedStore.discover(subject: "101", after: "")
        let mixedObject = try JSONSerialization.jsonObject(with: Data(mixedList.utf8)) as? [String: Any]
        try check(mixedObject?["eventIds"] as? [String] == ["event-legacy2", "event-version2"])
        try check(mixedBackend.values[account(for: mixedLegacyKey)] == mixedLegacyBytes)
        print("PASS: synthetic draft isolation, in-process races, paged discovery and uncertain write preservation")
    }
}
