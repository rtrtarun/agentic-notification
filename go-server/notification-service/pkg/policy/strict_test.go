package policy

import (
	"strings"
	"testing"
)

const validRules = `
rules:
  - name: slow-agent
    when:
      source: agent
      payloadMatch:
        - field: metadata.attempt
          op: gt
          value: 3
        - field: severity
          value: high
    then:
      attentionClass: standard
      reason: an agent keeps retrying
      channels: [slack, email]
  - name: outage
    locked: true
    when: {eventType: outage}
    then: {attentionClass: critical, reason: outage}
`

func TestStrictYAMLAcceptsPredicatesAndChannels(t *testing.T) {
	rules, err := ParseRules([]byte(validRules))
	if err != nil {
		t.Fatal(err)
	}
	r := rules[0]
	if len(r.When.PayloadMatch) != 2 || r.When.PayloadMatch[0].Op != OpGt || r.When.PayloadMatch[0].Value != 3 || strings.Join(r.Then.Channels, ",") != "slack,email" {
		t.Fatalf("rule = %+v", r)
	}
}

func TestStrictYAMLRejectsMisspeltKeysInsteadOfWideningARule(t *testing.T) {
	cases := map[string]string{
		"misspelt condition key": "rules:\n  - name: a\n    when: {eventTpye: push}\n    then: {attentionClass: standard}\n",
		"misspelt outcome key":   "rules:\n  - name: a\n    when: {source: x}\n    then: {attentionClas: standard}\n",
		"misspelt rule key":      "rules:\n  - name: a\n    whn: {source: x}\n    then: {attentionClass: standard}\n",
		"misspelt predicate key": "rules:\n  - name: a\n    when:\n      payloadMatch: [{feild: a, value: 1}]\n    then: {attentionClass: standard}\n",
		"unknown top-level key":  "rule:\n  - name: a\n",
		"misspelt channels key":  "rules:\n  - name: a\n    when: {source: x}\n    then: {attentionClass: standard, channel: [slack]}\n",
	}
	for name, doc := range cases {
		_, err := ParseRules([]byte(doc))
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), "field") && !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "parse rules yaml") {
			t.Errorf("%s: error should explain the problem: %v", name, err)
		}
	}
}

func TestParseRulesRejectsBadPredicatesAndChannels(t *testing.T) {
	cases := map[string]string{
		"unknown operator":     "rules:\n  - name: a\n    when:\n      payloadMatch: [{field: a, op: like, value: x}]\n    then: {attentionClass: standard}\n",
		"gt with text":         "rules:\n  - name: a\n    when:\n      payloadMatch: [{field: a, op: gt, value: many}]\n    then: {attentionClass: standard}\n",
		"unknown channel":      "rules:\n  - name: a\n    when: {source: x}\n    then: {attentionClass: standard, channels: [pager]}\n",
		"channels on critical": "rules:\n  - name: a\n    when: {source: x}\n    then: {attentionClass: critical, channels: [slack]}\n",
	}
	for name, doc := range cases {
		if _, err := ParseRules([]byte(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if !strings.Contains(err.Error(), `rule "a"`) {
			t.Errorf("%s: the error should name the rule: %v", name, err)
		}
	}
}

func TestEmptyAndBlankFilesStillReportNoRules(t *testing.T) {
	for _, doc := range []string{"", "rules: []\n", "# only a comment\n"} {
		if _, err := ParseRules([]byte(doc)); err == nil || !strings.Contains(err.Error(), "no rules") {
			t.Errorf("%q: got %v", doc, err)
		}
	}
}

func TestTheShippedRulesFileStillLoadsUnderStrictDecoding(t *testing.T) {
	rules, err := LoadRulesFromFile("../../config/rules.yaml")
	if err != nil {
		t.Fatalf("config/rules.yaml must load under strict decoding: %v", err)
	}
	locked := 0
	for _, r := range rules {
		if r.Locked {
			locked++
			if r.Then.AttentionClass != "critical" {
				t.Fatalf("locked rule %q is not critical", r.Name)
			}
		}
	}
	if locked == 0 {
		t.Fatal("the shipped file must keep its locked critical rules")
	}
}
