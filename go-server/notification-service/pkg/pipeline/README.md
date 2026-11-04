# pipeline

Runs one event through policy, dedup, delivery and persistence.

- Returns `ErrDuplicateDelivery` when the event ID was already stored (webhook redelivery), so nothing is sent twice.
- Critical events skip dedup merging and are delivered before being stored; if delivery or saving fails the dedup entry is released so the retry is not suppressed.
- A dedup store outage fails open: the event is treated as unique.
