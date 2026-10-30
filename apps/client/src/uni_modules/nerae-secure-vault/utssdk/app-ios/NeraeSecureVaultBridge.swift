import Foundation
import Security

// Internal backend is injectable for synthetic failure tests. The shipping
// instance always uses the iOS Keychain and never falls back to file storage.
protocol NeraeVaultBackend {
    func read() throws -> Data?
    func write(_ value: Data) throws
    func clear() throws
}

enum NeraeVaultError: Error {
    case invalidState
    case unavailable
    case conflict
}

struct NeraeVaultNamespace: Equatable {
    let service: String
    let account: String
}

enum NeraeVaultNamespaces {
    static let authenticationState = NeraeVaultNamespace(
        service: "com.nerae.authentication-state.v1", account: "authentication-state")
    static let deviceIdentityRegistry = NeraeVaultNamespace(
        service: "com.nerae.device-identity-registry.v1", account: "device-identity-registry")
}

final class NeraeKeychainBackend: NeraeVaultBackend {
    private let namespace: NeraeVaultNamespace

    init(namespace: NeraeVaultNamespace) {
        self.namespace = namespace
    }

    private func query() -> [String: Any] {
        return [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: namespace.service,
            kSecAttrAccount as String: namespace.account,
            kSecAttrSynchronizable as String: kCFBooleanFalse as Any
        ]
    }

    func read() throws -> Data? {
        var request = query()
        request[kSecReturnData as String] = kCFBooleanTrue
        request[kSecReturnAttributes as String] = kCFBooleanTrue
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let attributes = result as? [String: Any],
              let access = attributes[kSecAttrAccessible as String],
              CFEqual(access as CFTypeRef, kSecAttrAccessibleWhenUnlockedThisDeviceOnly),
              let data = attributes[kSecValueData as String] as? Data else {
            throw NeraeVaultError.unavailable
        }
        return data
    }

    func write(_ value: Data) throws {
        let update = [kSecValueData as String: value,
                      kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly] as CFDictionary
        let status = SecItemUpdate(query() as CFDictionary, update)
        if status == errSecSuccess { return }
        guard status == errSecItemNotFound else { throw NeraeVaultError.unavailable }
        var item = query()
        item[kSecValueData as String] = value
        item[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let added = SecItemAdd(item as CFDictionary, nil)
        // A conflicting writer does not authorize overwriting its value.
        guard added == errSecSuccess else { throw NeraeVaultError.unavailable }
    }

    func clear() throws {
        let status = SecItemDelete(query() as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw NeraeVaultError.unavailable
        }
    }
}

struct NeraeVaultRead: CustomStringConvertible, CustomDebugStringConvertible {
    let found: Bool
    let value: String
    var description: String { "NeraeVaultRead{redacted}" }
    var debugDescription: String { description }
}

final class NeraeCredentialVault {
    static let maxBytes = 64 * 1024
    private let backend: NeraeVaultBackend
    private let lock = NSLock()

    init(backend: NeraeVaultBackend) { self.backend = backend }

    func read() throws -> NeraeVaultRead {
        lock.lock()
        defer { lock.unlock() }
        guard let data = try backend.read() else {
            return NeraeVaultRead(found: false, value: "")
        }
        guard Self.valid(data), let value = String(data: data, encoding: .utf8) else {
            throw NeraeVaultError.unavailable
        }
        return NeraeVaultRead(found: true, value: value)
    }

    func write(_ value: String) throws {
        guard let data = value.data(using: .utf8), Self.valid(data) else {
            throw NeraeVaultError.invalidState
        }
        lock.lock()
        defer { lock.unlock() }
        try backend.write(data)
    }

    /// Atomic within this vault instance's process-level lock. This is not a
    /// cross-process compare-and-set guarantee.
    func compareAndSet(expectedFound: Bool, expectedValue: String, value: String) throws {
        guard let newData = value.data(using: .utf8), Self.valid(newData) else {
            throw NeraeVaultError.invalidState
        }
        let expectedData: Data?
        if expectedFound {
            guard let data = expectedValue.data(using: .utf8), Self.valid(data) else {
                throw NeraeVaultError.invalidState
            }
            expectedData = data
        } else {
            guard expectedValue.isEmpty else { throw NeraeVaultError.invalidState }
            expectedData = nil
        }
        lock.lock()
        defer { lock.unlock() }
        let actual = try backend.read()
        guard actual == expectedData else { throw NeraeVaultError.conflict }
        try backend.write(newData)
    }

    func clear() throws {
        lock.lock()
        defer { lock.unlock() }
        try backend.clear()
    }

    private static func valid(_ data: Data) -> Bool {
        guard !data.isEmpty, data.count <= maxBytes,
              String(data: data, encoding: .utf8) != nil,
              let json = try? JSONSerialization.jsonObject(with: data),
              json is [String: Any] else { return false }
        return true
    }
}

@objc(NeraeSecureVaultBridge)
public final class NeraeSecureVaultBridge: NSObject {
    private static let authenticationStateVault = NeraeCredentialVault(
        backend: NeraeKeychainBackend(namespace: NeraeVaultNamespaces.authenticationState)
    )
    private static let deviceIdentityRegistry = NeraeCredentialVault(
        backend: NeraeKeychainBackend(namespace: NeraeVaultNamespaces.deviceIdentityRegistry)
    )

    @objc public static func readAuthenticationState() -> String {
        do {
            let result = try authenticationStateVault.read()
            return encode(ok: true, found: result.found, value: result.value)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func writeAuthenticationState(_ value: String) -> String {
        do {
            try authenticationStateVault.write(value)
            return encode(ok: true)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func clearAuthenticationState() -> String {
        do {
            try authenticationStateVault.clear()
            return encode(ok: true)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func readDeviceIdentityRegistry() -> String {
        do {
            let result = try deviceIdentityRegistry.read()
            return encode(ok: true, found: result.found, value: result.value)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func compareAndSetDeviceIdentityRegistry(
        _ expectedFound: Bool, expectedValue: String, value: String
    ) -> String {
        do {
            try deviceIdentityRegistry.compareAndSet(
                expectedFound: expectedFound, expectedValue: expectedValue, value: value)
            return encode(ok: true)
        } catch {
            return encodeFailure(error)
        }
    }

    private static func encodeFailure(_ error: Error) -> String {
        if let vaultError = error as? NeraeVaultError, case .invalidState = vaultError {
            return encode(ok: false, errorCode: "invalid_state")
        }
        if let vaultError = error as? NeraeVaultError, case .conflict = vaultError {
            return encode(ok: false, errorCode: "conflict")
        }
        return encode(ok: false, errorCode: "unavailable")
    }

    private static func encode(ok: Bool, errorCode: String = "", found: Bool = false, value: String = "") -> String {
        let result: [String: Any] = ["ok": ok, "errorCode": errorCode, "found": found, "value": value]
        guard let data = try? JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]),
              let json = String(data: data, encoding: .utf8) else {
            return "{\"ok\":false,\"errorCode\":\"bridge_failure\",\"found\":false,\"value\":\"\"}"
        }
        return json
    }
}
