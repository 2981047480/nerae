package uts.sdk.modules.neraeSecureVault

import java.nio.charset.StandardCharsets
import java.nio.ByteBuffer
import java.security.MessageDigest
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.Cipher

private class VersionedDraftTestBackend : TrainingDraftBackend {
    val files = linkedMapOf<String, ByteArray>()
    val keys = linkedMapOf<String, SecretKey>()
    var failBeforeWrite = false
    var failAfterWrite = false
    var failNextReadAfterWrite = false
    private var failNextRead = false

    override fun ensureAvailable() = Unit
    override fun fileEntries(): List<String> = files.keys.map { "nerae-training-draft.v1.$it.enc" }
    override fun aliases(): List<String> = keys.keys.map { "com.nerae.secure-vault.training-draft.aes-gcm.v1.$it" }
    override fun slot(digest: String): TrainingDraftVault {
        val storage = object : VaultStorage {
            override fun read(): ByteArray? {
                if (failNextRead) { failNextRead = false; throw VaultFailure() }
                return files[digest]?.copyOf()
            }
            override fun writeAtomically(value: ByteArray) {
                if (failBeforeWrite) throw VaultFailure()
                files[digest] = value.copyOf()
                if (failNextReadAfterWrite) { failNextReadAfterWrite = false; failNextRead = true }
                if (failAfterWrite) throw VaultFailure()
            }
            override fun clear() { files.remove(digest) }
        }
        val keyStore = object : VaultKeys {
            override fun existing(): SecretKey? = keys[digest]
            override fun createHardware(): SecretKey = KeyGenerator.getInstance("AES").apply { init(256) }
                .generateKey().also { keys[digest] = it }
            override fun delete() { keys.remove(digest) }
        }
        val validator = VaultPayloadValidator { value -> value.startsWith("{") && value.endsWith("}") }
        return TrainingDraftVault(storage, keyStore, validator)
    }
    fun legacyVault(digest: String): NeraeCredentialVault {
        val slot = slot(digest)
        return NeraeCredentialVault(slot.storage, slot.keys, Any(), slot.validator)
    }
    fun digest(key: String): String = MessageDigest.getInstance("SHA-256")
        .digest(key.toByteArray(StandardCharsets.UTF_8)).joinToString("") { "%02x".format(it.toInt() and 0xff) }
}

private class SchemaValidator : TrainingDraftPayloadValidator {
    override fun owner(value: String): TrainingDraftPayloadOwner? {
        val subject = Regex("\\\"subjectId\\\":\\\"([0-9]+)\\\"").find(value)?.groupValues?.get(1) ?: return null
        val event = Regex("\\\"clientEventId\\\":\\\"([^\\\"]+)\\\"").find(value)?.groupValues?.get(1) ?: return null
        return try { draftEnvelopeSchema(value); TrainingDraftPayloadOwner(subject, event) } catch (_: Exception) { null }
    }
}
private fun value(schema: Int, note: String = "", pending: String? = null) =
    "{\"schema\":$schema,\"subjectId\":\"101\",\"draft\":{\"clientEventId\":\"event-0001\",\"note\":\"$note\"" +
        (pending?.let { ",\"syncPending\":\"$it\"" } ?: "") + "}}"
private fun expect(code: String, action: () -> Unit) {
    try { action(); error("expected $code") } catch (failure: VaultFailure) { check(failure.code == code) }
}
fun main() {
    val backend = VersionedDraftTestBackend()
    val store = NeraeProtectedTrainingDraftStore(backend, Any(), SchemaValidator())
    val key = "nerae.workout.draft.v1.101.event-0001"
    val old = value(1, "before", "pending"); val next = value(2, "after", "pending")
    check(store.create(key, old))
    val first = store.readVersioned(key)
    check(first.value == old && first.revision == "1")
    val committed = store.compareAndSetVersioned(key, first.revision, true, old, next)
    check(committed.revision == "2" && store.readVersioned(key).value == next)
    expect("conflict") { store.compareAndSetVersioned(key, first.revision, true, old, next) }
    expect("invalid_state") { store.compareAndSetVersioned(key, committed.revision, true, next, old) }
    expect("invalid_state") { store.write(key, old) }
    check(store.readVersioned(key).value == next && store.readVersioned(key).revision == "2")
    val changed = value(2, "next", "pending")
    val last = store.compareAndSetVersioned(key, committed.revision, true, next, changed)
    val duplicate = "{\"schema\":2,\"sc\\u0068ema\":1,\"subjectId\":\"101\",\"draft\":{\"clientEventId\":\"event-0001\"}}"
    expect("invalid_state") { store.compareAndSetVersioned(key, last.revision, true, changed, duplicate) }
    expect("invalid_state") { store.create(key, duplicate) }
    expect("invalid_state") { store.compareAndSetVersioned(key, last.revision, true, changed, value(3)) }
    check(store.readVersioned(key).value == changed)
    check(store.discover("101", "").contains("event-0001"))
    println("PASS: schema 1->2 CAS upgrade, pending preservation, stale/downgrade rejection, ambiguous schema rejection, discovery")
}
