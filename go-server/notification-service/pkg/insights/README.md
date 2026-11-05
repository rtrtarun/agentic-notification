# insights

Read-only analytics and the activity timeline, computed from the notification history.

- `Service.Analytics(ctx, range)` — totals by level and outcome, a time series (hourly for 24h, daily for 7d and 30d), top sources, repositories and event types, and an `Attention` summary (interruptions, held back, median time to read, open critical).
- `Service.Activity(ctx, limit)` — arrivals and people's actions merged newest first.
- `Analyze` and `BuildActivity` are pure functions, so the maths is tested without a store.

It reads through the `Source` interface, which `inbox.Service` satisfies. Nothing here changes an event or a decision.

Limits: it works over the newest 1000 events (the event store list limit) and the newest actions; it is not a data warehouse.
