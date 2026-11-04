package uts.sdk.modules.neraeSecureVault

import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import java.nio.ByteBuffer
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey

private class JournalMemoryStorage : VaultStorage {
    @Volatile var bytes: ByteArray? = null
    @Volatile var failWrite = false
    @Volatile var applyThenFail = false
  @Volatile var writes = 0
    override fun read(): ByteArray? = bytes?.copyOf()
    override fun writeAtomically(value: ByteArray) {
    writes++
        if (applyThenFail) { bytes = value.copyOf(); throw VaultFailure() }
        if (failWrite) throw VaultFailure()
        bytes = value.copyOf()
    }
    override fun clear() { bytes = null }
}

private class JournalSoftwareKeys : VaultKeys {
    @Volatile var key: SecretKey? = null
    override fun existing(): SecretKey? = key
    override fun createHardware(): SecretKey {
        check(key == null)
        return KeyGenerator.getInstance("AES").apply { init(256) }.generateKey().also { key = it }
    }
    override fun delete() { key = null }
}

/** JVM synthetic validator; Android JsonReader is a stub in the compile-only android.jar. */
private object JournalJsonValidator : VaultPayloadValidator {
    override fun isJsonObject(value: String): Boolean = try {
        val parser = StrictJsonParser(value)
        parser.objectDocument()
    } catch (_: Exception) { false }
}

private class StrictJsonParser(private val source: String) {
    private var index = 0
    fun objectDocument(): Boolean {
        whitespace(); if (peek() != '{') return false
        parseObject(0); whitespace(); return index == source.length
    }
    private fun parseValue(depth: Int) {
        require(depth <= 64); whitespace()
        when (peek()) {
            '{' -> parseObject(depth + 1)
            '[' -> parseArray(depth + 1)
            '"' -> parseString()
            't' -> literal("true")
            'f' -> literal("false")
            'n' -> literal("null")
            '-', in '0'..'9' -> parseNumber()
            else -> error("invalid JSON")
        }
    }
    private fun parseObject(depth: Int) {
        require(take() == '{'); whitespace()
        if (peek() == '}') { index++; return }
        while (true) {
            whitespace(); parseString(); whitespace(); require(take() == ':'); parseValue(depth); whitespace()
            when (take()) { '}' -> return; ',' -> Unit; else -> error("invalid JSON") }
        }
    }
    private fun parseArray(depth: Int) {
        require(take() == '['); whitespace()
        if (peek() == ']') { index++; return }
        while (true) {
            parseValue(depth); whitespace()
            when (take()) { ']' -> return; ',' -> Unit; else -> error("invalid JSON") }
        }
    }
    private fun parseString() {
        require(take() == '"')
        while (true) {
            val char = take()
            when {
                char == '"' -> return
                char == '\\' -> when (take()) {
                    '"', '\\', '/', 'b', 'f', 'n', 'r', 't' -> Unit
                    'u' -> repeat(4) { require(take().digitToIntOrNull(16) != null) }
                    else -> error("invalid escape")
                }
                char.code < 0x20 -> error("invalid JSON string")
            }
        }
    }
    private fun parseNumber() {
        val start = index
        if (peek() == '-') index++
        if (peek() == '0') index++ else { require(peek() in '1'..'9'); while (peek() in '0'..'9') index++ }
        if (peek() == '.') { index++; require(peek() in '0'..'9'); while (peek() in '0'..'9') index++ }
        if (peek() == 'e' || peek() == 'E') {
            index++; if (peek() == '+' || peek() == '-') index++
            require(peek() in '0'..'9'); while (peek() in '0'..'9') index++
        }
        require(index > start)
    }
    private fun literal(value: String) { require(source.startsWith(value, index)); index += value.length }
    private fun whitespace() { while (peek() == ' ' || peek() == '\n' || peek() == '\r' || peek() == '\t') index++ }
    private fun peek(): Char = source.getOrElse(index) { '\u0000' }
    private fun take(): Char = source.getOrNull(index++) ?: error("unexpected end")
}

private fun expectFailure(code: String? = null, block: () -> Unit) {
    try { block(); error("expected vault failure") }
    catch (failure: VaultFailure) { if (code != null) check(failure.code == code) }
}

fun main() {
    check(NeraeVaultSlot.entries.map { it.fileName }.toSet().size == 3)
    check(NeraeVaultSlot.entries.map { it.keyAlias }.toSet().size == 3)

    val authStorage = JournalMemoryStorage()
    val identityStorage = JournalMemoryStorage()
    val journalStorage = JournalMemoryStorage()
    val journalVersionedStorage = JournalMemoryStorage()
    val authKeys = JournalSoftwareKeys()
    val identityKeys = JournalSoftwareKeys()
    val journalKeys = JournalSoftwareKeys()
    val journalLock = Any()
    val auth = NeraeCredentialVault(authStorage, authKeys, Any(), JournalJsonValidator)
    val identity = NeraeCredentialVault(identityStorage, identityKeys, Any(), JournalJsonValidator)
    val journal = NeraeVersionedTrainingJournal(journalStorage, journalVersionedStorage, journalKeys, journalLock, JournalJsonValidator)

    auth.write("{\"session\":\"synthetic\"}")
    identity.write("{\"device\":\"synthetic\"}")
    check(journal.read().let { !it.found && it.value.isEmpty() && it.revision == 0L })
    val initial = "{\"ops\":[]}"
    val alternative = "{\"ops\":[1]}"
    check(journal.compareAndSet("0", false, "", true, initial) == 1L)
    check(journal.compareAndSet("1", true, initial, true, alternative) == 2L)
    check(journal.compareAndSet("2", true, alternative, true, initial) == 3L)
    check(journal.read().let { it.found && it.value == initial && it.revision == 3L })
    check(journal.compareAndSet("3", true, initial, true, initial) == 4L)
    check(journal.compareAndSet("4", true, initial, false, "") == 5L)
    check(journal.read().let { !it.found && it.value.isEmpty() && it.revision == 5L })
    check(journal.compareAndSet("5", false, "", true, initial) == 6L)
    expectFailure("conflict") { journal.compareAndSet("5", true, initial, true, alternative) }
    check(journal.read().let { it.found && it.value == initial && it.revision == 6L })
    auth.clear()
    check(!auth.read().found)
    check(identity.read().value == "{\"device\":\"synthetic\"}")
    check(journal.read().value == initial)

    val exactLimit = "{\"x\":\"" + "a".repeat(65_528) + "\"}"
    check(exactLimit.toByteArray(Charsets.UTF_8).size == NeraeCredentialVault.MAX_PLAINTEXT_BYTES)
    check(journal.compareAndSet("6", true, initial, true, exactLimit) == 7L)
    val oversized = "{\"x\":\"" + "a".repeat(65_529) + "\"}"
    expectFailure("invalid_state") { journal.compareAndSet("7", true, exactLimit, true, oversized) }
    expectFailure("invalid_state") { journal.compareAndSet("7", true, exactLimit, true, "[]") }
    expectFailure("invalid_state") { journal.compareAndSet("07", true, exactLimit, true, initial) }
    check(journal.read().let { it.value == exactLimit && it.revision == 7L })
    check(journal.readLegacy().let { it.found && it.value == exactLimit && it.revision == 7L })
    check(journal.compareAndSetLegacy(true, exactLimit, exactLimit) == 8L)

    val ready = CountDownLatch(2)
    val start = CountDownLatch(1)
    val stored = AtomicInteger()
    val conflicted = AtomicInteger()
    val expectedRevision = journal.read().revision.toString()
    val writers = listOf("{\"ops\":[1]}", "{\"ops\":[2]}").map { replacement ->
        Thread {
            ready.countDown(); start.await()
            try {
                val independentBridgeCall = NeraeVersionedTrainingJournal(
                    journalStorage, journalVersionedStorage, journalKeys, journalLock, JournalJsonValidator
                )
                independentBridgeCall.compareAndSet(expectedRevision, true, exactLimit, true, replacement)
                stored.incrementAndGet()
            } catch (failure: VaultFailure) {
                if (failure.code == "conflict") conflicted.incrementAndGet() else throw failure
            }
        }.apply { start() }
    }
    ready.await(); start.countDown(); writers.forEach { it.join() }
    check(stored.get() == 1 && conflicted.get() == 1)
    val prior = journal.read().value
    check(prior == "{\"ops\":[1]}" || prior == "{\"ops\":[2]}")

    val priorRevision = journal.read().revision
    journalVersionedStorage.applyThenFail = true
    expectFailure { journal.compareAndSet(priorRevision.toString(), true, prior, true, "{\"ops\":[3]}") }
    journalVersionedStorage.applyThenFail = false
    check(journal.read().let { it.value == "{\"ops\":[3]}" && it.revision == priorRevision + 1 })
    val stableRevision = journal.read().revision
    journalVersionedStorage.failWrite = true
    expectFailure { journal.compareAndSet(stableRevision.toString(), true, "{\"ops\":[3]}", true, "{\"ops\":[4]}") }
    journalVersionedStorage.failWrite = false
    check(journal.read().let { it.value == "{\"ops\":[3]}" && it.revision == stableRevision })

    val legacyStorage = JournalMemoryStorage()
    val legacyKeys = JournalSoftwareKeys()
    val legacyLock = Any()
    val oldApi = NeraeCredentialVault(legacyStorage, legacyKeys, legacyLock, JournalJsonValidator)
    val legacyValue = "{\"legacy\":\"preserved\"}"
    oldApi.write(legacyValue)
    val v1Bytes = legacyStorage.read()!!
    val legacyV2Storage = JournalMemoryStorage()
    val migrated = NeraeVersionedTrainingJournal(legacyStorage, legacyV2Storage, legacyKeys, legacyLock, JournalJsonValidator)
    legacyV2Storage.failWrite = true
    expectFailure { migrated.read() }
    check(legacyStorage.read()!!.contentEquals(v1Bytes))
    check(legacyV2Storage.read() == null)
    legacyV2Storage.failWrite = false
    legacyV2Storage.applyThenFail = true
    expectFailure { migrated.read() }
    legacyV2Storage.applyThenFail = false
    check(legacyStorage.read()!!.contentEquals(v1Bytes))
    check(legacyV2Storage.read() != null)
    check(migrated.read().let { it.revision == 1L && it.found && it.value == legacyValue })
    // A successful legacy migration now writes the v3 history envelope.
    check(legacyV2Storage.read()!![4] == 3.toByte())
    check(legacyStorage.read()!!.contentEquals(v1Bytes))
    check(migrated.compareAndSetLegacy(true, legacyValue, legacyValue) == 2L)
    check(migrated.read().let { it.revision == 2L && it.value == legacyValue })
    check(legacyStorage.read()!!.contentEquals(v1Bytes))

    val committedEnvelope = legacyV2Storage.read()!!
    val tamperedHeader = committedEnvelope.copyOf()
    tamperedHeader[12] = (tamperedHeader[12].toInt() xor 1).toByte()
    legacyV2Storage.bytes = tamperedHeader
    expectFailure { migrated.read() }
    legacyV2Storage.bytes = committedEnvelope

    val v1Fingerprint = MessageDigest.getInstance("SHA-256").digest(v1Bytes)
    val maxHeader = ByteBuffer.allocate(47).put(byteArrayOf(0x4e, 0x52, 0x56, 0x41)).put(2.toByte())
        .putLong(Long.MAX_VALUE).put(1.toByte()).put(1.toByte()).put(v1Fingerprint).array()
    val maxCipher = Cipher.getInstance("AES/GCM/NoPadding")
    maxCipher.init(Cipher.ENCRYPT_MODE, legacyKeys.key!!)
    maxCipher.updateAAD(maxHeader)
    legacyV2Storage.bytes = maxHeader + maxCipher.iv + maxCipher.doFinal(legacyValue.toByteArray(Charsets.UTF_8))
    check(migrated.read().revision == Long.MAX_VALUE)
    expectFailure("invalid_state") { migrated.compareAndSet(Long.MAX_VALUE.toString(), true, legacyValue, false, "") }
    check(migrated.read().revision == Long.MAX_VALUE)

    val changedLegacyStorage = JournalMemoryStorage()
    val changedV2Storage = JournalMemoryStorage()
    val changedKeys = JournalSoftwareKeys()
    val changedLock = Any()
    val oldWriter = NeraeCredentialVault(changedLegacyStorage, changedKeys, changedLock, JournalJsonValidator)
    oldWriter.write(legacyValue)
    val changedV1 = changedLegacyStorage.read()!!
    val changedJournal = NeraeVersionedTrainingJournal(
        changedLegacyStorage, changedV2Storage, changedKeys, changedLock, JournalJsonValidator
    )
    check(changedJournal.read().revision == 1L)
    val committedV2BeforeOldWrite = changedV2Storage.read()!!
    oldWriter.write("{\"legacy\":\"changed by old version\"}")
    expectFailure { changedJournal.read() }
    check(changedLegacyStorage.read()!!.contentEquals(changedV1).not())
    check(changedV2Storage.read()!!.contentEquals(committedV2BeforeOldWrite))

    val initiallyAbsentLegacy = JournalMemoryStorage()
    val initiallyAbsentV2 = JournalMemoryStorage()
    val activeKeys = JournalSoftwareKeys()
    val activeJournal = NeraeVersionedTrainingJournal(
        initiallyAbsentLegacy, initiallyAbsentV2, activeKeys, Any(), JournalJsonValidator
    )
    check(activeJournal.compareAndSet("0", false, "", true, initial) == 1L)
    val activeEnvelope = initiallyAbsentV2.read()!!
    val oldProcessKeys = JournalSoftwareKeys()
    NeraeCredentialVault(initiallyAbsentLegacy, oldProcessKeys, Any(), JournalJsonValidator).write(legacyValue)
    expectFailure { activeJournal.read() }
    check(initiallyAbsentV2.read()!!.contentEquals(activeEnvelope))

    println("PASS: revisioned AES-GCM journal, A-B-A, tombstone/rebuild, conflict, concurrency, unknown writes, v1 migration and overflow; software AES only")
}
