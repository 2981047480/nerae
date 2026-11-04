import Foundation

private let event = "event-12345678"
private let commonHeaders = [
    NeraeRecoveryHeaderField(name: "content-type", value: "application/json; charset=utf-8"),
    NeraeRecoveryHeaderField(name: "cache-control", value: "no-store"),
    NeraeRecoveryHeaderField(name: "x-request-id", value: "0123456789abcdef0123456789abcdef"),
    NeraeRecoveryHeaderField(name: "idempotency-replayed", value: "true"),
    NeraeRecoveryHeaderField(name: "idempotency-key-expires-at", value: "2026-11-04T12:30:00.123456Z")
]
private let baseDetail = #"{"id":"42","clientEventId":"event-12345678","date":"2026-11-04","timeZone":"Asia/Shanghai","status":"in_progress","source":"free","version":1,"createdAt":"2026-11-04T02:00:00Z","updatedAt":"2026-11-04T02:00:00Z","recordedAt":null,"sets":[],"exerciseEntryKeys":[]}"#
private func intent(_ path: String, _ body: String, eventId: String = event, subject: String = "100",
                    idempotencyKey: String = "idem-12345678", operationKey: String = "op-12345678") -> NeraeRecoveryIntent {
    .init(revision: "7", subjectId: subject, clientEventId: eventId, operationKey: operationKey,
          path: path, idempotencyKey: idempotencyKey, body: body)
}
private func detail(_ changes: [String: String]) -> String {
    guard var value = (try? JSONSerialization.jsonObject(with: Data(baseDetail.utf8))) as? [String: Any] else { fatalError("fixture") }
    for (key, replacement) in changes {
        guard let wrapped = try? JSONSerialization.jsonObject(with: Data(("[" + replacement + "]").utf8)),
              let values = wrapped as? [Any], let first = values.first else { fatalError("fixture value \(key): \(replacement)") }
        value[key] = first
    }
    let data = try! JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
    return String(data: data, encoding: .utf8)!
}
private func check(_ name: String, _ condition: Bool) {
    if !condition { fputs("FAIL \(name)\n", stderr); exit(1) }
    print("PASS \(name)")
}
private func validate(_ i: NeraeRecoveryIntent, status: Int = 201, headers: [NeraeRecoveryHeaderField] = commonHeaders, body: String = baseDetail, expected: String = "100") -> NeraeRecoveryReplayResult? {
    NeraeRecoveryReplayValidator.validate(intent: i, expectedSubjectId: expected, status: status, headers: headers, body: body)
}

@main struct NeraeRecoveryReplayValidatorTests {
    static func main() {
        let create = intent("/me/sessions", #"{"source":"free","date":"2026-11-04","timeZone":"Asia/Shanghai","clientEventId":"event-12345678"}"#)
        let created = validate(create)
        check("create result binds intent and immutable response", created?.sessionId == "42" && created?.clientEventId == event && created?.journalRevision == "7" && created?.rawBody == baseDetail)
        check("candidate description redacts response and identity", created.map {
            !String(describing: $0).contains(event) && !String(reflecting: $0).contains(baseDetail)
        } == true)
        check("reject wrong expected subject", validate(create, expected: "101") == nil)
        check("reject subject not matching persisted intent", validate(intent(create.path, create.body, subject: "101")) == nil)
        check("reject event mismatch", validate(intent(create.path, create.body, eventId: "event-other-123")) == nil)
        check("accept 65-byte ASCII idempotency key", validate(intent(create.path, create.body, idempotencyKey: String(repeating: "a", count: 65))) != nil)
        check("accept 128-byte ASCII idempotency key", validate(intent(create.path, create.body, idempotencyKey: String(repeating: "z", count: 128))) != nil)
        for badKey in ["short-1", "bad key 123", "clé-idem-123", String(repeating: "a", count: 129)] {
            check("reject invalid idempotency key \(badKey.count)", validate(intent(create.path, create.body, idempotencyKey: badKey)) == nil)
        }
        let crlfEvent = "event-1234\r\n"
        let crlfCreateBody = #"{"source":"free","date":"2026-11-04","clientEventId":"event-1234\r\n"}"#
        check("reject CRLF in event key", validate(intent("/me/sessions", crlfCreateBody, eventId: crlfEvent), body: detail(["clientEventId": #""event-1234\r\n""#])) == nil)
        check("reject CRLF in operation key", validate(intent(create.path, create.body, operationKey: "op-1234\r\n")) == nil)
        check("reject CRLF in response header value", validate(create, headers: commonHeaders.map { $0.name == "cache-control" ? NeraeRecoveryHeaderField(name: $0.name, value: "no-store\r\n") : $0 }) == nil)
        check("reject CRLF in response header name", validate(create, headers: commonHeaders.map { $0.name == "x-request-id" ? NeraeRecoveryHeaderField(name: "x-request-id\r\n", value: $0.value) : $0 }) == nil)
        check("reject create body event mismatch", validate(create, body: detail(["clientEventId": #""other-event-123""#])) == nil)
        check("reject wrong status", validate(create, status: 200) == nil)
        check("reject missing required header", validate(create, headers: Array(commonHeaders.dropLast())) == nil)
        check("reject duplicate selected header", validate(create, headers: commonHeaders + [commonHeaders[0]]) == nil)
        check("reject malformed request id", validate(create, headers: commonHeaders.map { $0.name == "x-request-id" ? NeraeRecoveryHeaderField(name: $0.name, value: "bad") : $0 }) == nil)
        check("reject invalid expiry", validate(create, headers: commonHeaders.map { $0.name == "idempotency-key-expires-at" ? NeraeRecoveryHeaderField(name: $0.name, value: "2026-02-30T12:00:00Z") : $0 }) == nil)
        check("reject non replay response", validate(create, headers: commonHeaders.map { $0.name == "idempotency-replayed" ? NeraeRecoveryHeaderField(name: $0.name, value: "false") : $0 }) == nil)
        check("reject duplicate JSON field", validate(create, body: #"{"id":"42","id":"43"}"#) == nil)
        check("reject nested duplicate JSON field", validate(create, body: baseDetail.replacingOccurrences(of: #""sets":[]"#, with: #""sets":[{"x":1,"x":2}]"#)) == nil)
        check("reject escaped duplicate JSON field", validate(create, body: String(baseDetail.dropLast()) + #", "\u0069d":"42"}"#) == nil)
        let largePadding = String(repeating: "a", count: 70_000)
        let largeBody = String(baseDetail.dropLast()) + ",\"extra\":\"" + largePadding + "\"}"
        check("accept bounded response beyond small-parser limit", validate(create, body: largeBody) != nil)
        check("reject duplicate field beyond small-parser limit", validate(create, body: String(largeBody.dropLast()) + #", "id":"42"}"#) == nil)
        let tooLarge = String(baseDetail.dropLast()) + ",\"extra\":\"" + String(repeating: "x", count: 4 * 1024 * 1024) + "\"}"
        check("reject oversized response", validate(create, body: tooLarge) == nil)
        let tooDeep = String(baseDetail.dropLast()) + ",\"extra\":" + String(repeating: "[", count: 66) + "0" + String(repeating: "]", count: 66) + "}"
        check("reject deeply nested response", validate(create, body: tooDeep) == nil)
        check("reject malformed response object", validate(create, body: "[]") == nil)
        check("reject plan session as free-create receipt", validate(create, body: detail(["source": #""plan""#])) == nil)
        check("reject trailing newline in replay route", validate(intent("/me/sessions/replay\n", create.body)) == nil)
        check("reject trailing Unicode line separator in request id", validate(create, headers: commonHeaders.map { $0.name == "x-request-id" ? NeraeRecoveryHeaderField(name: $0.name, value: "0123456789abcdef0123456789abcdef\u{2028}") : $0 }) == nil)
        check("reject trailing Unicode line separator in expiry", validate(create, headers: commonHeaders.map { $0.name == "idempotency-key-expires-at" ? NeraeRecoveryHeaderField(name: $0.name, value: "2026-11-04T12:30:00Z\u{2028}") : $0 }) == nil)
        check("reject trailing newline in server timestamp", validate(create, body: detail(["createdAt": #""2026-11-04T02:00:00Z\n""#])) == nil)

        let set = #"{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","weightKg":20,"reps":8}"#
        let setResponse = detail(["version": "2", "exerciseEntryKeys": #"["entry-1234"]"#,
            "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","resolvedExerciseName":"Squat","weightKg":20,"reps":8}]"#])
        let setBody = #"{"expectedVersion":1,"exerciseEntryKeys":["entry-1234"],"sets":[\#(set)]}"#
        let setIntent = intent("/me/sessions/42/sets", setBody)
        check("sets replay matches target, version, entry and submitted set", validate(setIntent, status: 200, body: setResponse) != nil)
        let extraServerSet = setResponse.replacingOccurrences(of: #""resolvedExerciseName":"Squat""#,
            with: #""resolvedExerciseName":"Squat","deviceProof":"unexpected""#)
        check("reject additional response set field", validate(setIntent, status: 200, body: extraServerSet) == nil)
        let extraPayloadSet = setBody.replacingOccurrences(of: #""exerciseName":"Squat""#,
            with: #""exerciseName":"Squat","deviceProof":"unexpected""#)
        check("reject additional submitted set field", validate(intent(setIntent.path, extraPayloadSet), status: 200, body: setResponse) == nil)
        check("reject wrong target session", validate(intent("/me/sessions/43/sets", setIntent.body), status: 200, body: setResponse) == nil)
        check("reject mismatched set payload", validate(intent(setIntent.path, setIntent.body.replacingOccurrences(of: "20", with: "21")), status: 200, body: setResponse) == nil)
        check("reject sets version beyond one-step commit", validate(setIntent, status: 200, body: detail(["version": "3", "exerciseEntryKeys": #"["entry-1234"]"#, "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","resolvedExerciseName":"Squat","weightKg":20,"reps":8}]"#])) == nil)
        check("reject set-entry order mismatch", validate(intent(setIntent.path, setBody.replacingOccurrences(of: "entry-1234", with: "entry-other-1234")), status: 200, body: setResponse) == nil)
        let nfcEvent = "é-event-1234"
        let unicodeCreateBody = #"{"source":"free","date":"2026-11-04","clientEventId":"é-event-1234"}"#
        check("reject NFC/NFD event mismatch", validate(intent("/me/sessions", unicodeCreateBody, eventId: nfcEvent), body: detail(["clientEventId": #""é-event-1234""#])) == nil)
        let nfdEntry = "e\u{301}-entry-1234"
        let unicodeEntryResponse = detail(["version": "2", "exerciseEntryKeys": "[\"\(nfdEntry)\"]",
            "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"é-entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","resolvedExerciseName":"Squat","weightKg":20,"reps":8}]"#])
        let unicodeEntryBody = #"{"expectedVersion":1,"exerciseEntryKeys":["é-entry-1234"],"sets":[{"recordKey":"record-1234","exerciseEntryKey":"é-entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","weightKg":20,"reps":8}]}"#
        check("reject NFC/NFD exercise-entry mismatch", validate(intent("/me/sessions/42/sets", unicodeEntryBody), status: 200, body: unicodeEntryResponse) == nil)
        let nfdRecord = "e\u{301}-record-1234"
        let recordBody = #"{"expectedVersion":1,"exerciseEntryKeys":["entry-1234"],"sets":[{"recordKey":"é-record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","weightKg":20,"reps":8}]}"#
        let recordResponse = detail(["version": "2", "exerciseEntryKeys": #"["entry-1234"]"#,
            "sets": "[{\"recordKey\":\"\(nfdRecord)\",\"exerciseEntryKey\":\"entry-1234\",\"setIndex\":1,\"kind\":\"weight\",\"completed\":true,\"exerciseName\":\"Squat\",\"resolvedExerciseName\":\"Squat\",\"weightKg\":20,\"reps\":8}]"])
        check("reject NFC/NFD record-key mismatch", validate(intent("/me/sessions/42/sets", recordBody), status: 200, body: recordResponse) == nil)
        let nameBody = #"{"expectedVersion":1,"exerciseEntryKeys":["entry-1234"],"sets":[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Café","weightKg":20,"reps":8}]}"#
        let nameResponse = detail(["version": "2", "exerciseEntryKeys": #"["entry-1234"]"#,
            "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Café","resolvedExerciseName":"Café","weightKg":20,"reps":8}]"#])
        check("reject NFC/NFD action-name mismatch", validate(intent("/me/sessions/42/sets", nameBody), status: 200, body: nameResponse) == nil)
        check("reject numeric one for completed boolean", validate(intent("/me/sessions/42/sets", setBody.replacingOccurrences(of: "true", with: "1")), status: 200, body: setResponse) == nil)
        check("reject invalid supplied exerciseId", validate(intent("/me/sessions/42/sets", setBody.replacingOccurrences(of: #""exerciseName":"Squat""#, with: #""exerciseName":"Squat","exerciseId":"bad""#)), status: 200, body: setResponse) == nil)
        check("reject invalid supplied exerciseName", validate(intent("/me/sessions/42/sets", setBody.replacingOccurrences(of: #""exerciseName":"Squat""#, with: #""exerciseName":"","exerciseId":"42""#)), status: 200, body: setResponse) == nil)
        check("reject numeric one for returned completed field", validate(setIntent, status: 200, body: setResponse.replacingOccurrences(of: #""completed":true"#, with: #""completed":1"#)) == nil)
        check("reject zero weight", validate(intent("/me/sessions/42/sets", setBody.replacingOccurrences(of: "20", with: "0")), status: 200, body: setResponse) == nil)
        check("reject integer beyond JavaScript safe range", validate(intent("/me/sessions/42/sets", setBody.replacingOccurrences(of: "\"expectedVersion\":1", with: "\"expectedVersion\":9007199254740992")), status: 200, body: setResponse) == nil)
        let tooManyScalars = String(repeating: "e\u{301}", count: 31)
        let longNameBody = "{\"expectedVersion\":1,\"exerciseEntryKeys\":[\"entry-1234\"],\"sets\":[{\"recordKey\":\"record-1234\",\"exerciseEntryKey\":\"entry-1234\",\"setIndex\":1,\"kind\":\"weight\",\"completed\":true,\"exerciseName\":\"\(tooManyScalars)\",\"weightKg\":20,\"reps\":8}]}"
        check("enforce action-name Unicode scalar limit", validate(intent("/me/sessions/42/sets", longNameBody), status: 200, body: setResponse) == nil)
        check("reject invalid status for set replay", validate(setIntent, status: 201, body: setResponse) == nil)

        let finishIntent = intent("/me/sessions/42/finish", #"{"expectedVersion":1,"date":"2026-11-04","durationMin":30,"rpe":7,"note":"done"}"#)
        let finished = detail(["version": "2", "status": #""completed""#,
            "recordedAt": #""2026-11-04T02:30:00Z""#, "durationMin": "30", "rpe": "7", "note": #""done""#,
            "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","resolvedExerciseName":"Squat","weightKg":20,"reps":8}]"#,
            "exerciseEntryKeys": #"["entry-1234"]"#])
        check("finish replay matches target, next version and committed fields", validate(finishIntent, status: 200, body: finished) != nil)
        check("reject finish field mismatch", validate(finishIntent, status: 200, body: detail(["version": "2", "status": #""completed""#, "recordedAt": #""2026-11-04T02:30:00Z""#, "durationMin": "31", "rpe": "7", "note": #""done""#, "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","resolvedExerciseName":"Squat","weightKg":20,"reps":8}]"#, "exerciseEntryKeys": #"["entry-1234"]"#])) == nil)
        let noteNFC = intent(finishIntent.path, #"{"expectedVersion":1,"note":"Café"}"#)
        check("reject NFC/NFD finish-note mismatch", validate(noteNFC, status: 200, body: detail(["version": "2", "status": #""completed""#, "recordedAt": #""2026-11-04T02:30:00Z""#, "note": #""Café""#, "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","resolvedExerciseName":"Squat","weightKg":20,"reps":8}]"#, "exerciseEntryKeys": #"["entry-1234"]"#])) == nil)
        let objectNote = intent(finishIntent.path, #"{"expectedVersion":1,"note":{"x":1}}"#)
        check("reject complex JSON optional field comparison", validate(objectNote, status: 200, body: detail(["version": "2", "status": #""completed""#, "recordedAt": #""2026-11-04T02:30:00Z""#, "note": #"{"x":1}"#, "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","resolvedExerciseName":"Squat","weightKg":20,"reps":8}]"#, "exerciseEntryKeys": #"["entry-1234"]"#])) == nil)
        check("reject finish without recordedAt", validate(finishIntent, status: 200, body: detail(["version": "2", "status": #""completed""#, "sets": #"[{"recordKey":"record-1234","exerciseEntryKey":"entry-1234","setIndex":1,"kind":"weight","completed":true,"exerciseName":"Squat","resolvedExerciseName":"Squat","weightKg":20,"reps":8}]"#, "exerciseEntryKeys": #"["entry-1234"]"#])) == nil)
        check("reject finish response wrong session", validate(intent("/me/sessions/43/finish", finishIntent.body), status: 200, body: finished) == nil)
        print("PASS candidate only; no origin trust or journal settlement authority")
    }
}
