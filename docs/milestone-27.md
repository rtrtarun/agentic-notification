# Milestone 27: Real-service and PostgreSQL verification

## Goals
- Close the verification gap recorded for milestones 22 to 26: stores that were only run in memory, and model adapters that were only run through unit fakes.
- Make "does it work against a real PostgreSQL" a repeatable check, not a promise.

## Architecture decisions
- **One contract per store, two runs.** Each store's expected behaviour is written once as a contract function (`storeContract`, `digestStoreContract`, `lifecycleContract`). It runs against the in-memory store in the normal `go test` (so the contract itself is proven correct and costs nothing), and against PostgreSQL in an `integration`-tagged test. A store that drifts from its memory twin fails the same assertions.
- **`pkg/db/dbtest`** opens a migrated database for those tests, skips when `TEST_DATABASE_URL` is unset, and clears named tables. It is test support only.
- **CI already provides the database.** `.github/workflows/ci.yml` starts PostgreSQL 16, sets `TEST_DATABASE_URL` and runs `go test -race -tags integration`, so these tests run on every push without any workflow change.
- **Model adapters are checked on the wire.** The replay tests send real requests through the real `ollama.Completer` and `claude.Client` to a local server that replays recorded response shapes, then assert on what the server received and on breaker and rate-cap behaviour.

## Database changes
None. The audit found every table and lookup index for milestones 22 to 25 already created by idempotent statements; a new unit test now fails if any is removed.

## APIs
None.

## Folder changes
New `pkg/db/dbtest`; `*_contract_test.go` and `*_integration_test.go` files in `pkg/auth`, `pkg/feedback`, `pkg/overrides`, `pkg/attention` and `pkg/delivery`; `pkg/attention/llm/adapter_replay_test.go`.

## What is verified
| Area | Check |
|---|---|
| Migrations | All eight tables (`delivery_jobs`, `users`, `user_identities`, `sessions`, `audit_log`, `feedback`, `overrides`, `digests`) and six lookup indexes exist; every statement is idempotent; running twice issues identical statements; `overrides.scope_key` stays unique |
| `pkg/delivery` | Retry with doubling backoff, dead-letter after the last attempt, manual retry resets attempts, a sent job cannot be retried, an expired lease makes a job claimable again with a second attempt counted, four competing workers claim each of 30 jobs exactly once (this is what `FOR UPDATE SKIP LOCKED` is for) |
| `pkg/auth` | User creation, duplicate email refused, lookups, active-admin count, update, identity linking and sign-in by identity (relinking is harmless), session create, lookup, expiry clean-up, delete and sign-out-everywhere, audit trail newest first with limit and all fields |
| `pkg/feedback` | Attribution (user, scope, reason, override, source, type, repository) survives storage; filters by event, person and time; newest first; limit |
| `pkg/overrides` | Scope stored with author and note; saving the same scope replaces the mode instead of adding a second override; repository wildcards coexist; delete removes only the target |
| `pkg/attention` | A digest round-trips with groups, event IDs, highlights, author and window; list is newest first and limited; a missing digest is `ErrDigestNotFound` |
| `ollama` and `claude` adapters | Classification through each real client; what the model receives has no comment bodies, descriptions, emails, tokens, secret URLs or private fields, and keeps the useful facts; critical events produce zero requests (classify, explain and summarise); breaker opens after three failures, stops calling, then a healthy probe closes it; the rate cap stops requests without calling the model; escalation, prose and wrong-shape answers never reach an event |

## Risks
| Risk | Handling |
|---|---|
| A PostgreSQL store behaves differently from memory | Same contract, both runs |
| Integration tests rot because nobody runs them | CI runs them on every push |
| Tests depend on timing | Clocks are injected; the only wait is a bounded `WaitGroup` |

## Tests
See the table above. All of it also runs without a database except the `integration` files.

## Acceptance criteria
- [x] Migrations audited and guarded by a test.
- [x] Contract tests pass against the in-memory stores (`go test ./...`).
- [x] Replay tests pass for both adapters.
- [x] Integration files compile (`go vet -tags integration ./...`).
- [x] **Integration tests pass against PostgreSQL**, confirmed in CI run 42356002884 on commit 367d735 (PostgreSQL 16 service), after two defects the in-memory runs could not see were fixed: overrides could not be saved in PostgreSQL, and an event integration test used `TRUNCATE` across a foreign key. They were not run on the development machine.

## Known limits
- The real Ollama and real Claude services were not contacted; the replay server follows their documented request shapes. A one-off run against each, with the status endpoint checked, is still recommended before depending on them.
- Real SNS delivery and real Entra ID sign-in were not exercised in this milestone.
- Coverage of 80 percent is not measured by CI and was not measured here.

## CI result
The first CI run on this branch failed at lint, then at the integration tests (the two defects above); after the fixes the whole Go job passed: format, vet, golangci-lint, govulncheck, `go test -race -tags integration` against PostgreSQL 16 and Redis 7, and the 80 percent coverage gate (run 42356002884, commit 367d735). The Java job fails and is unrelated.
