# Skills

Reusable reference guides for contributors and AI agents working in this repo. Each skill is a folder containing a `SKILL.md` with `name` and `description` frontmatter.

| Skill | Use when |
|---|---|
| [golang-pro](golang-pro/SKILL.md) | Writing Go: concurrency, interfaces, generics, testing, project structure, gRPC, performance |
| [tarun-code-review](tarun-code-review/SKILL.md) | Reviewing a diff or PR: design, auth, security, edge cases, database, observability, cost, CI/CD, tests |
| [tarun-design-eng](tarun-design-eng/SKILL.md) | Building or reviewing UI polish and animation: easing, timing, springs, gestures, accessibility |
| [mobile-native](mobile-native/SKILL.md) | Making the web app feel native on a phone: hover, tap, viewport units, safe areas, input zoom |
| [pick-ui-library](pick-ui-library/SKILL.md) | Choosing a frontend library for toasts, menus, charts, drag and drop, state, styling |

`golang-pro` references (loaded on demand): [concurrency](golang-pro/references/concurrency.md), [interfaces](golang-pro/references/interfaces.md), [generics](golang-pro/references/generics.md), [testing](golang-pro/references/testing.md), [project-structure](golang-pro/references/project-structure.md).

## Sources

- `tarun-code-review`: written by the repo owner. Its checklist is Python-flavoured; apply the Go equivalents and mark Python-only items N/A.
- `golang-pro`: [Jeffallan/claude-skills](https://github.com/jeffallan/claude-skills/tree/main/skills/golang-pro), MIT license, v1.1.1.
- `tarun-design-eng`, `mobile-native`, `pick-ui-library`: Tarun's design engineering skills, supplied by the repo owner. `pick-ui-library` only runs when explicitly invoked.

To add a skill: create `skills/<name>/SKILL.md`, list it above and note its source. `AGENTS.md` takes precedence over any skill.
