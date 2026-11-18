package uts.sdk.modules.neraeSecureVault

import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

private data class ProtectedEnvelope(
    val revision: Long, val found: Boolean, val payload: String, val historyKnown: Boolean,
    val dispatchMarkers: Set<String>, val activeBindings: Map<String, String>
)
private interface ProtectedJournalSlot {
    fun <T> transact(block: (ProtectedEnvelope) -> Pair<ProtectedEnvelope, T>): T
}
private sealed class ClaimResult {
    data class Claimed(val revision: Long) : ClaimResult()
    object Conflict : ClaimResult()
    object Unavailable : ClaimResult()
}
private class FirstDispatchGate(private val slot: ProtectedJournalSlot) {
    fun claim(expectedRevision: Long, expectedPayload: String, operationKey: String, nextPayload: String): ClaimResult {
        val identity = try { FirstDispatchNativeParser.derive(expectedPayload, nextPayload, operationKey) }
            catch (_: Exception) { return ClaimResult.Unavailable }
        return try {
            slot.transact { current ->
                if (!current.historyKnown || !current.found) return@transact current to ClaimResult.Unavailable
                if (current.revision != expectedRevision || current.payload != expectedPayload) return@transact current to ClaimResult.Conflict
                if (identity.keyMarker in current.dispatchMarkers || identity.bodyMarker in current.dispatchMarkers ||
                    identity.eventMarker in current.activeBindings || current.dispatchMarkers.size > 8192 - 2 || current.activeBindings.size >= 64) {
                    return@transact current to ClaimResult.Conflict
                }
                if (current.revision == Long.MAX_VALUE) return@transact current to ClaimResult.Unavailable
                val next = current.copy(revision = current.revision + 1, payload = nextPayload,
                    dispatchMarkers = current.dispatchMarkers + setOf(identity.keyMarker, identity.bodyMarker),
                    activeBindings = current.activeBindings + (identity.eventMarker to identity.binding))
                next to ClaimResult.Claimed(next.revision)
            }
        } catch (_: Throwable) { ClaimResult.Unavailable }
    }
}

private class MemorySlot(initial: ProtectedEnvelope) : ProtectedJournalSlot {
    @Volatile private var value = initial
    @Synchronized override fun <T> transact(block: (ProtectedEnvelope) -> Pair<ProtectedEnvelope, T>): T {
        val (next, result) = block(value); if (next != value) value = next; return result
    }
    @Synchronized fun snapshot() = value
}
private fun expect(ok: Boolean, label: String) { check(ok) { "failed: $label" } }
private const val key = "nerae.workout.operation.v1.12.event-0001"
private const val body = "{\"source\":\"free\",\"date\":\"2026-11-04\",\"timeZone\":\"Asia/Shanghai\",\"clientEventId\":\"event-0001\"}"
private fun op(state: String, revision: Int, attempt: String? = null, request: String = "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions\",\"idempotencyKey\":\"idem-0001\",\"body\":${quote(body)}}") =
    "{\"schema\":1,\"subjectId\":\"12\",\"clientEventId\":\"event-0001\",\"createdGeneration\":\"gen-A\",\"revision\":$revision,\"state\":\"$state\",\"createdAt\":100,\"expiresAt\":200,$request" +
        (attempt?.let { ",\"attemptGeneration\":\"$it\"}" } ?: "}")
private fun registry(entry: String, other: String? = null) = "{\"schema\":1,\"entries\":{\"$key\":${quote(entry)}" +
    (other?.let { ",\"other\":${quote(it)}" } ?: "") + "}}"
private fun quote(s: String) = "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\""
private fun env(payload: String) = ProtectedEnvelope(7, true, payload, true, emptySet(), emptyMap())
private val old = registry(op("ready", 1))
private val next = registry(op("in_flight", 2, "gen-A"))
private fun claim(slot: MemorySlot, before: String = old, after: String = next) = FirstDispatchGate(slot).claim(7, before, key, after)

fun main() {
    run {
        val slot = MemorySlot(env(old)); expect(claim(slot) == ClaimResult.Claimed(8), "valid exact transition")
        expect(slot.snapshot().dispatchMarkers.size == 2 && slot.snapshot().activeBindings.size == 1, "payload and history committed")
        expect(FirstDispatchGate(slot).claim(7, old, key, next) == ClaimResult.Conflict, "repeat claim refused")
        val restored = MemorySlot(slot.snapshot().copy(revision = 10, payload = old))
        expect(FirstDispatchGate(restored).claim(10, old, key, next) == ClaimResult.Conflict, "restored logical payload retains dispatch refusal")
    }
    run {
        val slot = MemorySlot(env(old).copy(historyKnown = false)); expect(claim(slot) == ClaimResult.Unavailable, "unknown history rejected")
        expect(FirstDispatchGate(MemorySlot(env(old))).claim(6, old, key, next) == ClaimResult.Conflict, "stale revision rejected")
        expect(FirstDispatchGate(MemorySlot(env(old))).claim(7, "{}", key, next) == ClaimResult.Unavailable, "malformed expected payload rejected")
    }
    run {
        val alteredOtherOld = registry(op("ready", 1), "unchanged")
        val alteredOtherNew = registry(op("in_flight", 2, "gen-A"), "changed")
        expect(FirstDispatchGate(MemorySlot(env(alteredOtherOld))).claim(7, alteredOtherOld, key, alteredOtherNew) == ClaimResult.Unavailable,
            "non-target registry entry mutation rejected")
        val wrongState = registry(op("uncertain", 3, "gen-A"))
        expect(FirstDispatchGate(MemorySlot(env(old))).claim(7, old, key, wrongState) == ClaimResult.Unavailable,
            "non-target state transition rejected")
        val changedReq = registry(op("in_flight", 2, "gen-A", "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions\",\"idempotencyKey\":\"idem-0002\",\"body\":${quote(body)}}"))
        expect(FirstDispatchGate(MemorySlot(env(old))).claim(7, old, key, changedReq) == ClaimResult.Unavailable, "request mutation rejected")
    }
    run {
        expect(strictJsonObject("{\"x\":\"\\u4e2d\"}"), "valid ASCII hex Unicode escape accepted")
        expect(strictJsonObject("{\"x\":\"\\uD83C\\uDFCB\"}"), "valid surrogate pair accepted")
        for (badDigits in listOf("+123", "-123", "１２３４", "١٢٣٤")) {
            val invalidEscape = "\\" + "u" + badDigits
            val invalidBody = "{\"source\":\"free\",\"date\":\"2026-11-04\",\"timeZone\":\"Asia/$invalidEscape\",\"clientEventId\":\"event-0001\"}"
            expect(!strictJsonObject(invalidBody), "signed Unicode escape rejected as invalid JSON")
            val request = "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions\",\"idempotencyKey\":\"idem-0001\",\"body\":${quote(invalidBody)}}"
            val badReady = registry(op("ready", 1, request = request))
            val badFlight = registry(op("in_flight", 2, "gen-A", request))
            expect(FirstDispatchGate(MemorySlot(env(badReady))).claim(7, badReady, key, badFlight) == ClaimResult.Unavailable,
                "invalid Unicode escape cannot claim a dispatch")
        }
    }
    run {
        val unsafeIntegerOperation = op("ready", 1).replace("\"createdAt\":100", "\"createdAt\":9007199254740992")
        val unsafeInteger = registry(unsafeIntegerOperation)
        expect(FirstDispatchGate(MemorySlot(env(unsafeInteger))).claim(7, unsafeInteger, key, next) == ClaimResult.Unavailable,
            "unsafe JavaScript integer rejected")
        val duplicateRegistry = "{\"schema\":1,\"schema\":1,\"entries\":{}}"
        expect(FirstDispatchGate(MemorySlot(env(old))).claim(7, duplicateRegistry, key, next) == ClaimResult.Unavailable, "duplicate registry field rejected")
        val duplicateOp = op("ready", 1).replace("\"schema\":1,", "\"schema\":1,\"schema\":1,")
        expect(FirstDispatchGate(MemorySlot(env(registry(duplicateOp)))).claim(7, registry(duplicateOp), key, next) == ClaimResult.Unavailable,
            "duplicate operation field rejected")
        val duplicateBody = "{\"clientEventId\":\"event-0001\",\"clientEventId\":\"event-0001\"}"
        val badBody = registry(op("ready", 1, request = "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions\",\"idempotencyKey\":\"idem-0001\",\"body\":${quote(duplicateBody)}}"))
        expect(FirstDispatchGate(MemorySlot(env(badBody))).claim(7, badBody, key, next) == ClaimResult.Unavailable, "duplicate request-body field rejected")
    }
    run {
        val huge = old.dropLast(2) + "," + "\"x\":\"" + "x".repeat(65_540) + "\"}}"
        expect(FirstDispatchGate(MemorySlot(env(huge))).claim(7, huge, key, next) == ClaimResult.Unavailable, "size boundary rejected")
        val slot = MemorySlot(env(old)); val ready = CountDownLatch(1); val done = CountDownLatch(2); val winners = AtomicInteger()
        val pool = Executors.newFixedThreadPool(2)
        repeat(2) { pool.execute { ready.countDown(); ready.await(); if (claim(slot) is ClaimResult.Claimed) winners.incrementAndGet(); done.countDown() } }
        ready.await(); done.await(); pool.shutdown(); expect(winners.get() == 1, "concurrent claim single winner")
    }
    run {
        val setsBody = "{\"expectedVersion\":1,\"exerciseEntryKeys\":[\"entry-0001\"],\"sets\":[{\"recordKey\":\"record-001\",\"exerciseEntryKey\":\"entry-0001\",\"exerciseName\":\"深蹲\",\"kind\":\"weight\",\"setIndex\":1,\"weightKg\":42.5,\"reps\":8,\"rpe\":null,\"completed\":true}]}"
        val finishBody = "{\"expectedVersion\":2,\"date\":\"2026-11-04\",\"durationMin\":null,\"rpe\":7.5,\"note\":null}"
        for ((path, payload) in listOf("/me/sessions/55/sets" to setsBody, "/me/sessions/55/finish" to finishBody)) {
            val request = "\"request\":{\"method\":\"POST\",\"path\":${quote(path)},\"idempotencyKey\":\"route-key-0001\",\"body\":${quote(payload)}}"
            val before = registry(op("ready", 1, request = request)); val after = registry(op("in_flight", 2, "gen-A", request))
            expect(FirstDispatchGate(MemorySlot(env(before))).claim(7, before, key, after) == ClaimResult.Claimed(8), "production $path request accepted without clientEventId body field")
        }
    }
    run {
        // JS JSON.stringify keeps insertion order; marker normalization sorts keys without requiring sorted wire bytes.
        val ordered = "{\"source\":\"free\",\"date\":\"2026-11-04\",\"timeZone\":\"Asia/Shanghai\",\"clientEventId\":\"event-0001\"}"
        val request = "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions\",\"idempotencyKey\":\"order-key-0001\",\"body\":${quote(ordered)}}"
        val before = registry(op("ready", 1, request = request)); val after = registry(op("in_flight", 2, "gen-A", request))
        expect(FirstDispatchGate(MemorySlot(env(before))).claim(7, before, key, after) == ClaimResult.Claimed(8), "production JSON.stringify property order accepted")
    }
    run {
        fun setRegistry(number: String, idempotency: String, state: String, revision: Int): String {
            val payload = "{\"expectedVersion\":$number,\"exerciseEntryKeys\":[]}"
            val req = "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions/55/sets\",\"idempotencyKey\":${quote(idempotency)},\"body\":${quote(payload)}}"
            return registry(op(state, revision, if (state == "in_flight") "gen-A" else null, req))
        }
        val first = setRegistry("1", "number-key-0001", "ready", 1)
        val nextFirst = setRegistry("1", "number-key-0001", "in_flight", 2)
        val slot = MemorySlot(env(first)); expect(FirstDispatchGate(slot).claim(7, first, key, nextFirst) == ClaimResult.Claimed(8), "integer JSON request accepted")
        for (numeric in listOf("1.0", "1e0")) {
            val changed = setRegistry(numeric, "number-key-0002", "ready", 1)
            val changedFlight = setRegistry(numeric, "number-key-0002", "in_flight", 2)
            val settled = slot.snapshot().copy(revision = 20, payload = changed, activeBindings = emptyMap())
            expect(FirstDispatchGate(MemorySlot(settled)).claim(20, changed, key, changedFlight) == ClaimResult.Conflict,
                "JS equivalent JSON number $numeric shares body history marker")
        }
    }
    run {
        val firstSlot = MemorySlot(env(old)); expect(claim(firstSlot) == ClaimResult.Claimed(8), "first event claim")
        val request = "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions/55/finish\",\"idempotencyKey\":\"later-key-0001\",\"body\":${quote("{\"expectedVersion\":2}")}}"
        val settledPayload = registry(op("ready", 1, request = request))
        val laterFlight = registry(op("in_flight", 2, "gen-A", request))
        val settled = firstSlot.snapshot().copy(revision = 20, payload = settledPayload, activeBindings = emptyMap())
        expect(FirstDispatchGate(MemorySlot(settled)).claim(20, settledPayload, key, laterFlight) == ClaimResult.Claimed(21),
            "cleared active event permits later different request")
    }
    run {
        fun transition(path: String, requestBody: String): Pair<String, String> {
            val request = "\"request\":{\"method\":\"POST\",\"path\":${quote(path)},\"idempotencyKey\":\"invalid-key-0001\",\"body\":${quote(requestBody)}}"
            return registry(op("ready", 1, request = request)) to registry(op("in_flight", 2, "gen-A", request))
        }
        for (date in listOf("2024-02-29", "2000-02-29")) {
            val valid = "{\"source\":\"free\",\"date\":${quote(date)},\"clientEventId\":\"event-0001\"}"
            val (before, after) = transition("/me/sessions", valid)
            expect(FirstDispatchGate(MemorySlot(env(before))).claim(7, before, key, after) == ClaimResult.Claimed(8), "Gregorian leap date $date accepted")
        }
        val invalidBodies = listOf(
            "/me/sessions" to "{\"source\":\"free\",\"date\":\"2026-11-04\",\"clientEventId\":\"event-0001\",\"timeZone\":1}",
            "/me/sessions" to "{\"source\":\"free\",\"date\":\"2026-02-30\",\"clientEventId\":\"event-0001\"}",
            "/me/sessions" to "{\"source\":\"free\",\"date\":\"1900-02-29\",\"clientEventId\":\"event-0001\"}",
            "/me/sessions" to "{\"source\":\"free\",\"date\":\"0000-01-01\",\"clientEventId\":\"event-0001\"}",
            "/me/sessions/55/finish" to "{\"expectedVersion\":2,\"date\":1}",
            "/me/sessions/55/finish" to "{\"expectedVersion\":2,\"date\":\"2026-02-30\"}",
            "/me/sessions" to "{\"source\":\"plan\",\"date\":\"2026-11-04\",\"clientEventId\":\"event-0001\",\"planId\":\"20\",\"planVersion\":1,\"planDayId\":\"9\",\"occurrenceId\":1}",
            "/me/sessions/55/sets" to "{\"expectedVersion\":1,\"exerciseEntryKeys\":[\"entry-0001\"],\"sets\":[{\"recordKey\":\"record-001\",\"exerciseEntryKey\":\"entry-0001\",\"exerciseId\":1,\"exerciseName\":\"深蹲\",\"kind\":\"weight\",\"setIndex\":1,\"weightKg\":42.5,\"reps\":8,\"completed\":true}]}",
            "/me/sessions/55/sets" to "{\"expectedVersion\":1,\"exerciseEntryKeys\":[\"entry-0001\"],\"sets\":[{\"recordKey\":\"record-001\",\"exerciseEntryKey\":\"entry-0001\",\"exerciseName\":\"深蹲\",\"kind\":\"weight\",\"setIndex\":1,\"weightKg\":1e-7,\"reps\":8,\"completed\":true}]}"
        )
        invalidBodies.forEachIndexed { index, (path, requestBody) ->
            val (before, after) = transition(path, requestBody)
            expect(FirstDispatchGate(MemorySlot(env(before))).claim(7, before, key, after) == ClaimResult.Unavailable, "invalid optional type/date/weight case $index rejected")
        }
    }
    run {
        fun finishBody(duration: String, idem: String): Pair<String, String> {
            val payload = "{\"expectedVersion\":2,\"durationMin\":$duration}"
            val request = "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions/55/finish\",\"idempotencyKey\":${quote(idem)},\"body\":${quote(payload)}}"
            return registry(op("ready", 1, request = request)) to registry(op("in_flight", 2, "gen-A", request))
        }
        val (zeroOld, zeroFlight) = finishBody("0", "zero-key-0001")
        val slot = MemorySlot(env(zeroOld)); expect(FirstDispatchGate(slot).claim(7, zeroOld, key, zeroFlight) == ClaimResult.Claimed(8), "zero duration accepted")
        for (equivalent in listOf("0e-128", "0e+128", "-0", "0.0")) {
            val (same, sameFlight) = finishBody(equivalent, "zero-key-0002")
            val settled = slot.snapshot().copy(revision = 20, payload = same, activeBindings = emptyMap())
            expect(FirstDispatchGate(MemorySlot(settled)).claim(20, same, key, sameFlight) == ClaimResult.Conflict,
                "zero numeric spelling $equivalent matches JSON zero history")
        }
    }
    run {
        fun setVersion(version: String, idem: String): Pair<String, String> {
            val payload = "{\"expectedVersion\":$version,\"exerciseEntryKeys\":[]}"
            val request = "\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions/55/sets\",\"idempotencyKey\":${quote(idem)},\"body\":${quote(payload)}}"
            return registry(op("ready", 1, request = request)) to registry(op("in_flight", 2, "gen-A", request))
        }
        val (a, aFlight) = setVersion("9007199254740990", "adjacent-key-0001")
        val slot = MemorySlot(env(a)); expect(FirstDispatchGate(slot).claim(7, a, key, aFlight) == ClaimResult.Claimed(8), "first adjacent binary64 integer accepted")
        val (b, bFlight) = setVersion("9007199254740991", "adjacent-key-0002")
        val settled = slot.snapshot().copy(revision = 20, payload = b, activeBindings = emptyMap())
        expect(FirstDispatchGate(MemorySlot(settled)).claim(20, b, key, bFlight) == ClaimResult.Claimed(21),
            "adjacent binary64 values have distinct body markers")
    }
    run {
        fun transition(path: String, requestBody: String): Pair<String, String> {
            val request = "\"request\":{\"method\":\"POST\",\"path\":${quote(path)},\"idempotencyKey\":\"decimal-key-0001\",\"body\":${quote(requestBody)}}"
            return registry(op("ready", 1, request = request)) to registry(op("in_flight", 2, "gen-A", request))
        }
        fun result(path: String, requestBody: String): ClaimResult {
            val (before, after) = transition(path, requestBody)
            return FirstDispatchGate(MemorySlot(env(before))).claim(7, before, key, after)
        }
        val setsPath = "/me/sessions/55/sets"
        for (version in listOf("1", "1.0", "1e0", "10e-1", "1." + "0".repeat(126))) {
            expect(result(setsPath, "{\"expectedVersion\":$version,\"exerciseEntryKeys\":[]}") is ClaimResult.Claimed,
                "exact integer $version accepted")
        }
        for (version in listOf("1.0000000000000001", "9007199254740991.0000000000000001", "1." + "0".repeat(127))) {
            expect(result(setsPath, "{\"expectedVersion\":$version,\"exerciseEntryKeys\":[]}") == ClaimResult.Unavailable,
                "fractional or oversized numeric token $version rejected")
        }
        val finishPath = "/me/sessions/55/finish"
        for (number in listOf("0e-128", "0e+128")) {
            expect(result(finishPath, "{\"expectedVersion\":1,\"durationMin\":$number}") is ClaimResult.Claimed,
                "duration exponent boundary $number accepted")
        }
        for (number in listOf("0e-129", "0e+129", "0e-999")) {
            expect(result(finishPath, "{\"expectedVersion\":1,\"durationMin\":$number}") == ClaimResult.Unavailable,
                "duration exponent beyond server boundary $number rejected")
        }
        fun weightBody(weight: String, reps: String = "8", index: String = "1", rpe: String = "7.5") =
            "{\"expectedVersion\":1,\"exerciseEntryKeys\":[\"entry-0001\"],\"sets\":[{\"recordKey\":\"record-001\",\"exerciseEntryKey\":\"entry-0001\",\"exerciseName\":\"深蹲\",\"kind\":\"weight\",\"setIndex\":$index,\"weightKg\":$weight,\"reps\":$reps,\"rpe\":$rpe,\"completed\":true}]}"
        expect(result(setsPath, weightBody("4.250e1", index = "1e0")) is ClaimResult.Claimed,
            "server-valid exact weight and integer exponent accepted")
        for (body in listOf(weightBody("42.500"), weightBody("42.500000000000001"), weightBody("42.5", reps = "8.0000000000000001"), weightBody("42.5", rpe = "7.500"))) {
            expect(result(setsPath, body) == ClaimResult.Unavailable, "server-invalid precision rejected")
        }
    }
    run {
        fun result(path: String, requestBody: String): ClaimResult {
            val request = "\"request\":{\"method\":\"POST\",\"path\":${quote(path)},\"idempotencyKey\":\"v2-test-key-1\",\"body\":${quote(requestBody)}}"
            val before = registry(op("ready", 1, request = request)); val after = registry(op("in_flight", 2, "gen-A", request))
            return FirstDispatchGate(MemorySlot(env(before))).claim(7, before, key, after)
        }
        val create = """{"source":"free","date":"2026-11-04","clientEventId":"event-0001","mode":"after_workout"}"""
        val blank = """{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"addEntries":[{"exerciseEntryKey":"entry-0001","exerciseName":"深蹲"}],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-0001","setIndex":1,"confirmed":false,"measurement":{"method":"total"}}]}"""
        val sides = """{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-0001","setIndex":1,"confirmed":true,"measurement":{"method":"sides","left":{"state":"performed","weightKg":"12","reps":10},"right":{"state":"not_performed"}}}]}"""
        val assisted = """{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-0001","setIndex":1,"confirmed":false,"measurement":{"method":"bodyweight_assisted","bodyweightKg":"70","adjustmentKg":"20","reps":8}}]}"""
        expect(result("/me/recordings", create) is ClaimResult.Claimed, "v2 create accepted")
        expect(result("/me/recordings/55/sets", blank) is ClaimResult.Claimed, "v2 blank set accepted")
        expect(result("/me/recordings/55/sets", """{"expectedVersion":1,"date":"2026-02-28","exerciseEntryKeys":[]}""") is ClaimResult.Claimed, "v2 training date accepted")
        expect(result("/me/recordings/55/sets", """{"expectedVersion":1,"date":"2026-02-30","exerciseEntryKeys":[]}""") == ClaimResult.Unavailable, "v2 invalid training date rejected")
        expect(result("/me/recordings/55/sets", sides) is ClaimResult.Claimed, "v2 unilateral set accepted")
        expect(result("/me/recordings/55/sets", assisted) is ClaimResult.Claimed, "v2 assisted set accepted")
        expect(result("/me/recordings/55/finish", """{"expectedVersion":2}""") is ClaimResult.Claimed, "v2 finish accepted")
        expect(result("/me/recordings", create.replace("event-0001", "event-other")) == ClaimResult.Unavailable, "v2 wrong event rejected")
        expect(result("/me/recordings", create.replace("2026-11-04", "2026-02-30")) == ClaimResult.Unavailable, "v2 invalid date rejected")
        expect(result("/me/recordings/55/sets", blank.replace("\"confirmed\":false", "\"confirmed\":1")) == ClaimResult.Unavailable, "v2 non boolean confirmation rejected")
        expect(result("/me/recordings/55/sets", assisted.replace("\"20\"", "\"80\"")) == ClaimResult.Unavailable, "v2 excessive assistance rejected")
        expect(result("/me/recordings/55/sets", blank.replace("\"expectedVersion\":1", "\"expectedVersion\":9007199254740992")) == ClaimResult.Unavailable, "v2 unsafe version rejected")
        expect(result("/me/recordings/55/finish", """{"expectedVersion":1,"extra":0}""") == ClaimResult.Unavailable, "v2 extra finish field rejected")
        expect(result("/me/recordings/55/other", blank) == ClaimResult.Unavailable, "v2 invalid route rejected")
    }
    println("PASS 15 synthetic groups including recording v2")
}
