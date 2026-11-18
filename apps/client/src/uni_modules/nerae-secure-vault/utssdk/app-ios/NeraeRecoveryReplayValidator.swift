import Foundation
import CoreFoundation

private struct ExactText: Hashable {
    let bytes: [UInt8]
    init(_ value: String) { bytes = Array(value.utf8) }
}

struct NeraeRecoveryHeaderField {
    let name: String
    let value: String
}

/// A validated historical response only. This value is not proof of origin and
/// is never authorization to settle, clear, or mutate a journal entry.
struct NeraeRecoveryReplayResult: CustomStringConvertible, CustomDebugStringConvertible {
    let expectedSubjectId: String
    let journalRevision: String
    let operationKey: String
    let clientEventId: String
    let sessionId: String
    let version: Int
    let expiresAt: String
    let rawBody: String
    var description: String { "NeraeRecoveryReplayResult{redacted}" }
    var debugDescription: String { description }
}

enum NeraeRecoveryReplayValidator {
    static func validate(
        intent: NeraeRecoveryIntent,
        expectedSubjectId: String,
        status: Int,
        headers: [NeraeRecoveryHeaderField],
        body: String
    ) -> NeraeRecoveryReplayResult? {
        guard !expectedSubjectId.isEmpty, sameText(intent.subjectId, expectedSubjectId),
              intent.revision.range(of: #"\A[1-9][0-9]{0,18}\z"#, options: .regularExpression) != nil,
              validOperationKey(intent.operationKey),
              validIdempotencyKey(intent.idempotencyKey), body.utf8.count <= 4 * 1024 * 1024 else { return nil }
        guard let requestBody = strictObject(intent.body),
              let sessionBody = strictObject(body, maxBytes: 4 * 1024 * 1024),
              let response = parseResponseHeaders(headers) else { return nil }
        if intent.path == "/me/recordings" || intent.path.hasPrefix("/me/recordings/") {
            return validateRecordingV2(intent: intent, expectedSubjectId: expectedSubjectId, status: status,
                                       response: response, request: requestBody, receipt: sessionBody, body: body)
        }
        guard let detail = parseDetail(sessionBody) else { return nil }

        let target: String?
        let expectedStatus: Int
        if intent.path == "/me/sessions" {
            target = nil
            expectedStatus = 201
            guard requestBody["source"] as? String == "free",
                  sameText(requestBody["clientEventId"] as? String, intent.clientEventId),
                  sameText(requestBody["date"] as? String, detail.date),
                  detail.source == "free", sameText(detail.clientEventId, intent.clientEventId),
                  optionalEqual(requestBody, sessionBody, key: "timeZone") else { return nil }
        } else if let match = routeMatch(intent.path) {
            target = match.id
            expectedStatus = 200
            guard sameText(detail.id, match.id), detail.source == "free",
                  sameText(detail.clientEventId, intent.clientEventId),
                  let version = exactInt(requestBody["expectedVersion"]), version >= 1 else { return nil }
            if match.kind == "finish" {
                guard version < Int.max, detail.version == version + 1,
                      detail.status == "completed", detail.recordedAt != nil,
                      optionalEqual(requestBody, sessionBody, key: "date"),
                      optionalEqual(requestBody, sessionBody, key: "durationMin"),
                      optionalEqual(requestBody, sessionBody, key: "rpe"),
                      optionalEqual(requestBody, sessionBody, key: "note") else { return nil }
            } else {
                guard detail.version == version || (version < Int.max && detail.version == version + 1),
                      let requestedOrder = stringArray(requestBody["exerciseEntryKeys"]),
                      sameTextArray(requestedOrder, detail.exerciseEntryKeys),
                      matchesSetBatch(requestBody, detail: detail) else { return nil }
            }
        } else { return nil }

        guard status == expectedStatus, response.replayed,
              (target.map { sameText(detail.id, $0) } ?? true) else { return nil }
        return NeraeRecoveryReplayResult(expectedSubjectId: expectedSubjectId,
                                    journalRevision: intent.revision,
                                    operationKey: intent.operationKey,
                                    clientEventId: intent.clientEventId,
                                    sessionId: detail.id,
                                    version: detail.version,
                                    expiresAt: response.expiry,
                                    rawBody: body)
    }

    private struct ParsedHeaders { let replayed: Bool; let expiry: String }

    // A receipt confirms only the authenticated request's historical result.
    // It cannot clear a pending journal or replace the latest server snapshot.
    private static func validateRecordingV2(intent: NeraeRecoveryIntent, expectedSubjectId: String, status: Int,
                                             response: ParsedHeaders, request: [String: Any],
                                             receipt: [String: Any], body: String) -> NeraeRecoveryReplayResult? {
        guard validKey(intent.clientEventId), response.replayed,
              NeraeRecordingV2JournalValidator.validate(path: intent.path, body: request, event: intent.clientEventId),
              Set(receipt.keys) == Set(["id", "version"]), let id = receipt["id"] as? String, validId(id),
              let version = exactInt(receipt["version"]), version >= 1 else { return nil }
        if intent.path == "/me/recordings" {
            guard status == 201 else { return nil }
        } else {
            guard let regex = try? NSRegularExpression(pattern: #"\A/me/recordings/([1-9][0-9]{0,19})/(?:sets|finish)\z"#),
                  let match = regex.firstMatch(in: intent.path, range: NSRange(intent.path.startIndex..., in: intent.path)),
                  let idRange = Range(match.range(at: 1), in: intent.path), sameText(id, String(intent.path[idRange])),
                  status == 200, let expected = exactInt(request["expectedVersion"]), expected >= 1,
                  version == expected || (expected < 9_007_199_254_740_991 && version == expected + 1) else { return nil }
        }
        return NeraeRecoveryReplayResult(expectedSubjectId: expectedSubjectId, journalRevision: intent.revision,
                                         operationKey: intent.operationKey, clientEventId: intent.clientEventId,
                                         sessionId: id, version: version, expiresAt: response.expiry, rawBody: body)
    }

    private static func parseResponseHeaders(_ fields: [NeraeRecoveryHeaderField]) -> ParsedHeaders? {
        var selected: [String: String] = [:]
        let names: Set<String> = ["idempotency-replayed", "idempotency-key-expires-at", "content-type", "cache-control", "x-request-id"]
        for field in fields {
            let name = field.name.lowercased()
            guard !field.name.isEmpty, !containsCRLF(field.name), !containsCRLF(field.value) else { return nil }
            if names.contains(name) {
                guard selected[name] == nil else { return nil }
                selected[name] = field.value
            }
        }
        guard selected["idempotency-replayed"] == "true",
              let contentType = selected["content-type"],
              contentType.range(of: #"\Aapplication/json(?:\s*;\s*charset=utf-8)?\z"#, options: [.regularExpression, .caseInsensitive]) != nil,
              selected["cache-control"]?.lowercased() == "no-store",
              let requestId = selected["x-request-id"], requestId.range(of: #"\A[0-9a-f]{32}\z"#, options: .regularExpression) != nil,
              let expiry = selected["idempotency-key-expires-at"], validExpiry(expiry) else { return nil }
        return ParsedHeaders(replayed: true, expiry: expiry)
    }

    private static func routeMatch(_ path: String) -> (id: String, kind: String)? {
        guard let regex = try? NSRegularExpression(pattern: #"\A/me/sessions/([1-9][0-9]{0,19})/(sets|finish)\z"#),
              let match = regex.firstMatch(in: path, range: NSRange(path.startIndex..., in: path)),
              let idRange = Range(match.range(at: 1), in: path),
              let kindRange = Range(match.range(at: 2), in: path) else { return nil }
        return (String(path[idRange]), String(path[kindRange]))
    }

    private struct Detail {
        let id: String; let clientEventId: String; let date: String; let timeZone: String
        let source: String; let status: String; let version: Int; let recordedAt: String?
        let sets: [[String: Any]]; let exerciseEntryKeys: [String]
    }

    private static func parseDetail(_ value: [String: Any]) -> Detail? {
        guard let id = value["id"] as? String, validId(id),
              let event = value["clientEventId"] as? String, validKey(event),
              let date = value["date"] as? String, validDate(date),
              let zone = value["timeZone"] as? String, zone.utf8.count <= 64,
              !zone.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, TimeZone(identifier: zone) != nil,
              let source = value["source"] as? String, ["free", "plan"].contains(source),
              let status = value["status"] as? String, ["in_progress", "completed", "skipped"].contains(status),
              let version = exactInt(value["version"]), version >= 1,
              let created = value["createdAt"] as? String, validDateTime(created),
              let updated = value["updatedAt"] as? String, validDateTime(updated),
              let recordedValue = value["recordedAt"],
              (recordedValue is NSNull || (recordedValue as? String).map(validDateTime) == true),
              validDetailOptionals(value),
              let sets = value["sets"] as? [[String: Any]], sets.count <= 100_000,
              let order = stringArray(value["exerciseEntryKeys"]), order.count <= 2000,
              Set(order.map(ExactText.init)).count == order.count, order.allSatisfy(validKey),
              sets.allSatisfy(validServerSet), Set(sets.compactMap { ($0["recordKey"] as? String).map(ExactText.init) }).count == sets.count,
              (status != "completed" || (!sets.isEmpty && !(recordedValue is NSNull))),
              (status != "skipped" || sets.isEmpty) else { return nil }
        let positions = Dictionary(uniqueKeysWithValues: order.enumerated().map { (ExactText($1), $0) })
        var seen = Set<ExactText>(), identities: [ExactText: ExactText] = [:], resolvedNames: [ExactText: ExactText] = [:]
        var previousEntry = -1, previousIndex = 0
        for set in sets {
            guard let entry = set["exerciseEntryKey"] as? String,
                  let position = positions[ExactText(entry)], let setIndex = exactInt(set["setIndex"]),
                  position >= previousEntry,
                  (position != previousEntry || setIndex > previousIndex),
                  let resolvedName = set["resolvedExerciseName"] as? String else { return nil }
            let identity: ExactText
            if let exerciseId = set["exerciseId"] as? String { identity = ExactText("id:" + exerciseId) }
            else if let exerciseName = set["exerciseName"] as? String { identity = ExactText("name:" + exerciseName) }
            else { return nil }
            let entryKey = ExactText(entry), nameKey = ExactText(resolvedName)
            if let old = identities[entryKey], old != identity { return nil }
            if let old = resolvedNames[entryKey], old != nameKey { return nil }
            identities[entryKey] = identity; resolvedNames[entryKey] = nameKey
            seen.insert(entryKey); previousEntry = position; previousIndex = setIndex
        }
        guard seen.count == order.count, seen == Set(order.map(ExactText.init)) else { return nil }
        return Detail(id: id, clientEventId: event, date: date, timeZone: zone, source: source,
                      status: status, version: version,
                      recordedAt: recordedValue as? String, sets: sets, exerciseEntryKeys: order)
    }

    private static func validDetailOptionals(_ value: [String: Any]) -> Bool {
        if let duration = value["durationMin"], !(duration is NSNull || exactInt(duration).map { $0 >= 0 } == true) { return false }
        if let rpe = value["rpe"], !(rpe is NSNull || (finiteNumber(rpe) && number(rpe) >= 1 && number(rpe) <= 10 && decimalPlaces(rpe) <= 1)) { return false }
        if let note = value["note"], !(note is NSNull || (note as? String).map { $0.unicodeScalars.count <= 2000 } == true) { return false }
        return true
    }

    private static func validServerSet(_ s: [String: Any]) -> Bool {
        let allowed: Set<String> = ["recordKey", "exerciseEntryKey", "setIndex", "kind", "exerciseId", "exerciseName",
                                    "resolvedExerciseName", "weightKg", "reps", "seconds", "rpe", "completed"]
        guard Set(s.keys).isSubset(of: allowed),
              let record = s["recordKey"] as? String, validKey(record),
              let entry = s["exerciseEntryKey"] as? String, validKey(entry),
              let index = exactInt(s["setIndex"]), index >= 1,
              let kind = s["kind"] as? String,
              ["weight", "perSide", "bodyweight", "bodyweightPerSide", "duration", "durationPerSide", "breath"].contains(kind),
              let resolved = s["resolvedExerciseName"] as? String, !resolved.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              (s["completed"] == nil || isJSONTrue(s["completed"])) else { return false }
        let hasId = (s["exerciseId"] as? String).map(validId) == true
        let hasName = (s["exerciseName"] as? String).map { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && $0.unicodeScalars.count <= 60 } == true
        guard hasId != hasName,
              (s["exerciseName"] == nil || hasName),
              (s["exerciseId"] == nil || hasId),
              !resolved.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, resolved.unicodeScalars.count <= 128,
              (!hasName || sameText(s["resolvedExerciseName"] as? String, s["exerciseName"] as? String)) else { return false }
        if let rpe = s["rpe"], !(rpe is NSNull || (finiteNumber(rpe) && number(rpe) >= 1 && number(rpe) <= 10 && decimalPlaces(rpe) <= 1)) { return false }
        switch kind {
        case "weight", "perSide":
            return finiteNumber(s["weightKg"]) && number(s["weightKg"]) > 0 && number(s["weightKg"]) <= 300 && decimalPlaces(s["weightKg"]) <= 2 &&
                exactInt(s["reps"]).map { (1...100).contains($0) } == true && s["seconds"] == nil
        case "duration", "durationPerSide":
            return s["weightKg"] == nil && s["reps"] == nil && exactInt(s["seconds"]).map { (5...600).contains($0) } == true
        default:
            return s["weightKg"] == nil && s["seconds"] == nil && exactInt(s["reps"]).map { (1...100).contains($0) } == true
        }
    }

    private static func matchesSetBatch(_ body: [String: Any], detail: Detail) -> Bool {
        let sets: [[String: Any]]
        if let raw = body["sets"] {
            guard let parsed = raw as? [[String: Any]], !parsed.isEmpty, parsed.count <= 200 else { return false }
            sets = parsed
        } else { sets = [] }
        let byKey = Dictionary(uniqueKeysWithValues: detail.sets.compactMap { s -> (ExactText, [String: Any])? in
            guard let key = s["recordKey"] as? String else { return nil }; return (ExactText(key), s)
        })
        var written = Set<ExactText>()
        for expected in sets {
            guard validPayloadSet(expected), isJSONTrue(expected["completed"]),
                  let key = expected["recordKey"] as? String, written.insert(ExactText(key)).inserted,
                  let found = byKey[ExactText(key)], equalSet(expected, found) else { return false }
        }
        if let deleted = body["deleteRecordKeys"] {
            guard let keys = stringArray(deleted), !keys.isEmpty, keys.count <= 200, Set(keys.map(ExactText.init)).count == keys.count,
                  keys.allSatisfy(validKey), keys.allSatisfy({ !written.contains(ExactText($0)) && byKey[ExactText($0)] == nil }),
                  keys.count + written.count <= 200 else { return false }
        }
        if let restored = body["restoreRecordKeys"] {
            guard let keys = stringArray(restored), !keys.isEmpty, keys.count <= 200, Set(keys.map(ExactText.init)).count == keys.count,
                  keys.allSatisfy({ written.contains(ExactText($0)) && byKey[ExactText($0)] != nil }) else { return false }
        }
        return true
    }

    private static func validPayloadSet(_ s: [String: Any]) -> Bool {
        let allowed: Set<String> = ["recordKey", "exerciseEntryKey", "setIndex", "kind", "exerciseId", "exerciseName",
                                    "weightKg", "reps", "seconds", "rpe", "completed"]
        guard Set(s.keys).isSubset(of: allowed),
              let record = s["recordKey"] as? String, validKey(record),
              let entry = s["exerciseEntryKey"] as? String, validKey(entry),
              let index = exactInt(s["setIndex"]), index >= 1,
              let kind = s["kind"] as? String,
              ["weight", "perSide", "bodyweight", "bodyweightPerSide", "duration", "durationPerSide", "breath"].contains(kind),
              isJSONTrue(s["completed"]) else { return false }
        let hasId = (s["exerciseId"] as? String).map(validId) == true
        let hasName = (s["exerciseName"] as? String).map { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && $0.unicodeScalars.count <= 60 } == true
        guard hasId != hasName,
              (s["exerciseId"] == nil || hasId),
              (s["exerciseName"] == nil || hasName) else { return false }
        if let rpe = s["rpe"], !(rpe is NSNull || (finiteNumber(rpe) && number(rpe) >= 1 && number(rpe) <= 10 && decimalPlaces(rpe) <= 1)) { return false }
        switch kind {
        case "weight", "perSide":
            return finiteNumber(s["weightKg"]) && number(s["weightKg"]) > 0 && number(s["weightKg"]) <= 300 && decimalPlaces(s["weightKg"]) <= 2 &&
                exactInt(s["reps"]).map { (1...100).contains($0) } == true && s["seconds"] == nil
        case "duration", "durationPerSide":
            return s["weightKg"] == nil && s["reps"] == nil && exactInt(s["seconds"]).map { (5...600).contains($0) } == true
        default:
            return s["weightKg"] == nil && s["seconds"] == nil && exactInt(s["reps"]).map { (1...100).contains($0) } == true
        }
    }

    private static func equalSet(_ a: [String: Any], _ b: [String: Any]) -> Bool {
        for key in ["recordKey", "exerciseEntryKey", "setIndex", "kind", "exerciseId", "exerciseName", "weightKg", "reps", "seconds"] {
            if !jsonEqual(a[key], b[key]) { return false }
        }
        let ar = a["rpe"] ?? NSNull(), br = b["rpe"] ?? NSNull()
        return jsonEqual(ar, br)
    }

    private static func optionalEqual(_ request: [String: Any], _ response: [String: Any], key: String) -> Bool {
        guard let value = request[key] else { return true }
        guard let returned = response[key] else { return false }
        return jsonEqual(value, returned)
    }

    private static func jsonEqual(_ a: Any?, _ b: Any?) -> Bool {
        guard let a, let b else { return a == nil && b == nil }
        if a is NSNull || b is NSNull { return a is NSNull && b is NSNull }
        if let an = a as? NSNumber {
            guard let bn = b as? NSNumber else { return false }
            let aBool = CFGetTypeID(an) == CFBooleanGetTypeID(), bBool = CFGetTypeID(bn) == CFBooleanGetTypeID()
            guard aBool == bBool else { return false }
            return aBool ? an.boolValue == bn.boolValue : an.compare(bn) == .orderedSame
        }
        if let aString = a as? String {
            guard let bString = b as? String else { return false }
            return sameText(aString, bString)
        }
        return false
    }

    private static func strictObject(_ text: String, maxBytes: Int = 64 * 1024) -> [String: Any]? {
        let bytes = Array(text.utf8)
        guard !bytes.isEmpty, bytes.count <= maxBytes else { return nil }
        if bytes.count <= NeraeCredentialVault.maxBytes && !NeraeStrictJSON.isObjectWithoutDuplicateKeys(text) { return nil }
        var scanner = DuplicateKeyScanner(bytes: bytes)
        guard scanner.scan(), let object = try? JSONSerialization.jsonObject(with: Data(bytes)) as? [String: Any] else { return nil }
        return object
    }

    private struct DuplicateKeyScanner {
        let bytes: [UInt8]; var i = 0; var nodes = 0
        mutating func scan() -> Bool { ws(); guard object(depth: 0) else { return false }; ws(); return i == bytes.count }
        mutating func value(depth: Int) -> Bool {
            guard depth <= 64 else { return false }; nodes += 1; guard nodes <= 100_000 else { return false }; ws()
            guard i < bytes.count else { return false }
            if bytes[i] == 0x7b { return object(depth: depth) }
            if bytes[i] == 0x5b { return array(depth: depth) }
            if bytes[i] == 0x22 { return string() != nil }
            let start = i
            while i < bytes.count && ![0x20,0x09,0x0a,0x0d,0x2c,0x5d,0x7d].contains(bytes[i]) { i += 1 }
            return i > start
        }
        mutating func object(depth: Int) -> Bool {
            guard take(0x7b) else { return false }; ws(); if take(0x7d) { return true }
            var names = Set<ExactText>()
            while true {
                ws(); guard let keyBytes = string(),
                    let decoded = try? JSONSerialization.jsonObject(with: Data([0x5b] + keyBytes + [0x5d])) as? [String],
                    decoded.count == 1, names.insert(ExactText(decoded[0])).inserted else { return false }
                ws(); guard take(0x3a), value(depth: depth + 1) else { return false }; ws()
                if take(0x7d) { return true }; guard take(0x2c) else { return false }
            }
        }
        mutating func array(depth: Int) -> Bool {
            guard take(0x5b) else { return false }; ws(); if take(0x5d) { return true }
            var count = 0
            while true { count += 1; guard count <= 100_000, value(depth: depth + 1) else { return false }; ws(); if take(0x5d) { return true }; guard take(0x2c) else { return false } }
        }
        mutating func string() -> [UInt8]? {
            guard take(0x22) else { return nil }; let start = i - 1
            while i < bytes.count {
                let b = bytes[i]
                if b == 0x22 { i += 1; return Array(bytes[start..<i]) }
                if b < 0x20 { return nil }
                if b == 0x5c { i += 1; guard i < bytes.count else { return nil }; if bytes[i] == 0x75 { guard i + 4 < bytes.count else { return nil }; i += 4 } }
                i += 1
            }
            return nil
        }
        mutating func ws() { while i < bytes.count && [0x20,0x09,0x0a,0x0d].contains(bytes[i]) { i += 1 } }
        mutating func take(_ b: UInt8) -> Bool { guard i < bytes.count, bytes[i] == b else { return false }; i += 1; return true }
    }

    private static func exactInt(_ value: Any?) -> Int? {
        let maxSafe = 9_007_199_254_740_991.0
        guard let n = value as? NSNumber, CFGetTypeID(n) != CFBooleanGetTypeID(), n.doubleValue.isFinite,
              n.doubleValue.rounded(.towardZero) == n.doubleValue, n.doubleValue >= -maxSafe, n.doubleValue <= maxSafe else { return nil }
        return n.intValue
    }
    private static func isJSONTrue(_ value: Any?) -> Bool {
        guard let n = value as? NSNumber, CFGetTypeID(n) == CFBooleanGetTypeID() else { return false }
        return n.boolValue
    }
    private static func sameText(_ a: String?, _ b: String?) -> Bool {
        guard let a, let b else { return a == nil && b == nil }
        return a.utf8.elementsEqual(b.utf8)
    }
    private static func sameTextArray(_ a: [String], _ b: [String]) -> Bool {
        a.count == b.count && zip(a, b).allSatisfy { sameText($0, $1) }
    }
    private static func finiteNumber(_ v: Any?) -> Bool { guard let n = v as? NSNumber else { return false }; return CFGetTypeID(n) != CFBooleanGetTypeID() && n.doubleValue.isFinite }
    private static func number(_ v: Any?) -> Double { (v as? NSNumber)?.doubleValue ?? .nan }
    private static func decimalPlaces(_ v: Any?) -> Int {
        guard let n = v as? NSNumber else { return 99 }
        let text = n.stringValue.lowercased(), parts = text.split(separator: "e", omittingEmptySubsequences: false)
        let exponent = parts.count == 2 ? (Int(parts[1]) ?? 99) : 0
        let fraction = parts[0].split(separator: ".", omittingEmptySubsequences: false).dropFirst().first?.count ?? 0
        return max(0, fraction - exponent)
    }
    private static func stringArray(_ value: Any?) -> [String]? { guard let a = value as? [Any] else { return nil }; let s = a.compactMap { $0 as? String }; return s.count == a.count ? s : nil }
    private static func validKey(_ s: String) -> Bool {
        let length = s.unicodeScalars.count
        return (8...64).contains(length) && !containsCRLF(s) && !s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
    private static func validOperationKey(_ s: String) -> Bool {
        !s.isEmpty && s.utf8.count <= 512 && !containsCRLF(s)
    }
    private static func validIdempotencyKey(_ s: String) -> Bool {
        let bytes = Array(s.utf8)
        return (8...128).contains(bytes.count) && bytes.allSatisfy {
            (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || [45, 46, 95, 126].contains(Int($0))
        }
    }
    private static func containsCRLF(_ s: String) -> Bool {
        s.utf8.contains(0x0d) || s.utf8.contains(0x0a)
    }
    private static func validId(_ s: String) -> Bool {
        !s.isEmpty && s.utf8.count <= 20 && s.utf8.allSatisfy { $0 >= 48 && $0 <= 57 } && s.first != "0"
    }
    private static func validDate(_ s: String) -> Bool {
        let f = DateFormatter(); f.locale = Locale(identifier: "en_US_POSIX"); f.timeZone = TimeZone(secondsFromGMT: 0); f.dateFormat = "yyyy-MM-dd"; f.isLenient = false
        guard let d = f.date(from: s) else { return false }; return f.string(from: d) == s
    }
    private static func validDateTime(_ s: String) -> Bool {
        guard let regex = try? NSRegularExpression(pattern: #"\A(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-](\d{2}):(\d{2}))\z"#),
              let match = regex.firstMatch(in: s, range: NSRange(s.startIndex..., in: s)),
              let dayRange = Range(match.range(at: 1), in: s), validDate(String(s[dayRange])),
              let hour = capture(match, 2, s).flatMap(Int.init), let minute = capture(match, 3, s).flatMap(Int.init),
              let second = capture(match, 4, s).flatMap(Int.init), hour < 24, minute < 60, second < 60 else { return false }
        guard let zone = capture(match, 5, s) else { return false }
        if zone == "Z" { return true }
        guard let offsetHour = capture(match, 6, s).flatMap(Int.init),
              let offsetMinute = capture(match, 7, s).flatMap(Int.init) else { return false }
        return offsetHour <= 23 && offsetMinute <= 59
    }
    private static func capture(_ match: NSTextCheckingResult, _ index: Int, _ source: String) -> String? {
        guard match.range(at: index).location != NSNotFound, let range = Range(match.range(at: index), in: source) else { return nil }
        return String(source[range])
    }
    private static func validExpiry(_ s: String) -> Bool {
        guard s.unicodeScalars.count <= 64, s.range(of: #"\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|\+00:00)\z"#, options: .regularExpression) != nil,
              let day = s.split(separator: "T").first.map(String.init), validDate(day),
              let tail = s.split(separator: "T").dropFirst().first else { return false }
        let hms = tail.prefix(8).split(separator: ":").compactMap { Int($0) }
        return hms.count == 3 && hms[0] <= 23 && hms[1] <= 59 && hms[2] <= 59
    }
}
