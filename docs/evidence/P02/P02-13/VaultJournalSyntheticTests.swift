import Foundation

private final class SyntheticBackend: NeraeVaultBackend {
    var data: Data?
    var failWrite = false
    var applyThenFail = false
    func read() throws -> Data? { data }
    func write(_ value: Data) throws {
        if applyThenFail { data = value; throw NeraeVaultError.unavailable }
        if failWrite { throw NeraeVaultError.unavailable }
        data = value
    }
    func clear() throws { data = nil }
}

private enum Failure: Error { case assertion }
private func check(_ condition: @autoclosure () throws -> Bool) throws {
    if try !condition() { throw Failure.assertion }
}

@main struct Main {
    static func main() throws {
        let authBackend = SyntheticBackend()
        let identityBackend = SyntheticBackend()
        let journalBackend = SyntheticBackend()
        let auth = NeraeCredentialVault(backend: authBackend)
        let identity = NeraeCredentialVault(backend: identityBackend)
        let journal = NeraeCredentialVault(backend: journalBackend)

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
