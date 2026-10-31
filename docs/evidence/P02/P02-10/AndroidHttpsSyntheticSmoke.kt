package uts.sdk.modules.neraeHttps

import java.io.File
import java.net.CookieHandler
import java.net.CookieManager
import java.net.CookiePolicy
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

private data class ProbeResult(
    val ok: Boolean,
    val requestId: String,
    val status: Int,
    val headersJson: String,
    val body: String,
    val code: String
)

private data class ProbeRequest(val payload: String, val timeoutMsText: String)

private data class Hit(val server: String, val path: String, val cookiePresent: Boolean, val method: String,
                      val authorizationPresent: Boolean, val contentLength: String)

private val resultKeys = setOf("ok", "requestId", "status", "headersJson", "body", "code")

fun main(args: Array<String>) {
    require(args.size == 3)
    val goodPort = args[0].toInt()
    val badPort = args[1].toInt()
    val hitsFile = File(args[2])
    val callbackExecutor = Executors.newSingleThreadExecutor { runnable -> Thread(runnable, "synthetic-main-callback").apply { isDaemon = true } }
    val workerExecutor = Executors.newFixedThreadPool(8) { runnable -> Thread(runnable, "synthetic-http-worker").apply { isDaemon = true } }
    val timerExecutor = Executors.newSingleThreadScheduledExecutor { runnable -> Thread(runnable, "synthetic-http-timer").apply { isDaemon = true } }
    val callbackDispatcher = HttpsCallbackDispatcher { callback -> callbackExecutor.execute(callback) }
    val registry = HttpsRequestRegistry(callbackDispatcher, workerExecutor, timerExecutor)
    val oldCookieHandler = CookieHandler.getDefault()
    val cookieManager = CookieManager(null, CookiePolicy.ACCEPT_ALL)
    CookieHandler.setDefault(cookieManager)
    try {
        assertTimeoutNumberFormatting()
        val success = call(registry, request("https://127.0.0.1:$goodPort", "/success?probe=ok"))
        check(success.ok && success.status == 200 && success.body == "{\"ok\":true}")
        check(success.headersJson.contains("content-type"))
        assertWireShape(success)

        val replayMarker = call(registry, request("https://127.0.0.1:$goodPort", "/replay-marker"))
        check(replayMarker.ok && replayMarker.status == 503)
        check(StrictJson.parseFlatObject(replayMarker.headersJson, 2048, null)["idempotency-replayed"] == JsonScalar.Text("true"))
        checkFailureCode(call(registry, request("https://127.0.0.1:$goodPort", "/replay-marker-malformed")), "invalid_response")
        checkFailureCode(call(registry, request("https://127.0.0.1:$goodPort", "/replay-marker-duplicate")), "invalid_response")

        check(call(registry, request("https://127.0.0.1:$goodPort", "/success", timeoutMs = 1_000,
            timeoutLexeme = "1000.0")).ok)
        check(call(registry, request("https://127.0.0.1:$goodPort", "/success", timeoutMs = 120_000,
            timeoutLexeme = "1.2e5")).ok)

        val inspect = call(registry, request("https://127.0.0.1:$goodPort", "/inspect"))
        val inspectFields = StrictJson.parseFlatObject(inspect.body, 4096, setOf(
            "acceptEncoding", "cacheControl", "authorizationPresent", "cookiePresent", "connection"
        ))
        check(inspectFields["acceptEncoding"] == JsonScalar.Text("identity"))
        check(inspectFields["cacheControl"] == JsonScalar.Text("no-store"))
        check(inspectFields["authorizationPresent"] == JsonScalar.Bool(true))
        check(inspectFields["cookiePresent"] == JsonScalar.Bool(false))
        check(inspectFields["connection"] == JsonScalar.Text("close"))

        call(registry, request("https://127.0.0.1:$goodPort", "/set-cookie"))
        val cookieCheck = call(registry, request("https://127.0.0.1:$goodPort", "/check-cookie"))
        check(cookieCheck.body == "absent")
        check(cookieManager.cookieStore.cookies.isEmpty())
        check(CookieHandler.getDefault() === cookieManager)

        val noTrust = call(registry, request("https://127.0.0.1:$badPort", "/success"))
        check(!noTrust.ok && noTrust.code == "network")
        val wrongHost = call(registry, request("https://localhost:$goodPort", "/success"))
        check(!wrongHost.ok && wrongHost.code == "network")

        for ((route, expectedStatus) in listOf("/redirect-301" to 301, "/redirect-302" to 302,
                 "/redirect-cross" to 302, "/redirect-downgrade" to 302)) {
            val before = readHits(hitsFile).size
            val redirected = call(registry, request("https://127.0.0.1:$goodPort", route))
            check(redirected.ok && redirected.status == expectedStatus)
            Thread.sleep(100)
            val newHits = readHits(hitsFile).drop(before)
            check(newHits.size == 1 && newHits.single().server == "primary" && newHits.single().path == route)
        }
        check(readHits(hitsFile).none { it.server == "alternate" || it.server == "http" })

        val fixedTooLarge = call(registry, request("https://127.0.0.1:$goodPort", "/large-fixed"))
        val chunkedTooLarge = call(registry, request("https://127.0.0.1:$goodPort", "/large-chunked"))
        val closeTooLarge = call(registry, request("https://127.0.0.1:$goodPort", "/large-close"))
        check(!fixedTooLarge.ok && fixedTooLarge.code == "response_too_large")
        check(!chunkedTooLarge.ok && chunkedTooLarge.code == "response_too_large")
        check(!closeTooLarge.ok && closeTooLarge.code == "response_too_large")

        val exactResponse = call(registry, request("https://127.0.0.1:$goodPort", "/exact-response"))
        check(exactResponse.ok && exactResponse.body.toByteArray(Charsets.UTF_8).size == HttpsRequestSpec.MAX_RESPONSE_BYTES)
        val validChunked = call(registry, request("https://127.0.0.1:$goodPort", "/chunked-good"))
        check(validChunked.ok && validChunked.body == "chunked-ok")
        val validClose = call(registry, request("https://127.0.0.1:$goodPort", "/close-good"))
        check(validClose.ok && validClose.body == "close-ok")

        val revokeHeaders = "{\"Accept\":\"application/json\",\"Authorization\":\"Bearer synthetic-probe\",\"Cache-Control\":\"no-store\"}"
        val beforeRevoke = readHits(hitsFile).size
        val empty204 = call(registry, request("https://127.0.0.1:$goodPort", "/logout-empty", method = "DELETE", headersJson = revokeHeaders))
        check(empty204.ok && empty204.status == 204 && empty204.body.isEmpty())
        val empty204Headers = StrictJson.parseFlatObject(empty204.headersJson, 2048, null)
        check(empty204Headers["cache-control"] == JsonScalar.Text("no-store"))
        val revokeHit = readHits(hitsFile).drop(beforeRevoke).single()
        check(revokeHit.method == "DELETE" && revokeHit.authorizationPresent && revokeHit.contentLength.isEmpty())
        checkFailureCode(call(registry, request("https://127.0.0.1:$goodPort", "/logout-content-length-zero", method = "DELETE", headersJson = revokeHeaders)), "invalid_response")
        checkFailureCode(call(registry, request("https://127.0.0.1:$goodPort", "/logout-body-length", method = "DELETE", headersJson = revokeHeaders)), "invalid_response")
        checkFailureCode(call(registry, request("https://127.0.0.1:$goodPort", "/logout-transfer-encoding", method = "DELETE", headersJson = revokeHeaders)), "invalid_response")
        val unframed204 = call(registry, request("https://127.0.0.1:$goodPort", "/logout-body-unframed", method = "DELETE", headersJson = revokeHeaders))
        check(unframed204.ok && unframed204.status == 204 && unframed204.body.isEmpty())
        val keepAliveStarted = System.nanoTime()
        val keepAlive204 = call(registry, request("https://127.0.0.1:$goodPort", "/logout-keepalive", method = "DELETE", headersJson = revokeHeaders))
        val keepAliveElapsedMs = (System.nanoTime() - keepAliveStarted) / 1_000_000
        check(keepAlive204.ok && keepAlive204.status == 204 && keepAlive204.body.isEmpty() && keepAliveElapsedMs < 1_000)
        val emptyFramingHits = readHits(hitsFile).size
        checkFailureCode(call(registry, request("https://127.0.0.1:$goodPort", "/logout-empty", method = "DELETE", body = "x", headersJson = revokeHeaders)), "invalid_request")
        check(readHits(hitsFile).size == emptyFramingHits)
        val rejected204Headers = call(registry, request("https://127.0.0.1:$goodPort", "/logout-no-store", method = "DELETE", headersJson = revokeHeaders))
        check(rejected204Headers.ok && rejected204Headers.status == 204 && rejected204Headers.headersJson == "{}")
        val explicit401 = call(registry, request("https://127.0.0.1:$goodPort", "/logout-unauthorized", method = "DELETE", headersJson = revokeHeaders))
        check(explicit401.ok && explicit401.status == 401 && explicit401.body.contains("UNAUTHENTICATED") &&
            explicit401.headersJson.contains("0123456789abcdef0123456789abcdef"))
        val malformed401 = call(registry, request("https://127.0.0.1:$goodPort", "/logout-unauthorized-malformed", method = "DELETE", headersJson = revokeHeaders))
        check(malformed401.ok && malformed401.status == 401 && malformed401.body.contains("0123456789abcdef0123456789abcdef") &&
            malformed401.headersJson.contains("ffffffffffffffffffffffffffffffff"))
        val non204 = call(registry, request("https://127.0.0.1:$goodPort", "/logout-200", method = "DELETE", headersJson = revokeHeaders))
        check(non204.ok && non204.status == 200)
        val lateLogout = call(registry, request("https://127.0.0.1:$goodPort", "/logout-slow", method = "DELETE", headersJson = revokeHeaders,
            timeoutMs = 5_000), cancelAfterMs = 100, lateCallbackWaitMs = 1_800)
        checkFailureCode(lateLogout, "cancelled")

        assertInvalidResponse(registry, goodPort, "/bad-status")
        assertInvalidResponse(registry, goodPort, "/long-header")
        assertInvalidResponse(registry, goodPort, "/folded-header")
        assertInvalidResponse(registry, goodPort, "/duplicate-cl")
        assertInvalidResponse(registry, goodPort, "/conflicting-cl")
        assertInvalidResponse(registry, goodPort, "/te-cl")
        assertInvalidResponse(registry, goodPort, "/bad-chunk")
        assertInvalidResponse(registry, goodPort, "/chunk-hex-overflow")
        assertInvalidResponse(registry, goodPort, "/truncated-length")
        assertInvalidResponse(registry, goodPort, "/truncated-chunk")
        assertInvalidResponse(registry, goodPort, "/bad-encoding")
        assertInvalidResponse(registry, goodPort, "/unknown-transfer")

        val overChunk = call(registry, request("https://127.0.0.1:$goodPort", "/chunk-over-limit"))
        check(!overChunk.ok && overChunk.code == "response_too_large")
        val noLengthBoundary = request("https://127.0.0.1:$goodPort", "/close-exact")
        check(call(registry, noLengthBoundary).let { it.ok && it.body.toByteArray(Charsets.UTF_8).size == HttpsRequestSpec.MAX_RESPONSE_BYTES })

        val exactRequest = request("https://127.0.0.1:$goodPort", "/post-size", method = "POST",
            body = "x".repeat(HttpsRequestSpec.MAX_REQUEST_BYTES))
        check(call(registry, exactRequest).body == HttpsRequestSpec.MAX_REQUEST_BYTES.toString())
        val postHitsBefore = readHits(hitsFile).count { it.path == "/post-size" }
        val tooLargeRequest = request("https://127.0.0.1:$goodPort", "/post-size", method = "POST",
            body = "x".repeat(HttpsRequestSpec.MAX_REQUEST_BYTES + 1))
        checkFailureCode(call(registry, tooLargeRequest), "invalid_request")
        check(readHits(hitsFile).count { it.path == "/post-size" } == postHitsBefore)

        val invalid = listOf(
            request("http://127.0.0.1:$goodPort", "/success") to "invalid_configuration",
            request("https://127.0.0.1:$goodPort", "relative") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/bad path") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/训练") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/?q=%2") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/%2fadmin") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/../admin") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/success", headersJson = "{\"Cookie\":\"x\"}") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/success", headersJson = "{\"Authorization\":\"a\",\"authorization\":\"b\"}") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/success", headersJson = "{\"Authorization\":\"a\\r\\nb\"}") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/success", method = "PUT") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/success", method = "GET", body = "x") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/logout-empty", method = "DELETE", body = "x") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/success", timeoutLexeme = "1000.5") to "invalid_request",
            request("https://127.0.0.1:$goodPort", "/success", timeoutLexeme = "1e999999999") to "invalid_request"
        )
        val requestsBeforeInvalid = readHits(hitsFile).size
        for ((payload, code) in invalid) checkFailureCode(call(registry, payload), code)
        check(readHits(hitsFile).size == requestsBeforeInvalid)

        val timeout = call(registry, request("https://127.0.0.1:$goodPort", "/slow", timeoutMs = 1_000))
        checkFailureCode(timeout, "timeout")
        Thread.sleep(1_100)
        val cancelled = call(registry, request("https://127.0.0.1:$goodPort", "/slow", timeoutMs = 5_000), cancelAfterMs = 100)
        checkFailureCode(cancelled, "cancelled")
        Thread.sleep(1_100)

        assertQueueDeadline(goodPort, hitsFile)
        assertRejectedExecutors(goodPort)

        check(CookieHandler.getDefault() === cookieManager)
        check(cookieManager.cookieStore.cookies.isEmpty())
        println("PASS: strict HTTP framing, system-trust TLS/hostname, redirects, bounded bodies, timeout/cancel one-shot callbacks, and no CookieHandler use.")
    } finally {
        registry.close()
        callbackExecutor.shutdownNow()
        CookieHandler.setDefault(oldCookieHandler)
    }
}

private fun request(
    baseUrl: String,
    path: String,
    method: String = "GET",
    body: String = "",
    headersJson: String = "{\"Accept\":\"application/json\",\"Authorization\":\"Bearer synthetic-probe\"}",
    timeoutMs: Int = 5_000,
    timeoutLexeme: String? = null
): ProbeRequest {
    val payload = "{" +
        "\"baseUrl\":" + StrictJson.quote(baseUrl) + "," +
        "\"path\":" + StrictJson.quote(path) + "," +
        "\"method\":" + StrictJson.quote(method) + "," +
        "\"headersJson\":" + StrictJson.quote(headersJson) + "," +
        "\"body\":" + StrictJson.quote(body) + "," +
        "\"timeoutMs\":" + (timeoutLexeme ?: timeoutMs.toString()) +
        "}"
    return ProbeRequest(payload, timeoutMs.toString())
}

private fun assertTimeoutNumberFormatting() {
    val cases: List<Pair<Number, String>> = listOf(1000.0 to "1000", 120000.0 to "120000")
    for ((value, expected) in cases) {
        check(value.toDouble() % 1.0 == 0.0)
        val generatedKotlinExpression = "" + value.toInt()
        check(generatedKotlinExpression == expected) { "unexpected canonical timeout: $generatedKotlinExpression" }
    }
}

private fun call(registry: HttpsRequestRegistry, request: ProbeRequest, cancelAfterMs: Long? = null,
                 lateCallbackWaitMs: Long = 0): ProbeResult {
    val count = AtomicInteger()
    val raw = AtomicReference<String>()
    val callbackThread = AtomicReference<String>()
    val latch = CountDownLatch(1)
    val id = registry.request(request.payload, request.timeoutMsText) { value ->
        count.incrementAndGet()
        raw.set(value)
        callbackThread.set(Thread.currentThread().name)
        latch.countDown()
    }
    if (cancelAfterMs != null) {
        Thread.sleep(cancelAfterMs)
        registry.cancel(id)
    }
    check(latch.await(15, TimeUnit.SECONDS)) { "callback timeout" }
    Thread.sleep(maxOf(75L, lateCallbackWaitMs))
    check(count.get() == 1) { "callback count ${count.get()}" }
    check(callbackThread.get().contains("callback"))
    return parseResult(raw.get())
}

private fun parseResult(raw: String): ProbeResult {
    val values = StrictJson.parseFlatObject(raw, 9 * 1024 * 1024, resultKeys)
    check(values.keys == resultKeys)
    val ok = (values["ok"] as? JsonScalar.Bool)?.value ?: error("missing ok")
    val requestId = (values["requestId"] as? JsonScalar.Text)?.value ?: error("missing request id")
    val status = (values["status"] as? JsonScalar.Number)?.lexeme?.toIntOrNull() ?: error("missing status")
    val headersJson = (values["headersJson"] as? JsonScalar.Text)?.value ?: error("missing response headers")
    val body = (values["body"] as? JsonScalar.Text)?.value ?: error("missing body")
    val code = (values["code"] as? JsonScalar.Text)?.value ?: error("missing code")
    if (ok) StrictJson.parseFlatObject(headersJson, 20 * 1024, null)
    check(requestId.matches(Regex("[0-9a-f-]{36}")))
    return ProbeResult(ok, requestId, status, headersJson, body, code)
}

private fun assertWireShape(result: ProbeResult) {
    check(result.ok && result.code.isEmpty())
    check(result.status in 100..599)
    check(StrictJson.parseFlatObject(result.headersJson, 20 * 1024, null).values.all { it is JsonScalar.Text })
}

private fun checkFailureCode(result: ProbeResult, expected: String) {
    check(!result.ok && result.code == expected) { "expected $expected, got $result" }
    check(result.status == 0 && result.headersJson == "{}" && result.body.isEmpty())
}

private fun assertInvalidResponse(registry: HttpsRequestRegistry, port: Int, path: String) {
    checkFailureCode(call(registry, request("https://127.0.0.1:$port", path)), "invalid_response")
}

private fun assertQueueDeadline(goodPort: Int, hitsFile: File) {
    val callbackExecutor = Executors.newSingleThreadExecutor { runnable -> Thread(runnable, "queued-callback").apply { isDaemon = true } }
    val workerExecutor = Executors.newSingleThreadExecutor { runnable -> Thread(runnable, "blocked-worker").apply { isDaemon = true } }
    val timerExecutor = Executors.newSingleThreadScheduledExecutor { runnable -> Thread(runnable, "queued-timer").apply { isDaemon = true } }
    val releaseWorker = CountDownLatch(1)
    val workerEntered = CountDownLatch(1)
    val drain = CountDownLatch(1)
    workerExecutor.submit { workerEntered.countDown(); releaseWorker.await() }
    check(workerEntered.await(2, TimeUnit.SECONDS))
    val registry = HttpsRequestRegistry(
        HttpsCallbackDispatcher { callback -> callbackExecutor.execute(callback) }, workerExecutor, timerExecutor, maxConcurrent = 1
    )
    try {
        val hitsBefore = readHits(hitsFile).size
        val firstCount = AtomicInteger()
        val firstRaw = AtomicReference<String>()
        val firstLatch = CountDownLatch(1)
        registry.request(request("https://127.0.0.1:$goodPort", "/success", timeoutMs = 1_000).payload, "1000") { raw ->
            firstCount.incrementAndGet(); firstRaw.set(raw); firstLatch.countDown()
        }
        val rejectedCount = AtomicInteger()
        val rejectedRaw = AtomicReference<String>()
        val rejectedLatch = CountDownLatch(1)
        registry.request(request("https://127.0.0.1:$goodPort", "/success", timeoutMs = 1_000).payload, "1000") { raw ->
            rejectedCount.incrementAndGet(); rejectedRaw.set(raw); rejectedLatch.countDown()
        }
        check(rejectedLatch.await(2, TimeUnit.SECONDS))
        checkFailureCode(parseResult(rejectedRaw.get()), "unavailable")
        check(firstLatch.await(3, TimeUnit.SECONDS))
        checkFailureCode(parseResult(firstRaw.get()), "timeout")

        val secondCount = AtomicInteger()
        val secondRaw = AtomicReference<String>()
        val secondLatch = CountDownLatch(1)
        registry.request(request("https://127.0.0.1:$goodPort", "/success", timeoutMs = 1_000).payload, "1000") { raw ->
            secondCount.incrementAndGet(); secondRaw.set(raw); secondLatch.countDown()
        }
        check(secondLatch.await(3, TimeUnit.SECONDS))
        checkFailureCode(parseResult(secondRaw.get()), "timeout")
        releaseWorker.countDown()
        workerExecutor.submit { drain.countDown() }
        check(drain.await(2, TimeUnit.SECONDS))
        Thread.sleep(75)
        check(firstCount.get() == 1 && secondCount.get() == 1 && rejectedCount.get() == 1)
        check(readHits(hitsFile).size == hitsBefore) { "a task timed out in the queue reached the server" }
    } finally {
        releaseWorker.countDown()
        registry.close()
        callbackExecutor.shutdownNow()
    }
}

private fun assertRejectedExecutors(goodPort: Int) {
    val callbackExecutor = Executors.newSingleThreadExecutor { runnable -> Thread(runnable, "rejection-callback").apply { isDaemon = true } }
    val timers = object : java.util.concurrent.ScheduledThreadPoolExecutor(1) {
        private val rejectFirst = AtomicInteger(1)
        override fun schedule(command: Runnable, delay: Long, unit: TimeUnit): java.util.concurrent.ScheduledFuture<*> {
            if (rejectFirst.getAndDecrement() > 0) throw java.util.concurrent.RejectedExecutionException()
            return super.schedule(command, delay, unit)
        }
    }
    val rejectingThenWorking = object : java.util.concurrent.ThreadPoolExecutor(
        1, 1, 0L, TimeUnit.MILLISECONDS, java.util.concurrent.LinkedBlockingQueue<Runnable>()
    ) {
        private val rejectFirst = AtomicInteger(1)
        override fun execute(command: Runnable) {
            if (rejectFirst.getAndDecrement() > 0) throw java.util.concurrent.RejectedExecutionException()
            super.execute(command)
        }
    }
    val schedulerRegistry = HttpsRequestRegistry(
        HttpsCallbackDispatcher { callback -> callbackExecutor.execute(callback) },
        Executors.newSingleThreadExecutor(), timers, maxConcurrent = 1
    )
    val submitRegistry = HttpsRequestRegistry(
        HttpsCallbackDispatcher { callback -> callbackExecutor.execute(callback) },
        rejectingThenWorking, Executors.newSingleThreadScheduledExecutor(), maxConcurrent = 1
    )
    try {
        checkFailureCode(call(schedulerRegistry, request("https://127.0.0.1:$goodPort", "/success")), "unavailable")
        check(call(schedulerRegistry, request("https://127.0.0.1:$goodPort", "/success")).ok)
        checkFailureCode(call(submitRegistry, request("https://127.0.0.1:$goodPort", "/success")), "unavailable")
        check(call(submitRegistry, request("https://127.0.0.1:$goodPort", "/success")).ok)
    } finally {
        schedulerRegistry.close()
        submitRegistry.close()
        callbackExecutor.shutdownNow()
    }
}

private fun readHits(file: File): List<Hit> = if (!file.exists()) emptyList() else file.readLines().map { line ->
    val fields = StrictJson.parseFlatObject(line, 2048, setOf("server", "path", "cookiePresent", "method", "authorizationPresent", "contentLength"))
    Hit(
        (fields["server"] as JsonScalar.Text).value,
        (fields["path"] as JsonScalar.Text).value,
        (fields["cookiePresent"] as JsonScalar.Bool).value,
        (fields["method"] as JsonScalar.Text).value,
        (fields["authorizationPresent"] as JsonScalar.Bool).value,
        (fields["contentLength"] as JsonScalar.Text).value
    )
}
