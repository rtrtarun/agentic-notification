# Milestone 23: Authentication (accounts, sessions, roles, single sign-on)

## Goals
- People sign in with their own account instead of sharing one API key. Services keep using the API key.
- Two roles: **member** (works the inbox) and **admin** (also changes rules, channels, preferences and accounts).
- Sign in with email and password, or with **Microsoft Entra ID** through a generic OpenID Connect layer that Google and others can use later.
- A security log of sign-ins and account changes.

## Architecture decisions
- **`pkg/auth` has no HTTP in it.** Passwords, sessions, roles, throttling, audit and OIDC live there; `pkg/server` only translates.
- **Passwords:** argon2id (19 MiB, 2 passes), 12 to 256 characters, no composition rules. A login for an unknown email still runs a dummy hash, and every failure (unknown, wrong password, disabled, SSO-only account) returns the same message.
- **Sessions:** a random 256-bit token in an `HttpOnly`, `SameSite=Lax` cookie (`Secure` over HTTPS). The database stores only the token's SHA-256. Default lifetime 12 h. Disabling an account or changing its password ends its sessions.
- **CSRF:** every session has a second token. A request authenticated by cookie that changes state must send it in `X-CSRF-Token`; the console gets it from `/api/auth/me` and keeps it in memory. API-key calls need no token.
- **Credentials and CORS:** `Access-Control-Allow-Credentials` is sent only for an origin named exactly in `CORS_ALLOWED_ORIGINS`, never for `*`.
- **Lock-out:** 5 failures in 15 minutes lock an account; an address is locked at 25 (looser, because offices share addresses; it catches one source trying many accounts). In process, so with several replicas the effective limit is higher.
- **First admin:** created at start-up from `AUTH_BOOTSTRAP_EMAIL` and `AUTH_BOOTSTRAP_PASSWORD` only when no account exists, so a restart can never reset a password. There is no open sign-up.
- **Single sign-on:** authorization code with PKCE and a nonce; discovery from the issuer; ID token verified (RS256 only, never `none` or HMAC; issuer, audience, expiry, not-before, nonce; keys cached, refetched at most once a minute). The in-flight state is a signed cookie, so no server state is needed. A person is found by (issuer, subject) then by email (and linked); a new person is created as a **member only if their email domain is in `OIDC_ALLOWED_DOMAINS`**. An empty list never creates accounts.
- **Entra ID:** `ENTRA_TENANT_ID` builds `https://login.microsoftonline.com/{tenant}/v2.0`; the stable `oid` claim is the subject; `preferred_username` stands in when there is no `email` claim. Use a tenant ID or domain, not `common`.
- **Admin-only actions:** editing or removing rules, preference changes, channel tests, delivery retries, `/api/users`, `/api/audit`. The rule dry run and every read stay open to members. The API key counts as admin.

## Database changes
New tables (idempotent): `users` (unique email, role check), `user_identities`, `sessions`, `audit_log`.

## APIs
| Route | Who | Purpose |
|---|---|---|
| `GET /api/auth/status` | public | Whether accounts exist and whether SSO is offered |
| `POST /api/auth/login` | public, throttled | Email and password; sets the cookie |
| `GET /api/auth/oidc/start`, `/callback` | public | Single sign-on; redirect the browser |
| `POST /api/auth/logout`, `GET /api/auth/me`, `POST /api/auth/password` | signed in | Session, identity and CSRF token, own password |
| `GET/POST /api/users`, `PATCH /api/users/{id}` | admin | List, add, change role, turn off, reset password. Accounts are never deleted |
| `GET /api/audit` | admin | Recent security events |

Configuration: `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD`, `AUTH_SESSION_TTL`, `AUTH_COOKIE_SECURE` (`auto`/`true`/`false`), `AUTH_COOKIE_SAMESITE` (`lax`/`strict`/`none`), `AUTH_SECRET`, `PUBLIC_URL`, `CONSOLE_URL`, `ENTRA_TENANT_ID` or `OIDC_ISSUER`, `OIDC_NAME`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_ALLOWED_DOMAINS`. Register `PUBLIC_URL/api/auth/oidc/callback` as the redirect URI at the provider.

## Folder changes
`pkg/auth` (password, store, pgstore, service, limiter, oidc), `pkg/server` (`auth_handlers.go`, `auth_setup.go`, new middleware), web `lib/auth.ts`, `app/AuthGate.tsx`, `components/LoginPage.tsx`, `pages/UsersPage.tsx`, an account menu with sign out. `golang.org/x/crypto` is the only new dependency (pinned to a Go 1.21 compatible version).

## Risks and how they are handled
| Risk | Handling |
|---|---|
| Session theft by script | `HttpOnly` cookie; CSRF token only in memory; token stored hashed |
| Cross-site request forgery | `SameSite`, CSRF header on every cookie write, no wildcard credentials |
| Account enumeration | One message and equal timing for every failure; audit holds no secrets |
| Forged or replayed ID tokens | RS256 only, issuer/audience/expiry/nonce, PKCE, signed one-time state |
| Locking out the only admin | The last enabled admin cannot be demoted or turned off |
| Open registration through SSO | Domain allow-list; empty means none |

## Tests
`pkg/auth` (hashing, malformed hashes, bootstrap, login, expiry, generic errors, lock-out, user rules, session ending, federation linking and refusal, audit hygiene, OIDC against a fake provider that signs real RS256 tokens including `alg:none`, HMAC confusion, wrong key, wrong issuer/audience/nonce/expiry, key caching, tampered state); `pkg/server` (cookie flags, CSRF, member vs admin on every gated route, API key unchanged, user endpoints, audit, CORS credentials, full SSO round trip, start-up validation); web `lib/auth.test.ts`. 201 Vitest tests in total.

## Acceptance criteria
- [x] An unauthenticated browser gets the sign-in screen; the API key still works for scripts.
- [x] A member cannot reach admin routes (checked route by route).
- [x] A cookie write without the CSRF token is refused.
- [x] A single sign-on round trip creates a session (against a fake provider).

## Known limits (honest list)
- **Never run against real Microsoft Entra ID.** The flow is tested against a fake provider with real signatures; app registration, redirect URI and tenant settings are yours to confirm. Role claims from Entra are not used; roles live in this service.
- **The PostgreSQL account store was not run against a real database** (Docker was not running). It shares the table design with the tested memory store; run the contract test before relying on it.
- **The sign-in screen was not viewed in a browser.** It appears only after the first-visit welcome screen, which could not be clicked through headlessly. The Users screen was viewed in demo mode.
- **Preferences are still workspace-wide**; per-user preferences arrive in milestone 24.
- **No email flows:** no password reset by email, no invitations, no verification. An admin sets a starting password or resets one.
- **No MFA** beyond what the identity provider enforces for single sign-on.
- **Members still see the edit buttons** on Rules, Preferences and Channels; the service refuses with "admin access required".
- Lock-out counters are in memory and the address is the connecting one, so behind a proxy it is the proxy's address.
- Other languages fall back to English for the new screens.

## Review checklist
- [ ] Start the service with `AUTH_BOOTSTRAP_*`, open the console without an API key and sign in.
- [ ] Add a member, sign in as them, and try to save a preference (expect "admin access required").
- [ ] Try five wrong passwords: the account locks for fifteen minutes.
- [ ] Register an Entra ID app, set `ENTRA_TENANT_ID`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `PUBLIC_URL`, `OIDC_ALLOWED_DOMAINS`, and sign in with Microsoft.
- [ ] Look at the sign-in screen at 360, 768 and 1280 px.
