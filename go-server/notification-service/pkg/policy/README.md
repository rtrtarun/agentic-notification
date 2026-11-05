# policy

Layer 1 rule engine. `Engine.Evaluate` returns the first rule matching source, event type and repository. Rules load from YAML (`LoadRulesFromFile`); `ParseRules` rejects unknown attention classes so a typo cannot silently demote a critical rule.

Since milestone 18: the engine can be reloaded at run time (`SetRules`, safe for concurrent use), rules may be `disabled` or `locked`, condition fields accept `*` wildcards (`acme/*`; `*` does not cross `/`), and `Describe` builds the read-only summary shown on the Policies screen. Editing lives in `pkg/rules`.
