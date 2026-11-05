package uts.sdk.modules.neraeSecureVault

import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import java.nio.ByteBuffer
import java.security.MessageDigest
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

private class MemoryStorage : VaultStorage {
    @Volatile var bytes: ByteArray? = null
    @Volatile var throwAfterWrite = false
    @Synchronized override fun read(): ByteArray? = bytes?.copyOf()
    @Synchronized override fun writeAtomically(value: ByteArray) {
        bytes = value.copyOf()
        if (throwAfterWrite) { throwAfterWrite = false; throw IllegalStateException("uncertain post-write failure") }
    }
    @Synchronized override fun clear() { bytes = null }
}
private class MemoryKeys : VaultKeys {
    @Volatile private var key: SecretKeySpec? = null
    override fun existing() = key
    override fun createHardware(): SecretKeySpec { check(key == null); return SecretKeySpec(ByteArray(32) { (it + 1).toByte() }, "AES").also { key = it } }
    override fun delete() { key = null }
}
private val validator = VaultPayloadValidator(::strictJsonObject)
private const val operationKey = "nerae.workout.operation.v1.12.event-0001"
private const val createBody = "{\"source\":\"free\",\"date\":\"2026-11-04\",\"clientEventId\":\"event-0001\"}"
private fun operation(state: String, revision: Int, attempt: String? = null, body: String = createBody, idem: String = "idem-key-0001") =
    "{\"schema\":1,\"subjectId\":\"12\",\"clientEventId\":\"event-0001\",\"createdGeneration\":\"auth-gen-a\",\"revision\":$revision,\"state\":\"$state\",\"createdAt\":100,\"expiresAt\":200,\"request\":{\"method\":\"POST\",\"path\":\"/me/sessions\",\"idempotencyKey\":\"$idem\",\"body\":${quote(body)}}" +
        (attempt?.let { ",\"attemptGeneration\":\"$it\"}" } ?: "}")
private fun quote(value: String) = "\"" + value.replace("\\", "\\\\").replace("\"", "\\\"") + "\""
private fun registry(op: String) = "{\"schema\":1,\"entries\":{\"$operationKey\":${quote(op)}}}"
private fun registryWithOther(op: String, other: String = operation("ready", 1).replace("event-0001", "event-0002").replace("idem-key-0001", "idem-key-0002")): String {
    val otherKey = "nerae.workout.operation.v1.12.event-0002"
    return "{\"schema\":1,\"entries\":{\"$operationKey\":${quote(op)},\"$otherKey\":${quote(other)}}}"
}
private val readyPayload = registry(operation("ready", 1))
private val flightPayload = registry(operation("in_flight", 2, "auth-gen-a"))
private data class Fixture(val legacy: MemoryStorage, val current: MemoryStorage, val keys: MemoryKeys, val lock: Any) {
    fun journal() = NeraeVersionedTrainingJournal(legacy, current, keys, lock, validator)
    companion object { fun empty() = Fixture(MemoryStorage(), MemoryStorage(), MemoryKeys(), Any()) }
}
private fun expect(ok: Boolean, label: String) { check(ok) { "failed: $label" } }
private inline fun expectVaultFailure(label: String, action: () -> Unit) {
    val failure = runCatching(action).exceptionOrNull()
    check(failure is VaultFailure) { "failed: $label; got $failure" }
}
private inline fun expectVaultFailureCode(code: String, label: String, action: () -> Unit) {
    val failure = runCatching(action).exceptionOrNull()
    check(failure is VaultFailure && failure.code == code) { "failed: $label; got $failure" }
}

private fun marker(seed: Int): String = MessageDigest.getInstance("SHA-256")
    .digest(ByteBuffer.allocate(4).putInt(seed).array()).joinToString("") { "%02x".format(it) }
private fun markerBytes(value: String) = ByteArray(32) { i -> value.substring(i * 2, i * 2 + 2).toInt(16).toByte() }
private fun v3Plaintext(payload: String, markers: List<String>, active: Map<String, String>, known: Boolean = true, tail: ByteArray = ByteArray(0)): ByteArray {
    val bytes = ByteArrayOutputStream()
    DataOutputStream(bytes).use { out ->
        out.write(byteArrayOf(0x4e, 0x4a, 0x33, 0x00)); out.writeByte(if (known) 1 else 0)
        val value = payload.toByteArray(Charsets.UTF_8); out.writeInt(value.size); out.write(value)
        out.writeInt(markers.size); markers.forEach { out.write(markerBytes(it)) }
        out.writeInt(active.size); active.toSortedMap().forEach { (event, binding) -> out.write(markerBytes(event)); out.write(markerBytes(binding)) }
        out.write(tail)
    }
    return bytes.toByteArray()
}
private fun v3Envelope(revision: Long, payload: String, markers: List<String>, active: Map<String, String>, key: SecretKeySpec, tail: ByteArray = ByteArray(0)): ByteArray {
    val header = ByteBuffer.allocate(5 + Long.SIZE_BYTES + 2 + 32)
        .put(byteArrayOf(0x4e, 0x52, 0x56, 0x41)).put(3).putLong(revision)
        .put(1).put(0).put(ByteArray(32)).array()
    val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE, key)
    val nonce = cipher.iv; cipher.updateAAD(header)
    return header + nonce + cipher.doFinal(v3Plaintext(payload, markers, active, tail = tail))
}
private fun damagedV3Envelope(revision: Long, plaintext: ByteArray, key: SecretKeySpec): ByteArray {
    val header = ByteBuffer.allocate(5 + Long.SIZE_BYTES + 2 + 32)
        .put(byteArrayOf(0x4e, 0x52, 0x56, 0x41)).put(3).putLong(revision)
        .put(1).put(0).put(ByteArray(32)).array()
    val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE, key)
    val nonce = cipher.iv; cipher.updateAAD(header)
    return header + nonce + cipher.doFinal(plaintext)
}

/** Produces an authentic AES-GCM v2 envelope using the old production header shape. */
private fun legacyV2Envelope(revision: Long, found: Boolean, plaintext: ByteArray, key: SecretKeySpec): ByteArray {
    val header = ByteBuffer.allocate(5 + Long.SIZE_BYTES + 2 + 32)
        .put(byteArrayOf(0x4e, 0x52, 0x56, 0x41)).put(2).putLong(revision)
        .put(if (found) 1 else 0).put(0).put(ByteArray(32)).array()
    val cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE, key)
    val nonce = cipher.iv; cipher.updateAAD(header)
    return header + nonce + cipher.doFinal(plaintext)
}

fun main() {
    fun result(ok: Boolean, code: String = "", revision: String = "") =
        "{\"ok\":$ok,\"errorCode\":\"$code\",\"found\":false,\"value\":\"\",\"revision\":\"$revision\"}"
    fun claim(journal: NeraeVersionedTrainingJournal, revision: String?, before: String? = readyPayload,
              key: String? = operationKey, after: String? = flightPayload): String =
        NeraeAndroidSecureVaultBridge.claimTrainingOperationDispatch(journal, revision, before, key, after)

    run {
        val f = Fixture.empty(); val journal = f.journal()
        journal.compareAndSet("0", false, "", true, readyPayload)
        expect(claim(journal, "1") == result(true, revision = "2"), "bridge returns only committed revision")
        expect(journal.read() == VersionedJournalResult(true, flightPayload, 2), "bridge forwards exact request")
        val before = f.current.read()!!
        expect(claim(journal, "1") == result(false, "conflict"), "stale bridge claim is redacted conflict")
        expect(f.current.read()!!.contentEquals(before), "stale bridge claim leaves ciphertext unchanged")
        expect(claim(journal, null) == result(false, "invalid_state"), "null revision fails closed")
        expect(claim(journal, "0") == result(false, "invalid_state"), "zero revision fails closed")
        expect(claim(journal, "2", before = null) == result(false, "invalid_state"), "null expected value fails closed")
        expect(claim(journal, "2", key = null) == result(false, "invalid_state"), "null operation key fails closed")
        expect(claim(journal, "2", after = null) == result(false, "invalid_state"), "null next value fails closed")
        expect(NeraeAndroidSecureVaultBridge.claimTrainingOperationDispatch(
            null as android.content.Context?, "1", readyPayload, operationKey, flightPayload
        ) == result(false, "unavailable"), "missing Android context fails closed")
    }
    run {
        val f = Fixture.empty(); val aes = f.keys.createHardware()
        f.current.bytes = legacyV2Envelope(1, true, readyPayload.toByteArray(Charsets.UTF_8), aes)
        val before = f.current.read()!!
        expect(claim(f.journal(), "1") == result(false, "unavailable"), "bridge refuses unknown history")
        expect(f.current.read()!!.contentEquals(before), "unknown history is preserved")
    }
    run {
        val f = Fixture.empty(); val aes = f.keys.createHardware()
        f.current.bytes = v3Envelope(1, readyPayload, (0 until 8192).map(::marker), emptyMap(), aes)
        val before = f.current.read()!!
        expect(claim(f.journal(), "1") == result(false, "unavailable"), "bridge refuses full history")
        expect(f.current.read()!!.contentEquals(before), "full history is preserved")
    }
    run {
        val f = Fixture.empty(); val journal = f.journal()
        journal.compareAndSet("0", false, "", true, readyPayload)
        f.current.throwAfterWrite = true
        expect(claim(journal, "1") == result(false, "unavailable"), "uncertain bridge write gives no permit")
        expect(journal.read() == VersionedJournalResult(true, flightPayload, 2), "uncertain committed bytes remain")
        expectVaultFailureCode("invalid_state", "uncertain bridge result cannot be rewound") {
            journal.compareAndSet("2", true, flightPayload, true, readyPayload)
        }
        expect(journal.read() == VersionedJournalResult(true, flightPayload, 2), "uncertain bridge request remains intact")
    }
    run {
        val f = Fixture.empty(); val journal = f.journal()
        journal.compareAndSet("0", false, "", true, readyPayload)
        expect(claim(journal, "1") == result(true, revision = "2"), "stage active operation")
        val before = f.current.read()!!
        val changedKey = registry(operation("in_flight", 2, "auth-gen-a", idem = "idem-key-0002"))
        val changedBody = registry(operation("in_flight", 2, "auth-gen-a", body = createBody.replace("2026-11-04", "2026-11-03")))
        val activeOperation = operation("in_flight", 2, "auth-gen-a")
        val replacements = listOf(
            "ready rewind" to readyPayload, "changed key" to changedKey, "changed body" to changedBody,
            "body whitespace" to registry(operation("in_flight", 2, "auth-gen-a", body = createBody.replace("{", "{ "))),
            "changed subject" to registry(activeOperation.replace("\"subjectId\":\"12\"", "\"subjectId\":\"13\"")),
            "changed event" to registry(activeOperation.replace("event-0001", "event-0002")),
            "changed path" to registry(activeOperation.replace("\"path\":\"/me/sessions\"", "\"path\":\"/me/sessions/1/finish\"")),
            "changed creation time" to registry(activeOperation.replace("\"createdAt\":100", "\"createdAt\":99")),
            "changed generation" to registry(activeOperation.replace("\"createdGeneration\":\"auth-gen-a\"", "\"createdGeneration\":\"auth-gen-b\"")),
            "changed attempt" to registry(operation("in_flight", 2, "auth-gen-b"))
        )
        for ((label, replacement) in replacements) {
            expectVaultFailureCode("invalid_state", "versioned CAS rejects $label") {
                journal.compareAndSet("2", true, flightPayload, true, replacement)
            }
            expectVaultFailureCode("invalid_state", "legacy CAS rejects $label") {
                journal.compareAndSetLegacy(true, flightPayload, replacement)
            }
            expect(f.current.read()!!.contentEquals(before), "$label keeps encrypted active request")
        }
        expectVaultFailureCode("invalid_state", "versioned CAS cannot delete an active request") {
            journal.compareAndSet("2", true, flightPayload, false, "")
        }
        expect(f.current.read()!!.contentEquals(before), "delete keeps encrypted active request")
        val uncertain = registry(operation("uncertain", 3))
        expect(journal.compareAndSetLegacy(true, flightPayload, uncertain) == 3L, "legacy CAS may mark the same request uncertain")
        val uncertainAgain = registry(operation("uncertain", 4))
        expect(journal.compareAndSet("3", true, uncertain, true, uncertainAgain) == 4L, "versioned CAS may advance uncertainty")
        expect(journal.read() == VersionedJournalResult(true, uncertainAgain, 4), "uncertain request remains readable")
        expectVaultFailureCode("invalid_state", "uncertain operation revision cannot rewind") {
            journal.compareAndSetLegacy(true, uncertainAgain, uncertain)
        }
        val withOther = registryWithOther(operation("uncertain", 4))
        expect(journal.compareAndSet("4", true, uncertainAgain, true, withOther) == 5L, "another event may be staged")
        expect(journal.compareAndSetLegacy(true, withOther, uncertainAgain) == 6L, "unrelated event may be removed")
    }
    run {
        // A v3 envelope may predate this guard and already contain an altered
        // active entry. Neither CAS nor a different event may silently advance it.
        val f = Fixture.empty(); val aes = f.keys.createHardware()
        val identity = FirstDispatchNativeParser.derive(readyPayload, flightPayload, operationKey)
        val changed = operation("in_flight", 2, "auth-gen-a", idem = "idem-key-0002")
        val otherFlight = operation("in_flight", 2, "auth-gen-a").replace("event-0001", "event-0002").replace("idem-key-0001", "idem-key-0002")
        val corrupted = registryWithOther(changed)
        val next = registryWithOther(changed, otherFlight)
        f.current.bytes = v3Envelope(1, corrupted, listOf(identity.keyMarker, identity.bodyMarker), mapOf(identity.eventMarker to identity.binding), aes)
        val before = f.current.read()!!
        expectVaultFailureCode("invalid_state", "CAS refuses a preexisting broken active binding") {
            f.journal().compareAndSet("1", true, corrupted, true, corrupted)
        }
        expectVaultFailureCode("unavailable", "new claim refuses a preexisting broken active binding") {
            f.journal().claimFirstSend("1", corrupted, "nerae.workout.operation.v1.12.event-0002", next)
        }
        expect(f.current.read()!!.contentEquals(before), "broken active binding preserves original ciphertext")
    }
    run {
        val f = Fixture.empty(); val journal = f.journal()
        journal.compareAndSet("0", false, "", true, readyPayload)
        journal.claimFirstSend("1", readyPayload, operationKey, flightPayload)
        val first = operation("in_flight", 2, "auth-gen-a")
        val withReady = registryWithOther(first)
        expect(journal.compareAndSet("2", true, flightPayload, true, withReady) == 3L, "stage a second event beside an active one")
        val otherFlight = operation("in_flight", 2, "auth-gen-a").replace("event-0001", "event-0002").replace("idem-key-0001", "idem-key-0002")
        val bothActive = registryWithOther(first, otherFlight)
        val otherKey = "nerae.workout.operation.v1.12.event-0002"
        expect(journal.claimFirstSend("3", withReady, otherKey, bothActive) == 4L, "claim another independent event")
        val before = f.current.read()!!
        expectVaultFailureCode("invalid_state", "cannot remove one of two active events") {
            journal.compareAndSet("4", true, bothActive, true, registry(first))
        }
        val alteredOther = otherFlight.replace("idem-key-0002", "idem-key-0003")
        expectVaultFailureCode("invalid_state", "cannot change the second active event") {
            journal.compareAndSetLegacy(true, bothActive, registryWithOther(first, alteredOther))
        }
        expect(f.current.read()!!.contentEquals(before), "two-event rejection preserves ciphertext")
        val firstUncertain = operation("uncertain", 3)
        val mixed = registryWithOther(firstUncertain, otherFlight)
        expect(journal.compareAndSetLegacy(true, bothActive, mixed) == 5L, "first active event can become uncertain")
        val otherUncertain = operation("uncertain", 3).replace("event-0001", "event-0002").replace("idem-key-0001", "idem-key-0002")
        expect(journal.compareAndSet("5", true, mixed, true, registryWithOther(firstUncertain, otherUncertain)) == 6L,
            "second active event can become uncertain")
    }
    run {
        val f = Fixture.empty(); val journal = f.journal()
        journal.compareAndSet("0", false, "", true, readyPayload)
        journal.claimFirstSend("1", readyPayload, operationKey, flightPayload)
        val first = operation("in_flight", 2, "auth-gen-a")
        val otherKey = "nerae.workout.operation.v1.13.event-0002"
        fun other(state: String, revision: Int, attempt: String? = null) = operation(state, revision, attempt)
            .replace("\"subjectId\":\"12\"", "\"subjectId\":\"13\"")
            .replace("event-0001", "event-0002").replace("idem-key-0001", "idem-key-0002")
        fun both(second: String) = "{\"schema\":1,\"entries\":{\"$operationKey\":${quote(first)},\"$otherKey\":${quote(second)}}}"
        val staged = both(other("ready", 1))
        expect(journal.compareAndSet("2", true, flightPayload, true, staged) == 3L, "different subject may stage without touching active request")
        val bothActive = both(other("in_flight", 2, "auth-gen-b"))
        expect(journal.claimFirstSend("3", staged, otherKey, bothActive) == 4L, "different subject may claim while first subject remains active")
        val before = f.current.read()!!
        expectVaultFailureCode("invalid_state", "other subject cannot remove first subject's active request") {
            journal.compareAndSet("4", true, bothActive, true, "{\"schema\":1,\"entries\":{\"$otherKey\":${quote(other("in_flight", 2, "auth-gen-b"))}}}")
        }
        expect(f.current.read()!!.contentEquals(before), "cross-subject rejection preserves both active requests")
    }
    run {
        val identity = FirstDispatchNativeParser.derive(readyPayload, flightPayload, operationKey)
        val active = registryWithOther(operation("in_flight", 2, "auth-gen-a"))
        val otherFlight = operation("in_flight", 2, "auth-gen-a").replace("event-0001", "event-0002").replace("idem-key-0001", "idem-key-0002")
        for ((missing, remaining) in listOf("body" to identity.keyMarker, "key" to identity.bodyMarker)) {
            val f = Fixture.empty(); val aes = f.keys.createHardware()
            f.current.bytes = v3Envelope(1, active, listOf(remaining), mapOf(identity.eventMarker to identity.binding), aes)
            val before = f.current.read()!!
            expectVaultFailureCode("invalid_state", "CAS refuses an active request missing its $missing marker") {
                f.journal().compareAndSet("1", true, active, true, active)
            }
            expectVaultFailureCode("unavailable", "new claim refuses an active request missing its $missing marker") {
                f.journal().claimFirstSend("1", active, "nerae.workout.operation.v1.12.event-0002",
                    registryWithOther(operation("in_flight", 2, "auth-gen-a"), otherFlight))
            }
            expect(f.current.read()!!.contentEquals(before), "missing $missing history keeps original ciphertext")
        }
    }
    run {
        val event = "训练事件 01%"
        val key = "nerae.workout.operation.v1.12.%E8%AE%AD%E7%BB%83%E4%BA%8B%E4%BB%B6%2001%25"
        fun namedRegistry(entry: String, operationKey: String = key) = "{\"schema\":1,\"entries\":{\"$operationKey\":${quote(entry)}}}"
        val ready = namedRegistry(operation("ready", 1).replace("event-0001", event))
        val flight = namedRegistry(operation("in_flight", 2, "auth-gen-a").replace("event-0001", event))
        val f = Fixture.empty(); val journal = f.journal()
        journal.compareAndSet("0", false, "", true, ready)
        expect(journal.claimFirstSend("1", ready, key, flight) == 2L, "Unicode/space/percent event can claim")
        val before = f.current.read()!!
        val changedKey = namedRegistry(operation("in_flight", 2, "auth-gen-a").replace("event-0001", event), key.replace("%25", "%2525"))
        expectVaultFailureCode("invalid_state", "percent-encoded active event key cannot be replaced") {
            journal.compareAndSet("2", true, flight, true, changedKey)
        }
        expect(f.current.read()!!.contentEquals(before), "Unicode event rejection preserves ciphertext")
        val uncertain = namedRegistry(operation("uncertain", 3).replace("event-0001", event))
        expect(journal.compareAndSetLegacy(true, flight, uncertain) == 3L, "Unicode event can become uncertain")
    }
    run {
        val f = Fixture.empty(); val journal = f.journal()
        expect(journal.read() == VersionedJournalResult(false, "", 0), "new empty item has known empty history")
        expect(journal.compareAndSet("0", false, "", true, readyPayload) == 1L, "ordinary create initializes v3 known-history envelope")
        expect(journal.claimFirstSend("1", readyPayload, operationKey, flightPayload) == 2L, "first claim commits")
        val afterRestart = f.journal(); expect(afterRestart.read() == VersionedJournalResult(true, flightPayload, 2), "restart reads committed v3")
        expectVaultFailureCode("invalid_state", "ordinary CAS cannot restore old logical payload") {
            afterRestart.compareAndSet("2", true, flightPayload, true, readyPayload)
        }
        expectVaultFailureCode("invalid_state", "ordinary CAS cannot delete an active request") {
            afterRestart.compareAndSet("2", true, flightPayload, false, "")
        }
        expect(afterRestart.read() == VersionedJournalResult(true, flightPayload, 2), "refused mutations preserve restart state")
    }
    run {
        val identity = FirstDispatchNativeParser.derive(readyPayload, flightPayload, operationKey)
        for ((label, savedMarker) in listOf("key" to identity.keyMarker, "body" to identity.bodyMarker)) {
            val f = Fixture.empty(); val aes = f.keys.createHardware()
            f.current.bytes = v3Envelope(1, readyPayload, listOf(savedMarker), emptyMap(), aes)
            val before = f.current.read()!!
            expect(claim(f.journal(), "1") == result(false, "conflict"), "$label history independently rejects old request")
            expect(f.current.read()!!.contentEquals(before), "$label history rejection preserves ciphertext")
        }
    }
    run {
        val f = Fixture.empty()
        val keys = f.keys; val aes = keys.createHardware()
        f.current.bytes = legacyV2Envelope(1, true, readyPayload.toByteArray(Charsets.UTF_8), aes)
        expect(f.journal().read() == VersionedJournalResult(true, readyPayload, 1), "old v2 is readable")
        expectVaultFailure("old v2 history unknown") { f.journal().claimFirstSend("1", readyPayload, operationKey, flightPayload) }
        expect(f.journal().compareAndSet("1", true, readyPayload, true, readyPayload) == 2L, "ordinary write migrates unknown history")
        expectVaultFailure("v2 migration remains fail closed after restart") { f.journal().claimFirstSend("2", readyPayload, operationKey, flightPayload) }
    }
    run {
        val f = Fixture.empty(); f.journal().compareAndSet("0", false, "", true, readyPayload)
        f.current.throwAfterWrite = true
        expectVaultFailure("post-write exception yields no claim success") { f.journal().claimFirstSend("1", readyPayload, operationKey, flightPayload) }
        val recovered = f.journal(); expect(recovered.read() == VersionedJournalResult(true, flightPayload, 2), "uncertain write is readable after restart")
        expectVaultFailureCode("invalid_state", "uncertain result cannot be rewound") {
            recovered.compareAndSet("2", true, flightPayload, true, readyPayload)
        }
        expect(recovered.read() == VersionedJournalResult(true, flightPayload, 2), "uncertain result keeps the exact request")
    }
    run {
        val f = Fixture.empty(); f.journal().compareAndSet("0", false, "", true, readyPayload)
        val first = f.journal(); val second = f.journal(); val gate = CountDownLatch(1); val done = CountDownLatch(2); val winners = AtomicInteger()
        val pool = Executors.newFixedThreadPool(2)
        listOf(first, second).forEach { journal -> pool.execute {
            gate.await(); try { journal.claimFirstSend("1", readyPayload, operationKey, flightPayload); winners.incrementAndGet() }
            catch (_: VaultFailure) { }
            finally { done.countDown() }
        } }
        gate.countDown(); done.await(); pool.shutdown(); expect(winners.get() == 1, "same-process journal instances serialize one winner")
    }
    run {
        val f = Fixture.empty(); val aes = f.keys.createHardware()
        val fullMarkers = (0 until 8192).map(::marker)
        f.current.bytes = v3Envelope(1, readyPayload, fullMarkers, emptyMap(), aes)
        val before = f.current.read()!!
        expect(f.journal().read() == VersionedJournalResult(true, readyPayload, 1), "max-capacity v3 envelope remains readable")
        expectVaultFailureCode("unavailable", "max marker capacity refuses another first claim") {
            f.journal().claimFirstSend("1", readyPayload, operationKey, flightPayload)
        }
        expect(f.current.read()!!.contentEquals(before), "capacity rejection leaves original ciphertext unchanged")
        expect(f.journal().compareAndSet("1", true, readyPayload, true, readyPayload) == 2L, "ordinary write succeeds at full history capacity")
        val afterOrdinaryWrite = f.current.read()!!
        expectVaultFailure("ordinary write retained full marker history") {
            f.journal().claimFirstSend("2", readyPayload, operationKey, flightPayload)
        }
        expect(f.current.read()!!.contentEquals(afterOrdinaryWrite), "retained history rejects without another envelope write")

        val activeOnly = Fixture.empty(); val activeKey = activeOnly.keys.createHardware()
        val activeCapacity = (0 until 64).associate { marker(30_000 + it) to marker(40_000 + it) }
        activeOnly.current.bytes = v3Envelope(1, readyPayload, emptyList(), activeCapacity, activeKey)
        expect(activeOnly.journal().read() == VersionedJournalResult(true, readyPayload, 1), "max active-binding envelope remains readable")
        expectVaultFailureCode("unavailable", "full active-binding capacity refuses first claim") {
            activeOnly.journal().claimFirstSend("1", readyPayload, operationKey, flightPayload)
        }
    }
    run {
        val marker = marker(99)
        val malformedStates = listOf(
            v3Plaintext(readyPayload, listOf(marker, marker), emptyMap()), // duplicate permanent marker
            v3Plaintext(readyPayload, emptyList(), emptyMap(), tail = byteArrayOf(0x01)) // extra plaintext data
        )
        malformedStates.forEachIndexed { index, plaintext ->
            val f = Fixture.empty(); val aes = f.keys.createHardware(); f.current.bytes = damagedV3Envelope(1, plaintext, aes)
            expectVaultFailure("malformed authenticated v3 structure $index rejected") { f.journal().read() }
        }
        for ((label, damage) in listOf(
            "truncated tag" to { bytes: ByteArray -> bytes.copyOf(bytes.size - 1) },
            "extra ciphertext byte" to { bytes: ByteArray -> bytes + byteArrayOf(0x01) }
        )) {
            val f = Fixture.empty(); val aes = f.keys.createHardware()
            val valid = v3Envelope(1, readyPayload, emptyList(), emptyMap(), aes)
            f.current.bytes = damage(valid)
            expectVaultFailure("$label rejected") { f.journal().read() }
        }
    }
    run {
        val f = Fixture.empty()
        val body = "{\"expectedVersion\":1.0000000000000001,\"exerciseEntryKeys\":[]}"
        fun setsOperation(state: String, revision: Int, attempt: String? = null) =
            registry(operation(state, revision, attempt, body).replace("\"path\":\"/me/sessions\"", "\"path\":\"/me/sessions/55/sets\""))
        val ready = setsOperation("ready", 1)
        val flight = setsOperation("in_flight", 2, "auth-gen-a")
        f.journal().compareAndSet("0", false, "", true, ready)
        val before = f.current.read()!!
        expectVaultFailureCode("invalid_state", "fractional expectedVersion cannot claim a send") {
            f.journal().claimFirstSend("1", ready, operationKey, flight)
        }
        expect(f.current.read()!!.contentEquals(before), "invalid decimal leaves journal ciphertext and history unchanged")
        expect(f.journal().read() == VersionedJournalResult(true, ready, 1), "invalid decimal leaves ready record intact")
    }
    println("PASS production-journal, active-CAS and bridge synthetic scenarios")
}
