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
private class VersionedDraftTestValidator : TrainingDraftPayloadValidator {
    override fun owner(value: String): TrainingDraftPayloadOwner? {
        val subject = Regex("\\\"subjectId\\\":\\\"([0-9]+)\\\"").find(value)?.groupValues?.get(1) ?: return null
        val event = Regex("\\\"clientEventId\\\":\\\"([^\\\"]+)\\\"").find(value)?.groupValues?.get(1) ?: return null
        if (!value.startsWith("{\"schema\":1,\"subjectId\":\"$subject\",\"draft\":{\"clientEventId\":\"$event\",\"note\":\"")) return null
        if (!value.endsWith("\"}}")) return null
        return TrainingDraftPayloadOwner(subject, event)
    }
}
private fun value(note: String = "", subject: String = "101", event: String = "event-0001") =
    "{\"schema\":1,\"subjectId\":\"$subject\",\"draft\":{\"clientEventId\":\"$event\",\"note\":\"$note\"}}"
private const val key = "nerae.workout.draft.v1.101.event-0001"
private fun bytes(value: String) = value.toByteArray(StandardCharsets.UTF_8)
private fun expect(code: String, operation: () -> Unit) {
    try { operation(); error("expected $code") } catch (failure: VaultFailure) { check(failure.code == code) }
}

fun main() {
    val legacyBackend = VersionedDraftTestBackend()
    val legacyPayload = value("旧草稿 e\u0301")
    legacyBackend.legacyVault(legacyBackend.digest(key)).compareAndSet(false, "", legacyPayload)
    val legacyCipher = legacyBackend.files.values.single().copyOf()
    val legacy = NeraeProtectedTrainingDraftStore(legacyBackend, Any(), VersionedDraftTestValidator())
    val beforeUpgrade = legacy.readVersioned(key)
    check(beforeUpgrade.found && beforeUpgrade.revision == "0" && beforeUpgrade.value == legacyPayload)
    check(legacyBackend.files.values.single().contentEquals(legacyCipher))
    val afterUpgrade = legacy.compareAndSetVersioned(key, "0", true, legacyPayload, legacyPayload)
    check(afterUpgrade.revision == "1")
    check(legacy.readVersioned(key).value == legacyPayload)
    check(legacy.readVersioned(key).revision == "1")
    check(legacy.read(key).value == legacyPayload)
    expect("conflict") { legacy.compareAndSetVersioned(key, "0", true, legacyPayload, value("stale")) }

    val migrationBackend = VersionedDraftTestBackend()
    val migrationDigest = migrationBackend.digest(key)
    val fullLegacy = value("x".repeat(65536 - bytes(value()).size))
    migrationBackend.legacyVault(migrationDigest).compareAndSet(false, "", fullLegacy)
    val oldCipher = migrationBackend.files.getValue(migrationDigest).copyOf()
    val migration = NeraeProtectedTrainingDraftStore(migrationBackend, Any(), VersionedDraftTestValidator())
    migrationBackend.failBeforeWrite = true
    expect("unavailable") { migration.compareAndSetVersioned(key, "0", true, fullLegacy, fullLegacy) }
    migrationBackend.failBeforeWrite = false
    check(migrationBackend.files.getValue(migrationDigest).contentEquals(oldCipher))
    check(migration.readVersioned(key).revision == "0" && migration.readVersioned(key).value == fullLegacy)
    migrationBackend.failAfterWrite = true
    expect("unavailable") { migration.compareAndSetVersioned(key, "0", true, fullLegacy, fullLegacy) }
    migrationBackend.failAfterWrite = false
    check(migration.readVersioned(key).revision == "1" && migration.readVersioned(key).value == fullLegacy)
    expect("conflict") { migration.compareAndSetVersioned(key, "0", true, fullLegacy, fullLegacy) }

    val createBackend = VersionedDraftTestBackend()
    val createStore = NeraeProtectedTrainingDraftStore(createBackend, Any(), VersionedDraftTestValidator())
    val created = createStore.compareAndSetVersioned(key, "0", false, "", value("new"))
    check(created.revision == "1" && createStore.readVersioned(key).value == value("new"))
    expect("conflict") { createStore.compareAndSetVersioned(key, "0", false, "", value("replace")) }
    val uncertainCreateBackend = VersionedDraftTestBackend()
    val uncertainCreate = NeraeProtectedTrainingDraftStore(uncertainCreateBackend, Any(), VersionedDraftTestValidator())
    uncertainCreateBackend.failAfterWrite = true
    expect("unavailable") { uncertainCreate.compareAndSetVersioned(key, "0", false, "", value("created-unknown")) }
    uncertainCreateBackend.failAfterWrite = false
    check(uncertainCreate.readVersioned(key).revision == "1" && uncertainCreate.readVersioned(key).value == value("created-unknown"))

    val backend = VersionedDraftTestBackend()
    val lock = Any()
    val store = NeraeProtectedTrainingDraftStore(backend, lock, VersionedDraftTestValidator())
    check(!store.readVersioned(key).found && store.readVersioned(key).revision == "0")
    check(store.create(key, value("A")))
    val a1 = store.readVersioned(key)
    check(a1.found && a1.value == value("A") && a1.revision == "1")
    check(!a1.toString().contains(value("A")))
    store.write(key, value("B"))
    check(store.readVersioned(key).revision == "2")
    store.write(key, value("A"))
    check(store.readVersioned(key).revision == "3")
    expect("conflict") { store.compareAndSetVersioned(key, a1.revision, true, a1.value, value("stale")) }
    check(store.readVersioned(key).value == value("A"))
    expect("conflict") { store.compareAndSetVersioned(key, "3", true, value("different"), value("stale")) }
    expect("invalid_state") { store.compareAndSetVersioned(key, "00", true, value("A"), value("stale")) }
    expect("invalid_state") { store.compareAndSetVersioned(key, "9223372036854775808", true, value("A"), value("stale")) }
    expect("invalid_state") { store.compareAndSetVersioned(key, "3", false, "", value("stale")) }
    expect("invalid_state") { store.compareAndSetVersioned(key, "3", true, value("A"), value("foreign", subject = "202")) }
    expect("invalid_state") { store.compareAndSetVersioned(key, "3", true, value("A"), value("\uD800")) }
    val composed = value("\u00e9")
    val decomposed = value("e\u0301")
    store.write(key, composed)
    val unicode = store.readVersioned(key)
    expect("conflict") { store.compareAndSetVersioned(key, unicode.revision, true, decomposed, value("stale")) }
    check(bytes(store.readVersioned(key).value).contentEquals(bytes(composed)))

    val baseLength = bytes(value()).size
    val exact = value("x".repeat(65536 - baseLength))
    check(bytes(exact).size == 65536)
    val exactSaved = store.compareAndSetVersioned(key, unicode.revision, true, composed, exact)
    check(store.readVersioned(key).value == exact && store.readVersioned(key).revision == exactSaved.revision)
    check(backend.files.values.single().size == 65536 + 5 + 8 + 12 + 16)
    check(NeraeVersionedTrainingJournal.MAX_ENVELOPE_BYTES >= backend.files.values.single().size)
    val tooLarge = value("x".repeat(65537 - baseLength))
    expect("invalid_state") { store.compareAndSetVersioned(key, exactSaved.revision, true, exact, tooLarge) }
    check(store.readVersioned(key).value == exact)

    val raceBase = store.readVersioned(key)
    val winners = AtomicInteger()
    val conflicts = AtomicInteger()
    val gate = CountDownLatch(1)
    val done = CountDownLatch(16)
    repeat(16) { index ->
        Thread {
            try {
                gate.await()
                val peer = NeraeProtectedTrainingDraftStore(backend, lock, VersionedDraftTestValidator())
                peer.compareAndSetVersioned(key, raceBase.revision, true, raceBase.value, value("race-$index"))
                winners.incrementAndGet()
            } catch (failure: VaultFailure) {
                if (failure.code == "conflict") conflicts.incrementAndGet() else throw failure
            } finally { done.countDown() }
        }.start()
    }
    gate.countDown()
    done.await()
    check(winners.get() == 1 && conflicts.get() == 15)

    val beforeUnknown = store.readVersioned(key)
    backend.failAfterWrite = true
    expect("unavailable") { store.compareAndSetVersioned(key, beforeUnknown.revision, true, beforeUnknown.value, value("unknown")) }
    backend.failAfterWrite = false
    val afterUnknown = store.readVersioned(key)
    check(afterUnknown.value == value("unknown") && afterUnknown.revision.toLong() == beforeUnknown.revision.toLong() + 1)
    expect("conflict") { store.compareAndSetVersioned(key, beforeUnknown.revision, true, beforeUnknown.value, value("retry")) }
    backend.failBeforeWrite = true
    expect("unavailable") { store.compareAndSetVersioned(key, afterUnknown.revision, true, afterUnknown.value, value("not-written")) }
    backend.failBeforeWrite = false
    check(store.readVersioned(key) == afterUnknown)
    backend.failNextReadAfterWrite = true
    expect("unavailable") { store.compareAndSetVersioned(key, afterUnknown.revision, true, afterUnknown.value, value("readback-unknown")) }
    check(store.readVersioned(key).value == value("readback-unknown"))

    val fullKey = backend.keys.getValue(backend.digest(key))
    val maxHeader = ByteBuffer.allocate(13).put(byteArrayOf(0x4e, 0x52, 0x44, 0x56, 0x02)).putLong(Long.MAX_VALUE).array()
    val cipher = Cipher.getInstance("AES/GCM/NoPadding")
    cipher.init(Cipher.ENCRYPT_MODE, fullKey)
    cipher.updateAAD(maxHeader)
    backend.files[backend.digest(key)] = maxHeader + cipher.iv + cipher.doFinal(bytes(value("max-revision")))
    val maximum = store.readVersioned(key)
    check(maximum.revision == Long.MAX_VALUE.toString())
    val maxCipher = backend.files.values.single().copyOf()
    expect("invalid_state") { store.write(key, value("overflow")) }
    expect("invalid_state") { store.compareAndSetVersioned(key, maximum.revision, true, maximum.value, value("overflow")) }
    check(backend.files.values.single().contentEquals(maxCipher))

    val corrupt = backend.files.values.single()
    corrupt[5] = (corrupt[5].toInt() xor 0x01).toByte()
    val savedCorrupt = corrupt.copyOf()
    expect("unavailable") { store.readVersioned(key) }
    expect("unavailable") { store.write(key, value("not-overwrite-corruption")) }
    check(backend.files.values.single().contentEquals(savedCorrupt))
    println("PASS: Android versioned draft legacy upgrade, CAS/ABA, 64 KiB, race, unknown outcome, corruption")
}
