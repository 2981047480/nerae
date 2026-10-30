import Foundation
import Dispatch

private final class RegistrySyntheticBackend: NeraeVaultBackend {
    var data: Data?
    var failWrite = false
    var failRead = false
    func read() throws -> Data? {
        if failRead { throw NeraeVaultError.unavailable }
        return data
    }
    func write(_ value: Data) throws {
        if failWrite { throw NeraeVaultError.unavailable }
        data = value
    }
    func clear() throws { data = nil }
}

private enum CheckFailure: Error { case assertion }
private func check(_ condition: @autoclosure () throws -> Bool) throws {
    if try condition() == false { throw CheckFailure.assertion }
}
private func expectFailure(_ action: () throws -> Void, invalid: Bool = false) throws {
    do { try action(); throw CheckFailure.assertion }
    catch NeraeVaultError.invalidState { if !invalid { throw CheckFailure.assertion } }
    catch NeraeVaultError.unavailable { if invalid { throw CheckFailure.assertion } }
}
private func expectConflict(_ action: () throws -> Void) throws {
    do { try action(); throw CheckFailure.assertion }
    catch NeraeVaultError.conflict { }
}

@main struct RegistrySyntheticTests {
    static func main() throws {
        let authNS = NeraeVaultNamespaces.authenticationState
        let registryNS = NeraeVaultNamespaces.deviceIdentityRegistry
        try check(authNS.service == "com.nerae.authentication-state.v1")
        try check(authNS.account == "authentication-state")
        try check(registryNS.service != authNS.service && registryNS.account != authNS.account)

        let failedInitBackend = RegistrySyntheticBackend()
        failedInitBackend.failWrite = true
        let failedInitVault = NeraeCredentialVault(backend: failedInitBackend)
        try expectFailure {
            try failedInitVault.compareAndSet(expectedFound: false, expectedValue: "", value: "{\"device\":\"retry\"}")
        }
        try check(failedInitBackend.data == nil)
        failedInitBackend.failWrite = false
        try failedInitVault.compareAndSet(expectedFound: false, expectedValue: "", value: "{\"device\":\"retry\"}")
        try check(try failedInitVault.read().value == "{\"device\":\"retry\"}")

        let initVault = NeraeCredentialVault(backend: RegistrySyntheticBackend())
        let initGate = DispatchSemaphore(value: 0)
        let initGroup = DispatchGroup()
        let initResultLock = NSLock()
        var initResults: [String] = []
        let initCandidates = ["{\"device\":\"init-a\"}", "{\"device\":\"init-b\"}"]
        for candidate in initCandidates {
            initGroup.enter()
            DispatchQueue.global().async {
                initGate.wait()
                let result: String
                do {
                    try initVault.compareAndSet(expectedFound: false, expectedValue: "", value: candidate)
                    result = "success"
                } catch NeraeVaultError.conflict {
                    result = "conflict"
                } catch {
                    result = "failure"
                }
                initResultLock.lock()
                initResults.append(result)
                initResultLock.unlock()
                initGroup.leave()
            }
        }
        initGate.signal(); initGate.signal()
        initGroup.wait()
        try check(initResults.sorted() == ["conflict", "success"])
        try check(initCandidates.contains(try initVault.read().value))

        let authBackend = RegistrySyntheticBackend()
        let registryBackend = RegistrySyntheticBackend()
        let auth = NeraeCredentialVault(backend: authBackend)
        let registry = NeraeCredentialVault(backend: registryBackend)
        let authValue = "{\"session\":\"synthetic-auth-only\"}"
        let registryValue = "{\"device\":\"synthetic-registry-only\"}"
        try auth.write(authValue)
        try registry.compareAndSet(expectedFound: false, expectedValue: "", value: registryValue)
        try check(try auth.read().value == authValue)
        try check(try registry.read().value == registryValue)

        // Initialization is conditional: a second initializer cannot replace
        // the first value and receives only a conflict status.
        try expectConflict {
            try registry.compareAndSet(expectedFound: false, expectedValue: "", value: "{\"device\":\"losing-init\"}")
        }
        try check(try registry.read().value == registryValue)

        // Two same-process writers based on the same old snapshot serialize at
        // the vault lock. Exactly one wins and the loser sees only conflict.
        let barrier = DispatchSemaphore(value: 0)
        let group = DispatchGroup()
        let resultLock = NSLock()
        var results: [String] = []
        let candidates = ["{\"device\":\"writer-a\"}", "{\"device\":\"writer-b\"}"]
        for candidate in candidates {
            group.enter()
            DispatchQueue.global().async {
                barrier.wait()
                let result: String
                do {
                    try registry.compareAndSet(expectedFound: true, expectedValue: registryValue, value: candidate)
                    result = "success"
                } catch NeraeVaultError.conflict {
                    result = "conflict"
                } catch {
                    result = "failure"
                }
                resultLock.lock()
                results.append(result)
                resultLock.unlock()
                group.leave()
            }
        }
        barrier.signal(); barrier.signal()
        group.wait()
        try check(results.sorted() == ["conflict", "success"])
        let winningValue = try registry.read().value
        try check(candidates.contains(winningValue))

        // Logout clears only the authentication-state vault. Registry has no
        // public clear operation and remains available afterwards.
        try auth.clear()
        try check(!(try auth.read()).found)
        try check(try registry.read().value == winningValue)

        registryBackend.failWrite = true
        try expectFailure {
            try registry.compareAndSet(expectedFound: true, expectedValue: winningValue, value: "{\"device\":\"replacement\"}")
        }
        registryBackend.failWrite = false
        try check(try registry.read().value == winningValue)
        try expectFailure({ try registry.write("[]") }, invalid: true)
        try expectFailure({ try registry.write("{\"x\":\"" + String(repeating: "x", count: 65_536) + "\"}") }, invalid: true)
        try check(try registry.read().value == winningValue)
        registryBackend.failRead = true
        try expectFailure { _ = try registry.read() }
        registryBackend.failRead = false
        try check(try registry.read().value == winningValue)
        print("PASS: synthetic independent namespace, conditional initialization, competing writers, logout isolation and failure preservation; process-local lock only, no real Keychain access")
    }
}
