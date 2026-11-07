# Milestone 10: Local Deployment and Operations Baseline

## Architecture

- Add a simple local deployment setup for PostgreSQL and Ollama.
- Keep operations lightweight and suitable for solo development.
- Leave room for later production hardening without changing the core architecture.

## Tasks

- Add a local Docker Compose file for PostgreSQL and Ollama.
- Document how to start the local infrastructure.

## Implementation

- The deploy directory now contains a compose file for PostgreSQL and Ollama.
- The local environment supports the persistence and learning layers introduced in earlier milestones.

## Tests

- Re-run the backend Go tests after adding the local deployment baseline.

## Review checklist

- [x] Local infrastructure compose file exists.
- [x] Backend tests pass.
