package uts.sdk.modules.neraeSecureVault

import android.content.Context
import android.os.Build
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.nio.CharBuffer
import java.nio.ByteBuffer
import java.nio.charset.StandardCharsets
import java.nio.charset.CodingErrorAction
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec

private const val DRAFT_KEY_PREFIX = "nerae.workout.draft.v1."
private const val DRAFT_DIRECTORY = "nerae-training-drafts.v1"
private const val DRAFT_FILE_PREFIX = "nerae-training-draft.v1."
private const val DRAFT_ALIAS_PREFIX = "com.nerae.secure-vault.training-draft.aes-gcm.v1."
private const val DRAFT_FILE_SUFFIX = ".enc"
private val DRAFT_LEGACY_HEADER = byteArrayOf(0x4e, 0x52, 0x56, 0x41, 0x01) // NRVA v1
private val DRAFT_VERSIONED_MAGIC = byteArrayOf(0x4e, 0x52, 0x44, 0x56, 0x02) // NRDV v2
private const val DRAFT_NONCE_BYTES = 12
private const val DRAFT_TAG_BYTES = 16
private const val DRAFT_REVISION_BYTES = 8

internal data class TrainingDraftVault(
    val storage: VaultStorage,
    val keys: VaultKeys,
    val validator: VaultPayloadValidator
) {
    override fun toString(): String = "TrainingDraftVault{redacted}"
}

internal data class VersionedTrainingDraftResult(val found: Boolean, val value: String, val revision: String) {
    override fun toString(): String = "VersionedTrainingDraftResult{redacted}"
}

/** Inventory and storage are injected so JVM tests can model uncertain I/O and orphaned state. */
internal interface TrainingDraftBackend {
    fun ensureAvailable()
    fun fileEntries(): List<String>
    fun aliases(): List<String>
    fun slot(digest: String): TrainingDraftVault
}

internal data class TrainingDraftPayloadOwner(val subject: String, val event: String)

internal interface TrainingDraftPayloadValidator {
    fun owner(value: String): TrainingDraftPayloadOwner?
}

/** Android JSON parsing stays behind an injectable seam for plain JVM tests. */
internal class AndroidTrainingDraftPayloadValidator : TrainingDraftPayloadValidator {
    override fun owner(value: String): TrainingDraftPayloadOwner? {
        return try {
            val root = JSONObject(value)
            if (root.keys().asSequence().toSet() != setOf("schema", "subjectId", "draft")) return null
            val schema = root.opt("schema")
            if (schema !is Number || (schema.toDouble() != 1.0 && schema.toDouble() != 2.0)) return null
            val subject = root.opt("subjectId") as? String ?: return null
            val draft = root.optJSONObject("draft") ?: return null
            val event = draft.opt("clientEventId") as? String ?: return null
            TrainingDraftPayloadOwner(subject, event)
        } catch (_: Exception) {
            null
        }
    }
}

internal class NeraeProtectedTrainingDraftStore(
    private val backend: TrainingDraftBackend,
    private val processLock: Any,
    private val payloadValidator: TrainingDraftPayloadValidator
) {
    companion object {
        const val DISCOVERY_PAGE_SIZE = 100
        const val MAX_VALUE_BYTES = 64 * 1024
    }

    fun read(key: String): VaultReadResult = synchronized(processLock) {
        val result = readVersioned(key)
        VaultReadResult(result.found, result.value)
    }

    fun readVersioned(key: String): VersionedTrainingDraftResult = synchronized(processLock) {
        val owner = parseOwner(key)
        val state = readSlot(backend.slot(owner.digest))
        if (state.found && !validPayload(state.value, owner.subject, owner.event)) throw VaultFailure()
        if (state.found) pendingBytes(state.value, "unavailable")
        VersionedTrainingDraftResult(state.found, state.value, state.revision.toString())
    }

    fun create(key: String, value: String): Boolean = synchronized(processLock) {
        val owner = parseOwner(key)
        if (!validPayload(value, owner.subject, owner.event)) throw VaultFailure("invalid_state")
        pendingBytes(value, "invalid_state")
        val slot = backend.slot(owner.digest)
        val previous = readSlot(slot)
        if (previous.found) {
            if (!validPayload(previous.value, owner.subject, owner.event)) throw VaultFailure()
            pendingBytes(previous.value, "unavailable")
            return@synchronized false
        }
        persist(slot, owner, previous, value)
        true
    }

    fun write(key: String, value: String): Unit = synchronized(processLock) {
        val owner = parseOwner(key)
        if (!validPayload(value, owner.subject, owner.event)) throw VaultFailure("invalid_state")
        val proposedPending = pendingBytes(value, "invalid_state")
        val slot = backend.slot(owner.digest)
        val previous = readSlot(slot)
        if (!previous.found) throw VaultFailure("conflict")
        if (!validPayload(previous.value, owner.subject, owner.event)) throw VaultFailure()
        val currentPending = pendingBytes(previous.value, "unavailable")
        // A stale unversioned editor may neither change nor resurrect a pending operation.
        if ((currentPending == null) != (proposedPending == null) ||
            (currentPending != null && proposedPending != null && !currentPending.contentEquals(proposedPending))) {
            throw VaultFailure("conflict")
        }
        requireNondecreasingSchema(previous.value, value)
        persist(slot, owner, previous, value)
    }

    fun compareAndSetVersioned(key: String, expectedRevision: String, expectedFound: Boolean,
                               expectedValue: String, value: String): VersionedTrainingDraftResult = synchronized(processLock) {
        val owner = parseOwner(key)
        val revision = parseRevision(expectedRevision)
        if ((!expectedFound && (revision != 0L || expectedValue.isNotEmpty())) ||
            (expectedFound && !validPayload(expectedValue, owner.subject, owner.event)) ||
            !validPayload(value, owner.subject, owner.event)) throw VaultFailure("invalid_state")
        if (expectedFound) pendingBytes(expectedValue, "invalid_state")
        pendingBytes(value, "invalid_state")
        val slot = backend.slot(owner.digest)
        val previous = readSlot(slot)
        if (previous.found && !validPayload(previous.value, owner.subject, owner.event)) throw VaultFailure()
        if (previous.found) pendingBytes(previous.value, "unavailable")
        if (previous.revision != revision || previous.found != expectedFound ||
            previous.value != expectedValue) throw VaultFailure("conflict")
        if (previous.found) requireNondecreasingSchema(previous.value, value)
        val committed = persist(slot, owner, previous, value)
        VersionedTrainingDraftResult(false, "", committed.revision.toString())
    }

    fun discover(subject: String, cursor: String): String = synchronized(processLock) {
        if (!validSubject(subject)) throw VaultFailure("invalid_state")
        if (cursor.isNotEmpty()) parseOwner("$DRAFT_KEY_PREFIX$subject.$cursor")

        backend.ensureAvailable()
        val files = backend.fileEntries()
        val aliases = backend.aliases()
        val fileIds = linkedSetOf<String>()
        val aliasIds = linkedSetOf<String>()
        var hasUnclassifiedItems = false
        for (entry in files) {
            val digest = parseFileDigest(entry)
            if (digest == null || !fileIds.add(digest)) hasUnclassifiedItems = true
        }
        for (alias in aliases) {
            if (!alias.startsWith(DRAFT_ALIAS_PREFIX)) continue
            val digest = alias.removePrefix(DRAFT_ALIAS_PREFIX)
            if (!isDigest(digest) || !aliasIds.add(digest)) hasUnclassifiedItems = true
        }

        val candidates = fileIds + aliasIds
        val events = java.util.TreeSet<String> { left, right -> compareUtf8(left, right) }
        var hasMore = false
        for (digest in candidates) {
            if (digest !in fileIds || digest !in aliasIds) {
                hasUnclassifiedItems = true
                continue
            }
            val result = try {
                readSlot(backend.slot(digest))
            } catch (_: Exception) {
                hasUnclassifiedItems = true
                continue
            }
            if (!result.found) {
                hasUnclassifiedItems = true
                continue
            }
            val owner = try {
                ownerFromPayload(result.value, digest)
            } catch (_: Exception) {
                hasUnclassifiedItems = true
                continue
            }
            if (owner.subject != subject || (cursor.isNotEmpty() && compareUtf8(owner.event, cursor) <= 0)) continue
            events.add(owner.event)
            if (events.size > DISCOVERY_PAGE_SIZE) {
                events.pollLast()
                hasMore = true
            }
        }

        val nextCursor = if (hasMore) events.last() else null
        "{\"eventIds\":[${events.joinToString(",") { jsonString(it) }}]," +
            "\"nextCursor\":${nextCursor?.let(::jsonString) ?: "null"}," +
            "\"hasUnclassifiedItems\":$hasUnclassifiedItems}"
    }

    private data class Owner(val subject: String, val event: String, val digest: String)
    private data class DraftState(val found: Boolean, val value: String, val revision: Long) {
        override fun toString(): String = "DraftState{redacted}"
    }

    private fun pendingBytes(value: String, errorCode: String): ByteArray? = try {
        draftSyncPendingBytes(value)
    } catch (_: Exception) {
        throw VaultFailure(errorCode)
    }

    private fun parseRevision(value: String): Long {
        if (!value.matches(Regex("^(0|[1-9][0-9]{0,18})$"))) throw VaultFailure("invalid_state")
        return value.toLongOrNull() ?: throw VaultFailure("invalid_state")
    }

    /** Revision and the unchanged payload are authenticated together in one file. */
    private fun readSlot(slot: TrainingDraftVault): DraftState {
        try {
            val encrypted = slot.storage.read()
            if (encrypted == null) {
                if (slot.keys.existing() != null) throw VaultFailure()
                return DraftState(false, "", 0)
            }
            val secret = slot.keys.existing() ?: throw VaultFailure()
            val versioned = encrypted.size >= DRAFT_VERSIONED_MAGIC.size &&
                encrypted.copyOfRange(0, DRAFT_VERSIONED_MAGIC.size).contentEquals(DRAFT_VERSIONED_MAGIC)
            val legacy = encrypted.size >= DRAFT_LEGACY_HEADER.size &&
                encrypted.copyOfRange(0, DRAFT_LEGACY_HEADER.size).contentEquals(DRAFT_LEGACY_HEADER)
            if (!versioned && !legacy) throw VaultFailure()
            val headerSize = if (versioned) DRAFT_VERSIONED_MAGIC.size + DRAFT_REVISION_BYTES else DRAFT_LEGACY_HEADER.size
            if (encrypted.size !in (headerSize + DRAFT_NONCE_BYTES + DRAFT_TAG_BYTES)..
                (headerSize + DRAFT_NONCE_BYTES + DRAFT_TAG_BYTES + MAX_VALUE_BYTES)) throw VaultFailure()
            val revision = if (versioned) ByteBuffer.wrap(encrypted, DRAFT_VERSIONED_MAGIC.size, DRAFT_REVISION_BYTES).long else 0L
            if (versioned && revision <= 0) throw VaultFailure()
            val header = encrypted.copyOfRange(0, headerSize)
            val nonce = encrypted.copyOfRange(headerSize, headerSize + DRAFT_NONCE_BYTES)
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE, secret, GCMParameterSpec(128, nonce))
            cipher.updateAAD(header)
            val plaintext = cipher.doFinal(encrypted, headerSize + DRAFT_NONCE_BYTES,
                encrypted.size - headerSize - DRAFT_NONCE_BYTES)
            if (plaintext.size !in 1..MAX_VALUE_BYTES) throw VaultFailure()
            val value = StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(plaintext)).toString()
            if (!slot.validator.isJsonObject(value)) throw VaultFailure()
            return DraftState(true, value, revision)
        } catch (failure: VaultFailure) { throw failure }
        catch (_: Exception) { throw VaultFailure() }
    }

    private fun persist(slot: TrainingDraftVault, owner: Owner, previous: DraftState, value: String): DraftState {
        if (previous.revision == Long.MAX_VALUE) throw VaultFailure("invalid_state")
        val payload = strictUtf8(value) ?: throw VaultFailure("invalid_state")
        if (payload.size !in 1..MAX_VALUE_BYTES || !slot.validator.isJsonObject(value)) throw VaultFailure("invalid_state")
        val revision = previous.revision + 1
        val header = ByteBuffer.allocate(DRAFT_VERSIONED_MAGIC.size + DRAFT_REVISION_BYTES)
            .put(DRAFT_VERSIONED_MAGIC).putLong(revision).array()
        var createdKey = false
        try {
            val secret = if (previous.found) slot.keys.existing() ?: throw VaultFailure()
                else {
                    if (slot.keys.existing() != null) throw VaultFailure()
                    slot.keys.createHardware().also { createdKey = true }
                }
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE, secret)
            val nonce = cipher.iv ?: throw VaultFailure()
            if (nonce.size != DRAFT_NONCE_BYTES) throw VaultFailure()
            cipher.updateAAD(header)
            slot.storage.writeAtomically(header + nonce + cipher.doFinal(payload))
            val committed = readSlot(slot)
            if (!committed.found || committed.revision != revision || committed.value != value ||
                !validPayload(committed.value, owner.subject, owner.event)) throw VaultFailure()
            return committed
        } catch (failure: VaultFailure) {
            if (createdKey) discardNewKeyIfEmpty(slot)
            throw failure
        } catch (_: Exception) {
            if (createdKey) discardNewKeyIfEmpty(slot)
            throw VaultFailure()
        }
    }

    private fun discardNewKeyIfEmpty(slot: TrainingDraftVault) {
        try { if (slot.storage.read() == null) slot.keys.delete() } catch (_: Exception) { /* preserve ambiguous state */ }
    }

    private fun parseOwner(key: String): Owner {
        val encodedKey = strictUtf8(key)
        if (!key.startsWith(DRAFT_KEY_PREFIX) || encodedKey == null || encodedKey.size > 1024) {
            throw VaultFailure("invalid_state")
        }
        val rest = key.removePrefix(DRAFT_KEY_PREFIX)
        val separator = rest.indexOf('.')
        if (separator <= 0 || separator == rest.lastIndex) throw VaultFailure("invalid_state")
        val subject = rest.substring(0, separator)
        val event = rest.substring(separator + 1)
        if (!validSubject(subject) || !validEvent(event)) throw VaultFailure("invalid_state")
        return Owner(subject, event, digest(encodedKey))
    }

    private fun ownerFromPayload(value: String, expectedDigest: String): Owner {
        val encoded = strictUtf8(value) ?: throw VaultFailure()
        if (encoded.size > MAX_VALUE_BYTES) throw VaultFailure()
        val parsed = payloadValidator.owner(value) ?: throw VaultFailure()
        val subject = parsed.subject
        val event = parsed.event
        if (!validSubject(subject) || !validEvent(event)) throw VaultFailure()
        val owner = Owner(subject, event, digest("$DRAFT_KEY_PREFIX$subject.$event"))
        if (owner.digest != expectedDigest || !validPayload(value, subject, event)) throw VaultFailure()
        pendingBytes(value, "unavailable")
        return owner
    }

    private fun requireNondecreasingSchema(previous: String, value: String) {
        try {
            if (draftEnvelopeSchema(value) < draftEnvelopeSchema(previous)) throw IllegalArgumentException()
        } catch (_: Exception) {
            throw VaultFailure("invalid_state")
        }
    }

    private fun validPayload(value: String, subject: String, event: String): Boolean {
        return try {
            val encoded = strictUtf8(value) ?: return false
            if (encoded.size !in 1..MAX_VALUE_BYTES) return false
            payloadValidator.owner(value) == TrainingDraftPayloadOwner(subject, event)
        } catch (_: Exception) {
            false
        }
    }

    private fun validSubject(value: String): Boolean = value.isNotEmpty() && value.toByteArray(StandardCharsets.UTF_8).size <= 20 &&
        value[0] != '0' && value.all { it in '0'..'9' }

    private fun validEvent(value: String): Boolean {
        if (strictUtf8(value) == null) return false
        val length = value.codePointCount(0, value.length)
        return length in 8..64 && value.trim().isNotEmpty()
    }

    private fun digest(value: ByteArray): String = MessageDigest.getInstance("SHA-256")
        .digest(value).joinToString("") { "%02x".format(it.toInt() and 0xff) }

    private fun digest(value: String): String = digest(strictUtf8(value) ?: throw VaultFailure("invalid_state"))

    private fun strictUtf8(value: String): ByteArray? = try {
        val buffer = StandardCharsets.UTF_8.newEncoder()
            .onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT)
            .encode(CharBuffer.wrap(value))
        ByteArray(buffer.remaining()).also(buffer::get)
    } catch (_: Exception) {
        null
    }

    private fun isDigest(value: String): Boolean = value.length == 64 && value.all { it in '0'..'9' || it in 'a'..'f' }

    private fun parseFileDigest(value: String): String? {
        if (!value.startsWith(DRAFT_FILE_PREFIX) || !value.endsWith(DRAFT_FILE_SUFFIX)) return null
        val digest = value.removePrefix(DRAFT_FILE_PREFIX).removeSuffix(DRAFT_FILE_SUFFIX)
        return digest.takeIf(::isDigest)
    }

    private fun compareUtf8(left: String, right: String): Int {
        val a = left.toByteArray(StandardCharsets.UTF_8)
        val b = right.toByteArray(StandardCharsets.UTF_8)
        for (index in 0 until minOf(a.size, b.size)) {
            val difference = (a[index].toInt() and 0xff) - (b[index].toInt() and 0xff)
            if (difference != 0) return difference
        }
        return a.size - b.size
    }

    private fun jsonString(value: String): String = buildString {
        append('"')
        for (char in value) {
            when (char) {
                '"' -> append("\\\"")
                '\\' -> append("\\\\")
                '\b' -> append("\\b")
                '\u000c' -> append("\\f")
                '\n' -> append("\\n")
                '\r' -> append("\\r")
                '\t' -> append("\\t")
                else -> if (char.code < 0x20) append("\\u%04x".format(char.code)) else append(char)
            }
        }
        append('"')
    }
}

/** Production backend uses a dedicated no-backup directory and one Keystore alias per digest. */
internal class AndroidTrainingDraftBackend(private val context: Context) : TrainingDraftBackend {
    override fun ensureAvailable() {
        ensureAndroidVaultUserUnlocked(context.applicationContext)
    }

    private fun draftDirectory(): File {
        ensureAvailable()
        val root = context.applicationContext.noBackupFilesDir.canonicalFile
        if (!root.isDirectory || !root.canRead() || !root.canWrite()) throw VaultFailure()
        val directory = File(root, DRAFT_DIRECTORY)
        if (directory.exists()) {
            if (!directory.isDirectory || directory.canonicalFile != directory.absoluteFile) throw VaultFailure()
        } else if (!directory.mkdir()) {
            throw VaultFailure()
        }
        val canonical = directory.canonicalFile
        if (canonical.parentFile?.canonicalFile != root || !canonical.canRead() || !canonical.canWrite()) throw VaultFailure()
        return canonical
    }

    override fun fileEntries(): List<String> {
        val entries = draftDirectory().list() ?: throw VaultFailure()
        return entries.toList()
    }

    override fun aliases(): List<String> = try {
        ensureAvailable()
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        store.aliases().toList()
    } catch (_: Exception) {
        throw VaultFailure()
    }

    override fun slot(digest: String): TrainingDraftVault {
        if (digest.length != 64 || digest.any { it !in '0'..'9' && it !in 'a'..'f' }) throw VaultFailure("invalid_state")
        val fileName = "$DRAFT_FILE_PREFIX$digest$DRAFT_FILE_SUFFIX"
        return TrainingDraftVault(
            AndroidVaultStorage(
                context,
                NeraeVaultSlot.TRAINING_OPERATION_JOURNAL,
                fileName,
                DRAFT_DIRECTORY
            ),
            AndroidVaultKeys("$DRAFT_ALIAS_PREFIX$digest"),
            AndroidJsonObjectValidator
        )
    }
}

@Suppress("unused")
object NeraeAndroidTrainingDraftBridge {
    private val processLock = Any()

    @JvmStatic
    fun readTrainingDraft(context: Context?, key: String?): String = withStore(context) { store ->
        val result = store.read(key ?: throw VaultFailure("invalid_state"))
        encode(ok = true, found = result.found, value = result.value)
    }

    @JvmStatic
    fun createTrainingDraft(context: Context?, key: String?, value: String?): String = withStore(context) { store ->
        if (key == null || value == null) throw VaultFailure("invalid_state")
        if (!store.create(key, value)) throw VaultFailure("conflict")
        encode(ok = true)
    }

    @JvmStatic
    fun writeTrainingDraft(context: Context?, key: String?, value: String?): String = withStore(context) { store ->
        rejectLegacyDraftWrite(store, key, value)
    }

    // An unconditional writer could restore stale input after a pending
    // marker was cleared. App writes must use the revision-bound CAS entry.
    @Suppress("UNUSED_PARAMETER")
    internal fun rejectLegacyDraftWrite(store: NeraeProtectedTrainingDraftStore, key: String?, value: String?): Nothing {
        throw VaultFailure("unavailable")
    }

    @JvmStatic
    fun readVersionedTrainingDraft(context: Context?, key: String?): String = withVersionedStore(context) { store ->
        val result = store.readVersioned(key ?: throw VaultFailure("invalid_state"))
        encodeVersioned(ok = true, found = result.found, value = result.value, revision = result.revision)
    }

    @JvmStatic
    fun compareAndSetVersionedTrainingDraft(context: Context?, key: String?, expectedRevision: String?,
                                             expectedFound: Boolean, expectedValue: String?, value: String?): String =
        withVersionedStore(context) { store ->
            if (key == null || expectedRevision == null || expectedValue == null || value == null) throw VaultFailure("invalid_state")
            val result = store.compareAndSetVersioned(key, expectedRevision, expectedFound, expectedValue, value)
            encodeVersioned(ok = true, revision = result.revision)
        }

    @JvmStatic
    fun discoverTrainingDrafts(context: Context?, subject: String?, cursor: String?): String = withStore(context) { store ->
        val listing = store.discover(subject ?: throw VaultFailure("invalid_state"), cursor ?: "")
        encode(ok = true, found = true, value = listing)
    }

    private fun withStore(context: Context?, action: (NeraeProtectedTrainingDraftStore) -> String): String {
        if (context == null || Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return failure("unavailable")
        return try {
            synchronized(processLock) {
                action(NeraeProtectedTrainingDraftStore(AndroidTrainingDraftBackend(context), processLock, AndroidTrainingDraftPayloadValidator()))
            }
        } catch (problem: VaultFailure) {
            if (problem.code == "conflict") conflict() else failure(problem.code)
        } catch (_: Exception) {
            failure("unavailable")
        }
    }

    private fun withVersionedStore(context: Context?, action: (NeraeProtectedTrainingDraftStore) -> String): String {
        if (context == null || Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return versionedFailure("unavailable")
        return try {
            synchronized(processLock) {
                action(NeraeProtectedTrainingDraftStore(AndroidTrainingDraftBackend(context), processLock, AndroidTrainingDraftPayloadValidator()))
            }
        } catch (problem: VaultFailure) {
            val code = if (problem.code in setOf("conflict", "invalid_state", "unavailable")) problem.code else "unavailable"
            versionedFailure(code)
        } catch (_: Exception) {
            versionedFailure("unavailable")
        }
    }

    private fun versionedFailure(code: String): String = encodeVersioned(ok = false, errorCode = code)

    private fun encodeVersioned(ok: Boolean, errorCode: String = "", found: Boolean = false,
                                value: String = "", revision: String = ""): String = JSONObject()
        .put("ok", ok).put("errorCode", errorCode).put("found", found).put("value", value).put("revision", revision).toString()

    private fun failure(code: String): String = JSONObject()
        .put("ok", false).put("errorCode", code).put("found", false).put("value", "").toString()

    private fun conflict(): String = JSONObject()
        .put("ok", false).put("errorCode", "conflict").put("found", false).put("value", "").toString()

    private fun encode(ok: Boolean, errorCode: String = "", found: Boolean = false, value: String = ""): String = JSONObject()
        .put("ok", ok).put("errorCode", errorCode).put("found", found).put("value", value).toString()
}
