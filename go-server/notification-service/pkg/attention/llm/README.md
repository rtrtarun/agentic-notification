# attention/llm

Puts a language model behind `attention.Classifier`, `Summarizer` and `Explainer`. It is an optional, untrusted replacement for the built-in heuristic.

```go
m := llm.New(ollama.NewCompleter(url, "llama3.2"), attention.NewHeuristic(), llm.Options{})
classifier := attention.Guarded(m) // still wrap it: critical events and escalation are refused here too
```

Guarantees (all tested):

- A critical event is never sent to the model.
- Only `standard` or `background` is accepted, with score 0 to 100 and confidence 0 to 1; anything else is discarded.
- Any failure (timeout, error, breaker open, rate cap, invalid answer) returns the heuristic's answer instead.
- The model sees redacted event titles and flags, never comment bodies or raw payloads.
- Digest groups, counts and event IDs always come from the heuristic; the model only supplies wording.

`Model.Status()` feeds `GET /api/attention/status`. A new provider only has to implement `Completer`. See [docs/milestones/milestone-26.md](../../../../../docs/archived/milestones/milestone-26.md).
