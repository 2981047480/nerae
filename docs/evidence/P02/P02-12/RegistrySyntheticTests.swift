import Foundation

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

@main struct RegistrySyntheticTests {
    static func main() throws {
        let authNS = NeraeVaultNamespaces.authenticationState
        let registryNS = NeraeVaultNamespaces.deviceIdentityRegistry
        try check(authNS.service == "com.nerae.authentication-state.v1")
        try check(authNS.account == "authentication-state")
        try check(registryNS.service != authNS.service && registryNS.account != authNS.account)

        let authBackend = RegistrySyntheticBackend()
        let registryBackend = RegistrySyntheticBackend()
        let auth = NeraeCredentialVault(backend: authBackend)
        let registry = NeraeCredentialVault(backend: registryBackend)
        let authValue = "{\"session\":\"synthetic-auth-only\"}"
        let registryValue = "{\"device\":\"synthetic-registry-only\"}"
        try auth.write(authValue)
        try registry.write(registryValue)
        try check(try auth.read().value == authValue)
        try check(try registry.read().value == registryValue)

        // Logout clears only the authentication-state vault. Registry has no
        // public clear operation and remains available afterwards.
        try auth.clear()
        try check(!(try auth.read()).found)
        try check(try registry.read().value == registryValue)

        registryBackend.failWrite = true
        try expectFailure { try registry.write("{\"device\":\"replacement\"}") }
        registryBackend.failWrite = false
        try check(try registry.read().value == registryValue)
        try expectFailure({ try registry.write("[]") }, invalid: true)
        try expectFailure({ try registry.write("{\"x\":\"" + String(repeating: "x", count: 65_536) + "\"}") }, invalid: true)
        try check(try registry.read().value == registryValue)
        registryBackend.failRead = true
        try expectFailure { _ = try registry.read() }
        registryBackend.failRead = false
        try check(try registry.read().value == registryValue)
        print("PASS: synthetic independent namespace, logout isolation, bounded writes and failure preservation; no real Keychain access")
    }
}
