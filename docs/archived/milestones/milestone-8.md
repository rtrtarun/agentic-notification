# Milestone 8: Feedback Layer

## Architecture

- Add a simple feedback capture path so user reactions can later influence policy and learning.
- Keep feedback storage lightweight and in-memory for the current milestone.
- Preserve a clean interface so feedback can later be backed by a database or event stream.

## Tasks

- Add a feedback model for actions such as opened, dismissed, and replied.
- Add an in-memory feedback recorder.
- Expose a minimal /feedback endpoint for accepting feedback submissions.

## Implementation

- The server now accepts POST requests to /feedback and stores feedback records.
- Feedback is captured as structured events that can be extended later.

## Tests

- Verify feedback is stored and retrievable from the in-memory recorder.
- Keep the existing server and other package tests passing.

## Review checklist

- [x] Feedback model exists.
- [x] Feedback recorder stores reactions.
- [x] Backend tests pass.
