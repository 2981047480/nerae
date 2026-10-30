package uts.sdk.modules.neraeRandom

import java.security.SecureRandom

private const val RANDOM_BYTE_COUNT = 32
private const val BASE64URL_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

internal fun neraeBase64Url(bytes: ByteArray): String {
    val output = StringBuilder((bytes.size * 8 + 5) / 6)
    var offset = 0
    while (offset + 3 <= bytes.size) {
        val first = bytes[offset].toInt() and 0xff
        val second = bytes[offset + 1].toInt() and 0xff
        val third = bytes[offset + 2].toInt() and 0xff
        output.append(BASE64URL_ALPHABET[first ushr 2])
        output.append(BASE64URL_ALPHABET[((first and 0x03) shl 4) or (second ushr 4)])
        output.append(BASE64URL_ALPHABET[((second and 0x0f) shl 2) or (third ushr 6)])
        output.append(BASE64URL_ALPHABET[third and 0x3f])
        offset += 3
    }
    val remaining = bytes.size - offset
    if (remaining == 1) {
        val first = bytes[offset].toInt() and 0xff
        output.append(BASE64URL_ALPHABET[first ushr 2])
        output.append(BASE64URL_ALPHABET[(first and 0x03) shl 4])
    } else if (remaining == 2) {
        val first = bytes[offset].toInt() and 0xff
        val second = bytes[offset + 1].toInt() and 0xff
        output.append(BASE64URL_ALPHABET[first ushr 2])
        output.append(BASE64URL_ALPHABET[((first and 0x03) shl 4) or (second ushr 4)])
        output.append(BASE64URL_ALPHABET[(second and 0x0f) shl 2])
    }
    return output.toString()
}

/** The random value is returned only to the UTS caller and is never logged. */
object NeraeAndroidSecureRandomBridge {
    @JvmStatic
    fun generateRequestKey(): String = try {
        val bytes = ByteArray(RANDOM_BYTE_COUNT)
        SecureRandom().nextBytes(bytes)
        neraeBase64Url(bytes).takeIf { it.length == 43 } ?: ""
    } catch (_: Exception) {
        ""
    }
}
