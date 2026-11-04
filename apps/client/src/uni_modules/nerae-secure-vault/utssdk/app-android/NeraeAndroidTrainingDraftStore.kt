package uts.sdk.modules.neraeSecureVault

import android.content.Context
import android.os.Build
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.nio.CharBuffer
import java.nio.charset.StandardCharsets
import java.nio.charset.CodingErrorAction
import java.security.KeyStore
import java.security.MessageDigest

private const val DRAFT_KEY_PREFIX = "nerae.workout.draft.v1."
private const val DRAFT_DIRECTORY = "nerae-training-drafts.v1"
private const val DRAFT_FILE_PREFIX = "nerae-training-draft.v1."
private const val DRAFT_ALIAS_PREFIX = "com.nerae.secure-vault.training-draft.aes-gcm.v1."
private const val DRAFT_FILE_SUFFIX = ".enc"

/** Inventory and storage are injected so JVM tests can model uncertain I/O and orphaned state. */
internal interface TrainingDraftBackend {
    fun ensureAvailable()
    fun fileEntries(): List<String>
    fun aliases(): List<String>
    fun vault(digest: String): NeraeCredentialVault
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
            if (schema !is Number || schema.toDouble() != 1.0 || schema.toInt() != 1) return null
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
        val owner = parseOwner(key)
        val result = backend.vault(owner.digest).read()
        if (!result.found) return@synchronized result
        if (!validPayload(result.value, owner.subject, owner.event)) throw VaultFailure()
        result
    }

    fun create(key: String, value: String): Boolean = synchronized(processLock) {
        val owner = parseOwner(key)
        if (!validPayload(value, owner.subject, owner.event)) throw VaultFailure("invalid_state")
        val vault = backend.vault(owner.digest)
        val previous = vault.read()
        if (previous.found) {
            if (!validPayload(previous.value, owner.subject, owner.event)) throw VaultFailure()
            return@synchronized false
        }
        try {
            vault.compareAndSet(expectedFound = false, expectedValue = "", value = value)
        } catch (failure: VaultFailure) {
            if (failure.code == "conflict") {
                val concurrent = vault.read()
                if (concurrent.found && validPayload(concurrent.value, owner.subject, owner.event)) return@synchronized false
                throw VaultFailure()
            }
            throw failure
        }
        val committed = vault.read()
        if (!committed.found || committed.value != value || !validPayload(committed.value, owner.subject, owner.event)) {
            throw VaultFailure()
        }
        true
    }

    fun write(key: String, value: String) = synchronized(processLock) {
        val owner = parseOwner(key)
        if (!validPayload(value, owner.subject, owner.event)) throw VaultFailure("invalid_state")
        val vault = backend.vault(owner.digest)
        val previous = vault.read()
        if (!previous.found) throw VaultFailure("conflict")
        if (!validPayload(previous.value, owner.subject, owner.event)) throw VaultFailure()
        vault.compareAndSet(expectedFound = true, expectedValue = previous.value, value = value)
        val committed = vault.read()
        if (!committed.found || committed.value != value || !validPayload(committed.value, owner.subject, owner.event)) {
            throw VaultFailure()
        }
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
                backend.vault(digest).read()
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
        return owner
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

    override fun vault(digest: String): NeraeCredentialVault {
        if (digest.length != 64 || digest.any { it !in '0'..'9' && it !in 'a'..'f' }) throw VaultFailure("invalid_state")
        val fileName = "$DRAFT_FILE_PREFIX$digest$DRAFT_FILE_SUFFIX"
        return NeraeCredentialVault(
            AndroidVaultStorage(
                context,
                NeraeVaultSlot.TRAINING_OPERATION_JOURNAL,
                fileName,
                DRAFT_DIRECTORY
            ),
            AndroidVaultKeys("$DRAFT_ALIAS_PREFIX$digest"),
            Any(),
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
        if (key == null || value == null) throw VaultFailure("invalid_state")
        store.write(key, value)
        encode(ok = true)
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

    private fun failure(code: String): String = JSONObject()
        .put("ok", false).put("errorCode", code).put("found", false).put("value", "").toString()

    private fun conflict(): String = JSONObject()
        .put("ok", false).put("errorCode", "conflict").put("found", false).put("value", "").toString()

    private fun encode(ok: Boolean, errorCode: String = "", found: Boolean = false, value: String = ""): String = JSONObject()
        .put("ok", ok).put("errorCode", errorCode).put("found", found).put("value", value).toString()
}
