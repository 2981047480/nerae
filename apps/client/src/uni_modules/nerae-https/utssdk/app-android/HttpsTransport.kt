package uts.sdk.modules.neraeHttps

import java.io.BufferedInputStream
import java.io.ByteArrayOutputStream
import java.io.EOFException
import java.io.IOException
import java.net.InetSocketAddress
import java.net.Socket
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.util.Locale
import java.util.concurrent.atomic.AtomicReference
import javax.net.ssl.SSLParameters
import javax.net.ssl.SSLSocket
import javax.net.ssl.SSLSocketFactory

internal data class HttpsOutcome(
    val ok: Boolean,
    val status: Int,
    val headersJson: String,
    val body: String,
    val code: String
)

internal object HttpsWire {
    fun success(status: Int, headersJson: String, body: String): HttpsOutcome =
        HttpsOutcome(true, status, headersJson, body, "")

    fun failure(code: String): HttpsOutcome = HttpsOutcome(false, 0, "{}", "", code)

    fun encode(requestId: String, result: HttpsOutcome): String =
        "{" +
            "\"ok\":" + result.ok + "," +
            "\"requestId\":" + StrictJson.quote(requestId) + "," +
            "\"status\":" + result.status + "," +
            "\"headersJson\":" + StrictJson.quote(result.headersJson) + "," +
            "\"body\":" + StrictJson.quote(result.body) + "," +
            "\"code\":" + StrictJson.quote(result.code) +
            "}"
}

internal object HttpsTransport {
    private const val MAX_STATUS_LINE_BYTES = 8 * 1024
    private const val MAX_HEADER_LINE_BYTES = 8 * 1024
    private const val MAX_RESPONSE_HEADER_BYTES = 16 * 1024
    private const val MAX_RESPONSE_HEADER_COUNT = 100
    private const val MAX_TRAILER_BYTES = 16 * 1024
    private const val MAX_TRAILER_COUNT = 100
    private const val MAX_CHUNK_COUNT = 65_536
    private const val READ_BUFFER_BYTES = 8 * 1024

    private val responseHeaderAllowlist = setOf(
        "content-type", "cache-control", "x-request-id", "idempotency-key-expires-at"
    )

    fun execute(
        request: HttpsRequestSpec,
        deadlineNanos: Long,
        registerSocket: (Socket) -> Unit
    ): HttpsOutcome {
        var rawSocket: Socket? = null
        var tlsSocket: SSLSocket? = null
        try {
            val raw = Socket()
            rawSocket = raw
            registerSocket(raw)
            raw.tcpNoDelay = true
            raw.connect(InetSocketAddress(request.host, request.port), remainingMillis(deadlineNanos))

            val factory = SSLSocketFactory.getDefault() as? SSLSocketFactory
                ?: throw HttpsFailure("network")
            val ssl = factory.createSocket(raw, request.host, request.port, true) as? SSLSocket
                ?: throw HttpsFailure("network")
            tlsSocket = ssl
            registerSocket(ssl)
            val parameters: SSLParameters = ssl.sslParameters
            parameters.endpointIdentificationAlgorithm = "HTTPS"
            ssl.sslParameters = parameters
            ssl.soTimeout = remainingMillis(deadlineNanos)
            ssl.startHandshake()

            writeRequest(ssl, request, deadlineNanos)
            ssl.soTimeout = remainingMillis(deadlineNanos)
            val parsed = readResponse(ssl, BufferedInputStream(ssl.inputStream, READ_BUFFER_BYTES), deadlineNanos)
            return parsed
        } catch (failure: HttpsFailure) {
            throw failure
        } catch (_: java.net.SocketTimeoutException) {
            throw HttpsFailure("timeout")
        } catch (_: javax.net.ssl.SSLException) {
            throw HttpsFailure("network")
        } catch (_: IOException) {
            throw HttpsFailure("network")
        } catch (_: Exception) {
            throw HttpsFailure("network")
        } finally {
            try { tlsSocket?.close() } catch (_: Exception) {}
            if (tlsSocket == null) try { rawSocket?.close() } catch (_: Exception) {}
        }
    }

    private fun writeRequest(socket: SSLSocket, request: HttpsRequestSpec, deadlineNanos: Long) {
        socket.soTimeout = remainingMillis(deadlineNanos)
        val output = socket.outputStream
        val head = StringBuilder()
        head.append(request.method).append(' ').append(request.target).append(" HTTP/1.1\r\n")
        head.append("Host: ").append(request.authority).append("\r\n")
        for ((name, value) in request.headers) {
            if (name.equals("cache-control", ignoreCase = true)) continue
            head.append(name).append(": ").append(value).append("\r\n")
        }
        head.append("Accept-Encoding: identity\r\n")
        head.append("Cache-Control: no-store\r\n")
        head.append("Connection: close\r\n")
        if (request.method == "POST") head.append("Content-Length: ").append(request.body.size).append("\r\n")
        head.append("\r\n")
        output.write(head.toString().toByteArray(StandardCharsets.US_ASCII))
        if (request.body.isNotEmpty()) output.write(request.body)
        output.flush()
        if (remainingMillis(deadlineNanos) <= 0) throw HttpsFailure("timeout")
    }

    private fun readResponse(socket: SSLSocket, input: BufferedInputStream, deadlineNanos: Long): HttpsOutcome {
        val statusLine = readAsciiLine(socket, input, MAX_STATUS_LINE_BYTES, deadlineNanos)
        val match = Regex("HTTP/1\\.[01] ([0-9]{3})(?: [\\x20-\\x7e]*)?").matchEntire(statusLine)
            ?: throw HttpsFailure("invalid_response")
        val status = match.groupValues[1].toIntOrNull() ?: throw HttpsFailure("invalid_response")
        if (status !in 200..599) throw HttpsFailure("invalid_response")

        val headers = LinkedHashMap<String, String>()
        val seenNames = HashSet<String>()
        var headerBytes = 0
        var headerCount = 0
        while (true) {
            val line = readAsciiLine(socket, input, MAX_HEADER_LINE_BYTES, deadlineNanos)
            headerBytes += line.toByteArray(StandardCharsets.ISO_8859_1).size + 2
            if (headerBytes > MAX_RESPONSE_HEADER_BYTES) throw HttpsFailure("invalid_response")
            if (line.isEmpty()) break
            if (line[0] == ' ' || line[0] == '\t') throw HttpsFailure("invalid_response")
            headerCount++
            if (headerCount > MAX_RESPONSE_HEADER_COUNT) throw HttpsFailure("invalid_response")
            val colon = line.indexOf(':')
            if (colon <= 0) throw HttpsFailure("invalid_response")
            val name = line.substring(0, colon)
            if (!name.all(::isHeaderToken)) throw HttpsFailure("invalid_response")
            val lower = name.lowercase(Locale.ROOT)
            val value = line.substring(colon + 1).trim(' ')
            if (!value.all { it.code in 0x20..0x7e }) throw HttpsFailure("invalid_response")
            if (lower != "set-cookie" && !seenNames.add(lower)) throw HttpsFailure("invalid_response")
            if (lower == "set-cookie") continue
            headers[lower] = value
        }

        val contentLength = parseContentLength(headers["content-length"])
        val transferEncoding = headers["transfer-encoding"]?.lowercase(Locale.ROOT)
        val contentEncoding = headers["content-encoding"]?.lowercase(Locale.ROOT)
        if (contentLength != null && transferEncoding != null) throw HttpsFailure("invalid_response")
        if (transferEncoding != null && transferEncoding != "chunked") throw HttpsFailure("invalid_response")
        if (contentEncoding != null && contentEncoding != "identity") throw HttpsFailure("invalid_response")

        val body = readBody(socket, input, status, contentLength, transferEncoding, deadlineNanos)
        val text = decodeUtf8(body)
        val publicHeaders = headers.filterKeys { it in responseHeaderAllowlist }
        return HttpsWire.success(status, StrictJson.encodeStringObject(publicHeaders), text)
    }

    private fun parseContentLength(value: String?): Long? {
        if (value == null) return null
        if (!value.matches(Regex("[0-9]{1,20}"))) throw HttpsFailure("invalid_response")
        val length = value.toLongOrNull() ?: throw HttpsFailure("invalid_response")
        if (length > HttpsRequestSpec.MAX_RESPONSE_BYTES) throw HttpsFailure("response_too_large")
        return length
    }

    private fun readBody(
        socket: SSLSocket,
        input: BufferedInputStream,
        status: Int,
        contentLength: Long?,
        transferEncoding: String?,
        deadlineNanos: Long
    ): ByteArray {
        if (status == 204) {
            if ((contentLength != null && contentLength != 0L) || transferEncoding != null) {
                throw HttpsFailure("invalid_response")
            }
            socket.soTimeout = remainingMillis(deadlineNanos)
            if (input.read() != -1) throw HttpsFailure("invalid_response")
            return ByteArray(0)
        }
        if (status == 304) return ByteArray(0)
        return when {
            transferEncoding == "chunked" -> readChunked(socket, input, deadlineNanos)
            contentLength != null -> readFixed(socket, input, contentLength.toInt(), deadlineNanos)
            else -> readUntilEof(socket, input, deadlineNanos)
        }
    }

    private fun readFixed(socket: SSLSocket, input: BufferedInputStream, size: Int, deadlineNanos: Long): ByteArray {
        if (size > HttpsRequestSpec.MAX_RESPONSE_BYTES) throw HttpsFailure("response_too_large")
        val output = ByteArrayOutputStream(size.coerceAtMost(READ_BUFFER_BYTES))
        val buffer = ByteArray(READ_BUFFER_BYTES)
        var remaining = size
        while (remaining > 0) {
            socket.soTimeout = remainingMillis(deadlineNanos)
            val count = input.read(buffer, 0, minOf(buffer.size, remaining))
            if (count < 0) throw HttpsFailure("invalid_response")
            if (count == 0) continue
            output.write(buffer, 0, count)
            remaining -= count
        }
        return output.toByteArray()
    }

    private fun readUntilEof(socket: SSLSocket, input: BufferedInputStream, deadlineNanos: Long): ByteArray {
        val output = ByteArrayOutputStream(READ_BUFFER_BYTES)
        val buffer = ByteArray(READ_BUFFER_BYTES)
        while (true) {
            socket.soTimeout = remainingMillis(deadlineNanos)
            val count = input.read(buffer)
            if (count < 0) return output.toByteArray()
            if (count == 0) continue
            if (output.size() > HttpsRequestSpec.MAX_RESPONSE_BYTES - count) {
                throw HttpsFailure("response_too_large")
            }
            output.write(buffer, 0, count)
        }
    }

    private fun readChunked(socket: SSLSocket, input: BufferedInputStream, deadlineNanos: Long): ByteArray {
        val output = ByteArrayOutputStream(READ_BUFFER_BYTES)
        var chunkCount = 0
        while (true) {
            val line = readAsciiLine(socket, input, MAX_HEADER_LINE_BYTES, deadlineNanos)
            chunkCount++
            if (chunkCount > MAX_CHUNK_COUNT) throw HttpsFailure("invalid_response")
            if (!line.matches(Regex("[0-9A-Fa-f]{1,16}"))) throw HttpsFailure("invalid_response")
            val chunkSize = line.toLongOrNull(16) ?: throw HttpsFailure("invalid_response")
            if (chunkSize == 0L) {
                var trailerBytes = 0
                var trailerCount = 0
                while (true) {
                    val trailer = readAsciiLine(socket, input, MAX_HEADER_LINE_BYTES, deadlineNanos)
                    trailerBytes += trailer.length + 2
                    if (trailerBytes > MAX_TRAILER_BYTES) throw HttpsFailure("invalid_response")
                    if (trailer.isEmpty()) return output.toByteArray()
                    trailerCount++
                    if (trailerCount > MAX_TRAILER_COUNT || trailer[0] == ' ' || trailer[0] == '\t') {
                        throw HttpsFailure("invalid_response")
                    }
                    val colon = trailer.indexOf(':')
                    if (colon <= 0 || !trailer.substring(0, colon).all(::isHeaderToken)) {
                        throw HttpsFailure("invalid_response")
                    }
                }
            }
            if (chunkSize > HttpsRequestSpec.MAX_RESPONSE_BYTES - output.size()) {
                throw HttpsFailure("response_too_large")
            }
            val chunk = readFixed(socket, input, chunkSize.toInt(), deadlineNanos)
            output.write(chunk, 0, chunk.size)
            socket.soTimeout = remainingMillis(deadlineNanos)
            if (input.read() != '\r'.code || input.read() != '\n'.code) throw HttpsFailure("invalid_response")
        }
    }

    private fun decodeUtf8(bytes: ByteArray): String = try {
        StandardCharsets.UTF_8.newDecoder()
            .onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT)
            .decode(ByteBuffer.wrap(bytes)).toString()
    } catch (_: Exception) {
        throw HttpsFailure("invalid_response")
    }

    private fun readAsciiLine(socket: SSLSocket, input: BufferedInputStream, maxBytes: Int, deadlineNanos: Long): String {
        val line = ByteArrayOutputStream()
        while (true) {
            socket.soTimeout = remainingMillis(deadlineNanos)
            val value = input.read()
            if (value < 0) throw HttpsFailure("invalid_response")
            if (value == '\r'.code) {
                if (input.read() != '\n'.code) throw HttpsFailure("invalid_response")
                return line.toByteArray().toString(StandardCharsets.ISO_8859_1)
            }
            if (value == '\n'.code || value > 0x7e || value < 0x20) throw HttpsFailure("invalid_response")
            line.write(value)
            if (line.size() > maxBytes) throw HttpsFailure("invalid_response")
        }
    }

    private fun remainingMillis(deadlineNanos: Long): Int {
        val remaining = deadlineNanos - System.nanoTime()
        if (remaining <= 0) throw HttpsFailure("timeout")
        val millis = (remaining + 999_999L) / 1_000_000L
        return millis.coerceIn(1L, Int.MAX_VALUE.toLong()).toInt()
    }

    private fun isHeaderToken(character: Char): Boolean = character.code < 128 &&
        (character.isLetterOrDigit() || character in "!#$%&'*+-.^_`|~")
}
