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
    run {
        val f = Fixture.empty(); val journal = f.journal()
        expect(journal.read() == VersionedJournalResult(false, "", 0), "new empty item has known empty history")
        expect(journal.compareAndSet("0", false, "", true, readyPayload) == 1L, "ordinary create initializes v3 known-history envelope")
        expect(journal.claimFirstSend("1", readyPayload, operationKey, flightPayload) == 2L, "first claim commits")
        val afterRestart = f.journal(); expect(afterRestart.read() == VersionedJournalResult(true, flightPayload, 2), "restart reads committed v3")
        expect(afterRestart.compareAndSet("2", true, flightPayload, true, readyPayload) == 3L, "ordinary CAS can restore old logical payload")
        expectVaultFailure("old value rollback cannot erase history") {
            f.journal().claimFirstSend("3", readyPayload, operationKey, flightPayload)
        }
        expect(afterRestart.compareAndSet("3", true, readyPayload, false, "") == 4L, "logical delete preserves metadata")
        expect(afterRestart.compareAndSet("4", false, "", true, readyPayload) == 5L, "logical recreate preserves metadata")
        expectVaultFailure("delete recreate cannot claim old request") { f.journal().claimFirstSend("5", readyPayload, operationKey, flightPayload) }
        expect(afterRestart.compareAndSetLegacy(true, readyPayload, flightPayload) == 6L, "legacy writer advances without clearing metadata")
        expectVaultFailure("legacy CAS cannot erase marker") { f.journal().claimFirstSend("6", readyPayload, operationKey, flightPayload) }
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
        expect(recovered.compareAndSet("2", true, flightPayload, true, readyPayload) == 3L, "restore old logical bytes after uncertain result")
        expectVaultFailure("uncertain committed marker prevents second claim") { f.journal().claimFirstSend("3", readyPayload, operationKey, flightPayload) }
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
        val fullActive = (0 until 64).associate { marker(10_000 + it) to marker(20_000 + it) }
        f.current.bytes = v3Envelope(1, readyPayload, fullMarkers, fullActive, aes)
        val before = f.current.read()!!
        expect(f.journal().read() == VersionedJournalResult(true, readyPayload, 1), "max-capacity v3 envelope remains readable")
        expectVaultFailureCode("unavailable", "max marker and active capacities refuse another first claim") {
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
    println("PASS 7 production-journal synthetic scenarios")
}
