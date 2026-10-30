package uts.sdk.modules.neraeSecureVault

import javax.crypto.KeyGenerator
import javax.crypto.SecretKey

private class MemoryVaultStorage : VaultStorage {
    var bytes: ByteArray? = null
    var failRead = false
    var failWrite = false
    var failClear = false

    override fun read(): ByteArray? {
        if (failRead) throw VaultFailure()
        return bytes?.copyOf()
    }

    override fun writeAtomically(value: ByteArray) {
        if (failWrite) throw VaultFailure()
        bytes = value.copyOf()
    }

    override fun clear() {
        if (failClear) throw VaultFailure()
        bytes = null
    }
}

private class SoftwareSyntheticKeys : VaultKeys {
    var key: SecretKey? = null
    var createCount = 0
    var deleteCount = 0
    var unavailable = false

    override fun existing(): SecretKey? {
        if (unavailable) throw VaultFailure()
        return key
    }

    override fun createHardware(): SecretKey {
        if (unavailable || key != null) throw VaultFailure()
        val generator = KeyGenerator.getInstance("AES")
        generator.init(256)
        return generator.generateKey().also {
            key = it
            createCount++
        }
    }

    override fun delete() {
        if (unavailable) throw VaultFailure()
        if (key != null) deleteCount++
        key = null
    }
}

private object SyntheticJsonObjectValidator : VaultPayloadValidator {
	override fun isJsonObject(value: String): Boolean = value.trim().let { it.startsWith("{") && it.endsWith("}") }
}

private fun expectFailure(code: String? = null, action: () -> Unit) {
    try {
        action()
        error("expected vault failure")
    } catch (failure: VaultFailure) {
        if (code != null && failure.code != code) error("unexpected failure category")
    }
}

private fun expectDepthFailure(action: () -> Unit) {
    try {
        action()
        error("expected JSON depth failure")
    } catch (_: IllegalArgumentException) {
        // Expected at the explicit parser recursion boundary.
    }
}

fun main() {
    ensureJsonDepth(0)
    ensureJsonDepth(64)
    expectDepthFailure { ensureJsonDepth(65) }

    val storage = MemoryVaultStorage()
    val keys = SoftwareSyntheticKeys()
    val vault = NeraeCredentialVault(storage, keys, Any(), SyntheticJsonObjectValidator)
    check(!vault.read().found && keys.createCount == 0)

    val secret = "{\"accessToken\":\"synthetic-token-never-log\",\"refreshToken\":\"synthetic-refresh\"}"
    expectFailure("invalid_state") { vault.write("[]") }
    expectFailure("invalid_state") { vault.write("{\"token\":\"" + "x".repeat(65_536) + "\"}") }
    expectFailure("invalid_state") { vault.write("{\"bad\":\"\uD800\"}") }
    check(keys.createCount == 0 && storage.bytes == null)

    vault.write(secret)
    val firstEnvelope = storage.bytes!!.copyOf()
    check(keys.createCount == 1)
    check(!firstEnvelope.contentEquals(secret.toByteArray(Charsets.UTF_8)))
    check(!firstEnvelope.toString(Charsets.ISO_8859_1).contains("synthetic-token"))
    check(vault.read().found && vault.read().value == secret)
    check(!vault.read().toString().contains("synthetic-token"))

    // A write failure leaves the synthetic store's original value intact.
    storage.failWrite = true
    expectFailure { vault.write("{\"accessToken\":\"replacement\"}") }
    storage.failWrite = false
    check(storage.bytes!!.contentEquals(firstEnvelope) && vault.read().value == secret)

    // An orphan alias after a crash is never silently adopted by first write.
    val orphanStorage = MemoryVaultStorage()
    val orphanKeys = SoftwareSyntheticKeys().apply { key = keys.key }
    val orphanVault = NeraeCredentialVault(orphanStorage, orphanKeys, Any(), SyntheticJsonObjectValidator)
    expectFailure { orphanVault.read() }
    check(orphanStorage.bytes == null && orphanKeys.key != null && orphanKeys.deleteCount == 0)
    expectFailure { orphanVault.write("{\"accessToken\":\"replacement\"}") }
    check(orphanStorage.bytes == null && orphanKeys.createCount == 0)
    orphanStorage.failClear = true
    expectFailure { orphanVault.clear() }
    check(orphanStorage.bytes == null && orphanKeys.key != null && orphanKeys.deleteCount == 0)

    // Missing key, bad tag, truncated envelope and storage/key errors fail closed.
    val missingKeyVault = NeraeCredentialVault(storage, SoftwareSyntheticKeys(), Any(), SyntheticJsonObjectValidator)
    expectFailure { missingKeyVault.read() }
    val saved = storage.bytes!!.copyOf()
    storage.bytes!![storage.bytes!!.lastIndex] = (storage.bytes!!.last().toInt() xor 1).toByte()
    expectFailure { vault.read() }
    expectFailure { vault.write("{\"accessToken\":\"replacement\"}") }
    check(storage.bytes!!.size == firstEnvelope.size)
    storage.bytes = saved.copyOfRange(0, 7)
    expectFailure { vault.read() }
    storage.bytes = saved
    storage.failRead = true
    expectFailure { vault.read() }
    storage.failRead = false
    keys.unavailable = true
    expectFailure { vault.read() }
    keys.unavailable = false
    check(vault.read().value == secret)

    val corruptedJson = storage.bytes!!.copyOf()
    val key = keys.key!!
    val invalidPayload = "[]".toByteArray(Charsets.UTF_8)
    storage.bytes = encryptForTest(invalidPayload, key)
    expectFailure { vault.read() }
    check(storage.bytes!!.isNotEmpty())
    storage.bytes = corruptedJson

    vault.clear()
    check(storage.bytes == null && keys.key == null && keys.deleteCount == 1)
    check(!vault.read().found)
    vault.clear()
    vault.write(secret)
    check(keys.createCount == 2 && vault.read().value == secret)

    println("PASS: synthetic AES-GCM envelope, corruption, error classification and state replacement; software key only")
}

private fun encryptForTest(plaintext: ByteArray, key: SecretKey): ByteArray {
    val cipher = javax.crypto.Cipher.getInstance("AES/GCM/NoPadding")
    cipher.init(javax.crypto.Cipher.ENCRYPT_MODE, key)
    val aad = byteArrayOf(0x4e, 0x52, 0x56, 0x41, 1)
    cipher.updateAAD(aad)
    val encrypted = cipher.doFinal(plaintext)
    return aad + cipher.iv + encrypted
}
