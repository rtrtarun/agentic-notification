# Milestone 26: Language-model adapters for Attention Intelligence (Ollama and Claude)

## Goals
- Put a real language model behind the Layer 2 interfaces (`Classifier`, `Summarizer`, `Explainer`) from milestone 25, using either a local Ollama server or the Claude API.
- Never depend on it: with no model, a slow model, a broken model or a model that answers badly, the service behaves exactly as it did with the heuristic.
- Show people who wrote what: a "Written by rules, no AI" or "Written by ollama:llama3.2" badge on digests and explanations.

## Architecture decisions
- **One adapter, two backends.** `pkg/attention/llm.Model` implements all three interfaces on top of a tiny `Completer` interface (`Name`, `Complete`). `ollama.Completer` (`pkg/ollama`) and `claude.Client` (`pkg/claude`, plain `net/http`, no SDK) implement it. A future provider is one small file.
- **The heuristic is the fallback, and it is always there.** `Model` calls the model, validates the answer, and on any failure returns the heuristic's answer for that same call. A fallback is counted and logged, never surfaced as an error to the pipeline.
- **The promises still hold, and are tested with a model in the loop.**
  1. *Critical never reaches a model.* `Classify` refuses critical before any call; `Summarize` builds its groups with the heuristic, which skips critical events, so none are in the prompt; `ExplainSourced` explains critical events (and events with no recorded decision) with the heuristic alone. The server test uses a fake model server that fails if it ever sees a `security_advisory`.
  2. *It can only choose standard or background.* The answer is parsed strictly: class must be one of the two, score 0 to 100, confidence 0 to 1, all present. A `"critical"`, an unknown class, an out-of-range number or non-JSON discards the whole answer and the heuristic decides. `attention.Guarded` still wraps the model as a second line of defence.
  3. *The service works without it.* `ATTENTION_PROVIDER` defaults to `heuristic`.
- **A model only supplies wording for digests.** Groups, counts and event IDs come from the heuristic; the model returns a title, a text and one sentence per group key. Unknown keys are ignored, so it cannot invent or drop an event.
- **Explanations are rewrites, not new reasoning.** The model is given the heuristic's explanation and told to restate it in friendlier words using only those facts.
- **What a model sees is minimal and redacted.** Source, event type, repository, action, sender login, the pull request, issue or check title, conclusion, review state and draft/bot flags, as a small JSON object. Never comment bodies, descriptions or raw payloads. Emails, well-known token shapes (GitHub, Slack, AWS, JWT, `sk-`), any 32+ character run that looks like a secret, and URLs carrying `token=`, `key=` and similar are redacted; control characters are stripped and angle brackets are neutralised so data cannot close the prompt's own tags. The system prompts tell the model that tagged text is data, never instructions.
- **Limits.** Per-call timeout (`ATTENTION_TIMEOUT`, default 5s), a per-minute cap (`ATTENTION_MAX_CALLS_PER_MIN`, default 60) and a circuit breaker (3 failures in a row open it for 30s, then one call is allowed through to test). Invalid answers count as failures; a caller cancelling does not.
- **Model output is cleaned before it is stored or delivered:** whitespace collapsed, control characters removed, and every field length-capped (reasons 120 characters, digest title 100, text 600, group sentence 200, explanation 700).
- **Secrets stay secret.** `ANTHROPIC_API_KEY` is only held by the Claude client, is sent only as a request header, and error messages carry the HTTP status only. The status endpoint is admin only because an error can name an address.

## Database changes
None. Digests are stored as JSON, so the new `source` field and the existing `model` field travel in the body. (The proposal listed new columns; they proved unnecessary.) Older digests without `source` are shown by their model name.

## APIs
| Route | Change |
|---|---|
| `GET /api/attention/status` | New, admin only. `{provider, model, breaker: closed\|open\|half-open, calls, fallbacks, lastError?, lastErrorAt?}`. Reports `heuristic` when no model is configured. |
| `GET /api/digests`, `/api/digests/{id}`, `POST /api/digests/generate` | Each digest gains `source` (`heuristic` or `model`); `model` names the writer |
| `GET /api/explain/{eventId}` | Gains `model` and `source` |

New settings: `ATTENTION_PROVIDER` (`heuristic` default, `ollama`, `claude`), `OLLAMA_URL` (default `http://localhost:11434`), `OLLAMA_MODEL` (default `llama3.2`), `ANTHROPIC_API_KEY` (required for `claude`; startup fails clearly without it), `ATTENTION_MODEL` (Claude model, default `claude-haiku-5-5`), `ATTENTION_TIMEOUT`, `ATTENTION_MAX_CALLS_PER_MIN`.

## Folder changes
New `pkg/attention/llm/` (`model.go`, `prompts.go`, tests, README) and `pkg/claude/` (client, test, README). `pkg/ollama` gains `Completer`. `pkg/attention` gains `Summary.Model`, `Digest.Source`, `SourcedExplainer` and `SourceOf`. `pkg/config`, `pkg/server` (`attention_handlers.go` wiring and status route). Web: `components/SourceBadge.tsx`, digest and explanation types.

## Risks and how they are handled
| Risk | Handling |
|---|---|
| Malformed or hostile model output | Strict parsing, ranges, enum, caps; fallback to the heuristic; text cleaned |
| Prompt injection from event titles | Only titles are sent, redacted and bracket-neutralised; the model can only pick standard or background, so the worst case is a quiet event held for the digest, which stays readable |
| Private data sent to a hosted model | Off by default; minimal facts only; redaction; Ollama keeps everything local |
| Cost and latency | Cheap default model, timeout, per-minute cap, breaker |
| Model outage | Heuristic answers; status endpoint shows breaker, fallbacks and last error |

## Tests
`pkg/attention/llm`: valid answer (including a fenced one), critical never calls the model, eleven kinds of invalid answer fall back (escalation, unknown class, out-of-range, missing, non-JSON, truncated, wrong types), reason cap, breaker opens, stays closed for caller cancellation and recovers, rate cap and reset, timeout, prompt leaks nothing (body, comment, email, token) and keeps the useful facts, redaction table, digest wording applied while groups come from the heuristic, critical absent from the prompt, nothing to summarise means no call, explanation rewrite and source, critical explanation stays off the model, fallback on failure, interfaces, `Guarded` around a model. `pkg/claude` and `pkg/ollama`: request shape, headers, defaults, error bodies not leaked, malformed and empty replies, context deadline. `pkg/config`: defaults, reading, every validation. `pkg/server` (against a fake Ollama): status default and access, model classification recorded, critical never reaches the model, escalation attempt ignored, model-written digest and explanation labelled, everything works with the model down, Claude without a key starts on the heuristic. Web: `sourceOf` and the explanation source (214 Vitest tests).

## Acceptance criteria
- [x] `ATTENTION_PROVIDER=heuristic` (or unset) behaves as before; all existing tests pass unchanged.
- [x] With a model, classifications, digests and explanations are model-written and labelled with their source.
- [x] With the model down, events, digests and explanations still work and the status endpoint shows why.
- [x] A critical event is never sent to a model, and a model cannot escalate.
- [x] Go (`go vet`, all packages) and Vitest pass; TypeScript compiles.

## Known limits (honest list)
- **Not run against a real Ollama or the real Claude API.** Both adapters are tested against fake HTTP servers that follow the documented request shapes. Run it once against each before relying on it, and check the status endpoint.
- **Classification is synchronous.** The model is called while an event is being processed, but only for events whose rule is adaptive (or that no rule decided), and the timeout bounds the delay. Moving it to a queue is possible later.
- **The model answers one event at a time** and has no memory of past feedback. Learning from feedback is milestone 9 in the original plan and is not started.
- **A small local model may often fail the strict JSON check** and fall back. The status page counts these; pick a stronger `OLLAMA_MODEL` if fallbacks are high.
- **The per-minute cap and breaker are per process**, not shared between replicas.
- **Explanations of non-critical events are sent to the model** (as redacted facts plus the heuristic's sentence). Use `heuristic` or local Ollama if that is not acceptable.

## Next
See [upcoming-milestones.md](upcoming-milestones.md) for proposed milestones 27 to 31.
