# Coding Standards

These standards apply to all code in this repository. Items marked **[CI]** are enforced automatically; the rest are enforced in review. Items marked **[Gap]** are known deviations in the current code that new work must not repeat and that should be fixed incrementally.

## 1. Architecture

- Clean Architecture: domain → application → infrastructure → interfaces. Dependencies point inward only.
- No business logic in HTTP handlers. Handlers decode, call one service/pipeline method, and encode.
- No SQL outside repository implementations. Repositories are interfaces defined next to their consumers.
- Layer 1 (policy, dedup, threading) is deterministic. **No AI calls outside Layer 2.** Critical events never touch AI or dedup merging.
- External providers (Novu, SMTP, Ollama) sit behind an interface. Never import a provider package from domain or pipeline code.
- Wire dependencies by constructor injection in `main`/`server.New`. No package-level mutable state or `init()` side effects.

## 2. Go

- **[CI]** `gofmt` clean and `go vet ./...` passes. Tests run with `-race`.
- Exported identifiers have doc comments starting with the identifier name.
- Return errors; never `panic` outside `main`. Wrap with context: `fmt.Errorf("persist event: %w", err)`. Never discard an error silently.
- Accept `context.Context` as the first parameter of anything that does I/O (DB, HTTP, delivery) and honor cancellation. **[Gap]** I/O methods currently take no context.
- Every outbound HTTP call uses a client with a timeout and a context. No `http.DefaultClient`.
- No magic values: statuses, classes, header names and defaults are named constants.
- Prefer small interfaces (1–3 methods), accept interfaces, return structs.
- Concurrency: guard shared state with a mutex or channel; run tests with `-race`; every goroutine has an owner and a stop path.
- Config comes from environment, validated at startup, and fails fast. Secrets are never logged.

## 3. Logging and observability

- Structured logging only (`log/slog`, JSON in production) with stable keys: `event_id`, `thread_id`, `attention_class`, `decision_status`, `request_id`. **[Gap]** the server uses `log.Printf`.
- Never log payload bodies, credentials, or recipient addresses at info level.
- Every service exposes `/health` and `/metrics`.
- Every decision is logged at write time with its reason; decisions are never recomputed later.

## 4. API

- Every public endpoint is documented (path, auth, request, response, errors) in the milestone doc or `api/`.
- Return JSON errors with a consistent shape and correct status codes.
- Protected routes require the API key. The GitHub webhook must verify `X-Hub-Signature-256`. **[Gap]** signature verification is not implemented.
- Limit request body size and validate all input.

## 5. Database

- Migrations are idempotent and additive. No destructive change without an explicit migration milestone.
- Parameterised queries only. Never build SQL by string concatenation with input.
- Index every column used for lookup or grouping.

## 6. Testing

- Every package has unit tests; every bug fix adds a regression test.
- Tests use fakes behind the interfaces, not live services. No network in unit tests.
- Table-driven tests where inputs vary. Tests are deterministic: inject clocks instead of sleeping.
- **[CI]** `go test -race ./...` must pass before merge.
- Critical-path rules need explicit tests: critical events are delivered, never merged, never sent to AI.

## 7. Frontend (React + TypeScript)

- `strict` TypeScript; no `any` without a comment explaining why.
- **[CI]** `npm run build` (includes `tsc -b`) passes.
- API access goes through one typed client module; components do not call `fetch` directly.

## 8. Repository hygiene

- LF line endings, UTF-8 (`.gitattributes`, `.editorconfig`). **[CI]** formatting check.
- No build output or secrets committed (`target/`, `dist/`, `node_modules/`, `.env*`).
- Every package has a `README.md`; every milestone has `docs/milestone-N.md`.
- Commits are small, imperative, and reference the milestone. One milestone per PR, reviewed before the next begins.
- Dependencies are pinned through `go.sum` / lockfiles; add a dependency only when the standard library is insufficient.

## 9. Definition of done

A change is done when: it builds, `gofmt`/`vet`/tests (`-race`) pass, new behaviour has tests, public APIs and packages are documented, logs are structured, and no item in this document is violated without a recorded reason.
