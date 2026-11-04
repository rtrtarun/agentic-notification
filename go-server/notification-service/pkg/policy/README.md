# policy

Layer 1 rule engine. `Engine.Evaluate` returns the first rule matching source, event type and repository. Rules load from YAML (`LoadRulesFromFile`); `ParseRules` rejects unknown attention classes so a typo cannot silently demote a critical rule.
