package uts.sdk.modules.neraeHttps

import android.os.Handler
import android.os.Looper
import java.net.Socket
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.ThreadFactory
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

internal fun interface HttpsCallbackDispatcher {
    fun dispatch(callback: () -> Unit)
}

private object MainLooperHttpsDispatcher : HttpsCallbackDispatcher {
    private val handler by lazy(LazyThreadSafetyMode.SYNCHRONIZED) { Handler(Looper.getMainLooper()) }

    override fun dispatch(callback: () -> Unit) {
        try { handler.post { callback() } } catch (_: Exception) { }
    }
}

private object HttpsExecutors {
    private val daemonFactory = ThreadFactory { runnable ->
        Thread(runnable, "nerae-https").apply { isDaemon = true }
    }
    val workers: ExecutorService = Executors.newFixedThreadPool(8, daemonFactory)
    val timers: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor { runnable ->
        Thread(runnable, "nerae-https-timeouts").apply { isDaemon = true }
    }
}

internal class HttpsRequestRegistry(
    private val callbackDispatcher: HttpsCallbackDispatcher,
    private val workers: ExecutorService,
    private val timers: ScheduledExecutorService,
    private val maxConcurrent: Int = 8
) : AutoCloseable {
    private val guard = Any()
    private val active = ConcurrentHashMap<String, PendingHttpsRequest>()

    fun request(payload: String, timeoutMsText: String, completion: (String) -> Unit): String {
        val id = UUID.randomUUID().toString().lowercase()
        val submittedAt = System.nanoTime()
        val timeoutMs = parseTimeout(timeoutMsText)
        if (timeoutMs == null) {
            dispatchResult(completion, HttpsWire.encode(id, HttpsWire.failure("invalid_request")))
            return id
        }
        val pending = PendingHttpsRequest(id, completion, callbackDispatcher) {
            active.remove(id)
        }.also { it.setDeadline(submittedAt + TimeUnit.MILLISECONDS.toNanos(timeoutMs.toLong())) }
        val atCapacity = synchronized(guard) {
            if (active.size >= maxConcurrent) true
            else {
                active[id] = pending
                false
            }
        }
        if (atCapacity) {
            dispatchResult(completion, HttpsWire.encode(id, HttpsWire.failure("unavailable")))
            return id
        }
        if (!pending.scheduleTimeout(timers)) return id
        if (pending.isSettled()) return id
        val future = try { workers.submit {
            if (pending.isSettled()) return@submit
            try {
                val spec = HttpsRequestSpec.parse(payload)
                if (spec.timeoutMs != timeoutMs) throw HttpsFailure("invalid_request")
                if (pending.isSettled()) return@submit
                val outcome = HttpsTransport.execute(spec, pending.deadlineNanos(), pending::registerSocket)
                pending.finish(outcome)
            } catch (failure: HttpsFailure) {
                pending.finish(HttpsWire.failure(failure.code))
            } catch (_: Exception) {
                pending.finish(HttpsWire.failure("network"))
            }
        } } catch (_: Exception) {
            pending.finish(HttpsWire.failure("unavailable"))
            return id
        }
        pending.attachFuture(future)
        return id
    }

    fun cancel(requestId: String) {
        active[requestId]?.cancel()
    }

    override fun close() {
        active.values.toList().forEach { it.cancel() }
        workers.shutdownNow()
        timers.shutdownNow()
    }

    private fun dispatchResult(completion: (String) -> Unit, result: String) {
        try { callbackDispatcher.dispatch { completion(result) } } catch (_: Exception) { }
    }

    private fun parseTimeout(value: String): Int? {
        if (!value.matches(Regex("[0-9]{1,6}"))) return null
        val timeout = value.toIntOrNull() ?: return null
        return timeout.takeIf { it in 1_000..120_000 }
    }

    private class PendingHttpsRequest(
        private val id: String,
        private val completion: (String) -> Unit,
        private val dispatcher: HttpsCallbackDispatcher,
        private val onSettled: () -> Unit
    ) {
        private val settled = AtomicBoolean(false)
        private val socket = AtomicReference<Socket?>(null)
        @Volatile private var future: Future<*>? = null
        @Volatile private var timer: ScheduledFuture<*>? = null
        @Volatile private var deadline: Long = 0L

        fun isSettled(): Boolean = settled.get()

        fun setDeadline(value: Long) {
            deadline = value
        }

        fun attachFuture(value: Future<*>) {
            future = value
            if (settled.get()) value.cancel(true)
        }

        fun scheduleTimeout(timers: ScheduledExecutorService): Boolean {
            val remaining = deadline - System.nanoTime()
            if (remaining <= 0) {
                finish(HttpsWire.failure("timeout"))
                return false
            }
            return try {
                val scheduled = timers.schedule({ finish(HttpsWire.failure("timeout")) }, remaining, TimeUnit.NANOSECONDS)
                timer = scheduled
                if (settled.get()) scheduled.cancel(false)
                true
            } catch (_: Exception) {
                finish(HttpsWire.failure("unavailable"))
                false
            }
        }

        fun deadlineNanos(): Long = deadline

        fun registerSocket(value: Socket) {
            socket.set(value)
            if (settled.get()) closeSocket()
        }

        fun cancel() {
            if (finish(HttpsWire.failure("cancelled"))) future?.cancel(true)
        }

        fun finish(outcome: HttpsOutcome): Boolean {
            if (!settled.compareAndSet(false, true)) return false
            timer?.cancel(false)
            if (outcome.code == "timeout") future?.cancel(true)
            closeSocket()
            onSettled()
            val encoded = HttpsWire.encode(id, outcome)
            try { dispatcher.dispatch { completion(encoded) } } catch (_: Exception) { }
            return true
        }

        private fun closeSocket() {
            try { socket.getAndSet(null)?.close() } catch (_: Exception) { }
        }
    }
}

@Suppress("unused")
object NeraeHttpsBridge {
    private val registry by lazy(LazyThreadSafetyMode.SYNCHRONIZED) {
        HttpsRequestRegistry(MainLooperHttpsDispatcher, HttpsExecutors.workers, HttpsExecutors.timers)
    }

    @JvmStatic
    fun request(payload: String, timeoutMsText: String, completion: (String) -> Unit): String =
        registry.request(payload, timeoutMsText, completion)

    @JvmStatic
    fun cancel(requestId: String) = registry.cancel(requestId)
}
