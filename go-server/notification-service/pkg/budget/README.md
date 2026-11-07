# budget

A cap on how often standard notifications interrupt people: "human attention is expensive".

- `Budget.Take(ctx, scope)` counts one interruption in a rolling window and says whether it fits, and when a slot frees if not.
- `MemoryBudget` is per process. `RedisBudget` shares one window across replicas through a single atomic Lua script (`ZREMRANGEBYSCORE`, `ZCARD`, `ZADD`).
- `ATTENTION_BUDGET_PER_HOUR` (default 10, 0 turns it off). Scope is `workspace`; keys are `attention:budget:<scope>` so per-person scopes can follow.
- Never consulted for a critical event. Background events and "always notify" choices do not spend it. A store error fails open.
- Events over the budget become `budget_exceeded` and go in the next digest; they are never dropped.

See [docs/milestones/milestone-29.md](../../../../docs/milestones/milestone-29.md).
