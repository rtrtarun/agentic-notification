# auth

People: accounts, passwords, sessions, roles, sign-in throttling, an audit trail and OpenID Connect. No HTTP lives here; `pkg/server` translates. The shared API key for services is handled by the server, not this package.

- `HashPassword` / `VerifyPassword` / `CheckPassword` — argon2id, 12 to 256 characters. A dummy hash keeps unknown-account logins as slow as real ones.
- `Service` — `Bootstrap` (first admin only when no account exists), `Login`, `LoginFederated`, `Authenticate`, `Logout`, `CreateUser`, `UpdateUser` (the last enabled admin cannot be removed; disabling or a password change ends sessions), `ChangePassword`, `Audit`.
- `Store` — users, identities, sessions, audit. `MemoryStore` and `PostgresStore` (tables `users`, `user_identities`, `sessions`, `audit_log`). Sessions store the SHA-256 of the cookie token.
- `Limiter` — locks an account after 5 failures in 15 minutes and an address at 25. In process.
- `OIDC` — authorization code with PKCE and nonce for any OpenID Connect provider. RS256 only; issuer, audience, expiry and nonce checked; keys cached. `EntraIssuer(tenant)` builds a Microsoft Entra ID issuer. `SealTransaction` / `OpenTransaction` keep the in-flight state in a signed cookie.
- `ValidCSRF` — constant-time comparison with the session's token.

Errors are deliberately uniform: `ErrInvalidCredentials` covers an unknown email, a wrong password, a disabled account and an SSO-only account.

Tests cover hashing, login and sessions, lock-out, role rules, federation (link, provision by domain, refuse), audit hygiene, and OIDC against a fake provider that signs real tokens, including `alg:none`, HMAC confusion and tampered state.
