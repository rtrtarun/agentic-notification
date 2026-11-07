# Documentation

Current documentation only. Anything superseded lives in [archived/](archived/README.md).

## Layout

```text
docs/
├── README.md                this index
├── architecture.md          the checked reference: layers, flows, contracts, endpoints, status
├── implementation-sync.md   what is built, proven, open and decided (read this first for status)
├── roadmap.md               what is not built yet
├── java-service.md          the Java service: scope, layout, config, tests
├── frontend.md              the web console
├── agentpulse.md            AI agent observability (traces, cost, retries, MCP)
├── deployment.md            going live
├── runbook.md               diagnosing problems
├── CHANGELOG.md             every change, newest first
├── contract/                the shared HTTP contract both services pass
├── milestones/              records for the current work (34, 35)
├── rules/                   coding standards every change follows
└── archived/                milestones 1 to 33 and superseded design notes
```

`AGENTS.md` (the design brief), `CONTRIBUTING.md` and the root `README.md` stay at the repository root because tools and GitHub look for them there.

## Read in this order

| You want to | Read |
|---|---|
| Know what the project is and run it | [Root README](../README.md) |
| Know what is built and what was proven | [implementation-sync.md](implementation-sync.md) |
| Understand the design | [architecture.md](architecture.md) |
| Know what comes next | [roadmap.md](roadmap.md) |
| Work on the Java service | [java-service.md](java-service.md), [contract/](contract/README.md), [milestones/](milestones/README.md) |
| Work on the console | [frontend.md](frontend.md), [../apps/web/README.md](../apps/web/README.md) |
| Report an agent run | [agentpulse.md](agentpulse.md), [../sdk/python/README.md](../sdk/python/README.md) |
| Go live | [deployment.md](deployment.md), [../deploy/README.md](../deploy/README.md) |
| Fix a problem | [runbook.md](runbook.md), [Go service README](../go-server/notification-service/README.md) (every environment variable) |
| Change code | [rules/coding-standards.md](rules/coding-standards.md), [../CONTRIBUTING.md](../CONTRIBUTING.md), [../AGENTS.md](../AGENTS.md) |
| See the history | [CHANGELOG.md](CHANGELOG.md), [archived/](archived/README.md) |

## Documentation rules
1. **Every change is documented in the same commit**: a [CHANGELOG.md](CHANGELOG.md) entry plus the affected current document (and the package or service README if its behaviour changed).
2. **Few files, much detail.** New material goes into the existing current document it belongs to; add a file only when no current one fits.
3. **A current document describes the present.** When it stops being true or useful, move it to `archived/` (use `git mv`, never delete) and fix the index here.
4. **Milestone records** go in `milestones/` while the work is current; the milestone that is superseded moves to `archived/milestones/`.
5. **Claims of "verified" name the command or CI run.** Unrun checks are written as not verified.
6. Every package has a `README.md`; every public API is documented in [architecture.md](architecture.md) section 19 or the service README.
