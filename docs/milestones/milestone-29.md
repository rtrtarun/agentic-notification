# Milestone 29: Asynchronous attention worker and attention budgets

## Goals
- Make `queued`, `delayed` and `budget_exceeded` real lifecycle states with an owner, instead of labels.
- Move the slow, optional part of handling an event (model classification, preferences, budget, delivery) out of the ingestion request when asked to.
- Cap how often standard notifications interrupt people, holding the rest for the digest rather than dropping them.
- Release events held for quiet hours when quiet hours end.

## Architecture decisions
- **One place decides.** The pipeline gained `attend` (classify, preferences, budget) and `settle` (final status, queue delivery). `Handle` calls them at ingest in the default mode; the worker calls them through `ProcessPending` and `ReleaseDelayed`. The rules cannot drift between the two paths.
- **`pkg/attention/worker.go`** (`Worker`) depends on two small interfaces: `event.WorkQueue` (the store) and `attention.Processor` (the pipeline). It polls (`ATTENTION_WORKER_INTERVAL`, default 2s), claims a batch with a lease, decides, and stores the decision. It owns no goroutines; the server runs it under its existing `WaitGroup` and it returns when the context is cancelled.
- **The work queue is the events table.** New columns `attention_state` (`pending`, `processing`, `done`), `attention_lease_until`, `attention_attempts`, and two partial indexes. `ClaimPending` is one `UPDATE ... WHERE id IN (SELECT ... FOR UPDATE SKIP LOCKED) RETURNING`, restricted to `attention_class IN ('standard','background')` and `decision_status = 'queued'`, so several workers or replicas never claim the same event. A crashed worker's lease expires and the event is claimed again, so nothing is lost. The memory repository implements the same interface with the same rules.
- **Async is opt-in** (`ATTENTION_ASYNC`, default `false`). With it on, a non-critical event is saved as `queued` and pending with the reason "waiting for the attention worker", and nothing is delivered until the worker decides. With it off, behaviour is as before. The worker always runs, so delayed events are released in both modes.
- **Attention budgets** (`pkg/budget`): `Budget.Take(ctx, scope)` counts one interruption in a **rolling** window and answers allowed or refused with when a slot frees. `MemoryBudget` is per process; `RedisBudget` shares one window across replicas through a single atomic Lua script (sorted set: trim, count, add). `ATTENTION_BUDGET_PER_HOUR` defaults to 10 and `0` turns it off. The scope is `workspace` now; the key is already `attention:budget:<scope>` so Milestone 30 can add `user:<id>`.
- **What the budget governs.** Only a **standard** event that would really be delivered (a standard route exists), and not one an "always notify" override covers. Background events do not spend it. When it is used up the event becomes `budget_exceeded` (class stays standard), is not delivered, and is included in the next digest and explained as such; it is stored, never dropped. A budget store failure lets the event through, like the dedup store.
- **Quiet hours:** `delayed` already existed. `ReleaseDelayed` re-asks preferences each pass: still quiet means leave it; quiet hours over means it is released as queued (spending the budget like any interruption) and delivered; a mute that now applies moves it to the digest. Releasing twice delivers once (the outbox is idempotent per event and channel, and the event leaves the `delayed` set).
- **Critical events, five independent guarantees** (each tested):
  1. `Handle` delivers a critical event inline in the first branch; it is never deferred, never pending, never budgeted.
  2. The budget is not consulted for a critical event (a spy budget fails the test if called).
  3. `ClaimPending` and `ListDelayed` never return one (class filter in SQL and memory), even if one is wrongly marked pending.
  4. The worker skips a critical event if a store leaks one, on both paths, and logs it loudly; `ProcessPending` and `ReleaseDelayed` refuse one with `ErrCritical`.
  5. `UpdateDecision` refuses to change one (`WHERE attention_class <> 'critical'` and an explicit check), so a decision cannot be written onto a critical row.

## Database changes
Idempotent: `events.attention_state` (default `done`, so every existing row is already decided), `attention_lease_until`, `attention_attempts`, indexes `events_attention_work_idx` (pending or processing) and `events_delayed_idx` (delayed). Migration tests cover them.

## APIs
No new routes. Behaviour changes: events can have status `budget_exceeded`; `GET /api/explain/{id}` explains it; digests include it. New settings: `ATTENTION_BUDGET_PER_HOUR` (default `10`, `0` off), `ATTENTION_ASYNC` (default `false`), `ATTENTION_WORKER_INTERVAL` (default `2s`); `REDIS_URL` now also shares the budget.

## Folder changes
New `pkg/budget`, `pkg/attention/worker.go`, `pkg/event/workqueue*.go`; the pipeline, event repositories, config and server wiring changed; one shared Redis client (`connectRedis`) now serves dedup and budget. CI runs on `feature/**` pushes and starts Redis.

## Risks
| Risk | Handling |
|---|---|
| The default budget changes behaviour: more than 10 standard notifications in an hour now stop interrupting | They are held for the digest, explained and visible, never dropped; set `ATTENTION_BUDGET_PER_HOUR=0` to disable. **Flagged for the design side** (see the sync doc) |
| Async events wait if no worker runs | The worker is always started with the server; async is off by default |
| A budget outage hides notifications | Fails open and logs |
| Two replicas decide on one event | `FOR UPDATE SKIP LOCKED` plus leases |

## Tests
`pkg/budget` (96.6 percent): shared contract on memory and Redis (limit and reset time, rolling window, scope isolation, concurrency never exceeds the limit), key namespacing, fail-open errors, the Lua reply parser through a fake `Scripter`, plus a real-Redis integration test. `pkg/event`: work-queue contract on memory and PostgreSQL (only pending standard and background claimed, **a critical event wrongly marked pending is never claimed**, leases, limit, decision update, **critical decisions immutable and no escalation**, delayed listing), competing workers on PostgreSQL. `pkg/pipeline` (95.6 percent): budget holds and never drops, **critical never touches the budget**, background does not spend it, no route means nothing counted, always override outranks it, budget failure fails open, scope is workspace, async defers and the worker decides, delivery once, **critical never deferred in async mode**, **a leaking store cannot get a critical event processed or changed**, retry after lease expiry, quiet-hours release (still quiet, released, once only, budget applies, mute wins, critical never delayed). `pkg/attention` (87.3 percent): worker paths and errors, digest includes budget-held events, explanation. `pkg/server`: budget over HTTP including digest and explain, budget off, critical sent with an exhausted budget, async end to end. `pkg/config`.

## Acceptance criteria
- [x] The async worker drains queued standard and background events with a SKIP LOCKED queue (PostgreSQL and in-memory).
- [x] Budgets with `MemoryBudget` and `RedisBudget`; exhausted standard events become `budget_exceeded` and reach the next digest.
- [x] Quiet-hours events are `delayed` and released when quiet hours end.
- [x] Explicit invariant tests prove a critical event cannot be picked up by the worker or throttled by the budget.
- [x] `gofmt`, `go vet` (also `-tags integration`), `go test -cover ./...` pass locally.
- [x] The PostgreSQL work queue (including competing workers) and the Redis Lua script pass against real servers, confirmed in CI run 42356002884 on commit 367d735. Not run locally.
- [x] `go test -race` and `golangci-lint` pass, confirmed in CI run 42356002884 on commit 367d735; neither can run on the development machine.

## Known limits
- The window is rolling and exact in Redis, and per process in memory. A fixed per-recipient budget needs Milestone 30.
- Budget-held events wait for the next digest rather than being re-offered when the window resets. A digest runs on `ATTENTION_DIGEST_EVERY` or on request.
- The worker polls; it does not use NATS yet.
- Delayed events are released within one worker interval of quiet hours ending, not at the exact minute.
