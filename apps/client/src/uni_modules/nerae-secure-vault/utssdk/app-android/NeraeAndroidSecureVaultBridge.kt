package uts.sdk.modules.neraeSecureVault

import android.content.Context
import android.os.Build
import android.app.KeyguardManager
import android.os.UserManager
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyProperties
import android.security.keystore.StrongBoxUnavailableException
import android.util.AtomicFile
import android.util.JsonReader
import android.util.JsonToken
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File
import java.io.FileInputStream
import java.io.FileNotFoundException
import java.io.FileOutputStream
import java.nio.ByteBuffer
import java.nio.CharBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.io.StringReader
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec

private const val KEYSTORE = "AndroidKeyStore"

/** Fixed storage identities; callers cannot supply aliases or filesystem paths. */
internal enum class NeraeVaultSlot(val keyAlias: String, val fileName: String) {
    AUTHENTICATION_STATE(
        "com.nerae.secure-vault.authentication-state.aes-gcm.v1",
        "nerae-auth-state.v1.enc"
    ),
    DEVICE_IDENTITY_REGISTRY(
        "com.nerae.secure-vault.device-identity-registry.aes-gcm.v1",
        "nerae-device-identity-registry.v1.enc"
    ),
    TRAINING_OPERATION_JOURNAL(
        "com.nerae.secure-vault.training-operation-journal.aes-gcm.v1",
        "nerae-training-operation-journal.v1.enc"
    )
}
private const val AES_TRANSFORMATION = "AES/GCM/NoPadding"
private const val KEY_SIZE_BITS = 256
private const val GCM_TAG_BITS = 128
private const val GCM_NONCE_BYTES = 12
private const val VERSION: Byte = 1
private const val VERSIONED_TRAINING_JOURNAL_FILE = "nerae-training-operation-journal.v2.enc"
private val MAGIC = byteArrayOf(0x4e, 0x52, 0x56, 0x41) // NRVA
private val AAD = MAGIC + byteArrayOf(VERSION)

internal class VaultFailure(val code: String = "unavailable") : Exception()

internal data class VaultReadResult(val found: Boolean, val value: String) {
    override fun toString(): String = "VaultReadResult{redacted}"
}

internal interface VaultStorage {
    fun read(): ByteArray?
    fun writeAtomically(value: ByteArray)
    fun clear()
}

internal interface VaultKeys {
    /** Returns null only when the dedicated alias is absent. */
    fun existing(): SecretKey?
    fun createHardware(): SecretKey
    fun delete()
}

internal fun interface VaultPayloadValidator {
	fun isJsonObject(value: String): Boolean
}

internal object AndroidJsonObjectValidator : VaultPayloadValidator {
	override fun isJsonObject(value: String): Boolean = try {
		JsonReader(StringReader(value)).use { reader ->
			reader.isLenient = false
			if (reader.peek() != JsonToken.BEGIN_OBJECT) return false
			reader.beginObject()
			while (reader.hasNext()) {
				reader.nextName()
				consumeJsonValue(reader, 1)
			}
			reader.endObject()
			reader.peek() == JsonToken.END_DOCUMENT
		}
	} catch (_: Exception) {
		false
	}

	private fun consumeJsonValue(reader: JsonReader, depth: Int) {
		ensureJsonDepth(depth)
		when (reader.peek()) {
			JsonToken.BEGIN_OBJECT -> {
				reader.beginObject()
				while (reader.hasNext()) {
					reader.nextName()
					consumeJsonValue(reader, depth + 1)
				}
				reader.endObject()
			}
			JsonToken.BEGIN_ARRAY -> {
				reader.beginArray()
				while (reader.hasNext()) consumeJsonValue(reader, depth + 1)
				reader.endArray()
			}
			JsonToken.STRING -> reader.nextString()
			JsonToken.NUMBER -> reader.nextString()
			JsonToken.BOOLEAN -> reader.nextBoolean()
			JsonToken.NULL -> reader.nextNull()
			else -> throw IllegalArgumentException("invalid JSON")
		}
	}
}

internal fun ensureJsonDepth(depth: Int) {
	if (depth !in 0..64) throw IllegalArgumentException("JSON nesting too deep")
}

/** Cryptographic payload logic is injectable only for deterministic JVM tests. */
internal class NeraeCredentialVault(
    private val storage: VaultStorage,
    private val keys: VaultKeys,
	private val processLock: Any = Any(),
	private val payloadValidator: VaultPayloadValidator
) {
    companion object {
        const val MAX_PLAINTEXT_BYTES = 64 * 1024
        const val MAX_ENVELOPE_BYTES = MAX_PLAINTEXT_BYTES + 4 + 1 + GCM_NONCE_BYTES + 16
        private const val MAX_READ_CHUNK = 4096
    }

    fun read(): VaultReadResult = synchronized(processLock) {
        try {
            val envelope = storage.read() ?: run {
                // A key without ciphertext can be left by an interrupted first
                // write or clear. It is ambiguous state, not an empty vault.
                if (keys.existing() != null) throw VaultFailure()
                return@synchronized VaultReadResult(false, "")
            }
            val key = keys.existing() ?: throw VaultFailure()
            val plaintext = decrypt(envelope, key)
            val value = validateStoredJson(plaintext)
            VaultReadResult(true, value)
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    fun write(value: String) = synchronized(processLock) {
        val plaintext = validateInput(value)
        writePlaintext(plaintext)
    }

    /** Atomic within the supplied process-level lock, not across processes. */
    fun compareAndSet(expectedFound: Boolean, expectedValue: String, value: String) = synchronized(processLock) {
        val plaintext = validateInput(value)
        val expected = if (expectedFound) {
            validateInput(expectedValue)
        } else {
            if (expectedValue.isNotEmpty()) throw VaultFailure("invalid_state")
            null
        }
        var createdKey = false
        try {
            val old = storage.read()
            val key = if (old == null) {
                // A leftover key without its ciphertext is ambiguous after a
                // crash or interrupted clear; never silently reuse or replace it.
                if (keys.existing() != null) throw VaultFailure()
                if (expected != null) throw VaultFailure("conflict")
                keys.createHardware().also { createdKey = true }
            } else {
                val existingKey = keys.existing() ?: throw VaultFailure()
                val storedPlaintext = decrypt(old, existingKey)
                validateStoredJson(storedPlaintext)
                if (expected == null || !storedPlaintext.contentEquals(expected)) {
                    throw VaultFailure("conflict")
                }
                existingKey
            }
            storage.writeAtomically(encrypt(plaintext, key))
        } catch (failure: VaultFailure) {
            if (createdKey) discardNewKeyOnlyIfStorageIsStillEmpty()
            throw failure
        } catch (_: Exception) {
            if (createdKey) discardNewKeyOnlyIfStorageIsStillEmpty()
            throw VaultFailure()
        }
    }

    private fun discardNewKeyOnlyIfStorageIsStillEmpty() {
        try {
            // A failed atomic write can have an uncertain outcome. Delete the
            // key only when storage proves no ciphertext was committed.
            if (storage.read() == null) keys.delete()
        } catch (_: Exception) {
            // Preserve potentially committed ciphertext/key state on uncertainty.
        }
    }

    private fun writePlaintext(plaintext: ByteArray) {
        try {
            val old = storage.read()
            val key = if (old == null) {
                // A leftover key without its ciphertext is ambiguous after a
                // crash or interrupted clear; never silently reuse or replace it.
                if (keys.existing() != null) throw VaultFailure()
                keys.createHardware()
            } else {
                val existingKey = keys.existing() ?: throw VaultFailure()
                validateStoredJson(decrypt(old, existingKey))
                existingKey
            }
            storage.writeAtomically(encrypt(plaintext, key))
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    fun clear() = synchronized(processLock) {
        try {
            storage.clear()
            // This alias is exclusively the vault AES key. The device-signing
            // bridge uses another namespace and is never queried here.
            keys.delete()
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    private fun encrypt(plaintext: ByteArray, key: SecretKey): ByteArray {
        val cipher = Cipher.getInstance(AES_TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key)
        val nonce = cipher.iv ?: throw VaultFailure()
        if (nonce.size != GCM_NONCE_BYTES) throw VaultFailure()
        cipher.updateAAD(AAD)
        val ciphertext = cipher.doFinal(plaintext)
        val output = ByteArray(AAD.size + nonce.size + ciphertext.size)
        System.arraycopy(AAD, 0, output, 0, AAD.size)
        System.arraycopy(nonce, 0, output, AAD.size, nonce.size)
        System.arraycopy(ciphertext, 0, output, AAD.size + nonce.size, ciphertext.size)
        if (output.size > MAX_ENVELOPE_BYTES) throw VaultFailure()
        return output
    }

    private fun decrypt(envelope: ByteArray, key: SecretKey): ByteArray {
        if (envelope.size !in (AAD.size + GCM_NONCE_BYTES + 16)..MAX_ENVELOPE_BYTES ||
            !envelope.copyOfRange(0, AAD.size).contentEquals(AAD)) throw VaultFailure()
        val nonce = envelope.copyOfRange(AAD.size, AAD.size + GCM_NONCE_BYTES)
        val cipher = Cipher.getInstance(AES_TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(GCM_TAG_BITS, nonce))
        cipher.updateAAD(AAD)
        return cipher.doFinal(envelope, AAD.size + GCM_NONCE_BYTES,
            envelope.size - AAD.size - GCM_NONCE_BYTES)
    }

    private fun validateInput(value: String): ByteArray {
        if (value.isEmpty() || value.length > MAX_PLAINTEXT_BYTES) throw VaultFailure("invalid_state")
        try {
            val encoded = StandardCharsets.UTF_8.newEncoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .encode(CharBuffer.wrap(value))
            if (encoded.remaining() !in 1..MAX_PLAINTEXT_BYTES) throw VaultFailure("invalid_state")
            val bytes = ByteArray(encoded.remaining())
            encoded.get(bytes)
			if (!payloadValidator.isJsonObject(String(bytes, StandardCharsets.UTF_8))) throw VaultFailure("invalid_state")
            return bytes
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure("invalid_state")
        }
    }

    private fun validateStoredJson(value: ByteArray): String {
        if (value.isEmpty() || value.size > MAX_PLAINTEXT_BYTES) throw VaultFailure()
        try {
            val decoded = StandardCharsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .decode(ByteBuffer.wrap(value)).toString()
			if (!payloadValidator.isJsonObject(decoded)) throw VaultFailure()
            return decoded
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }
}

/**
 * Revisioned storage for the training journal. Revision and found state are in
 * the authenticated v2 envelope header, keeping the payload's existing 64 KiB
 * JSON-object limit and making legacy v1 JSON unambiguous.
 */
internal data class VersionedJournalResult(
    val found: Boolean,
    val value: String,
    val revision: Long
) {
    override fun toString(): String = "VersionedJournalResult{redacted}"
}

internal class NeraeVersionedTrainingJournal(
    private val legacyStorage: VaultStorage,
    private val versionedStorage: VaultStorage,
    private val keys: VaultKeys,
    private val processLock: Any,
    private val payloadValidator: VaultPayloadValidator
) {
    companion object {
        private const val LEGACY_VERSION: Byte = 1
        private const val VERSIONED_VERSION_V2: Byte = 2
        private const val VERSIONED_VERSION_V3: Byte = 3
        private const val LEGACY_HEADER_BYTES = 5
        private const val LEGACY_DIGEST_BYTES = 32
        private const val VERSIONED_HEADER_BYTES = 5 + Long.SIZE_BYTES + 2 + LEGACY_DIGEST_BYTES
        private const val MAX_VALUE_BYTES = 64 * 1024
        private const val MAX_DISPATCH_MARKERS = 8192
        private const val MAX_ACTIVE_DISPATCHES = 64
        private const val MARKER_BYTES = 32
        private const val MAX_HISTORY_BYTES = (MAX_DISPATCH_MARKERS * MARKER_BYTES) + (MAX_ACTIVE_DISPATCHES * MARKER_BYTES * 2) + 4096
        internal const val MAX_ENVELOPE_BYTES = VERSIONED_HEADER_BYTES + GCM_NONCE_BYTES + MAX_VALUE_BYTES + MAX_HISTORY_BYTES + 16
    }

    fun read(): VersionedJournalResult = synchronized(processLock) {
        try {
            val state = readCurrentLocked()
            VersionedJournalResult(state.found, state.value, state.revision)
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    /** Legacy API adapter: still reads the same v2 envelope and never raw-writes. */
    fun readLegacy(): VersionedJournalResult = synchronized(processLock) {
        val state = readCurrentLocked()
        VersionedJournalResult(state.found, state.value, state.revision)
    }

    /** Legacy CAS adapter. Its value comparison is retained; successful writes still advance revision. */
    fun compareAndSetLegacy(expectedFound: Boolean, expectedValue: String, value: String): Long = synchronized(processLock) {
        validateExpected(expectedFound, expectedValue)
        validateValue(value)
        val current = readCurrentLocked()
        if (current.found != expectedFound || (expectedFound && current.value != expectedValue)) {
            throw VaultFailure("conflict")
        }
        if (!FirstDispatchNativeParser.preservesActiveOperations(current.value, value, current.activeBindings, current.dispatchMarkers)) {
            throw VaultFailure("invalid_state")
        }
        replaceLocked(current, found = true, value = value)
    }

    /** Atomic first-send mark; no caller-provided metadata is accepted apart from stored bytes and target key. */
    fun claimFirstSend(expectedRevision: String, expectedValue: String, operationKey: String, nextValue: String): Long = synchronized(processLock) {
        val expected = parseRevision(expectedRevision)
        validateValue(expectedValue)
        validateValue(nextValue)
        val identity = try { FirstDispatchNativeParser.derive(expectedValue, nextValue, operationKey) }
        catch (_: Exception) { throw VaultFailure("invalid_state") }
        val current = readCurrentLocked()
        if (!current.dispatchHistoryKnown) throw VaultFailure("unavailable")
        if (current.revision != expected || !current.found || !current.value.toByteArray(StandardCharsets.UTF_8).contentEquals(expectedValue.toByteArray(StandardCharsets.UTF_8))) {
            throw VaultFailure("conflict")
        }
        if (!FirstDispatchNativeParser.preservesActiveOperations(current.value, nextValue, current.activeBindings, current.dispatchMarkers)) {
            throw VaultFailure("unavailable")
        }
        if (identity.eventMarker in current.activeBindings || identity.keyMarker in current.dispatchMarkers ||
            identity.bodyMarker in current.dispatchMarkers) throw VaultFailure("conflict")
        if (current.dispatchMarkers.size > MAX_DISPATCH_MARKERS - 2 || current.activeBindings.size >= MAX_ACTIVE_DISPATCHES) {
            throw VaultFailure("unavailable")
        }
        replaceLocked(current, true, nextValue,
            current.dispatchMarkers + setOf(identity.keyMarker, identity.bodyMarker),
            current.activeBindings + (identity.eventMarker to identity.binding))
    }

    fun compareAndSet(
        expectedRevision: String,
        expectedFound: Boolean,
        expectedValue: String,
        newFound: Boolean,
        newValue: String
    ): Long = synchronized(processLock) {
        val expected = parseRevision(expectedRevision)
        validateExpected(expectedFound, expectedValue)
        if (newFound) validateValue(newValue) else if (newValue.isNotEmpty()) throw VaultFailure("invalid_state")
        val current = readCurrentLocked()
        if (current.revision != expected || current.found != expectedFound ||
            (expectedFound && current.value != expectedValue)) throw VaultFailure("conflict")
        if (!FirstDispatchNativeParser.preservesActiveOperations(current.value, if (newFound) newValue else "", current.activeBindings, current.dispatchMarkers)) {
            throw VaultFailure("invalid_state")
        }
        replaceLocked(current, newFound, newValue)
    }

    private data class State(
        val revision: Long,
        val found: Boolean,
        val value: String,
        val legacy: Boolean,
        val legacyFingerprint: ByteArray?,
        val dispatchHistoryKnown: Boolean,
        val dispatchMarkers: Set<String>,
        val activeBindings: Map<String, String>
    )

    private fun readCurrentLocked(): State {
        val state = readLocked()
        if (!state.legacy) return state
        try {
            val key = keys.existing() ?: throw VaultFailure()
            val fingerprint = state.legacyFingerprint ?: throw VaultFailure()
            if (versionedStorage.read() != null) throw VaultFailure()
            val migratedState = state.copy(dispatchHistoryKnown = false, dispatchMarkers = emptySet(), activeBindings = emptyMap())
            versionedStorage.writeAtomically(encryptValue(
                encodeJournalEnvelope(migratedState), 1L, true, fingerprint, key, VERSIONED_VERSION_V3
            ))
            val migrated = readLocked()
            if (migrated.legacy || migrated.revision != 1L || !migrated.found || migrated.value != state.value ||
                migrated.legacyFingerprint?.contentEquals(fingerprint) != true) {
                throw VaultFailure()
            }
            return migrated
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    private fun readLocked(): State {
        val versioned = versionedStorage.read()
        val legacy = legacyStorage.read()
        if (versioned == null) {
            if (legacy == null) {
                if (keys.existing() != null) throw VaultFailure()
                return State(0, false, "", false, null, true, emptySet(), emptyMap())
            }
            if (legacy.size !in (LEGACY_HEADER_BYTES + GCM_NONCE_BYTES + 16)..MAX_ENVELOPE_BYTES ||
                !legacy.copyOfRange(0, MAGIC.size).contentEquals(MAGIC) || legacy[MAGIC.size] != LEGACY_VERSION) throw VaultFailure()
            val legacyPlaintext = decrypt(legacy, LEGACY_HEADER_BYTES, legacyHeader())
            return State(0, true, decodeValue(legacyPlaintext), true, MessageDigest.getInstance("SHA-256").digest(legacy), false, emptySet(), emptyMap())
        }
        if (versioned.size !in (VERSIONED_HEADER_BYTES + GCM_NONCE_BYTES + 16)..MAX_ENVELOPE_BYTES ||
            !versioned.copyOfRange(0, MAGIC.size).contentEquals(MAGIC) ||
            versioned[MAGIC.size] !in setOf(VERSIONED_VERSION_V2, VERSIONED_VERSION_V3)) throw VaultFailure()
        val envelopeVersion = versioned[MAGIC.size]
        val header = versioned.copyOfRange(0, VERSIONED_HEADER_BYTES)
        val revision = ByteBuffer.wrap(versioned, MAGIC.size + 1, Long.SIZE_BYTES).long
        val foundOffset = MAGIC.size + 1 + Long.SIZE_BYTES
        val foundByte = versioned[foundOffset]
        val digestFlag = versioned[foundOffset + 1]
        val digest = versioned.copyOfRange(foundOffset + 2, VERSIONED_HEADER_BYTES)
        if (revision < 1 || (foundByte != 0.toByte() && foundByte != 1.toByte()) ||
            (digestFlag != 0.toByte() && digestFlag != 1.toByte()) ||
            (digestFlag == 0.toByte() && digest.any { it != 0.toByte() })) throw VaultFailure()
        val legacyFingerprint = if (digestFlag == 1.toByte()) digest else null
        when {
            legacyFingerprint == null && legacy != null -> throw VaultFailure()
            legacyFingerprint != null && (legacy == null || !MessageDigest.isEqual(
                legacyFingerprint, MessageDigest.getInstance("SHA-256").digest(legacy)
            )) -> throw VaultFailure()
        }
        val plaintext = decrypt(versioned, VERSIONED_HEADER_BYTES, header)
        val found = foundByte == 1.toByte()
        if (envelopeVersion == VERSIONED_VERSION_V2) {
            if (!found && plaintext.isNotEmpty()) throw VaultFailure()
            val value = if (found) decodeValue(plaintext) else ""
            return State(revision, found, value, false, legacyFingerprint, false, emptySet(), emptyMap())
        }
        return decodeJournalEnvelope(plaintext, revision, found, legacyFingerprint)
    }

    private fun replaceLocked(
        current: State, found: Boolean, value: String,
        dispatchMarkers: Set<String> = current.dispatchMarkers,
        activeBindings: Map<String, String> = current.activeBindings
    ): Long {
        if (current.revision == Long.MAX_VALUE) throw VaultFailure("invalid_state")
        val targetRevision = current.revision + 1
        val key: SecretKey
        var createdKey = false
        try {
            key = keys.existing() ?: run {
                if (versionedStorage.read() != null || legacyStorage.read() != null) throw VaultFailure()
                keys.createHardware().also { createdKey = true }
            }
            val nextState = current.copy(revision = targetRevision, found = found, value = if (found) value else "",
                legacy = false, dispatchMarkers = dispatchMarkers, activeBindings = activeBindings)
            versionedStorage.writeAtomically(encryptValue(
                encodeJournalEnvelope(nextState), targetRevision, found, current.legacyFingerprint, key, VERSIONED_VERSION_V3
            ))
            val committed = readLocked()
            if (committed.revision != targetRevision || committed.found != found || committed.value != (if (found) value else "") ||
                committed.dispatchHistoryKnown != current.dispatchHistoryKnown || committed.dispatchMarkers != dispatchMarkers ||
                committed.activeBindings != activeBindings || !sameFingerprint(committed.legacyFingerprint, current.legacyFingerprint)) throw VaultFailure()
            return targetRevision
        } catch (failure: VaultFailure) {
            if (createdKey) discardKeyOnlyIfEmpty()
            throw failure
        } catch (_: Exception) {
            if (createdKey) discardKeyOnlyIfEmpty()
            throw VaultFailure()
        }
    }

    private fun encodeJournalEnvelope(state: State): ByteArray {
        if (state.dispatchMarkers.size > MAX_DISPATCH_MARKERS || state.activeBindings.size > MAX_ACTIVE_DISPATCHES ||
            state.dispatchMarkers.any { !isMarker(it) } || state.activeBindings.any { !isMarker(it.key) || !isMarker(it.value) }) throw VaultFailure("invalid_state")
        val output = ByteArrayOutputStream()
        DataOutputStream(output).use { data ->
            data.write(byteArrayOf(0x4e, 0x4a, 0x33, 0x00)) // NJ3\0
            data.writeByte(if (state.dispatchHistoryKnown) 1 else 0)
            val valueBytes = if (state.found) state.value.toByteArray(StandardCharsets.UTF_8) else ByteArray(0)
            data.writeInt(valueBytes.size); data.write(valueBytes)
            val markers = state.dispatchMarkers.sorted()
            data.writeInt(markers.size)
            markers.forEach { data.write(hexToBytes(it)) }
            val bindings = state.activeBindings.toSortedMap()
            data.writeInt(bindings.size)
            bindings.forEach { (event, binding) -> data.write(hexToBytes(event)); data.write(hexToBytes(binding)) }
        }
        val encoded = output.toByteArray()
        if (encoded.size > MAX_VALUE_BYTES + MAX_HISTORY_BYTES) throw VaultFailure("invalid_state")
        return encoded
    }

    private fun decodeJournalEnvelope(bytes: ByteArray, revision: Long, found: Boolean, fingerprint: ByteArray?): State {
        try {
            DataInputStream(ByteArrayInputStream(bytes)).use { data ->
                if (!ByteArray(4).also(data::readFully).contentEquals(byteArrayOf(0x4e, 0x4a, 0x33, 0x00))) throw VaultFailure()
                val historyFlag = data.readUnsignedByte()
                if (historyFlag !in 0..1) throw VaultFailure()
                val valueSize = data.readInt()
                if (valueSize !in 0..MAX_VALUE_BYTES || (!found && valueSize != 0)) throw VaultFailure()
                val valueBytes = ByteArray(valueSize); data.readFully(valueBytes)
                val value = if (found) decodeValue(valueBytes) else ""
                val markerCount = data.readInt()
                if (markerCount !in 0..MAX_DISPATCH_MARKERS) throw VaultFailure()
                val markers = LinkedHashSet<String>()
                repeat(markerCount) {
                    val marker = bytesToHex(ByteArray(MARKER_BYTES).also(data::readFully))
                    if (!markers.add(marker)) throw VaultFailure()
                }
                val activeCount = data.readInt()
                if (activeCount !in 0..MAX_ACTIVE_DISPATCHES) throw VaultFailure()
                val active = LinkedHashMap<String, String>()
                repeat(activeCount) {
                    val event = bytesToHex(ByteArray(MARKER_BYTES).also(data::readFully))
                    val binding = bytesToHex(ByteArray(MARKER_BYTES).also(data::readFully))
                    if (active.put(event, binding) != null) throw VaultFailure()
                }
                if (data.read() != -1 || (historyFlag == 0 && (markers.isNotEmpty() || active.isNotEmpty()))) throw VaultFailure()
                return State(revision, found, value, false, fingerprint, historyFlag == 1, markers, active)
            }
        } catch (failure: VaultFailure) { throw failure }
        catch (_: Exception) { throw VaultFailure() }
    }

    private fun isMarker(value: String): Boolean = value.matches(Regex("[0-9a-f]{64}"))
    private fun hexToBytes(value: String): ByteArray {
        if (!isMarker(value)) throw VaultFailure("invalid_state")
        return ByteArray(MARKER_BYTES) { index -> value.substring(index * 2, index * 2 + 2).toInt(16).toByte() }
    }
    private fun bytesToHex(value: ByteArray): String = value.joinToString("") { "%02x".format(it) }

    private fun discardKeyOnlyIfEmpty() {
        try {
            if (versionedStorage.read() == null && legacyStorage.read() == null) keys.delete()
        } catch (_: Exception) { }
    }

    private fun sameFingerprint(left: ByteArray?, right: ByteArray?): Boolean = when {
        left == null -> right == null
        right == null -> false
        else -> MessageDigest.isEqual(left, right)
    }

    private fun validateExpected(found: Boolean, value: String) {
        if (found) validateValue(value) else if (value.isNotEmpty()) throw VaultFailure("invalid_state")
    }

    private fun validateValue(value: String) {
        if (value.isEmpty() || value.length > MAX_VALUE_BYTES) throw VaultFailure("invalid_state")
        try {
            val encoded = StandardCharsets.UTF_8.newEncoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .encode(CharBuffer.wrap(value))
            if (encoded.remaining() !in 1..MAX_VALUE_BYTES) throw VaultFailure("invalid_state")
            val bytes = ByteArray(encoded.remaining())
            encoded.get(bytes)
            if (!payloadValidator.isJsonObject(String(bytes, StandardCharsets.UTF_8))) throw VaultFailure("invalid_state")
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure("invalid_state")
        }
    }

    private fun decodeValue(bytes: ByteArray): String {
        if (bytes.isEmpty() || bytes.size > MAX_VALUE_BYTES) throw VaultFailure()
        val value = try {
            StandardCharsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .decode(ByteBuffer.wrap(bytes)).toString()
        } catch (_: Exception) { throw VaultFailure() }
        if (!payloadValidator.isJsonObject(value)) throw VaultFailure()
        return value
    }

    private fun parseRevision(value: String): Long {
        if (!Regex("0|[1-9][0-9]{0,18}").matches(value)) throw VaultFailure("invalid_state")
        return value.toLongOrNull() ?: throw VaultFailure("invalid_state")
    }

    private fun legacyHeader(): ByteArray = MAGIC + byteArrayOf(LEGACY_VERSION)

    private fun versionedHeader(revision: Long, found: Boolean, legacyFingerprint: ByteArray?, version: Byte = VERSIONED_VERSION_V3): ByteArray {
        if (legacyFingerprint != null && legacyFingerprint.size != LEGACY_DIGEST_BYTES) throw VaultFailure()
        return ByteBuffer
        .allocate(VERSIONED_HEADER_BYTES)
        .put(MAGIC)
        .put(version)
        .putLong(revision)
        .put(if (found) 1 else 0)
        .put(if (legacyFingerprint == null) 0 else 1)
        .put(legacyFingerprint ?: ByteArray(LEGACY_DIGEST_BYTES))
        .array()
    }

    private fun encryptValue(
        plaintext: ByteArray, revision: Long, found: Boolean, legacyFingerprint: ByteArray?, key: SecretKey,
        version: Byte = VERSIONED_VERSION_V3
    ): ByteArray {
        val header = versionedHeader(revision, found, legacyFingerprint, version)
        val cipher = Cipher.getInstance(AES_TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key)
        val nonce = cipher.iv ?: throw VaultFailure()
        if (nonce.size != GCM_NONCE_BYTES) throw VaultFailure()
        cipher.updateAAD(header)
        val ciphertext = cipher.doFinal(plaintext)
        val output = header + nonce + ciphertext
        if (output.size > MAX_ENVELOPE_BYTES) throw VaultFailure("invalid_state")
        return output
    }

    private fun decrypt(envelope: ByteArray, headerBytes: Int, header: ByteArray): ByteArray {
        if (envelope.size !in (headerBytes + GCM_NONCE_BYTES + 16)..MAX_ENVELOPE_BYTES ||
            !envelope.copyOfRange(0, headerBytes).contentEquals(header)) throw VaultFailure()
        val nonce = envelope.copyOfRange(headerBytes, headerBytes + GCM_NONCE_BYTES)
        val cipher = Cipher.getInstance(AES_TRANSFORMATION)
        val key = keys.existing() ?: throw VaultFailure()
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(GCM_TAG_BITS, nonce))
        cipher.updateAAD(header)
        return cipher.doFinal(envelope, headerBytes + GCM_NONCE_BYTES,
            envelope.size - headerBytes - GCM_NONCE_BYTES)
    }
}

internal class AndroidVaultStorage(
    context: Context,
    slot: NeraeVaultSlot,
    fileNameOverride: String? = null,
    directoryNameOverride: String? = null
) : VaultStorage {
    private val fileName = fileNameOverride ?: slot.fileName
    private val appContext: Context
    private val directory: File
    private val baseFile: File
    private val newFile: File
    private val legacyBackupFile: File
    private val atomicFile: AtomicFile

    init {
        try {
            appContext = context.applicationContext
            ensureAndroidVaultUserUnlocked(appContext)
            val noBackupDirectory = appContext.noBackupFilesDir.canonicalFile
            if (!noBackupDirectory.isDirectory || !noBackupDirectory.canRead() || !noBackupDirectory.canWrite()) throw VaultFailure()
            directory = if (directoryNameOverride == null) noBackupDirectory else {
                if (!directoryNameOverride.matches(Regex("^[a-z0-9_.-]{1,80}$"))) throw VaultFailure()
                val candidate = File(noBackupDirectory, directoryNameOverride)
                if (candidate.exists()) {
                    if (!candidate.isDirectory || candidate.canonicalFile != candidate.absoluteFile) throw VaultFailure()
                } else if (!candidate.mkdir()) {
                    throw VaultFailure()
                }
                candidate.canonicalFile
            }
            if (directory != noBackupDirectory && directory.parentFile?.canonicalFile != noBackupDirectory) throw VaultFailure()
            if (!directory.isDirectory || !directory.canRead() || !directory.canWrite()) throw VaultFailure()
            baseFile = File(directory, fileName)
            newFile = File(directory, "$fileName.new")
            legacyBackupFile = File(directory, "$fileName.bak")
            validateAtomicPaths()
            atomicFile = AtomicFile(baseFile)
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    override fun read(): ByteArray? {
        ensureDirectory()
        // AtomicFile.openRead may restore or remove recovery artifacts. Preserve
        // ambiguous state for explicit recovery instead of mutating it on read.
        if (hasRecoveryArtifact()) throw VaultFailure()
        val stream = try {
            atomicFile.openRead()
        } catch (_: FileNotFoundException) {
            if (hasAnyAtomicArtifact() || !directory.canRead() || !directory.canWrite()) throw VaultFailure()
            return null
        } catch (_: Exception) {
            throw VaultFailure()
        }
        try {
            stream.use { input ->
                if (hasRecoveryArtifact()) throw VaultFailure()
                return readBounded(input)
            }
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    override fun writeAtomically(value: ByteArray) {
        ensureDirectory()
        if (value.size > maxEnvelopeBytes()) throw VaultFailure()
        var output: FileOutputStream? = null
        try {
            output = atomicFile.startWrite()
            output.write(value)
            output.fd.sync()
            atomicFile.finishWrite(output)
            output = null
        } catch (_: Exception) {
            output?.let { try { atomicFile.failWrite(it) } catch (_: Exception) { } }
            throw VaultFailure()
        }
    }

    override fun clear() {
        ensureDirectory()
        try {
            // These are exactly the base, current `.new`, and legacy `.bak`
            // names used by platform AtomicFile. Never sweep the directory.
            atomicFile.delete()
            if (hasAnyAtomicArtifact()) throw VaultFailure()
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    private fun ensureDirectory() {
        ensureAndroidVaultUserUnlocked(appContext)
        if (!directory.isDirectory || !directory.canRead() || !directory.canWrite() ||
            !validateAtomicPaths()) {
            throw VaultFailure()
        }
    }

    private fun validateAtomicPaths(): Boolean = listOf(baseFile, newFile, legacyBackupFile).all { file ->
        !file.exists() || (file.isFile && file.canonicalFile == file.absoluteFile)
    }

    private fun hasRecoveryArtifact(): Boolean = newFile.exists() || legacyBackupFile.exists()

    private fun hasAnyAtomicArtifact(): Boolean = baseFile.exists() || hasRecoveryArtifact()

    private fun readBounded(input: FileInputStream): ByteArray {
        val out = ByteArrayOutputStream()
        val buffer = ByteArray(4096)
        val maximum = maxEnvelopeBytes()
        var total = 0
        while (true) {
            val count = input.read(buffer, 0, minOf(buffer.size, maximum + 1 - total))
            if (count < 0) break
            total += count
            if (total > maximum) throw VaultFailure()
            out.write(buffer, 0, count)
        }
        return out.toByteArray()
    }

    private fun maxEnvelopeBytes(): Int = maxOf(
        NeraeCredentialVault.MAX_ENVELOPE_BYTES,
        NeraeVersionedTrainingJournal.MAX_ENVELOPE_BYTES
    )
}

/** Shared by fixed-slot vault reads and draft inventory, including empty inventories. */
internal fun ensureAndroidVaultUserUnlocked(context: Context) {
    try {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            val manager = context.getSystemService(Context.USER_SERVICE) as? UserManager ?: throw VaultFailure()
            if (!manager.isUserUnlocked) throw VaultFailure()
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val keyguard = context.getSystemService(Context.KEYGUARD_SERVICE) as? KeyguardManager ?: throw VaultFailure()
            if (keyguard.isDeviceLocked) throw VaultFailure()
        }
    } catch (failure: VaultFailure) {
        throw failure
    } catch (_: Exception) {
        throw VaultFailure()
    }
}

internal class AndroidVaultKeys(private val keyAlias: String) : VaultKeys {
    constructor(slot: NeraeVaultSlot) : this(slot.keyAlias)

    init {
        if (keyAlias !in NeraeVaultSlot.entries.map { it.keyAlias } &&
            !keyAlias.matches(Regex("^com\\.nerae\\.secure-vault\\.training-draft\\.aes-gcm\\.v1\\.[a-f0-9]{64}$"))) {
            throw VaultFailure("invalid_state")
        }
    }

    override fun existing(): SecretKey? {
        try {
            val store = loadStore()
            if (!store.containsAlias(keyAlias)) return null
            val key = store.getKey(keyAlias, null) as? SecretKey ?: throw VaultFailure()
            verifyHardware(key)
            return key
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    override fun createHardware(): SecretKey {
        try {
            if (loadStore().containsAlias(keyAlias)) throw VaultFailure()
            val key = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                try {
                    generateKey(true)
                } catch (_: StrongBoxUnavailableException) {
                    if (loadStore().containsAlias(keyAlias)) throw VaultFailure()
                    generateKey(false) // TEE may still be available; verify below.
                }
            } else {
                generateKey(false)
            }
            try {
                verifyHardware(key)
            } catch (_: Exception) {
                // This exact alias was created by this call. Remove an
                // unverifiable/software key; never fall back to it.
                try { loadStore().deleteEntry(keyAlias) } catch (_: Exception) { }
                throw VaultFailure()
            }
            return key
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    override fun delete() {
        try {
            val store = loadStore()
            if (store.containsAlias(keyAlias)) store.deleteEntry(keyAlias)
            if (store.containsAlias(keyAlias)) throw VaultFailure()
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    private fun generateKey(strongBox: Boolean): SecretKey {
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        val builder = KeyGenParameterSpec.Builder(keyAlias,
            KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setKeySize(KEY_SIZE_BITS)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setRandomizedEncryptionRequired(true)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) builder.setUnlockedDeviceRequired(true)
        if (strongBox) builder.setIsStrongBoxBacked(true)
        generator.init(builder.build())
        return generator.generateKey()
    }

    private fun verifyHardware(key: SecretKey) {
        try {
            if (key.algorithm != "AES" || key.encoded != null) throw VaultFailure()
            val factory = SecretKeyFactory.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
            val info = factory.getKeySpec(key, KeyInfo::class.java) as KeyInfo
            val hardware = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                info.securityLevel == KeyProperties.SECURITY_LEVEL_TRUSTED_ENVIRONMENT ||
                    info.securityLevel == KeyProperties.SECURITY_LEVEL_STRONGBOX
            } else {
                info.isInsideSecureHardware
            }
            if (!hardware) throw VaultFailure()
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    private fun loadStore(): KeyStore = KeyStore.getInstance(KEYSTORE).apply { load(null) }
}

@Suppress("unused")
object NeraeAndroidSecureVaultBridge {
    @JvmStatic
    fun readAuthenticationState(context: Context?): String = withVault(context) { vault ->
        val result = vault.read()
        encode(ok = true, found = result.found, value = result.value)
    }

    @JvmStatic
    fun writeAuthenticationState(context: Context?, value: String?): String = withVault(context) { vault ->
        if (value == null) throw VaultFailure("invalid_state")
        vault.write(value)
        encode(ok = true)
    }

    @JvmStatic
    fun clearAuthenticationState(context: Context?): String = withSlot(
        context, NeraeVaultSlot.AUTHENTICATION_STATE, authenticationProcessLock
    ) { vault ->
        vault.clear()
        encode(ok = true)
    }

    @JvmStatic
    fun readDeviceIdentityRegistry(context: Context?): String = withSlot(
        context, NeraeVaultSlot.DEVICE_IDENTITY_REGISTRY, registryProcessLock
    ) { vault ->
        val result = vault.read()
        encode(ok = true, found = result.found, value = result.value)
    }

    @JvmStatic
    fun compareAndSetDeviceIdentityRegistry(
        context: Context?, expectedFound: Boolean, expectedValue: String?, value: String?
    ): String = withSlot(
        context, NeraeVaultSlot.DEVICE_IDENTITY_REGISTRY, registryProcessLock
    ) { vault ->
        if (expectedValue == null || value == null) throw VaultFailure("invalid_state")
        vault.compareAndSet(expectedFound, expectedValue, value)
        encode(ok = true)
    }

    @JvmStatic
    fun readTrainingOperationJournal(context: Context?): String = withTrainingJournal(context, versionedResult = false) { journal ->
        val result = journal.readLegacy()
        encode(ok = true, found = result.found, value = result.value)
    }

    @JvmStatic
    fun compareAndSetTrainingOperationJournal(
        context: Context?, expectedFound: Boolean, expectedValue: String?, value: String?
    ): String = withTrainingJournal(context, versionedResult = false) { journal ->
        if (expectedValue == null || value == null) throw VaultFailure("invalid_state")
        journal.compareAndSetLegacy(expectedFound, expectedValue, value)
        encode(ok = true)
    }

    @JvmStatic
    fun readVersionedTrainingOperationJournal(context: Context?): String = withTrainingJournal(context) { journal ->
        val result = journal.read()
        encodeJournal(ok = true, found = result.found, value = result.value, revision = result.revision.toString())
    }

    @JvmStatic
    fun compareAndSetVersionedTrainingOperationJournal(
        context: Context?, expectedRevision: String?, expectedFound: Boolean, expectedValue: String?,
        newFound: Boolean, newValue: String?
    ): String = withTrainingJournal(context) { journal ->
        if (expectedRevision == null || expectedValue == null || newValue == null) throw VaultFailure("invalid_state")
        val revision = journal.compareAndSet(expectedRevision, expectedFound, expectedValue, newFound, newValue)
        encodeJournal(ok = true, revision = revision.toString())
    }

    /** A successful revision is a first-send permit, not a server receipt. */
    @JvmStatic
    fun claimTrainingOperationDispatch(
        context: Context?, expectedRevision: String?, expectedValue: String?, operationKey: String?, nextValue: String?
    ): String = withTrainingJournal(context) { journal ->
        claimTrainingOperationDispatch(journal, expectedRevision, expectedValue, operationKey, nextValue)
    }

    // The journal overload keeps the production mapping testable with isolated
    // synthetic storage; the public entry above always supplies the native slot.
    internal fun claimTrainingOperationDispatch(
        journal: NeraeVersionedTrainingJournal, expectedRevision: String?, expectedValue: String?,
        operationKey: String?, nextValue: String?
    ): String = try {
        if (expectedRevision.isNullOrEmpty() || expectedRevision == "0" || expectedValue.isNullOrEmpty() ||
            operationKey.isNullOrEmpty() || nextValue.isNullOrEmpty()) throw VaultFailure("invalid_state")
        val revision = journal.claimFirstSend(expectedRevision, expectedValue, operationKey, nextValue)
        encodeJournal(ok = true, revision = revision.toString())
    } catch (problem: VaultFailure) {
        when (problem.code) {
            "invalid_state", "conflict" -> journalFailure(problem.code)
            else -> journalFailure("unavailable")
        }
    } catch (_: Exception) {
        journalFailure("unavailable")
    }

    private fun withVault(context: Context?, action: (NeraeCredentialVault) -> String): String =
        withSlot(context, NeraeVaultSlot.AUTHENTICATION_STATE, authenticationProcessLock, action)

    private fun withTrainingJournal(
        context: Context?, versionedResult: Boolean = true,
        action: (NeraeVersionedTrainingJournal) -> String
    ): String {
        if (context == null) return if (versionedResult) journalFailure("unavailable") else failure("unavailable")
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return if (versionedResult) journalFailure("unavailable") else failure("unavailable")
        return try {
            val journal = NeraeVersionedTrainingJournal(
                AndroidVaultStorage(context, NeraeVaultSlot.TRAINING_OPERATION_JOURNAL),
                AndroidVaultStorage(context, NeraeVaultSlot.TRAINING_OPERATION_JOURNAL, VERSIONED_TRAINING_JOURNAL_FILE),
                AndroidVaultKeys(NeraeVaultSlot.TRAINING_OPERATION_JOURNAL),
                trainingJournalProcessLock,
                AndroidJsonObjectValidator
            )
            action(journal)
        } catch (problem: VaultFailure) {
            if (versionedResult) journalFailure(problem.code)
            else if (problem.code == "conflict") conflict() else failure(problem.code)
        } catch (_: Exception) {
            if (versionedResult) journalFailure("unavailable") else failure("unavailable")
        }
    }

    private fun withSlot(
        context: Context?,
        slot: NeraeVaultSlot,
        processLock: Any,
        action: (NeraeCredentialVault) -> String
    ): String {
        if (context == null) return failure("unavailable")
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return failure("unavailable")
        return try {
            val vault = NeraeCredentialVault(
                AndroidVaultStorage(context, slot), AndroidVaultKeys(slot), processLock,
                AndroidJsonObjectValidator
            )
            action(vault)
        } catch (problem: VaultFailure) {
            if (problem.code == "conflict") conflict() else failure(problem.code)
        } catch (_: Exception) {
            failure("unavailable")
        }
    }

    private val authenticationProcessLock = Any()
    private val registryProcessLock = Any()
    private val trainingJournalProcessLock = Any()

    private fun failure(code: String): String = JSONObject()
        .put("ok", false).put("errorCode", code).put("found", false).put("value", "").toString()

    private fun conflict(): String = JSONObject()
        .put("ok", false).put("errorCode", "conflict").put("found", false).put("value", "").toString()

    private fun journalFailure(code: String): String = JSONObject()
        .put("ok", false).put("errorCode", code).put("found", false).put("value", "").put("revision", "").toString()

    private fun encodeJournal(
        ok: Boolean, errorCode: String = "", found: Boolean = false, value: String = "", revision: String = ""
    ): String = JSONObject().put("ok", ok).put("errorCode", errorCode).put("found", found)
        .put("value", value).put("revision", revision).toString()

    private fun encode(ok: Boolean, errorCode: String = "", found: Boolean = false, value: String = ""): String =
        JSONObject().put("ok", ok).put("errorCode", errorCode).put("found", found).put("value", value).toString()
}
