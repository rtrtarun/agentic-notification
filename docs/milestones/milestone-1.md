# Milestone 1: Repository Setup, Docker, CI, Basic API, React App, Database, Health Endpoint

## Architecture

- Keep the backend focused on a thin HTTP surface with a health endpoint and a notification service interface.
- Keep the frontend as a simple React app shell so later milestones can plug in workflows without re-architecting.
- Use PostgreSQL as the initial persistence dependency and leave persistence access behind a simple infrastructure boundary.

## Tasks

- Set up a Go API service with a health endpoint.
- Add a lightweight React + TypeScript shell.
- Add Docker Compose services for the API and PostgreSQL.
- Add CI coverage for the Go service.

## Implementation

- Backend: Go service with structured health response at /health.
- Frontend: Vite + React + TypeScript shell served on port 3000.
- Database: PostgreSQL container available locally for future persistence work.

## Tests

- Unit test for the health response payload.
- Frontend build verification via Vite.

## Documentation

- Update the repo README with local run steps.
- Add this milestone document for review.

## Review checklist

- [x] Health endpoint returns JSON.
- [x] Docker Compose starts local infrastructure.
- [x] Go service has a basic test.
- [ ] Frontend build passes in CI or locally.
