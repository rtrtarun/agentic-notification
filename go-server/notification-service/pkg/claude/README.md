# claude

A minimal client for the Anthropic Messages API (`POST /v1/messages`), built on `net/http` with no SDK. `Client.Complete(ctx, system, user, wantJSON)` returns the reply text and implements `llm.Completer` for Attention Intelligence.

- Requires `ANTHROPIC_API_KEY`; the key is sent only as the `x-api-key` header.
- Defaults to `claude-haiku-5-5` (override with `ATTENTION_MODEL`).
- Errors carry the HTTP status only, never the response body.
- The caller's context sets the deadline.

Tested against a fake server; not run against the live API.
