package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Rule;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.dataformat.yaml.YAMLFactory;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

/**
 * Reads the same {@code rules.yaml} the Go service reads. Decoding is strict: a misspelt key fails at load time
 * instead of silently widening a rule to match everything. Keys the Java service does not support yet
 * ({@code payloadMatch}, {@code channels}) are therefore refused rather than ignored.
 */
public final class RulesLoader {
    private static final ObjectMapper YAML = new ObjectMapper(new YAMLFactory())
            .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, true);

    private RulesLoader() {}

    /** Thrown when the rules file cannot be read or is not valid. */
    public static final class InvalidRulesException extends RuntimeException {
        public InvalidRulesException(String message, Throwable cause) {
            super(message, cause);
        }

        public InvalidRulesException(String message) {
            super(message);
        }
    }

    private record RulesFile(List<Rule> rules) {}

    public static List<Rule> loadFile(Path path) {
        try {
            return parse(Files.readString(path, StandardCharsets.UTF_8));
        } catch (IOException e) {
            throw new InvalidRulesException("read rules file: " + e.getMessage(), e);
        }
    }

    public static List<Rule> parse(String yaml) {
        RulesFile file;
        try {
            file = YAML.readValue(yaml, RulesFile.class);
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            throw new InvalidRulesException("parse rules yaml: " + e.getOriginalMessage(), e);
        }
        if (file == null || file.rules() == null || file.rules().isEmpty()) {
            throw new InvalidRulesException("rules file contains no rules");
        }
        List<Rule> rules = file.rules();
        for (int i = 0; i < rules.size(); i++) {
            validate(i, rules.get(i));
        }
        return rules;
    }

    private static void validate(int index, Rule rule) {
        if (rule == null || rule.name() == null || rule.name().isEmpty()) {
            throw new InvalidRulesException("rule at index " + index + " missing name");
        }
        String name = rule.name();
        if (rule.then() == null || rule.then().attentionClass() == null || rule.then().attentionClass().isEmpty()) {
            throw new InvalidRulesException("rule \"" + name + "\" missing attentionClass");
        }
        // A typo such as "critcal" would silently demote a rule to non-critical.
        if (!Attention.isKnown(rule.then().attentionClass())) {
            throw new InvalidRulesException("rule \"" + name + "\" has unknown attentionClass \""
                    + rule.then().attentionClass() + "\"");
        }
        for (String pattern : List.of(rule.when().source(), rule.when().eventType(), rule.when().repository())) {
            String problem = Glob.validate(pattern);
            if (problem != null) {
                throw new InvalidRulesException("rule \"" + name + "\" has an invalid pattern \"" + pattern
                        + "\": " + problem);
            }
        }
    }
}
