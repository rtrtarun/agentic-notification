# Documentation

Everything about the project, grouped by what you want to do. Start with the first group if you are new.

## Layout

```text
docs/
├── README.md                  this index
├── architecture.md            the checked reference
├── orchestration-platform.md  the design narrative
├── deployment.md  runbook.md  going live and operating
├── frontend.md  ui-architecture.md   the console
├── agentpulse.md              AI agent observability
├── implementation-sync.md  CHANGELOG.md  status and history
├── milestones/                one document per milestone, milestone-1.md to milestone-34.md
└── rules/                     the rules every change follows (coding-standards.md)
```

Everything stays under `docs/`. `AGENTS.md` (the design brief), `CONTRIBUTING.md` and the root `README.md` stay at the repository root because tools and GitHub look for them there. Add new milestone documents to `docs/milestones/` and new standards or process rules to `docs/rules/`.

## Contents

- [Layout](#layout)
1. [Start here](#1-start-here)
2. [Understand the system](#2-understand-the-system)
3. [Run it and operate it](#3-run-it-and-operate-it)
4. [Build on it](#4-build-on-it)
5. [Status, verification and history](#5-status-verification-and-history)
6. [Milestones](#milestones)
7. [Standards and process](#7-standards-and-process)
8. [Background and vision](#8-background-and-vision)

---

## 1. Start here

| Document | Read it to |
|---|---|
| [Root README](../README.md) | Learn what the platform is, run it, and try the examples |
| [Architecture](architecture.md) | See the layers, the feature inventory, every endpoint and what is proven |
| [Deployment](deployment.md) | Put the console on GitHub Pages and the service on a host |

## 2. Understand the system

| Document | Read it to |
|---|---|
| [Architecture](architecture.md) | The checked reference: layers, flows, data contracts, endpoints, status. Sections 3, 5, 12 to 14, 19 and 22 are current; sections 6 to 11 and 15 to 18 describe the original pipeline |
| [Orchestration overview](orchestration-platform.md) | The design narrative: classes, attention budget, digests, feedback, the provider abstraction |
| [Attention Intelligence, milestone 25](milestones/milestone-25.md) and [model adapters, milestone 26](milestones/milestone-26.md) | How Layer 2 works and how a model is kept on a leash |
| [Compared with Azure Notification Hubs](notification-hubs-comparison.md) | How this differs from a push-delivery service |

## 3. Run it and operate it

| Document | Read it to |
|---|---|
| [Deployment](deployment.md) | Go live: GitHub Pages, the container image, a host, the production environment table, smoke checks |
| [Runbook](runbook.md) | Diagnose problems: each section is a symptom, its meaning and what to do |
| [Service README](../go-server/notification-service/README.md) | Every environment variable with its default |
| [Local services](../deploy/README.md), [Kubernetes manifests](../k8s) | Run supporting services locally, or on a cluster |

## 4. Build on it

| Document | Read it to |
|---|---|
| [AgentPulse](agentpulse.md) | The product idea for agent observability (traces, cost, retries, MCP) |
| [Milestone 31](milestones/milestone-31.md) | The agent event API, traces and the MCP server as built |
| [Python SDK](../sdk/python/README.md) | Report an agent run in a few lines |
| [Frontend](frontend.md) and [UI architecture](ui-architecture.md) | Work on the console: screens, components, design tokens, accessibility |
| [Rules (milestones 18 and 30)](milestones/milestone-18.md), [predicates and channels](milestones/milestone-30.md) | Write rules: conditions, payload predicates, per-rule channels |

## 5. Status, verification and history

| Document | Read it to |
|---|---|
| [Implementation sync](implementation-sync.md) | The handshake between design and implementation: status by milestone, decisions, the gates that could and could not be run, open questions, CI findings |
| [Design and implementation sync (working copy)](design_and_implementation_sync.md) | The designer's working copy that was merged into the file above; the file above is canonical |
| [Changelog](CHANGELOG.md) | Every change, newest first |
| [Upcoming milestones](upcoming-milestones.md) | The original proposal for milestones 27 onward. **Historical:** milestones 27 to 33 are done as `PROMPT_SPEC.md` defined them; see the sync document |

## Milestones

Each milestone has one document: goals, decisions, database and API changes, risks, tests, acceptance criteria and known limits.

| Era | Milestones | What was built |
|---|---|---|
| **Foundation** | [1](milestones/milestone-1.md), [2](milestones/milestone-2.md), [3](milestones/milestone-3.md), [4](milestones/milestone-4.md) | Repository, auth and configuration, GitHub webhooks, the rule engine |
| | [5](milestones/milestone-5.md), [6](milestones/milestone-6.md), [7](milestones/milestone-7.md), [8](milestones/milestone-8.md), [9](milestones/milestone-9.md) | Persistence, the delivery abstraction, SMTP, first feedback endpoint, an Ollama stub |
| | [10](milestones/milestone-10.md), [11](milestones/milestone-11.md), [12](milestones/milestone-12.md), [13](milestones/milestone-13.md), [14](milestones/milestone-14.md) | Local deployment, the critical pipeline, deduplication and threading, hardening, the first console |
| **Console build-out** | [15](milestones/milestone-15.md), [16](milestones/milestone-16.md), [17](milestones/milestone-17.md) | Inbox, all notifications, overview, activity and analytics |
| | [18](milestones/milestone-18.md), [19](milestones/milestone-19.md), [20](milestones/milestone-20.md), [21](milestones/milestone-21.md) | Rules and policies, channels and integrations, preferences and live updates, help |
| **Service completion** | [22](milestones/milestone-22.md), [23](milestones/milestone-23.md) | Built-in delivery with a retry queue; accounts and single sign-on |
| | [24](milestones/milestone-24.md), [25](milestones/milestone-25.md), [26](milestones/milestone-26.md) | Feedback and overrides; Attention Intelligence; Ollama and Claude adapters |
| **Verification and depth** | [27](milestones/milestone-27.md), [28](milestones/milestone-28.md), [29](milestones/milestone-29.md) | PostgreSQL contract tests; the learning engine; the async worker and attention budgets |
| | [30](milestones/milestone-30.md), [31](milestones/milestone-31.md), [32](milestones/milestone-32.md), [33](milestones/milestone-33.md) | Predicates, channels and per-person digests; AgentPulse and MCP; Novu, cursors, tracing and STARTTLS; this reconciliation and the SDK |
| | [34](milestones/milestone-34.md) | Java foundation: the core pipeline in Java on the shared database, and the shared HTTP contract in [contract/](contract/README.md) |

## 7. Standards and process

| Document | Read it to |
|---|---|
| [Coding standards](rules/coding-standards.md) | The rules every change follows: architecture, Go, logging, API, database, testing, frontend, hygiene, definition of done |
| [Contributing](../CONTRIBUTING.md) | How to propose and land a change |
| [AGENTS.md](../AGENTS.md) | The original design brief and the milestone process |
| [Skills](../skills/README.md) | Review and design guides used while building (Go, mobile, UI library choice, code review) |
| [PROMPT_SPEC.md](../PROMPT_SPEC.md) | The instruction set milestones 27 to 33 were built from (kept out of version control) |

## 8. Background and vision

| Document | What it is |
|---|---|
| [Project vision](project%20vision.md) | Early positioning notes: why "attention orchestration" and not "another notification app" |
| [What we wanted to see](extrat%20of%20what%20we%20wnted%20to%20see.md) | Raw product notes that fed [AgentPulse](agentpulse.md). The file name has a typo; it is left alone so existing links keep working |
| [Roadmap outline](../roadmap.md) | A seven-line outline from the first planning pass; [architecture.md section 13](architecture.md#13-forward-roadmap) is the current roadmap |
