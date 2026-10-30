package uts.sdk.modules.neraeHttps

import java.net.URI
import java.math.BigDecimal

internal class HttpsRequestSpec private constructor(
    val method: String,
    val host: String,
    val port: Int,
    val authority: String,
    val target: String,
    val headers: Map<String, String>,
    val body: ByteArray,
    val timeoutMs: Int
) {
    companion object {
        const val MAX_REQUEST_BYTES = 4 * 1024 * 1024
        const val MAX_RESPONSE_BYTES = 4 * 1024 * 1024
        private const val MAX_ENVELOPE_BYTES = 9 * 1024 * 1024
        private const val MAX_HEADER_JSON_BYTES = 20 * 1024
        private const val MAX_HEADER_TOTAL_BYTES = 16 * 1024
        private const val MAX_HEADER_VALUE_BYTES = 8 * 1024
        private const val MAX_HEADER_NAME_BYTES = 128

        private val envelopeKeys = setOf("baseUrl", "path", "method", "headersJson", "body", "timeoutMs")
        private val allowedRequestHeaders = setOf(
            "accept", "authorization", "cache-control", "content-type", "idempotency-key", "x-request-id"
        )

        fun parse(payload: String): HttpsRequestSpec {
            val fields = try {
                StrictJson.parseFlatObject(payload, MAX_ENVELOPE_BYTES, envelopeKeys)
            } catch (_: Exception) {
                throw HttpsFailure("invalid_request")
            }
            if (fields.keys != envelopeKeys) throw HttpsFailure("invalid_request")
            val base = fields.text("baseUrl")
            val path = fields.text("path")
            val method = fields.text("method")
            val headersJson = fields.text("headersJson")
            val bodyText = fields.text("body")
            val timeoutLexeme = (fields["timeoutMs"] as? JsonScalar.Number)?.lexeme
                ?: throw HttpsFailure("invalid_request")
            val timeout = parseTimeout(timeoutLexeme) ?: throw HttpsFailure("invalid_request")
            if (method !in setOf("GET", "POST")) {
                throw HttpsFailure("invalid_request")
            }

            val requestHeaders = parseHeaders(headersJson)
            val body = try {
                bodyText.toByteArray(Charsets.UTF_8)
            } catch (_: Exception) {
                throw HttpsFailure("invalid_request")
            }
            if (body.size > MAX_REQUEST_BYTES || (method == "GET" && body.isNotEmpty())) {
                throw HttpsFailure("invalid_request")
            }
            val (host, port, authority, basePath) = parseBase(base)
            val target = buildTarget(path, basePath, authority, host, port)
            return HttpsRequestSpec(method, host, port, authority, target, requestHeaders, body, timeout)
        }

        private fun Map<String, JsonScalar>.text(key: String): String =
            (this[key] as? JsonScalar.Text)?.value ?: throw HttpsFailure("invalid_request")

        private fun parseHeaders(source: String): Map<String, String> {
            val values = try {
                StrictJson.parseFlatObject(source, MAX_HEADER_JSON_BYTES, null)
            } catch (_: Exception) {
                throw HttpsFailure("invalid_request")
            }
            val headers = LinkedHashMap<String, String>()
            val normalizedNames = HashSet<String>()
            var byteTotal = 0
            for ((name, scalar) in values) {
                val value = (scalar as? JsonScalar.Text)?.value ?: throw HttpsFailure("invalid_request")
                val normalized = name.lowercase()
                if (normalized !in allowedRequestHeaders || !normalizedNames.add(normalized) ||
                    name.isEmpty() || name.length > MAX_HEADER_NAME_BYTES || !name.all(::isHeaderToken) ||
                    value.toByteArray(Charsets.UTF_8).size > MAX_HEADER_VALUE_BYTES ||
                    !value.all { it.code in 0x20..0x7e }) {
                    throw HttpsFailure("invalid_request")
                }
                byteTotal += name.length + value.length
                if (byteTotal > MAX_HEADER_TOTAL_BYTES) throw HttpsFailure("invalid_request")
                headers[name] = value
            }
            return headers
        }

        private fun parseTimeout(lexeme: String): Int? {
            if (lexeme.length > 32) return null
            val exactInteger = try { BigDecimal(lexeme).intValueExact() } catch (_: Exception) { return null }
            return exactInteger.takeIf { it in 1_000..120_000 }
        }

        private fun parseBase(value: String): BaseParts {
            val uri = try { URI(value) } catch (_: Exception) { throw HttpsFailure("invalid_configuration") }
            val host = uri.host
            val port = if (uri.port == -1) 443 else uri.port
            val authority = uri.rawAuthority
            val rawPath = uri.rawPath ?: ""
            if (!uri.isAbsolute || !uri.scheme.equals("https", ignoreCase = true) ||
                host.isNullOrEmpty() || uri.rawUserInfo != null || uri.rawQuery != null || uri.rawFragment != null ||
                authority.isNullOrEmpty() || authority.contains('@') || port !in 1..65_535 ||
                !validPath(rawPath, allowEmpty = true)) {
                throw HttpsFailure("invalid_configuration")
            }
            return BaseParts(host, port, authority, rawPath)
        }

        private fun buildTarget(path: String, basePath: String, authority: String, host: String, port: Int): String {
            if (path.isEmpty() || !path.startsWith('/') || path.startsWith("//") ||
                path.contains('#') || path.contains('\\') || containsControl(path)) {
                throw HttpsFailure("invalid_request")
            }
            val question = path.indexOf('?')
            val routePath = if (question >= 0) path.substring(0, question) else path
            val query = if (question >= 0) path.substring(question + 1) else null
            if (!validPath(routePath, allowEmpty = false) || (query != null && !validQuery(query))) {
                throw HttpsFailure("invalid_request")
            }
            val prefix = basePath.removeSuffix("/")
            val combinedPath = prefix + routePath
            val target = combinedPath + (query?.let { "?$it" } ?: "")
            val assembled = try { URI("https://$authority$target") } catch (_: Exception) {
                throw HttpsFailure("invalid_request")
            }
            if (!assembled.scheme.equals("https", ignoreCase = true) ||
                !assembled.host.equals(host, ignoreCase = true) ||
                (if (assembled.port == -1) 443 else assembled.port) != port ||
                assembled.rawUserInfo != null || assembled.rawFragment != null ||
                assembled.rawPath != combinedPath || assembled.rawQuery != query) {
                throw HttpsFailure("invalid_request")
            }
            return target
        }

        private fun validPath(value: String, allowEmpty: Boolean): Boolean {
            if (!allowEmpty && value.isEmpty()) return false
            if (value.isNotEmpty() && !value.startsWith('/')) return false
            val lowered = value.lowercase()
            if ("%2f" in lowered || "%5c" in lowered || "%25" in lowered) return false
            if (!validPercentEncoded(value, allowSlash = true, allowQuestion = false)) return false
            val decodedDots = value.split('/').map { it.replace("%2e", ".", ignoreCase = true) }
            return decodedDots.none { it == "." || it == ".." }
        }

        private fun validQuery(value: String): Boolean =
            validPercentEncoded(value, allowSlash = true, allowQuestion = true)

        private fun validPercentEncoded(value: String, allowSlash: Boolean, allowQuestion: Boolean): Boolean {
            val bytes = value.toByteArray(Charsets.UTF_8)
            var index = 0
            while (index < bytes.size) {
                val byte = bytes[index].toInt() and 0xff
                if (byte == '%'.code) {
                    if (index + 2 >= bytes.size || !isHex(bytes[index + 1]) || !isHex(bytes[index + 2])) return false
                    index += 3
                    continue
                }
                if (!(isUnreserved(byte) || isSubDelimiter(byte) || byte == ':'.code || byte == '@'.code ||
                        (allowSlash && byte == '/'.code) || (allowQuestion && byte == '?'.code))) return false
                index++
            }
            return true
        }

        private fun isUnreserved(value: Int): Boolean =
            value in 0x41..0x5a || value in 0x61..0x7a || value in 0x30..0x39 || value == 0x2d || value == 0x2e ||
                value == 0x5f || value == 0x7e

        private fun isSubDelimiter(value: Int): Boolean =
            value == 0x21 || value == 0x24 || value == 0x26 || value == 0x27 || value == 0x28 || value == 0x29 ||
                value == 0x2a || value == 0x2b || value == 0x2c || value == 0x3b || value == 0x3d

        private fun isHex(value: Byte): Boolean {
            val digit = value.toInt().toChar()
            return digit in '0'..'9' || digit in 'a'..'f' || digit in 'A'..'F'
        }

        private fun isHeaderToken(character: Char): Boolean = character.code < 128 &&
            (character.isLetterOrDigit() || character in "!#$%&'*+-.^_`|~")

        private fun containsControl(value: String): Boolean = value.any { it.code < 0x20 || it.code == 0x7f }

        private data class BaseParts(val host: String, val port: Int, val authority: String, val basePath: String)
    }
}

internal class HttpsFailure(val code: String) : Exception()
