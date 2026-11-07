# Contributing

Thank you for helping. This project is built in small, reviewable steps, and a few rules keep it safe to change. They come from [docs/coding-standards.md](docs/coding-standards.md) and [AGENTS.md](AGENTS.md); read those for the full text.

## The one rule that matters most

A **critical** event (a security alert, an outage, a failed payment) must never touch AI, never be merged, muted, delayed, budgeted or deferred, and is always delivered inline to every configured channel. Only a rule makes an event critical, and an agent describing its own event as severe must not change that. If your change is anywhere near that path, add a test that proves the guarantee still holds. Existing examples: `pkg/pipeline/budget_async_test.go`, `pkg/learning/service_test.go`, `pkg/agentevents/agentevents_test.go`.

## Before you start

1. Open an issue, or comment on one, describing what you want to change and why. For anything larger than a fix, say which milestone-sized step it is.
2. Fork the repository and branch from `main`: `feature/<topic>` for new work, `fix/<topic>` for a bug.
3. Prefer one small vertical slice: the change should leave a system that builds, runs and passes its tests.

## What a good change looks like

- **Clean Architecture.** Handlers only decode, call one service and encode. No SQL outside repository implementations. Dependencies are injected in `server.NewServer`; no package-level mutable state or `init()` side effects.
- **Layer 1 stays deterministic.** No AI call outside Layer 2 (`pkg/attention`).
- **Go:** `gofmt -s` clean, `go vet` clean, exported identifiers documented, a `context.Context` first on anything that does I/O, errors wrapped with context, no `http.DefaultClient`, no magic values.
- **Stores get a contract test** that runs against the in-memory implementation always and against PostgreSQL under `-tags integration` (see `pkg/db/dbtest` and any `*_contract_test.go`).
- **Console:** strict TypeScript, no new runtime UI dependencies, all `fetch` calls inside `src/lib/api.ts`, logic in `src/lib/` with Vitest tests, and demo mode must keep working for every screen.
- **Write the test first, or at least with the change.** Every bug fix adds a regression test. Tests use fakes behind interfaces and inject clocks instead of sleeping.

## Run the checks

```bash
# Go
cd go-server/notification-service
gofmt -s -l .                          # prints nothing when clean
go vet ./... && go vet -tags integration ./...
go test -cover ./...
# CI also runs, and you can too if you have a C compiler, PostgreSQL and Redis:
TEST_DATABASE_URL=postgres://... TEST_REDIS_URL=redis://... go test -race -tags integration ./...

# Console
cd apps/web && npm test -- --run && npm run build

# Python SDK
cd sdk/python && python -m unittest
```

CI (`.github/workflows/ci.yml`) additionally runs golangci-lint, govulncheck and an 80 percent coverage gate. If you cannot run something locally, say so in the pull request instead of leaving it out.

## Document the change in the same commit

- Add an entry at the top of [`docs/CHANGELOG.md`](docs/CHANGELOG.md).
- For a milestone-sized change, add or update `docs/milestone-N.md` (goals, decisions, database and API changes, risks, tests, acceptance, known limits). For a package, update its `README.md`.
- If you decided something the design did not say, or could not verify something, record it in [`docs/implementation-sync.md`](docs/implementation-sync.md).
- New settings go in the service README; new routes in `docs/architecture.md` section 19.

Never write that something is verified unless you ran it. A check that could not run is listed as **not verified**.

## Commits and pull requests

- Commit messages are short and imperative, and mention the milestone or area (`feat: ...`, `fix: ...`, `docs: ...`, `test: ...`). **Do not add a `Co-Authored-By` trailer.**
- One milestone per pull request, reviewed before the next begins.
- In the description, say what changed, which commands you ran and their results, and anything you could not verify.
- Do not commit build output (`dist/`, `*.tsbuildinfo`, binaries, `__pycache__`), secrets or `.env` files.

## Security

Please report a vulnerability through a private security advisory on the repository, not a public issue. Do not include real keys, tokens or webhook URLs in issues, logs or tests.

## Licence of contributions

The project does not have a licence yet (see the [README](README.md#license)). Until one is added, hold off on outside contributions, or ask in an issue first.
