import Foundation

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
        let one = try value(1, "before", pending: "same-pending")
        let two = try value(2, "after", pending: "same-pending")
        try check(try store.create(key: key, value: one))
        let old = try store.readVersioned(key: key)
        try check(old.value == one && old.revision == "1")
        try check(try store.compareAndSetVersioned(key: key, expectedRevision: old.revision,
            expectedFound: true, expectedValue: one, value: two))
        let upgraded = try store.readVersioned(key: key)
        try check(upgraded.value == two && upgraded.revision == "2")
        try check(!(try store.compareAndSetVersioned(key: key, expectedRevision: old.revision,
            expectedFound: true, expectedValue: one, value: two)))
        try expectInvalid { _ = try store.compareAndSetVersioned(key: key, expectedRevision: upgraded.revision,
            expectedFound: true, expectedValue: two, value: one) }
        try expectInvalid { try store.write(key: key, value: one) }
        try expectInvalid { _ = try store.compareAndSet(key: key, expected: two, value: one) }
        try check(try store.readVersioned(key: key).value == two && store.readVersioned(key: key).revision == "2")
        let changed = try value(2, "next", pending: "same-pending")
        try check(try store.compareAndSetVersioned(key: key, expectedRevision: "2", expectedFound: true,
            expectedValue: two, value: changed))
        let duplicate = "{\"schema\":2,\"sc\\u0068ema\":1,\"subjectId\":\"101\",\"draft\":{\"clientEventId\":\"event-0001\"}}"
        let last = try store.readVersioned(key: key)
        try expectInvalid { _ = try store.compareAndSetVersioned(key: key, expectedRevision: last.revision,
            expectedFound: true, expectedValue: last.value, value: duplicate) }
        try expectInvalid { _ = try store.create(key: key, value: duplicate) }
        try expectInvalid { _ = try store.compareAndSetVersioned(key: key, expectedRevision: last.revision,
            expectedFound: true, expectedValue: last.value, value: try value(3)) }
        try check(try store.readVersioned(key: key).value == changed)
        let listing = try store.discover(subject: "101", after: "")
        try check(listing.contains("event-0001"))
        print("PASS: schema 1->2 CAS upgrade, pending preservation, stale/downgrade rejection, ambiguous schema rejection, discovery")
    }
}
