package uts.sdk.modules.neraeSecureVault

import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import java.math.BigDecimal
import java.nio.CharBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.security.MessageDigest

private sealed interface J
private data class JO(val fields: Map<String, J>) : J
private data class JA(val items: List<J>) : J
private data class JS(val value: String) : J
private data class JN(val raw: String) : J
private data class JB(val value: Boolean) : J
private data object JNull : J

/** Strict bounded JSON parser: rejects duplicate keys, malformed Unicode, trailing bytes, and excessive depth. */
private class StrictJson(private val source: String) {
    private var i = 0
    fun parse(): J {
        require(source.toByteArray(UTF8).size <= MAX_JSON_BYTES) { "size" }
        val result = value(0)
        ws()
        require(i == source.length) { "trailing" }
        return result
    }
    private fun value(depth: Int): J {
        require(depth <= MAX_DEPTH) { "depth" }; ws(); require(i < source.length) { "eof" }
        return when (source[i]) {
            '{' -> obj(depth + 1)
            '[' -> array(depth + 1)
            '"' -> JS(string())
            't' -> { literal("true"); JB(true) }
            'f' -> { literal("false"); JB(false) }
            'n' -> { literal("null"); JNull }
            '-', in '0'..'9' -> number()
            else -> error("value")
        }
    }
    private fun obj(depth: Int): JO {
        i++; ws(); val fields = LinkedHashMap<String, J>()
        if (take('}')) return JO(fields)
        while (true) {
            ws(); require(i < source.length && source[i] == '"') { "object key" }
            val key = string(); require(!fields.containsKey(key)) { "duplicate key" }
            ws(); require(take(':')) { "colon" }; fields[key] = value(depth); ws()
            if (take('}')) return JO(fields)
            require(take(',')) { "comma" }
        }
    }
    private fun array(depth: Int): JA {
        i++; ws(); val values = ArrayList<J>()
        if (take(']')) return JA(values)
        while (true) { values += value(depth); ws(); if (take(']')) return JA(values); require(take(',')) { "comma" } }
    }
    private fun string(): String {
        require(take('"')); val out = StringBuilder()
        while (i < source.length) {
            val c = source[i++]
            when {
                c == '"' -> { val s = out.toString(); strictUtf8(s); return s }
                c == '\\' -> {
                    require(i < source.length); when (val e = source[i++]) {
                        '"', '\\', '/' -> out.append(e)
                        'b' -> out.append('\b'); 'f' -> out.append('\u000c'); 'n' -> out.append('\n')
                        'r' -> out.append('\r'); 't' -> out.append('\t')
                        'u' -> { require(i + 4 <= source.length); val n = source.substring(i, i + 4).toIntOrNull(16) ?: error("unicode escape"); out.append(n.toChar()); i += 4 }
                        else -> error("escape")
                    }
                }
                c.code < 0x20 -> error("control")
                else -> out.append(c)
            }
        }
        error("unterminated string")
    }
    private fun number(): JN {
        val start = i
        if (source[i] == '-') i++
        require(i < source.length)
        if (source[i] == '0') { i++; require(i == source.length || source[i] !in '0'..'9') }
        else { require(source[i] in '1'..'9'); while (i < source.length && source[i] in '0'..'9') i++ }
        if (i < source.length && source[i] == '.') { i++; require(i < source.length && source[i] in '0'..'9'); while (i < source.length && source[i] in '0'..'9') i++ }
        if (i < source.length && source[i] in "eE") { i++; if (i < source.length && source[i] in "+-") i++; require(i < source.length && source[i] in '0'..'9'); while (i < source.length && source[i] in '0'..'9') i++ }
        return JN(source.substring(start, i))
    }
    private fun literal(value: String) { require(source.startsWith(value, i)); i += value.length }
    private fun ws() { while (i < source.length && source[i] in " \t\r\n") i++ }
    private fun take(c: Char): Boolean = if (i < source.length && source[i] == c) { i++; true } else false
    companion object { private const val MAX_JSON_BYTES = 65_536; private const val MAX_DEPTH = 64; private val UTF8 = StandardCharsets.UTF_8 }
}

internal fun strictJsonObject(value: String): Boolean = try { StrictJson(value).parse() is JO } catch (_: Exception) { false }

private fun strictUtf8(value: String): ByteArray = StandardCharsets.UTF_8.newEncoder()
    .onMalformedInput(CodingErrorAction.REPORT).onUnmappableCharacter(CodingErrorAction.REPORT)
    .encode(CharBuffer.wrap(value)).let { b -> ByteArray(b.remaining()).also { b.get(it) } }
private fun JO.str(key: String) = (fields[key] as? JS)?.value
private fun JO.obj(key: String) = fields[key] as? JO
private fun JO.num(key: String): Long? = (fields[key] as? JN)?.raw?.takeIf { Regex("0|[1-9][0-9]{0,15}").matches(it) }?.toLongOrNull()?.takeIf { it <= 9_007_199_254_740_991L }
private fun JO.exactKeys(vararg keys: String) = fields.keys == keys.toSet()
private fun validGeneration(s: String?) = s != null && s.length in 1..128 && s.none { it.isWhitespace() }
private fun scalarCount(s: String) = s.codePointCount(0, s.length)

internal data class NativeDispatchIdentity(val eventMarker: String, val keyMarker: String, val bodyMarker: String, val binding: String)

/** Strict, storage-independent parser used by the Android native journal itself. */
internal object FirstDispatchNativeParser {
    private data class Transition(val subject: String, val path: String, val idempotencyKey: String, val body: String, val normalizedBody: String)
    fun derive(oldText: String, nextText: String, key: String): NativeDispatchIdentity {
        val transition = parseTransition(oldText, nextText, key)
        return NativeDispatchIdentity(marker("event", key),
            marker("key", transition.subject, transition.path, transition.idempotencyKey),
            marker("body-binary64-v2", transition.subject, transition.path, transition.normalizedBody),
            marker("binding-v1", key, transition.subject, transition.path, transition.idempotencyKey, transition.body))
    }
    private fun parseTransition(oldText: String, nextText: String, key: String): Transition {
        require(strictUtf8(oldText).size in 1..MAX_JSON_BYTES && strictUtf8(nextText).size in 1..MAX_JSON_BYTES)
        val oldRoot = StrictJson(oldText).parse() as? JO ?: error("registry")
        val newRoot = StrictJson(nextText).parse() as? JO ?: error("registry")
        require(oldRoot.exactKeys("schema", "entries") && newRoot.exactKeys("schema", "entries"))
        require(oldRoot.fields["schema"] == JN("1") && newRoot.fields["schema"] == JN("1"))
        val oldEntries = oldRoot.obj("entries") ?: error("entries"); val newEntries = newRoot.obj("entries") ?: error("entries")
        require(oldEntries.fields.size <= MAX_ENTRIES && newEntries.fields.size <= MAX_ENTRIES && oldEntries.fields.keys == newEntries.fields.keys)
        oldEntries.fields.values.forEach { entry ->
            val encoded = (entry as? JS)?.value ?: error("entry must be a string")
            require(StrictJson(encoded).parse() is JO) { "entry must be an operation object" }
        }
        val oldEntry = oldEntries.fields[key] as? JS ?: error("target absent")
        val newEntry = newEntries.fields[key] as? JS ?: error("target absent")
        for (name in oldEntries.fields.keys) if (name != key) require(oldEntries.fields[name] == newEntries.fields[name]) { "other entry changed" }
        val before = StrictJson(oldEntry.value).parse() as? JO ?: error("operation")
        val after = StrictJson(newEntry.value).parse() as? JO ?: error("operation")
        val oldKeys = setOf("schema", "subjectId", "clientEventId", "createdGeneration", "revision", "state", "createdAt", "expiresAt", "request")
        require(before.fields.keys == oldKeys && after.fields.keys == oldKeys + "attemptGeneration")
        require(before.fields["schema"] == JN("1") && after.fields["schema"] == JN("1"))
        require(before.str("state") == "ready" && before.num("revision") == 1L && before.fields["attemptGeneration"] == null)
        require(after.str("state") == "in_flight" && after.num("revision") == 2L && validGeneration(after.str("attemptGeneration")))
        val subject = before.str("subjectId") ?: error("subject")
        val event = before.str("clientEventId") ?: error("event")
        require(Regex("[1-9][0-9]{0,19}").matches(subject) && scalarCount(event) in 8..64 && event.isNotBlank())
        require(key == "nerae.workout.operation.v1.$subject.${encodeURIComponent(event)}")
        require(before.str("createdGeneration") == after.str("createdGeneration") && validGeneration(before.str("createdGeneration")))
        require(before.num("createdAt") != null && before.num("createdAt") == after.num("createdAt"))
        require(before.num("expiresAt") != null && before.num("expiresAt") == after.num("expiresAt") && before.num("expiresAt")!! > before.num("createdAt")!!)
        val request = before.obj("request") ?: error("request"); val nextRequest = after.obj("request") ?: error("request")
        require(request.exactKeys("method", "path", "idempotencyKey", "body") && request.fields == nextRequest.fields)
        require(request.str("method") == "POST")
        val path = request.str("path") ?: error("path"); require(path == "/me/sessions" || Regex("/me/sessions/[1-9][0-9]{0,19}/(sets|finish)").matches(path))
        val idem = request.str("idempotencyKey") ?: error("idempotency"); require(Regex("[A-Za-z0-9._~-]{8,128}").matches(idem))
        val body = request.str("body") ?: error("body"); val bodyObject = StrictJson(body).parse() as? JO ?: error("body object")
        validateBody(path, event, bodyObject)
        val expected = LinkedHashMap(before.fields); expected["state"] = JS("in_flight"); expected["revision"] = JN("2"); expected["attemptGeneration"] = after.fields["attemptGeneration"]
        require(expected == after.fields) { "non-target field changed" }
        return Transition(subject, path, idem, body, canonicalBodyMarker(bodyObject))
    }
    private fun validateBody(path: String, event: String, body: JO) {
        fun allowed(required: Set<String>, all: Set<String>) = required.all { it in body.fields.keys } && body.fields.keys.all { it in all }
        fun str(k: String) = body.str(k)
        fun integer(k: String): Long? = (body.fields[k] as? JN)?.raw?.let { raw ->
            raw.toDoubleOrNull()?.takeIf { it.isFinite() && it % 1.0 == 0.0 && it >= 0 && it <= 9_007_199_254_740_991.0 }?.toLong()
        }
        fun keyText(v: J?): String? = (v as? JS)?.value?.takeIf { scalarCount(it) in 8..64 && it.isNotBlank() && '\r' !in it && '\n' !in it }
        if (path == "/me/sessions") {
            val date = str("date") ?: error("date must be a string")
            require(validDate(date) && str("clientEventId") == event)
            if ("timeZone" in body.fields) {
                val timeZone = str("timeZone") ?: error("timeZone must be a string")
                require(scalarCount(timeZone) in 1..64)
            }
            when (str("source")) {
                "free" -> require(allowed(setOf("source", "date", "clientEventId"), setOf("source", "date", "clientEventId", "timeZone")))
                "plan" -> {
                    require(allowed(setOf("source", "date", "clientEventId", "planId", "planVersion", "planDayId"), setOf("source", "date", "clientEventId", "timeZone", "planId", "planVersion", "planDayId", "occurrenceId")))
                    require(str("planId")?.matches(Regex("[1-9][0-9]{0,19}")) == true && str("planDayId")?.matches(Regex("[1-9][0-9]{0,19}")) == true)
                    require(integer("planVersion")?.let { it > 0 } == true)
                    if ("occurrenceId" in body.fields) {
                        val occurrence = str("occurrenceId") ?: error("occurrenceId must be a string")
                        require(occurrence.matches(Regex("[1-9][0-9]{0,19}")))
                    }
                }
                else -> error("create source")
            }
            return
        }
        if (path.endsWith("/sets")) {
            require(allowed(setOf("expectedVersion", "exerciseEntryKeys"), setOf("expectedVersion", "exerciseEntryKeys", "sets", "deleteRecordKeys", "restoreRecordKeys")))
            require(integer("expectedVersion")?.let { it > 0 } == true)
            val entryKeys = (body.fields["exerciseEntryKeys"] as? JA)?.items?.map { keyText(it) ?: error("entry key") } ?: error("entry keys")
            require(entryKeys.size <= 2000 && entryKeys.distinct().size == entryKeys.size)
            val sets = body.fields["sets"]?.let { it as? JA ?: error("sets") }?.items ?: emptyList()
            val deletes = body.fields["deleteRecordKeys"]?.let { (it as? JA)?.items?.map { v -> keyText(v) ?: error("delete key") } ?: error("deletes") } ?: emptyList()
            val restores = body.fields["restoreRecordKeys"]?.let { (it as? JA)?.items?.map { v -> keyText(v) ?: error("restore key") } ?: error("restores") } ?: emptyList()
            require((body.fields["sets"] == null || sets.size in 1..200) &&
                (body.fields["deleteRecordKeys"] == null || deletes.size in 1..200) &&
                (body.fields["restoreRecordKeys"] == null || restores.size in 1..200) &&
                sets.size + deletes.size <= 200)
            require(deletes.distinct().size == deletes.size && restores.distinct().size == restores.size && (restores.isEmpty() || sets.isNotEmpty()))
            val records = HashSet<String>(); val indexes = HashSet<String>(); val identities = HashMap<String, String>()
            sets.forEach { value ->
                val set = value as? JO ?: error("set object")
                val allowedSet = setOf("completed", "exerciseEntryKey", "exerciseId", "exerciseName", "kind", "recordKey", "reps", "rpe", "seconds", "setIndex", "weightKg")
                val requiredSet = setOf("recordKey", "exerciseEntryKey", "setIndex", "kind")
                require(set.fields.keys.containsAll(requiredSet) && set.fields.keys.all { it in allowedSet })
                val record = keyText(set.fields["recordKey"]) ?: error("record key")
                val entry = keyText(set.fields["exerciseEntryKey"]) ?: error("exercise entry")
                val index = (set.fields["setIndex"] as? JN)?.raw?.toLongOrNull()?.takeIf { it in 1..9_007_199_254_740_991L } ?: error("set index")
                require(records.add(record) && entry in entryKeys)
                val hasExerciseId = "exerciseId" in set.fields
                val hasExerciseName = "exerciseName" in set.fields
                val exerciseId = (set.fields["exerciseId"] as? JS)?.value
                val exerciseName = (set.fields["exerciseName"] as? JS)?.value
                require(hasExerciseId xor hasExerciseName)
                if (hasExerciseId) require(exerciseId?.matches(Regex("[1-9][0-9]{0,19}")) == true)
                if (hasExerciseName) require(exerciseName != null && scalarCount(exerciseName) in 1..60 && exerciseName.isNotBlank())
                val identity = exerciseId?.let { "id:$it" } ?: "name:$exerciseName"
                require(identities.putIfAbsent(entry, identity).let { it == null || it == identity })
                require(indexes.add("$entry:$index"))
                require(set.fields["completed"] == JB(true))
                val kind = set.str("kind") ?: error("kind")
                require(kind in setOf("weight", "perSide", "bodyweight", "bodyweightPerSide", "duration", "durationPerSide", "breath"))
                val weightToken = set.fields["weightKg"]
                val repsToken = set.fields["reps"]
                val secondsToken = set.fields["seconds"]
                val rpeToken = set.fields["rpe"]
                val weight = (weightToken as? JN)?.raw?.toDoubleOrNull()
                val reps = (repsToken as? JN)?.raw?.toDoubleOrNull()
                val seconds = (secondsToken as? JN)?.raw?.toDoubleOrNull()
                require(weightToken == null || weight != null && weight.isFinite() && weight > 0 && weight <= 300 && decimalPlacesAtMost(weightToken, 2))
                require(repsToken == null || reps != null && reps.isFinite() && reps % 1.0 == 0.0 && reps in 1.0..100.0)
                require(secondsToken == null || seconds != null && seconds.isFinite() && seconds % 1.0 == 0.0 && seconds in 5.0..600.0)
                require(rpeToken == null || rpeToken == JNull || rpeToken is JN && rpeToken.raw.toDoubleOrNull()?.let { it.isFinite() && it in 1.0..10.0 && decimalPlacesAtMost(rpeToken, 1) } == true)
                when (kind) {
                    "weight", "perSide" -> require(weight != null && reps != null && secondsToken == null)
                    "duration", "durationPerSide" -> require(weightToken == null && repsToken == null && seconds != null)
                    else -> require(weightToken == null && secondsToken == null && reps != null)
                }
            }
            require(deletes.none { it in records } && restores.all { it in records } && deletes.none { it in restores })
            return
        }
        if (path.endsWith("/finish")) {
            require(allowed(setOf("expectedVersion"), setOf("expectedVersion", "date", "durationMin", "note", "rpe")))
            require(integer("expectedVersion")?.let { it > 0 } == true)
            if ("date" in body.fields) require(str("date")?.let { validDate(it) } == true)
            val duration = body.fields["durationMin"]
            require(duration == null || duration == JNull || integer("durationMin") != null)
            val note = body.fields["note"]
            require(note == null || note == JNull || note is JS && scalarCount(note.value) <= 2000)
            val rpe = body.fields["rpe"]
            require(rpe == null || rpe == JNull || rpe is JN && rpe.raw.toDoubleOrNull()?.let { it.isFinite() && it in 1.0..10.0 && decimalPlacesAtMost(rpe, 1) } == true)
            return
        }
        error("unsupported path")
    }

    private fun decimalPlacesAtMost(number: J, max: Int): Boolean {
        val text = renderCanonical(number)
        val match = Regex("^-?(?:\\d+)(?:\\.(\\d+))?(?:e([+-]?\\d+))?$", RegexOption.IGNORE_CASE).matchEntire(text) ?: return false
        val fractionalDigits = match.groupValues[1].length
        val exponent = match.groupValues[2].toIntOrNull() ?: 0
        return (fractionalDigits - exponent).coerceAtLeast(0) <= max
    }

    private fun validDate(value: String): Boolean {
        if (!Regex("\\d{4}-\\d{2}-\\d{2}").matches(value)) return false
        val year = value.substring(0, 4).toIntOrNull() ?: return false
        val month = value.substring(5, 7).toIntOrNull() ?: return false
        val day = value.substring(8, 10).toIntOrNull() ?: return false
        if (year < 1 || month !in 1..12) return false
        val leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)
        val days = when (month) { 2 -> if (leap) 29 else 28; 4, 6, 9, 11 -> 30; else -> 31 }
        return day in 1..days
    }

    private fun renderCanonical(value: J): String = when (value) {
        is JO -> value.fields.keys.sorted().joinToString(separator = ",", prefix = "{", postfix = "}") { quoteJson(it) + ":" + renderCanonical(value.fields.getValue(it)) }
        is JA -> value.items.joinToString(separator = ",", prefix = "[", postfix = "]") { renderCanonical(it) }
        is JS -> quoteJson(value.value)
        is JN -> canonicalNumber(value.raw)
        is JB -> value.value.toString()
        JNull -> "null"
    }
    /** Stable typed structural encoding for persistent body-history hashing. */
    private fun canonicalBodyMarker(value: J): String {
        val bytes = ByteArrayOutputStream()
        DataOutputStream(bytes).use { out -> writeCanonicalValue(out, value) }
        return bytes.toByteArray().joinToString("") { "%02x".format(it) }
    }

    private fun writeCanonicalValue(out: DataOutputStream, value: J) {
        when (value) {
            JNull -> out.writeByte(0)
            is JB -> out.writeByte(if (value.value) 2 else 1)
            is JN -> {
                val number = value.raw.toDoubleOrNull() ?: error("number")
                val mantissa = value.raw.substringBefore('e').substringBefore('E')
                require(number.isFinite() && !(number == 0.0 && mantissa.any { it in '1'..'9' })) { "unsupported number" }
                out.writeByte(3)
                // Collapse both signed zero encodings to the one ECMAScript JSON zero value.
                out.writeLong(if (number == 0.0) 0L else java.lang.Double.doubleToLongBits(number))
            }
            is JS -> { out.writeByte(4); writeLengthPrefixed(out, strictUtf8(value.value)) }
            is JA -> {
                out.writeByte(5); out.writeInt(value.items.size)
                value.items.forEach { writeCanonicalValue(out, it) }
            }
            is JO -> {
                out.writeByte(6); out.writeInt(value.fields.size)
                value.fields.keys.sorted().forEach { key ->
                    writeLengthPrefixed(out, strictUtf8(key))
                    writeCanonicalValue(out, value.fields.getValue(key))
                }
            }
        }
    }

    private fun writeLengthPrefixed(out: DataOutputStream, bytes: ByteArray) {
        out.writeInt(bytes.size); out.write(bytes)
    }

    // JSON numbers are ECMAScript Number/binary64 values in the JS journal. This
    // canonical form is only for marker comparison, never emitted as request JSON.
    // Reject non-finite/underflow values; Double.toString is deterministic and round-trips.
    private fun canonicalNumber(raw: String): String {
        val d = raw.toDoubleOrNull() ?: error("number")
        val mantissa = raw.substringBefore('e').substringBefore('E')
        val hasNonZeroMantissaDigit = mantissa.any { it in '1'..'9' }
        require(d.isFinite() && !(d == 0.0 && hasNonZeroMantissaDigit)) { "unsupported number" }
        if (d == 0.0) return "0"
        val abs = kotlin.math.abs(d)
        val shortest = BigDecimal.valueOf(d).stripTrailingZeros()
        if (abs >= 1e-6 && abs < 1e21) return shortest.toPlainString()
        val scientific = shortest.toString().lowercase()
        val pieces = scientific.split('e')
        require(pieces.size == 2) { "number form" }
        val exponent = pieces[1].toIntOrNull() ?: error("exponent")
        return pieces[0] + "e" + (if (exponent >= 0) "+" else "") + exponent
    }

    private fun quoteJson(value: String): String = buildString {
        append('"'); value.forEach { c -> when (c) {
            '"' -> append("\\\""); '\\' -> append("\\\\"); '\b' -> append("\\b"); '\u000c' -> append("\\f")
            '\n' -> append("\\n"); '\r' -> append("\\r"); '\t' -> append("\\t")
            else -> if (c.code < 0x20) append("\\u%04x".format(c.code)) else append(c)
        } }; append('"')
    }
    private fun encodeURIComponent(s: String): String {
        val safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"; val out = StringBuilder()
        strictUtf8(s).forEach { b -> val n = b.toInt() and 255; val c = n.toChar(); if (n < 128 && c in safe) out.append(c) else out.append("%" + "%02X".format(n)) }
        return out.toString()
    }
    private fun marker(kind: String, vararg parts: String): String {
        val out = ByteArrayOutputStream(); out.write("nerae.android.training-dispatch.v1\u0000$kind\u0000".toByteArray(UTF8))
        parts.forEach { val b = strictUtf8(it); out.write(byteArrayOf((b.size ushr 24).toByte(), (b.size ushr 16).toByte(), (b.size ushr 8).toByte(), b.size.toByte())); out.write(b) }
        return MessageDigest.getInstance("SHA-256").digest(out.toByteArray()).joinToString("") { "%02x".format(it) }
    }
    private val UTF8 = StandardCharsets.UTF_8
    private const val MAX_JSON_BYTES = 65_536
    private const val MAX_ENTRIES = 64
}
