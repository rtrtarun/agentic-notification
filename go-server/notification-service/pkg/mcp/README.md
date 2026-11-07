# mcp

A Model Context Protocol server. `Server.Handle` turns one JSON-RPC 2.0 message into one response (or none, for a notification). Tools: `publish_event`, `get_recent_events`, `notify_user`, `explain_decision`, each acting through a `Backend`.

- `HTTPBackend` runs them against the REST API with an API key; `cmd/mcp-server` uses it over stdio.
- `POST /api/mcp` uses an in-process backend. Both share this dispatcher, so the tools cannot drift.
- Arguments are strict and bounded; `notify_user` goes through the validating notification service.

See [docs/milestones/milestone-31.md](../../../../docs/archived/milestones/milestone-31.md).
