# Milestone 36: Sign-in and sign-up for real use, two versioned images

Planned and approved in conversation on 2026-11-07, built on branch `feature/signup-and-sign-in`. The console now starts at sign-in (with sign-up when the service allows it), the API key is an "Advanced" option for services, signing out really signs out, and a person who signs up waits for an admin to approve the account. The Java service gets the same accounts. Both services are published as versioned images.

Java parity part 3 (Attention Intelligence, feedback, learning) moves to milestone 37.

## 1. Goals
1. First run goes to sign-in, not to an API key box; the service address built into the console is used.
2. People can ask for an account (`AUTH_SIGNUP=approval`); the account is pending and sees nothing until an admin approves it. Default is off.
3. Signing out ends access: an API key is forgotten too, and a reload does not walk back in.
4. A sign-in the browser does not keep (a cookie from another site) is reported, not looped back to the sign-in page.
5. The same account system, endpoints and messages in the Java service, proved by a shared contract file.
6. Two images, Go and Java, with release versions; `/health` reports the version.

## 2. Architecture decisions
- **Pending, then admin approval.** One workspace means everyone signed in sees every event, so sign-up cannot grant access by itself. Alternatives: open sign-up (only safe after multi-tenancy), invite-only (already possible: an admin creates accounts).
- **`users.status`** with `active`, `pending`, `rejected`. An empty status counts as active, so older rows keep working. `User.Active()` is the single check, used at sign-in, for every session and for the last-admin rule.
- **No information leaks.** A repeat sign-up for an existing email gets the same `202 pending` answer and changes nothing. "Waiting for approval" and "not approved" are only returned after the right password; a wrong password stays "incorrect email or password".
- **Throttling.** Sign-up allows 20 requests per address per hour; sign-in keeps its lock-out (5 failures per account, 25 per address, 15 minutes).
- **API key is for services.** It still works everywhere; the console hides it under "Advanced".
- **Java hashes passwords exactly as Go does** (`argon2id$m=19456,t=2,p=1$salt$hash`, raw standard base64) through BouncyCastle, so an account made by one service signs in on the other. A hash made by Go verifies in Java (tested with a real Go hash).
- **Versioning.** A `VERSION` file at the repository root is the single source. Pre-1.0 the minor number follows the milestone, so this release is `0.36.0`. The images carry it as tags (`0.36.0`, `0.36`, `latest`, `sha-<commit>`), as the OCI label and as `APP_VERSION`, which `/health` returns.

## 3. Database changes
`ALTER TABLE users ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'` in `pkg/db/migrate.go` (idempotent; existing users become active). Java does not migrate; it reads the column the Go migration creates.

## 4. APIs
New: `POST /api/auth/signup` (public, `{"email","name","password"}`, `202 {"status":"pending"}`; `404` when sign-up is off; `400` for a bad email or password; `429` with `Retry-After` when throttled).
Changed: `GET /api/auth/status` also returns `signup`; `POST /api/auth/login` answers `403` with `{"code":"pending"}` or `{"code":"rejected"}` after a correct password; `PATCH /api/users/{id}` accepts `{"status":"active"|"rejected"}` (admin; ends the person's sessions; audited); user objects carry `status`; `GET /health` returns `version`.
Settings: `AUTH_SIGNUP` (`off`, `approval`), `APP_VERSION`. Java also gains `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD`, `AUTH_SESSION_TTL`, `AUTH_COOKIE_SECURE`, `AUTH_COOKIE_SAMESITE`.
Java, new endpoints (same shapes as Go): `/api/auth/status`, `login`, `signup`, `logout`, `me`, and `GET`/`POST /api/users`, `PATCH /api/users/{id}`. Sessions work on every protected Java endpoint (writes need the CSRF token); rule changes and manual delivery retries need an admin.

## 5. Folder changes
`pkg/auth`, `pkg/server`, `pkg/config`, `pkg/db` (Go); `apps/web/src` (`lib/auth.ts`, `lib/settings.ts`, `lib/api.ts`, `app/AuthGate.tsx`, `components/LoginPage.tsx`, new `components/SignUpForm.tsx`, `pages/UsersPage.tsx`, `lib/i18n/en.ts`, `styles.css`); Java new `application/Accounts.java`, `application/PasswordHasher.java`, `infrastructure/MemoryAccountStore.java`, `infrastructure/PostgresAccountStore.java`, `interfaces/AuthApi.java`; `docs/contract/10-signup.json`; root `VERSION`; `.github/workflows/publish-image.yml`; both Dockerfiles.

## 6. Risks and behaviours to know
- **Sessions across sites.** The console on `github.io` and the API on `onrender.com` are different sites. The cookie is `SameSite=Lax` by default, which a browser does not send across sites, so email sign-in looks like it works and then returns to the sign-in page. Set `AUTH_COOKIE_SAMESITE=none` on the service (it forces `Secure`) as a stopgap, or host the console and the API on one site (a custom domain) as the real fix. The console now says so when it happens. Safari and some privacy settings block third-party cookies even with `none`.
- **Behind a proxy every person looks like one address.** The per-address limits use the connecting address, which on Render is the proxy, so the sign-up limit (20 per hour) and the sign-in per-address lock are shared by everyone. Trusting `X-Forwarded-For` safely needs a trusted-proxy setting; logged as finding F11.
- **Approved members see everything.** There is one workspace; sign-up approval is the only gate (finding F12, open question on multi-tenancy).
- **No email verification and no password reset yet.** Approval is by an admin who knows the person; an admin can set a password through `PATCH /api/users/{id}`.
- **Java has no CORS and no single sign-on**; the console talks to the Go service.
- Existing deployments: the first start after upgrading adds the `status` column; sign-up stays off until `AUTH_SIGNUP=approval` is set.

## 7. Tests
- Go: service tests (off by default, pending member, approval, rejection, repeat sign-up, input checks and throttle, a no-longer-approved session stops working, empty status is active); store contract (memory and PostgreSQL) for status; handler tests (status flag, sign-up then approval then sign-in, pending login, rejected session, bad input, repeat answers identical, bad setting refused); `/health` version and `APP_VERSION`.
- Java: `AccountsTest` (hash made by Go verifies, malformed hashes never verify, bootstrap, sign-in and sign-out, expiry, generic errors, lock-out, sign-up flows, last admin, audit without secrets, settings), `AuthApiTest` (status, sign-up, approval, cookie flags, CSRF, roles, rejected sessions, user endpoints, API key still works, health version), `PostgresAccountStoreTest` (real database).
- Web (Vitest): sign-in outcomes (pending, rejected, blocked cookie), sign-up request, failure wording, the demo flow, `connectSettings` and `signedOutSettings`.
- Shared contract `10-signup.json`, replayed by Go (`TestContract`) and Java (`ContractTest`).

## 8. Acceptance criteria and result
| Criterion | Result |
|---|---|
| Go: build, vet, `gofmt`, full `go test ./...` | **Yes**, locally |
| Go integration tests against PostgreSQL 16 (`-tags integration`, `pkg/auth`, `pkg/db`, `pkg/server`) | **Yes**, locally, with the Go migration applied |
| Java `mvn verify` | **Yes**: 156 tests with no database (95.2 percent line coverage, gate 80), 170 with PostgreSQL (3 skipped, Redis) |
| Contract passes on both services (10 files) | **Yes** (Go and Java) |
| Web: `tsc -b`, `npm test`, `npm run build` | **Yes**: 238 tests |
| Both images build, start and report the version | **Yes**: locally, `/health` returned `0.36.0` and the image label matched, for Go and Java |
| The publish workflow ran on GitHub | **Not yet**: it runs when this branch is merged to the default branch |
| Sign-up, approval and sign-in clicked through in a browser against the live service | **Not verified**; the live service still runs the previous release |
| Cross-site cookie behaviour with `AUTH_COOKIE_SAMESITE=none` | **Not verified** in a browser |

## 9. Review checklist
- [ ] Merge, then watch "Publish service images" push both images tagged `0.36.0`.
- [ ] Deploy `notification-service:0.36.0` on Render with `AUTH_SIGNUP=approval` and `AUTH_COOKIE_SAMESITE=none`, then click through sign-up, approval and sign-in.
- [ ] Decide on multi-tenancy, email verification, password reset and trusted-proxy handling (see `implementation-sync.md`).
