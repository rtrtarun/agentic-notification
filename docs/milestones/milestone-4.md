# Milestone 4: Rule Engine, Criticality, Policy Evaluation

## Architecture

- Keep policy evaluation deterministic and separate from AI concerns.
- Classify normalized events with a small rules engine that can later expand into richer policies.
- Attach decision metadata to the event payload without altering the wider architecture.

## Tasks

- Add a simple policy engine package.
- Evaluate rules against ingested GitHub events.
- Persist the resulting attention decision with the normalized event.

## Implementation

- The server now uses a lightweight rule engine to assign an attention class and reason to incoming events.
- The default behavior assigns a standard attention class to GitHub events.
- The resulting decision is attached to the event payload for future auditing.

## Tests

- Rule engine matches a configured rule.
- Rule engine returns no match when no rule applies.
- Webhook ingestion still passes through the server successfully.

## Review checklist

- [x] Deterministic rule evaluation is available.
- [x] Incoming events can be classified with attention metadata.
- [x] Backend test suite passes.
