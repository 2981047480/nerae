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
