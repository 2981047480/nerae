package uts.sdk.modules.neraeHttps

internal sealed class JsonScalar {
    data class Text(val value: String) : JsonScalar()
    data class Number(val lexeme: String) : JsonScalar()
    data class Bool(val value: Boolean) : JsonScalar()
}

/** A deliberately small strict JSON object parser for bounded flat string/number maps. */
internal object StrictJson {
    fun parseFlatObject(source: String, maxBytes: Int, allowedKeys: Set<String>?): Map<String, JsonScalar> {
        require(source.length <= maxBytes && source.toByteArray(Charsets.UTF_8).size <= maxBytes)
        return Parser(source, allowedKeys).parse()
    }

    fun quote(value: String): String {
        val out = StringBuilder(value.length + 2).append('"')
        for (character in value) {
            when (character) {
                '"' -> out.append("\\\"")
                '\\' -> out.append("\\\\")
                '\b' -> out.append("\\b")
                '\u000c' -> out.append("\\f")
                '\n' -> out.append("\\n")
                '\r' -> out.append("\\r")
                '\t' -> out.append("\\t")
                else -> if (character.code < 0x20) {
                    out.append("\\u").append(character.code.toString(16).padStart(4, '0'))
                } else {
                    out.append(character)
                }
            }
        }
        return out.append('"').toString()
    }

    fun encodeStringObject(value: Map<String, String>): String = value.entries
        .sortedBy { it.key }
        .joinToString(prefix = "{", postfix = "}", separator = ",") { (key, item) ->
            "${quote(key)}:${quote(item)}"
        }

    private class Parser(private val input: String, private val allowedKeys: Set<String>?) {
        private var offset = 0

        fun parse(): Map<String, JsonScalar> {
            whitespace()
            expect('{')
            whitespace()
            val values = LinkedHashMap<String, JsonScalar>()
            if (peek() == '}') {
                offset++
                whitespace()
                require(offset == input.length)
                return values
            }
            while (true) {
                whitespace()
                val key = string()
                require((allowedKeys == null || key in allowedKeys) && key !in values)
                whitespace()
                expect(':')
                whitespace()
                val value = when (peek()) {
                    '"' -> JsonScalar.Text(string())
                    '-', in '0'..'9' -> JsonScalar.Number(number())
                    't' -> { literal("true"); JsonScalar.Bool(true) }
                    'f' -> { literal("false"); JsonScalar.Bool(false) }
                    else -> throw IllegalArgumentException("invalid json")
                }
                values[key] = value
                whitespace()
                when (peek()) {
                    ',' -> { offset++; continue }
                    '}' -> { offset++; break }
                    else -> throw IllegalArgumentException("invalid json")
                }
            }
            whitespace()
            require(offset == input.length)
            return values
        }

        private fun string(): String {
            expect('"')
            val out = StringBuilder()
            while (offset < input.length) {
                val character = input[offset++]
                when {
                    character == '"' -> {
                        validateSurrogates(out)
                        return out.toString()
                    }
                    character == '\\' -> {
                        require(offset < input.length)
                        when (val escaped = input[offset++]) {
                            '"', '\\', '/' -> out.append(escaped)
                            'b' -> out.append('\b')
                            'f' -> out.append('\u000c')
                            'n' -> out.append('\n')
                            'r' -> out.append('\r')
                            't' -> out.append('\t')
                            'u' -> out.append(unicodeEscape())
                            else -> throw IllegalArgumentException("invalid json")
                        }
                    }
                    character.code < 0x20 -> throw IllegalArgumentException("invalid json")
                    else -> out.append(character)
                }
            }
            throw IllegalArgumentException("invalid json")
        }

        private fun unicodeEscape(): Char {
            require(offset + 4 <= input.length)
            var result = 0
            repeat(4) {
                val digit = input[offset++].digitToIntOrNull(16) ?: throw IllegalArgumentException("invalid json")
                result = (result shl 4) or digit
            }
            return result.toChar()
        }

        private fun validateSurrogates(value: CharSequence) {
            var index = 0
            while (index < value.length) {
                val character = value[index]
                when {
                    character.isHighSurrogate() -> {
                        require(index + 1 < value.length && value[index + 1].isLowSurrogate())
                        index += 2
                    }
                    character.isLowSurrogate() -> throw IllegalArgumentException("invalid json")
                    else -> index++
                }
            }
        }

        private fun number(): String {
            val start = offset
            if (peek() == '-') offset++
            when (peek()) {
                '0' -> {
                    offset++
                    require(peek() !in '0'..'9')
                }
                in '1'..'9' -> while (peek() in '0'..'9') offset++
                else -> throw IllegalArgumentException("invalid json")
            }
            if (peek() == '.') {
                offset++
                require(peek() in '0'..'9')
                while (peek() in '0'..'9') offset++
            }
            if (peek() == 'e' || peek() == 'E') {
                offset++
                if (peek() == '+' || peek() == '-') offset++
                require(peek() in '0'..'9')
                while (peek() in '0'..'9') offset++
            }
            return input.substring(start, offset)
        }

        private fun whitespace() {
            while (peek() == ' ' || peek() == '\n' || peek() == '\r' || peek() == '\t') offset++
        }

        private fun expect(expected: Char) {
            require(peek() == expected)
            offset++
        }

        private fun literal(value: String) {
            require(input.regionMatches(offset, value, 0, value.length))
            offset += value.length
        }

        private fun peek(): Char = input.getOrElse(offset) { '\u0000' }
    }
}
