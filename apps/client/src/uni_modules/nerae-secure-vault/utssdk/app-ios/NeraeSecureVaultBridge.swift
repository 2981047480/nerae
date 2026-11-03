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

// Each draft is a separate Keychain item, so its 64 KiB bound is not shared
// by every workout. The account is a digest rather than a user/event string.
protocol NeraeDraftBackend {
    func read(account: String) throws -> Data?
    func create(account: String, value: Data) throws -> Bool
    func update(account: String, value: Data) throws
}

final class NeraeKeychainDraftBackend: NeraeDraftBackend {
    private let service = "com.nerae.training-draft.v1"

    private func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service, kSecAttrAccount as String: account,
         kSecAttrSynchronizable as String: kCFBooleanFalse as Any]
    }

    func read(account: String) throws -> Data? {
        var request = query(account)
        request[kSecReturnData as String] = kCFBooleanTrue
        request[kSecReturnAttributes as String] = kCFBooleanTrue
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var raw: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &raw)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let attributes = raw as? [String: Any],
              let access = attributes[kSecAttrAccessible as String],
              CFEqual(access as CFTypeRef, kSecAttrAccessibleWhenUnlockedThisDeviceOnly),
              let data = attributes[kSecValueData as String] as? Data else { throw NeraeVaultError.unavailable }
        return data
    }

    func create(account: String, value: Data) throws -> Bool {
        var item = query(account)
        item[kSecValueData as String] = value
        item[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let status = SecItemAdd(item as CFDictionary, nil)
        if status == errSecDuplicateItem { return false }
        guard status == errSecSuccess else { throw NeraeVaultError.unavailable }
        return true
    }

    func update(account: String, value: Data) throws {
        // Validate the existing item's protection before updating; never
        // turn a missing draft into an unconditional new item.
        guard try read(account: account) != nil else { throw NeraeVaultError.conflict }
        let fields = [kSecValueData as String: value] as CFDictionary
        let status = SecItemUpdate(query(account) as CFDictionary, fields)
        guard status == errSecSuccess else {
            if status == errSecItemNotFound { throw NeraeVaultError.conflict }
            throw NeraeVaultError.unavailable
        }
    }
}

final class NeraeProtectedDraftStore {
    private static let lock = NSLock()
    private let backend: NeraeDraftBackend
    init(backend: NeraeDraftBackend) { self.backend = backend }

    func read(key: String) throws -> NeraeVaultRead {
        let owner = try Self.owner(key)
        Self.lock.lock()
        defer { Self.lock.unlock() }
        guard let data = try backend.read(account: owner.account) else { return NeraeVaultRead(found: false, value: "") }
        guard let value = String(data: data, encoding: .utf8), Self.valid(value, owner: owner) else { throw NeraeVaultError.unavailable }
        return NeraeVaultRead(found: true, value: value)
    }

    func create(key: String, value: String) throws -> Bool {
        let owner = try Self.owner(key)
        guard Self.valid(value, owner: owner), let data = value.data(using: .utf8) else { throw NeraeVaultError.invalidState }
        Self.lock.lock()
        defer { Self.lock.unlock() }
        let created = try backend.create(account: owner.account, value: data)
        if created {
            guard try backend.read(account: owner.account) == data else { throw NeraeVaultError.unavailable }
        }
        return created
    }

    func write(key: String, value: String) throws {
        let owner = try Self.owner(key)
        guard Self.valid(value, owner: owner), let data = value.data(using: .utf8) else { throw NeraeVaultError.invalidState }
        Self.lock.lock()
        defer { Self.lock.unlock() }
        guard let previous = try backend.read(account: owner.account) else { throw NeraeVaultError.conflict }
        guard let previousValue = String(data: previous, encoding: .utf8), Self.valid(previousValue, owner: owner) else {
            throw NeraeVaultError.unavailable
        }
        try backend.update(account: owner.account, value: data)
        guard try backend.read(account: owner.account) == data else { throw NeraeVaultError.unavailable }
    }

    private struct Owner { let subject: String; let event: String; let account: String }
    private static func owner(_ key: String) throws -> Owner {
        let prefix = "nerae.workout.draft.v1."
        guard key.hasPrefix(prefix), key.utf8.count <= 1024 else { throw NeraeVaultError.invalidState }
        let rest = key.dropFirst(prefix.count)
        guard let separator = rest.firstIndex(of: ".") else { throw NeraeVaultError.invalidState }
        let subject = String(rest[..<separator])
        let event = String(rest[rest.index(after: separator)...])
        guard !subject.isEmpty, subject.utf8.count <= 20, subject.first != "0",
              subject.utf8.allSatisfy({ (48...57).contains($0) }),
              (8...64).contains(event.unicodeScalars.count),
              !event.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { throw NeraeVaultError.invalidState }
        let digest = SHA256.hash(data: Data(key.utf8)).map { String(format: "%02x", $0) }.joined()
        return Owner(subject: subject, event: event, account: digest)
    }

    private static func valid(_ value: String, owner: Owner) -> Bool {
        guard let data = value.data(using: .utf8), !data.isEmpty, data.count <= NeraeCredentialVault.maxBytes,
              let raw = try? JSONSerialization.jsonObject(with: data), let envelope = raw as? [String: Any],
              Set(envelope.keys) == Set(["schema", "subjectId", "draft"]),
              let schema = envelope["schema"] as? NSNumber, CFGetTypeID(schema) != CFBooleanGetTypeID(),
              schema.intValue == 1, schema.doubleValue == 1,
              envelope["subjectId"] as? String == owner.subject,
              let draft = envelope["draft"] as? [String: Any], draft["clientEventId"] as? String == owner.event else { return false }
        return true
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
        // This metadata belongs to the native envelope, not to the caller's
        // replaceable registry payload. Unknown history never permits a claim.
        let dispatchHistoryKnown: Bool
        let dispatchMarkers: [String]
        let activeDispatchEvents: [String]
    }

    private static let processLock = NSRecursiveLock()
    private static let format = "com.nerae.training-operation-journal"
    // Binary plist may represent a Unicode String as UTF-16, while the
    // existing public payload limit is measured in UTF-8 bytes.
    // Request markers remain after settlement. Active events cannot advance
    // until a future trusted settlement operation explicitly clears them.
    private static let maxDispatchMarkers = 8192
    private static let maxActiveDispatchEvents = 64
    private static let maxEnvelopeBytes = (NeraeCredentialVault.maxBytes * 2) + ((maxDispatchMarkers + maxActiveDispatchEvents) * 80) + 2048

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

    /// Records a first-send marker and the ready -> in-flight registry change
    /// in one native item update. A return is a permit only when the write and
    /// read-back both succeed. Legacy histories are intentionally unavailable.
    func claimFirstSend(expectedRevision: String, expectedValue: String,
                        operationKey: String, nextValue: String) throws -> NeraeVersionedJournalRead {
        guard let expected = Self.parseRevision(expectedRevision),
              Self.validPayload(found: true, value: expectedValue),
              Self.validPayload(found: true, value: nextValue) else {
            throw NeraeVaultError.invalidState
        }
        Self.processLock.lock()
        defer { Self.processLock.unlock() }
        let state = try loadLocked()
        guard state.dispatchHistoryKnown else { throw NeraeVaultError.unavailable }
        guard state.revision == expected, state.found, state.value == expectedValue else {
            throw NeraeVaultError.conflict
        }
        let identity = try Self.dispatchIdentity(before: expectedValue, after: nextValue, operationKey: operationKey)
        guard !state.activeDispatchEvents.contains(identity.event),
              identity.history.allSatisfy({ !state.dispatchMarkers.contains($0) }) else { throw NeraeVaultError.conflict }
        return try commitLocked(state, found: true, value: nextValue,
                                addDispatchMarkers: identity.history, addActiveEvent: identity.event)
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
            return State(found: false, value: "", revision: 0, legacyFingerprint: nil,
                         dispatchHistoryKnown: true, dispatchMarkers: [], activeDispatchEvents: [])
        }
        guard Self.validPayloadData(legacyData),
              let legacyValue = String(data: legacyData, encoding: .utf8) else {
            // Keep malformed v1 bytes intact for recovery; never replace them.
            throw NeraeVaultError.unavailable
        }

        let migrated = State(found: true, value: legacyValue, revision: 1,
                             legacyFingerprint: Self.fingerprint(legacyData),
                             dispatchHistoryKnown: false, dispatchMarkers: [], activeDispatchEvents: [])
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

    private func commitLocked(_ state: State, found: Bool, value: String,
                              addDispatchMarkers: [String] = [], addActiveEvent: String? = nil) throws -> NeraeVersionedJournalRead {
        let (nextRevision, overflow) = state.revision.addingReportingOverflow(1)
        guard !overflow else { throw NeraeVaultError.unavailable }
        var markers = state.dispatchMarkers
        var activeEvents = state.activeDispatchEvents
        if !addDispatchMarkers.isEmpty || addActiveEvent != nil {
            guard state.dispatchHistoryKnown, addDispatchMarkers.count == 2,
                  Set(addDispatchMarkers).count == addDispatchMarkers.count,
                  addDispatchMarkers.allSatisfy(Self.isSHA256Hex),
                  addDispatchMarkers.allSatisfy({ !markers.contains($0) }),
                  let event = addActiveEvent, Self.isSHA256Hex(event), !activeEvents.contains(event),
                  activeEvents.count < Self.maxActiveDispatchEvents,
                  markers.count <= Self.maxDispatchMarkers - addDispatchMarkers.count else {
                throw NeraeVaultError.unavailable
            }
            markers.append(contentsOf: addDispatchMarkers)
            markers.sort()
            activeEvents.append(event)
            activeEvents.sort()
        }
        let next = State(found: found, value: found ? value : "", revision: nextRevision,
                         legacyFingerprint: state.legacyFingerprint,
                         dispatchHistoryKnown: state.dispatchHistoryKnown, dispatchMarkers: markers,
                         activeDispatchEvents: activeEvents)
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
            "version": 3,
            "revision": String(state.revision),
            "found": state.found,
            "value": state.value,
            "legacyFingerprint": state.legacyFingerprint ?? "",
            "dispatchHistoryKnown": state.dispatchHistoryKnown,
            "dispatchMarkers": state.dispatchMarkers,
            "activeDispatchEvents": state.activeDispatchEvents
        ]
        let data = try PropertyListSerialization.data(fromPropertyList: object, format: .binary, options: 0)
        guard data.count <= maxEnvelopeBytes else { throw NeraeVaultError.invalidState }
        return data
    }

    private static func decode(_ data: Data) throws -> State {
        guard !data.isEmpty, data.count <= maxEnvelopeBytes,
              let object = try? PropertyListSerialization.propertyList(from: data, options: [], format: nil),
              let fields = object as? [String: Any],
              fields["format"] as? String == format,
              let version = fields["version"] as? NSNumber,
              CFGetTypeID(version) != CFBooleanGetTypeID(),
              !CFNumberIsFloatType(version as CFNumber),
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
        let dispatchHistoryKnown: Bool
        let dispatchMarkers: [String]
        let activeDispatchEvents: [String]
        if version.intValue == 2 {
            guard Set(fields.keys) == Set(["format", "version", "revision", "found", "value", "legacyFingerprint"]) else {
                throw NeraeVaultError.unavailable
            }
            // V2 did not track per-operation dispatch. Keep its payload but
            // do not infer that any operation was never sent.
            dispatchHistoryKnown = false
            dispatchMarkers = []
            activeDispatchEvents = []
        } else if version.intValue == 3 {
            guard Set(fields.keys) == Set(["format", "version", "revision", "found", "value", "legacyFingerprint", "dispatchHistoryKnown", "dispatchMarkers", "activeDispatchEvents"]),
                  let history = fields["dispatchHistoryKnown"] as? NSNumber,
                  CFGetTypeID(history) == CFBooleanGetTypeID(),
                  let markers = fields["dispatchMarkers"] as? [String],
                  let active = fields["activeDispatchEvents"] as? [String],
                  markers.count <= maxDispatchMarkers,
                  markers.allSatisfy(isSHA256Hex), markers == markers.sorted(),
                  Set(markers).count == markers.count,
                  active.count <= maxActiveDispatchEvents, active.allSatisfy(isSHA256Hex),
                  active == active.sorted(), Set(active).count == active.count,
                  (history.boolValue ? markers.count % 2 == 0 && active.count <= markers.count / 2 : markers.isEmpty && active.isEmpty) else {
                throw NeraeVaultError.unavailable
            }
            dispatchHistoryKnown = history.boolValue
            dispatchMarkers = markers
            activeDispatchEvents = active
        } else {
            throw NeraeVaultError.unavailable
        }
        return State(found: found, value: value, revision: revision,
                     legacyFingerprint: legacyFingerprint,
                     dispatchHistoryKnown: dispatchHistoryKnown, dispatchMarkers: dispatchMarkers,
                     activeDispatchEvents: activeDispatchEvents)
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

    private static func jsonObject(_ value: String) throws -> [String: Any] {
        guard let data = value.data(using: .utf8),
              let object = try? JSONSerialization.jsonObject(with: data),
              let fields = object as? [String: Any] else {
            throw NeraeVaultError.invalidState
        }
        return fields
    }

    private static func equalJSON(_ first: Any, _ second: Any) -> Bool {
        guard JSONSerialization.isValidJSONObject(first), JSONSerialization.isValidJSONObject(second),
              let lhs = try? JSONSerialization.data(withJSONObject: first, options: [.sortedKeys]),
              let rhs = try? JSONSerialization.data(withJSONObject: second, options: [.sortedKeys]) else {
            return false
        }
        return lhs == rhs
    }

    private static func exactInteger(_ value: Any?, _ expected: Int) -> Bool {
        guard let number = value as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(),
              !CFNumberIsFloatType(number as CFNumber) else { return false }
        return number.intValue == expected
    }

    private static func dispatchIdentity(before: String, after: String, operationKey: String) throws -> (event: String, history: [String]) {
        let previous = try jsonObject(before)
        let next = try jsonObject(after)
        guard Set(previous.keys) == Set(["schema", "entries"]), Set(next.keys) == Set(previous.keys),
              exactInteger(previous["schema"], 1), exactInteger(next["schema"], 1),
              var oldEntries = previous["entries"] as? [String: String],
              var newEntries = next["entries"] as? [String: String],
              oldEntries.count <= 64, newEntries.count <= 64,
              let oldValue = oldEntries.removeValue(forKey: operationKey),
              let newValue = newEntries.removeValue(forKey: operationKey), oldEntries == newEntries else {
            throw NeraeVaultError.invalidState
        }
        var oldOperation = try jsonObject(oldValue)
        let newOperation = try jsonObject(newValue)
        guard oldOperation["state"] as? String == "ready", exactInteger(oldOperation["revision"], 1),
              newOperation["state"] as? String == "in_flight", exactInteger(newOperation["revision"], 2),
              oldOperation["attemptGeneration"] == nil,
              let attempt = newOperation["attemptGeneration"] as? String,
              !attempt.isEmpty, attempt.utf8.count <= 128,
              attempt.rangeOfCharacter(from: .whitespacesAndNewlines) == nil,
              let subject = oldOperation["subjectId"] as? String,
              !subject.isEmpty, subject.utf8.count <= 20, subject.first != "0",
              subject.utf8.allSatisfy({ (48...57).contains($0) }),
              let event = oldOperation["clientEventId"] as? String,
              event.unicodeScalars.count >= 8, event.unicodeScalars.count <= 64,
              !event.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              let encodedEvent = event.addingPercentEncoding(withAllowedCharacters: Self.componentCharacters),
              operationKey == "nerae.workout.operation.v1.\(subject).\(encodedEvent)",
              let request = oldOperation["request"] as? [String: Any],
              Set(request.keys) == Set(["method", "path", "idempotencyKey", "body"]),
              request["method"] as? String == "POST",
              let idempotencyKey = request["idempotencyKey"] as? String,
              idempotencyKey.utf8.count >= 8, idempotencyKey.utf8.count <= 128,
              idempotencyKey.utf8.allSatisfy(Self.isIdempotencyByte),
              let path = request["path"] as? String, validWorkoutPath(path),
              let body = request["body"] as? String,
              let normalizedBody = try? normalizedBody(body) else {
            throw NeraeVaultError.invalidState
        }
        oldOperation["state"] = "in_flight"
        oldOperation["revision"] = 2
        oldOperation["attemptGeneration"] = attempt
        guard equalJSON(oldOperation, newOperation) else { throw NeraeVaultError.invalidState }
        // Length prefixes make the identity unambiguous without relying on a
        // separator that could occur inside an application identifier.
        func marker(_ kind: String, _ parts: [String]) -> String {
            var markerInput = Data("nerae.ios.training-dispatch.v1\0\(kind)\0".utf8)
            for part in parts {
                let bytes = Data(part.utf8)
                var length = UInt32(bytes.count).bigEndian
                withUnsafeBytes(of: &length) { markerInput.append(contentsOf: $0) }
                markerInput.append(bytes)
            }
            return SHA256.hash(data: markerInput).map { String(format: "%02x", $0) }.joined()
        }
        // The server scopes an idempotency key by subject and request path.
        // The body marker also omits the local event key so changing that key
        // cannot disguise the same HTTP write as a fresh operation.
        // Until a trusted settlement protocol advances this event, a second
        // claim is refused even if old CAS rewrites its request and path.
        return (marker("event", [operationKey]),
                [marker("key", [subject, path, idempotencyKey]), marker("body", [subject, path, normalizedBody])])
    }

    private static func normalizedBody(_ value: String) throws -> String {
        let object = try jsonObject(value)
        let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        guard let normalized = String(data: data, encoding: .utf8) else { throw NeraeVaultError.invalidState }
        return normalized
    }

    private static func validWorkoutPath(_ path: String) -> Bool {
        if path == "/me/sessions" { return true }
        guard let match = path.range(of: #"^/me/sessions/[1-9][0-9]{0,19}/(sets|finish)$"#, options: .regularExpression) else {
            return false
        }
        return match.lowerBound == path.startIndex && match.upperBound == path.endIndex
    }

    private static let componentCharacters: CharacterSet = {
        CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()")
    }()

    private static func isIdempotencyByte(_ byte: UInt8) -> Bool {
        (48...57).contains(byte) || (65...90).contains(byte) || (97...122).contains(byte) ||
            byte == 45 || byte == 46 || byte == 95 || byte == 126
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
            lhs.legacyFingerprint == rhs.legacyFingerprint &&
            lhs.dispatchHistoryKnown == rhs.dispatchHistoryKnown && lhs.dispatchMarkers == rhs.dispatchMarkers &&
            lhs.activeDispatchEvents == rhs.activeDispatchEvents
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
    private static let trainingDrafts = NeraeProtectedDraftStore(backend: NeraeKeychainDraftBackend())

    @objc public static func readTrainingDraft(_ key: String) -> String {
        do {
            let result = try trainingDrafts.read(key: key)
            return encode(ok: true, found: result.found, value: result.value)
        } catch { return encodeFailure(error) }
    }

    @objc public static func createTrainingDraft(_ key: String, _ value: String) -> String {
        do {
            guard try trainingDrafts.create(key: key, value: value) else { throw NeraeVaultError.conflict }
            return encode(ok: true)
        } catch { return encodeFailure(error) }
    }

    @objc public static func writeTrainingDraft(_ key: String, _ value: String) -> String {
        do { try trainingDrafts.write(key: key, value: value); return encode(ok: true) }
        catch { return encodeFailure(error) }
    }

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
