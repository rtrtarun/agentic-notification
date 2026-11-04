# Deployment and hosting

The platform has two deployable parts. They are hosted differently.

| Part | What it is | Where it can run |
|---|---|---|
| Console (`apps/web`) | Static files: HTML, CSS, JavaScript | GitHub Pages, or any static host |
| Service (`go-server/notification-service`) | A Go server that needs PostgreSQL (and optionally Redis) | Any container host: Docker Compose, Kubernetes, Fly.io, Render, AWS |

GitHub Pages cannot run the service. It only serves static files, so the hosted console runs in **demo mode** (sample data) until someone enters a real service address and key in Connection settings.

## Host the console on GitHub Pages

Workflow: [.github/workflows/pages.yml](../.github/workflows/pages.yml).

One-time setup:

1. Push the repository to GitHub and merge to `main`. The workflow runs on pushes to `main` that touch `apps/web/**`, or manually from the Actions tab (Run workflow).
2. In the repository, open Settings, then Pages, and set Source to **GitHub Actions**.
3. Open the Actions tab and wait for "Deploy console to GitHub Pages". It prints the site address, normally `https://<user>.github.io/<repository>/`.

What the workflow does: installs with `npm ci`, runs the unit tests, builds with `VITE_BASE=/<repository>/` (a project site lives under a sub-path) and `VITE_DEFAULT_DEMO=true`, then publishes `apps/web/dist`. A first-time visitor lands in demo mode with a notice and a "Connect your service" link. Nothing is stored on a server; settings live in the visitor's browser tab.

The Pages environment may only deploy from the default branch. If a run from another branch is refused, merge to `main` or change the environment's branch rule in Settings, then Environments, then github-pages.

Build the same thing locally:

```bash
cd apps/web
VITE_BASE=/agentic-notification/ VITE_DEFAULT_DEMO=true npm run build
npx vite preview     # serves dist/
```

## Run the whole platform

On one machine, with Docker:

```bash
docker compose up --build        # service on :8080, PostgreSQL, Redis
cd apps/web && npm install && npm run dev      # console on :3000
```

Enter `http://localhost:8080` and the value of `API_KEY` in Connection settings. Compose allows the console origins `http://localhost:3000` and `http://localhost:5173` for CORS.

On a server: build the service image from `go-server/notification-service/Dockerfile` (multi-stage, non-root, health check). Run it with the environment variables in the service README, using `k8s/` manifests if you use Kubernetes. Set secrets (`API_KEY`, `GITHUB_WEBHOOK_SECRET`, `DATABASE_URL`) from your platform's secret store, never in the image or the repository.

## Connect the hosted console to a real service

1. Host the service on a public **https** address. A page served over https cannot call a plain-http server (browsers block it as mixed content). The one exception is `http://localhost`, which most browsers except Safari allow.
2. Set `CORS_ALLOWED_ORIGINS` on the service to the exact console origin, for example `https://<user>.github.io` (origin only: no path, no trailing slash). Without it the browser blocks the calls and the console says "Can't reach ...".
3. Open the hosted console, choose Connection settings, enter the service address and `API_KEY`, and save. The key is kept in that tab only.

## Security notes for a public console

- The console is public static code. It contains no secrets; the API key is typed in by the person using it.
- The service must stay fail-closed: set `API_KEY` and `GITHUB_WEBHOOK_SECRET`, and leave `ALLOW_UNAUTHENTICATED_API` and `ALLOW_UNSIGNED_WEBHOOKS` unset. Both exist only for local development.
- Anyone who knows the service address can send it requests, so the key is the only gate until real user login exists (listed as open in [ui-architecture.md](ui-architecture.md) section 16).
- Do not set `VITE_API_KEY` in a hosted build. Anything in a built bundle is public.

## Checks before announcing a deployment

| Check | How |
|---|---|
| Console loads at the Pages address | Open it; the welcome or demo view appears |
| Assets load from the sub-path | Browser network tab shows `/<repository>/assets/...` with 200 |
| Service is healthy | `GET https://<service>/health` returns 200 |
| CORS is right | Connection settings save without "Can't reach" |
| Signed webhook accepted | Send a test delivery from the GitHub webhook page; expect 202 |
