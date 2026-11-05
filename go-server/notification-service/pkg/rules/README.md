# rules

Manage the Layer 1 policy rules while the service runs. No AI here.

- `Service` lists, creates, updates, deletes, reorders and dry-runs rules (`Test` / `Evaluate`).
- `Store` keeps the ordered list: `MemoryStore`, or `PostgresStore` (table `policy_rules`, replaced in one transaction).
- Every change is handed to the engine through `Applier` (`*policy.Engine.SetRules`), so it affects the next event with no restart.
- **Locked rules** (set by the built-in defaults or `locked: true` in the rules file) cannot be edited, disabled or removed, always run first, and cannot be forged through the API.
- On start-up the store is seeded from the rules file or defaults. After that the stored rules win; a locked rule that is in the file but missing from the store is added back.
