# Documentation

Everything about the project, grouped by what you want to do. Start with the first group if you are new.

> Files are grouped here rather than moved into sub-folders: many documents, workflows and the changelog link to each other by path, and moving them would break those links. If you later want folders, move one group at a time and fix its links in the same commit.

## Contents

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
| [Attention Intelligence, milestone 25](milestone-25.md) and [model adapters, milestone 26](milestone-26.md) | How Layer 2 works and how a model is kept on a leash |
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
| [Milestone 31](milestone-31.md) | The agent event API, traces and the MCP server as built |
| [Python SDK](../sdk/python/README.md) | Report an agent run in a few lines |
| [Frontend](frontend.md) and [UI architecture](ui-architecture.md) | Work on the console: screens, components, design tokens, accessibility |
| [Rules (milestones 18 and 30)](milestone-18.md), [predicates and channels](milestone-30.md) | Write rules: conditions, payload predicates, per-rule channels |

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
| **Foundation** | [1](milestone-1.md), [2](milestone-2.md), [3](milestone-3.md), [4](milestone-4.md) | Repository, auth and configuration, GitHub webhooks, the rule engine |
| | [5](milestone-5.md), [6](milestone-6.md), [7](milestone-7.md), [8](milestone-8.md), [9](milestone-9.md) | Persistence, the delivery abstraction, SMTP, first feedback endpoint, an Ollama stub |
| | [10](milestone-10.md), [11](milestone-11.md), [12](milestone-12.md), [13](milestone-13.md), [14](milestone-14.md) | Local deployment, the critical pipeline, deduplication and threading, hardening, the first console |
| **Console build-out** | [15](milestone-15.md), [16](milestone-16.md), [17](milestone-17.md) | Inbox, all notifications, overview, activity and analytics |
| | [18](milestone-18.md), [19](milestone-19.md), [20](milestone-20.md), [21](milestone-21.md) | Rules and policies, channels and integrations, preferences and live updates, help |
| **Service completion** | [22](milestone-22.md), [23](milestone-23.md) | Built-in delivery with a retry queue; accounts and single sign-on |
| | [24](milestone-24.md), [25](milestone-25.md), [26](milestone-26.md) | Feedback and overrides; Attention Intelligence; Ollama and Claude adapters |
| **Verification and depth** | [27](milestone-27.md), [28](milestone-28.md), [29](milestone-29.md) | PostgreSQL contract tests; the learning engine; the async worker and attention budgets |
| | [30](milestone-30.md), [31](milestone-31.md), [32](milestone-32.md), [33](milestone-33.md) | Predicates, channels and per-person digests; AgentPulse and MCP; Novu, cursors, tracing and STARTTLS; this reconciliation and the SDK |

## 7. Standards and process

| Document | Read it to |
|---|---|
| [Coding standards](coding-standards.md) | The rules every change follows: architecture, Go, logging, API, database, testing, frontend, hygiene, definition of done |
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
