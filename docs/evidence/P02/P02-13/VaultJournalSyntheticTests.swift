import Foundation

private final class SyntheticBackend: NeraeVaultBackend {
    var data: Data?
    var failWrite = false
    var applyThenFail = false
    var writes = 0
    func read() throws -> Data? { data }
    func write(_ value: Data) throws {
        writes += 1
        if applyThenFail { data = value; throw NeraeVaultError.unavailable }
        if failWrite { throw NeraeVaultError.unavailable }
        data = value
    }
    func clear() throws { data = nil }
}

private func expect(_ expected: NeraeVaultError, file: StaticString = #fileID,
                    line: UInt = #line, _ body: () throws -> Void) throws {
    do {
        try body()
        fputs("EXPECTED ERROR MISSING at \(file):\(line)\n", stderr)
        throw Failure.assertion
    } catch let error as NeraeVaultError {
        switch (expected, error) {
        case (.conflict, .conflict), (.invalidState, .invalidState), (.unavailable, .unavailable): return
        default:
            fputs("WRONG ERROR at \(file):\(line)\n", stderr)
            throw Failure.assertion
        }
    }
}

private func encodedEnvelope(revision: String, found: Bool, value: String,
                             fingerprint: String? = nil) throws -> Data {
    let object: [String: Any] = [
        "format": "com.nerae.training-operation-journal",
        "version": 2,
        "revision": revision,
        "found": found,
        "value": value,
        "legacyFingerprint": fingerprint ?? ""
    ]
    return try PropertyListSerialization.data(fromPropertyList: object, format: .binary, options: 0)
}

private func encodedDispatchEnvelope(value: String, historyCount: Int, activeCount: Int) throws -> Data {
    let markers = (0..<historyCount).map { String(format: "%064x", $0) }
    let active = (0..<activeCount).map { String(format: "%064x", $0) }
    let object: [String: Any] = [
        "format": "com.nerae.training-operation-journal", "version": 3, "revision": "1",
        "found": true, "value": value, "legacyFingerprint": "",
        "dispatchHistoryKnown": true, "dispatchMarkers": markers, "activeDispatchEvents": active
    ]
    return try PropertyListSerialization.data(fromPropertyList: object, format: .binary, options: 0)
}

private func jsonString(_ object: [String: Any]) throws -> String {
    let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys, .withoutEscapingSlashes])
    guard let value = String(data: data, encoding: .utf8) else { throw Failure.assertion }
    return value
}

private func dispatchFixtures(event: String = "event-test-0001", idempotencyKey: String = "request-00000001",
                              subject: String = "101", bodyOverride: String? = nil,
                              path: String = "/me/sessions") throws -> (String, String, String) {
    let allowed = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()")
    guard let encodedEvent = event.addingPercentEncoding(withAllowedCharacters: allowed) else { throw Failure.assertion }
    let key = "nerae.workout.operation.v1.\(subject).\(encodedEvent)"
    let body: String
    if let bodyOverride { body = bodyOverride }
    else { body = try jsonString(["source": "free", "date": "2026-11-03", "clientEventId": event]) }
    let ready: [String: Any] = [
        "schema": 1, "subjectId": subject, "clientEventId": event, "revision": 1,
        "state": "ready", "createdGeneration": "generation-1", "createdAt": 1, "expiresAt": 999999,
        "request": ["method": "POST", "path": path, "idempotencyKey": idempotencyKey, "body": body]
    ]
    var inFlight = ready
    inFlight["state"] = "in_flight"
    inFlight["revision"] = 2
    inFlight["attemptGeneration"] = "generation-1"
    return (key,
            try jsonString(["schema": 1, "entries": [key: try jsonString(ready)]]),
            try jsonString(["schema": 1, "entries": [key: try jsonString(inFlight)]]))
}

private func mutateRegistryEntry(_ registry: String, key: String, _ transform: (inout [String: Any]) throws -> Void) throws -> String {
    guard var object = try JSONSerialization.jsonObject(with: Data(registry.utf8)) as? [String: Any],
          var entries = object["entries"] as? [String: String], let raw = entries[key],
          var entry = try JSONSerialization.jsonObject(with: Data(raw.utf8)) as? [String: Any] else { throw Failure.assertion }
    try transform(&entry)
    entries[key] = try jsonString(entry)
    object["entries"] = entries
    return try jsonString(object)
}

private func registryEntries(_ value: String) throws -> [String: String] {
    guard let object = try JSONSerialization.jsonObject(with: Data(value.utf8)) as? [String: Any],
          let entries = object["entries"] as? [String: String] else { throw Failure.assertion }
    return entries
}

private func strictRequestJSONTests() throws {
    let valid = [
        "{}",
        "{\"name\":\"quote \\\" slash \\\\ and 🏋️\",\"items\":[true,false,null,-1,1.5,1e3]}",
        "{\"first\":{\"x\":1},\"second\":{\"x\":2}}",
        "{\"é\":1,\"e\\u0301\":2}",
    ]
    for body in valid { try check(NeraeStrictJSON.isObjectWithoutDuplicateKeys(body)) }
    let invalid = [
        "[]", "{\"x\":1,\"x\":2}", "{\"a\":1,\"\\u0061\":2}",
        "{\"outer\":[{\"x\":1,\"x\":2}]}", "{\"x\":01}", "{\"x\":truefalse}",
        "{\"x\":\"unterminated}", "{\"x\":\"bad\nline\"}",
    ]
    for body in invalid { try check(!NeraeStrictJSON.isObjectWithoutDuplicateKeys(body)) }
    let atDepth = "{\"x\":" + String(repeating: "[", count: 31) + "0" + String(repeating: "]", count: 31) + "}"
    let tooDeep = "{\"x\":" + String(repeating: "[", count: 32) + "0" + String(repeating: "]", count: 32) + "}"
    try check(NeraeStrictJSON.isObjectWithoutDuplicateKeys(atDepth))
    try check(!NeraeStrictJSON.isObjectWithoutDuplicateKeys(tooDeep))
    let atCount = "{\"x\":[" + Array(repeating: "0", count: 5000).joined(separator: ",") + "]}"
    let tooMany = "{\"x\":[" + Array(repeating: "0", count: 5001).joined(separator: ",") + "]}"
    try check(NeraeStrictJSON.isObjectWithoutDuplicateKeys(atCount))
    try check(!NeraeStrictJSON.isObjectWithoutDuplicateKeys(tooMany))
    let atNodes = "{\"a\":[" + Array(repeating: "0", count: 4999).joined(separator: ",") + "],\"b\":[" + Array(repeating: "0", count: 4999).joined(separator: ",") + "],\"c\":[" + Array(repeating: "0", count: 1998).joined(separator: ",") + "]}"
    let tooManyNodes = "{\"a\":[" + Array(repeating: "0", count: 4999).joined(separator: ",") + "],\"b\":[" + Array(repeating: "0", count: 4999).joined(separator: ",") + "],\"c\":[" + Array(repeating: "0", count: 1999).joined(separator: ",") + "]}"
    try check(NeraeStrictJSON.isObjectWithoutDuplicateKeys(atNodes))
    try check(!NeraeStrictJSON.isObjectWithoutDuplicateKeys(tooManyNodes))
    let hostileDepth = "{\"x\":" + String(repeating: "[", count: 1000) + "0" + String(repeating: "]", count: 1000) + "}"
    try check(!NeraeStrictJSON.isObjectWithoutDuplicateKeys(hostileDepth))
    try check(!NeraeStrictJSON.isObjectWithoutDuplicateKeys("{\"recordKey\":1}", asciiKeysOnly: true))
    try check(NeraeStrictJSON.isObjectWithoutDuplicateKeys("{\"name\":\"深蹲 🏋️\"}", asciiKeysOnly: true))
    for canonical in ["{\"x\":1e-7}", "{\"x\":1e+21}", "{\"x\":\"a/b\\n🏋️\"}", "{\"x\":0}"] {
        try check(NeraeStrictJSON.isCanonicalJournalObject(canonical))
    }
    for nonCanonical in ["{\"x\": 1}", "{\"x\":-0}", "{\"x\":1.0}", "{\"x\":1e0}", "{\"\\u0078\":1}"] {
        try check(!NeraeStrictJSON.isCanonicalJournalObject(nonCanonical))
    }
    try check(!NeraeStrictJSON.isObjectWithoutDuplicateKeys("{\"x\":\"" + String(repeating: "a", count: 65_536) + "\"}"))
    print("PASS: strict request JSON rejects duplicate keys, invalid syntax and resource bounds")
}

private func recoveryBindingTests() throws {
    let duplicateBodies = [
        "{\"source\":\"free\",\"date\":\"2026-11-03\",\"clientEventId\":\"event-test-0001\",\"clientEventId\":\"event-test-0001\"}",
        "{\"source\":\"free\",\"date\":\"2026-11-03\",\"clientEventId\":\"event-test-0001\",\"client\\u0045ventId\":\"event-test-0001\"}",
        "{\"source\":\"free\",\"date\":\"2026-11-03\",\"clientEventId\":\"event-test-0001\",\"extra\":[{\"x\":1,\"x\":2}]}",
    ]
    for body in duplicateBodies {
        let (duplicateKey, duplicateReady, duplicateInFlight) = try dispatchFixtures(bodyOverride: body)
        let backend = SyntheticBackend()
        let store = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: backend)
        let staged = try store.compareAndSet(expectedRevision: "0", expectedFound: false, expectedValue: "", newFound: true, newValue: duplicateReady)
        try expect(.invalidState) {
            _ = try store.claimFirstSend(expectedRevision: staged.revision, expectedValue: duplicateReady,
                                         operationKey: duplicateKey, nextValue: duplicateInFlight)
        }
        try check(try store.read().value == duplicateReady)
    }
    let invalidContracts: [(String, String)] = [
        ("/me/sessions", "{\"source\":\"free\",\"clientEventId\":\"event-test-0001\"}"),
        ("/me/sessions", "{\"source\":\"other\",\"date\":\"2026-11-03\",\"clientEventId\":\"event-test-0001\"}"),
        ("/me/sessions", "{\"source\":\"free\",\"date\":0,\"clientEventId\":\"event-test-0001\"}"),
        ("/me/sessions", "{\"source\":\"free\",\"date\":\"2026-11-03\",\"clientEventId\":\"event-test-0001\",\"extra\":true}"),
        ("/me/sessions", "{\"source\":\"plan\",\"date\":\"2026-11-03\",\"clientEventId\":\"event-test-0001\"}"),
        ("/me/sessions/123/sets", "{\"expectedVersion\":true,\"exerciseEntryKeys\":[]}"),
        ("/me/sessions/123/sets", "{\"expectedVersion\":1,\"exerciseEntryKeys\":[7]}"),
        ("/me/sessions/123/sets", "{\"expectedVersion\":1,\"exerciseEntryKeys\":[],\"sets\":[{\"recordKey\":\"r\",\"exerciseEntryKey\":\"e\",\"setIndex\":1,\"kind\":\"weight\",\"exerciseName\":\"x\",\"weightKg\":true}]}"),
        ("/me/sessions/123/sets", "{\"expectedVersion\":1,\"exerciseEntryKeys\":[],\"sets\":[{\"recordKey\":\"r\",\"exerciseEntryKey\":\"e\",\"setIndex\":1,\"kind\":\"weight\",\"exerciseId\":\"1\",\"exerciseName\":\"x\"}]}"),
        ("/me/sessions/123/sets", "{\"expectedVersion\":1,\"exerciseEntryKeys\":[],\"sets\":[{\"recordKey\":\"r\",\"exerciseEntryKey\":\"e\",\"setIndex\":1,\"kind\":\"weight\",\"exerciseName\":\"x\"}]}"),
        ("/me/sessions/123/sets", "{\"expectedVersion\":1,\"exerciseEntryKeys\":[],\"sets\":[{\"recordKey\":\"r\",\"exerciseEntryKey\":\"e\",\"setIndex\":1,\"kind\":\"weight\",\"exerciseName\":\"x\",\"completed\":false}]}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":\"1\"}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"note\":7}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"unexpected\":1}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"durationMin\":18446744073709551615}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"durationMin\":9223372036854775808}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"durationMin\":9007199254740992}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"durationMin\":-9007199254740992}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"durationMin\":true}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\": 1}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"durationMin\":-0}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"rpe\":1.0}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"rpe\":1e0}"),
        ("/me/sessions/123/finish", "{\"expected\\u0056ersion\":1}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":1,\"note\":\"a\\/b\"}"),
    ]
    for (path, body) in invalidContracts {
        let (invalidKey, invalidReady, invalidNext) = try dispatchFixtures(bodyOverride: body, path: path)
        let store = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: SyntheticBackend())
        let before = try store.compareAndSet(expectedRevision: "0", expectedFound: false, expectedValue: "", newFound: true, newValue: invalidReady)
        try expect(.invalidState) {
            _ = try store.claimFirstSend(expectedRevision: before.revision, expectedValue: invalidReady, operationKey: invalidKey, nextValue: invalidNext)
        }
        try check(try store.read().value == invalidReady)
    }
    let validContracts: [(String, String)] = [
        ("/me/sessions", "{\"source\":\"free\",\"date\":\"2026-11-03\",\"timeZone\":\"Asia/Shanghai\",\"clientEventId\":\"event-test-0001\"}"),
        ("/me/sessions", "{\"source\":\"plan\",\"date\":\"2026-11-03\",\"planId\":\"1\",\"planVersion\":1,\"planDayId\":\"2\",\"clientEventId\":\"event-test-0001\",\"occurrenceId\":\"3\"}"),
        ("/me/sessions/123/sets", "{\"expectedVersion\":1,\"exerciseEntryKeys\":[\"entry-001\"],\"sets\":[{\"recordKey\":\"record-001\",\"exerciseEntryKey\":\"entry-001\",\"setIndex\":1,\"kind\":\"weight\",\"exerciseName\":\"深蹲\",\"weightKg\":42.5,\"reps\":8,\"rpe\":null,\"completed\":true}]}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":2,\"durationMin\":null,\"date\":\"2026-11-03\",\"rpe\":8.5,\"note\":\"完成 🏋️\"}"),
        ("/me/sessions/123/finish", "{\"expectedVersion\":9007199254740991,\"durationMin\":-9007199254740991}"),
    ]
    for (path, body) in validContracts {
        let (validKey, validReady, validNext) = try dispatchFixtures(bodyOverride: body, path: path)
        let store = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: SyntheticBackend())
        let before = try store.compareAndSet(expectedRevision: "0", expectedFound: false, expectedValue: "", newFound: true, newValue: validReady)
        _ = try store.claimFirstSend(expectedRevision: before.revision, expectedValue: validReady, operationKey: validKey, nextValue: validNext)
        try check(try store.recoveryIntent(operationKey: validKey, subject: "101").body == body)
    }
    // A realistic larger batch must stay usable under the scanner's node bound.
    let batchSets: [[String: Any]] = (1...200).map { index in
        ["recordKey": "r\(index)", "exerciseEntryKey": "e\(index)", "setIndex": 1,
         "kind": "weight", "exerciseName": "深蹲", "weightKg": 42.5, "reps": 8]
    }
    let batch = try jsonString(["expectedVersion": 1, "exerciseEntryKeys": (1...2000).map { "e\($0)" }, "sets": batchSets])
    let (batchKey, batchReady, batchNext) = try dispatchFixtures(bodyOverride: batch, path: "/me/sessions/123/sets")
    let batchStore = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: SyntheticBackend())
    let batchBefore = try batchStore.compareAndSet(expectedRevision: "0", expectedFound: false, expectedValue: "", newFound: true, newValue: batchReady)
    _ = try batchStore.claimFirstSend(expectedRevision: batchBefore.revision, expectedValue: batchReady, operationKey: batchKey, nextValue: batchNext)
    try check(try batchStore.recoveryIntent(operationKey: batchKey, subject: "101").body == batch)
    for (event, bodyEvent) in [("event-test-0001", "event-test-0002"),
                               (String(repeating: "é", count: 8), String(repeating: "e\u{301}", count: 8))] {
        let wrongBody = try jsonString(["source": "free", "date": "2026-11-03", "clientEventId": bodyEvent])
        let (wrongKey, wrongReady, wrongInFlight) = try dispatchFixtures(event: event, bodyOverride: wrongBody)
        let backend = SyntheticBackend()
        let store = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: backend)
        let staged = try store.compareAndSet(expectedRevision: "0", expectedFound: false, expectedValue: "", newFound: true, newValue: wrongReady)
        try expect(.invalidState) {
            _ = try store.claimFirstSend(expectedRevision: staged.revision, expectedValue: wrongReady,
                                         operationKey: wrongKey, nextValue: wrongInFlight)
        }
        try check(try store.read().value == wrongReady)
    }
    let body = "{\"expectedVersion\":1,\"note\":\"test\"}"
    let (key, ready, inFlight) = try dispatchFixtures(bodyOverride: body, path: "/me/sessions/123/finish")
    let legacy = SyntheticBackend(), current = SyntheticBackend()
    let journal = NeraeVersionedTrainingJournal(legacy: legacy, current: current)
    let staged = try journal.compareAndSet(expectedRevision: "0", expectedFound: false, expectedValue: "", newFound: true, newValue: ready)
    let sent = try journal.claimFirstSend(expectedRevision: staged.revision, expectedValue: ready, operationKey: key, nextValue: inFlight)
    let first = try journal.recoveryIntent(operationKey: key, subject: "101")
    try check(first.body == body && first.idempotencyKey == "request-00000001" && first.revision == sent.revision)
    try check(!String(describing: first).contains(body) && !String(reflecting: first).contains("request-00000001"))
    let restarted = NeraeVersionedTrainingJournal(legacy: legacy, current: current)
    try check(try restarted.recoveryIntent(operationKey: key, subject: "101").body == body)
    try expect(.invalidState) { _ = try journal.recoveryIntent(operationKey: key, subject: "0101") }
    try expect(.invalidState) { _ = try journal.recoveryIntent(operationKey: key, subject: "102") }
    let uncertain = try mutateRegistryEntry(inFlight, key: key) { entry in
        entry["state"] = "uncertain"; entry["revision"] = 3; entry.removeValue(forKey: "attemptGeneration")
    }
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: inFlight, value: uncertain)
    try check(try journal.recoveryIntent(operationKey: key, subject: "101").body == body)
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: uncertain, value: inFlight)

    let nonCanonical = try mutateRegistryEntry(inFlight, key: key) { entry in
        guard var request = entry["request"] as? [String: Any] else { throw Failure.assertion }
        request["body"] = "{ \"expectedVersion\":1,\"note\":\"test\" }"
        entry["request"] = request
    }
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: inFlight, value: nonCanonical)
    try expect(.invalidState) { _ = try journal.recoveryIntent(operationKey: key, subject: "101") }
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: nonCanonical, value: inFlight)
    for (field, replacement) in [("body", "{\"note\":\"test\",\"expectedVersion\":1}"),
                                 ("idempotencyKey", "request-00000002"), ("path", "/me/sessions/124/finish")] {
        let changed = try mutateRegistryEntry(inFlight, key: key) { entry in
            guard var request = entry["request"] as? [String: Any] else { throw Failure.assertion }
            request[field] = replacement
            entry["request"] = request
        }
        try journal.compareAndSetLegacy(expectedFound: true, expectedValue: inFlight, value: changed)
        try expect(.conflict) { _ = try journal.recoveryIntent(operationKey: key, subject: "101") }
        try journal.compareAndSetLegacy(expectedFound: true, expectedValue: changed, value: inFlight)
    }
    for (field, replacement) in [("subjectId", "102"), ("clientEventId", "event-test-0002")] {
        let changed = try mutateRegistryEntry(inFlight, key: key) { $0[field] = replacement }
        try journal.compareAndSetLegacy(expectedFound: true, expectedValue: inFlight, value: changed)
        try expect(.invalidState) { _ = try journal.recoveryIntent(operationKey: key, subject: "101") }
        try journal.compareAndSetLegacy(expectedFound: true, expectedValue: changed, value: inFlight)
    }
    let afterABA = try journal.recoveryIntent(operationKey: key, subject: "101")
    try check(afterABA.revision != first.revision && afterABA.body == first.body)

    // An active V3 record has no request binding. Keep it readable, preserve
    // its marker history through compatibility writes, and refuse recovery.
    guard let saved = current.data,
          var v3 = try PropertyListSerialization.propertyList(from: saved, options: [], format: nil) as? [String: Any] else { throw Failure.assertion }
    v3["version"] = 3
    v3.removeValue(forKey: "activeBindingsKnown")
    v3.removeValue(forKey: "activeDispatchBindings")
    let oldCurrent = SyntheticBackend()
    oldCurrent.data = try PropertyListSerialization.data(fromPropertyList: v3, format: .binary, options: 0)
    let oldJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: oldCurrent)
    let oldRead = try oldJournal.read()
    try check(oldRead.value == inFlight)
    try expect(.unavailable) { _ = try oldJournal.recoveryIntent(operationKey: key, subject: "101") }
    try oldJournal.compareAndSetLegacy(expectedFound: true, expectedValue: inFlight, value: inFlight)
    try expect(.unavailable) { _ = try oldJournal.recoveryIntent(operationKey: key, subject: "101") }
    let migrated = try oldJournal.read()
    try expect(.unavailable) { _ = try oldJournal.claimFirstSend(expectedRevision: migrated.revision, expectedValue: inFlight, operationKey: key, nextValue: inFlight) }
    guard let migratedData = oldCurrent.data,
          let migratedObject = try PropertyListSerialization.propertyList(from: migratedData, options: [], format: nil) as? [String: Any] else { throw Failure.assertion }
    try check(migratedObject["activeDispatchEvents"] as? [String] == v3["activeDispatchEvents"] as? [String])
    try check(migratedObject["dispatchMarkers"] as? [String] == v3["dispatchMarkers"] as? [String])
    try check((migratedObject["version"] as? NSNumber)?.intValue == 4)

    let raceCurrent = SyntheticBackend()
    raceCurrent.data = try PropertyListSerialization.data(fromPropertyList: v3, format: .binary, options: 0)
    let raceOne = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: raceCurrent)
    let raceTwo = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: raceCurrent)
    let observed = try raceOne.read()
    _ = try raceOne.compareAndSet(expectedRevision: observed.revision, expectedFound: true, expectedValue: inFlight, newFound: true, newValue: inFlight)
    try expect(.conflict) {
        _ = try raceTwo.compareAndSet(expectedRevision: observed.revision, expectedFound: true, expectedValue: inFlight, newFound: true, newValue: inFlight)
    }

    // A V3 slot without active work can migrate and retains prior history.
    let idleCurrent = SyntheticBackend()
    idleCurrent.data = try encodedDispatchEnvelope(value: ready, historyCount: 2, activeCount: 0)
    let idleJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: idleCurrent)
    let idle = try idleJournal.read()
    let newClaim = try idleJournal.claimFirstSend(expectedRevision: idle.revision, expectedValue: ready, operationKey: key, nextValue: inFlight)
    try check(try idleJournal.recoveryIntent(operationKey: key, subject: "101").revision == newClaim.revision)
    guard let idleData = idleCurrent.data,
          let idleObject = try PropertyListSerialization.propertyList(from: idleData, options: [], format: nil) as? [String: Any],
          let history = idleObject["dispatchMarkers"] as? [String] else { throw Failure.assertion }
    try check(history.count == 4 && history.contains(String(format: "%064x", 0)) && history.contains(String(format: "%064x", 1)))

    // Two active events may each have valid global history markers. Replacing
    // A's request with B's cannot borrow B's markers as A's binding.
    let (otherKey, otherReady, _) = try dispatchFixtures(event: "event-test-0002", idempotencyKey: "request-00000002",
                                                        bodyOverride: "{\"expectedVersion\":1,\"note\":\"other\"}", path: "/me/sessions/123/finish")
    let joinedBackend = SyntheticBackend()
    let joined = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: joinedBackend)
    let start = try joined.compareAndSet(expectedRevision: "0", expectedFound: false, expectedValue: "", newFound: true, newValue: ready)
    let firstClaim = try joined.claimFirstSend(expectedRevision: start.revision, expectedValue: ready, operationKey: key, nextValue: inFlight)
    var joinedEntries = try registryEntries(inFlight)
    joinedEntries[otherKey] = try registryEntries(otherReady)[otherKey]
    let bothReady = try jsonString(["schema": 1, "entries": joinedEntries])
    let withOther = try joined.compareAndSet(expectedRevision: firstClaim.revision, expectedFound: true, expectedValue: inFlight, newFound: true, newValue: bothReady)
    let bothSent = try mutateRegistryEntry(bothReady, key: otherKey) { entry in
        entry["state"] = "in_flight"; entry["revision"] = 2; entry["attemptGeneration"] = "generation-1"
    }
    _ = try joined.claimFirstSend(expectedRevision: withOther.revision, expectedValue: bothReady, operationKey: otherKey, nextValue: bothSent)
    try check(try joined.recoveryIntent(operationKey: key, subject: "101").idempotencyKey == "request-00000001")
    try check(try joined.recoveryIntent(operationKey: otherKey, subject: "101").idempotencyKey == "request-00000002")
    let otherEntryRaw = try registryEntries(bothSent)[otherKey]!
    guard let otherEntry = try JSONSerialization.jsonObject(with: Data(otherEntryRaw.utf8)) as? [String: Any],
          let otherRequest = otherEntry["request"] as? [String: Any] else { throw Failure.assertion }
    let spliced = try mutateRegistryEntry(bothSent, key: key) { $0["request"] = otherRequest }
    try joined.compareAndSetLegacy(expectedFound: true, expectedValue: bothSent, value: spliced)
    try expect(.conflict) { _ = try joined.recoveryIntent(operationKey: key, subject: "101") }

    // A malformed binding does not delete, replace, or silently trust the item.
    guard var corrupt = try PropertyListSerialization.propertyList(from: saved, options: [], format: nil) as? [String: Any],
          var bindings = corrupt["activeDispatchBindings"] as? [String: String], let marker = bindings.keys.first else { throw Failure.assertion }
    bindings[marker] = "invalid"
    corrupt["activeDispatchBindings"] = bindings
    let damaged = try PropertyListSerialization.data(fromPropertyList: corrupt, format: .binary, options: 0)
    let corruptCurrent = SyntheticBackend(); corruptCurrent.data = damaged
    let corruptJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: corruptCurrent)
    try expect(.unavailable) { _ = try corruptJournal.recoveryIntent(operationKey: key, subject: "101") }
    try check(corruptCurrent.data == damaged)

    // A maximal V4 envelope remains readable; full history/active capacity
    // refuses new claims without evicting any existing marker or binding.
    let maxMarkers = (0..<8192).map { String(format: "%064x", $0) }
    let maxEvents = (0..<64).map { String(format: "%064x", $0) }
    var maxBindings: [String: String] = [:]
    for index in 0..<64 { maxBindings[maxEvents[index]] = String(format: "%064x", 10_000 + index) }
    let prefix = "{\"payload\":\"", suffix = "\"}"
    let maxValue = prefix + String(repeating: "a", count: 64 * 1024 - prefix.utf8.count - suffix.utf8.count) + suffix
    try check(maxValue.utf8.count == 64 * 1024)
    let maxObject: [String: Any] = ["format": "com.nerae.training-operation-journal", "version": 4, "revision": "1",
        "found": true, "value": maxValue, "legacyFingerprint": "", "dispatchHistoryKnown": true,
        "dispatchMarkers": maxMarkers, "activeDispatchEvents": maxEvents,
        "activeBindingsKnown": true, "activeDispatchBindings": maxBindings]
    let maxData = try PropertyListSerialization.data(fromPropertyList: maxObject, format: .binary, options: 0)
    let maxCurrent = SyntheticBackend(); maxCurrent.data = maxData
    let maxJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: maxCurrent)
    try check(try maxJournal.read().value == maxValue)
    var fullObject = maxObject
    fullObject["value"] = ready
    let fullData = try PropertyListSerialization.data(fromPropertyList: fullObject, format: .binary, options: 0)
    let fullCurrent = SyntheticBackend(); fullCurrent.data = fullData
    let fullJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: fullCurrent)
    try expect(.unavailable) {
        _ = try fullJournal.claimFirstSend(expectedRevision: "1", expectedValue: ready, operationKey: key, nextValue: inFlight)
    }
    try check(fullCurrent.data == fullData)
    print("PASS: native recovery intent binds exact request and safely handles older active journals")
}

private func dispatchMarkerTests() throws {
    let (key, ready, inFlight) = try dispatchFixtures()
    let legacy = SyntheticBackend()
    let current = SyntheticBackend()
    let journal = NeraeVersionedTrainingJournal(legacy: legacy, current: current)
    let initial = try journal.compareAndSet(expectedRevision: "0", expectedFound: false,
                                            expectedValue: "", newFound: true, newValue: ready)
    let claimed = try journal.claimFirstSend(expectedRevision: initial.revision, expectedValue: ready,
                                             operationKey: key, nextValue: inFlight)
    try check(claimed.value == inFlight && claimed.revision == "2")
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: inFlight, value: ready)
    let restored = try journal.read()
    try expect(.conflict) {
        _ = try journal.claimFirstSend(expectedRevision: restored.revision, expectedValue: ready,
                                       operationKey: key, nextValue: inFlight)
    }
    let deleted = try journal.compareAndSet(expectedRevision: restored.revision, expectedFound: true,
                                            expectedValue: ready, newFound: false, newValue: "")
    let recreated = try journal.compareAndSet(expectedRevision: deleted.revision, expectedFound: false,
                                              expectedValue: "", newFound: true, newValue: ready)
    try expect(.conflict) {
        _ = try journal.claimFirstSend(expectedRevision: recreated.revision, expectedValue: ready,
                                       operationKey: key, nextValue: inFlight)
    }
    let changedKeyReady = ready.replacingOccurrences(of: "request-00000001", with: "request-00000002")
    let changedKeyInFlight = inFlight.replacingOccurrences(of: "request-00000001", with: "request-00000002")
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: ready, value: changedKeyReady)
    let changedKeyState = try journal.read()
    try expect(.conflict) {
        _ = try journal.claimFirstSend(expectedRevision: changedKeyState.revision, expectedValue: changedKeyReady,
                                       operationKey: key, nextValue: changedKeyInFlight)
    }
    let (_, changedPathReady, changedPathInFlight) = try dispatchFixtures(idempotencyKey: "request-00000003",
        bodyOverride: "{\"expectedVersion\":1}", path: "/me/sessions/123/finish")
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: changedKeyReady, value: changedPathReady)
    let changedPathState = try journal.read()
    try expect(.conflict) {
        _ = try journal.claimFirstSend(expectedRevision: changedPathState.revision, expectedValue: changedPathReady,
                                       operationKey: key, nextValue: changedPathInFlight)
    }
    let (sameKeyOtherEvent, sameKeyReady, sameKeyInFlight) = try dispatchFixtures(event: "event-test-0002")
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: changedPathReady, value: sameKeyReady)
    let sameKeyState = try journal.read()
    try expect(.conflict) {
        _ = try journal.claimFirstSend(expectedRevision: sameKeyState.revision, expectedValue: sameKeyReady,
                                       operationKey: sameKeyOtherEvent, nextValue: sameKeyInFlight)
    }
    let reorderedBody = "{\"source\":\"free\",\"date\":\"2026-11-03\",\"clientEventId\":\"event-test-0001\"}"
    let (_, reorderedReady, reorderedInFlight) = try dispatchFixtures(idempotencyKey: "request-00000003", bodyOverride: reorderedBody)
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: sameKeyReady, value: reorderedReady)
    let reorderedState = try journal.read()
    try expect(.conflict) {
        _ = try journal.claimFirstSend(expectedRevision: reorderedState.revision, expectedValue: reorderedReady,
                                       operationKey: key, nextValue: reorderedInFlight)
    }
    let (otherKey, otherReady, otherInFlight) = try dispatchFixtures(event: "event-test-0002", idempotencyKey: "request-00000002")
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: reorderedReady, value: otherReady)
    let otherState = try journal.read()
    let otherClaim = try journal.claimFirstSend(expectedRevision: otherState.revision, expectedValue: otherReady,
                                                operationKey: otherKey, nextValue: otherInFlight)
    try check(otherClaim.value == otherInFlight)
    let (otherSubjectKey, otherSubjectReady, otherSubjectInFlight) = try dispatchFixtures(event: "event-test-0002", idempotencyKey: "request-00000002", subject: "102")
    try journal.compareAndSetLegacy(expectedFound: true, expectedValue: otherInFlight, value: otherSubjectReady)
    let otherSubjectState = try journal.read()
    let otherSubjectClaim = try journal.claimFirstSend(expectedRevision: otherSubjectState.revision, expectedValue: otherSubjectReady,
                                                       operationKey: otherSubjectKey, nextValue: otherSubjectInFlight)
    try check(otherSubjectClaim.value == otherSubjectInFlight)

    let oldCurrent = SyntheticBackend()
    oldCurrent.data = try encodedEnvelope(revision: "5", found: true, value: ready)
    let oldJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: oldCurrent)
    let old = try oldJournal.read()
    try check(old.value == ready && old.revision == "5")
    try expect(.unavailable) {
        _ = try oldJournal.claimFirstSend(expectedRevision: old.revision, expectedValue: ready,
                                          operationKey: key, nextValue: inFlight)
    }
    try oldJournal.compareAndSetLegacy(expectedFound: true, expectedValue: ready, value: ready)
    let migrated = try oldJournal.read()
    try expect(.unavailable) {
        _ = try oldJournal.claimFirstSend(expectedRevision: migrated.revision, expectedValue: ready,
                                          operationKey: key, nextValue: inFlight)
    }
    let v1Legacy = SyntheticBackend()
    v1Legacy.data = Data(ready.utf8)
    let v1Journal = NeraeVersionedTrainingJournal(legacy: v1Legacy, current: SyntheticBackend())
    let v1 = try v1Journal.read()
    try expect(.unavailable) {
        _ = try v1Journal.claimFirstSend(expectedRevision: v1.revision, expectedValue: ready,
                                         operationKey: key, nextValue: inFlight)
    }

    let invalidCurrent = SyntheticBackend()
    let invalidJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: invalidCurrent)
    let base = try invalidJournal.compareAndSet(expectedRevision: "0", expectedFound: false,
                                                 expectedValue: "", newFound: true, newValue: ready)
    let changedBody = inFlight.replacingOccurrences(of: "request-00000001", with: "request-00000002")
    try expect(.invalidState) {
        _ = try invalidJournal.claimFirstSend(expectedRevision: base.revision, expectedValue: ready,
                                              operationKey: key, nextValue: changedBody)
    }
    try expect(.invalidState) {
        _ = try invalidJournal.claimFirstSend(expectedRevision: base.revision, expectedValue: ready,
                                              operationKey: key + "x", nextValue: inFlight)
    }
    try check(try invalidJournal.read().value == ready)
    let (invalidPathKey, invalidPathReady, invalidPathNext) = try dispatchFixtures(path: "/me/sessions/1/sets\n")
    try invalidJournal.compareAndSetLegacy(expectedFound: true, expectedValue: ready, value: invalidPathReady)
    let invalidPathState = try invalidJournal.read()
    try expect(.invalidState) {
        _ = try invalidJournal.claimFirstSend(expectedRevision: invalidPathState.revision, expectedValue: invalidPathReady,
                                             operationKey: invalidPathKey, nextValue: invalidPathNext)
    }
    let (unicodeKey, unicodeReady, unicodeNext) = try dispatchFixtures(event: "动作-卧推/001")
    try check(unicodeKey.contains("%2F"))
    try invalidJournal.compareAndSetLegacy(expectedFound: true, expectedValue: invalidPathReady, value: unicodeReady)
    let unicodeState = try invalidJournal.read()
    let unicodeClaim = try invalidJournal.claimFirstSend(expectedRevision: unicodeState.revision, expectedValue: unicodeReady,
                                                        operationKey: unicodeKey, nextValue: unicodeNext)
    try check(unicodeClaim.value == unicodeNext)

    let uncertainCurrent = SyntheticBackend()
    let uncertainJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: uncertainCurrent)
    let staged = try uncertainJournal.compareAndSet(expectedRevision: "0", expectedFound: false,
                                                     expectedValue: "", newFound: true, newValue: ready)
    uncertainCurrent.applyThenFail = true
    try expect(.unavailable) {
        _ = try uncertainJournal.claimFirstSend(expectedRevision: staged.revision, expectedValue: ready,
                                                operationKey: key, nextValue: inFlight)
    }
    uncertainCurrent.applyThenFail = false
    let uncertain = try uncertainJournal.read()
    try check(uncertain.value == inFlight)
    try uncertainJournal.compareAndSetLegacy(expectedFound: true, expectedValue: inFlight, value: ready)
    let rewound = try uncertainJournal.read()
    try expect(.conflict) {
        _ = try uncertainJournal.claimFirstSend(expectedRevision: rewound.revision, expectedValue: ready,
                                                operationKey: key, nextValue: inFlight)
    }

    let fullCurrent = SyntheticBackend()
    fullCurrent.data = try encodedDispatchEnvelope(value: ready, historyCount: 8192, activeCount: 0)
    let fullJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: fullCurrent)
    let fullBefore = fullCurrent.data
    try expect(.unavailable) {
        _ = try fullJournal.claimFirstSend(expectedRevision: "1", expectedValue: ready,
                                         operationKey: key, nextValue: inFlight)
    }
    try check(fullCurrent.data == fullBefore)
    let activeFullCurrent = SyntheticBackend()
    activeFullCurrent.data = try encodedDispatchEnvelope(value: ready, historyCount: 128, activeCount: 64)
    let activeFullJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: activeFullCurrent)
    let activeFullBefore = activeFullCurrent.data
    try expect(.unavailable) {
        _ = try activeFullJournal.claimFirstSend(expectedRevision: "1", expectedValue: ready,
                                                operationKey: key, nextValue: inFlight)
    }
    try check(activeFullCurrent.data == activeFullBefore)

    let concurrentLegacy = SyntheticBackend()
    let concurrentCurrent = SyntheticBackend()
    let first = NeraeVersionedTrainingJournal(legacy: concurrentLegacy, current: concurrentCurrent)
    let second = NeraeVersionedTrainingJournal(legacy: concurrentLegacy, current: concurrentCurrent)
    let pending = try first.compareAndSet(expectedRevision: "0", expectedFound: false,
                                          expectedValue: "", newFound: true, newValue: ready)
    let group = DispatchGroup()
    let queue = DispatchQueue(label: "native-dispatch-claim", attributes: .concurrent)
    let resultLock = NSLock()
    var success = 0
    var conflict = 0
    for store in [first, second] {
        group.enter()
        queue.async {
            defer { group.leave() }
            do {
                _ = try store.claimFirstSend(expectedRevision: pending.revision, expectedValue: ready,
                                             operationKey: key, nextValue: inFlight)
                resultLock.lock(); success += 1; resultLock.unlock()
            } catch NeraeVaultError.conflict {
                resultLock.lock(); conflict += 1; resultLock.unlock()
            } catch { }
        }
    }
    group.wait()
    try check(success == 1 && conflict == 1)
    print("PASS: native dispatch claim survives CAS rewind, recreate, unknown write and concurrency; old history fails closed")
}

private func versionedJournalTests() throws {
    let readWire = NeraeSecureVaultBridge.encodeVersioned(ok: true, found: true,
                                                         value: "{\"op\":\"visible-only-on-read\"}", revision: "7")
    let readObject = try JSONSerialization.jsonObject(with: Data(readWire.utf8)) as? [String: Any]
    try check(readObject?["ok"] as? Bool == true && readObject?["found"] as? Bool == true &&
              readObject?["revision"] as? String == "7" &&
              readObject?["value"] as? String == "{\"op\":\"visible-only-on-read\"}")
    let casWire = NeraeSecureVaultBridge.encodeVersioned(ok: true, found: false, value: "", revision: "8")
    let casObject = try JSONSerialization.jsonObject(with: Data(casWire.utf8)) as? [String: Any]
    try check(casObject?["ok"] as? Bool == true && casObject?["found"] as? Bool == false &&
              casObject?["value"] as? String == "" && casObject?["revision"] as? String == "8")
    let failureWire = NeraeSecureVaultBridge.encodeVersionedFailure(NeraeVaultError.conflict)
    let failureObject = try JSONSerialization.jsonObject(with: Data(failureWire.utf8)) as? [String: Any]
    try check(failureObject?["ok"] as? Bool == false && failureObject?["errorCode"] as? String == "conflict" &&
              failureObject?["found"] as? Bool == false && failureObject?["value"] as? String == "" &&
              failureObject?["revision"] as? String == "")

    let legacy = SyntheticBackend()
    let current = SyntheticBackend()
    let journal = NeraeVersionedTrainingJournal(legacy: legacy, current: current)

    let empty = try journal.read()
    try check(!empty.found && empty.value == "" && empty.revision == "0")
    let first = try journal.compareAndSet(expectedRevision: "0", expectedFound: false,
                                          expectedValue: "", newFound: true, newValue: "{\"op\":\"A\"}")
    try check(first.revision == "1" && first.value == "{\"op\":\"A\"}")
    let captured = first
    let second = try journal.compareAndSet(expectedRevision: first.revision, expectedFound: true,
                                           expectedValue: first.value, newFound: true, newValue: "{\"op\":\"B\"}")
    let third = try journal.compareAndSet(expectedRevision: second.revision, expectedFound: true,
                                          expectedValue: second.value, newFound: true, newValue: "{\"op\":\"A\"}")
    try check(third.value == captured.value && third.revision == "3")
    try expect(.conflict) {
        _ = try journal.compareAndSet(expectedRevision: captured.revision, expectedFound: captured.found,
                                      expectedValue: captured.value, newFound: true, newValue: "{\"op\":\"stale\"}")
    }
    try check(try journal.read().revision == "3")

    let deleted = try journal.compareAndSet(expectedRevision: third.revision, expectedFound: true,
                                            expectedValue: third.value, newFound: false, newValue: "")
    try check(!deleted.found && deleted.revision == "4")
    let recreated = try journal.compareAndSet(expectedRevision: deleted.revision, expectedFound: false,
                                              expectedValue: "", newFound: true, newValue: "{\"op\":\"A\"}")
    try check(recreated.value == captured.value && recreated.revision == "5")

    // If a new install started with no v1 item, a later old-version writer
    // creating one is also detected rather than treated as a fresh migration.
    let lateLegacy = SyntheticBackend()
    let lateCurrent = SyntheticBackend()
    let lateJournal = NeraeVersionedTrainingJournal(legacy: lateLegacy, current: lateCurrent)
    let neverStored = try lateJournal.read()
    try check(!neverStored.found && neverStored.revision == "0")
    let versionedOnly = try lateJournal.compareAndSet(expectedRevision: "0", expectedFound: false,
                                                      expectedValue: "", newFound: true,
                                                      newValue: "{\"source\":\"v2\"}")
    let versionedBytesBeforeLegacyAppears = lateCurrent.data
    lateLegacy.data = Data("{\"source\":\"late-v1\"}".utf8)
    try expect(.unavailable) { _ = try lateJournal.read() }
    try expect(.unavailable) {
        _ = try lateJournal.compareAndSet(expectedRevision: versionedOnly.revision, expectedFound: true,
                                          expectedValue: versionedOnly.value, newFound: true,
                                          newValue: "{\"source\":\"overwrite\"}")
    }
    try check(lateCurrent.data == versionedBytesBeforeLegacyAppears)

    // A same-value rewrite is still a new native version.
    let rewritten = try journal.compareAndSet(expectedRevision: recreated.revision, expectedFound: true,
                                              expectedValue: recreated.value, newFound: true,
                                              newValue: recreated.value)
    try check(rewritten.revision == "6")

    let overflowCurrent = SyntheticBackend()
    overflowCurrent.data = try encodedEnvelope(revision: String(Int64.max), found: true,
                                               value: "{\"op\":\"max\"}")
    let overflowJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: overflowCurrent)
    try expect(.unavailable) {
        _ = try overflowJournal.compareAndSet(expectedRevision: String(Int64.max), expectedFound: true,
                                              expectedValue: "{\"op\":\"max\"}", newFound: false, newValue: "")
    }
    try check(overflowCurrent.writes == 0)
    try expect(.invalidState) {
        _ = try journal.compareAndSet(expectedRevision: "01", expectedFound: true,
                                      expectedValue: rewritten.value, newFound: true, newValue: "{\"op\":1}")
    }
    try expect(.invalidState) {
        _ = try journal.compareAndSet(expectedRevision: "9223372036854775808", expectedFound: true,
                                      expectedValue: rewritten.value, newFound: true, newValue: "{\"op\":1}")
    }
    current.failWrite = true
    try expect(.unavailable) {
        _ = try journal.compareAndSet(expectedRevision: rewritten.revision, expectedFound: true,
                                      expectedValue: rewritten.value, newFound: true, newValue: "{\"op\":\"no-write\"}")
    }
    current.failWrite = false
    try check(try journal.read().revision == rewritten.revision && journal.read().value == rewritten.value)

    let exactUnicode = "{\"x\":\"" + String(repeating: "中", count: 21_842) + "é\"}"
    try check(exactUnicode.utf8.count == NeraeCredentialVault.maxBytes)
    let unicodeBackend = SyntheticBackend()
    let unicodeJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: unicodeBackend)
    let unicodeWritten = try unicodeJournal.compareAndSet(expectedRevision: "0", expectedFound: false,
                                                           expectedValue: "", newFound: true,
                                                           newValue: exactUnicode)
    try check(unicodeWritten.revision == "1" && unicodeWritten.value == exactUnicode)
    let overUnicode = exactUnicode.replacingOccurrences(of: "é\"}", with: "éa\"}")
    try expect(.invalidState) {
        _ = try unicodeJournal.compareAndSet(expectedRevision: unicodeWritten.revision, expectedFound: true,
                                             expectedValue: exactUnicode, newFound: true, newValue: overUnicode)
    }
    try check(try unicodeJournal.read().revision == "1")

    let damagedCurrent = SyntheticBackend()
    damagedCurrent.data = Data("not-a-versioned-envelope".utf8)
    let retainedLegacy = SyntheticBackend()
    retainedLegacy.data = Data("{\"legacy\":true}".utf8)
    let damagedJournal = NeraeVersionedTrainingJournal(legacy: retainedLegacy, current: damagedCurrent)
    try expect(.unavailable) { _ = try damagedJournal.read() }
    try check(damagedCurrent.data == Data("not-a-versioned-envelope".utf8))
    try check(retainedLegacy.data == Data("{\"legacy\":true}".utf8))

    let invalidLegacy = SyntheticBackend()
    let invalidLegacyBytes = Data("[]".utf8)
    invalidLegacy.data = invalidLegacyBytes
    let invalidMigrationCurrent = SyntheticBackend()
    let invalidMigration = NeraeVersionedTrainingJournal(legacy: invalidLegacy,
                                                         current: invalidMigrationCurrent)
    try expect(.unavailable) { _ = try invalidMigration.read() }
    try check(invalidLegacy.data == invalidLegacyBytes && invalidMigrationCurrent.data == nil)

    // Migration copies v1 into v2, retains v1 unchanged, and all compatibility
    // writes go through v2 so the native revision still advances.
    let migrationLegacy = SyntheticBackend()
    let migrationCurrent = SyntheticBackend()
    let legacyBytes = Data("{\"legacy\":\"kept\"}".utf8)
    migrationLegacy.data = legacyBytes
    let migratedJournal = NeraeVersionedTrainingJournal(legacy: migrationLegacy, current: migrationCurrent)
    let migrated = try migratedJournal.read()
    try check(migrated.revision == "1" && migrated.value == String(decoding: legacyBytes, as: UTF8.self))
    try check(migrationLegacy.data == legacyBytes && migrationCurrent.data != nil)
    try migratedJournal.compareAndSetLegacy(expectedFound: true, expectedValue: migrated.value,
                                            value: "{\"legacy\":\"updated\"}")
    let afterLegacyCompatibleWrite = try migratedJournal.read()
    try check(afterLegacyCompatibleWrite.revision == "2" && migrationLegacy.data == legacyBytes)

    // A pre-versioned writer changing v1 after migration is detected; v2 is
    // not overwritten and stale v1 is never used as a fallback.
    migrationLegacy.data = Data("{\"legacy\":\"changed-by-old-version\"}".utf8)
    try expect(.unavailable) { _ = try migratedJournal.read() }
    try check(migrationCurrent.data != nil)

    let uncertainMigrationLegacy = SyntheticBackend()
    let uncertainMigrationCurrent = SyntheticBackend()
    let preservedBytes = Data("{\"migration\":\"preserve\"}".utf8)
    uncertainMigrationLegacy.data = preservedBytes
    uncertainMigrationCurrent.applyThenFail = true
    let uncertainMigration = NeraeVersionedTrainingJournal(legacy: uncertainMigrationLegacy,
                                                           current: uncertainMigrationCurrent)
    try expect(.unavailable) { _ = try uncertainMigration.read() }
    try check(uncertainMigrationLegacy.data == preservedBytes)
    uncertainMigrationCurrent.applyThenFail = false
    let recoveredMigration = try uncertainMigration.read()
    try check(recoveredMigration.revision == "1" && recoveredMigration.value == String(decoding: preservedBytes, as: UTF8.self))

    let concurrentLegacy = SyntheticBackend()
    let concurrentCurrent = SyntheticBackend()
    let concurrentA = NeraeVersionedTrainingJournal(legacy: concurrentLegacy, current: concurrentCurrent)
    let concurrentB = NeraeVersionedTrainingJournal(legacy: concurrentLegacy, current: concurrentCurrent)
    let concurrentBase = try concurrentA.compareAndSet(expectedRevision: "0", expectedFound: false,
                                                        expectedValue: "", newFound: true, newValue: "{\"n\":0}")
    let casGroup = DispatchGroup()
    let casQueue = DispatchQueue(label: "versioned-journal-cas", attributes: .concurrent)
    let casLock = NSLock()
    var casSuccess = 0
    var casConflict = 0
    for (store, next) in [(concurrentA, "{\"n\":1}"), (concurrentB, "{\"n\":2}")] {
        casGroup.enter()
        casQueue.async {
            defer { casGroup.leave() }
            do {
                _ = try store.compareAndSet(expectedRevision: concurrentBase.revision, expectedFound: true,
                                            expectedValue: concurrentBase.value, newFound: true, newValue: next)
                casLock.lock(); casSuccess += 1; casLock.unlock()
            } catch NeraeVaultError.conflict {
                casLock.lock(); casConflict += 1; casLock.unlock()
            } catch { }
        }
    }
    casGroup.wait()
    try check(casSuccess == 1 && casConflict == 1 && (try concurrentA.read().revision == "2"))

    let unknownCurrent = SyntheticBackend()
    let unknownJournal = NeraeVersionedTrainingJournal(legacy: SyntheticBackend(), current: unknownCurrent)
    unknownCurrent.applyThenFail = true
    try expect(.unavailable) {
        _ = try unknownJournal.compareAndSet(expectedRevision: "0", expectedFound: false,
                                             expectedValue: "", newFound: true, newValue: "{\"op\":1}")
    }
    unknownCurrent.applyThenFail = false
    let unknownOutcome = try unknownJournal.read()
    try check(unknownOutcome.revision == "1" && unknownOutcome.value == "{\"op\":1}")

    print("PASS: native versioned journal ABA, tombstone/recreate, overflow, corruption, v1 migration and uncertain-write cases")
}

private enum Failure: Error { case assertion }
private func check(_ condition: @autoclosure () throws -> Bool,
                   file: StaticString = #fileID, line: UInt = #line) throws {
    if try !condition() {
        fputs("ASSERTION FAILED at \(file):\(line)\n", stderr)
        throw Failure.assertion
    }
}

@main struct Main {
    static func main() throws {
        let authBackend = SyntheticBackend()
        let identityBackend = SyntheticBackend()
        let journalBackend = SyntheticBackend()
        let auth = NeraeCredentialVault(backend: authBackend)
        let identity = NeraeCredentialVault(backend: identityBackend)
        let journal = NeraeCredentialVault(backend: journalBackend)

        try versionedJournalTests()
        try dispatchMarkerTests()
        try strictRequestJSONTests()
        try recoveryBindingTests()

        try check(NeraeVaultNamespaces.authenticationState != NeraeVaultNamespaces.deviceIdentityRegistry)
        try check(NeraeVaultNamespaces.authenticationState != NeraeVaultNamespaces.trainingOperationJournal)
        try check(NeraeVaultNamespaces.deviceIdentityRegistry != NeraeVaultNamespaces.trainingOperationJournal)

        try auth.write("{\"session\":\"synthetic\"}")
        try identity.write("{\"device\":\"synthetic\"}")
        try journal.compareAndSet(expectedFound: false, expectedValue: "", value: "{\"ops\":[]}")
        try check(try journal.read().value == "{\"ops\":[]}")
        try auth.clear()
        try check(!(try auth.read()).found)
        try check(try identity.read().value == "{\"device\":\"synthetic\"}")
        try check(try journal.read().value == "{\"ops\":[]}")

        let exactLimit = "{\"x\":\"" + String(repeating: "a", count: 65_528) + "\"}"
        try check(exactLimit.utf8.count == NeraeCredentialVault.maxBytes)
        try journal.compareAndSet(expectedFound: true, expectedValue: "{\"ops\":[]}", value: exactLimit)
        try check(try journal.read().value == exactLimit)
        try journal.compareAndSet(expectedFound: true, expectedValue: exactLimit, value: "{\"ops\":[]}")

        do {
            try journal.compareAndSet(expectedFound: true, expectedValue: "{\"ops\":[]} ", value: "{\"ops\":[1]}")
            throw Failure.assertion
        } catch NeraeVaultError.conflict { }
        try check(try journal.read().value == "{\"ops\":[]}")
        do {
            try journal.compareAndSet(expectedFound: true, expectedValue: "{\"ops\":[]}", value: "[]")
            throw Failure.assertion
        } catch NeraeVaultError.invalidState { }
        do {
            try journal.compareAndSet(expectedFound: true, expectedValue: "{\"ops\":[]}", value: "{\"x\":1} trailing")
            throw Failure.assertion
        } catch NeraeVaultError.invalidState { }

        let oversized = "{\"x\":\"" + String(repeating: "a", count: 65_529) + "\"}"
        do {
            try journal.compareAndSet(expectedFound: true, expectedValue: "{\"ops\":[]}", value: oversized)
            throw Failure.assertion
        } catch NeraeVaultError.invalidState { }
        try check(try journal.read().value == "{\"ops\":[]}")

        journalBackend.failWrite = true
        do {
            try journal.compareAndSet(expectedFound: true, expectedValue: "{\"ops\":[]}", value: "{\"ops\":[0]}")
            throw Failure.assertion
        } catch NeraeVaultError.unavailable { }
        journalBackend.failWrite = false
        try check(try journal.read().value == "{\"ops\":[]}")

        // All competing writers use the same vault instance and the same expected value.
        let group = DispatchGroup()
        let queue = DispatchQueue(label: "journal-cas-test", attributes: .concurrent)
        let resultLock = NSLock()
        var stored = 0
        var conflicts = 0
        for replacement in ["{\"ops\":[1]}", "{\"ops\":[2]}"] {
            group.enter()
            queue.async {
                defer { group.leave() }
                do {
                    try journal.compareAndSet(expectedFound: true, expectedValue: "{\"ops\":[]}", value: replacement)
                    resultLock.lock(); stored += 1; resultLock.unlock()
                } catch NeraeVaultError.conflict {
                    resultLock.lock(); conflicts += 1; resultLock.unlock()
                } catch { }
            }
        }
        group.wait()
        try check(stored == 1 && conflicts == 1)
        let winningValue = try journal.read().value
        try check(winningValue == "{\"ops\":[1]}" || winningValue == "{\"ops\":[2]}")

        let beforeUnknown = winningValue
        journalBackend.applyThenFail = true
        do {
            try journal.compareAndSet(expectedFound: true, expectedValue: beforeUnknown, value: "{\"ops\":[3]}")
            throw Failure.assertion
        } catch NeraeVaultError.unavailable { }
        journalBackend.applyThenFail = false
        // An unknown write may have committed. The new value remains readable; no cleanup erases it.
        try check(try journal.read().value == "{\"ops\":[3]}")

        print("PASS: synthetic three-slot isolation, bounded journal CAS, conflict, concurrency and uncertain-write preservation")
    }
}
