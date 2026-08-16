# Identity and session

## Summary

This slice replaces the incomplete registration and login path with email identity, one active device, short access tokens, and rotating refresh tokens. PostgreSQL is authoritative for session and account validity on every protected request.

## Requirements

This child implements **AC-1**, **AC-5** through **AC-9**, **AC-16**, **AC-17**, and the identity and session parts of **AC-19** from [index.md](index.md).

## Decision

Use the existing bcrypt and HS256 JWT adapters. Add opaque refresh tokens backed by session token history, database enforced single session, and a session aware HTTP middleware. (basis: existing auth adapters, refresh rotation practice, and PostgreSQL partial unique indexes)

Short rationale: the current JWT only path cannot revoke access immediately, bind login to an app installation, or detect refresh replay. A hosted auth provider was not chosen because the project already has an internal identity schema and only email password auth is in scope.

## Feature design

### Data model

`users` gains required unique `phone_number`, `failed_login_count INT NOT NULL DEFAULT 0`, nullable `failed_login_window_started_at`, and nullable `login_blocked_until`. Existing rows must be handled before adding the phone constraint if any data exists. Email is at most 254 bytes, phone is at most 16 characters including `+`, and display name is 1 to 100 Unicode characters after trim.

`sessions` keeps its UUID v7 primary key, indexed user FK, required canonical UUID device ID, optional device name of at most 120 Unicode characters, issue and absolute expiry times, optional revocation time, and optional reason. Remove `refresh_token_hash`. Add a unique partial index on `user_id WHERE revoked_at IS NULL`.

`session_refresh_tokens` has UUID v7 ID, indexed session FK with cascade, unique SHA 256 token hash, issue and expiry times, optional used time, and optional revoked time.

### API details

#### `POST /api/v1/auth/sign-up`

Input is email, phone number, display name, and password. Normalize email to lowercase and phone to E.164 using default region `VN`. Trim every input except password. Apply the shared text limits in [index.md](index.md). Insert without an ID and return the database generated safe user. Duplicate email and phone map to distinct `409` codes without exposing database errors.

#### `POST /api/v1/auth/sign-in`

Input is email, password, a canonical UUID `device_id` generated once per app installation, and optional trimmed OS supplied `device_name`. Lock the user row before updating failure counters or replacing a session. Password comparison still runs for a known user even when later status checks deny access.

On success, reset failure fields, revoke old sessions and tokens, create one session and one refresh token, and issue a JWT containing `sub`, `role`, `iss`, `iat`, `exp`, and `sid`. Keep the transaction short and perform no external calls inside it.

After five failed attempts in a rolling 15 minute window, set `login_blocked_until` to 15 minutes after the threshold event. A blocked request returns `429` and `Retry-After` as delta seconds without password comparison.

#### `POST /api/v1/auth/refresh`

Input is refresh token and device ID. Hash the token before lookup. Lock the matching token and session rows. Require an active user, live session, matching device, unexpired token, and unused token. Mark the current token used, create the replacement, and issue the new pair in one transaction.

An already used token revokes the session and every token in the same transaction. The replacement token expires at the earlier of seven days after rotation and the absolute session expiry. Rotation never extends the session beyond seven days from sign in. The client must serialize refresh calls. There is no retry grace window in v1.

#### `POST /api/v1/auth/sign-out`

Require a valid bearer token and live session. Revoke the session named by `sid` and all its refresh tokens. Return `204` when already revoked.

### Middleware

Verify HS256 algorithm, issuer, signature, required expiry, subject, role, and `sid`. Query session joined to user on every protected request. Store user ID, role, and session ID in request context only when the session is live and user status is active.

### Token rules

1. Access token TTL is 15 minutes.
2. A session expires absolutely 7 days after sign in. Each refresh token expires no later than that session.
3. Refresh token material is 32 random bytes encoded as URL safe base64.
4. Only SHA 256 hashes are persisted.
5. Token values never enter logs or error messages.

### Request and response contract

JSON bodies are limited to 64 KB and reject unknown fields. Email is at most 254 bytes. Device ID must parse as a canonical UUID string. Device name is optional and at most 120 Unicode characters after trim. Refresh token input is at most 128 characters.

Success responses use the exact field sets in [index.md](index.md). All expiry fields are UTC RFC 3339 strings. `Retry-After` is always a nonnegative integer number of seconds.

### Error mapping

Shared errors use `{ "error": { "code": "...", "message": "...", "fields": {} } }`. Validation fields are optional. `INVALID_CREDENTIALS` is identical for missing email and wrong password. Pending email uses `EMAIL_NOT_VERIFIED`. Suspended and administrative locked accounts use `ACCOUNT_UNAVAILABLE`.

## Build notes

1. Prefer sqlc generated queries behind the repository adapter. Domain and usecase code must not import sqlc or pgx.
2. Use PostgreSQL row locks in a consistent order: user, session, refresh token.
3. Let unique and FK violations map to domain errors at the adapter boundary.
4. Delete the old `/register` and `/login` routes only after the replacement integration tests pass.

## Critical test scenarios

1. Concurrent sign ins end with exactly one live session.
2. Five failed attempts block the account and successful sign in after the window resets counters.
3. A second device invalidates the first device access token immediately.
4. Refresh rotation persists only hashes and replay revokes the session.
5. Sign out is idempotent and protected middleware rejects the revoked `sid`.
