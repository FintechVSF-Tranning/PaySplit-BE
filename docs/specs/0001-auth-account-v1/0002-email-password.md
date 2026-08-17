# Email and password recovery

## Summary

This slice activates accounts and recovers passwords through short lived single use email tokens. Gmail SMTP is an adapter, not a dependency of the auth usecase, and delivery failure never keeps a database transaction open.

## Requirements

This child implements **AC-2** through **AC-4**, **AC-10**, **AC-11**, **AC-16**, **AC-17**, and the email token part of **AC-18** from [index.md](index.md).

## Decision

Use `github.com/wneessen/go-mail` with Gmail SMTP, TLS on port 587, and a Google App Password. The usecase depends on a `Mailer` interface. Verification and password reset tokens share `user_tokens` but use separate token types. (basis: the personal Gmail sender requirement and Google App Password guidance)

Short rationale: Gmail matches the engineer's personal sender requirement. Resend cannot verify an address under `gmail.com`, while Gmail SMTP can send from that account with two step verification and an App Password. The adapter boundary preserves a later move to a transactional provider.

## Feature design

### Token lifecycle

Create 32 random bytes with `crypto/rand`, encode as URL safe base64, and persist only the SHA 256 hash. Verification and reset tokens expire after 10 minutes. `used_at` means a token completed its intended action. `superseded_at` means a newer token replaced it before use. Creating a new token locks the user row, marks every unused token of the same user and type as superseded, then inserts the replacement in the same transaction. A partial unique index on user and type where both timestamps are null prevents concurrent active tokens.

Email links append the raw token as the `token` query parameter to the configured callback base. Never log the full link.

### API details

#### `POST /api/v1/auth/verify-email`

Hash the supplied token and lock its row. A valid unused and unsuperseded token sets `email_verified_at`, changes pending status to active, and marks the token used in one transaction. Reopening that successfully consumed token returns `200` while its row remains in the 30 day retention window. A superseded, expired, unknown, or wrong type token returns `400 INVALID_OR_EXPIRED_TOKEN`. After cleanup removes a consumed token, reopening it also returns `400`. No auth tokens are issued.

#### `POST /api/v1/auth/resend-verification`

Accept email and always return the same `202` body. Only a pending account receives a new token. Invalidate earlier verification tokens before creating the new token. Limit normalized email and IP as independent dimensions to one request per rolling minute and 10 per rolling hour. Exceeding either limit rejects the request.

#### `POST /api/v1/auth/forgot-password`

Accept email and always return the same `202` body. Only an existing account receives a token. Invalidate earlier reset tokens before creating the new token. Use the same independent persisted limits as resend verification.

#### `POST /api/v1/auth/reset-password`

Accept token and new password. Lock token and user rows, validate the shared password policy, update the bcrypt hash, mark the token used, and revoke all sessions and refresh tokens in one transaction. Return `204`.

#### `PUT /api/v1/users/me/password`

Require a bearer token and live session. Accept current and new password. Require the current password to match, the new password to differ, and the shared policy to pass. Update the hash and revoke every session except the current `sid` in one transaction. Keep the current session and its existing refresh tokens. This accepts the explicit tradeoff that a refresh token already exposed from the current installation remains valid until rotation, revocation, or the absolute session expiry. Return `204`.

### Rate limit persistence

Store accepted request events in `auth_rate_limit_events`. Use `action`, `dimension`, and a SHA 256 hash of the normalized email or canonical IP as the key. Email and IP are separate keys, not a combined pair. Acquire transaction scoped advisory locks for all applicable keys in deterministic order, count events inside the rolling minute and hour, then insert the accepted event. Sign up records only the IP dimension. Daily auth cleanup removes events older than 24 hours in bounded batches.

### Delivery failure

Gmail settings are required and validated at process startup. Commit the user and token before SMTP. Send with a 5 second timeout. If configured Gmail is unavailable at request time or delivery fails, sign up returns `201` with `verification_email_sent: false`. Resend and forgot password retain their generic `202`. Log a redacted event and do not automatically retry in v1.

### Email templates

Provide plain text and HTML templates for verification and reset. Templates receive display name, safe callback URL, and 10 minute expiry text. HTML escapes all values.

## Configuration

Use the SMTP and callback variables in [index.md](index.md). `.env.example` and README must explain that Gmail requires two step verification and an App Password. The normal Gmail password must never be accepted as configuration documentation.

### Request and response contract

JSON bodies are limited to 64 KB and reject unknown fields. Email is at most 254 bytes after normalization. Verification and reset token inputs are at most 128 characters. Success bodies, safe user fields, and UTC RFC 3339 expiry formats are fixed in [index.md](index.md). Rate limited responses use `Retry-After` as delta seconds.

## Critical test scenarios

1. Sign up survives SMTP failure and resend later delivers a fresh token.
2. Verification activates once, remains idempotent, and rejects expired tokens.
3. Forgot password does not reveal whether an email exists.
4. Reset revokes all sessions and rejects token reuse.
5. Change password keeps the current session and rejects the old password afterward.
6. Email limits return `429` with `Retry-After` and do not reveal account existence.
