# 0001. Auth and account v1

**Date**: 2026-08-16
**Status**: In Progress

## Summary

PaySplit v1 uses email and password for identity, database backed sessions for immediate revocation, Gmail SMTP for account email, and Cloudinary for avatars. Registration also requires a unique Vietnamese phone number, but phone login and phone verification are outside v1. PostgreSQL 18 generates UUID v7 identifiers for every table.

## Structure

1. [Identity and session](0001-identity-session.md): registration, sign in, refresh rotation, one active device, sign out, and request authentication.
2. [Email and password recovery](0002-email-password.md): email verification, resend, forgot password, reset password, and change password.
3. [Profile and avatar](0003-profile-avatar.md): profile reads and updates, bank validation, image conversion, and Cloudinary storage.

## Requirements

**User stories**:

1. As a guest, I want to register with email, phone number, display name, and password so that I can create a PaySplit account.
2. As a user, I want to verify my email and sign in on one device so that access can be revoked immediately when another device signs in.
3. As a user, I want to recover or change my password securely so that a lost credential does not leave old sessions active.
4. As a user, I want to maintain my profile, bank details, and avatar so that groups and VietQR use current information.

**Acceptance criteria**:

1. **AC-1**: Sign up requires a syntactically valid unique email, a required unique phone number normalized to E.164 with region `VN`, a nonempty display name, and a password of 8 to 72 bytes containing lowercase, uppercase, and a digit.
2. **AC-2**: Successful sign up creates a `pending_verification` user, sends an email verification token when Gmail is available, returns `201`, never returns auth tokens, and reports `verification_email_sent` without rolling back the account when email delivery fails.
3. **AC-3**: Email verification uses a random single use token valid for 10 minutes, activates the account, is idempotent after success while the consumed token record remains in the 30 day retention window, and does not sign the user in automatically. Superseded and expired tokens return an invalid token response.
4. **AC-4**: Resend verification and forgot password return the same `202` response for existing, missing, active, and pending accounts, while only eligible accounts receive email.
5. **AC-5**: Sign in accepts only email, password, required `device_id`, and optional `device_name`; only active users receive tokens and all old active sessions are revoked before the new session is committed.
6. **AC-6**: Five failed sign in attempts within 15 minutes block the account identifier for 15 minutes, return `429` with `Retry-After`, persist in PostgreSQL, and reset after successful sign in.
7. **AC-7**: Access tokens expire after 15 minutes and include user ID, role, issuer, expiry, and session ID claim `sid`; every protected request rejects a revoked session or a nonactive user even when the JWT signature is valid.
8. **AC-8**: Opaque refresh tokens are stored only as SHA 256 hashes, rotate atomically, and revoke the session when an already used token is presented again. A session has an absolute lifetime of 7 days from sign in, and rotation never extends a replacement refresh token beyond that session expiry.
9. **AC-9**: Sign out is idempotent, revokes the current session and all its refresh tokens, and returns `204`.
10. **AC-10**: Password reset tokens expire after 10 minutes, are single use, and a successful reset changes the password and revokes every session in one transaction.
11. **AC-11**: Change password requires the current password, applies the shared password policy, keeps the current session and its current refresh tokens, revokes every other session and their refresh tokens, and returns `204`.
12. **AC-12**: The authenticated user can read and patch their own safe profile; email cannot change in v1, phone remains unique E.164, and bank fields update or clear as one validated group.
13. **AC-13**: Bank validation loads the committed snapshot sourced from `https://vietqr.app/banks.json`, accepts only `supported: true`, requires an account number of 6 to 19 digits, and requires a nonempty account holder.
14. **AC-14**: Avatar upload accepts at most 10 MB, removes metadata, applies EXIF orientation, limits the longest edge to 1024 pixels, and stores a WebP result at quality 82. Backend conversion runs first with a 10 second processing timeout and bounded concurrency, while Cloudinary conversion is the fallback. V1 does not impose a fixed source pixel count limit.
15. **AC-15**: Replacing an avatar uses a unique public ID, updates the database only after the new asset succeeds, preserves the old avatar on failure, and cleans the old asset after success. Avatar deletion is idempotent and clears the profile even when Cloudinary cleanup must enter a durable PostgreSQL retry queue.
16. **AC-16**: Verification resend and forgot password allow at most one request per rolling minute and 10 requests per rolling hour on each independent normalized email and IP dimension. Exceeding either dimension rejects the request. Sign up allows at most 10 requests per rolling hour for each IP. Rate events persist in PostgreSQL and use hashed keys.
17. **AC-17**: Public errors use stable `code` and `message` fields plus optional validation `fields`; logs and metrics never contain passwords, token values, full email, phone number, bank number, or tokenized URLs.
18. **AC-18**: Expired, used, and revoked auth records remain for 30 days, then a daily cleanup uses a PostgreSQL advisory lock and bounded batches so only one API instance performs cleanup.
19. **AC-19**: PostgreSQL 18 supplies `DEFAULT uuidv7()` for every primary UUID, the API omits generated IDs on insert, and the live schema is recreated and verified on a PostgreSQL 18 volume.

## Decision

**Chosen option**: Extend the existing modular auth code with database backed sessions, explicit email workflows, and a separate profile surface. Replace the unpublished `/register` and `/login` routes directly with `/sign-up` and `/sign-in`, while building the three child slices in order. (basis: existing module boundaries, the PRD auth flows, and the installed PostgreSQL skill)

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Cross child data model

| Entity | Key fields | Constraints and relationships |
|---|---|---|
| `users` | UUID v7 `id`, email, E.164 phone, password hash, profile fields, role, status, verification timestamp, failed sign in count, failure window start, blocked until, timestamps | Email and phone are required and unique. Primary ID defaults to `uuidv7()`. Text lengths follow the input contract below |
| `sessions` | UUID v7 `id`, user ID, device ID, nullable device name, issued and absolute expiry times, revoked time and reason | FK to users with an index. Device ID is a canonical UUID string. A partial unique index permits one row per user where `revoked_at IS NULL` |
| `session_refresh_tokens` | UUID v7 `id`, session ID, token hash, issued, expiry, used, and revoked times | FK to sessions with cascade and an index. Token hash is unique |
| `user_tokens` | UUID v7 `id`, user ID, type, token hash, expiry, used time, superseded time, created time | Type is email verification or password reset. Token hash is unique. FK to users is indexed. A partial unique index permits one unsuperseded and unused token for each user and type |
| `auth_rate_limit_events` | UUID v7 `id`, action, dimension, hashed key, occurred time | Supports exact rolling windows. An index covers action, dimension, hashed key, and descending occurrence time |
| `media_cleanup_jobs` | UUID v7 `id`, provider, object key, attempt count, next attempt, last error code, completion time, timestamps | A partial unique index prevents duplicate open cleanup jobs for the same provider object |
| `banks.json` | Code, BIN, short name, supported flag | Embedded repository snapshot, not a database table |

All UUID primary keys in the whole initial schema receive `DEFAULT uuidv7()`, not only auth tables. The old `sessions.refresh_token_hash` column is replaced by `session_refresh_tokens`.

### Cross child state transitions

```text
user: pending_verification → active → suspended | locked → active
session: active → revoked | expired
refresh token: active → used | revoked | expired
user token: active → used | expired | superseded
```

Only admin workflows may change `suspended` or administrative `locked`. Temporary sign in blocking uses timestamp fields and never changes account status.

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/auth/sign-up` | POST | email, phone, display name, password | safe user, status, verification send result and expiry | public | `VALIDATION_FAILED`, `EMAIL_EXISTS`, `PHONE_EXISTS`, `RATE_LIMITED` |
| `/api/v1/auth/verify-email` | POST | token | active status | public | `INVALID_OR_EXPIRED_TOKEN` |
| `/api/v1/auth/resend-verification` | POST | email | generic accepted message | public | `VALIDATION_FAILED`, `RATE_LIMITED` |
| `/api/v1/auth/sign-in` | POST | email, password, device ID, optional device name | safe user, access token, refresh token, expiries | public | `INVALID_CREDENTIALS`, `EMAIL_NOT_VERIFIED`, `ACCOUNT_UNAVAILABLE`, `RATE_LIMITED` |
| `/api/v1/auth/refresh` | POST | refresh token, device ID | rotated access and refresh tokens, expiries | refresh token | `INVALID_OR_EXPIRED_TOKEN`, `SESSION_REVOKED` |
| `/api/v1/auth/sign-out` | POST | bearer token | no body | bearer and live session | `AUTHENTICATION_REQUIRED` |
| `/api/v1/auth/forgot-password` | POST | email | generic accepted message | public | `VALIDATION_FAILED`, `RATE_LIMITED` |
| `/api/v1/auth/reset-password` | POST | token, new password | no body | reset token | `INVALID_OR_EXPIRED_TOKEN`, `VALIDATION_FAILED` |
| `/api/v1/users/me/password` | PUT | current password, new password | no body | bearer and live session | `INVALID_CURRENT_PASSWORD`, `VALIDATION_FAILED`, `ACCOUNT_UNAVAILABLE` |
| `/api/v1/users/me` | GET | none | safe profile and derived avatar URL | bearer and live session | `AUTHENTICATION_REQUIRED` |
| `/api/v1/users/me` | PATCH | profile and bank fields | updated safe profile | bearer and live session | `VALIDATION_FAILED`, `PHONE_EXISTS`, `UNSUPPORTED_BANK` |
| `/api/v1/users/me/avatar` | PUT | multipart avatar | avatar URL | bearer and live session | `INVALID_IMAGE`, `PAYLOAD_TOO_LARGE`, `IMAGE_STORAGE_FAILED` |
| `/api/v1/users/me/avatar` | DELETE | none | no body | bearer and live session | `AUTHENTICATION_REQUIRED` |
| `/api/v1/banks` | GET | optional `supported` query param | list of VietQR banks | public | none |

The old `/api/v1/auth/register` and `/api/v1/auth/login` routes are removed without aliases.

### HTTP contract

All JSON endpoints reject unknown fields and request bodies over 64 KB. Time values use UTC RFC 3339 strings. Token inputs are JSON strings no longer than 128 characters. `verify-email` accepts only `token`. `reset-password` accepts `token` and `new_password`. `refresh` accepts `refresh_token` and `device_id`.

The canonical safe user object contains `id`, `email`, `phone_number`, `display_name`, `role`, `status`, `email_verified_at`, `bank_code`, `bank_account_number`, `bank_account_holder`, `avatar_url`, `created_at`, and `updated_at`. Nullable values are JSON `null`. Password hashes, token data, Cloudinary keys, login counters, and internal revocation fields never appear.

| Endpoint | Exact success body |
|---|---|
| `POST /auth/sign-up` | `201` with `user`, `verification_email_sent`, and `verification_expires_at` |
| `POST /auth/verify-email` | `200` with `status: "active"` |
| `POST /auth/resend-verification` | `202` with `message: "If the account is eligible, an email will be sent."` |
| `POST /auth/sign-in` | `200` with `user`, `token_type: "Bearer"`, `access_token`, `access_token_expires_at`, `refresh_token`, and `refresh_token_expires_at` |
| `POST /auth/refresh` | `200` with `token_type: "Bearer"`, `access_token`, `access_token_expires_at`, `refresh_token`, and `refresh_token_expires_at` |
| `POST /auth/sign-out` | `204` with no body |
| `POST /auth/forgot-password` | `202` with the same generic message as resend verification |
| `POST /auth/reset-password` | `204` with no body |
| `PUT /users/me/password` | `204` with no body |
| `GET /users/me` | `200` with `user` |
| `PATCH /users/me` | `200` with updated `user` |
| `PUT /users/me/avatar` | `200` with `avatar_url` |
| `DELETE /users/me/avatar` | `204` with no body |
| `GET /banks` | `200` with `banks` array and `Cache-Control: public, max-age=86400` |

Input strings are trimmed before validation except passwords and token material. Email is at most 254 bytes after normalization. Display name is 1 to 100 Unicode characters after trim. Phone is stored in E.164 and is at most 16 characters including `+`. Device ID is a canonical UUID generated once per app installation. Optional device name is at most 120 Unicode characters after trim. Bank account holder is 1 to 100 Unicode characters after trim. Bank account number is 6 to 19 ASCII digits.

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| Sign up | User ID and timestamps | PostgreSQL 18 defaults and `RETURNING` |
| Sign up | Normalized phone | Request phone parsed with default region `VN` |
| Sign up and password changes | Password policy result | Shared validator fixed by AC-1 |
| Sign up | Verification send result and expiry | Gmail adapter result and persisted token expiry |
| Verify email | Active account status | `users.status` after a valid token transaction |
| Resend and forgot password | Generic accepted message | Static API contract, independent of account lookup result |
| Verification and reset email | Token expiry | Transaction timestamp plus configured 10 minute TTL |
| Email links | Callback URL | `AUTH_EMAIL_VERIFICATION_URL` or `AUTH_PASSWORD_RESET_URL` plus token query parameter |
| Sign in | User status and block state | `users` columns read under transaction |
| Sign in | Device identity | Required canonical UUID generated once by the client installation; optional trimmed OS supplied `device_name` |
| Sign in and refresh | Session ID | PostgreSQL generated `sessions.id`, returned and placed in JWT `sid` |
| Sign in and refresh | Token expiries | Access expiry is issue time plus 15 minutes. `sessions.expires_at` is the sign in transaction time plus 7 days. Refresh expiry is the earlier of issue time plus 7 days and `sessions.expires_at` |
| Sign in and refresh | Raw token values | JWT signer plus 32 random refresh bytes; only refresh hash is persisted |
| Sign out and password mutation | Empty success response | Fixed `204` endpoint contract |
| Profile read | Avatar URL | Cloudinary adapter derives a delivery URL from the unique stored `avatar_object_key` public ID |
| Profile read and patch | Safe profile fields | Authenticated user row selected by JWT subject and live session |
| Profile update | Valid bank | Embedded `banks.json` entry whose `supported` field is true |
| Avatar upload | Stored public ID | Backend generated UUID v7 suffix plus authenticated user ID, confirmed by the Cloudinary signed upload response after WebP conversion |
| Avatar upload | Dimensions, quality, and size result | Uploaded bytes plus fixed AC-14 limits |
| Error response | Stable code and request message | Domain error mapping in shared HTTP helpers |

### Cross child invariants

1. A user has at most one nonrevoked session, enforced by a partial unique index.
2. External calls to Gmail and Cloudinary never run inside a database transaction.
3. Refresh rotation locks the session and token rows in one short transaction. A used token revokes its whole session.
4. The Flutter client creates `device_id` once per installation, stores it in secure storage, and serializes refresh calls with a single flight mechanism.
5. Passwords use bcrypt. Email, refresh, verification, and reset tokens are never stored or logged in plaintext.
6. Public account discovery endpoints return generic responses where account enumeration is possible.
7. Protected endpoints validate JWT signature, issuer, expiry, `sid`, live session, and active account.
8. `Retry-After` always uses delta seconds, never an HTTP date.
9. Email and IP rate limits are independent. Each dimension is checked and recorded transactionally under deterministic PostgreSQL advisory locks.
10. Gmail configuration is required at startup. A runtime SMTP delivery failure does not roll back committed account or token data.

### Security model

Guest endpoints may create or recover only the account identified by validated proof tokens. Bearer endpoints may read or mutate only the user ID in the verified token and live session. Admin role does not bypass ownership on `/users/me`.

This is a nonproduction prototype. Deployment still requires TLS, encrypted database volumes, least privilege database credentials, protected Gmail and Cloudinary secrets, and redacted logs. Field level encryption for bank account numbers is a separate production hardening decision.

### Configuration required

| Variable | Purpose |
|---|---|
| `JWT_SECRET_KEY` | HS256 signing secret |
| `JWT_ISSUER` | Expected JWT issuer |
| `JWT_ACCESS_TOKEN_TTL_MINUTES` | Set to `15` |
| `AUTH_REFRESH_TOKEN_TTL_HOURS` | Set to `168` for 7 days |
| `AUTH_EMAIL_VERIFICATION_TTL_MINUTES` | Set to `10` |
| `AUTH_PASSWORD_RESET_TTL_MINUTES` | Set to `10` |
| `AUTH_EMAIL_VERIFICATION_URL` | Flutter deep link or HTTPS callback base |
| `AUTH_PASSWORD_RESET_URL` | Flutter deep link or HTTPS callback base |
| `SMTP_HOST` | `smtp.gmail.com` |
| `SMTP_PORT` | `587` |
| `SMTP_USERNAME` | Full Gmail address |
| `SMTP_APP_PASSWORD` | Google App Password, never the Gmail account password |
| `SMTP_FROM_NAME` | Human readable sender name |
| `CLOUDINARY_CLOUD_NAME` | Cloudinary cloud identifier |
| `CLOUDINARY_API_KEY` | Cloudinary API key |
| `CLOUDINARY_API_SECRET` | Cloudinary API secret |
| `AVATAR_UPLOAD_TIMEOUT_SECONDS` | Set to `15` |
| `AVATAR_PROCESSING_TIMEOUT_SECONDS` | Set to `10` |
| `AVATAR_MAX_CONCURRENT_CONVERSIONS` | Set to `2` by default and configurable for deployment capacity |
| `AUTH_CLEANUP_INTERVAL_HOURS` | Set to `24` |
| `AUTH_RECORD_RETENTION_DAYS` | Set to `30` |
| `MEDIA_CLEANUP_WORKER_INTERVAL_SECONDS` | Set to `60` |
| `MEDIA_CLEANUP_MAX_ATTEMPTS` | Set to `10` |

All variables are validated at startup and documented in `.env.example` and README. Gmail configuration is a hard startup dependency. Gmail requires two step verification and a 16 character App Password. Runtime SMTP availability remains a soft dependency for sign up, resend, and recovery requests.

### Critical test scenarios

1. Sign up through verification and sign in on one device, verifies **AC-1** through **AC-5**.
2. Five failed sign ins block the account for 15 minutes and a later success resets counters, verifies **AC-6**.
3. A second device sign in immediately invalidates protected requests and refresh from the first device, verifies **AC-5**, **AC-7**, and **AC-8**.
4. Two concurrent refreshes allow one rotation and cause replay revocation for the other, verifies **AC-8**.
5. Reset password revokes every session, while change password retains only the current session, verifies **AC-10** and **AC-11**.
6. Bank validation, avatar local conversion, HEIC Cloudinary fallback, storage timeout, replacement, and deletion verify **AC-12** through **AC-15**.
7. Generic account recovery responses, independent persisted email and IP limits, structured errors, redacted logs, auth cleanup, and durable media cleanup retries verify **AC-4**, **AC-15** through **AC-18**.
8. Recreate PostgreSQL on version 18, apply migration, and inspect every UUID default plus auth index and FK, verifies **AC-19**.

## Build plan

The project has no recorded delivery approach, so this plan uses end to end tracer slices.

1. Upgrade Docker PostgreSQL to 18 on a new named volume, update `000001_init_schema.up.sql` so every primary UUID defaults to `uuidv7()`, add the confirmed auth columns and tables, regenerate sqlc, apply the migration, and inspect the live schema, satisfies **AC-1**, **AC-5**, **AC-6**, **AC-8**, **AC-18**, **AC-19**.
2. Build the identity and session slice from [0001-identity-session.md](0001-identity-session.md), including shared structured errors and session aware middleware, satisfies **AC-1**, **AC-5** through **AC-9**, **AC-16**, **AC-17**.
3. Build the email and password slice from [0002-email-password.md](0002-email-password.md), including Gmail SMTP templates and configuration docs, satisfies **AC-2** through **AC-4**, **AC-10**, **AC-11**, **AC-16**, **AC-17**.
4. Build the profile and avatar slice from [0003-profile-avatar.md](0003-profile-avatar.md), including embedded bank data, backend WebP conversion, Cloudinary fallback, and the durable media cleanup queue, satisfies **AC-12** through **AC-15**, **AC-17**.
5. Add persisted rolling rate limits, the daily bounded auth cleanup, the bounded media cleanup worker, integration tests, OpenAPI contracts, `.env.example`, README, and module documentation, then remove the old routes and superseded auth code, satisfies **AC-15** through **AC-19**.

## Consequences

**Positive**:

1. Session revocation, one device enforcement, account status changes, and refresh reuse take effect immediately.
2. Each external provider stays behind an interface, so Gmail and Cloudinary can be replaced without changing usecases.
3. PostgreSQL constraints protect concurrency invariants that application checks alone cannot guarantee.

**Negative and tradeoffs**:

1. Every protected request adds a PostgreSQL lookup.
2. Strict refresh reuse detection can sign a user out when a refresh response is lost, so the mobile client must serialize refresh calls.
3. Gmail SMTP has personal account limits and weaker operational controls than a transactional email provider.
4. Backend image conversion increases CPU and memory use. HEIC usually falls back to Cloudinary.
5. PostgreSQL 18 requires a new local volume because an existing PostgreSQL 17 data directory cannot be mounted directly.

**Neutral**:

1. Phone login, phone verification, OAuth, MFA, account email change, production field encryption, and Prometheus export are outside v1.
2. The old PostgreSQL 17 volume remains available for rollback until the engineer chooses to remove it.

## Migration plan

**Strategy**: Direct replacement for an unpublished prototype, delivered in end to end slices.

**Phases**:

1. Create a new PostgreSQL 18 named volume and apply the revised initial migration without deleting the PostgreSQL 17 volume.
2. Verify schema defaults, constraints, indexes, and Goose version on PostgreSQL 18.
3. Deploy new auth endpoints and remove unpublished `/register` and `/login` routes when all auth integration tests pass.

**Rollback**: Stop the API, point Docker Compose and `DATABASE_URL` back to the preserved PostgreSQL 17 volume, and run the previous application revision. Data written to PostgreSQL 18 after cutover will not exist in the old volume.

**Risks**: Editing an already applied version 1 migration does not update an existing database. Development environments must use the new PostgreSQL 18 volume or recreate their database before validation.

## Follow-up

1. [ ] Add the project wide PostgreSQL conventions from `supabase-postgres-best-practices` to root `AGENTS.md`; the skill is installed but no project context file references it yet.
2. [ ] Optionally connect Google MCP Toolbox to a nonproduction PostgreSQL database for live schema verification.
3. [ ] Optionally connect the official Cloudinary MCP server for asset and environment inspection.
4. [ ] Design phone verification and phone sign in as a separate versioned feature.
5. [ ] Design production field encryption and a transactional email provider before production deployment.

## Rationale

Reasoning and options: see [rationale.md](rationale.md).
