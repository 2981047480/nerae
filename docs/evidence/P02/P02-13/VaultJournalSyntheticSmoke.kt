package uts.sdk.modules.neraeSecureVault

import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey

private class JournalMemoryStorage : VaultStorage {
    @Volatile var bytes: ByteArray? = null
    @Volatile var failWrite = false
    @Volatile var applyThenFail = false
    override fun read(): ByteArray? = bytes?.copyOf()
    override fun writeAtomically(value: ByteArray) {
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
    val authKeys = JournalSoftwareKeys()
    val identityKeys = JournalSoftwareKeys()
    val journalKeys = JournalSoftwareKeys()
    val journalLock = Any()
    val auth = NeraeCredentialVault(authStorage, authKeys, Any(), JournalJsonValidator)
    val identity = NeraeCredentialVault(identityStorage, identityKeys, Any(), JournalJsonValidator)
    val journal = NeraeCredentialVault(journalStorage, journalKeys, journalLock, JournalJsonValidator)

    auth.write("{\"session\":\"synthetic\"}")
    identity.write("{\"device\":\"synthetic\"}")
    journal.compareAndSet(false, "", "{\"ops\":[]}")
    check(journal.read().value == "{\"ops\":[]}")
    auth.clear()
    check(!auth.read().found)
    check(identity.read().value == "{\"device\":\"synthetic\"}")
    check(journal.read().value == "{\"ops\":[]}")

    val exactLimit = "{\"x\":\"" + "a".repeat(65_528) + "\"}"
    check(exactLimit.toByteArray(Charsets.UTF_8).size == NeraeCredentialVault.MAX_PLAINTEXT_BYTES)
    journal.compareAndSet(true, "{\"ops\":[]}", exactLimit)
    check(journal.read().value == exactLimit)
    journal.compareAndSet(true, exactLimit, "{\"ops\":[]}")

    expectFailure("conflict") { journal.compareAndSet(true, "{\"ops\":[]} ", "{\"ops\":[1]}") }
    check(journal.read().value == "{\"ops\":[]}")
    expectFailure("invalid_state") { journal.compareAndSet(true, "{\"ops\":[]}", "[]") }
    expectFailure("invalid_state") { journal.compareAndSet(true, "{\"ops\":[]}", "{\"x\":1} trailing") }
    val oversized = "{\"x\":\"" + "a".repeat(65_529) + "\"}"
    expectFailure("invalid_state") { journal.compareAndSet(true, "{\"ops\":[]}", oversized) }
    check(journal.read().value == "{\"ops\":[]}")

    val ready = CountDownLatch(2)
    val start = CountDownLatch(1)
    val stored = AtomicInteger()
    val conflicted = AtomicInteger()
    val writers = listOf("{\"ops\":[1]}", "{\"ops\":[2]}").map { replacement ->
        Thread {
            ready.countDown(); start.await()
            try {
                journal.compareAndSet(true, "{\"ops\":[]}", replacement)
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

    journalStorage.applyThenFail = true
    expectFailure { journal.compareAndSet(true, prior, "{\"ops\":[3]}") }
    journalStorage.applyThenFail = false
    check(journal.read().value == "{\"ops\":[3]}")
    journalStorage.failWrite = true
    expectFailure { journal.compareAndSet(true, "{\"ops\":[3]}", "{\"ops\":[4]}") }
    journalStorage.failWrite = false
    check(journal.read().value == "{\"ops\":[3]}")
    println("PASS: synthetic three-slot isolation, bounded journal CAS, conflict, concurrency and uncertain-write preservation; software AES only")
}
