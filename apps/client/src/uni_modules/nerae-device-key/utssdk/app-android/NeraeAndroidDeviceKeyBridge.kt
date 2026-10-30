package uts.sdk.modules.neraeDeviceKey

import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyProperties
import org.json.JSONObject
import java.math.BigInteger
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.SecureRandom
import java.security.Signature
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import android.util.Base64

/** Android Keystore implementation behind the three-operation UTS surface. */
object NeraeAndroidDeviceKeyBridge {
    private const val STORE = "AndroidKeyStore"
    private const val PREFIX = "com.nerae.device-key.v1/"
    private const val AUDIENCE = "nerae-auth-device"
    private const val PROOF_TTL_SECONDS = 60L
    private val random = SecureRandom()
    private val lock = Any()

    @JvmStatic
    fun createNewDeviceKey(): String = synchronized(lock) {
        var createdAlias: String? = null
        try {
            val alias = base64Url(ByteArray(32).also { random.nextBytes(it) })
            if (!validAlias(alias)) return@synchronized failure("operation_failed")
            val tag = PREFIX + alias
            val store = loadStore()
            if (store.containsAlias(tag)) return@synchronized failure("ambiguous_key")

            val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, STORE)
            val spec = KeyGenParameterSpec.Builder(tag, KeyProperties.PURPOSE_SIGN)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setDigests(KeyProperties.DIGEST_SHA256)
                .build()
            generator.initialize(spec)
            generator.generateKeyPair()
            createdAlias = tag

            val entry = privateEntry(loadStore(), tag)
            requireHardware(entry.privateKey)
            val jwk = publicJwk(entry.certificate.publicKey)
            success(alias, jwk, "")
        } catch (problem: DeviceKeyFailure) {
            createdAlias?.let { deleteQuietly(it) }
            failure(problem.code)
        } catch (_: Exception) {
            createdAlias?.let { deleteQuietly(it) }
            failure("hardware_unavailable")
        }
    }

    /** Creates exactly once under a caller-reserved alias; existing keys are never replaced. */
    @JvmStatic
    fun createDeviceKeyAtReservedAlias(alias: String): String = synchronized(lock) {
        try {
            if (!validAlias(alias)) return@synchronized failure("invalid_alias")
            val tag = PREFIX + alias
            val store = loadStore()
            if (store.containsAlias(tag)) return@synchronized failure("alias_conflict")

            val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, STORE)
            val spec = KeyGenParameterSpec.Builder(tag, KeyProperties.PURPOSE_SIGN)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setDigests(KeyProperties.DIGEST_SHA256)
                .build()
            generator.initialize(spec)
            generator.generateKeyPair()

            val entry = privateEntry(loadStore(), tag)
            requireHardware(entry.privateKey)
            success(alias, publicJwk(entry.certificate.publicKey), "")
        } catch (problem: DeviceKeyFailure) {
            failure(problem.code)
        } catch (_: Exception) {
            failure("hardware_unavailable")
        }
    }

    @JvmStatic
    fun readExistingDevicePublicKey(alias: String): String = synchronized(lock) {
        try {
            if (!validAlias(alias)) return@synchronized failure("invalid_alias")
            val tag = PREFIX + alias
            val store = loadStore()
            if (!store.containsAlias(tag)) return@synchronized failure("missing_key")
            val entry = privateEntry(store, tag)
            requireHardware(entry.privateKey)
            success(alias, publicJwk(entry.certificate.publicKey), "")
        } catch (problem: DeviceKeyFailure) {
            failure(problem.code)
        } catch (_: Exception) {
            failure("operation_failed")
        }
    }

    @JvmStatic
    fun signExistingDeviceProof(alias: String, purpose: String, resourceId: String, nonce: String): String = synchronized(lock) {
        try {
            if (!validAlias(alias)) return@synchronized failure("invalid_alias")
            if (!validPurpose(purpose) || !validOpaque(resourceId, 22, 128) || !validOpaque(nonce, 43, 256)) {
                return@synchronized failure("invalid_proof_input")
            }
            val tag = PREFIX + alias
            val store = loadStore()
            if (!store.containsAlias(tag)) return@synchronized failure("missing_key")
            val entry = privateEntry(store, tag)
            requireHardware(entry.privateKey)
            publicJwk(entry.certificate.publicKey)

            val now = System.currentTimeMillis() / 1000L
            if (now < 0 || now >= 100_000_000_000L - PROOF_TTL_SECONDS) {
                return@synchronized failure("invalid_proof_input")
            }
            val jtiBytes = ByteArray(32).also { random.nextBytes(it) }
            val signingInput = base64Url(HEADER.toByteArray(Charsets.UTF_8)) + "." + base64Url(
                "{\"aud\":\"$AUDIENCE\",\"purpose\":\"$purpose\",\"resourceId\":\"$resourceId\",\"nonce\":\"$nonce\",\"iat\":$now,\"exp\":${now + PROOF_TTL_SECONDS},\"jti\":\"${base64Url(jtiBytes)}\"}"
                    .toByteArray(Charsets.UTF_8)
            )
            val signer = Signature.getInstance("SHA256withECDSA")
            signer.initSign(entry.privateKey)
            signer.update(signingInput.toByteArray(Charsets.UTF_8))
            val der = signer.sign()
            val jose = derToJose(der)
            success(alias, emptyJwk(), signingInput + "." + base64Url(jose))
        } catch (problem: DeviceKeyFailure) {
            failure(problem.code)
        } catch (_: Exception) {
            failure("operation_failed")
        }
    }

    private fun loadStore(): KeyStore = KeyStore.getInstance(STORE).apply { load(null) }

    private fun privateEntry(store: KeyStore, alias: String): KeyStore.PrivateKeyEntry {
        val entry = store.getEntry(alias, null) as? KeyStore.PrivateKeyEntry
            ?: throw DeviceKeyFailure("unexpected_key")
        if (entry.privateKey.algorithm != "EC") throw DeviceKeyFailure("unexpected_key")
        return entry
    }

    private fun requireHardware(privateKey: java.security.PrivateKey) {
        try {
            val factory = KeyFactory.getInstance(privateKey.algorithm, STORE)
            val info = factory.getKeySpec(privateKey, KeyInfo::class.java)
            val hardwareBacked = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                info.securityLevel == KeyProperties.SECURITY_LEVEL_TRUSTED_ENVIRONMENT ||
                    info.securityLevel == KeyProperties.SECURITY_LEVEL_STRONGBOX
            } else {
                info.isInsideSecureHardware
            }
            if (!hardwareBacked) throw DeviceKeyFailure("unexpected_key")
        } catch (failure: DeviceKeyFailure) {
            throw failure
        } catch (_: Exception) {
            throw DeviceKeyFailure("hardware_unavailable")
        }
    }

    private fun publicJwk(publicKey: java.security.PublicKey): JSONObject {
        val ec = publicKey as? ECPublicKey ?: throw DeviceKeyFailure("unexpected_key")
        if (ec.params.curve.field.fieldSize != 256 || ec.params.order != P256_ORDER ||
            ec.params.curve.field !is java.security.spec.ECFieldFp ||
            (ec.params.curve.field as java.security.spec.ECFieldFp).p != P256_P ||
            ec.params.curve.a != P256_A || ec.params.curve.b != P256_B ||
            ec.params.generator.affineX != P256_GX || ec.params.generator.affineY != P256_GY) {
            throw DeviceKeyFailure("unexpected_key")
        }
        val x = ec.w.affineX
        val y = ec.w.affineY
        if (x.signum() < 0 || y.signum() < 0 || x >= P256_P || y >= P256_P ||
            y.modPow(BigInteger.valueOf(2L), P256_P) != x.modPow(BigInteger.valueOf(3L), P256_P)
                .add(P256_A.multiply(x)).add(P256_B).mod(P256_P)) {
            throw DeviceKeyFailure("unexpected_key")
        }
        return JSONObject().put("kty", "EC").put("crv", "P-256")
            .put("x", base64Url(fixedWidth(x))).put("y", base64Url(fixedWidth(y)))
    }

    private fun fixedWidth(value: BigInteger): ByteArray {
        var bytes = value.toByteArray()
        if (bytes.size == 33 && bytes[0] == 0.toByte()) bytes = bytes.copyOfRange(1, bytes.size)
        if (bytes.size > 32) throw DeviceKeyFailure("unexpected_key")
        return ByteArray(32 - bytes.size) + bytes
    }

    /** Converts strict short-form ASN.1 ECDSA DER into JOSE's fixed r || s. */
    private fun derToJose(der: ByteArray): ByteArray {
        if (der.size < 8 || der[0] != 0x30.toByte() || (der[1].toInt() and 0xff) >= 0x80 ||
            (der[1].toInt() and 0xff) != der.size - 2) throw DeviceKeyFailure("operation_failed")
        var offset = 2
        fun readInteger(): ByteArray {
            if (offset + 2 > der.size || der[offset] != 0x02.toByte()) throw DeviceKeyFailure("operation_failed")
            val length = der[offset + 1].toInt() and 0xff
            offset += 2
            if (length !in 1..33 || offset + length > der.size) throw DeviceKeyFailure("operation_failed")
            var value = der.copyOfRange(offset, offset + length)
            offset += length
            if ((value[0].toInt() and 0x80) != 0) throw DeviceKeyFailure("operation_failed")
            if (value.size > 1 && value[0] == 0.toByte()) {
                if ((value[1].toInt() and 0x80) == 0) throw DeviceKeyFailure("operation_failed")
                value = value.copyOfRange(1, value.size)
            }
            val scalar = BigInteger(1, value)
            if (value.size > 32 || scalar.signum() <= 0 || scalar >= P256_ORDER) {
                throw DeviceKeyFailure("operation_failed")
            }
            return ByteArray(32 - value.size) + value
        }
        val r = readInteger()
        val s = readInteger()
        if (offset != der.size) throw DeviceKeyFailure("operation_failed")
        return r + s
    }

    private fun validAlias(alias: String): Boolean {
        if (!validOpaque(alias, 43, 43)) return false
        // A 32-byte value leaves four data bits in its final base64url
        // character; the remaining two pad bits must both be zero.
        val alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
        return alphabet.indexOf(alias.last()) % 4 == 0
    }

    private fun validOpaque(value: String, min: Int, max: Int): Boolean {
        if (value.length !in min..max) return false
        return value.all { it in 'A'..'Z' || it in 'a'..'z' || it in '0'..'9' || it == '-' || it == '_' }
    }

    private fun validPurpose(purpose: String): Boolean = purpose == "email_login" || purpose == "phone_login" ||
        purpose == "approval_exchange" || purpose == "recovery_code"

    private fun base64Url(bytes: ByteArray): String = Base64.encodeToString(
        bytes, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING
    )

    private fun emptyJwk(): JSONObject = JSONObject().put("kty", "EC").put("crv", "P-256").put("x", "").put("y", "")

    private fun success(alias: String, publicKey: JSONObject, proof: String): String = JSONObject()
        .put("ok", true).put("errorCode", "").put("alias", alias).put("publicKey", publicKey).put("proof", proof).toString()

    private fun failure(code: String): String = JSONObject()
        .put("ok", false).put("errorCode", code).put("alias", "").put("publicKey", emptyJwk()).put("proof", "").toString()

    private fun deleteQuietly(alias: String) {
        try { loadStore().deleteEntry(alias) } catch (_: Exception) { /* preserve the original failure */ }
    }

    private class DeviceKeyFailure(val code: String) : RuntimeException()

    private val P256_ORDER = BigInteger("FFFFFFFF00000000FFFFFFFFFFFFFFFFBCE6FAADA7179E84F3B9CAC2FC632551", 16)
    private val P256_P = BigInteger("FFFFFFFF00000001000000000000000000000000FFFFFFFFFFFFFFFFFFFFFFFF", 16)
    private val P256_A = BigInteger("FFFFFFFF00000001000000000000000000000000FFFFFFFFFFFFFFFFFFFFFFFC", 16)
    private val P256_B = BigInteger("5AC635D8AA3A93E7B3EBBD55769886BC651D06B0CC53B0F63BCE3C3E27D2604B", 16)
    private val P256_GX = BigInteger("6B17D1F2E12C4247F8BCE6E563A440F277037D812DEB33A0F4A13945D898C296", 16)
    private val P256_GY = BigInteger("4FE342E2FE1A7F9B8EE7EB4A7C0F9E162BCE33576B315ECECBB6406837BF51F5", 16)
    private const val HEADER = "{\"alg\":\"ES256\",\"typ\":\"JWT\"}"
}
