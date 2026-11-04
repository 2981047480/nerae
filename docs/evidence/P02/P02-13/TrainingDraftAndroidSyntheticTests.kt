package uts.sdk.modules.neraeSecureVault

import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey

private const val TEST_ALIAS_PREFIX = "com.nerae.secure-vault.training-draft.aes-gcm.v1."
private const val TEST_FILE_PREFIX = "nerae-training-draft.v1."

private class DraftTestBackend : TrainingDraftBackend {
    val files = linkedMapOf<String, ByteArray>()
    val keys = linkedMapOf<String, SecretKey>()
    val extraFiles = mutableListOf<String>()
    val extraAliases = mutableListOf<String>()
    var failKeyCreation = false
    var failWrite = false
    var applyThenFail = false
    var unavailable = false

    override fun ensureAvailable() {
        if (unavailable) throw VaultFailure()
    }

    override fun fileEntries(): List<String> = files.keys.map { "$TEST_FILE_PREFIX$it.enc" } + extraFiles.toList()

    override fun aliases(): List<String> = keys.keys.map { "$TEST_ALIAS_PREFIX$it" } + extraAliases.toList()

    override fun vault(digest: String): NeraeCredentialVault {
        val storage = object : VaultStorage {
            override fun read(): ByteArray? = files[digest]?.copyOf()
            override fun writeAtomically(value: ByteArray) {
                if (failWrite) throw VaultFailure()
                files[digest] = value.copyOf()
                if (applyThenFail) throw VaultFailure()
            }
            override fun clear() { files.remove(digest) }
        }
        val vaultKeys = object : VaultKeys {
            override fun existing(): SecretKey? = keys[digest]
            override fun createHardware(): SecretKey {
                if (failKeyCreation) throw VaultFailure()
                return KeyGenerator.getInstance("AES").apply { init(256) }.generateKey().also { keys[digest] = it }
            }
            override fun delete() { keys.remove(digest) }
        }
        val validator = VaultPayloadValidator { value ->
            value.startsWith("{") && value.endsWith("}") && value.toByteArray(StandardCharsets.UTF_8).size <= 64 * 1024
        }
        return NeraeCredentialVault(storage, vaultKeys, Any(), validator)
    }

    fun rawDigest(key: String): String = MessageDigest.getInstance("SHA-256")
        .digest(key.toByteArray(StandardCharsets.UTF_8)).joinToString("") { "%02x".format(it.toInt() and 0xff) }

    fun addOrphanAlias(digest: String) {
        keys[digest] = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
    }
}

private class DraftTestPayloadValidator : TrainingDraftPayloadValidator {
    override fun owner(value: String): TrainingDraftPayloadOwner? {
        val subject = Regex("\\\"subjectId\\\":\\\"([0-9]+)\\\"").find(value)?.groupValues?.get(1) ?: return null
        val event = Regex("\\\"clientEventId\\\":\\\"([^\\\"]+)\\\"").find(value)?.groupValues?.get(1) ?: return null
        if (!value.startsWith("{\"schema\":1,\"subjectId\":\"$subject\",\"draft\":{\"clientEventId\":\"$event\",\"note\":\"")) return null
        if (!value.endsWith("\"}}")) return null
        return TrainingDraftPayloadOwner(subject, event)
    }
}

private fun draftPayload(subject: String = "101", event: String = "event-0001", note: String = ""): String =
    "{\"schema\":1,\"subjectId\":\"$subject\",\"draft\":{\"clientEventId\":\"$event\",\"note\":\"$note\"}}"

private fun draftKey(subject: String = "101", event: String = "event-0001"): String =
    "nerae.workout.draft.v1.$subject.$event"

private fun newStore(backend: DraftTestBackend, lock: Any = Any()) =
    NeraeProtectedTrainingDraftStore(backend, lock, DraftTestPayloadValidator())

private fun checkThat(value: Boolean, message: String) { check(value) { message } }

fun main() {
    val emptyLockedBackend = DraftTestBackend().apply { unavailable = true }
    try { newStore(emptyLockedBackend).discover("101", ""); error("empty locked inventory reported success") }
    catch (failure: VaultFailure) { checkThat(failure.code == "unavailable", "empty locked inventory wrong error") }

    val backend = DraftTestBackend()
    val lock = Any()
    val store = newStore(backend, lock)
    val firstKey = draftKey()
    val first = draftPayload()
    checkThat(!store.read(firstKey).found, "missing draft was not absent")
    checkThat(store.create(firstKey, first), "first create failed")
    checkThat(!store.create(firstKey, draftPayload(note = "replacement")), "duplicate create overwrote")
    checkThat(store.read(firstKey).value == first, "duplicate create changed prior draft")
    store.write(firstKey, draftPayload(note = "updated"))
    checkThat(store.read(firstKey).value == draftPayload(note = "updated"), "existing draft write failed")
    try { store.write(draftKey(event = "event-missing"), draftPayload(event = "event-missing")); error("missing write created a draft") }
    catch (failure: VaultFailure) { checkThat(failure.code == "conflict", "missing write wrong error") }

    val otherSubjectKey = draftKey("202", "event-0001")
    checkThat(store.create(otherSubjectKey, draftPayload("202", "event-0001")), "other subject draft create failed")
    checkThat(store.read(otherSubjectKey).value == draftPayload("202", "event-0001"), "other subject draft was not isolated")
    try { store.read(draftKey("0101")); error("noncanonical subject accepted") }
    catch (failure: VaultFailure) { checkThat(failure.code == "invalid_state", "invalid subject wrong error") }
    try { store.create(firstKey, draftPayload("202", "event-0001")); error("payload subject mismatch accepted") }
    catch (failure: VaultFailure) { checkThat(failure.code == "invalid_state", "subject mismatch wrong error") }

    val concurrentBackend = DraftTestBackend()
    val concurrentLock = Any()
    val stores = listOf(newStore(concurrentBackend, concurrentLock), newStore(concurrentBackend, concurrentLock))
    val winners = AtomicInteger()
    val failures = AtomicInteger()
    val gate = CountDownLatch(1)
    val done = CountDownLatch(64)
    repeat(64) { index ->
        Thread {
            try {
                gate.await()
                if (stores[index % stores.size].create(draftKey("101", "event-race01"), draftPayload("101", "event-race01"))) winners.incrementAndGet()
            } catch (_: Exception) { failures.incrementAndGet() }
            finally { done.countDown() }
        }.start()
    }
    gate.countDown()
    done.await()
    checkThat(winners.get() == 1 && failures.get() == 0, "same-process duplicate create race was not serialized")

    val unknownBackend = DraftTestBackend()
    unknownBackend.applyThenFail = true
    val unknownStore = newStore(unknownBackend)
    try { unknownStore.create(firstKey, first); error("unknown create outcome was reported successful") }
    catch (failure: VaultFailure) { checkThat(failure.code == "unavailable", "unknown create result wrong error") }
    unknownBackend.applyThenFail = false
    checkThat(unknownStore.read(firstKey).value == first, "committed unknown create was not recoverable")
    unknownBackend.applyThenFail = true
    try { unknownStore.write(firstKey, draftPayload(note = "uncertain")); error("unknown write outcome was reported successful") }
    catch (failure: VaultFailure) { checkThat(failure.code == "unavailable", "unknown write result wrong error") }
    unknownBackend.applyThenFail = false
    checkThat(unknownStore.read(firstKey).value == draftPayload(note = "uncertain"), "committed unknown write was not recoverable")

    val keyOnlyBackend = DraftTestBackend()
    keyOnlyBackend.addOrphanAlias(keyOnlyBackend.rawDigest(firstKey))
    try { newStore(keyOnlyBackend).read(firstKey); error("orphan key reported as absent") }
    catch (failure: VaultFailure) { checkThat(failure.code == "unavailable", "orphan key wrong error") }
    val keyOnlyListing = newStore(keyOnlyBackend).discover("101", "")
    checkThat(keyOnlyListing.contains("\"hasUnclassifiedItems\":true"), "orphan key not surfaced")

    val fileOnlyBackend = DraftTestBackend()
    val fileOnlyId = fileOnlyBackend.rawDigest(firstKey)
    fileOnlyBackend.files[fileOnlyId] = byteArrayOf(1, 2, 3)
    try { newStore(fileOnlyBackend).read(firstKey); error("orphan file reported as absent") }
    catch (failure: VaultFailure) { checkThat(failure.code == "unavailable", "orphan file wrong error") }
    checkThat(newStore(fileOnlyBackend).discover("101", "").contains("\"hasUnclassifiedItems\":true"), "orphan file not surfaced")

    val corruptBackend = DraftTestBackend()
    val corruptStore = newStore(corruptBackend)
    checkThat(corruptStore.create(firstKey, first), "corruption setup failed")
    val corruptId = corruptBackend.rawDigest(firstKey)
    corruptBackend.files[corruptId]!![0] = (corruptBackend.files[corruptId]!![0].toInt() xor 0x40).toByte()
    try { corruptStore.read(firstKey); error("tampered ciphertext was accepted") }
    catch (_: VaultFailure) { }
    checkThat(corruptStore.discover("101", "").contains("\"hasUnclassifiedItems\":true"), "corrupt item not surfaced")
    checkThat(corruptBackend.files.containsKey(corruptId) && corruptBackend.keys.containsKey(corruptId), "corrupt item was discarded")

    val mismatchedBackend = DraftTestBackend()
    val mismatchedId = mismatchedBackend.rawDigest(firstKey)
    mismatchedBackend.vault(mismatchedId).compareAndSet(false, "", draftPayload("202", "event-foreign1"))
    val mismatchedStore = newStore(mismatchedBackend)
    try { mismatchedStore.read(firstKey); error("wrong-owner envelope was accepted") }
    catch (_: VaultFailure) { }
    checkThat(mismatchedStore.discover("101", "").contains("\"hasUnclassifiedItems\":true"), "wrong-owner item not surfaced")
    checkThat(mismatchedBackend.files.containsKey(mismatchedId) && mismatchedBackend.keys.containsKey(mismatchedId), "wrong-owner item was discarded")

    val capacityBackend = DraftTestBackend()
    val savedBeforeCapacityFailure = newStore(capacityBackend)
    checkThat(savedBeforeCapacityFailure.create(firstKey, first), "capacity preservation setup failed")
    val priorFileBytes = capacityBackend.files.values.single().copyOf()
    capacityBackend.failKeyCreation = true
    try { savedBeforeCapacityFailure.create(draftKey(event = "event-next01"), draftPayload(event = "event-next01")); error("keystore create failure was reported successful") }
    catch (failure: VaultFailure) { checkThat(failure.code == "unavailable", "keystore capacity failure wrong error") }
    checkThat(capacityBackend.files.size == 1 && capacityBackend.keys.size == 1 &&
        capacityBackend.files.values.single().contentEquals(priorFileBytes), "failed key creation changed an existing draft")
    checkThat(savedBeforeCapacityFailure.read(firstKey).value == first, "existing draft unreadable after key creation failure")

    val failedWriteBackend = DraftTestBackend()
    val failedWriteStore = newStore(failedWriteBackend)
    checkThat(failedWriteStore.create(firstKey, first), "failed-write setup failed")
    failedWriteBackend.failWrite = true
    try { failedWriteStore.write(firstKey, draftPayload(note = "not-committed")); error("pre-commit write failure was reported successful") }
    catch (failure: VaultFailure) { checkThat(failure.code == "unavailable", "pre-commit write failure wrong error") }
    failedWriteBackend.failWrite = false
    checkThat(failedWriteStore.read(firstKey).value == first, "pre-commit write failure lost the previous draft")

    val pageBackend = DraftTestBackend()
    val pageStore = newStore(pageBackend)
    repeat(205) { index ->
        val event = "event-page-%04d".format(index)
        checkThat(pageStore.create(draftKey("101", event), draftPayload("101", event)), "page setup create failed")
    }
    checkThat(pageStore.create(draftKey("202", "event-other1"), draftPayload("202", "event-other1")), "foreign page setup failed")
    pageBackend.extraFiles += "nerae-training-draft.v1.bad.enc"
    val firstPage = pageStore.discover("101", "")
    checkThat(firstPage.contains("\"hasUnclassifiedItems\":true"), "malformed inventory not surfaced")
    checkThat(firstPage.contains("\"eventIds\":[\"event-page-0000\""), "first page is not UTF-8 sorted")
    checkThat(firstPage.contains("\"nextCursor\":\"event-page-0099\""), "first page cursor mismatch")
    val secondPage = pageStore.discover("101", "event-page-0099")
    checkThat(secondPage.contains("\"nextCursor\":\"event-page-0199\""), "second page cursor mismatch")
    val lastPage = pageStore.discover("101", "event-page-0199")
    checkThat(lastPage.contains("\"event-page-0204\""), "last page omitted expected item")
    checkThat(lastPage.contains("\"nextCursor\":null"), "last page has a cursor")
    checkThat(!lastPage.contains("event-other1"), "other subject leaked into discovery")

    println("PASS: Android draft isolated create/update, same-process race, fail-closed key/file states, unknown writes, and UTF-8 paged discovery")
}
