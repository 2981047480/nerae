package uts.sdk.modules.neraeSecureVault

import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors

private class MemorySlot : VaultStorage {
    var value: ByteArray? = null
    var failWrite = false
    var failRead = false
    override fun read(): ByteArray? {
        if (failRead) throw VaultFailure()
        return value?.copyOf()
    }
    override fun writeAtomically(value: ByteArray) {
        if (failWrite) throw VaultFailure()
        this.value = value.copyOf()
    }
    override fun clear() { value = null }
}
private class SyntheticKeys : VaultKeys {
    var key: SecretKey? = null
    override fun existing(): SecretKey? = key
    override fun createHardware(): SecretKey {
        if (key != null) throw VaultFailure()
        return KeyGenerator.getInstance("AES").apply { init(256) }.generateKey().also { key = it }
    }
    override fun delete() { key = null }
}
private object JsonShape : VaultPayloadValidator {
    override fun isJsonObject(value: String): Boolean = value.trim().startsWith("{") && value.trim().endsWith("}")
}
private fun expectVaultFailure(action: () -> Unit) {
    try { action(); error("expected failure") } catch (_: VaultFailure) { }
}
private fun expectVaultCode(code: String, action: () -> Unit) {
    try { action(); error("expected $code") } catch (failure: VaultFailure) {
        check(failure.code == code)
    }
}

fun main() {
    val authSlot = NeraeVaultSlot.AUTHENTICATION_STATE
    val registrySlot = NeraeVaultSlot.DEVICE_IDENTITY_REGISTRY
    check(authSlot.fileName == "nerae-auth-state.v1.enc")
    check(authSlot.keyAlias == "com.nerae.secure-vault.authentication-state.aes-gcm.v1")
    check(registrySlot.fileName != authSlot.fileName && registrySlot.keyAlias != authSlot.keyAlias)
    check(!registrySlot.keyAlias.startsWith("com.nerae.device-key.v1/"))

    val initVault = NeraeCredentialVault(MemorySlot(), SyntheticKeys(), Any(), JsonShape)
    val initGate = CountDownLatch(1)
    val initPool = Executors.newFixedThreadPool(2)
    val initCandidates = listOf("{\"device\":\"init-a\"}", "{\"device\":\"init-b\"}")
    val initFutures = initCandidates.map { candidate ->
        initPool.submit<String> {
            initGate.await()
            try {
                initVault.compareAndSet(expectedFound = false, expectedValue = "", value = candidate)
                "success"
            } catch (failure: VaultFailure) {
                if (failure.code == "conflict") "conflict" else "failure"
            }
        }
    }
    initGate.countDown()
    check(initFutures.map { it.get() }.sorted() == listOf("conflict", "success"))
    initPool.shutdownNow()
    check(initCandidates.contains(initVault.read().value))

    val failedInitStorage = MemorySlot().apply { failWrite = true }
    val failedInitKeys = SyntheticKeys()
    val failedInitVault = NeraeCredentialVault(failedInitStorage, failedInitKeys, Any(), JsonShape)
    expectVaultFailure {
        failedInitVault.compareAndSet(expectedFound = false, expectedValue = "", value = "{\"device\":\"retry\"}")
    }
    check(failedInitStorage.value == null && failedInitKeys.key == null)
    failedInitStorage.failWrite = false
    failedInitVault.compareAndSet(expectedFound = false, expectedValue = "", value = "{\"device\":\"retry\"}")
    check(failedInitVault.read().value == "{\"device\":\"retry\"}")

    val authStorage = MemorySlot()
    val registryStorage = MemorySlot()
    val authKeys = SyntheticKeys()
    val registryKeys = SyntheticKeys()
    val auth = NeraeCredentialVault(authStorage, authKeys, Any(), JsonShape)
    val registry = NeraeCredentialVault(registryStorage, registryKeys, Any(), JsonShape)
    val authValue = "{\"session\":\"synthetic-auth-only\"}"
    val registryValue = "{\"device\":\"synthetic-registry-only\"}"
    auth.write(authValue)
    registry.compareAndSet(expectedFound = false, expectedValue = "", value = registryValue)
    check(auth.read().value == authValue && registry.read().value == registryValue)

    // Initializing an already populated registry must conflict without data.
    expectVaultCode("conflict") {
        registry.compareAndSet(expectedFound = false, expectedValue = "", value = "{\"device\":\"losing-init\"}")
    }
    check(registry.read().value == registryValue)

    // Two same-process writers race from the same snapshot. The process lock
    // makes exactly one succeed; the other sees a content-free conflict.
    val gate = CountDownLatch(1)
    val pool = Executors.newFixedThreadPool(2)
    val candidates = listOf("{\"device\":\"writer-a\"}", "{\"device\":\"writer-b\"}")
    val futures = candidates.map { candidate ->
        pool.submit<String> {
            gate.await()
            try {
                registry.compareAndSet(expectedFound = true, expectedValue = registryValue, value = candidate)
                "success"
            } catch (failure: VaultFailure) {
                if (failure.code == "conflict") "conflict" else "failure"
            }
        }
    }
    gate.countDown()
    val outcomes = futures.map { it.get() }.sorted()
    pool.shutdownNow()
    check(outcomes == listOf("conflict", "success"))
    val winningValue = registry.read().value
    check(candidates.contains(winningValue))

    auth.clear()
    check(!auth.read().found)
    check(registry.read().value == winningValue)

    val saved = registryStorage.value!!.copyOf()
    expectVaultFailure { registry.write("[]") }
    expectVaultFailure { registry.write("{\"x\":\"" + "x".repeat(65_536) + "\"}") }
    registryStorage.failWrite = true
    expectVaultFailure {
        registry.compareAndSet(expectedFound = true, expectedValue = winningValue, value = "{\"device\":\"replacement\"}")
    }
    registryStorage.failWrite = false
    check(registryStorage.value!!.contentEquals(saved) && registry.read().value == winningValue)
    registryStorage.failRead = true
    expectVaultFailure { registry.read() }
    registryStorage.failRead = false
    check(registry.read().value == winningValue)
    println("PASS: synthetic independent slots, conditional initialization, competing writers, logout isolation and failure preservation; process-local lock only, software AES, no Android Keystore access")
}
