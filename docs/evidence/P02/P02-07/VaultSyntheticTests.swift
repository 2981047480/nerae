import Foundation

private final class SyntheticBackend: NeraeVaultBackend {
    var data: Data?
    var failRead = false
    var failWrite = false
    var failClear = false

    func read() throws -> Data? {
        if failRead { throw NeraeVaultError.unavailable }
        return data
    }
    func write(_ value: Data) throws {
        if failWrite { throw NeraeVaultError.unavailable }
        data = value
    }
    func clear() throws {
        if failClear { throw NeraeVaultError.unavailable }
        data = nil
    }
}

private enum Failure: Error { case assertion }
private func check(_ condition: @autoclosure () throws -> Bool) throws {
    if try condition() == false { throw Failure.assertion }
}
private func expectFailure(_ action: () throws -> Void, invalid: Bool = false) throws {
    do { try action(); throw Failure.assertion }
    catch NeraeVaultError.invalidState { if !invalid { throw Failure.assertion } }
    catch NeraeVaultError.unavailable { if invalid { throw Failure.assertion } }
}

@main struct Main {
    static func main() throws {
        let backend = SyntheticBackend()
        let vault = NeraeCredentialVault(backend: backend)
        let empty = try vault.read()
        try check(!empty.found && empty.value == "")
        let secret = "{\"token\":\"synthetic-secret-do-not-log\",\"alias\":\"synthetic\"}"
        try vault.write(secret)
        let saved = try vault.read()
        try check(saved.found && saved.value == secret)
        try check(!String(describing: saved).contains("synthetic-secret"))
        try check(!String(reflecting: saved).contains("synthetic-secret"))
        try expectFailure({ try vault.write("") }, invalid: true)
        try expectFailure({ try vault.write("[]") }, invalid: true)
        try expectFailure({ try vault.write("{broken") }, invalid: true)
        try expectFailure({ try vault.write("{\"x\":\"" + String(repeating: "a", count: 65_536) + "\"}") }, invalid: true)
        try check(try vault.read().value == secret)
        backend.failWrite = true
        try expectFailure { try vault.write("{\"new\":true}") }
        backend.failWrite = false
        try check(try vault.read().value == secret)
        backend.failRead = true
        try expectFailure { _ = try vault.read() }
        backend.failRead = false
        try check(try vault.read().value == secret)
        backend.failClear = true
        try expectFailure { try vault.clear() }
        backend.failClear = false
        try check(try vault.read().value == secret)
        try vault.clear()
        try vault.clear()
        try check(!(try vault.read()).found)
        backend.data = Data([0xff, 0xfe])
        try expectFailure { _ = try vault.read() }
        backend.data = Data("[]".utf8)
        try expectFailure { _ = try vault.read() }
        backend.data = Data(repeating: 0x20, count: 65_537)
        try expectFailure { _ = try vault.read() }
        print("PASS: synthetic vault read/write/clear, corruption, bounds and failure preservation")
    }
}
