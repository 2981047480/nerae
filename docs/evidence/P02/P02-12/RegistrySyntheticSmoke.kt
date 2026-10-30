package uts.sdk.modules.neraeSecureVault

import javax.crypto.KeyGenerator
import javax.crypto.SecretKey

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

fun main() {
    val authSlot = NeraeVaultSlot.AUTHENTICATION_STATE
    val registrySlot = NeraeVaultSlot.DEVICE_IDENTITY_REGISTRY
    check(authSlot.fileName == "nerae-auth-state.v1.enc")
    check(authSlot.keyAlias == "com.nerae.secure-vault.authentication-state.aes-gcm.v1")
    check(registrySlot.fileName != authSlot.fileName && registrySlot.keyAlias != authSlot.keyAlias)
    check(!registrySlot.keyAlias.startsWith("com.nerae.device-key.v1/"))

    val authStorage = MemorySlot()
    val registryStorage = MemorySlot()
    val authKeys = SyntheticKeys()
    val registryKeys = SyntheticKeys()
    val auth = NeraeCredentialVault(authStorage, authKeys, Any(), JsonShape)
    val registry = NeraeCredentialVault(registryStorage, registryKeys, Any(), JsonShape)
    val authValue = "{\"session\":\"synthetic-auth-only\"}"
    val registryValue = "{\"device\":\"synthetic-registry-only\"}"
    auth.write(authValue)
    registry.write(registryValue)
    check(auth.read().value == authValue && registry.read().value == registryValue)

    auth.clear()
    check(!auth.read().found)
    check(registry.read().value == registryValue)

    val saved = registryStorage.value!!.copyOf()
    expectVaultFailure { registry.write("[]") }
    expectVaultFailure { registry.write("{\"x\":\"" + "x".repeat(65_536) + "\"}") }
    registryStorage.failWrite = true
    expectVaultFailure { registry.write("{\"device\":\"replacement\"}") }
    registryStorage.failWrite = false
    check(registryStorage.value!!.contentEquals(saved) && registry.read().value == registryValue)
    registryStorage.failRead = true
    expectVaultFailure { registry.read() }
    registryStorage.failRead = false
    check(registry.read().value == registryValue)
    println("PASS: synthetic independent slots, logout isolation and failure preservation; software AES only, no Android Keystore access")
}
