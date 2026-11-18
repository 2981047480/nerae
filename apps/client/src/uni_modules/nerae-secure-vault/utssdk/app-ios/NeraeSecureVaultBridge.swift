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
    private static let maxPayloadBytes = NeraeCredentialVault.maxBytes
    private static let maxEnvelopeBytes = maxPayloadBytes + 1024
    private static let versionedFormat = "com.nerae.training-draft.versioned.v2"
    private static let maxRevision = "9223372036854775807"
    private let backend: NeraeDraftBackend
    init(backend: NeraeDraftBackend) { self.backend = backend }

    func read(key: String) throws -> NeraeVaultRead {
        let result = try readVersioned(key: key)
        return NeraeVaultRead(found: result.found, value: result.value)
    }

    /// Reads legacy JSON without rewriting it. Legacy items have revision zero;
    /// the first successful mutation upgrades the same Keychain item to v2.
    func readVersioned(key: String) throws -> NeraeVersionedDraftRead {
        let owner = try Self.owner(key)
        Self.lock.lock()
        defer { Self.lock.unlock() }
        guard let data = try backend.read(account: owner.account) else {
            return NeraeVersionedDraftRead(found: false, value: "", revision: "0")
        }
        let state = try Self.decode(data, owner: owner)
        guard let value = String(data: state.payload, encoding: .utf8) else { throw NeraeVaultError.unavailable }
        _ = try Self.syncPendingBytes(in: state.payload, malformedError: .unavailable)
        return NeraeVersionedDraftRead(found: true, value: value, revision: state.revision)
    }

    func create(key: String, value: String) throws -> Bool {
        let owner = try Self.owner(key)
        guard Self.valid(value, owner: owner), let payload = value.data(using: .utf8) else { throw NeraeVaultError.invalidState }
        _ = try Self.syncPendingBytes(in: payload, malformedError: .invalidState)
        Self.lock.lock()
        defer { Self.lock.unlock() }
        let data = try Self.encode(payload: payload, revision: "1")
        let created = try backend.create(account: owner.account, value: data)
        if created {
            guard let stored = try backend.read(account: owner.account) else { throw NeraeVaultError.unavailable }
            let committed = try Self.decode(stored, owner: owner)
            guard committed.revision == "1", committed.payload == payload else {
                throw NeraeVaultError.unavailable
            }
        }
        return created
    }

    func write(key: String, value: String) throws {
        let owner = try Self.owner(key)
        guard Self.valid(value, owner: owner), let payload = value.data(using: .utf8) else { throw NeraeVaultError.invalidState }
        Self.lock.lock()
        defer { Self.lock.unlock() }
        guard let previous = try backend.read(account: owner.account) else { throw NeraeVaultError.conflict }
        let old = try Self.decode(previous, owner: owner)
        try Self.requireLegacyWriterAllowed(old.payload)
        try Self.requireLegacySyncPendingPreserved(from: old.payload, to: payload)
        try Self.requireNondecreasingSchema(from: old.payload, to: payload)
        let next = try Self.nextRevision(old.revision)
        try Self.persist(payload: payload, revision: next, owner: owner, account: owner.account, backend: backend)
    }

    /// Conditionally update one existing draft within this process. A caller
    /// must still establish that its proposed changes are authorized. Failure
    /// after backend.update is an unknown outcome, not a failed comparison.
    func compareAndSet(key: String, expected: String, value: String) throws -> Bool {
        let owner = try Self.owner(key)
        guard Self.valid(expected, owner: owner), Self.valid(value, owner: owner),
              let expectedData = expected.data(using: .utf8), let data = value.data(using: .utf8) else {
            throw NeraeVaultError.invalidState
        }
        Self.lock.lock()
        defer { Self.lock.unlock() }
        guard let previous = try backend.read(account: owner.account) else { return false }
        let old = try Self.decode(previous, owner: owner)
        guard old.payload == expectedData else { return false }
        try Self.requireLegacyWriterAllowed(old.payload)
        try Self.requireLegacySyncPendingPreserved(from: old.payload, to: data)
        try Self.requireNondecreasingSchema(from: old.payload, to: data)
        let next = try Self.nextRevision(old.revision)
        try Self.persist(payload: data, revision: next, owner: owner, account: owner.account, backend: backend)
        return true
    }

    /// Revision-bound CAS. Unlike the compatibility value-only CAS, this
    /// rejects A→B→A because each successful mutation advances the revision.
    func compareAndSetVersioned(key: String, expectedRevision: String, expectedFound: Bool,
                                expectedValue: String, value: String) throws -> Bool {
        try commitVersioned(key: key, expectedRevision: expectedRevision, expectedFound: expectedFound,
                            expectedValue: expectedValue, value: value) != nil
    }

    /// Returns the revision from the same locked commit, never from a later
    /// read which may already include another writer's changes.
    func commitVersioned(key: String, expectedRevision: String, expectedFound: Bool,
                         expectedValue: String, value: String) throws -> NeraeVersionedDraftRead? {
        let owner = try Self.owner(key)
        guard Self.validRevision(expectedRevision), Self.validExpected(found: expectedFound, value: expectedValue),
              (!expectedFound || Self.valid(expectedValue, owner: owner)),
              Self.valid(value, owner: owner), let newPayload = value.data(using: .utf8) else {
            throw NeraeVaultError.invalidState
        }
        if expectedFound { _ = try Self.syncPendingBytes(in: Data(expectedValue.utf8), malformedError: .invalidState) }
        _ = try Self.syncPendingBytes(in: newPayload, malformedError: .invalidState)
        Self.lock.lock()
        defer { Self.lock.unlock() }
        let previous = try backend.read(account: owner.account)
        let old: State
        if let previous { old = try Self.decode(previous, owner: owner) }
        else { old = State(payload: Data(), revision: "0") }
        if previous != nil { _ = try Self.syncPendingBytes(in: old.payload, malformedError: .unavailable) }
        guard old.revision == expectedRevision, (previous != nil) == expectedFound,
              old.payload == Data(expectedValue.utf8) else { return nil }
        if previous != nil { try Self.requireNondecreasingSchema(from: old.payload, to: newPayload) }
        let next = try Self.nextRevision(old.revision)
        let encoded = try Self.encode(payload: newPayload, revision: next)
        if previous == nil {
            let created = try backend.create(account: owner.account, value: encoded)
            guard created else { return nil }
        } else {
            try backend.update(account: owner.account, value: encoded)
        }
        guard let stored = try backend.read(account: owner.account) else { throw NeraeVaultError.unavailable }
        let committed = try Self.decode(stored, owner: owner)
        guard committed.revision == next, committed.payload == newPayload else {
            throw NeraeVaultError.unavailable
        }
        return NeraeVersionedDraftRead(found: true, value: value, revision: next)
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
            guard data.count <= Self.maxEnvelopeBytes,
                  let decoded = try? Self.decodeUnowned(data),
                  let parsed = try? JSONSerialization.jsonObject(with: decoded.payload), let envelope = parsed as? [String: Any],
                  let itemSubject = envelope["subjectId"] as? String,
                  let draft = envelope["draft"] as? [String: Any], let event = draft["clientEventId"] as? String,
                  let itemOwner = try? Self.owner("nerae.workout.draft.v1.\(itemSubject).\(event)"),
                  itemOwner.account == account, let payload = String(data: decoded.payload, encoding: .utf8),
                  Self.valid(payload, owner: itemOwner) else { hasUnclassifiedItems = true; continue }
            do { _ = try Self.syncPendingBytes(in: decoded.payload, malformedError: .unavailable) }
            catch { hasUnclassifiedItems = true; continue }
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
    private struct State { let payload: Data; let revision: String }
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
        valid(value, owner: Optional<Owner>.some(owner))
    }

    private static func valid(_ value: String, owner: Owner?) -> Bool {
        guard let data = value.data(using: .utf8), validPayload(data),
              let raw = try? JSONSerialization.jsonObject(with: data), let envelope = raw as? [String: Any],
              Set(envelope.keys) == Set(["schema", "subjectId", "draft"]),
              let schema = envelope["schema"] as? NSNumber, CFGetTypeID(schema) != CFBooleanGetTypeID(),
              (1...4).contains(schema.intValue), schema.doubleValue == Double(schema.intValue),
              let subject = envelope["subjectId"] as? String,
              let draft = envelope["draft"] as? [String: Any], let event = draft["clientEventId"] as? String else { return false }
        if let owner {
            guard subject.utf8.elementsEqual(owner.subject.utf8), event.utf8.elementsEqual(owner.event.utf8) else { return false }
        }
        return true
    }

    /// Once an item uses the newer payload schema, an older writer must not
    /// replace it with a representation that cannot preserve its contents.
    private static func requireNondecreasingSchema(from previous: Data, to proposed: Data) throws {
        func version(_ data: Data) -> Int? {
            guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let number = root["schema"] as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(),
                  (1...4).contains(number.intValue),
                  number.doubleValue == Double(number.intValue) else { return nil }
            return number.intValue
        }
        guard let old = version(previous), let next = version(proposed), next >= old else {
            throw NeraeVaultError.invalidState
        }
    }

    /// Tracked drafts require revision-bound writes; compatibility APIs cannot
    /// preserve the change-tracking guarantees needed for cloud adoption.
    private static func requireLegacyWriterAllowed(_ previous: Data) throws {
        guard let root = try? JSONSerialization.jsonObject(with: previous) as? [String: Any],
              let number = root["schema"] as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(),
              number.doubleValue == Double(number.intValue), (1...3).contains(number.intValue) else {
            throw NeraeVaultError.invalidState
        }
    }

    /// Compatibility writers have no revision-bound authority to create,
    /// change, or settle a dispatch marker. They may edit ordinary draft
    /// fields only while preserving the marker's exact UTF-8 value bytes.
    private static func requireLegacySyncPendingPreserved(from previous: Data, to proposed: Data) throws {
        let oldValue = try syncPendingBytes(in: previous, malformedError: .unavailable)
        let newValue = try syncPendingBytes(in: proposed, malformedError: .invalidState)
        switch (oldValue, newValue) {
        case (nil, nil):
            return
        case let (old?, new?) where old.elementsEqual(new):
            return
        default:
            throw NeraeVaultError.invalidState
        }
    }

    /// Returns nil only for an unambiguous absent field. Duplicate draft or
    /// syncPending keys and non-string markers are not treated as absence.
    private static func syncPendingBytes(in payload: Data, malformedError: NeraeVaultError) throws -> Data? {
        guard let value = String(data: payload, encoding: .utf8),
              let raw = try? JSONSerialization.jsonObject(with: payload),
              let envelope = raw as? [String: Any],
              let draft = envelope["draft"] as? [String: Any] else {
            throw malformedError
        }
        // Foundation accepts duplicate keys. Scan only the two keys that
        // decide whether a pending request exists, preserving compatibility
        // with unrelated fields in older drafts.
        var scanner = NeraeDraftPendingScanner(bytes: Array(value.utf8))
        switch scanner.scan() {
        case .absent:
            guard !draft.keys.contains("syncPending") else { throw malformedError }
            return nil
        case .value(let bytes):
            guard let marker = draft["syncPending"] as? String,
                  bytes.elementsEqual(marker.utf8) else { throw malformedError }
            return bytes
        case .invalid:
            throw malformedError
        }
    }

    private static func validExpected(found: Bool, value: String) -> Bool {
        found ? valid(value, owner: Optional<Owner>.none) : value.isEmpty
    }

    private static func validPayload(_ data: Data) -> Bool {
        !data.isEmpty && data.count <= maxPayloadBytes && String(data: data, encoding: .utf8) != nil
    }

    private static func validRevision(_ value: String) -> Bool {
        guard value.range(of: #"\A(?:0|[1-9][0-9]{0,18})\z"#, options: .regularExpression) != nil else { return false }
        return value.count < 19 || value <= maxRevision
    }

    private static func nextRevision(_ value: String) throws -> String {
        guard validRevision(value), value != maxRevision else { throw NeraeVaultError.unavailable }
        var digits = Array(value.utf8)
        for index in digits.indices.reversed() {
            if digits[index] < 57 { digits[index] += 1; return String(decoding: digits, as: UTF8.self) }
            digits[index] = 48
        }
        let next = "1" + String(decoding: digits, as: UTF8.self)
        guard validRevision(next) else { throw NeraeVaultError.unavailable }
        return next
    }

    private static func encode(payload: Data, revision: String) throws -> Data {
        guard !payload.isEmpty, payload.count <= maxPayloadBytes, validRevision(revision), revision != "0" else {
            throw NeraeVaultError.invalidState
        }
        let object: [String: Any] = ["format": versionedFormat, "payload": payload, "revision": revision]
        let encoded = try PropertyListSerialization.data(fromPropertyList: object, format: .binary, options: 0)
        guard encoded.count <= maxEnvelopeBytes else { throw NeraeVaultError.invalidState }
        return encoded
    }

    private static func decode(_ data: Data, owner: Owner) throws -> State {
        guard data.count <= maxEnvelopeBytes else { throw NeraeVaultError.unavailable }
        let state = try decodeUnowned(data)
        guard let value = String(data: state.payload, encoding: .utf8),
              Self.valid(value, owner: owner) else { throw NeraeVaultError.unavailable }
        return state
    }

    private static func decodeUnowned(_ data: Data) throws -> State {
        guard !data.isEmpty, data.count <= maxEnvelopeBytes else { throw NeraeVaultError.unavailable }
        if data.starts(with: Data("bplist00".utf8)) {
            guard let object = try? PropertyListSerialization.propertyList(from: data, options: [], format: nil) as? [String: Any],
                  Set(object.keys) == Set(["format", "payload", "revision"]),
                  object["format"] as? String == versionedFormat,
                  let payload = object["payload"] as? Data, validPayload(payload),
                  let revision = object["revision"] as? String, validRevision(revision), revision != "0" else {
                throw NeraeVaultError.unavailable
            }
            return State(payload: payload, revision: revision)
        }
        guard validPayload(data) else {
            throw NeraeVaultError.unavailable
        }
        return State(payload: data, revision: "0")
    }

    private static func persist(payload: Data, revision: String, owner: Owner, account: String,
                                backend: NeraeDraftBackend) throws {
        let encoded = try encode(payload: payload, revision: revision)
        try backend.update(account: account, value: encoded)
        guard let stored = try backend.read(account: account) else { throw NeraeVaultError.unavailable }
        let committed = try decode(stored, owner: owner)
        guard committed.revision == revision, committed.payload == payload else { throw NeraeVaultError.unavailable }
    }
}

/// Checks the root schema/draft keys and the draft's syncPending field. The containing
/// JSON is first validated by Foundation. Nested values are skipped with an
/// explicit stack so old, deeply nested unknown fields do not recurse here.
private struct NeraeDraftPendingScanner {
    enum Result { case absent, value(Data), invalid }
    let bytes: [UInt8]
    private var index = 0

    init(bytes: [UInt8]) { self.bytes = bytes }

    mutating func scan() -> Result {
        whitespace()
        guard take(0x7b) else { return .invalid }
        var seenDraft = false
        var seenSchema = false
        var result: Result = .absent
        whitespace()
        if take(0x7d) { return .invalid }
        while true {
            whitespace()
            guard let key = string(), colon() else { return .invalid }
            if key == "schema" {
                guard !seenSchema else { return .invalid }
                seenSchema = true
                guard skipValue() else { return .invalid }
            } else if key == "draft" {
                guard !seenDraft else { return .invalid }
                seenDraft = true
                result = draft()
                if case .invalid = result { return .invalid }
            } else if !skipValue() { return .invalid }
            whitespace()
            if take(0x7d) { break }
            guard take(0x2c) else { return .invalid }
        }
        whitespace()
        return seenDraft && seenSchema && index == bytes.count ? result : .invalid
    }

    private mutating func draft() -> Result {
        whitespace()
        guard take(0x7b) else { return .invalid }
        var seen = false
        var result: Result = .absent
        whitespace()
        if take(0x7d) { return result }
        while true {
            whitespace()
            guard let key = string(), colon() else { return .invalid }
            if key == "syncPending" {
                guard !seen else { return .invalid }
                seen = true
                whitespace()
                guard let value = string() else { return .invalid }
                result = .value(Data(value.utf8))
            } else if !skipValue() { return .invalid }
            whitespace()
            if take(0x7d) { return result }
            guard take(0x2c) else { return .invalid }
        }
    }

    private mutating func string() -> String? {
        guard let range = stringRange() else { return nil }
        var wrapped = Data([0x5b])
        wrapped.append(contentsOf: bytes[range])
        wrapped.append(0x5d)
        guard let values = try? JSONSerialization.jsonObject(with: wrapped) as? [String],
              values.count == 1 else { return nil }
        return values[0]
    }

    private mutating func stringRange() -> Range<Int>? {
        guard index < bytes.count, bytes[index] == 0x22 else { return nil }
        let start = index
        index += 1
        while index < bytes.count {
            let byte = bytes[index]
            index += 1
            if byte == 0x22 { return start..<index }
            if byte == 0x5c {
                guard index < bytes.count else { return nil }
                index += 1
            }
        }
        return nil
    }

    private mutating func skipValue() -> Bool {
        whitespace()
        guard index < bytes.count else { return false }
        if bytes[index] == 0x22 { return stringRange() != nil }
        if bytes[index] == 0x7b || bytes[index] == 0x5b {
            var closes: [UInt8] = [bytes[index] == 0x7b ? 0x7d : 0x5d]
            index += 1
            while index < bytes.count {
                let byte = bytes[index]
                if byte == 0x22 {
                    guard stringRange() != nil else { return false }
                } else if byte == 0x7b || byte == 0x5b {
                    closes.append(byte == 0x7b ? 0x7d : 0x5d)
                    index += 1
                } else if byte == 0x7d || byte == 0x5d {
                    guard closes.popLast() == byte else { return false }
                    index += 1
                    if closes.isEmpty { return true }
                } else {
                    index += 1
                }
            }
            return false
        }
        let start = index
        while index < bytes.count && ![0x20, 0x09, 0x0a, 0x0d, 0x2c, 0x5d, 0x7d].contains(bytes[index]) {
            index += 1
        }
        return index > start
    }

    private mutating func colon() -> Bool { whitespace(); return take(0x3a) }
    private mutating func whitespace() {
        while index < bytes.count && [0x20, 0x09, 0x0a, 0x0d].contains(bytes[index]) { index += 1 }
    }
    private mutating func take(_ byte: UInt8) -> Bool {
        guard index < bytes.count, bytes[index] == byte else { return false }
        index += 1
        return true
    }
}

struct NeraeVersionedDraftRead: CustomStringConvertible, CustomDebugStringConvertible {
    let found: Bool
    let value: String
    let revision: String
    var description: String { "NeraeVersionedDraftRead{redacted}" }
    var debugDescription: String { description }
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
              state.value.utf8.elementsEqual(expectedValue.utf8) else {
            throw NeraeVaultError.conflict
        }
        guard state.dispatchHistoryKnown, state.activeBindingsKnown else { throw NeraeVaultError.unavailable }
        guard Self.preservesActiveOperations(state: state, nextFound: newFound, nextValue: newValue) else {
            throw NeraeVaultError.invalidState
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
        guard state.revision == expected, state.found,
              state.value.utf8.elementsEqual(expectedValue.utf8) else {
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
        guard state.found == expectedFound, state.value.utf8.elementsEqual(expectedValue.utf8) else {
            throw NeraeVaultError.conflict
        }
        guard state.dispatchHistoryKnown, state.activeBindingsKnown else { throw NeraeVaultError.unavailable }
        guard Self.preservesActiveOperations(state: state, nextFound: true, nextValue: value) else {
            throw NeraeVaultError.invalidState
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

    private static func equalRegistryEntries(_ first: [String: String], _ second: [String: String]) -> Bool {
        guard first.count == second.count else { return false }
        return first.allSatisfy { key, value in
            second.contains { otherKey, otherValue in
                key.utf8.elementsEqual(otherKey.utf8) && value.utf8.elementsEqual(otherValue.utf8)
            }
        }
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
              let newValue = newEntries.removeValue(forKey: operationKey), equalRegistryEntries(oldEntries, newEntries) else {
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

    /// Ordinary and compatibility CAS may update other entries and may record
    /// the one-way in_flight -> uncertain transition. They may never erase or
    /// rewrite a first-send request or its native active binding. Unknown
    /// historical formats fail closed because their dispatch state cannot be
    /// reconstructed from the caller-controlled payload.
    private static func preservesActiveOperations(state: State, nextFound: Bool, nextValue: String) -> Bool {
        guard state.dispatchHistoryKnown, state.activeBindingsKnown else { return false }
        if state.activeDispatchBindings.isEmpty {
            return state.activeDispatchEvents.isEmpty
        }
        guard state.found, nextFound,
              let oldEntries = journalEntries(state.value),
              let nextEntries = journalEntries(nextValue) else { return false }

        var matched = Set<String>()
        for (operationKey, oldRaw) in oldEntries {
            let eventMarker = dispatchMarker("event", [operationKey])
            guard let binding = state.activeDispatchBindings[eventMarker] else { continue }
            guard matched.insert(eventMarker).inserted,
                  let nextRaw = nextEntries[operationKey],
                  let oldOperation = try? jsonObject(oldRaw),
                  let nextOperation = try? jsonObject(nextRaw),
                  validActiveOperation(oldOperation, key: operationKey, binding: binding, history: Set(state.dispatchMarkers)),
                  validActiveOperation(nextOperation, key: operationKey, binding: binding, history: Set(state.dispatchMarkers)),
                  let oldRevision = safeInteger(oldOperation["revision"]),
                  let nextRevision = safeInteger(nextOperation["revision"]),
                  activeTransitionIsAllowed(oldOperation, nextOperation, oldRevision: oldRevision, nextRevision: nextRevision),
                  immutableActiveFieldsEqual(oldOperation, nextOperation) else { return false }
        }
        return matched == Set(state.activeDispatchBindings.keys) &&
            Set(state.activeDispatchEvents) == Set(state.activeDispatchBindings.keys)
    }

    private static func journalEntries(_ value: String) -> [String: String]? {
        guard NeraeStrictJSON.isCanonicalJournalObject(value),
              let root = try? jsonObject(value), Set(root.keys) == Set(["schema", "entries"]),
              exactInteger(root["schema"], 1),
              let entries = root["entries"] as? [String: String], entries.count <= 64,
              entries.values.allSatisfy(NeraeStrictJSON.isCanonicalJournalObject) else { return nil }
        return entries
    }

    private static func validActiveOperation(_ operation: [String: Any], key: String,
                                             binding: String, history: Set<String>) -> Bool {
        guard let state = operation["state"] as? String,
              let revision = safeInteger(operation["revision"]),
              let subject = operation["subjectId"] as? String,
              let event = operation["clientEventId"] as? String,
              let createdGeneration = operation["createdGeneration"] as? String,
              let createdAt = safeInteger(operation["createdAt"]),
              let expiresAt = safeInteger(operation["expiresAt"]), expiresAt > createdAt,
              let request = operation["request"] as? [String: Any],
              Set(request.keys) == Set(["method", "path", "idempotencyKey", "body"]),
              request["method"] as? String == "POST",
              let path = request["path"] as? String, validWorkoutPath(path),
              let idempotencyKey = request["idempotencyKey"] as? String,
              (8...128).contains(idempotencyKey.utf8.count), idempotencyKey.utf8.allSatisfy(isIdempotencyByte),
              let body = request["body"] as? String,
              let normalized = try? normalizedBody(body), validRequestBody(path: path, body: body, event: event),
              exactInteger(operation["schema"], 1), validGeneration(createdGeneration), validSubject(subject),
              (8...64).contains(event.unicodeScalars.count), !event.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              let encodedEvent = event.addingPercentEncoding(withAllowedCharacters: componentCharacters),
              key == "nerae.workout.operation.v1.\(subject).\(encodedEvent)" else { return false }

        let common = Set(["schema", "subjectId", "clientEventId", "createdGeneration", "revision", "state", "createdAt", "expiresAt", "request"])
        if state == "in_flight" {
            guard revision == 2, Set(operation.keys) == common.union(["attemptGeneration"]),
                  let attempt = operation["attemptGeneration"] as? String, validGeneration(attempt) else { return false }
        } else if state == "uncertain" {
            guard revision >= 3, Set(operation.keys) == common else { return false }
        } else { return false }

        let eventMarker = dispatchMarker("event", [key])
        let keyMarker = dispatchMarker("key", [subject, path, idempotencyKey])
        let bodyMarker = dispatchMarker("body", [subject, path, normalized])
        let expectedBinding = dispatchMarker("binding-v1", [key, subject, path, idempotencyKey, body])
        return eventMarker.utf8.count == 64 && history.contains(keyMarker) && history.contains(bodyMarker) &&
            binding.utf8.elementsEqual(expectedBinding.utf8)
    }

    private static func activeTransitionIsAllowed(_ old: [String: Any], _ next: [String: Any],
                                                  oldRevision: Int64, nextRevision: Int64) -> Bool {
        guard let oldState = old["state"] as? String, let nextState = next["state"] as? String else { return false }
        if oldState == "in_flight", nextState == "in_flight" {
            return oldRevision == 2 && nextRevision == oldRevision &&
                exactTextEqual(old["attemptGeneration"] as? String, next["attemptGeneration"] as? String)
        }
        if oldState == "in_flight", nextState == "uncertain" {
            return oldRevision == 2 && nextRevision == 3 && next["attemptGeneration"] == nil
        }
        if oldState == "uncertain", nextState == "uncertain" {
            return nextRevision == oldRevision && next["attemptGeneration"] == nil
        }
        return false
    }

    private static func immutableActiveFieldsEqual(_ old: [String: Any], _ next: [String: Any]) -> Bool {
        var oldFields = old, nextFields = next
        for key in ["state", "revision", "attemptGeneration", "request"] {
            oldFields.removeValue(forKey: key); nextFields.removeValue(forKey: key)
        }
        guard exactTextEqual(old["subjectId"] as? String, next["subjectId"] as? String),
              exactTextEqual(old["clientEventId"] as? String, next["clientEventId"] as? String),
              exactTextEqual(old["createdGeneration"] as? String, next["createdGeneration"] as? String),
              let oldRequest = old["request"] as? [String: Any],
              let nextRequest = next["request"] as? [String: Any],
              ["method", "path", "idempotencyKey", "body"].allSatisfy({
                  exactTextEqual(oldRequest[$0] as? String, nextRequest[$0] as? String)
              }) else { return false }
        return equalJSON(oldFields, nextFields)
    }

    private static func exactTextEqual(_ lhs: String?, _ rhs: String?) -> Bool {
        guard let lhs, let rhs else { return lhs == nil && rhs == nil }
        return lhs.utf8.elementsEqual(rhs.utf8)
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
        if path == "/me/recordings" || path.hasPrefix("/me/recordings/") {
            return NeraeRecordingV2JournalValidator.validate(path: path, body: fields, event: event)
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
        if path == "/me/sessions" || path == "/me/recordings" { return true }
        guard let match = path.range(of: #"^/me/(?:sessions|recordings)/[1-9][0-9]{0,19}/(sets|finish)$"#, options: .regularExpression) else {
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
        lhs.found == rhs.found && lhs.value.utf8.elementsEqual(rhs.value.utf8) && lhs.revision == rhs.revision &&
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
    private static let authenticationStateGate = NeraeAuthenticationStateGenerationGate(vault: authenticationStateVault)
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
        writeTrainingDraft(in: trainingDrafts, key: key, value: value)
    }

    /// Unversioned writes can restore a stale draft after a pending marker is
    /// cleared. App callers must use the revision-bound CAS entry instead.
    static func writeTrainingDraft(in store: NeraeProtectedDraftStore, key: String, value: String) -> String {
        encodeFailure(NeraeVaultError.unavailable)
    }

    @objc public static func readVersionedTrainingDraft(_ key: String) -> String {
        readVersionedTrainingDraft(in: trainingDrafts, key: key)
    }

    static func readVersionedTrainingDraft(in store: NeraeProtectedDraftStore, key: String) -> String {
        do {
            let result = try store.readVersioned(key: key)
            return encodeVersioned(ok: true, found: result.found, value: result.value, revision: result.revision)
        } catch { return encodeVersionedFailure(error) }
    }

    @objc public static func compareAndSetVersionedTrainingDraft(
        _ key: String, _ expectedRevision: String, _ expectedFound: Bool,
        _ expectedValue: String, _ value: String
    ) -> String {
        compareAndSetVersionedTrainingDraft(in: trainingDrafts, key: key, expectedRevision: expectedRevision,
                                            expectedFound: expectedFound, expectedValue: expectedValue, value: value)
    }

    static func compareAndSetVersionedTrainingDraft(
        in store: NeraeProtectedDraftStore, key: String, expectedRevision: String,
        expectedFound: Bool, expectedValue: String, value: String
    ) -> String {
        do {
            guard let result = try store.commitVersioned(key: key, expectedRevision: expectedRevision,
                expectedFound: expectedFound, expectedValue: expectedValue, value: value) else {
                throw NeraeVaultError.conflict
            }
            return encodeVersioned(ok: true, revision: result.revision)
        } catch { return encodeVersionedFailure(error) }
    }

    @objc public static func readAuthenticationState() -> String {
        do {
            let result = try authenticationStateGate.snapshot()
            return encode(ok: true, found: result.found, value: result.encodedState)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func writeAuthenticationState(_ value: String) -> String {
        do {
            try authenticationStateGate.write(value)
            return encode(ok: true)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func clearAuthenticationState() -> String {
        do {
            try authenticationStateGate.clear()
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
