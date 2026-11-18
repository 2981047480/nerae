import Foundation
import CryptoKit

private enum TestFailure: Error { case assertion }
private func check(_ condition: @autoclosure () throws -> Bool) throws {
    guard try condition() else { throw TestFailure.assertion }
}
private func expectInvalid(_ action: () throws -> Void) throws {
    do { try action(); throw TestFailure.assertion }
    catch NeraeVaultError.invalidState { return }
}
private final class Backend: NeraeDraftBackend {
    var values: [String: Data] = [:]
    func accounts() throws -> [String] { values.keys.sorted() }
    func read(account: String) throws -> Data? { values[account] }
    func create(account: String, value: Data) throws -> Bool {
        guard values[account] == nil else { return false }; values[account] = value; return true
    }
    func update(account: String, value: Data) throws {
        guard values[account] != nil else { throw NeraeVaultError.conflict }; values[account] = value
    }
}
private func value(_ schema: Int, _ note: String = "", pending: String? = nil) throws -> String {
    var draft: [String: Any] = ["clientEventId": "event-0001", "note": note]
    if let pending { draft["syncPending"] = pending }
    let object: [String: Any] = ["schema": schema, "subjectId": "101", "draft": draft]
    return String(decoding: try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]), as: UTF8.self)
}
@main struct Main {
    static func main() throws {
        let backend = Backend(); let store = NeraeProtectedDraftStore(backend: backend)
        let key = "nerae.workout.draft.v1.101.event-0001"
        let account = SHA256.hash(data: Data(key.utf8)).map { String(format: "%02x", $0) }.joined()
        let old = try value(1, "legacy")
        let next = try value(2, "contains-recovery")
        let encoded = try PropertyListSerialization.data(fromPropertyList: [
            "format": "com.nerae.training-draft.versioned.v2", "payload": Data(next.utf8), "revision": "2"
        ], format: .binary, options: 0)
        backend.values[account] = encoded
        do { _ = try store.readVersioned(key: key); throw TestFailure.assertion }
        catch NeraeVaultError.unavailable { }
        do { _ = try store.compareAndSetVersioned(key: key, expectedRevision: "1", expectedFound: true,
            expectedValue: old, value: old); throw TestFailure.assertion }
        catch NeraeVaultError.unavailable { }
        try check(backend.values[account] == encoded)
        print("PASS: old iOS storage rejects newer draft schema and preserves stored bytes")
    }
}
