import Foundation
import Security
import CryptoKit
import CoreFoundation

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
    static let trainingOperationJournal = NeraeVaultNamespace(
        service: "com.nerae.training-operation-journal.v1", account: "training-operation-journal")
    static let versionedTrainingOperationJournal = NeraeVaultNamespace(
        service: "com.nerae.training-operation-journal.v2", account: "training-operation-journal")
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

struct NeraeVersionedJournalRead {
    let found: Bool
    let value: String
    let revision: String
}

/// The native revision is stored in the same Keychain item as its payload.
/// All instances share this process lock; this is deliberately not described
/// as a cross-process Keychain compare-and-set guarantee.
final class NeraeVersionedTrainingJournal {
    private struct State {
        let found: Bool
        let value: String
        let revision: Int64
        let legacyFingerprint: String?
    }

    private static let processLock = NSRecursiveLock()
    private static let format = "com.nerae.training-operation-journal"
    // Binary plist may represent a Unicode String as UTF-16, while the
    // existing public payload limit is measured in UTF-8 bytes.
    private static let maxEnvelopeBytes = (NeraeCredentialVault.maxBytes * 2) + 2048

    private let legacy: NeraeVaultBackend
    private let current: NeraeVaultBackend

    init(legacy: NeraeVaultBackend, current: NeraeVaultBackend) {
        self.legacy = legacy
        self.current = current
    }

    func read() throws -> NeraeVersionedJournalRead {
        Self.processLock.lock()
        defer { Self.processLock.unlock() }
        let state = try loadLocked()
        return Self.readResult(state)
    }

    func compareAndSet(
        expectedRevision: String,
        expectedFound: Bool,
        expectedValue: String,
        newFound: Bool,
        newValue: String
    ) throws -> NeraeVersionedJournalRead {
        guard let expected = Self.parseRevision(expectedRevision),
              Self.validExpected(found: expectedFound, value: expectedValue),
              Self.validPayload(found: newFound, value: newValue) else {
            throw NeraeVaultError.invalidState
        }
        Self.processLock.lock()
        defer { Self.processLock.unlock() }
        let state = try loadLocked()
        guard state.revision == expected,
              state.found == expectedFound,
              state.value == expectedValue else {
            throw NeraeVaultError.conflict
        }
        return try commitLocked(state, found: newFound, value: newValue)
    }

    /// Compatibility entry for the existing UTS API. It still commits only
    /// to v2 and increments the native revision, but cannot validate a
    /// caller-held revision because the old UTS signature has no such field.
    func compareAndSetLegacy(expectedFound: Bool, expectedValue: String, value: String) throws {
        guard Self.validExpected(found: expectedFound, value: expectedValue),
              Self.validPayload(found: true, value: value) else {
            throw NeraeVaultError.invalidState
        }
        Self.processLock.lock()
        defer { Self.processLock.unlock() }
        let state = try loadLocked()
        guard state.found == expectedFound, state.value == expectedValue else {
            throw NeraeVaultError.conflict
        }
        _ = try commitLocked(state, found: true, value: value)
    }

    private func loadLocked() throws -> State {
        if let encoded = try current.read() {
            let state = try Self.decode(encoded)
            let legacyData = try legacy.read()
            guard Self.fingerprint(legacyData) == state.legacyFingerprint else {
                // A pre-versioned writer changed or recreated the v1 item.
                // Do not fall back to it or overwrite the v2 state.
                throw NeraeVaultError.unavailable
            }
            return state
        }

        guard let legacyData = try legacy.read() else {
            return State(found: false, value: "", revision: 0, legacyFingerprint: nil)
        }
        guard Self.validPayloadData(legacyData),
              let legacyValue = String(data: legacyData, encoding: .utf8) else {
            // Keep malformed v1 bytes intact for recovery; never replace them.
            throw NeraeVaultError.unavailable
        }

        let migrated = State(found: true, value: legacyValue, revision: 1,
                             legacyFingerprint: Self.fingerprint(legacyData))
        let encoded = try Self.encode(migrated)
        do {
            try current.write(encoded)
        } catch {
            // Keychain writes can have an uncertain outcome. Preserve v1 and
            // report unavailable; a later read validates the v2 item.
            throw NeraeVaultError.unavailable
        }
        guard try current.read() == encoded,
              Self.fingerprint(try legacy.read()) == migrated.legacyFingerprint else {
            throw NeraeVaultError.unavailable
        }
        return migrated
    }

    private func commitLocked(_ state: State, found: Bool, value: String) throws -> NeraeVersionedJournalRead {
        let (nextRevision, overflow) = state.revision.addingReportingOverflow(1)
        guard !overflow else { throw NeraeVaultError.unavailable }
        let next = State(found: found, value: found ? value : "", revision: nextRevision,
                         legacyFingerprint: state.legacyFingerprint)
        let encoded = try Self.encode(next)
        do {
            try current.write(encoded)
        } catch {
            // Do not claim success or roll back: the write may have committed.
            throw NeraeVaultError.unavailable
        }
        guard let saved = try current.read(),
              let verified = try? Self.decode(saved),
              Self.same(verified, next),
              Self.fingerprint(try legacy.read()) == next.legacyFingerprint else {
            throw NeraeVaultError.unavailable
        }
        return Self.readResult(verified)
    }

    private static func encode(_ state: State) throws -> Data {
        let object: [String: Any] = [
            "format": format,
            "version": 2,
            "revision": String(state.revision),
            "found": state.found,
            "value": state.value,
            "legacyFingerprint": state.legacyFingerprint ?? ""
        ]
        let data = try PropertyListSerialization.data(fromPropertyList: object, format: .binary, options: 0)
        guard data.count <= maxEnvelopeBytes else { throw NeraeVaultError.invalidState }
        return data
    }

    private static func decode(_ data: Data) throws -> State {
        guard !data.isEmpty, data.count <= maxEnvelopeBytes,
              let object = try? PropertyListSerialization.propertyList(from: data, options: [], format: nil),
              let fields = object as? [String: Any],
              Set(fields.keys) == Set(["format", "version", "revision", "found", "value", "legacyFingerprint"]),
              fields["format"] as? String == format,
              let version = fields["version"] as? NSNumber,
              CFGetTypeID(version) != CFBooleanGetTypeID(),
              !CFNumberIsFloatType(version as CFNumber), version.intValue == 2,
              let revisionText = fields["revision"] as? String,
              let revision = parseRevision(revisionText), revision > 0,
              let foundValue = fields["found"] as? NSNumber,
              CFGetTypeID(foundValue) == CFBooleanGetTypeID(),
              let value = fields["value"] as? String else {
            throw NeraeVaultError.unavailable
        }
        guard let fingerprint = fields["legacyFingerprint"] as? String else {
            throw NeraeVaultError.unavailable
        }
        let legacyFingerprint: String? = fingerprint.isEmpty ? nil : fingerprint
        if let legacyFingerprint, !isSHA256Hex(legacyFingerprint) {
            throw NeraeVaultError.unavailable
        }
        let found = foundValue.boolValue
        guard validPayload(found: found, value: value) else { throw NeraeVaultError.unavailable }
        return State(found: found, value: value, revision: revision,
                     legacyFingerprint: legacyFingerprint)
    }

    private static func validPayloadData(_ data: Data) -> Bool {
        guard data.count <= NeraeCredentialVault.maxBytes,
              String(data: data, encoding: .utf8) != nil,
              let value = try? JSONSerialization.jsonObject(with: data),
              value is [String: Any] else { return false }
        return true
    }

    private static func validPayload(found: Bool, value: String) -> Bool {
        if !found { return value.isEmpty }
        guard let data = value.data(using: .utf8), !data.isEmpty else { return false }
        return validPayloadData(data)
    }

    private static func validExpected(found: Bool, value: String) -> Bool {
        if !found { return value.isEmpty }
        return validPayload(found: true, value: value)
    }

    private static func parseRevision(_ value: String) -> Int64? {
        guard !value.isEmpty, value == "0" || (value.first != "0" && value.allSatisfy(\.isNumber)),
              value.allSatisfy({ $0 >= "0" && $0 <= "9" }) else { return nil }
        return Int64(value)
    }

    private static func fingerprint(_ data: Data?) -> String? {
        guard let data else { return nil }
        return SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    private static func isSHA256Hex(_ value: String) -> Bool {
        value.count == 64 && value.utf8.allSatisfy { (48...57).contains($0) || (97...102).contains($0) }
    }

    private static func same(_ lhs: State, _ rhs: State) -> Bool {
        lhs.found == rhs.found && lhs.value == rhs.value && lhs.revision == rhs.revision &&
            lhs.legacyFingerprint == rhs.legacyFingerprint
    }

    private static func readResult(_ state: State) -> NeraeVersionedJournalRead {
        NeraeVersionedJournalRead(found: state.found, value: state.value,
                                  revision: String(state.revision))
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
    private static let trainingOperationJournal = NeraeVersionedTrainingJournal(
        legacy: NeraeKeychainBackend(namespace: NeraeVaultNamespaces.trainingOperationJournal),
        current: NeraeKeychainBackend(namespace: NeraeVaultNamespaces.versionedTrainingOperationJournal)
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
        _ expectedFound: Bool, _ expectedValue: String, _ value: String
    ) -> String {
        do {
            try deviceIdentityRegistry.compareAndSet(
                expectedFound: expectedFound, expectedValue: expectedValue, value: value)
            return encode(ok: true)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func readTrainingOperationJournal() -> String {
        do {
            let result = try trainingOperationJournal.read()
            return encode(ok: true, found: result.found, value: result.value)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func compareAndSetTrainingOperationJournal(
        _ expectedFound: Bool, _ expectedValue: String, _ value: String
    ) -> String {
        do {
            try trainingOperationJournal.compareAndSetLegacy(
                expectedFound: expectedFound, expectedValue: expectedValue, value: value)
            return encode(ok: true)
        } catch {
            return encodeFailure(error)
        }
    }

    /// Version-aware API for the next UTS adapter. The revision is read-only
    /// output; callers supply only the revision they observed.
    @objc public static func readVersionedTrainingOperationJournal() -> String {
        do {
            let result = try trainingOperationJournal.read()
            return encodeVersioned(ok: true, found: result.found, value: result.value,
                                   revision: result.revision)
        } catch {
            return encodeVersionedFailure(error)
        }
    }

    @objc public static func compareAndSetVersionedTrainingOperationJournal(
        _ expectedRevision: String,
        _ expectedFound: Bool,
        _ expectedValue: String,
        _ newFound: Bool,
        _ newValue: String
    ) -> String {
        do {
            let result = try trainingOperationJournal.compareAndSet(
                expectedRevision: expectedRevision,
                expectedFound: expectedFound,
                expectedValue: expectedValue,
                newFound: newFound,
                newValue: newValue)
            return encodeVersioned(ok: true, found: false, value: "",
                                   revision: result.revision)
        } catch {
            return encodeVersionedFailure(error)
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

    static func encodeVersioned(ok: Bool, errorCode: String = "", found: Bool = false,
                                value: String = "", revision: String = "0") -> String {
        let result: [String: Any] = ["ok": ok, "errorCode": errorCode, "found": found,
                                     "value": value, "revision": revision]
        guard let data = try? JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]),
              let json = String(data: data, encoding: .utf8) else {
            return "{\"ok\":false,\"errorCode\":\"bridge_failure\",\"found\":false,\"value\":\"\",\"revision\":\"0\"}"
        }
        return json
    }

    static func encodeVersionedFailure(_ error: Error) -> String {
        if let vaultError = error as? NeraeVaultError, case .invalidState = vaultError {
            return encodeVersioned(ok: false, errorCode: "invalid_state", revision: "")
        }
        if let vaultError = error as? NeraeVaultError, case .conflict = vaultError {
            return encodeVersioned(ok: false, errorCode: "conflict", revision: "")
        }
        return encodeVersioned(ok: false, errorCode: "unavailable", revision: "")
    }
}
