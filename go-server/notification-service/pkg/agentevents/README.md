# agentevents

Turns what an AI agent reports about one step into the platform's event. `Normalize` validates (event type, ids, `step_kind`, non-negative telemetry, `confidence` 0 to 1, size caps) and returns an event with **no attention class**: the pipeline's rules decide it. An agent's `severity` is stored as payload data for rules to match and never sets the class. See [docs/milestones/milestone-31.md](../../../../docs/archived/milestones/milestone-31.md).
