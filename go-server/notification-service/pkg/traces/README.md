# traces

Groups stored events that share a `traceId` into runs: ordered steps (depth from the parent chain), totals (duration, tokens, cost, retries, failures), worst attention class and a status read from the last step. Pure functions over `event.Event`; it decides nothing and calls no model. Telemetry is read from `payload.metadata`. See [docs/milestones/milestone-31.md](../../../../docs/milestones/milestone-31.md).
