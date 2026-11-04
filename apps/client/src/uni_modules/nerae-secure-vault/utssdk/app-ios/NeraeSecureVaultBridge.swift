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
    func accounts() throws -> [String]
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

    func accounts() throws -> [String] {
        let request: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrSynchronizable as String: kCFBooleanFalse as Any,
            kSecReturnAttributes as String: kCFBooleanTrue as Any,
            kSecMatchLimit as String: kSecMatchLimitAll
        ]
        var raw: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &raw)
        if status == errSecItemNotFound { return [] }
        guard status == errSecSuccess else { throw NeraeVaultError.unavailable }
        let items: [[String: Any]]
        if let array = raw as? [[String: Any]] { items = array }
        else if let single = raw as? [String: Any] { items = [single] }
        else { throw NeraeVaultError.unavailable }
        return items.map { $0[kSecAttrAccount as String] as? String ?? "" }
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
    static let discoveryPageSize = 100
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

    // The Keychain items themselves are the only source of truth. No second
    // manifest can become detached from a successfully-created draft.
    func discover(subject: String, after cursor: String) throws -> String {
        guard Self.validSubject(subject) else { throw NeraeVaultError.invalidState }
        if !cursor.isEmpty { _ = try Self.owner("nerae.workout.draft.v1.\(subject).\(cursor)") }
        Self.lock.lock()
        defer { Self.lock.unlock() }
        let accounts = try backend.accounts()
        var seen = Set<String>()
        var events = [String]()
        var hasMore = false
        var hasUnclassifiedItems = false
        func before(_ first: String, _ second: String) -> Bool { first.utf8.lexicographicallyPrecedes(second.utf8) }
        for account in accounts {
            guard account.count == 64, account.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }),
                  seen.insert(account).inserted else { hasUnclassifiedItems = true; continue }
            guard let data = try backend.read(account: account) else { hasUnclassifiedItems = true; continue }
            guard data.count <= NeraeCredentialVault.maxBytes,
                  let value = String(data: data, encoding: .utf8),
                  let parsed = try? JSONSerialization.jsonObject(with: data), let envelope = parsed as? [String: Any],
                  let itemSubject = envelope["subjectId"] as? String,
                  let draft = envelope["draft"] as? [String: Any], let event = draft["clientEventId"] as? String,
                  let itemOwner = try? Self.owner("nerae.workout.draft.v1.\(itemSubject).\(event)"),
                  itemOwner.account == account, Self.valid(value, owner: itemOwner) else { hasUnclassifiedItems = true; continue }
            guard itemSubject == subject, cursor.isEmpty || before(cursor, event) else { continue }
            events.append(event)
            events.sort(by: before)
            if events.count > Self.discoveryPageSize { events.removeLast(); hasMore = true }
        }
        let nextCursor: Any = hasMore ? events.last! : NSNull()
        let result: [String: Any] = ["eventIds": events, "nextCursor": nextCursor, "hasUnclassifiedItems": hasUnclassifiedItems]
        let data = try JSONSerialization.data(withJSONObject: result, options: [.sortedKeys])
        guard let value = String(data: data, encoding: .utf8) else { throw NeraeVaultError.unavailable }
        return value
    }

    private struct Owner { let subject: String; let event: String; let account: String }
    private static func validSubject(_ subject: String) -> Bool {
        !subject.isEmpty && subject.utf8.count <= 20 && subject.first != "0" &&
            subject.utf8.allSatisfy({ (48...57).contains($0) })
    }
    private static func owner(_ key: String) throws -> Owner {
        let prefix = "nerae.workout.draft.v1."
        guard key.hasPrefix(prefix), key.utf8.count <= 1024 else { throw NeraeVaultError.invalidState }
        let rest = key.dropFirst(prefix.count)
        guard let separator = rest.firstIndex(of: ".") else { throw NeraeVaultError.invalidState }
        let subject = String(rest[..<separator])
        let event = String(rest[rest.index(after: separator)...])
        guard validSubject(subject),
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

// Internal data for a future authenticated replay request. It is not a
// dispatch permit, server receipt or authorization to settle a draft.
struct NeraeRecoveryIntent: CustomStringConvertible, CustomDebugStringConvertible {
    let revision: String
    let subjectId: String
    let clientEventId: String
    let operationKey: String
    let path: String
    let idempotencyKey: String
    let body: String
    var description: String { "NeraeRecoveryIntent{redacted}" }
    var debugDescription: String { description }
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
        // V4 binds each active event to one exact request, including raw body
        // bytes. An older active V3 item has no safely reconstructible binding.
        let activeBindingsKnown: Bool
        let activeDispatchBindings: [String: String]
    }

    private static let processLock = NSRecursiveLock()
    private static let format = "com.nerae.training-operation-journal"
    // Binary plist may represent a Unicode String as UTF-16, while the
    // existing public payload limit is measured in UTF-8 bytes.
    // Request markers remain after settlement. Active events cannot advance
    // until a future trusted settlement operation explicitly clears them.
    private static let maxDispatchMarkers = 8192
    private static let maxActiveDispatchEvents = 64
    private static let maxEnvelopeBytes = (NeraeCredentialVault.maxBytes * 2) + (maxDispatchMarkers * 80) + (maxActiveDispatchEvents * 256) + 2048

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
        guard state.dispatchHistoryKnown, state.activeBindingsKnown else { throw NeraeVaultError.unavailable }
        guard state.revision == expected, state.found, state.value == expectedValue else {
            throw NeraeVaultError.conflict
        }
        let identity = try Self.dispatchIdentity(before: expectedValue, after: nextValue, operationKey: operationKey)
        guard !state.activeDispatchEvents.contains(identity.event),
              identity.history.allSatisfy({ !state.dispatchMarkers.contains($0) }) else { throw NeraeVaultError.conflict }
        return try commitLocked(state, found: true, value: nextValue,
                                addDispatchMarkers: identity.history, addActiveEvent: identity.event,
                                addActiveBinding: identity.binding)
    }

    /// A consistent native snapshot only. A future caller must obtain the
    /// subject from the authenticated vault, recheck that session around
    /// network activity, and verify a server replay before any settlement.
    func recoveryIntent(operationKey: String, subject: String) throws -> NeraeRecoveryIntent {
        guard Self.validSubject(subject) else { throw NeraeVaultError.invalidState }
        Self.processLock.lock()
        defer { Self.processLock.unlock() }
        let state = try loadLocked()
        guard state.dispatchHistoryKnown, state.activeBindingsKnown, state.found else { throw NeraeVaultError.unavailable }
        let registry = try Self.jsonObject(state.value)
        guard Set(registry.keys) == Set(["schema", "entries"]), Self.exactInteger(registry["schema"], 1),
              let entries = registry["entries"] as? [String: String], entries.count <= 64,
              let entry = entries[operationKey] else { throw NeraeVaultError.conflict }
        let operation = try Self.jsonObject(entry)
        let common = Set(["schema", "subjectId", "clientEventId", "createdGeneration", "revision", "state", "createdAt", "expiresAt", "request"])
        guard Self.exactInteger(operation["schema"], 1), operation["subjectId"] as? String == subject,
              let event = operation["clientEventId"] as? String,
              (8...64).contains(event.unicodeScalars.count), !event.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              let encodedEvent = event.addingPercentEncoding(withAllowedCharacters: Self.componentCharacters),
              operationKey == "nerae.workout.operation.v1.\(subject).\(encodedEvent)",
              let createdGeneration = operation["createdGeneration"] as? String, Self.validGeneration(createdGeneration),
              let revision = Self.safeInteger(operation["revision"]),
              let createdAt = Self.safeInteger(operation["createdAt"]),
              let expiresAt = Self.safeInteger(operation["expiresAt"]), expiresAt > createdAt,
              let status = operation["state"] as? String else { throw NeraeVaultError.invalidState }
        if status == "in_flight" {
            guard Set(operation.keys) == common.union(["attemptGeneration"]), revision == 2,
                  let attempt = operation["attemptGeneration"] as? String, Self.validGeneration(attempt) else { throw NeraeVaultError.invalidState }
        } else if status == "uncertain" {
            guard Set(operation.keys) == common, revision >= 3 else { throw NeraeVaultError.invalidState }
        } else { throw NeraeVaultError.conflict }
        guard let request = operation["request"] as? [String: Any], Set(request.keys) == Set(["method", "path", "idempotencyKey", "body"]),
              request["method"] as? String == "POST", let path = request["path"] as? String, Self.validWorkoutPath(path),
              let key = request["idempotencyKey"] as? String, (8...128).contains(key.utf8.count), key.utf8.allSatisfy(Self.isIdempotencyByte),
              let body = request["body"] as? String, Self.validPayload(found: true, value: body),
              let normalized = try? Self.normalizedBody(body), Self.validRequestBody(path: path, body: body, event: event) else {
            throw NeraeVaultError.invalidState
        }
        let eventMarker = Self.dispatchMarker("event", [operationKey])
        let history = [Self.dispatchMarker("key", [subject, path, key]), Self.dispatchMarker("body", [subject, path, normalized])]
        let binding = Self.dispatchMarker("binding-v1", [operationKey, subject, path, key, body])
        guard state.activeDispatchEvents.contains(eventMarker), state.activeDispatchBindings[eventMarker] == binding,
              history.allSatisfy({ state.dispatchMarkers.contains($0) }) else { throw NeraeVaultError.conflict }
        return NeraeRecoveryIntent(revision: String(state.revision), subjectId: subject, clientEventId: event,
                                   operationKey: operationKey, path: path, idempotencyKey: key, body: body)
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
                         dispatchHistoryKnown: true, dispatchMarkers: [], activeDispatchEvents: [],
                         activeBindingsKnown: true, activeDispatchBindings: [:])
        }
        guard Self.validPayloadData(legacyData),
              let legacyValue = String(data: legacyData, encoding: .utf8) else {
            // Keep malformed v1 bytes intact for recovery; never replace them.
            throw NeraeVaultError.unavailable
        }

        let migrated = State(found: true, value: legacyValue, revision: 1,
                             legacyFingerprint: Self.fingerprint(legacyData),
                             dispatchHistoryKnown: false, dispatchMarkers: [], activeDispatchEvents: [],
                             activeBindingsKnown: false, activeDispatchBindings: [:])
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
                              addDispatchMarkers: [String] = [], addActiveEvent: String? = nil,
                              addActiveBinding: String? = nil) throws -> NeraeVersionedJournalRead {
        let (nextRevision, overflow) = state.revision.addingReportingOverflow(1)
        guard !overflow else { throw NeraeVaultError.unavailable }
        var markers = state.dispatchMarkers
        var activeEvents = state.activeDispatchEvents
        var activeBindings = state.activeDispatchBindings
        if !addDispatchMarkers.isEmpty || addActiveEvent != nil || addActiveBinding != nil {
            guard state.dispatchHistoryKnown, state.activeBindingsKnown, addDispatchMarkers.count == 2,
                  Set(addDispatchMarkers).count == addDispatchMarkers.count,
                  addDispatchMarkers.allSatisfy(Self.isSHA256Hex),
                  addDispatchMarkers.allSatisfy({ !markers.contains($0) }),
                  let event = addActiveEvent, Self.isSHA256Hex(event), !activeEvents.contains(event),
                  let binding = addActiveBinding, Self.isSHA256Hex(binding), activeBindings[event] == nil,
                  activeEvents.count < Self.maxActiveDispatchEvents,
                  markers.count <= Self.maxDispatchMarkers - addDispatchMarkers.count else {
                throw NeraeVaultError.unavailable
            }
            markers.append(contentsOf: addDispatchMarkers)
            markers.sort()
            activeEvents.append(event)
            activeEvents.sort()
            activeBindings[event] = binding
        }
        let next = State(found: found, value: found ? value : "", revision: nextRevision,
                         legacyFingerprint: state.legacyFingerprint,
                         dispatchHistoryKnown: state.dispatchHistoryKnown, dispatchMarkers: markers,
                         activeDispatchEvents: activeEvents, activeBindingsKnown: state.activeBindingsKnown,
                         activeDispatchBindings: activeBindings)
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
            "version": 4,
            "revision": String(state.revision),
            "found": state.found,
            "value": state.value,
            "legacyFingerprint": state.legacyFingerprint ?? "",
            "dispatchHistoryKnown": state.dispatchHistoryKnown,
            "dispatchMarkers": state.dispatchMarkers,
            "activeDispatchEvents": state.activeDispatchEvents,
            "activeBindingsKnown": state.activeBindingsKnown,
            "activeDispatchBindings": state.activeDispatchBindings
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
        let activeBindingsKnown: Bool
        let activeDispatchBindings: [String: String]
        if version.intValue == 2 {
            guard Set(fields.keys) == Set(["format", "version", "revision", "found", "value", "legacyFingerprint"]) else {
                throw NeraeVaultError.unavailable
            }
            // V2 did not track per-operation dispatch. Keep its payload but
            // do not infer that any operation was never sent.
            dispatchHistoryKnown = false
            dispatchMarkers = []
            activeDispatchEvents = []
            activeBindingsKnown = false
            activeDispatchBindings = [:]
        } else if version.intValue == 3 || version.intValue == 4 {
            let baseKeys = Set(["format", "version", "revision", "found", "value", "legacyFingerprint", "dispatchHistoryKnown", "dispatchMarkers", "activeDispatchEvents"])
            let expectedKeys = version.intValue == 3 ? baseKeys : baseKeys.union(["activeBindingsKnown", "activeDispatchBindings"])
            guard Set(fields.keys) == expectedKeys,
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
            if version.intValue == 3 {
                // An active V3 event has no persisted exact-request binding.
                // Preserve it, but do not infer or reconstruct that binding.
                activeBindingsKnown = history.boolValue && active.isEmpty
                activeDispatchBindings = [:]
            } else {
                guard let known = fields["activeBindingsKnown"] as? NSNumber,
                      CFGetTypeID(known) == CFBooleanGetTypeID(),
                      let bindings = fields["activeDispatchBindings"] as? [String: String],
                      (known.boolValue
                       ? history.boolValue && Set(bindings.keys) == Set(active) && bindings.values.allSatisfy(isSHA256Hex) &&
                         Set(bindings.values).count == bindings.count
                       : bindings.isEmpty) else { throw NeraeVaultError.unavailable }
                activeBindingsKnown = known.boolValue
                activeDispatchBindings = bindings
            }
        } else {
            throw NeraeVaultError.unavailable
        }
        return State(found: found, value: value, revision: revision,
                     legacyFingerprint: legacyFingerprint,
                     dispatchHistoryKnown: dispatchHistoryKnown, dispatchMarkers: dispatchMarkers,
                     activeDispatchEvents: activeDispatchEvents, activeBindingsKnown: activeBindingsKnown,
                     activeDispatchBindings: activeDispatchBindings)
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

    private static func signedSafeInteger(_ value: Any?) -> Int64? {
        guard let number = value as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(),
              !CFNumberIsFloatType(number as CFNumber) else { return nil }
        // Compare before conversion: an unsigned NSNumber above Int64.max
        // wraps via int64Value and can otherwise masquerade as a small integer.
        guard number.compare(NSNumber(value: -9_007_199_254_740_991 as Int64)) != .orderedAscending,
              number.compare(NSNumber(value: 9_007_199_254_740_991 as Int64)) != .orderedDescending else { return nil }
        return number.int64Value
    }

    private static func safeInteger(_ value: Any?) -> Int64? {
        guard let result = signedSafeInteger(value), result >= 0 else { return nil }
        return result
    }

    private static func validSubject(_ subject: String) -> Bool {
        !subject.isEmpty && subject.utf8.count <= 20 && subject.first != "0" &&
            subject.utf8.allSatisfy({ (48...57).contains($0) })
    }

    private static func validGeneration(_ generation: String) -> Bool {
        !generation.isEmpty && generation.utf8.count <= 128 &&
            generation.rangeOfCharacter(from: .whitespacesAndNewlines) == nil
    }

    private static func dispatchIdentity(before: String, after: String, operationKey: String) throws -> (event: String, history: [String], binding: String) {
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
              let normalizedBody = try? normalizedBody(body), validRequestBody(path: path, body: body, event: event) else {
            throw NeraeVaultError.invalidState
        }
        oldOperation["state"] = "in_flight"
        oldOperation["revision"] = 2
        oldOperation["attemptGeneration"] = attempt
        guard equalJSON(oldOperation, newOperation) else { throw NeraeVaultError.invalidState }
        // The server scopes an idempotency key by subject and request path.
        // The body marker also omits the local event key so changing that key
        // cannot disguise the same HTTP write as a fresh operation.
        // Until a trusted settlement protocol advances this event, a second
        // claim is refused even if old CAS rewrites its request and path.
        return (dispatchMarker("event", [operationKey]),
                [dispatchMarker("key", [subject, path, idempotencyKey]), dispatchMarker("body", [subject, path, normalizedBody])],
                dispatchMarker("binding-v1", [operationKey, subject, path, idempotencyKey, body]))
    }

    // A domain label and fixed-width lengths bind the raw UTF-8 bytes without
    // ambiguity. The binding is consistency evidence, not authentication.
    private static func dispatchMarker(_ kind: String, _ parts: [String]) -> String {
        var markerInput = Data("nerae.ios.training-dispatch.v1\0\(kind)\0".utf8)
        for part in parts {
            let bytes = Data(part.utf8)
            var length = UInt32(bytes.count).bigEndian
            withUnsafeBytes(of: &length) { markerInput.append(contentsOf: $0) }
            markerInput.append(bytes)
        }
        return SHA256.hash(data: markerInput).map { String(format: "%02x", $0) }.joined()
    }

    private static func normalizedBody(_ value: String) throws -> String {
        guard NeraeStrictJSON.isCanonicalJournalObject(value) else { throw NeraeVaultError.invalidState }
        let object = try jsonObject(value)
        let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        guard let normalized = String(data: data, encoding: .utf8) else { throw NeraeVaultError.invalidState }
        return normalized
    }

    // Call only after normalizedBody has rejected duplicate/non-ASCII keys.
    // The native shape mirrors the shared journal contract; the service still
    // performs its own full business validation and authorization.
    private static func validRequestBody(path: String, body: String, event: String) -> Bool {
        guard let fields = try? jsonObject(body) else { return false }
        func keys(_ object: [String: Any], allowed: Set<String>, required: Set<String>) -> Bool {
            let present = Set(object.keys)
            return present.isSubset(of: allowed) && required.isSubset(of: present)
        }
        func optional(_ object: [String: Any], _ key: String, _ check: (Any) -> Bool) -> Bool {
            guard let value = object[key] else { return true }
            return check(value)
        }
        func strings(_ value: Any) -> Bool { (value as? [Any])?.allSatisfy({ $0 is String }) ?? false }
        func number(_ value: Any) -> Bool {
            guard let item = value as? NSNumber, CFGetTypeID(item) != CFBooleanGetTypeID() else { return false }
            return item.doubleValue.isFinite
        }
        func trueBoolean(_ value: Any) -> Bool {
            guard let item = value as? NSNumber, CFGetTypeID(item) == CFBooleanGetTypeID() else { return false }
            return item.boolValue
        }
        func validSet(_ raw: Any) -> Bool {
            guard let item = raw as? [String: Any],
                  keys(item, allowed: Set(["recordKey", "exerciseEntryKey", "setIndex", "kind", "exerciseId", "exerciseName", "weightKg", "reps", "seconds", "rpe", "completed"]),
                       required: Set(["recordKey", "exerciseEntryKey", "setIndex", "kind"])),
                  item["recordKey"] is String, item["exerciseEntryKey"] is String, item["kind"] is String,
                  let index = safeInteger(item["setIndex"]), index >= 1,
                  (item["exerciseId"] != nil) != (item["exerciseName"] != nil),
                  optional(item, "exerciseId", { $0 is String }), optional(item, "exerciseName", { $0 is String }),
                  optional(item, "weightKg", number), optional(item, "reps", { signedSafeInteger($0) != nil }),
                  optional(item, "seconds", { signedSafeInteger($0) != nil }),
                  optional(item, "rpe", { $0 is NSNull || number($0) }), optional(item, "completed", trueBoolean) else { return false }
            return true
        }
        if path == "/me/sessions" {
            guard let source = fields["source"] as? String, let submitted = fields["clientEventId"] as? String,
                  submitted.utf8.elementsEqual(event.utf8), fields["date"] is String,
                  optional(fields, "timeZone", { $0 is String }) else { return false }
            if source.utf8.elementsEqual("free".utf8) {
                return keys(fields, allowed: Set(["source", "date", "timeZone", "clientEventId"]),
                            required: Set(["source", "date", "clientEventId"]))
            }
            if source.utf8.elementsEqual("plan".utf8) {
                return keys(fields, allowed: Set(["source", "date", "timeZone", "planId", "planVersion", "planDayId", "clientEventId", "occurrenceId"]),
                            required: Set(["source", "date", "planId", "planVersion", "planDayId", "clientEventId"])) &&
                    fields["planId"] is String && fields["planDayId"] is String &&
                    (safeInteger(fields["planVersion"]) ?? 0) >= 1 && optional(fields, "occurrenceId", { $0 is String })
            }
            return false
        }
        guard (safeInteger(fields["expectedVersion"]) ?? 0) >= 1 else { return false }
        if path.hasSuffix("/sets") {
            return keys(fields, allowed: Set(["expectedVersion", "exerciseEntryKeys", "sets", "deleteRecordKeys", "restoreRecordKeys"]),
                        required: Set(["expectedVersion", "exerciseEntryKeys"])) &&
                fields["exerciseEntryKeys"].map(strings) == true &&
                optional(fields, "sets", { ($0 as? [Any])?.allSatisfy(validSet) ?? false }) &&
                optional(fields, "deleteRecordKeys", strings) && optional(fields, "restoreRecordKeys", strings)
        }
        if path.hasSuffix("/finish") {
            return keys(fields, allowed: Set(["expectedVersion", "durationMin", "date", "rpe", "note"]), required: Set(["expectedVersion"])) &&
                optional(fields, "durationMin", { $0 is NSNull || signedSafeInteger($0) != nil }) &&
                optional(fields, "date", { $0 is String }) && optional(fields, "rpe", { $0 is NSNull || number($0) }) &&
                optional(fields, "note", { $0 is NSNull || $0 is String })
        }
        return false
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
            lhs.activeDispatchEvents == rhs.activeDispatchEvents && lhs.activeBindingsKnown == rhs.activeBindingsKnown &&
            lhs.activeDispatchBindings == rhs.activeDispatchBindings
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

    @objc public static func discoverTrainingDrafts(_ subject: String, _ cursor: String) -> String {
        do { return encode(ok: true, found: true, value: try trainingDrafts.discover(subject: subject, after: cursor)) }
        catch { return encodeFailure(error) }
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

    /// A successful response means the native journal recorded the first-send
    /// marker and the transition together. It is not a server receipt.
    @objc public static func claimTrainingOperationDispatch(
        _ expectedRevision: String, _ expectedValue: String,
        _ operationKey: String, _ nextValue: String
    ) -> String {
        claimTrainingOperationDispatch(in: trainingOperationJournal, expectedRevision: expectedRevision,
                                       expectedValue: expectedValue, operationKey: operationKey,
                                       nextValue: nextValue)
    }

    static func claimTrainingOperationDispatch(
        in journal: NeraeVersionedTrainingJournal, expectedRevision: String,
        expectedValue: String, operationKey: String, nextValue: String
    ) -> String {
        do {
            let result = try journal.claimFirstSend(expectedRevision: expectedRevision, expectedValue: expectedValue,
                                                    operationKey: operationKey, nextValue: nextValue)
            return encodeVersioned(ok: true, found: false, value: "", revision: result.revision)
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
