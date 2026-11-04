# Deployment and hosting

The platform has two deployable parts. They are hosted differently.

| Part | What it is | Where it can run |
|---|---|---|
| Console (`apps/web`) | Static files: HTML, CSS, JavaScript | GitHub Pages, or any static host |
| Service (`go-server/notification-service`) | A Go server that needs PostgreSQL (and optionally Redis) | Any container host: Docker Compose, Kubernetes, Fly.io, Render, AWS |

GitHub Pages cannot run the service. It only serves static files, so until a service is hosted the console runs in **demo mode** (sample data).

## Go live under GitHub

GitHub can host the **console** (GitHub Pages) and store the **service image** (GitHub Container Registry). It cannot run the Go service, which needs a server and PostgreSQL. So a fully live system is two steps: Pages for the console, then any container host for the service.

### Step 1: publish the console (live in minutes, no other account)

Workflow: [.github/workflows/pages.yml](../.github/workflows/pages.yml).

1. Push the repository to GitHub and merge to `main`:
   ```bash
   git push origin feature-impl
   # then open a pull request into main on GitHub and merge it
   ```
   The workflow runs on pushes to `main` that touch `apps/web/**`, or by hand from the Actions tab (Run workflow).
2. In the repository open **Settings, then Pages**, and set **Source** to **GitHub Actions**.
3. Open the **Actions** tab, wait for "Deploy console to GitHub Pages", and open the address it prints: `https://<user>.github.io/<repository>/`.

With nothing else configured the site opens in **demo mode** (sample data, nothing sent or saved), so it is live and explorable at once. The workflow runs the unit tests and builds with `VITE_BASE=/<repository>/` (a project site lives under a sub-path). Nothing is stored on a server; settings stay in the visitor's browser.

The Pages environment may only deploy from the default branch. If a run from another branch is refused, merge to `main` or change the rule under Settings, then Environments, then github-pages.

### Step 2: publish the service image

Workflow: [.github/workflows/publish-image.yml](../.github/workflows/publish-image.yml). On a push to `main` that touches the service, it builds the Docker image and pushes `ghcr.io/<owner>/<repository>/notification-service:latest` (and a tag per commit) using the built-in token. No secrets to create. To let a host pull it without logging in, set the package to public under your profile's Packages, then the package's settings.

### Step 3: run the service somewhere

Any host that runs a container works: Render, Fly.io, Railway, Azure Container Apps, AWS App Runner or a small VM with Docker. Give it:

| Setting | Value |
|---|---|
| Image | `ghcr.io/<owner>/<repository>/notification-service:latest` |
| Port | `8080` (the service reads `PORT`) |
| `API_KEY` | A long random value (the key people type into the console) |
| `GITHUB_WEBHOOK_SECRET` | A long random value, also entered in the GitHub webhook |
| `DATABASE_URL` | A PostgreSQL connection string. Without one, events live in memory and vanish on restart |
| `CORS_ALLOWED_ORIGINS` | `https://<user>.github.io` (origin only: no path, no trailing slash) |
| `REDIS_URL` | Optional; needed only for more than one replica |

Check it with `https://<service>/health` (expect 200). It must be served over **https**.

### Step 4: connect the live console to the live service

1. In the repository open **Settings, then Secrets and variables, then Actions, then Variables**, and add a variable `BACKEND_URL` with the service address, for example `https://notifications.example.com`.
2. Re-run "Deploy console to GitHub Pages". The console is now built without demo mode and with that address pre-filled; visitors choose **Connect your service** and enter the API key. (Remove the variable to go back to demo mode.)
3. In your GitHub repository's webhook settings, set the payload URL to `https://<service>/webhooks/github`, content type `application/json`, and the same secret as `GITHUB_WEBHOOK_SECRET`.

Build the console locally the way the workflow does:

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

## Notes on connecting a hosted console

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
