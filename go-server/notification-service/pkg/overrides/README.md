# overrides

The "always notify" and "never notify" choices that come from feedback. They are plain rules in the deterministic layer, shown in full and removable.

- `Override` — `source`, `eventType`, `repository` (`*` allowed, like `acme/*`; an empty field matches anything), a `mode` and who created it.
- `Service.Decide(event)` — answers for **non-critical events only**. The most specific match wins; on a tie "always" wins. The pipeline applies it before quiet hours and mutes: "never" sends the event to the digest, "always" keeps it deliverable.
- `Upsert` replaces an override with the same scope; `Delete`; `List` (newest first). Decisions come from a per-process cache loaded at start and updated on change.
- `Store` — `MemoryStore` and `PostgresStore` (table `overrides`, unique per scope).

Tests cover validation, matching, critical immunity, specificity, ties and reload.
