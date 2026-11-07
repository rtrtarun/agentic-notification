# pipeline

Runs one event through policy, dedup, delivery and persistence.

- Returns `ErrDuplicateDelivery` when the event ID was already stored (webhook redelivery), so nothing is sent twice.
- Critical events skip dedup merging and are delivered before being stored; if delivery or saving fails the dedup entry is released so the retry is not suppressed.
- A dedup store outage fails open: the event is treated as unique.

## Delivery of non-critical events (milestone 22)

`WithOutbox` hands standard and background events to `delivery.Outbox`, which queues them for the channels they are routed to. Only events that are still `queued` after preferences and dedup are handed over, so a muted, held or merged event stays quiet. A queue failure is logged and never fails or loses the event. Critical events are still delivered inline; the outbox only logs them and never queues them.

The attention worker (milestone 29): `Handle` can defer the attention step with `WithAsyncAttention`; `ProcessPending` and `ReleaseDelayed` finish it for a worker (`pkg/attention/worker.go`). `WithBudget` caps standard interruptions. A critical event is never deferred, budgeted or released by either path.
