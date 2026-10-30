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
import java.io.ByteArrayOutputStream
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
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec

private const val KEYSTORE = "AndroidKeyStore"
private const val VAULT_KEY_ALIAS = "com.nerae.secure-vault.authentication-state.aes-gcm.v1"
private const val FILE_NAME = "nerae-auth-state.v1.enc"
private const val AES_TRANSFORMATION = "AES/GCM/NoPadding"
private const val KEY_SIZE_BITS = 256
private const val GCM_TAG_BITS = 128
private const val GCM_NONCE_BYTES = 12
private const val VERSION: Byte = 1
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

private object AndroidJsonObjectValidator : VaultPayloadValidator {
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

private class AndroidVaultStorage(context: Context) : VaultStorage {
    private val appContext: Context
    private val directory: File
    private val baseFile: File
    private val newFile: File
    private val legacyBackupFile: File
    private val atomicFile: AtomicFile

    init {
        try {
            appContext = context.applicationContext
            ensureUserUnlocked(appContext)
            directory = appContext.noBackupFilesDir.canonicalFile
            if (!directory.isDirectory || !directory.canRead() || !directory.canWrite()) throw VaultFailure()
            baseFile = File(directory, FILE_NAME)
            newFile = File(directory, "$FILE_NAME.new")
            legacyBackupFile = File(directory, "$FILE_NAME.bak")
            validateAtomicPaths()
            atomicFile = AtomicFile(baseFile)
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    override fun read(): ByteArray? {
        ensureDirectory()
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
        if (value.size > NeraeCredentialVault.MAX_ENVELOPE_BYTES) throw VaultFailure()
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
        ensureUserUnlocked(appContext)
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

    private fun ensureUserUnlocked(context: Context) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            val manager = context.getSystemService(Context.USER_SERVICE) as? UserManager ?: throw VaultFailure()
            if (!manager.isUserUnlocked) throw VaultFailure()
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val keyguard = context.getSystemService(Context.KEYGUARD_SERVICE) as? KeyguardManager ?: throw VaultFailure()
            if (keyguard.isDeviceLocked) throw VaultFailure()
        }
    }

    private fun readBounded(input: FileInputStream): ByteArray {
        val out = ByteArrayOutputStream()
        val buffer = ByteArray(4096)
        var total = 0
        while (true) {
            val count = input.read(buffer, 0, minOf(buffer.size, NeraeCredentialVault.MAX_ENVELOPE_BYTES + 1 - total))
            if (count < 0) break
            total += count
            if (total > NeraeCredentialVault.MAX_ENVELOPE_BYTES) throw VaultFailure()
            out.write(buffer, 0, count)
        }
        return out.toByteArray()
    }
}

private class AndroidVaultKeys : VaultKeys {
    override fun existing(): SecretKey? {
        try {
            val store = loadStore()
            if (!store.containsAlias(VAULT_KEY_ALIAS)) return null
            val key = store.getKey(VAULT_KEY_ALIAS, null) as? SecretKey ?: throw VaultFailure()
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
            if (loadStore().containsAlias(VAULT_KEY_ALIAS)) throw VaultFailure()
            val key = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                try {
                    generateKey(true)
                } catch (_: StrongBoxUnavailableException) {
                    if (loadStore().containsAlias(VAULT_KEY_ALIAS)) throw VaultFailure()
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
                try { loadStore().deleteEntry(VAULT_KEY_ALIAS) } catch (_: Exception) { }
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
            if (store.containsAlias(VAULT_KEY_ALIAS)) store.deleteEntry(VAULT_KEY_ALIAS)
            if (store.containsAlias(VAULT_KEY_ALIAS)) throw VaultFailure()
        } catch (failure: VaultFailure) {
            throw failure
        } catch (_: Exception) {
            throw VaultFailure()
        }
    }

    private fun generateKey(strongBox: Boolean): SecretKey {
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        val builder = KeyGenParameterSpec.Builder(VAULT_KEY_ALIAS,
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
    fun clearAuthenticationState(context: Context?): String = withVault(context) { vault ->
        vault.clear()
        encode(ok = true)
    }

    private fun withVault(context: Context?, action: (NeraeCredentialVault) -> String): String {
        if (context == null) return failure("unavailable")
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return failure("unavailable")
        return try {
            val vault = NeraeCredentialVault(AndroidVaultStorage(context), AndroidVaultKeys(), processLock, AndroidJsonObjectValidator)
            action(vault)
        } catch (problem: VaultFailure) {
            failure(problem.code)
        } catch (_: Exception) {
            failure("unavailable")
        }
    }

    private val processLock = Any()

    private fun failure(code: String): String = JSONObject()
        .put("ok", false).put("errorCode", code).put("found", false).put("value", "").toString()

    private fun encode(ok: Boolean, errorCode: String = "", found: Boolean = false, value: String = ""): String =
        JSONObject().put("ok", ok).put("errorCode", errorCode).put("found", found).put("value", value).toString()
}
