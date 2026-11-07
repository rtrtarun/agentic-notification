# Deployment and hosting

> **Status after Milestone 36** (accounts with sign-up, versioned images for both services; the live service may still run an earlier release, see "Where we have deployed").
>
> **Earlier status (milestone 35).** Two services exist: the Go service (everything below) and the Java service (milestones 34 and 35; see "The Java service" at the end). The Go service has accounts and single sign-on, a delivery retry queue, optional Ollama or Claude and Novu, and an MCP server. The service now has accounts and single sign-on, a delivery retry queue, optional Ollama or Claude and Novu, and an MCP server. The environment variables for all of it are in "Production reference" at the end of this document, and each milestone document lists its own settings.

The platform has two deployable parts. They are hosted differently.

| Part | What it is | Where it can run |
|---|---|---|
| Console (`apps/web`) | Static files: HTML, CSS, JavaScript | GitHub Pages, or any static host |
| Service (`go-server/notification-service`) | A Go server that needs PostgreSQL (and optionally Redis) | Any container host: Docker Compose, Kubernetes, Fly.io, Render, AWS |

GitHub Pages cannot run the service. It only serves static files, so until a service is hosted the console runs in **demo mode** (sample data).

## Where we have deployed
What is running, where, and how to check it. No secrets here: values live in the host dashboards and in the (git-ignored) local `env.txt`.

| Part | Where | Address | Notes |
|---|---|---|---|
| Console | GitHub Pages, from [pages.yml](../.github/workflows/pages.yml) | https://rtrtarun.github.io/agentic-notification/ | Built with the repository variable `BACKEND_URL` set to the service below, so it is not in demo mode; **Try the demo** still works from the welcome screen |
| Go service | Render web service, image `ghcr.io/rtrtarun/agentic-notification/notification-service` (set to `:latest` on 2026-11-07) | https://agentpulse-rpyb.onrender.com | Environment variables set: `PORT`, `API_KEY`, `GITHUB_WEBHOOK_SECRET`, `DATABASE_URL`, `CORS_ALLOWED_ORIGINS`, `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD`; Slack is configured only in the local `env.txt`, not yet on Render |
| Database | Neon PostgreSQL (AWS `us-east-2`), **direct** host (no `-pooler`) with `binary_parameters=yes` | project in the Neon dashboard | The pooler broke prepared statements; see the Neon note below |
| Image registry | GitHub Container Registry (public packages) | `ghcr.io/rtrtarun/agentic-notification/` | Two images, see "Releases and images" |
| Java service | **Not deployed anywhere** | none | Images are published from milestone 36; no host runs one |
| CI | GitHub Actions | the repository's Actions tab | `ci.yml` (all four jobs), `pages.yml`, `publish-image.yml` |

**What the live service is running.** `GET https://agentpulse-rpyb.onrender.com/health` returns the release in `version` once it runs `0.36.0` or later. Before that it returns no `version` field, which means a release older than milestone 36 (the one deployed on 2026-11-07 predates it). Webhooks are not connected to a repository yet.

## Secrets and variables
Names only; values are never written in this repository. Where each one lives, so a change or a rotation touches every place.

| Name | Render service (environment) | GitHub repository | Local `env.txt` (git-ignored) | Used for |
|---|---|---|---|---|
| `API_KEY` | yes | **secret** `API_KEY` (set 2026-11-07, no workflow uses it yet) | yes | Services and scripts calling the API; the console hides it under "Advanced" |
| `GITHUB_WEBHOOK_SECRET` | yes | no (it is also typed into the GitHub webhook settings of each monitored repository) | yes | Verifying `X-Hub-Signature-256` on webhooks |
| `DATABASE_URL` | yes (Neon **direct** host, `sslmode=require`, `binary_parameters=yes`) | no | yes (the old pooler string, to be updated) | PostgreSQL |
| `CORS_ALLOWED_ORIGINS` | yes (`https://rtrtarun.github.io`, a public value) | no | yes | Letting the console call the service |
| `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD` | yes | no | yes | Creating the first admin once, when no account exists |
| `SLACK_WEBHOOK_URL`, `NOTIFY_STANDARD_CHANNELS` | **not yet** | no | yes | Delivering alerts to Slack |
| `AUTH_SIGNUP`, `AUTH_COOKIE_SAMESITE` | **not yet** (needed for milestone 36 features) | no | no | Sign-up with admin approval; sessions across sites |
| `BACKEND_URL` | no | **variable** `BACKEND_URL` (the service address, public) | no | Building the console with the service address |

- **A GitHub secret can be used by a workflow**, for example a post-deploy smoke check (`/health` for the version, then an authenticated call). Nothing does that yet.
- **Never put a secret in the console build.** Anything in a built bundle is public; the API key is typed in or, better, people sign in with an account.
- **Rotating the API key:** generate a new one (`openssl rand -hex 32`), then update Render, the GitHub secret (`gh secret set API_KEY --repo rtrtarun/agentic-notification`) and `env.txt`, and redeploy. Anything else calling the API with the old key must be updated too.
- **Rotating the database password:** reset the role's password in Neon, update `DATABASE_URL` on Render, redeploy.
- **Secrets pasted into a chat or a log are exposed**: the Neon password and the Slack webhook were shared in conversation on 2026-11-07 and should be rotated.

## Deployment log
What was done to get here, newest first. Dates are 2026-11-07 unless stated.

1. **Milestone 36 built, not deployed.** Sign-in first, sign-up with admin approval, Java accounts, two versioned images, `VERSION` `0.36.0`, CI on `master`. On branch `feature/signup-and-sign-in`; merging it will run the publish workflow and redeploy the console.
2. **Repository secret `API_KEY` added** (from the local `env.txt`) with the GitHub CLI after signing in as the repository owner.
3. **Test events sent to the live service** (a push, a star, a security advisory, a repeat delivery and an agent step) through the signed webhook endpoint and `POST /api/events`: all decided correctly, the repeat answered `duplicate`. They are stored in the Neon database under the repository `acme/billing` and show in the Inbox and Traces.
4. **Console connected to the service:** repository variable `BACKEND_URL` set and "Deploy console to GitHub Pages" re-run; the live site was built without demo mode.
5. **CORS fixed:** the first preflight from the console origin had no `Access-Control-Allow-Origin`; after `CORS_ALLOWED_ORIGINS=https://rtrtarun.github.io` was corrected and the service redeployed, it returned the exact origin.
6. **Neon pooler problem found and fixed:** with a `-pooler` host the delivery dispatcher and the retention purge failed every few seconds with prepared-statement errors. Using the direct host with `binary_parameters=yes` gave a clean start (see the Neon note in the worked example).
7. **Service created on Render** from the public image `notification-service:latest`, with Neon as the database (`/health` 200, unsigned requests 401 as designed).
8. **Pull requests #9 and #10 merged to `master`:** the Pages workflow and the image workflow ran green; the Go image was published and made public.
9. **Earlier, in the same project:** the console on GitHub Pages in demo mode; CI green for the Go, Java, web and SDK jobs on the feature branch.

Not done yet: webhooks from a real repository, Slack on the live service, `AUTH_SIGNUP` and `AUTH_COOKIE_SAMESITE` on Render, deploying a versioned tag, a post-deploy smoke check, and running the Java service anywhere.

## Releases and images
Every release publishes **two images** to GitHub Container Registry, built by [publish-image.yml](../.github/workflows/publish-image.yml):

| Image | Service | Used by |
|---|---|---|
| `ghcr.io/rtrtarun/agentic-notification/notification-service` | Go (the one the console talks to) | Render today |
| `ghcr.io/rtrtarun/agentic-notification/notification-service-java` | Java | nothing yet |

- **The release number is the `VERSION` file** at the repository root (`0.36.0` for milestone 36). Pre-1.0 the minor number follows the milestone; patch numbers are fixes.
- **Tags on each image:** `0.36.0` (this release), `0.36` (latest patch of that minor), `latest`, and `sha-<commit>` (always unique).
- **Running service identification:** the version is baked into the image (`APP_VERSION`, also the `org.opencontainers.image.version` label) and `GET /health` returns it, so you can tell what is running without a commit hash. A build outside the workflow reports `dev`.
- **To release:** edit `VERSION`, update `docs/CHANGELOG.md`, merge to the default branch. The workflow runs when `VERSION`, either service or the workflow changes (or by hand from the Actions tab). Publishing a version that already exists moves that tag to the new build and prints a warning; the `sha-` tag never moves.
- **To deploy a release:** point the host at a version tag instead of `latest`, for example `notification-service:0.36.0`, so a restart never picks up an unreviewed build. On Render: the service's settings, image URL, then redeploy.
- **Check:** `docker run --rm -p 8080:8080 -e API_KEY=k ghcr.io/rtrtarun/agentic-notification/notification-service:0.36.0` then `curl localhost:8080/health`.

## Go live under GitHub

GitHub can host the **console** (GitHub Pages) and store the **service image** (GitHub Container Registry). It cannot run the Go service, which needs a server and PostgreSQL. So a fully live system is two steps: Pages for the console, then any container host for the service.

### Step 1: publish the console (live in minutes, no other account)

Workflow: [.github/workflows/pages.yml](../.github/workflows/pages.yml).

1. Push the repository to GitHub and merge to the default branch (`master` or `main`):
   ```bash
   git push origin feature-impl
   # then open a pull request into master on GitHub and merge it
   ```
   The workflow runs on pushes to `main` or `master` that touch `apps/web/**`, or by hand from the Actions tab (Run workflow).
2. In the repository open **Settings, then Pages**, and set **Source** to **GitHub Actions**.
3. Open the **Actions** tab, wait for "Deploy console to GitHub Pages", and open the address it prints: `https://<user>.github.io/<repository>/`.

With nothing else configured the site opens in **demo mode** (sample data, nothing sent or saved), so it is live and explorable at once. The workflow runs the unit tests and builds with `VITE_BASE=/<repository>/` (a project site lives under a sub-path). Nothing is stored on a server; settings stay in the visitor's browser.

The Pages environment may only deploy from the default branch. If a run from another branch is refused, merge to `main` or change the rule under Settings, then Environments, then github-pages.

### Step 2: publish the service images

See "Releases and images" above: both the Go and the Java image are published, with the release from `VERSION`. The rest of this step describes the Go image, which is the one the console uses.

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
docker compose up --build postgres redis go-notification-service   # service on :8080, PostgreSQL, Redis
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
- Anyone who knows the service address can send it requests. The API key gates services; people sign in with an account (milestone 23): set `AUTH_BOOTSTRAP_EMAIL` and `AUTH_BOOTSTRAP_PASSWORD` once to create the first admin (they are used only when no account exists), and use `AUTH_COOKIE_SECURE` and `PUBLIC_URL` for an https deployment. Prefer sign-in over typing the key into the console.
- Do not set `VITE_API_KEY` in a hosted build. Anything in a built bundle is public.

## Checks before announcing a deployment

| Check | How |
|---|---|
| Console loads at the Pages address | Open it; the welcome or demo view appears |
| Assets load from the sub-path | Browser network tab shows `/<repository>/assets/...` with 200 |
| Service is healthy | `GET https://<service>/health` returns 200 |
| CORS is right | Connection settings save without "Can't reach" |
| Signed webhook accepted | Send a test delivery from the GitHub webhook page; expect 202 |

## Production reference

Everything in the earlier steps, as one checklist for going live at `https://rtrtarun.github.io/agentic-notification/` (console) and your own HTTPS service address (API). Variables marked **planned** belong to a milestone that is not built yet; setting them today does nothing.

### Order of work
1. Settings, then Pages, source **GitHub Actions**; merge to `main`. The console is live in demo mode.
2. Merge a change under `go-server/notification-service` to `main`; the image is pushed to `ghcr.io/rtrtarun/agentic-notification/notification-service:latest`. Make the package public.
3. Provision PostgreSQL (Neon, Supabase or Render) and, only for more than one replica, Redis (Upstash). Run the image on port 8080 with the variables below.
4. Add the repository variable `BACKEND_URL`, re-run the Pages workflow, and add the GitHub webhook.

### Service environment

| Group | Variable | Production value |
|---|---|---|
| Security (fail closed) | `API_KEY` | 32+ random bytes, for example `openssl rand -hex 32` |
| | `GITHUB_WEBHOOK_SECRET` | Random secret, identical to the one in the GitHub webhook |
| | `CORS_ALLOWED_ORIGINS` | `https://rtrtarun.github.io` (origin only, no path, no trailing slash) |
| | `ALLOW_UNAUTHENTICATED_API`, `ALLOW_UNSIGNED_WEBHOOKS` | Leave unset |
| Data | `DATABASE_URL` | `postgres://user:pass@host:5432/db?sslmode=require` |
| | `REDIS_URL` | `rediss://default:pass@host:6379`; shared deduplication across replicas |
| | `EVENT_RETENTION` | For example `2160h` (90 days) |
| | `DEDUP_WINDOW` | `10m` |
| Attention | `ATTENTION_PROVIDER` | `heuristic` (default), `ollama` or `claude`; a model that fails falls back to the heuristic |
| | `ANTHROPIC_API_KEY` | Required when the provider is `claude` |
| | `OLLAMA_URL`, `OLLAMA_MODEL` | When the provider is `ollama` |
| | `ATTENTION_CLASSIFY`, `ATTENTION_DIGEST_EVERY` | `true`, and for example `1h` |
| Channels | `SLACK_WEBHOOK_URL` | Your Slack incoming webhook |
| | `NOTIFY_ALLOWED_WEBHOOK_HOSTS` | `hooks.slack.com,webhook.office.com` (the SSRF allow-list) |
| | `NOTIFY_STANDARD_CHANNELS`, `NOTIFY_DIGEST_CHANNELS` | For example `slack,email` |
| Attention budget (milestone 29) | `ATTENTION_BUDGET_PER_HOUR` | Standard interruptions per rolling hour (default `10`, `0` off); shared across replicas through `REDIS_URL` |
| | `ATTENTION_ASYNC`, `ATTENTION_WORKER_INTERVAL` | Defer the attention step to a background worker (default off); poll interval (default `2s`) |
| Email (milestone 32) | `NOVU_API_KEY`, `NOVU_API_URL`, `NOVU_WORKFLOW_ID` | Email through Novu, with SMTP as the fallback when SMTP is configured; exactly one provider sends each email |
| | `SMTP_REQUIRE_STARTTLS` | `true` refuses to send mail over a plaintext connection |

The budget, the worker, agent traces and the Novu provider are implemented. Their PostgreSQL, Redis and real-service parts have only run in CI or against local fakes; try Novu with one test recipient before relying on it. 

### Claude Desktop or Cursor (built in milestone 31)
Build the MCP server once with `go build -o bin/mcp-server ./cmd/mcp-server` in `go-server/notification-service`, then configure the client (the same tools are also served over HTTP at `POST /api/mcp`). It has not been tried with a real MCP client yet:

```json
{
  "mcpServers": {
    "agentpulse": {
      "command": "/path/to/go-server/notification-service/bin/mcp-server",
      "env": { "AGENTPULSE_URL": "https://your-service", "AGENTPULSE_API_KEY": "your-api-key" }
    }
  }
}
```

### Smoke checks
- `GET https://<service>/health` returns 200.
- Redeliver a ping from the GitHub webhook page to `https://<service>/webhooks/github`: first delivery is accepted (202), a redelivery of the same event answers `{"status":"duplicate"}` with 200.
- `GET https://<service>/api/attention/status` with the API key shows the provider and a closed breaker.

## Worked example: Render and Neon
One concrete path through "Order of work". Dashboard labels change; match by meaning. Not tested on these providers.

1. **Secrets:** run `openssl rand -hex 32` twice: the first is `API_KEY`, the second `GITHUB_WEBHOOK_SECRET`. Keep them in a password manager; never commit them.
2. **Database (Neon):** create a project and copy the connection string (`postgres://user:pass@host/db?sslmode=require`) as `DATABASE_URL`. The service creates its tables at start-up.
3. **Service (Render):** New, Web Service, deploy an existing image: `ghcr.io/rtrtarun/agentic-notification/notification-service:0.36.0` (public; use a version tag so restarts do not pick up a new build, or `:latest` to follow releases). Use a plan that stays running (a sleeping service can miss GitHub's webhook timeout). Health check path `/health`. Variables:

| Variable | Value |
|---|---|
| `PORT` | `8080` |
| `API_KEY`, `GITHUB_WEBHOOK_SECRET` | the two secrets |
| `DATABASE_URL` | the Neon **direct** string (host without `-pooler`) with `?sslmode=require&binary_parameters=yes` (see the Neon note below) |
| `CORS_ALLOWED_ORIGINS` | `https://rtrtarun.github.io` |
| `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD` | creates the first admin once |
| `AUTH_COOKIE_SAMESITE` | `none` when the console is on another site (GitHub Pages and Render are), or sessions will not stick (it forces `Secure`; use one site for both as the real fix) |
| `AUTH_SIGNUP` | `approval` to let people ask for an account (an admin approves each one); leave unset to keep sign-up off |
| `SLACK_WEBHOOK_URL`, `NOTIFY_STANDARD_CHANNELS=slack` | recommended, so alerts have somewhere to go |

   Leave `ALLOW_UNAUTHENTICATED_API` and `ALLOW_UNSIGNED_WEBHOOKS` unset.
4. **Check:** `/health` returns 200; `/events` without a key returns 401 and with `X-API-Key` returns 200; `/api/attention/status` shows the provider.
5. **Console:** add the repository variable `BACKEND_URL` (the Render address) and re-run "Deploy console to GitHub Pages"; choose Connect your service and enter the key, or sign in with the bootstrap account.
6. **Webhook:** in the monitored repository add `https://<service>/webhooks/github`, `application/json`, the same secret. The ping shows 202; Redeliver answers 200 `duplicate`.

**Neon note (found deploying 2026-11-07).** With a `-pooler` host the service logged `bind message has 15 result formats but query has 18 columns` (the delivery dispatcher) and `bind message supplies 1 parameters, but prepared statement "" requires 3` (the retention purge) every few seconds. Neon's pooler is PgBouncer in transaction mode: the Go driver's two-step queries from two background workers (the dispatcher's 15-column job query and the attention worker's 18-column event query) landed on one backend connection and clashed. The fix that worked: the direct host and `binary_parameters=yes` on `DATABASE_URL`; after the redeploy the start-up log was clean. Not tested: the direct host alone, or `binary_parameters=yes` alone. The service also takes a lock while migrating, which a pooler can break.

If Slack is not configured a critical delivery can fail and answer 500; that is the designed behaviour (the sender retries). The ingestion endpoints have no rate limiting yet (finding F1 in [implementation-sync.md](implementation-sync.md)), so keep `API_KEY` private. Fly.io, Railway or a VM with Docker take the same image and variables.

## The Java service
Image: `ghcr.io/rtrtarun/agentic-notification/notification-service-java` (published with every release, tagged like the Go image), or built from `java-server/notification-service/Dockerfile`; Compose service `java-notification-service` on port 8081.

- **Run:** `docker compose up --build postgres redis go-notification-service java-notification-service`, then `curl localhost:8081/health`. The Go service must start first because it creates the schema (Java never migrates; it waits up to 60 seconds for the `events` table). `go run ./cmd/migrate` in the Go module can create the schema on its own.
- **Settings:** the Go service's names: `PORT`, `API_KEY`, `GITHUB_WEBHOOK_SECRET`, `DATABASE_URL`, `REDIS_URL`, `RULES_PATH`, `DEDUP_WINDOW`, `SMTP_*`, `SLACK_WEBHOOK_URL`, `TEAMS_WEBHOOK_URL`, `NOTIFY_*`, `EVENT_RETENTION`, `LOG_LEVEL`. With `REDIS_URL` set it refuses to start if Redis is unreachable.
- **Rules:** it refuses rules that use `payloadMatch` or per-rule `channels` (Go only). Do not share a `rules.yaml` that uses them.
- **Do not run both services against one database with different delivery settings.** Both run a dispatcher on `delivery_jobs`; a queued job goes to whichever claims it first and can be dead-lettered by a service that lacks the channel. Run one implementation per database, or configure both alike.
- **Accounts (milestone 36):** the same sign-in, sign-up with admin approval and sessions as Go, shared database tables, settings `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD`, `AUTH_SESSION_TTL`, `AUTH_COOKIE_SECURE`, `AUTH_COOKIE_SAMESITE`, `AUTH_SIGNUP`. No CORS and no single sign-on, so the console uses the Go service.
- **Not in Java:** single sign-on, Attention Intelligence, feedback, learning, traces, MCP, SMS, Novu, metrics, retention. See [java-service.md](java-service.md).
- **Smoke checks:** same as the Go service for `/health`, `/events`, `/webhooks/github` and `/notify`; the shared contract in [contract/](contract/README.md) is the reference for behaviour.
