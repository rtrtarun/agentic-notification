package com.agentpulse.notification.application;

import com.fasterxml.jackson.core.JsonFactory;
import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonToken;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

/**
 * The parts of Go's {@code encoding/json} the MCP server's answers depend on, so the Java service gives the same
 * bytes: raw field values ({@code json.RawMessage}), Go's key matching (without regard to case, the last match wins)
 * and Go's compaction of a raw value when it is written back (whitespace removed, {@code < > &} and U+2028/U+2029
 * escaped).
 */
final class RawJson {
    static final ObjectMapper STRICT = new ObjectMapper().enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS);
    private static final JsonFactory FACTORY = STRICT.getFactory();

    private RawJson() {}

    /** One member of an object: its key as written and the exact bytes of its value. */
    record Member(String key, byte[] value) {}

    /** Parses a complete JSON text as Go's json.Unmarshal would accept it; null when it is not valid JSON. */
    static JsonNode parse(byte[] json) {
        try {
            JsonNode node = STRICT.readTree(json);
            return node == null || node.isMissingNode() ? null : node;
        } catch (IOException e) {
            return null;
        }
    }

    /** The members of a JSON object in document order, duplicates included; null when the value is not an object. */
    static List<Member> members(byte[] object) {
        try (JsonParser p = FACTORY.createParser(object)) {
            if (p.nextToken() != JsonToken.START_OBJECT) {
                return null;
            }
            List<Member> out = new ArrayList<>();
            while (p.nextToken() == JsonToken.FIELD_NAME) {
                String key = p.currentName();
                JsonToken value = p.nextToken();
                int start = (int) p.currentTokenLocation().getByteOffset();
                if (value == JsonToken.START_OBJECT || value == JsonToken.START_ARRAY) {
                    p.skipChildren();
                } else if (value == JsonToken.VALUE_STRING) {
                    p.getText(); // strings are read lazily; finish this one so the location is after it
                }
                int end = (int) p.currentLocation().getByteOffset();
                out.add(new Member(key, Arrays.copyOfRange(object, start, end)));
            }
            return out;
        } catch (IOException e) {
            return null;
        }
    }

    /**
     * The value Go's json.Unmarshal puts in the struct field {@code name}: keys match without regard to case and the
     * last match wins. Null when no key matches.
     */
    static byte[] field(List<Member> members, String name) {
        byte[] found = null;
        for (Member m : members) {
            if (m.key().equalsIgnoreCase(name)) {
                found = m.value();
            }
        }
        return found;
    }

    /** Whether the raw value is JSON null. */
    static boolean isNull(byte[] raw) {
        return raw != null && new String(raw, StandardCharsets.UTF_8).equals("null");
    }

    /** A raw value written back as Go writes a json.RawMessage: compacted, with HTML-sensitive characters escaped. */
    static String compact(byte[] raw) {
        String s = new String(raw, StandardCharsets.UTF_8);
        StringBuilder out = new StringBuilder(s.length());
        boolean inString = false;
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            if (inString) {
                if (c == '\\') {
                    out.append(c).append(s.charAt(++i));
                    continue;
                }
                if (c == '"') {
                    inString = false;
                }
                switch (c) {
                    case '<' -> out.append("\\u003c");
                    case '>' -> out.append("\\u003e");
                    case '&' -> out.append("\\u0026");
                    case ' ' -> out.append("\\u2028");
                    case ' ' -> out.append("\\u2029");
                    default -> out.append(c);
                }
            } else if (c == '"') {
                inString = true;
                out.append(c);
            } else if (c != ' ' && c != '\t' && c != '\n' && c != '\r') {
                out.append(c);
            }
        }
        return out.toString();
    }
}
