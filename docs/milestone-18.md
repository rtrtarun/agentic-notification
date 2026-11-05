# Milestone 18: Rules and Policies

## Goals
Let people change how events are classified without a redeploy, safely: a mistake must never silence a security alert.

## Architecture decisions
- New package `pkg/rules` (service, `Store`, memory and PostgreSQL stores, dry run). `policy.Engine` gained `SetRules` (mutex-guarded), `disabled`/`locked` rule flags and `*` wildcards (`path.Match`, so `*` does not cross `/`). See the [rules README](../go-server/notification-service/pkg/rules/README.md).
- **Locked rules** are the safety net. They come from the built-in defaults or `locked: true` in `config/rules.yaml` (the two critical rules are locked). They cannot be edited, disabled or removed (409), run before every unlocked rule (reorder rejects anything else), and the `locked` flag in a request body is ignored, so the API cannot create one.
- The store is seeded from the rules file or defaults when empty; afterwards the stored rules win. A locked rule in the file that is missing from the store is added back at start-up.
- Every change replaces the whole list in one transaction and is pushed to the engine immediately; the next event uses it. A test drives the real pipeline to prove it.
- **Policies** is read only: `policy.Describe` reports the guardrails (fixed statements of how the pipeline already behaves), what each class gets, the live dedup window, retention and rule counts. Critical is never AI eligible.
- The dry run (`POST /api/rules/test`) returns every rule's part in the decision and whether the outcome bypasses AI. Nothing is stored or delivered.
- Limits: 100 rules, name 80, description 300, reason 200, pattern 100 characters. Duplicate names (case-insensitive) are rejected.

## Database (idempotent, in `pkg/db/migrate.go`)
`policy_rules(position INT, id TEXT PRIMARY KEY, body JSONB)`.

## API
All need the API key. Errors are `{"error": "..."}`: 400 invalid, 404 unknown id, 409 locked.

| Method and path | Purpose |
|---|---|
| `GET /api/rules` | `{rules: [...]}` in evaluation order |
| `POST /api/rules` | create (201), appended last |
| `PUT /api/rules/{id}` | replace an unlocked rule (id and position kept) |
| `DELETE /api/rules/{id}` | remove an unlocked rule (204) |
| `POST /api/rules/reorder` | `{ids: [...]}` every id once, locked first |
| `POST /api/rules/test` | `{source, eventType, repository}` returns `{matched, attentionClass, reason, matchedRule, aiBypassed, steps[]}` |
| `GET /api/policies` | guardrails, classes, `dedupWindowSeconds`, `retentionHours`, `lockedRules`, counts |

## Frontend
`#/rules`: ordered table with on/off switch, move up/down, edit form with early validation, delete confirmation, and the "Try an event" tester. `#/policies`: stat tiles, guardrails, class table, locked rules. `lib/rules.ts` has HTTP and demo clients; the demo client enforces the same guards.

## Risks
- A person can still make a bad non-critical rule (for example send everything to background). That is their policy; the tester and the "Decided" trace make it visible. Critical routing cannot be touched.
- Stored rules win over the file after first start; editing `rules.yaml` alone will not change an existing install (except locked rules being added back). Documented in the runbook section below.
- Concurrent edits from two browsers: last write wins (the list is replaced under a service-level lock; no version check yet).

## Tests
Go: `pkg/rules` (seed, merge of missing locked rules, create/update/delete, forged lock, validation, locked protection, reorder rules, max rules, trace and wildcard evaluation), `pkg/policy` (summary), `pkg/server` (CRUD, locked over HTTP, validation and methods, reorder and test, policies, auth, and an end-to-end test that an edited rule changes the real pipeline's decision). Web: `lib/rules.test.ts` (10). 155 Vitest tests; `go test ./...` passes.

## Acceptance criteria
- [x] Rules can be created, edited, disabled, reordered and deleted from the console and the API, and apply to the next event.
- [x] Locked rules cannot be weakened by any request.
- [x] A dry run explains which rule decides.
