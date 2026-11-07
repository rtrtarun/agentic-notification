# ollama

Clients for a local Ollama server.

- `Completer` (milestone 26): `Complete(ctx, system, user, wantJSON)` over `/api/chat`, used by `pkg/attention/llm` for classification, digests and explanations. `Name()` is `ollama:<model>`. Configured with `OLLAMA_URL` and `OLLAMA_MODEL`.
- `Service.Summarize`: the older one-shot feedback summariser. Not wired into anything.

Anything model-backed must go through `attention.Guarded` so a critical event never reaches it and a model can never escalate.

It was `pkg/learning` until milestone 26, when that name was freed for the learning engine.
