package org.json;

import java.util.LinkedHashMap;
import java.util.Map;

/** Minimal host-only stand-in for testing the bridge's redacted result JSON.
 *  The Kotlin production source is compiled against Android API 35 first.
 *  This does not exercise Android's JSONObject or device runtime. */
public final class JSONObject {
    private final Map<String, Object> values = new LinkedHashMap<>();

    public JSONObject put(String key, boolean value) { values.put(key, value); return this; }
    public JSONObject put(String key, Object value) { values.put(key, value); return this; }

    private static String quote(String value) {
        StringBuilder result = new StringBuilder("\"");
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c == '"' || c == '\\') result.append('\\').append(c);
            else if (c < 0x20) result.append(String.format("\\u%04x", (int)c));
            else result.append(c);
        }
        return result.append('"').toString();
    }

    @Override public String toString() {
        StringBuilder result = new StringBuilder("{");
        for (Map.Entry<String, Object> entry : values.entrySet()) {
            if (result.length() > 1) result.append(',');
            result.append(quote(entry.getKey())).append(':');
            Object value = entry.getValue();
            if (value instanceof String) result.append(quote((String)value));
            else if (value instanceof Boolean) result.append(value);
            else throw new IllegalArgumentException("unexpected test value");
        }
        return result.append('}').toString();
    }
}
